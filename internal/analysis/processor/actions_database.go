// processor/actions_database.go
// This file contains database and logging action implementations.

package processor

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/species"
	"github.com/tphakala/birdnet-go/internal/audiocore/audionorm"
	"github.com/tphakala/birdnet-go/internal/audiocore/convert"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/audiocore/flac"
	"github.com/tphakala/birdnet-go/internal/audiocore/resample"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// errAudioExportDeferred signals that SaveAudioAction cannot yet read the
// requested capture segment because the tail of the clip is still being
// written (Extended Capture). The job queue's retry mechanism re-runs the
// action after a backoff delay until the buffer has caught up.
var errAudioExportDeferred = errors.NewStd("audio export deferred until capture tail is available")

// Encoder tags recorded in the audio-export success log so the active encoder is
// visible per clip (native go-flac or native WAV writer, or FFmpeg for the lossy
// formats).
const (
	encoderFFmpeg     = "ffmpeg"
	encoderNativeWAV  = "native-wav"
	encoderNativeFLAC = "native-flac"
)

// Execute logs the note to the log file.
// Note: File logging errors are logged but not returned. This is intentional because:
// 1. File logging is a non-critical supplementary feature
// 2. Console output and database storage are the primary detection records
// 3. Failing the entire action for a log file issue would be overly disruptive
func (a *LogAction) Execute(_ context.Context, data any) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Check if the event should be handled for this species (supports scientific name lookup)
	if !a.EventTracker.TrackEventWithNames(a.Result.Species.CommonName, a.Result.Species.ScientificName, LogToFile) {
		return nil
	}

	// Log detection result to file using detection package.
	// Errors are logged but not returned - file logging is non-critical.
	if err := detection.LogToFile(a.Settings, &a.Result); err != nil {
		GetLogger().Error("Failed to log detection to file",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.Error(err),
			logger.String("species", a.Result.Species.CommonName),
			logger.Float64("confidence", a.Result.Confidence),
			logger.String("clip_name", a.Result.ClipName),
			logger.String("operation", "log_to_file"))
		// Note: Continue despite file error - detection is still processed via database/SSE
	} else {
		GetLogger().Info("Detection logged to file",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.String("species", a.Result.Species.CommonName),
			logger.Float64("confidence", a.Result.Confidence),
			logger.String("time", a.Result.Time()),
			logger.String("operation", "log_to_file_success"))
	}

	return nil
}

// Execute saves the note to the database.
// The context parameter allows for timeout/cancellation support.
func (a *DatabaseAction) Execute(ctx context.Context, data any) error {
	return a.ExecuteContext(ctx, data)
}

