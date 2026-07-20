// processor/actions_database.go
// This file contains database and logging action implementations.

package processor

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/analysis/species"
	"github.com/tphakala/birdnet-go/internal/audiocore/aac"
	"github.com/tphakala/birdnet-go/internal/audiocore/audionorm"
	"github.com/tphakala/birdnet-go/internal/audiocore/convert"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/audiocore/flac"
	"github.com/tphakala/birdnet-go/internal/audiocore/opus"
	"github.com/tphakala/birdnet-go/internal/audiocore/pcmgain"
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
// action after a backoff delay until the buffer has caught up. It wraps
// jobqueue.ErrJobDeferred so LogJobRetryScheduled records the reschedule at Debug
// rather than Warn (this is expected backpressure, not a failure).
var errAudioExportDeferred = fmt.Errorf("audio export deferred until capture tail is available: %w", jobqueue.ErrJobDeferred)

// Encoder tags recorded in the audio-export logs so the active encoder is
// visible per clip (native go-flac or native WAV writer, native go-aac/go-opus
// where the gate opted the format in, or FFmpeg).
const (
	encoderFFmpeg     = "ffmpeg"
	encoderNativeWAV  = "native-wav"
	encoderNativeFLAC = "native-flac"
	encoderNativeAAC  = "native-aac"
	encoderNativeOpus = "native-opus"
)

// clipEncoding records how a clip was written: which encoder owned the file and
// the parameters a support log needs to explain the result on disk.
//
// It is resolved BEFORE the encoder runs, which is the point of the type: a
// failed export can then name the encoder that failed. Otherwise an operator who
// opted a format into its native encoder has no way to tell from the logs
// whether go-aac or FFmpeg produced the failure, which is the one question the
// gated rollout exists to answer.
type clipEncoding struct {
	Encoder     string  // encoderNativeAAC, encoderFFmpeg, ...
	GainDB      float64 // loudness gain the encoder applies to the clip
	BitrateKbps int     // encoded bitrate for the lossy formats; 0 when lossless
	// GainResolved distinguishes "the gain is 0 dB" from "the export failed
	// before the gain was known". Without it a failure during gain resolution
	// logs gain_db=0, which is also the default Export.Gain, so the field reads
	// as a measurement when it is really an absence.
	GainResolved bool
	// MeasureMs and EncodeMs are timed separately because they answer different
	// questions and have wildly different costs. Resolving the gain with
	// normalization enabled is a full-clip EBU R128 pass, which on a Pi with a
	// 20-minute extended capture dominates the export; folding it into a single
	// encode_ms blames the codec for normalization cost and makes both "exports
	// are slow" and native-vs-FFmpeg comparisons read wrong.
	MeasureMs int64 // wall time resolving the loudness gain (0 when normalization is off)
	EncodeMs  int64 // wall time inside the encoder itself
}

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

	// Ensure the directory exists. The error unwinds to the job queue, which
	// logs it; there is no export context to add here that it does not carry.
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o750); err != nil {
		return err
	}

	exportRate, exportFormat, outputPath := a.resolveExportParams(outputPath)

	enc, err := a.encodeClip(ctx, exportRate, exportFormat, outputPath)
	if err != nil {
		a.logExportFailure(&enc, exportFormat, exportRate, outputPath, err)
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
			logger.String("clip_path", a.relativeClipPath(outputPath)),
			logger.String("operation", "audio_export_stat"))
	}

	// Log successful audio export at INFO level (BG-18).
	//
	// This is the primary support artefact for the clip export path, so it names
	// the encoder that produced the file and the parameters that explain it. Two
	// of the recurring reports are answered straight from this line: "my clips are
	// too quiet" (gain_db, against the configured normalization) and "my clips are
	// truncated" (duration_ms next to file_size_bytes, which on its own cannot
	// distinguish a short capture from a bad encode).
	//
	// The species field is not decoration: GET /api/v2/system/events/detections
	// attributes recorded clip paths to a species by reading species and clip_path
	// back off this line (the audio_export_success case in
	// internal/api/v2/system/events_aggregation.go). It consumes only this line, so
	// dropping the field empties every ClipPaths the endpoint returns.
	// 14 = the 12 unconditional fields below, plus bitrate_kbps on the lossy
	// formats, plus operation.
	fields := make([]logger.Field, 0, 14)
	fields = append(fields,
		logger.String("component", "analysis.processor.actions"),
		logger.String("detection_id", a.CorrelationID),
		logger.String("species", a.species),
		logger.String("clip_path", a.relativeClipPath(outputPath)),
		logger.Int64("file_size_bytes", fileSize),
		logger.String("format", exportFormat),
		logger.String("encoder", enc.Encoder),
		logger.Int("sample_rate", exportRate),
		logger.Float64("gain_db", enc.GainDB),
		logger.Int64("duration_ms", clipDurationMs(len(a.pcmData), exportRate)),
		logger.Int64("measure_ms", enc.MeasureMs),
		logger.Int64("encode_ms", enc.EncodeMs),
	)
	if enc.BitrateKbps > 0 {
		fields = append(fields, logger.Int("bitrate_kbps", enc.BitrateKbps))
	}
	fields = append(fields, logger.String("operation", "audio_export_success"))
	GetLogger().Info("Audio clip saved successfully", fields...)

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
				logger.String("clip_path", a.relativeClipPath(outputPath)),
				logger.Error(err),
				logger.String("operation", "prerender_submit"))
		}
	}

	return nil
}

