// processor/actions_database.go
// This file contains database and logging action implementations.

package processor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/go-audio-dsp/loudnorm"

	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/analysis/species"
	"github.com/tphakala/birdnet-go/internal/audiocore/aac"
	"github.com/tphakala/birdnet-go/internal/audiocore/audioformat"
	"github.com/tphakala/birdnet-go/internal/audiocore/clipenc"
	"github.com/tphakala/birdnet-go/internal/audiocore/flac"
	"github.com/tphakala/birdnet-go/internal/audiocore/loudness"
	"github.com/tphakala/birdnet-go/internal/audiocore/mp3"
	"github.com/tphakala/birdnet-go/internal/audiocore/opus"
	"github.com/tphakala/birdnet-go/internal/audiocore/resample"
	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/audiocore/wav"
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

// clipEncoding records how a clip was written: which encoder owned the file and
// the parameters a support log needs to explain the result on disk.
//
// It is resolved BEFORE the encoder runs, which is the point of the type: a
// failed export can then name the encoder that failed.
type clipEncoding struct {
	Encoder     string  // clipenc.NativeAAC, clipenc.NativeMP3, ...
	GainDB      float64 // loudness gain the encoder applies to the clip
	BitrateKbps int     // encoded bitrate for the lossy formats; 0 when lossless
	// GainResolved distinguishes "the gain is 0 dB" from "the export failed
	// before the gain was known". Without it a failure during gain resolution
	// logs gain_db=0, which is also the default Export.Gain, so the field reads
	// as a measurement when it is really an absence.
	GainResolved bool
	// Normalized reports whether the gain came from an actual loudness
	// measurement, as opposed to falling back to the static Export.Gain. It is
	// on the success line because the WARN explaining a fallback is emitted at
	// most once per (reason, format, rate) for the process lifetime: on a
	// long-running install the explanation can fall outside the window a support
	// dump captures, leaving every clip in that dump unexplained. gain_db cannot
	// substitute, because the default Export.Gain is 0 and so is a measured gain
	// of 0. This makes each clip say for itself which path produced it.
	Normalized bool
	// MeasureMs and EncodeMs are timed separately because they answer different
	// questions and have wildly different costs. Resolving the gain with
	// normalization enabled is a full-clip EBU R128 pass, which on a Pi with a
	// 20-minute extended capture dominates the export; folding it into a single
	// encode_ms blames the codec for normalization cost and makes both "exports
	// are slow" and codec comparisons read wrong.
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
	a.publishDetectionEvent(ctx, isNewSpecies, daysSinceFirstSeen, novelty)

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

	// Cached-only lookup. The previous BirdImageCache.Get here was a synchronous,
	// uncancellable provider fetch inside the CompositeAction whose 30s timeout this
	// file's own note (see the audio-export comment above) records as the reason slow
	// work was moved out of it. A cold species could take minutes.
	//
	// The URL stays the provider's upstream address rather than becoming a media-proxy
	// URL. This metadata reaches notification templates as bg_image_url and ends up in
	// Discord rich embeds and webhook payloads, and those are fetched server-side by
	// the notification service, from outside the user's network. A BirdNET-Go URL is
	// unreachable from there for the typical home-LAN install, whether it is
	// root-relative or an absolute one built from a private host. The provider URL is a
	// public CDN address and is the only form that renders. Tracked separately.
	// Shared with the SSE and MQTT actions of the same CompositeAction through the
	// detection context, so one detection costs one lookup rather than three. The
	// helper also schedules the prefetch when nothing is cached yet, so that this
	// notification carries no image but the next detection of the species can.
	if a.processor != nil {
		birdImage := getBirdImageFromCache(a.DetectionCtx, a.processor.BirdImageCache,
			a.Result.Species.ScientificName, a.Result.Species.CommonName, a.CorrelationID)
		if birdImage.URL != "" {
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

// newSpeciesImageWait bounds how long a new-species notification will wait for its
// image to resolve. It is short relative to the enclosing CompositeAction's 30s
// timeout, so a throttled provider degrades to a notification without an image
// rather than putting the whole Database -> SSE -> MQTT chain at risk.
const newSpeciesImageWait = 3 * time.Second

// clipRateCorrectionTimeout bounds the post-encode clip sample rate write. A stalled
// write must give up while the job's execution context is still live, otherwise the
// job would be recorded as failed (and possibly retried) for a clip already written.
const clipRateCorrectionTimeout = 5 * time.Second

// warmSpeciesImage resolves this detection's species image, waiting at most
// newSpeciesImageWait, so that the caller's subsequent GetCached lookup can succeed.
//
// This is the one image lookup that is still allowed to wait, and only for a new
// species. Every other path in this file and its siblings is cached-only, because a
// cold species can otherwise occupy the provider chain for minutes. A new species is
// rare by construction (once per species, ever) and is precisely the detection whose
// notification an image matters most for.
func (a *DatabaseAction) warmSpeciesImage(ctx context.Context) {
	if a.processor == nil || a.processor.BirdImageCache == nil {
		return
	}
	scientificName := a.Result.Species.ScientificName
	if scientificName == "" {
		return
	}
	if _, found, _ := a.processor.BirdImageCache.GetCached(scientificName); found {
		return
	}

	waitCtx, cancel := context.WithTimeout(ctx, newSpeciesImageWait)
	defer cancel()
	if _, err := a.processor.BirdImageCache.GetWithContext(waitCtx, scientificName); err != nil {
		GetLogger().Debug("New species image did not resolve within the notification wait",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.String("scientific_name", scientificName),
			logger.Duration("waited", newSpeciesImageWait),
			logger.Error(err))
	}
}

// publishDetectionEvent publishes a detection event to the event bus.
// All detections are published so that alert rules on detection.occurred can fire.
// New species detections additionally go through suppression and notification recording.
func (a *DatabaseAction) publishDetectionEvent(ctx context.Context, isNewSpecies bool, daysSinceFirstSeen int, novelty species.NoveltyStatus) {
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
			// A new species is, by definition, absent from the image cache: the
			// startup warm-up only covers previously detected species. Without a
			// brief wait here the notification for the one detection users most
			// want an image for would never carry one. Bounded so that a slow or
			// throttled provider cannot push the surrounding CompositeAction
			// towards its 30s timeout, and only on this branch: ordinary
			// detections stay entirely off the provider path.
			a.warmSpeciesImage(ctx)

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
		// species is carried here for the same reason it is carried on the
		// success line, and with more force: this is the branch where the clip is
		// lost outright, so it is the export log where naming the bird matters
		// most. Unguarded on purpose, unlike the configuration warnings above:
		// this reports a per-detection loss, not a standing misconfiguration.
		GetLogger().Warn("Skipping audio export: no PCM data available",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.String("species", a.species),
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
	a.correctClipSampleRate(ctx, exportRate)

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
	// 15 = the 13 unconditional fields below, plus bitrate_kbps on the lossy
	// formats, plus operation.
	fields := make([]logger.Field, 0, 15)
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
		logger.Bool("normalized", enc.Normalized),
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
// (/api/v2/media/audio/{name}). "Normally" because a name with no known
// extension gets its extension rewritten by resolveExportParams; for every other
// name the file is written at the stored name, so the logged name and the
// database row agree.
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
// the error and names neither the encoder nor the format, so an operator could
// not tell from the logs which encoder produced the failure.
//
// A CANCELLED export is shutdown rather than a defect, so it is recorded at
// Debug: exports in flight when the process stops must not look like errors.
//
// The classification reads the ERROR, not the context state at the moment of
// logging. Asking the context whether it was cancelled answers a different
// question: it says the process is shutting down, not that shutdown is why this
// export failed. ENOSPC or corrupt PCM hit while a
// shutdown happens to be in progress all satisfy a ctx-based test and would be
// filed at Debug under a message asserting they were cancelled, which on a
// container that OOM-restarts misattributes every in-flight failure in the
// restart window.
//
// Every path that CONTEXT cancellation can interrupt reports it in the error, so
// nothing about a normal shutdown is lost: every native encoder checks the context before it writes anything (FLAC
// additionally re-checks per chunk while applying gain, AAC and Opus via
// audiotemp.WriteFile), and resolveExportGainDB joins it onto a measurement that
// cancellation turned terminal. The stretches that take no context at all (the
// WAV writer, and the one-shot encode calls once audiotemp has let them start)
// cannot be interrupted by cancellation either, so any error they return is a
// genuine defect and belongs at WARN.
//
// A context that EXPIRED is not shutdown either. The job queue wraps every
// execution in context.WithTimeout, so an encode slow enough to blow
// it (a long extended capture on a Pi) is precisely the failure
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

// selectEncoder names the encoder that will write a clip of this format.
//
// Deciding the encoder as its own step, rather than inside the encode switch, is
// what lets a failed export name its encoder: the tag exists before the encoder
// is ever invoked.
//
// Every supported export format is always native (the WAV writer, go-flac,
// go-aac with go-m4a, go-mp3 and go-opus); clip export never uses FFmpeg. An
// unrecognized format is an error rather than a fallback.
//
// There is deliberately no fallback for the lossy formats either.
// resolveExportParams leaves them only at a rate their encoder accepts (it
// resamples everything else to conf.SampleRate), so an encoder that still rejects
// the clip, because a resample could not run, fails the export loudly instead of
// writing a wrong file.
func selectEncoder(exportFormat string) (string, error) {
	if tag, ok := clipenc.ForFormat(exportFormat); ok {
		return tag, nil
	}
	return "", unsupportedExportFormatError(exportFormat)
}

// unsupportedExportFormatError reports an export format no native encoder
// handles.
func unsupportedExportFormatError(exportFormat string) error {
	return errors.Newf("unsupported audio export format %q", exportFormat).
		Component("analysis.processor").
		Category(errors.CategoryValidation).
		Context("operation", "audio_export_select_encoder").
		Context("format", exportFormat).
		Build()
}

// lossyBitrateKbps returns the bitrate a lossy encode will actually use, or 0
// for the lossless formats. The configured setting is not simply echoed:
// EffectiveBitrateKbps clamps it to what the codec accepts, and it is the
// clamped value that explains the size of the file on disk.
func lossyBitrateKbps(exportFormat, bitrate string) int {
	switch exportFormat {
	case audioformat.MP3, audioformat.AAC, audioformat.Opus:
		return audioformat.EffectiveBitrateKbps(exportFormat, bitrate)
	default:
		return 0
	}
}

// encodeClip writes the captured PCM to outputPath in the resolved format and
// returns how it was encoded, for the success and failure logs.
//
// Loudness is resolved identically for every format and once for all of them,
// before any encoder runs: when normalization is enabled the EBU R128 gain is
// measured in Go via go-audio-dsp loudnorm, otherwise the static Export.Gain is used. A
// native encoder applies that gain itself and WAV has it applied to the samples
// before writing. No export format depends on FFmpeg.
//
// The returned clipEncoding is populated as far as the export got, so a caller
// handling an error can still report the encoder and, past gain resolution, the
// gain that was going to be applied.
func (a *SaveAudioAction) encodeClip(ctx context.Context, exportRate int, exportFormat, outputPath string) (clipEncoding, error) {
	bitrateKbps := lossyBitrateKbps(exportFormat, a.Settings.Realtime.Audio.Export.Bitrate)
	encoder, err := selectEncoder(exportFormat)
	if err != nil {
		return clipEncoding{}, err
	}
	if encoder == clipenc.NativeMP3 {
		// go-mp3 codes only the 14 fixed MPEG-1 rates and the wrapper rounds the
		// requested bitrate to the nearest, so report that rounded value here to keep
		// the encoding log and the file on disk in agreement.
		bitrateKbps = mp3.RoundBitrateKbps(bitrateKbps)
	}
	enc := clipEncoding{
		Encoder:     encoder,
		BitrateKbps: bitrateKbps,
	}

	measureStart := time.Now()
	gainDB, normalized, err := a.resolveExportGainDB(ctx, exportRate, exportFormat)
	enc.MeasureMs = time.Since(measureStart).Milliseconds()
	if err != nil {
		return enc, err
	}
	enc.GainDB = gainDB
	enc.GainResolved = true
	enc.Normalized = normalized

	encodeStart := time.Now()
	err = a.runEncoder(ctx, enc.Encoder, exportRate, enc.BitrateKbps, exportFormat, outputPath, gainDB)
	enc.EncodeMs = time.Since(encodeStart).Milliseconds()
	return enc, err
}

// runEncoder writes the clip with the encoder selectEncoder picked, applying the
// already-resolved loudness gain. An encoder tag with no native writer is an
// error; there is no FFmpeg route. It is split out of encodeClip so the encoder's
// own wall time can be measured without the loudness measurement that precedes
// it; it deliberately takes no clipEncoding, because it only reads the routing
// decision and must not be able to alter what the logs report.
func (a *SaveAudioAction) runEncoder(ctx context.Context, encoder string, exportRate, bitrateKbps int, exportFormat, outputPath string, gainDB float64) error {
	switch encoder {
	case clipenc.NativeWAV:
		// WAV honours the resolved gain like every other format. It used to be the
		// one exception, writing captured samples verbatim, which meant an
		// operator with normalization or Export.Gain configured silently got
		// neither. (A bat/ultrasonic capture above the analysis rate is stored
		// losslessly in Export.UltrasonicType, which may be WAV.) wav.EncodePCM
		// gains a copy, so a.pcmData stays pristine for the spectrogram pre-render
		// job that reads it after the export.
		return wav.EncodePCM(ctx, &wav.Options{
			PCMData:    a.pcmData,
			OutputPath: outputPath,
			SampleRate: exportRate,
			BitDepth:   conf.BitDepth,
			GainDB:     gainDB,
		})

	case clipenc.NativeFLAC:
		// FLAC always encodes natively via go-flac; FFmpeg is never used for FLAC.
		// The detection's safe metadata is embedded as VORBIS_COMMENT tags.
		vendor := clipVendor(a.Settings)
		return flac.EncodePCM(ctx, &flac.Options{
			PCMData:    a.pcmData,
			OutputPath: outputPath,
			SampleRate: exportRate,
			Channels:   conf.NumChannels,
			BitDepth:   conf.BitDepth,
			GainDB:     gainDB,
			Tags:       buildClipTags(a.clipTags, a.modelName, a.modelVersion, vendor),
			Vendor:     vendor,
		})

	case clipenc.NativeAAC:
		return a.encodeClipNativeAAC(ctx, exportRate, bitrateKbps, outputPath, gainDB)

	case clipenc.NativeOpus:
		return a.encodeClipNativeOpus(ctx, exportRate, bitrateKbps, outputPath, gainDB)

	case clipenc.NativeMP3:
		return a.encodeClipNativeMP3(ctx, exportRate, bitrateKbps, outputPath, gainDB)

	default:
		return unsupportedExportFormatError(exportFormat)
	}
}

// encodeClipNativeAAC encodes the clip to AAC-LC in an MP4 (.m4a) container with
// go-aac and go-m4a. There is no FFmpeg fallback for AAC: a failure is returned
// and surfaced through the export failure log.
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
// with AAC, there is no FFmpeg fallback: a failure is surfaced.
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

// encodeClipNativeMP3 encodes the clip to CBR MP3 (.mp3) with go-mp3. As with
// AAC, there is no FFmpeg fallback: a failure is surfaced.
func (a *SaveAudioAction) encodeClipNativeMP3(ctx context.Context, exportRate, bitrateKbps int, outputPath string, gainDB float64) error {
	return mp3.EncodePCM(ctx, &mp3.Options{
		PCMData:     a.pcmData,
		OutputPath:  outputPath,
		SampleRate:  exportRate,
		Channels:    conf.NumChannels,
		BitDepth:    conf.BitDepth,
		BitrateKbps: bitrateKbps,
		GainDB:      gainDB,
	})
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

// formatRateKey identifies a format-and-rate decision by the two inputs
// that vary at runtime, so each distinct combination explains itself exactly
// once instead of the first one silencing the rest.
func formatRateKey(format string, rate int) string {
	return format + "@" + strconv.Itoa(rate)
}

// reasonFormatRateKey adds the decision's reason to formatRateKey, for guards
// whose site can fire for more than one cause. Without the reason in the key, an
// install that trips one cause would never report the other for the same format
// and rate, which is the exact silencing onceByKey exists to prevent.
func reasonFormatRateKey(reason, format string, rate int) string {
	return reason + "|" + formatRateKey(format, rate)
}

// resampleKey identifies a resampling attempt by the rate pair it converts
// between, which is what determines whether the attempt can succeed.
func resampleKey(srcRate, dstRate int) string {
	return strconv.Itoa(srcRate) + "->" + strconv.Itoa(dstRate)
}

// Reasons a clip was encoded without normalization. Named rather than inlined
// because each is now part of a log-flood guard key as well as a log field, so a
// typo in one of the two spellings would silently split or merge the guard.
const (
	normalizeSkipMeasureFailed     = "measurement_failed"
	normalizeSkipTargetsOutOfRange = "normalization_targets_out_of_native_range"
	normalizeSkipUnsupportedDepth  = "unsupported_bit_depth"
)

// Guards for the export-path warnings. Each fires on every single detection of
// an affected install, so without a guard they are a permanent log flood; the
// per-clip encoder and format fields carry the ongoing signal instead.
//
// None of the guarded lines carries detection_id. Under a guard the field would
// name only the first detection to trip the condition, reading as if the
// condition were specific to that clip when every one of these is a property of
// the configuration or the source, not of a detection.
//
//nolint:gochecknoglobals // process-lifetime log-flood guards
var (
	normalizeSkipLogged       onceByKey
	resampleFailureLogged     onceByKey
	clipPathExtFallbackLogged onceByKey
)

// resolveExportGainDB returns the gain in dB to apply to this clip. Every export
// path reads it: a native encoder applies the value itself. With normalization enabled and supported it is the
// measured EBU R128 gain, which intentionally replaces rather than compounds the
// static Export.Gain, matching the FFmpeg loudnorm behaviour this replaced, where
// an enabled loudnorm filter also superseded the volume filter. Otherwise it is
// the static Export.Gain.
//
// Note that loudnorm normalization applies a single linear gain to reach the target
// integrated loudness under the true-peak ceiling. It does not consume
// Normalization.LoudnessRange, so no export format gets LRA/dynamic-range
// treatment any more.
//
// That is deliberate. On a clip built to force FFmpeg out of linear mode
// (measured LRA 7.40 against a 7.0 target) loudnorm's dynamic mode compressed
// LRA only 7.4 to 7.1 while missing the loudness target by 1.1 LU, where the
// linear gain landed within 0.02 LU. These figures were measured with a
// comparison harness that is no longer in the repository.
//
// Scope that evidence honestly: it was gathered on clips around the 15 s default
// export length. Extended capture can export far longer clips
// (conf.MaxExtendedCaptureDuration is 1200 s), where dynamic-range treatment
// would matter more, and those measurements do not cover that.
func (a *SaveAudioAction) resolveExportGainDB(ctx context.Context, exportRate int, format string) (gainDB float64, normalized bool, err error) {
	exportSettings := &a.Settings.Realtime.Audio.Export
	if !exportSettings.Normalization.Enabled {
		return exportSettings.Gain, false, nil
	}

	depthSupported := conf.BitDepth == nativeNormalizationBitDepth
	if depthSupported && loudnorm.ValidateTargets(exportSettings.Normalization.TargetLUFS, exportSettings.Normalization.TruePeak) == nil {
		planned, err := a.planNativeNormalizationGain(ctx, exportRate, format,
			exportSettings.Normalization.TargetLUFS, exportSettings.Normalization.TruePeak)
		switch {
		case err == nil:
			return planned, true, nil
		case ctx.Err() != nil:
			// The caller is going away; do not paper over that with a fallback.
			//
			// Joining the context error in is not the ctx-based misclassification
			// logExportFailure warns about, it is the opposite: on THIS branch the
			// measurement error alone is not fatal, because the code below would
			// have degraded to the static gain and finished the export. The clip
			// fails only because the context is done, so cancellation genuinely is
			// the cause and the joined error says so. The planner returns a
			// cancellation unwrapped, but it can also fail for another reason
			// (for example a sub-8 kHz source) just before the context is done;
			// that error need not wrap ctx.Err(), so without the join a normal
			// shutdown here would be reported as a WARN failure. The join is
			// skipped when the error already carries the cancellation.
			if ctxErr := ctx.Err(); !errors.Is(err, ctxErr) {
				err = errors.Join(err, ctxErr)
			}
			return 0, false, err
		}
		// The clip could not be measured (loudnorm rejects the dimensions, e.g. a
		// source reporting a sample rate below loudnorm.MinSampleRate). Losing
		// normalization is bad; losing the recording is worse, and every other
		// unmeasurable case below already degrades to the static gain. Before this
		// path stopped using loudnorm, FFmpeg would still have produced a clip
		// here, so failing hard would be a new way to lose audio on the default
		// MP3 install.
		// Guarded per (format, rate): loudnorm rejects the clip's dimensions, and
		// those come from the source, not from this detection, so the failure
		// repeats for every clip that source produces. The error text can vary
		// within a rate pair but the cause does not, so it is not part of the key.
		normalizeSkipLogged.do(reasonFormatRateKey(normalizeSkipMeasureFailed, format, exportRate), func() {
			GetLogger().Warn("Loudness measurement failed; encoding without normalization",
				logger.String("component", "analysis.processor.actions"),
				logger.String("format", format),
				logger.Int("sample_rate", exportRate),
				logger.Error(err),
				logger.String("reason", normalizeSkipMeasureFailed),
				logger.String("operation", "audio_export_normalize_skip"))
		})
		return exportSettings.Gain, false, nil
	}

	// Unreachable for a validated config: capture is 16-bit (conf.BitDepth) and
	// settings validation REJECTS loudness targets outside loudnorm's range at
	// startup rather than clamping them, so a running instance cannot hold one.
	// Defense-in-depth for unvalidated/legacy settings only. There is no longer a
	// loudnorm fallback on any path, FFmpeg included, so encode with the static
	// gain and surface the skipped normalization at WARN.
	reason := normalizeSkipTargetsOutOfRange
	if !depthSupported {
		reason = normalizeSkipUnsupportedDepth
	}
	// The reason is part of the key, unlike the measurement-failure guard above.
	// The bit-depth arm is decided by a build constant, but the targets arm is
	// decided by hot-reloadable normalization settings, so a format+rate key
	// would let whichever arm fired first silence the other for good.
	//
	// sample_rate is logged here as well: both sites carry the same operation
	// tag, and emitting divergent field sets under one tag makes the tag lie
	// about being one thing.
	normalizeSkipLogged.do(reasonFormatRateKey(reason, format, exportRate), func() {
		GetLogger().Warn("Native normalization skipped; encoding without normalization",
			logger.String("component", "analysis.processor.actions"),
			logger.String("format", format),
			logger.Int("sample_rate", exportRate),
			logger.String("reason", reason),
			logger.String("operation", "audio_export_normalize_skip"))
	})
	return exportSettings.Gain, false, nil
}

// nativeNormalizationBitDepth is the only PCM bit depth the native gain and
// normalization path handles: loudnorm's byte planner (loudnorm.PlanBytes) and
// gain's byte paths (gain.Gain.ApplyBytes, ApplyBytesTo, AppliedBytes) both
// operate on int16 samples. The constraint belongs to those helpers rather than
// to any one codec, so every native encoder shares it.
const nativeNormalizationBitDepth = 16

// planNativeNormalizationGain measures the clip's EBU R128 integrated loudness and
// true peak with loudnorm.PlanBytes and returns the single linear gain (dB) that
// brings it to targetLUFS without its true peak exceeding truePeakDBTP, clamped
// to +/-loudness.MaxClipGainDB. A clip under the R128 absolute gate is lifted by
// its true-peak headroom instead (see loudnorm.GateFallbackGainDB); genuine
// silence yields 0. Cancellation is checked while measuring and returned as is.
// The gain is applied by the encoder; a.pcmData is not modified here.
func (a *SaveAudioAction) planNativeNormalizationGain(ctx context.Context, sampleRate int, format string, targetLUFS, truePeakDBTP float64) (float64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	// PlanBytes decodes the PCM bytes in chunks and only reads them; a.pcmData is
	// not mutated.
	plan, err := loudnorm.PlanBytes(ctx, a.pcmData,
		loudness.ClipPlanOptions(sampleRate, conf.NumChannels, targetLUFS, truePeakDBTP))
	if err != nil {
		return 0, errors.New(err).
			Component("analysis.processor").
			Category(errors.CategoryAudio).
			Context("operation", "native_normalize_measure").
			Context("format", format).
			Context("detection_id", a.CorrelationID).
			Build()
	}
	gainDB := plan.GainDB

	GetLogger().Debug("Native loudness analysis (detection save)",
		logger.String("format", format),
		logger.Float64("measured_lufs", plan.Result.Input.IntegratedLUFS),
		logger.Float64("true_peak_dbtp", plan.Result.Input.TruePeakDBTP),
		logger.Float64("target_lufs", targetLUFS),
		logger.Float64("gain_db", gainDB),
		logger.Bool("peak_limited", plan.PeakLimited),
		logger.Bool("gate_lifted", plan.GateLifted),
		logger.Bool("gain_clamped", plan.Clamped),
		logger.String("detection_id", a.CorrelationID))
	return gainDB, nil
}

// resolveExportFormat is the naming rule for the container/codec a clip is
// stored in. A bat/ultrasonic capture above the analysis rate is stored
// losslessly in Export.UltrasonicType (WAV/FLAC, default FLAC) at full source
// rate; every other case (including a non-bat capture above the analysis rate,
// which resolveExportParams downsamples to conf.SampleRate) uses the configured
// Export.Type. applyExportFormatExtension names clips by it, and it is the
// encoder's fallback for a ClipName with no known extension (resolveExportParams);
// otherwise the encoder follows the stored name. Pass the ORIGINAL source rate
// (pre-downsample).
func resolveExportFormat(isBat bool, sourceRate int, export *conf.ExportSettings) string {
	if isBat && sourceRate > conf.SampleRate {
		return export.UltrasonicType
	}
	return export.Type
}

// isBatModel reports whether the model name and version identify a bat model. The
// export format, the planned clip rate and the encoder all use it, always with the
// name paired with its version, so they cannot disagree.
func isBatModel(name, version string) bool {
	return detection.ResolveModelType(name, version) == entities.ModelTypeBat
}

// exportFormatForModel resolves the export container for a clip from its model
// identity and source rate, encapsulating the bat/ultrasonic model-type check so
// the clip-name path (applyExportFormatExtension) picks the format the name
// records without duplicating the ResolveModelType call. The encoder follows the
// stored name (resolveExportParams).
func exportFormatForModel(modelName, modelVersion string, sourceRate int, export *conf.ExportSettings) string {
	isBat := isBatModel(modelName, modelVersion)
	return resolveExportFormat(isBat, sourceRate, export)
}

// exportClipRate returns the sample rate a clip is written at, given whether it
// is a bat capture, its ORIGINAL source rate and the resolved export format. It is
// the single rule behind both the encoder (resolveExportParams) and the rate stored
// with the detection (plannedClipSampleRate), so the two cannot drift. Bat audio
// above the analysis rate keeps its full rate; other audio above it is downsampled
// to conf.SampleRate; audio below it is raised to conf.SampleRate only when the
// native encoder for format cannot carry the source rate.
func exportClipRate(isBat bool, sourceRate int, format string) int {
	switch {
	case sourceRate > conf.SampleRate && !isBat:
		return conf.SampleRate
	case sourceRate < conf.SampleRate && nativeEncoderNeedsUpsample(sourceRate, format):
		return conf.SampleRate
	default:
		return sourceRate
	}
}

// exportContainer returns the container a clip is written in. The stored name
// decides: when its extension names a known export format, that format is used
// (fromName is true). Only a name with no known extension falls back to the
// settings through resolveExportFormat. The planner and the encoder both use it,
// so a settings reload between naming and encoding cannot split them.
func exportContainer(clipName string, isBat bool, sourceRate int, export *conf.ExportSettings) (format string, fromName bool) {
	format, fromName = audioformat.FormatForExtension(strings.TrimPrefix(filepath.Ext(clipName), "."))
	if !fromName {
		format = resolveExportFormat(isBat, sourceRate, export)
	}
	return format, fromName
}

// plannedClipSampleRate returns the sample rate the exporter is expected to write
// the detection's clip at, or 0 when the result has no clip. It applies the same
// rule as resolveExportParams to the result's source rate (defaulting to
// conf.SampleRate like the exporter), taking the container from the clip name
// (exportContainer). The stored value is corrected after the encode when the
// written rate differs (a resample failure, or a name with no known extension
// whose settings-derived format changed since planning).
func plannedClipSampleRate(result *detection.Result, export *conf.ExportSettings) int {
	if result.ClipName == "" {
		return 0
	}
	rate := result.AudioSource.SampleRate
	if rate <= 0 {
		rate = conf.SampleRate
	}
	isBat := isBatModel(result.Model.Name, result.Model.Version)
	format, _ := exportContainer(result.ClipName, isBat, rate, export)
	return exportClipRate(isBat, rate, format)
}

// resolveExportParams determines the export sample rate, format, and output
// path. The stored ClipName decides the container: when outputPath's extension
// names a known export format, that format is used and the path is returned
// unchanged, so the file lands exactly where the DB row, SSE and MQTT say it is.
// Settings can be hot-reloaded between naming and flush, so re-deriving the
// format from them here would split the file from its stored name. Only a name
// with no known extension falls back to resolveExportFormat and gets its
// extension rewritten to match.
//
// Bat/ultrasonic audio above the analysis rate keeps its full source rate: its
// name records UltrasonicType (WAV or FLAC), and the lossy formats never carry
// ultrasonic content. Non-bat audio above the analysis rate is downsampled to it.
//
// Audio below the analysis rate whose native lossy encoder (AAC, MP3 or Opus)
// cannot carry the source rate is resampled UP to the analysis rate, which every
// native lossy encoder accepts. That holds for every clip, bat-model clips below
// the analysis rate included, and whether or not FFmpeg is installed: the lossy
// formats never use FFmpeg and are never downgraded to WAV. If a resample cannot
// run (an unusable source rate), the clip stays at that rate and the encoder
// rejects it, failing the export through the normal failure path. A trailing
// partial sample is trimmed from the PCM first for every format, resample or not.
// The target rate comes from exportClipRate, the rule shared with the rate stored at save time.
func (a *SaveAudioAction) resolveExportParams(outputPath string) (rate int, format, path string) {
	rate = a.sourceSampleRate
	if rate <= 0 {
		rate = conf.SampleRate
	}

	export := &a.Settings.Realtime.Audio.Export
	path = outputPath

	// Pair modelName with modelVersion (matching the naming path's
	// applyExportFormatExtension) so the encode-side isBat check cannot skew from
	// the clip-name side for a future version-specific model type.
	isBat := isBatModel(a.modelName, a.modelVersion)

	// The stored name decides the container. Settings decide only for a name with
	// no known extension (an older row or a manual rename), from the ORIGINAL
	// source rate, before the resample below can mutate it.
	format, fromName := exportContainer(outputPath, isBat, rate, export)

	// A trailing partial sample is trimmed for every format before anything else,
	// whether or not a resample follows: the resampler, the WAV writer, go-flac and
	// every lossy encoder reject a buffer that is not a whole number of frames, and
	// with no fallback format that would cost the clip. Dropping under one sample
	// is inaudible.
	a.pcmData = trimPartialSample(a.pcmData)

	// The target rate is exportClipRate, shared with the rate stored at save time.
	if target := exportClipRate(isBat, rate, format); target != rate {
		rate = a.resampleExportTo(rate, target, format)
	}

	// Only a name with no known extension takes the format's extension. Guard
	// against an empty extension (only reachable with an unvalidated empty format)
	// so the path keeps buildClipPath's own extension rather than being clobbered
	// to a trailing dot (GitHub #2810/#2814).
	if !fromName {
		if ext := audioformat.FileExtension(format); ext != "" {
			path = replaceExtension(path, "."+ext)
		}
	}

	return rate, format, path
}

// correctClipSampleRate rewrites the sample rate stored with the detection when
// the clip was written at a different rate than the one planned at save time
// (a resample failure keeps the source rate, and a name with no known extension
// takes its format from the settings at encode time). It does nothing without a repository
// or a saved note, and a write error is logged without failing the export. The write
// has its own clipRateCorrectionTimeout bound derived from ctx, so a stalled write
// cannot run out the job's execution context after the clip is already on disk.
func (a *SaveAudioAction) correctClipSampleRate(ctx context.Context, exportRate int) {
	if a.clipRates == nil || a.NoteID == 0 || exportRate == a.plannedRate {
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, clipRateCorrectionTimeout)
	defer cancel()
	if err := a.clipRates.RecordClipSampleRate(writeCtx, a.NoteID, a.ClipName, exportRate); err != nil {
		GetLogger().Warn("Failed to correct the stored clip sample rate",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.Any("note_id", a.NoteID),
			logger.Int("planned_rate", a.plannedRate),
			logger.Int("export_rate", exportRate),
			logger.Error(err),
			logger.String("operation", "clip_sample_rate_correct"))
	}
}

// resampleExportTo converts the captured PCM from srcRate to dstRate for export,
// updating the action's PCM buffer and source rate on success and returning the
// rate the clip is now at. On failure it logs once per rate pair and leaves the
// clip at srcRate; for the lossy formats the encoder then rejects that rate and
// the export fails.
//
// The failure log is keyed per rate pair because resampling a fixed pair either
// works or it does not, so an affected source would otherwise warn on every
// detection forever; keying by the pair (rather than assuming a fixed target) keeps
// a future variable target from silencing a new pair.
func (a *SaveAudioAction) resampleExportTo(srcRate, dstRate int, format string) int {
	resampled, err := resample.ResampleBytes(a.pcmData, srcRate, dstRate)
	if err != nil {
		resampleFailureLogged.do(resampleKey(srcRate, dstRate), func() {
			GetLogger().Warn("Resampling failed, leaving the clip at its source rate",
				logger.String("component", "analysis.processor.actions"),
				logger.Int("source_rate", srcRate),
				logger.Int("target_rate", dstRate),
				logger.String("format", format),
				logger.Error(err),
				logger.String("operation", "audio_export_resample"))
		})
		return srcRate
	}
	a.pcmData = resampled
	a.sourceSampleRate = dstRate
	return dstRate
}

// trimPartialSample drops a trailing partial frame (bytes that do not fill one
// sample of conf.BitDepth bits for every one of conf.NumChannels channels) from
// interleaved PCM.
func trimPartialSample(pcm []byte) []byte {
	const bitsPerByte = 8
	bytesPerFrame := conf.BitDepth / bitsPerByte * conf.NumChannels
	return pcm[:len(pcm)-len(pcm)%bytesPerFrame]
}

// nativeEncoderNeedsUpsample reports whether a sub-48k clip must be resampled up to
// conf.SampleRate to be encodable in its configured lossy format: the format's
// native encoder does not accept the source rate, and 48kHz (accepted by every
// native lossy encoder) does. FfmpegPath and the bat model type are ignored on
// purpose: the lossy formats never use FFmpeg, and a bat-model clip below the
// analysis rate is upsampled like any other (one above it never reaches here: it
// is kept at full rate in its lossless named format).
//
// In this pipeline bit depth and channels are fixed (conf.BitDepth,
// conf.NumChannels), so the only thing Supports rejects here is the sample rate.
func nativeEncoderNeedsUpsample(rate int, format string) bool {
	return !transcode.EncoderAcceptsRate(format, rate, conf.BitDepth, conf.NumChannels)
}

// replaceExtension swaps the file extension on path (e.g. ".mp3" -> ".wav").
func replaceExtension(path, newExt string) string {
	ext := filepath.Ext(path)
	if ext == "" {
		return path + newExt
	}
	return path[:len(path)-len(ext)] + newExt
}