// ExecuteContext implements the ContextAction interface for proper context propagation.
// This allows CompositeAction to pass timeout and cancellation signals to the database save.
func (a *DatabaseAction) ExecuteContext(ctx context.Context, _ any) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Check event frequency (supports scientific name lookup)
	if !a.EventTracker.TrackEventWithNames(a.Result.Species.CommonName, a.Result.Species.ScientificName, DatabaseSave) {
		return nil
	}

	// Check if this is a new species and update atomically to prevent race conditions
	var isNewSpecies bool
	var daysSinceFirstSeen int
	var novelty species.NoveltyStatus
	if a.NewSpeciesTracker != nil {
		// Use atomic check-and-update to prevent duplicate "new species" notifications
		// when multiple detections of the same species arrive concurrently
		isNewSpecies, daysSinceFirstSeen, novelty = a.NewSpeciesTracker.CheckAndUpdateSpeciesWithNovelty(a.Result.Species.ScientificName, a.Result.BeginTime)
	}

	// Save detection to database using preferred path
	if a.Repo != nil {
		// New path: Use DetectionRepository (handles conversion internally)
		if err := a.Repo.Save(ctx, &a.Result, a.Results); err != nil {
			GetLogger().Error("Failed to save detection to database",
				logger.String("component", "analysis.processor.actions"),
				logger.String("detection_id", a.CorrelationID),
				logger.Error(err),
				logger.String("species", a.Result.Species.CommonName),
				logger.String("scientific_name", a.Result.Species.ScientificName),
				logger.Float64("confidence", a.Result.Confidence),
				logger.String("clip_name", a.Result.ClipName),
				logger.String("operation", "database_save_repository"))
			return err
		}
		// Repo.Save() updates a.Result.ID internally via result.ID = note.ID.
		// Defensive check: warn if ID is unexpectedly 0 after a successful save.
		// This aids diagnosis of GitHub #2453 (MQTT detectionId always 0).
		if a.Result.ID == 0 {
			GetLogger().Warn("Detection ID is 0 after successful Repo.Save(), downstream actions will not have a valid ID",
				logger.String("component", "analysis.processor.actions"),
				logger.String("detection_id", a.CorrelationID),
				logger.String("species", a.Result.Species.CommonName),
				logger.String("scientific_name", a.Result.Species.ScientificName),
				logger.String("operation", "database_save_id_check"))
		}
	} else {
		// Legacy path: Use datastore.Interface directly
		// Convert Result to Note for GORM persistence
		note := datastore.NoteFromResult(&a.Result)

		// Convert domain AdditionalResults to legacy datastore.Results format for GORM.
		// Results are passed separately to Save() - not assigned to note.Results because
		// saveNoteInTransaction uses Omit("Results") to prevent GORM auto-save.
		legacyResults := datastore.AdditionalResultsToDatastoreResults(a.Results)

		// Save note to database
		if err := a.Ds.Save(&note, legacyResults); err != nil {
			GetLogger().Error("Failed to save note and results to database",
				logger.String("component", "analysis.processor.actions"),
				logger.String("detection_id", a.CorrelationID),
				logger.Error(err),
				logger.String("species", a.Result.Species.CommonName),
				logger.String("scientific_name", a.Result.Species.ScientificName),
				logger.Float64("confidence", a.Result.Confidence),
				logger.String("clip_name", a.Result.ClipName),
				logger.String("operation", "database_save"))
			return err
		}

		// Sync database-assigned ID back to Result
		a.Result.ID = note.ID
	}

	// Share the database ID with downstream actions (MQTT, SSE) immediately.
	if a.DetectionCtx != nil {
		a.DetectionCtx.NoteID.Store(uint64(a.Result.ID))
	}

	// Add an explanatory comment when the ultrasonic validation filter tagged this detection as unlikely.
	if a.Result.Unlikely && a.Result.ID != 0 && a.Repo != nil {
		locale := a.Settings.Realtime.Dashboard.Locale
		comment := formatUnlikelyComment(locale, a.Result.UltrasonicCV, a.Result.UltrasonicCVThreshold)
		noteID := strconv.FormatUint(uint64(a.Result.ID), 10)
		if err := a.Repo.AddComment(ctx, noteID, comment); err != nil {
			GetLogger().Warn("failed to add unlikely comment to detection",
				logger.String("detection_id", a.CorrelationID),
				logger.Uint64("note_id", uint64(a.Result.ID)),
				logger.String("species", a.Result.Species.CommonName),
				logger.Error(err),
				logger.String("operation", "unlikely_comment"))
		}
	}

	// After successful save, publish detection event to the event bus.
	a.publishDetectionEvent(isNewSpecies, daysSinceFirstSeen, novelty)

	// NOTE: Audio export is intentionally NOT performed here.
	// It runs as a separate action (SaveAudioAction) outside the CompositeAction
	// that contains Database -> SSE -> MQTT. This prevents slow audio encoding
	// (e.g., FFmpeg on Raspberry Pi) from blocking SSE/MQTT broadcasts and
	// causing CompositeAction 30s timeouts (Sentry BIRDNET-GO-WD).
	//
	// The media API already handles the race where SSE broadcasts a ClipName
	// before the audio file is on disk, using waitForAudioFile() with retries
	// and 503 + Retry-After responses.

	return nil
}