// relativeClipPath is the clip's name relative to the export directory, which is
// how the rest of the system identifies a clip: it is normally the value stored
// on the detection, and the shape the media endpoint resolves
// (/api/v2/media/audio/{name}). "Normally" because resolveExportParams can
// rewrite the extension to .wav; the bat downgrade mirrors that into the stored
// ClipName, but the stranded-without-encoder fallback does not, so on that path
// the logged name ends .wav while the database row still names the configured
// format.
//
// The export directory is deliberately stripped. Support logs and uploaded
// support dumps are read by someone other than the operator, and the absolute
// path leaks the account name and layout of their machine. The year/month
// segments leak nothing and are load-bearing: GET /api/v2/system/events/detections
// reports these values as SpeciesEntry.ClipPaths, and a bare basename cannot be
// resolved back to a file. Recomputed from outputPath rather than reusing
// a.ClipName because resolveExportParams may have corrected the extension.
func (a *SaveAudioAction) relativeClipPath(outputPath string) string {
	rel, err := filepath.Rel(a.Settings.Realtime.Audio.Export.Path, outputPath)
	// Rel reports an error only when it cannot express the relationship at all
	// (one path absolute and the other relative, different Windows volumes). It
	// happily walks UP with ".." when both are absolute but outputPath sits
	// outside the export directory, and that result would defeat the whole point
	// of the function: "../../../etc/passwd" leaks the layout above the export
	// directory into a support dump and is not a clip name any consumer can
	// resolve. IsLocal rejects exactly that, plus absolute results and Windows
	// reserved names. Either way the basename is the safe answer: it still names
	// the clip and still contains no directory.
	if err != nil || !filepath.IsLocal(rel) {
		return filepath.Base(outputPath)
	}
	return filepath.ToSlash(rel)
}

// clipDurationMs is the wall duration of the captured PCM at the export rate.
// On the success line it is logged next to the encoded file size, because size
// alone cannot tell a short capture apart from a truncated encode and both get
// reported the same way ("my clips are cut off"). The failure line has no file
// size to pair it with; there it says how much audio the export was carrying.
func clipDurationMs(pcmBytes, sampleRate int) int64 {
	bytesPerFrame := (conf.BitDepth / 8) * conf.NumChannels
	if sampleRate <= 0 || bytesPerFrame <= 0 {
		return 0
	}
	const msPerSecond = 1000
	return int64(pcmBytes/bytesPerFrame) * msPerSecond / int64(sampleRate)
}

// logExportFailure records a failed clip export against the encoder that failed.
//
// Without it the only trace of a failed export is the job queue's own line
// ("Job failed permanently" once the attempts are exhausted, "Job scheduled for
// retry after failure" before that), which carries the action description and
// the error and names neither the encoder nor the format. An operator who opted
// a format into its native encoder could not tell from the logs whether go-aac
// or FFmpeg produced the failure, which is exactly what the gated rollout needs
// to know.
//
// A CANCELLED export is shutdown rather than a defect, so it is recorded at
// Debug: exports in flight when the process stops must not look like errors.
//
// The classification reads the ERROR, not the context state at the moment of
// logging. Asking the context whether it was cancelled answers a different
// question: it says the process is shutting down, not that shutdown is why this
// export failed. ENOSPC, corrupt PCM or a missing FFmpeg binary hit while a
// shutdown happens to be in progress all satisfy a ctx-based test and would be
// filed at Debug under a message asserting they were cancelled, which on a
// container that OOM-restarts misattributes every in-flight failure in the
// restart window.
//
// Every path that CONTEXT cancellation can interrupt reports it in the error, so
// nothing about a normal shutdown is lost: FFmpeg wraps ctx.Err() at each of its
// exits, every native encoder checks the context before it writes anything (FLAC
// additionally re-checks per chunk while applying gain, AAC and Opus via
// audiotemp.WriteFile), and resolveExportGainDB joins it onto a measurement that
// cancellation turned terminal. The stretches that take no context at all (the
// WAV writer, and the one-shot encode calls once audiotemp has let them start)
// cannot be interrupted by cancellation either, so any error they return is a
// genuine defect and belongs at WARN.
//
// Not covered, deliberately: a signal delivered to the whole process group can
// kill the FFmpeg child directly, and cmd.Wait() then reports "signal:
// terminated" while ctx.Err() is still nil. That lands at WARN. It is a narrow
// native-install case (the container image runs tini as PID 1 without -g, so it
// forwards to its direct child rather than the group), and the alternative is
// the misclassification above.
//
// A context that EXPIRED is not shutdown either. The job queue wraps every
// execution in context.WithTimeout, so an encode slow enough to blow
// it (a hung FFmpeg, a long extended capture on a Pi) is precisely the failure
// this line exists to surface, and it stays at WARN because DeadlineExceeded is
// not Canceled. The wrap happens in the queue's executeJobWithTimeout, which
// gives every execution DefaultJobExecutionTimeout.
//
// Everything else is WARN, not ERROR, even though it is a genuine failure. The
// error is returned unchanged, and the job queue logs that same failure as ERROR
// once the attempts are exhausted (handleJobFailure only calls LogJobFailed when
// Attempts >= MaxAttempts). Raising this line to ERROR too would report one root
// cause as two, inflating error counts and alerting twice. This line exists to
// carry the context the queue's generic line cannot, not to raise the alarm.
// Retries exist only on the deferred path (an Extended Capture tail is the only
// SaveAudioAction getJobQueueRetryConfig enables them for); the ordinary
// eager-read action gets RetryConfig{Enabled: false}, so the queue logs it once,
// permanently failed. On the deferred path most intermediate attempts are the
// action rescheduling itself while it waits for the tail, and those carry
// errAudioExportDeferred, which LogJobRetryScheduled deliberately drops to Debug.
// A genuine encode failure there is logged by the queue at WARN, but without the
// encoder context, which is what this line adds.
func (a *SaveAudioAction) logExportFailure(enc *clipEncoding, exportFormat string, exportRate int, outputPath string, err error) {
	// 14 = the 9 unconditional fields below, plus gain_db and encode_ms when the
	// export got far enough to have them, plus bitrate_kbps on the lossy formats,
	// plus error and operation.
	fields := make([]logger.Field, 0, 14)
	fields = append(fields,
		logger.String("component", "analysis.processor.actions"),
		logger.String("detection_id", a.CorrelationID),
		logger.String("species", a.species),
		logger.String("clip_path", a.relativeClipPath(outputPath)),
		logger.String("format", exportFormat),
		logger.String("encoder", enc.Encoder),
		logger.Int("sample_rate", exportRate),
		logger.Int64("duration_ms", clipDurationMs(len(a.pcmData), exportRate)),
		// Gain resolution is always attempted, so this is reportable even on a
		// pre-gain failure. It is 0 both when normalization is off (nothing to
		// measure) and when a measurement was instant; the gain_db field
		// disambiguates, since it is absent only when resolution did not finish.
		logger.Int64("measure_ms", enc.MeasureMs),
	)
	// Both gated on the same fact: the encoder runs only once the gain resolves,
	// so on a pre-gain failure neither the gain nor the encode duration exists.
	// Reporting encode_ms=0 there would read as "the encoder returned instantly"
	// rather than "the encoder never ran", which is the absence-versus-zero trap
	// GainResolved was added to close; leaving its sibling in it would be odd.
	//
	// How long each phase ran before failing is what separates a hung encoder
	// that burned the whole job deadline from one that rejected its arguments
	// instantly. Both were already measured; only the success line consumed them.
	if enc.GainResolved {
		fields = append(fields,
			logger.Float64("gain_db", enc.GainDB),
			logger.Int64("encode_ms", enc.EncodeMs),
		)
	}
	// A rejected bitrate is one of the ways a lossy encode fails, so report the
	// value that was going to be used rather than making it a second question.
	if enc.BitrateKbps > 0 {
		fields = append(fields, logger.Int("bitrate_kbps", enc.BitrateKbps))
	}
	fields = append(fields,
		logger.Error(err),
		logger.String("operation", "audio_export_failed"))

	if errors.Is(err, context.Canceled) {
		GetLogger().Debug("Audio clip export cancelled", fields...)
		return
	}
	GetLogger().Warn("Audio clip export failed", fields...)
}

// selectEncoder names the encoder that will write a clip of this format at this
// sample rate.
//
// Deciding the encoder as its own step, rather than inside the encode switch, is
// what lets a failed export name its encoder: the tag exists before the encoder
// is ever invoked.
//
// WAV and FLAC are always native (the WAV writer and go-flac); FFmpeg is never
// used for them. AAC and Opus have native encoders too, but they are opt-in
// while they earn field confidence, so they reach go-aac/go-m4a and go-opus only
// when the matching gate in internal/conf is set and the encoder accepts the
// clip's shape. Everything else, and every non-gated AAC or Opus clip, goes to
// FFmpeg.
func selectEncoder(exportFormat string, exportRate int) string {
	switch exportFormat {
	case ffmpeg.FormatWAV:
		return encoderNativeWAV
	case ffmpeg.FormatFLAC:
		return encoderNativeFLAC
	case ffmpeg.FormatAAC:
		// Opt-in; see internal/conf/native_encoders.go for the gate and its removal.
		if nativeAACSelected(exportRate) {
			return encoderNativeAAC
		}
		return encoderFFmpeg
	case ffmpeg.FormatOpus:
		// Opt-in; see internal/conf/native_encoders.go for the gate and its removal.
		if nativeOpusSelected(exportRate) {
			return encoderNativeOpus
		}
		return encoderFFmpeg
	default:
		// MP3 and ALAC are the only remaining formats, and FFmpeg owns their
		// codecs only; the loudness gain is resolved in Go first.
		return encoderFFmpeg
	}
}

// lossyBitrateKbps returns the bitrate a lossy encode will actually use, or 0
// for the lossless formats. The configured setting is not simply echoed:
// EffectiveBitrateKbps clamps it to what the codec accepts, and it is the
// clamped value that explains the size of the file on disk.
func lossyBitrateKbps(exportFormat, bitrate string) int {
	switch exportFormat {
	case ffmpeg.FormatMP3, ffmpeg.FormatAAC, ffmpeg.FormatOpus:
		return ffmpeg.EffectiveBitrateKbps(exportFormat, bitrate)
	default:
		return 0
	}
}