// shouldSuppressNewSpeciesNotification checks if a new species notification should be suppressed.
// Returns true if notification should be suppressed, along with the notification time.
func (a *DatabaseAction) shouldSuppressNewSpeciesNotification() (suppress bool, notificationTime time.Time) {
	if a.NewSpeciesTracker == nil {
		return false, time.Time{}
	}

	notificationTime = a.Result.BeginTime
	if a.NewSpeciesTracker.ShouldSuppressNotification(a.Result.Species.ScientificName, notificationTime) {
		if a.Settings.Debug {
			GetLogger().Debug("Suppressing duplicate new species notification",
				logger.String("component", "analysis.processor.actions"),
				logger.String("detection_id", a.CorrelationID),
				logger.String("species", a.Result.Species.CommonName),
				logger.String("scientific_name", a.Result.Species.ScientificName),
				logger.String("operation", "suppress_notification"))
		}
		return true, notificationTime
	}
	return false, notificationTime
}

// createDetectionEvent creates a new species detection event with metadata.
// Returns nil if event creation fails.
func (a *DatabaseAction) createDetectionEvent(isNewSpecies bool, daysSinceFirstSeen int) events.DetectionEvent {
	displayLocation := a.Result.AudioSource.DisplayName

	detectionEvent, err := events.NewDetectionEvent(
		a.Result.Species.CommonName,
		a.Result.Species.ScientificName,
		a.Result.Confidence,
		displayLocation,
		isNewSpecies,
		daysSinceFirstSeen,
	)
	if err != nil {
		if a.Settings.Debug {
			GetLogger().Debug("Failed to create detection event",
				logger.String("component", "analysis.processor.actions"),
				logger.String("detection_id", a.CorrelationID),
				logger.Error(err),
				logger.String("species", a.Result.Species.CommonName),
				logger.String("scientific_name", a.Result.Species.ScientificName),
				logger.Bool("is_new_species", isNewSpecies),
				logger.Int("days_since_first_seen", daysSinceFirstSeen),
				logger.String("operation", "create_detection_event"))
		}
		return nil
	}
	return detectionEvent
}

// populateEventMetadata adds location, time, note ID, and image URL to event metadata.
func (a *DatabaseAction) populateEventMetadata(detectionEvent events.DetectionEvent, novelty species.NoveltyStatus) {
	metadata := detectionEvent.GetMetadata()
	if metadata == nil {
		GetLogger().Error("Detection event metadata is nil",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.String("species", a.Result.Species.CommonName),
			logger.String("scientific_name", a.Result.Species.ScientificName),
			logger.String("operation", "publish_detection_event"))
		return
	}

	metadata["note_id"] = a.Result.ID
	metadata["latitude"] = a.Result.Latitude
	metadata["longitude"] = a.Result.Longitude
	metadata["begin_time"] = a.Result.BeginTime
	if hasNoveltyStatus(novelty) {
		if novelty.DaysSinceLastSeen >= 0 {
			metadata[events.DetectionMetadataDaysSinceLastSeen] = novelty.DaysSinceLastSeen
		}
		if novelty.NoveltyEpisodeActive {
			metadata[events.DetectionMetadataNoveltyEpisodeDays] = novelty.NoveltyEpisodeDays
			if !novelty.NoveltyEpisodeStart.IsZero() {
				metadata[events.DetectionMetadataNoveltyEpisodeStart] = novelty.NoveltyEpisodeStart.Format(time.RFC3339)
			}
		}
	}

	if a.processor != nil && a.processor.BirdImageCache != nil {
		if birdImage, err := a.processor.BirdImageCache.Get(a.Result.Species.ScientificName); err == nil && birdImage.URL != "" {
			metadata["image_url"] = birdImage.URL
		}
	}
}

func hasNoveltyStatus(novelty species.NoveltyStatus) bool {
	// Inactive same-day detections intentionally omit novelty metadata; active
	// episodes still publish days_since_last_seen=0 for same-day episode repeats.
	return novelty.NoveltyEpisodeActive || novelty.DaysSinceLastSeen > 0
}

// recordNotificationSent records that a notification was sent and logs debug info.
func (a *DatabaseAction) recordNotificationSent(notificationTime time.Time) {
	if a.NewSpeciesTracker != nil && !notificationTime.IsZero() {
		a.NewSpeciesTracker.RecordNotificationSent(a.Result.Species.ScientificName, notificationTime)
	}

	if a.Settings.Debug {
		GetLogger().Debug("Published new species detection event",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.String("species", a.Result.Species.CommonName),
			logger.String("scientific_name", a.Result.Species.ScientificName),
			logger.Float64("confidence", a.Result.Confidence),
			logger.Bool("is_new_species", true),
			logger.String("operation", "publish_detection_event"))
	}
}