// encodeClip writes the captured PCM to outputPath in the resolved format and
// returns how it was encoded, for the success and failure logs.
//
// Loudness is resolved identically for every format and once for all of them,
// before any encoder runs: when normalization is enabled the EBU R128 gain is
// measured in Go via audionorm, otherwise the static Export.Gain is used. A
// native encoder applies that gain itself, WAV has it applied to the samples
// before writing, and FFmpeg receives it as a plain volume filter. FFmpeg's
// loudnorm filter is not used on this path at all, so no export format depends
// on FFmpeg for normalization.
//
// The returned clipEncoding is populated as far as the export got, so a caller
// handling an error can still report the encoder and, past gain resolution, the
// gain that was going to be applied.
func (a *SaveAudioAction) encodeClip(ctx context.Context, exportRate int, exportFormat, outputPath string) (clipEncoding, error) {
	enc := clipEncoding{
		Encoder:     selectEncoder(exportFormat, exportRate),
		BitrateKbps: lossyBitrateKbps(exportFormat, a.Settings.Realtime.Audio.Export.Bitrate),
	}

	measureStart := time.Now()
	gainDB, err := a.resolveExportGainDB(ctx, exportRate, exportFormat)
	enc.MeasureMs = time.Since(measureStart).Milliseconds()
	if err != nil {
		return enc, err
	}
	enc.GainDB = gainDB
	enc.GainResolved = true

	encodeStart := time.Now()
	err = a.runEncoder(ctx, enc.Encoder, exportRate, enc.BitrateKbps, exportFormat, outputPath, gainDB)
	enc.EncodeMs = time.Since(encodeStart).Milliseconds()
	return enc, err
}

// runEncoder writes the clip with the encoder selectEncoder picked, applying the
// already-resolved loudness gain. It is split out of encodeClip so the encoder's
// own wall time can be measured without the loudness measurement that precedes
// it; it deliberately takes no clipEncoding, because it only reads the routing
// decision and must not be able to alter what the logs report.
func (a *SaveAudioAction) runEncoder(ctx context.Context, encoder string, exportRate, bitrateKbps int, exportFormat, outputPath string, gainDB float64) error {
	switch encoder {
	case encoderNativeWAV:
		// WAV honours the resolved gain like every other format. It used to be the
		// one exception, writing captured samples verbatim, which meant an
		// operator with normalization or Export.Gain configured silently got
		// neither. That was easy to miss because WAV is not only a chosen format:
		// resolveExportParams downgrades to it for bat/ultrasonic clips
		// (needsBatFormatFallback) and for installs with no usable encoder
		// (strandedWithoutEncoder), so a user could land here without asking to.
		pcm := a.pcmData
		if gainDB != 0 {
			// Applied returns a gained copy, so a.pcmData stays pristine for the
			// spectrogram pre-render job that reads it after the export.
			pcm = pcmgain.Applied(pcm, gainDB)
		}
		return convert.SavePCMDataToWAV(outputPath, pcm, exportRate, conf.BitDepth)

	case encoderNativeFLAC:
		// FLAC always encodes natively via go-flac; FFmpeg is never used for FLAC.
		return flac.EncodePCM(ctx, &flac.Options{
			PCMData:    a.pcmData,
			OutputPath: outputPath,
			SampleRate: exportRate,
			Channels:   conf.NumChannels,
			BitDepth:   conf.BitDepth,
			GainDB:     gainDB,
		})

	case encoderNativeAAC:
		return a.encodeClipNativeAAC(ctx, exportRate, bitrateKbps, outputPath, gainDB)

	case encoderNativeOpus:
		return a.encodeClipNativeOpus(ctx, exportRate, bitrateKbps, outputPath, gainDB)

	default:
		return a.encodeClipFFmpeg(ctx, exportRate, exportFormat, outputPath, gainDB)
	}
}

// encodeClipFFmpeg encodes the clip with FFmpeg, which owns the codec only. The
// loudness gain is planned in Go before FFmpeg is invoked, exactly as on the
// native paths, and FFmpeg applies it as a plain volume filter.
func (a *SaveAudioAction) encodeClipFFmpeg(ctx context.Context, exportRate int, exportFormat, outputPath string, gainDB float64) error {
	return ffmpeg.ExportAudio(ctx, a.buildFFmpegExportOptions(exportRate, exportFormat, outputPath, gainDB))
}

// buildFFmpegExportOptions assembles the FFmpeg export request. It is separate
// from encodeClipFFmpeg so a test can assert every field without running FFmpeg:
// this is the default path every existing install takes, so a silently dropped
// field here would be a regression no other test would catch.
//
// GainDB carries the resolved loudness gain rather than the raw Export.Gain
// setting, so an FFmpeg-encoded clip is normalised by the same audionorm
// measurement as a natively encoded one. FFmpeg's loudnorm filter is no longer
// used anywhere on this path.
func (a *SaveAudioAction) buildFFmpegExportOptions(exportRate int, exportFormat, outputPath string, gainDB float64) *ffmpeg.ExportOptions {
	return &ffmpeg.ExportOptions{
		PCMData:    a.pcmData,
		OutputPath: outputPath,
		Format:     exportFormat,
		Bitrate:    a.Settings.Realtime.Audio.Export.Bitrate,
		SampleRate: exportRate,
		Channels:   conf.NumChannels,
		BitDepth:   conf.BitDepth,
		FFmpegPath: a.Settings.Realtime.Audio.FfmpegPath,
		GainDB:     gainDB,
	}
}