// publishDetectionEvent publishes a detection event to the event bus.
// All detections are published so that alert rules on detection.occurred can fire.
// New species detections additionally go through suppression and notification recording.
func (a *DatabaseAction) publishDetectionEvent(isNewSpecies bool, daysSinceFirstSeen int, novelty species.NoveltyStatus) {
	if !events.IsInitialized() {
		return
	}

	eventBus := events.GetEventBus()
	if eventBus == nil {
		return
	}

	if isNewSpecies {
		suppress, notificationTime := a.shouldSuppressNewSpeciesNotification()
		if !suppress {
			detectionEvent := a.createDetectionEvent(true, daysSinceFirstSeen)
			if detectionEvent != nil {
				a.populateEventMetadata(detectionEvent, novelty)
				if published := eventBus.TryPublishDetection(detectionEvent); published {
					a.recordNotificationSent(notificationTime)
				}
			}
			return
		}
		// Suppressed new species still gets published as an ordinary detection
		// so that detection.occurred alert rules fire for every detection.
	}

	detectionEvent := a.createDetectionEvent(false, daysSinceFirstSeen)
	if detectionEvent == nil {
		return
	}

	a.populateEventMetadata(detectionEvent, novelty)
	eventBus.TryPublishDetection(detectionEvent)
}