// encodeClipNativeAAC encodes the clip to AAC-LC in an MP4 (.m4a) container with
// go-aac and go-m4a. A failure is returned rather than retried through FFmpeg:
// the operator opted this clip into the native encoder, and a silent fallback
// would hide exactly the failures this rollout exists to surface.
func (a *SaveAudioAction) encodeClipNativeAAC(ctx context.Context, exportRate, bitrateKbps int, outputPath string, gainDB float64) error {
	return aac.EncodePCM(ctx, &aac.Options{
		PCMData:     a.pcmData,
		OutputPath:  outputPath,
		SampleRate:  exportRate,
		Channels:    conf.NumChannels,
		BitDepth:    conf.BitDepth,
		BitrateKbps: bitrateKbps,
		GainDB:      gainDB,
	})
}

// encodeClipNativeOpus encodes the clip to Ogg Opus (.opus) with go-opus. As
// with AAC, a failure is surfaced rather than falling back to FFmpeg.
func (a *SaveAudioAction) encodeClipNativeOpus(ctx context.Context, exportRate, bitrateKbps int, outputPath string, gainDB float64) error {
	return opus.EncodePCM(ctx, &opus.Options{
		PCMData:     a.pcmData,
		OutputPath:  outputPath,
		SampleRate:  exportRate,
		Channels:    conf.NumChannels,
		BitDepth:    conf.BitDepth,
		BitrateKbps: bitrateKbps,
		GainDB:      gainDB,
	})
}

// nativeAACSelected reports whether this clip should take the native AAC path:
// the operator opted in AND go-aac accepts the clip's rate, depth and channel
// count. A gated-on clip the encoder cannot carry falls back to FFmpeg with a
// warning rather than failing outright. This whole check goes away when the gate
// is removed.
//
// On an install with no FFmpeg at all, opting in stops config validation from
// downgrading the format to WAV, so there would be nothing left to fall back to
// here. resolveExportParams catches that case earlier and resolves the clip to
// WAV, so the recording survives either way.
func nativeAACSelected(exportRate int) bool {
	if !conf.NativeAACEncoderEnabled() {
		return false
	}
	if err := aac.Supports(exportRate, conf.BitDepth, conf.NumChannels); err != nil {
		logNativeEncoderSkipped(ffmpeg.FormatAAC, exportRate, err)
		return false
	}
	return true
}

// nativeOpusSelected is the Opus counterpart of nativeAACSelected.
func nativeOpusSelected(exportRate int) bool {
	if !conf.NativeOpusEncoderEnabled() {
		return false
	}
	if err := opus.Supports(exportRate, conf.BitDepth, conf.NumChannels); err != nil {
		logNativeEncoderSkipped(ffmpeg.FormatOpus, exportRate, err)
		return false
	}
	return true
}

// onceByKey emits a message at most once per distinct condition, where the
// condition is described by a key rather than by "the first time we got here".
//
// A bare sync.Once is the wrong instrument for these logs. Each guards a
// decision made from a format and a sample rate, and neither is fixed for the
// process: Export.Type is hot-reloadable (settings changed in the UI take effect
// without a restart), and the capture rate is per-source, so a multi-source
// install runs several at once. A sync.Once lets whichever condition happens to
// fire first silence every later, different one, leaving the operator with an
// explanation that names a format they have since changed away from, or a rate
// belonging to another source.
//
// The key space is the product of a handful of formats and the configured source
// rates, so it is small and bounded by the configuration rather than by traffic;
// no eviction is needed.
type onceByKey struct {
	seen sync.Map // string key -> struct{}
}

// do runs emit unless this key has already been emitted.
//
// This is NOT sync.Once semantics and must not be reused as a lazy-init guard:
// the key is marked before emit runs, so a concurrent second caller returns
// immediately rather than blocking until the first has finished. That is what a
// log-flood guard wants (no caller depends on the line having been written yet)
// and exactly what an initializer does not.
func (o *onceByKey) do(key string, emit func()) {
	if _, loaded := o.seen.LoadOrStore(key, struct{}{}); !loaded {
		emit()
	}
}

// formatRateKey identifies a downgrade or fallback decision by the two inputs
// that vary at runtime, so each distinct combination explains itself exactly
// once instead of the first one silencing the rest.
func formatRateKey(format string, rate int) string {
	return format + "@" + strconv.Itoa(rate)
}

// Guards for the export-format warnings. Each fires on every single detection of
// an affected install, so without a guard they are a permanent log flood; the
// per-clip encoder and format fields carry the ongoing signal instead.
//
//nolint:gochecknoglobals // process-lifetime log-flood guards, matching mqttNotReadyWarnOnce
var (
	nativeEncoderSkipLogged  onceByKey
	batFormatDowngradeLogged onceByKey
	strandedFormatLogged     onceByKey
)

// logBatFormatDowngrade explains why an install configured for a lossy format is
// producing .wav files: an ultrasonic capture runs at a rate the configured
// container cannot carry, so the export switched to WAV. The sibling downgrade
// in strandedWithoutEncoder already logged its reason; this one did not, leaving
// the operator with no explanation for a format they did not choose.
//
// Logged at WARN, matching the sibling downgrade in resolveExportParams: both
// mean "you are not getting the export format you configured", which is a
// configuration mismatch the operator should act on rather than a note.
//
// Guarded per (format, rate) rather than once per process: a bat install left on
// a lossy export format takes this path on every single detection, but the two
// inputs still vary, by hot-reloaded Export.Type and by capture source.
func logBatFormatDowngrade(requestedFormat string, rate int) {
	batFormatDowngradeLogged.do(formatRateKey(requestedFormat, rate), func() {
		GetLogger().Warn("Ultrasonic clip format downgraded to WAV; the configured format cannot carry this sample rate",
			logger.String("component", "analysis.processor.actions"),
			logger.String("requested_format", requestedFormat),
			logger.Int("sample_rate", rate),
			logger.String("operation", "audio_export_bat_format_fallback"))
	})
}