// Execute saves the audio clip to a file
func (a *SaveAudioAction) Execute(ctx context.Context, _ any) error {
	// Hot-reload guard: skip export if audio export was disabled at runtime.
	// This mirrors the pattern used by MqttAction and BirdWeatherAction.
	if !a.Settings.Realtime.Audio.Export.Enabled {
		GetLogger().Debug("Skipping audio export: disabled at runtime",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.String("clip_name", a.ClipName),
			logger.String("operation", "audio_export_disabled"))
		return nil
	}

	// Hot-reload race guard: a detection created while export was disabled carries
	// an empty ClipName. If export was enabled between createDetection and this
	// action running, there is no valid output path to write to
	// (filepath.Join(Export.Path, "") collapses to the export directory itself),
	// so skip the export rather than encode a clip onto the directory path.
	if a.ClipName == "" {
		GetLogger().Debug("Skipping audio export: empty clip name",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.String("operation", "audio_export_empty_clipname"))
		return nil
	}

	// Deferred-read path: Extended Capture may schedule an export whose tail
	// still has not been written to the ring buffer. buildSaveAudioAction
	// populates bufferMgr/sourceID/beginTime/duration/readyAt and leaves
	// pcmData empty in that case. If readyAt is still in the future, return
	// errAudioExportDeferred so the job queue retries with backoff; once the
	// window has fully written, read the segment and fall through to encode.
	if len(a.pcmData) == 0 && a.bufferMgr != nil && a.duration > 0 {
		if time.Until(a.readyAt) > 0 {
			return errAudioExportDeferred
		}

		cb, err := a.bufferMgr.CaptureBuffer(a.sourceID)
		if err != nil {
			return err
		}

		endTime := a.beginTime.Add(time.Duration(a.duration) * time.Second)
		pcmData, err := cb.ReadSegment(a.beginTime, endTime)
		if err != nil {
			// ErrInsufficientData and any other read error unwind to the
			// job-queue retry layer via the SaveAudioAction case in
			// getJobQueueRetryConfig (workers.go). No special per-error
			// handling is needed here.
			return err
		}
		a.pcmData = pcmData
	}

	// If PCM data was not captured (e.g., buffer read failed), skip export.
	if len(a.pcmData) == 0 {
		GetLogger().Warn("Skipping audio export: no PCM data available",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.String("clip_name", a.ClipName),
			logger.String("operation", "audio_export_skip"))
		return nil
	}

	// Resolve NoteID from DetectionContext (set by DatabaseAction).
	if a.DetectionCtx != nil {
		const noteIDWaitTimeout = 5 * time.Second
		const noteIDPollInterval = 50 * time.Millisecond
		deadline := time.Now().Add(noteIDWaitTimeout)
		for time.Now().Before(deadline) {
			if liveID := uint(a.DetectionCtx.NoteID.Load()); liveID > 0 {
				a.NoteID = liveID
				break
			}
			select {
			case <-ctx.Done():
			case <-time.After(noteIDPollInterval):
			}
			if ctx.Err() != nil {
				break
			}
		}
	}

	// Get the full path by joining the export path with the relative clip name
	outputPath := filepath.Join(a.Settings.Realtime.Audio.Export.Path, a.ClipName)

	// Ensure the directory exists
	// Note: Errors are logged by the caller (handleAudioExportError) with full context
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o750); err != nil {
		return err
	}

	exportRate, exportFormat, outputPath := a.resolveExportParams(outputPath)

	encoderUsed, err := a.encodeClip(ctx, exportRate, exportFormat, outputPath)
	if err != nil {
		return err
	}

	// Get file size for logging
	fileInfo, err := os.Stat(outputPath)
	var fileSize int64
	if err == nil {
		fileSize = fileInfo.Size()
	} else {
		// Debug log if we can't stat the file (shouldn't happen after successful write)
		GetLogger().Debug("Failed to stat audio file for size logging",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.Error(err),
			logger.String("path", outputPath),
			logger.String("operation", "audio_export_stat"))
	}

	// Log successful audio export at INFO level (BG-18)
	// This provides evidence that audio export completed successfully
	GetLogger().Info("Audio clip saved successfully",
		logger.String("component", "analysis.processor.actions"),
		logger.String("detection_id", a.CorrelationID),
		logger.String("clip_path", filepath.Base(outputPath)),
		logger.Int64("file_size_bytes", fileSize),
		logger.String("format", exportFormat),
		logger.String("encoder", encoderUsed),
		logger.Int("sample_rate", exportRate),
		logger.String("operation", "audio_export_success"))

	// Signal that the clip file exists on disk. This is used by any late
	// consumers that check whether the audio was actually exported.
	if a.DetectionCtx != nil {
		a.DetectionCtx.ClipSaved.Store(true)
	}

	// Submit for pre-rendering if enabled
	if a.Settings.Realtime.Dashboard.Spectrogram.Enabled && a.PreRenderer != nil {
		// Create pre-render job using local DTO (avoids direct spectrogram dependency)
		job := PreRenderJob{
			PCMData:    a.pcmData,
			SampleRate: exportRate,
			ClipPath:   outputPath, // Use full path to audio file
			NoteID:     a.NoteID,
			Timestamp:  time.Now(),
			ModelType:  string(detection.ResolveModelType(a.modelName, "")),
		}

		// Non-blocking submission - errors logged but don't fail action
		if err := a.PreRenderer.Submit(&job); err != nil {
			GetLogger().Warn("Failed to submit spectrogram pre-render job",
				logger.String("component", "analysis.processor.actions"),
				logger.String("detection_id", a.CorrelationID),
				logger.Any("note_id", a.NoteID),
				logger.String("clip_path", outputPath),
				logger.Error(err),
				logger.String("operation", "prerender_submit"))
		}
	}

	return nil
}