// logStrandedFormatFallback explains why an install with no FFmpeg is producing
// .wav files: the operator opted a lossy format into its native encoder, which
// kept config validation from downgrading the format, and the native encoder
// then turned out not to accept this clip's shape.
//
// Same level and same guard as logBatFormatDowngrade, because it means the same
// thing to the operator: you are not getting the format you configured. It used
// to be the sibling's opposite on both counts, warning on every single clip
// forever where the bat downgrade logged once.
//
// No detection_id, matching the sibling. Once the line is guarded the id names
// whichever clip happened to arrive first, which can be hours stale by the time
// anyone reads it, and correlating by it finds a single line. The condition is a
// property of the configuration, not of a clip.
//
// The key covers the format and the rate, not FfmpegPath, which is also part of
// the condition and is hot-reloadable too. An operator who sets a path and later
// clears it again will not be warned a second time for a combination already
// reported. That is the intended trade: the message is about a configuration
// that was already explained once, and keying on it would re-flood on every
// path edit.
func logStrandedFormatFallback(requestedFormat string, rate int) {
	strandedFormatLogged.do(formatRateKey(requestedFormat, rate), func() {
		GetLogger().Warn("No encoder can carry this clip; exporting as WAV",
			logger.String("component", "analysis.processor.actions"),
			logger.String("requested_format", requestedFormat),
			logger.Int("sample_rate", rate),
			logger.String("operation", "audio_export_no_encoder_fallback"))
	})
}

// logNativeEncoderSkipped records that an opted-in native encoder could not
// carry this clip, so an operator who set the env flag and sees FFmpeg in the
// encoder field has a reason rather than a mystery. The reason comes from the
// encoder's own Supports error, so the log names the offending value instead of
// dumping all three.
//
// Guarded per (format, rate): the rate is what the encoder usually rejects, and
// it varies per capture source, so a once-per-format guard would report only
// whichever source happened to export first. One guard covers both formats,
// because the format is part of the key.
func logNativeEncoderSkipped(format string, rate int, reason error) {
	nativeEncoderSkipLogged.do(formatRateKey(format, rate), func() {
		GetLogger().Warn("Native encoder requested but the clip format is unsupported; using FFmpeg for this format",
			logger.String("component", "analysis.processor.actions"),
			logger.String("format", format),
			logger.Int("sample_rate", rate),
			logger.String("reason", reason.Error()),
			logger.String("operation", "audio_export_native_unsupported"))
	})
}

// resolveExportGainDB returns the gain in dB to apply to this clip. Every export
// path reads it: a native encoder applies the value itself, and FFmpeg receives
// it as a volume filter. With normalization enabled and supported it is the
// measured EBU R128 gain, which intentionally replaces rather than compounds the
// static Export.Gain, matching the FFmpeg loudnorm behaviour this replaced, where
// an enabled loudnorm filter also superseded the volume filter. Otherwise it is
// the static Export.Gain.
//
// Note that audionorm applies a single linear gain to reach the target
// integrated loudness under the true-peak ceiling. It does not consume
// Normalization.LoudnessRange, so no export format gets LRA/dynamic-range
// treatment any more.
//
// That is deliberate. On a clip built to force FFmpeg out of linear mode
// (measured LRA 7.40 against a 7.0 target) loudnorm's dynamic mode compressed
// LRA only 7.4 to 7.1 while missing the loudness target by 1.1 LU, where the
// linear gain landed within 0.02 LU. Reproduce with the normbench
// transient-over-quiet-bed case: go test -tags normcompare
// ./internal/audiocore/normbench/ and read the LRA p/n/f column.
//
// Scope that evidence honestly: it was gathered on clips around the 15 s default
// export length. Extended capture can export far longer clips
// (conf.MaxExtendedCaptureDuration is 1200 s), where dynamic-range treatment
// would matter more, and the corpus does not cover that.
func (a *SaveAudioAction) resolveExportGainDB(ctx context.Context, exportRate int, format string) (float64, error) {
	exportSettings := &a.Settings.Realtime.Audio.Export
	if !exportSettings.Normalization.Enabled {
		return exportSettings.Gain, nil
	}

	depthSupported := conf.BitDepth == nativeNormalizationBitDepth
	if depthSupported && audionormSupportsTargets(exportSettings.Normalization.TargetLUFS, exportSettings.Normalization.TruePeak) {
		gainDB, err := a.planNativeNormalizationGain(ctx, exportRate, format,
			exportSettings.Normalization.TargetLUFS, exportSettings.Normalization.TruePeak)
		switch {
		case err == nil:
			return gainDB, nil
		case ctx.Err() != nil:
			// The caller is going away; do not paper over that with a fallback.
			//
			// Joining the context error in is not the ctx-based misclassification
			// logExportFailure warns about, it is the opposite: on THIS branch the
			// measurement error alone is not fatal, because the code below would
			// have degraded to the static gain and finished the export. The clip
			// fails only because the context is done, so cancellation genuinely is
			// the cause and the joined error says so. audionorm's own error need
			// not wrap ctx.Err(), so without the join a normal shutdown here would
			// be reported as a WARN failure.
			return 0, errors.Join(err, ctx.Err())
		}
		// The clip could not be measured (audionorm rejects the dimensions, e.g. a
		// source reporting a sample rate below the K-weighting minimum). Losing
		// normalization is bad; losing the recording is worse, and every other
		// unmeasurable case below already degrades to the static gain. Before this
		// path stopped using loudnorm, FFmpeg would still have produced a clip
		// here, so failing hard would be a new way to lose audio on the default
		// MP3 install.
		GetLogger().Warn("Loudness measurement failed; encoding without normalization",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.String("format", format),
			logger.Int("sample_rate", exportRate),
			logger.Error(err),
			logger.String("reason", "measurement_failed"),
			logger.String("operation", "audio_export_normalize_skip"))
		return exportSettings.Gain, nil
	}

	// Unreachable for a validated config: capture is 16-bit (conf.BitDepth) and
	// settings validation REJECTS loudness targets outside audionorm's range at
	// startup rather than clamping them, so a running instance cannot hold one.
	// Defense-in-depth for unvalidated/legacy settings only. There is no longer a
	// loudnorm fallback on any path, FFmpeg included, so encode with the static
	// gain and surface the skipped normalization at WARN.
	reason := "normalization_targets_out_of_native_range"
	if !depthSupported {
		reason = "unsupported_bit_depth"
	}
	GetLogger().Warn("Native normalization skipped; encoding without normalization",
		logger.String("component", "analysis.processor.actions"),
		logger.String("detection_id", a.CorrelationID),
		logger.String("format", format),
		logger.String("reason", reason),
		logger.String("operation", "audio_export_normalize_skip"))
	return exportSettings.Gain, nil
}

// nativeNormalizationBitDepth is the only PCM bit depth the native gain and
// normalization path handles: audionorm.PlanClampedGainInt16Bytes and pcmgain
// both operate on int16 samples. The constraint belongs to those helpers rather
// than to any one codec, so every native encoder shares it.
const nativeNormalizationBitDepth = 16

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

// nativeExportMaxGainDB is the absolute backstop on the loudness gain a clip
// export applies, whichever encoder writes the file. It is not an arbitrary round
// number: the widest lift any in-range configuration can legitimately ask for is
// the most aggressive permitted target (conf.MaxTargetLUFS, -10) minus the EBU
// R128 absolute gate (audionormMinTargetLUFS, -70), which is exactly 60 dB.
//
// It is numerically equal to ffmpeg.MaxGainDB, but do not read that as "the
// ceiling loudnorm allowed": ffmpeg.MaxGainDB bounded only the gate-fallback
// OFFSET the removed export path computed, never loudnorm's own two-pass output
// (see the note on audionorm.DefaultMaxGainDB). The equality is a coincidence of
// two independent derivations, not a shared constraint.
//
// Deliberately not audionorm.DefaultMaxGainDB (30 dB). That is the BirdWeather
// soundscape-upload ceiling and stays where it is: those uploads go to a public
// platform and are not this path's to change. At the default -23 LUFS target
// the per-clip bounds below always bind first, so this constant only backstops
// the most aggressive targets.
const nativeExportMaxGainDB = 60.0

// gateFallbackGainDB handles the clip EBU R128 gating cannot measure. A clip
// whose gated integrated loudness falls below the -70 LUFS absolute gate yields
// -Inf, and PlanGain plans no gain at all for it, so the clip would be exported
// untouched and inaudible. When such a clip still has a finite true peak there
// is enough signal to work with, so the gain is anchored to the true-peak
// ceiling, which is what the removed FFmpeg loudnorm path did for the same case.
//
// That anchor alone is not enough. Lifting purely by true-peak headroom leaves
// the resulting loudness at (ceiling - crest factor), so a FLAT signal (steady
// hiss from a dead or muted microphone, whose crest factor is only a few dB)
// lands far LOUDER than the loudness target: measured, a sub-gate noise floor
// came out at -13.8 LUFS against a -23 target. The second bound closes that.
// Gating failing is itself information: the clip's true loudness must be below
// audionormMinTargetLUFS, so lifting by no more than (target - gate) cannot
// overshoot the target however peaky or flat the clip turns out to be.
//
// That premise is worth stating precisely, since the bound rests on it.
// audionorm's meter reports -Inf on exactly three paths: every 400 ms block sat
// under the absolute gate (the premise holds by construction); the relative gate
// eliminated every block (unreachable, because the loudest gated block always
// exceeds a gate set 10 LU below their mean); or the clip is shorter than one
// 400 ms block, where loudness is undefined at ANY level. Only that third path
// breaks the premise, and it cannot arise here because exported clips are whole
// detection segments, orders of magnitude longer. Even if it did, the true-peak
// anchor is the tighter bound for a short LOUD clip, so it would still attenuate
// rather than amplify.
//
// Genuine silence (a true peak of -Inf too) reports false and is left alone;
// there is nothing to lift.
func gateFallbackGainDB(meas audionorm.Measurement, targetLUFS, truePeakDBTP float64) (gainDB float64, ok bool) {
	if !math.IsInf(meas.IntegratedLUFS, -1) || math.IsInf(meas.TruePeakDBTP, -1) {
		return 0, false
	}
	peakBound := truePeakDBTP - meas.TruePeakDBTP
	loudnessBound := targetLUFS - audionormMinTargetLUFS
	return math.Min(peakBound, loudnessBound), true
}