// encodeClip writes the captured PCM to outputPath in the resolved format,
// dispatching to the native WAV writer, the native go-flac encoder, or the FFmpeg
// exporter (non-FLAC formats only). It returns the encoder tag for the success log.
//
// FLAC is always encoded natively (go-flac); FFmpeg is never used for FLAC. Gain is
// applied in Go. When normalization is enabled, EBU R128 loudness is measured and
// applied via audionorm, with no FFmpeg loudnorm dependency.
func (a *SaveAudioAction) encodeClip(ctx context.Context, exportRate int, exportFormat, outputPath string) (string, error) {
	exportSettings := &a.Settings.Realtime.Audio.Export

	switch exportFormat {
	case ffmpeg.FormatWAV:
		if err := convert.SavePCMDataToWAV(outputPath, a.pcmData, exportRate, conf.BitDepth); err != nil {
			return "", err
		}
		return encoderNativeWAV, nil

	case ffmpeg.FormatFLAC:
		// FLAC always encodes natively via go-flac; FFmpeg is never used for FLAC.
		gainDB := exportSettings.Gain
		if exportSettings.Normalization.Enabled {
			switch {
			case flac.SupportedBitDepth(conf.BitDepth) &&
				audionormSupportsTargets(exportSettings.Normalization.TargetLUFS, exportSettings.Normalization.TruePeak):
				// Native FLAC + EBU R128 normalization via audionorm, no FFmpeg. The
				// static Export.Gain is intentionally NOT applied on top: normalization
				// takes precedence over gain (mirroring the old FFmpeg loudnorm path),
				// so only the loudness gain is used. audionorm normalizes to the target
				// integrated loudness under the true-peak ceiling; LoudnessRange is not
				// consumed (no LRA/dynamic compression on this path).
				var err error
				gainDB, err = a.planNativeNormalizationGain(ctx, exportRate,
					exportSettings.Normalization.TargetLUFS, exportSettings.Normalization.TruePeak)
				if err != nil {
					return "", err
				}
			default:
				// Unreachable for a validated config: capture is 16-bit (conf.BitDepth)
				// and settings validation clamps the loudness targets into audionorm's
				// range. Defense-in-depth for unvalidated/legacy settings only. With
				// FFmpeg FLAC removed there is no loudnorm fallback, so encode natively
				// with the static gain and surface the skipped normalization at WARN.
				reason := "normalization_targets_out_of_native_range"
				if !flac.SupportedBitDepth(conf.BitDepth) {
					reason = "unsupported_bit_depth"
				}
				GetLogger().Warn("Native FLAC normalization skipped; encoding without normalization",
					logger.String("component", "analysis.processor.actions"),
					logger.String("detection_id", a.CorrelationID),
					logger.String("reason", reason),
					logger.String("operation", "audio_export_flac_normalize_skip"))
			}
		}
		if err := flac.EncodePCM(ctx, &flac.Options{
			PCMData:    a.pcmData,
			OutputPath: outputPath,
			SampleRate: exportRate,
			Channels:   conf.NumChannels,
			BitDepth:   conf.BitDepth,
			GainDB:     gainDB,
		}); err != nil {
			return "", err
		}
		return encoderNativeFLAC, nil

	default:
		// FFmpeg for the remaining formats (MP3, AAC, Opus), including their
		// EBU R128 loudnorm normalization. FLAC and WAV never reach this branch.
		opts := &ffmpeg.ExportOptions{
			PCMData:    a.pcmData,
			OutputPath: outputPath,
			Format:     exportFormat,
			Bitrate:    exportSettings.Bitrate,
			SampleRate: exportRate,
			Channels:   conf.NumChannels,
			BitDepth:   conf.BitDepth,
			FFmpegPath: a.Settings.Realtime.Audio.FfmpegPath,
			GainDB:     exportSettings.Gain,
			Normalization: ffmpeg.ExportNormalization{
				Enabled:       exportSettings.Normalization.Enabled,
				TargetLUFS:    exportSettings.Normalization.TargetLUFS,
				TruePeak:      exportSettings.Normalization.TruePeak,
				LoudnessRange: exportSettings.Normalization.LoudnessRange,
			},
		}
		if err := ffmpeg.ExportAudio(ctx, opts); err != nil {
			return "", err
		}
		return encoderFFmpeg, nil
	}
}

// audionormMinTargetLUFS is the exclusive lower bound of audionorm's valid target
// loudness range (audionorm rejects targets <= -70 LUFS, the absolute gate).
const audionormMinTargetLUFS = -70.0

// audionormSupportsTargets reports whether the configured integrated-loudness
// target and true-peak ceiling fall within audionorm's valid range
// (-70 < TargetLUFS < 0, ceiling <= 0). Out-of-range configs (unreachable for a
// validated config, whose targets are clamped) are encoded natively WITHOUT
// normalization rather than fed values audionorm would mis-handle; FFmpeg is no
// longer used for FLAC.
func audionormSupportsTargets(targetLUFS, truePeakDBTP float64) bool {
	return targetLUFS < 0 && targetLUFS > audionormMinTargetLUFS && truePeakDBTP <= 0
}

// planNativeNormalizationGain measures the clip's EBU R128 integrated loudness and
// true peak with audionorm and returns the single linear gain (dB) that brings it
// to targetLUFS without its true peak exceeding truePeakDBTP, clamped to
// +/-audionorm.DefaultMaxGainDB. Silent or sub-400 ms input yields 0 (the clip is
// left unchanged rather than boosted into noise), matching the BirdWeather native
// path. The gain is applied by flac.EncodePCM during encoding; a.pcmData is not
// modified here.
func (a *SaveAudioAction) planNativeNormalizationGain(ctx context.Context, sampleRate int, targetLUFS, truePeakDBTP float64) (float64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	// audionorm decodes the PCM bytes inline; a.pcmData is not mutated. Silence
	// yields GainDB == 0 (audionorm returns -Inf LUFS); the clamp is the secondary
	// guard for low-peak clips and for attenuation, applied silently on this path
	// (the BirdWeather path logs its own limiting, hence the discarded flag).
	gainDB, meas, res, _, err := audionorm.PlanClampedGainInt16Bytes(a.pcmData, audionorm.Options{
		SampleRate:   sampleRate,
		Channels:     conf.NumChannels,
		TargetLUFS:   targetLUFS,
		TruePeakDBTP: truePeakDBTP,
	}, audionorm.DefaultMaxGainDB)
	if err != nil {
		return 0, errors.New(err).
			Component("analysis.processor").
			Category(errors.CategoryAudio).
			Context("operation", "native_flac_normalize_measure").
			Context("detection_id", a.CorrelationID).
			Build()
	}

	GetLogger().Debug("Native FLAC loudness analysis (detection save)",
		logger.Float64("measured_lufs", meas.IntegratedLUFS),
		logger.Float64("true_peak_dbtp", meas.TruePeakDBTP),
		logger.Float64("target_lufs", targetLUFS),
		logger.Float64("gain_db", gainDB),
		logger.Bool("peak_limited", res.PeakLimited),
		logger.String("detection_id", a.CorrelationID))
	return gainDB, nil
}

// resolveExportParams determines the export sample rate, format, and output
// path. Bird audio at rates above 48kHz is downsampled. Bat audio keeps the
// native rate; if the configured format cannot carry it, the format is
// silently switched to WAV.
func (a *SaveAudioAction) resolveExportParams(outputPath string) (rate int, format, path string) {
	rate = a.sourceSampleRate
	if rate <= 0 {
		rate = conf.SampleRate
	}

	format = a.Settings.Realtime.Audio.Export.Type
	path = outputPath

	isBat := detection.ResolveModelType(a.modelName, "") == entities.ModelTypeBat

	if needsBatFormatFallback(a.modelName, "", rate, format) {
		format = "wav"
		path = replaceExtension(path, ".wav")
	} else if rate > conf.SampleRate && !isBat {
		resampled, err := resample.ResampleBytes(a.pcmData, rate, conf.SampleRate)
		if err != nil {
			GetLogger().Warn("Resampling failed, exporting at source rate",
				logger.String("component", "analysis.processor.actions"),
				logger.String("detection_id", a.CorrelationID),
				logger.Int("source_rate", rate),
				logger.Int("target_rate", conf.SampleRate),
				logger.Error(err),
				logger.String("operation", "audio_export_resample"))
		} else {
			a.pcmData = resampled
			a.sourceSampleRate = conf.SampleRate
			rate = conf.SampleRate
		}
	}

	return rate, format, path
}

// replaceExtension swaps the file extension on path (e.g. ".mp3" -> ".wav").
func replaceExtension(path, newExt string) string {
	ext := filepath.Ext(path)
	if ext == "" {
		return path + newExt
	}
	return path[:len(path)-len(ext)] + newExt
}

// needsBatFormatFallback returns true when the model is a bat classifier
// and the actual source sample rate exceeds what the configured export
// format can carry (MP3/Opus/AAC cap at 48kHz).
func needsBatFormatFallback(modelName, modelVersion string, sourceRate int, exportFormat string) bool {
	if detection.ResolveModelType(modelName, modelVersion) != entities.ModelTypeBat {
		return false
	}
	if sourceRate <= conf.SampleRate {
		return false
	}
	switch exportFormat {
	case "mp3", "opus", "aac":
		return true
	}
	return false
}