// refineLiftedGainDB returns the extra gain to add on top of a gate-fallback
// lift. Loudness gating is not linear in gain, so the loudness of the lifted
// signal cannot be derived arithmetically: blocks that sat under the absolute
// gate before the lift pass it afterwards. The only way to know is to measure
// the lifted signal, which is what this does, on a copy so pcm is untouched.
//
// Once the clip is measurable a normal PlanGain finishes the job, landing it on
// the target and respecting the true-peak ceiling. A clip still under the gate
// after the lift (or a measurement error) yields 0, leaving the conservative
// fallback as the final answer rather than guessing.
//
// This runs only for sub-gate clips, so the extra measurement pass is off the
// common path entirely.
func refineLiftedGainDB(pcm []byte, liftDB float64, opts audionorm.Options) float64 {
	lifted, err := audionorm.MeasureInt16Bytes(pcmgain.Applied(pcm, liftDB), opts.SampleRate, opts.Channels)
	if err != nil || math.IsInf(lifted.IntegratedLUFS, -1) {
		return 0
	}
	return audionorm.PlanGain(lifted, opts).GainDB
}

// planNativeNormalizationGain measures the clip's EBU R128 integrated loudness and
// true peak with audionorm and returns the single linear gain (dB) that brings it
// to targetLUFS without its true peak exceeding truePeakDBTP, clamped to
// +/-nativeExportMaxGainDB. A clip under the R128 absolute gate is lifted by its
// true-peak headroom instead (see gateFallbackGainDB); genuine silence yields 0.
// The gain is applied by the encoder; a.pcmData is not modified here.
func (a *SaveAudioAction) planNativeNormalizationGain(ctx context.Context, sampleRate int, format string, targetLUFS, truePeakDBTP float64) (float64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	// audionorm decodes the PCM bytes inline; a.pcmData is not mutated.
	meas, err := audionorm.MeasureInt16Bytes(a.pcmData, sampleRate, conf.NumChannels)
	if err != nil {
		return 0, errors.New(err).
			Component("analysis.processor").
			Category(errors.CategoryAudio).
			Context("operation", "native_normalize_measure").
			Context("format", format).
			Context("detection_id", a.CorrelationID).
			Build()
	}

	opts := audionorm.Options{
		SampleRate:   sampleRate,
		Channels:     conf.NumChannels,
		TargetLUFS:   targetLUFS,
		TruePeakDBTP: truePeakDBTP,
	}
	res := audionorm.PlanGain(meas, opts)

	planned := res.GainDB
	gateLifted := false
	if fallback, ok := gateFallbackGainDB(meas, targetLUFS, truePeakDBTP); ok {
		// The fallback is deliberately conservative, because a sub-gate clip's
		// real loudness is unknown and the bound has to assume the worst case.
		// Lifting it, however, usually raises it above the gate, at which point
		// it CAN be measured: refine from that real measurement so a very quiet
		// clip still lands on target instead of merely somewhere audible.
		planned = fallback + refineLiftedGainDB(a.pcmData, fallback, opts)
		gateLifted = true
	}

	gainDB, clamped := audionorm.ClampGainDB(planned, nativeExportMaxGainDB)

	GetLogger().Debug("Native loudness analysis (detection save)",
		logger.String("format", format),
		logger.Float64("measured_lufs", meas.IntegratedLUFS),
		logger.Float64("true_peak_dbtp", meas.TruePeakDBTP),
		logger.Float64("target_lufs", targetLUFS),
		logger.Float64("gain_db", gainDB),
		logger.Bool("peak_limited", res.PeakLimited),
		logger.Bool("gate_lifted", gateLifted),
		logger.Bool("gain_clamped", clamped),
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
		logBatFormatDowngrade(format, rate)
		format = ffmpeg.FormatWAV
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

	if a.strandedWithoutEncoder(rate, format) {
		logStrandedFormatFallback(format, rate)
		format = ffmpeg.FormatWAV
		path = replaceExtension(path, ".wav")
	}

	return rate, format, path
}

// strandedWithoutEncoder reports whether this clip has no encoder left: the
// operator opted a lossy format into its native encoder, so config validation
// did not downgrade the format to WAV despite FFmpeg being absent, but the
// native encoder turns out not to accept this clip's shape.
//
// Without this the export would call FFmpeg with an empty binary path and the
// recording would be lost. Resolving it here rather than at the encode step
// matters because the clip path still gets its extension corrected, so the file
// on disk and the name recorded in the database cannot disagree.
//
// REMOVAL: this goes away with the gate. Once the native encoders are the
// default, config validation stops downgrading these formats at all and the
// question becomes a plain "can the native encoder carry it".
func (a *SaveAudioAction) strandedWithoutEncoder(rate int, format string) bool {
	if a.Settings.Realtime.Audio.FfmpegPath != "" {
		return false // FFmpeg can still take it
	}
	switch format {
	case ffmpeg.FormatAAC:
		return conf.NativeAACEncoderEnabled() && !nativeAACSelected(rate)
	case ffmpeg.FormatOpus:
		return conf.NativeOpusEncoderEnabled() && !nativeOpusSelected(rate)
	default:
		// Every other format either has an unconditional native encoder (WAV,
		// FLAC) or was already downgraded to WAV by config validation when
		// FFmpeg went missing (MP3).
		return false
	}
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
