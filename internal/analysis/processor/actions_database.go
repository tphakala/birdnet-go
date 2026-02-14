// processor/actions_database.go
// This file contains database and logging action implementations.

package processor

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/myaudio"
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
	if a.NewSpeciesTracker != nil {
		// Use atomic check-and-update to prevent duplicate "new species" notifications
		// when multiple detections of the same species arrive concurrently
		isNewSpecies, daysSinceFirstSeen = a.NewSpeciesTracker.CheckAndUpdateSpecies(a.Result.Species.ScientificName, a.Result.BeginTime)
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
		// Note: a.Result.ID is updated by Repo.Save()
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
	// This must happen before audio export so downstream actions get the ID
	// even if audio export fails.
	if a.DetectionCtx != nil {
		a.DetectionCtx.NoteID.Store(uint64(a.Result.ID))
	}

	// After successful save, publish detection event for new species
	a.publishNewSpeciesDetectionEvent(isNewSpecies, daysSinceFirstSeen)

	// Save audio clip to file if enabled.
	// IMPORTANT: Audio export errors are logged but NOT returned.
	// This allows downstream actions (SSE, MQTT) to proceed with the detection.
	// The detection record is valuable even without audio - users integrating with
	// Home Assistant want the detection event regardless of audio export status.
	if a.Settings.Realtime.Audio.Export.Enabled {
		captureLength := a.Settings.Realtime.Audio.Export.Length

		// debug log note begin, end and capture length
		GetLogger().Debug("Saving detection audio clip",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.Time("begin_time", a.Result.BeginTime),
			logger.Time("end_time", a.Result.EndTime),
			logger.Int("capture_length", captureLength),
			logger.String("operation", "note_begin_end_capture_length"))

		// handleAudioExportError logs the error and signals downstream actions.
		// This helper reduces duplication between buffer read and save failures.
		handleAudioExportError := func(err error, extraFields ...logger.Field) {
			fields := make([]logger.Field, 0, 5+len(extraFields))
			fields = append(fields,
				logger.String("component", "analysis.processor.actions"),
				logger.String("detection_id", a.CorrelationID),
				logger.Error(err),
				logger.String("species", a.Result.Species.CommonName),
				logger.String("operation", "audio_export_non_fatal"),
			)
			fields = append(fields, extraFields...)
			GetLogger().Error("Audio export failed (continuing with detection broadcast)", fields...)

			// Signal to downstream actions that audio export failed
			// This prevents SSEAction from waiting 5 seconds for a file that won't appear
			if a.DetectionCtx != nil {
				a.DetectionCtx.AudioExportFailed.Store(true)
			}
		}

		// export audio clip from capture buffer
		pcmData, err := myaudio.ReadSegmentFromCaptureBuffer(a.Result.AudioSource.ID, a.Result.BeginTime, captureLength)
		if err != nil {
			handleAudioExportError(err,
				logger.String("source", a.Result.AudioSource.SafeString),
				logger.Time("begin_time", a.Result.BeginTime),
				logger.Int("duration_seconds", captureLength))
		} else {
			// Create a SaveAudioAction and execute it
			saveAudioAction := &SaveAudioAction{
				Settings:      a.Settings,
				ClipName:      a.Result.ClipName,
				pcmData:       pcmData,
				NoteID:        a.Result.ID,
				PreRenderer:   a.PreRenderer,
				CorrelationID: a.CorrelationID,
			}

			if err := saveAudioAction.Execute(ctx, nil); err != nil {
				handleAudioExportError(err, logger.String("clip_name", a.Result.ClipName))
			} else if a.Settings.Debug {
				// Add structured logging
				GetLogger().Debug("Saved audio clip successfully",
					logger.String("component", "analysis.processor.actions"),
					logger.String("detection_id", a.CorrelationID),
					logger.String("species", a.Result.Species.CommonName),
					logger.String("clip_name", a.Result.ClipName),
					logger.String("detection_time", a.Result.Time()),
					logger.Time("begin_time", a.Result.BeginTime),
					logger.Time("end_time", time.Now()),
					logger.String("operation", "save_audio_clip_debug"))
			}
		}
	}

	return nil
}

// isEOFError checks if an error is an EOF error using both precise matching and string fallback
func isEOFError(err error) bool {
	if err == nil {
		return false
	}
	// Check for specific EOF errors first
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	// Fall back to string matching for wrapped or custom EOF errors
	return strings.Contains(strings.ToLower(err.Error()), "eof")
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
func (a *DatabaseAction) populateEventMetadata(detectionEvent events.DetectionEvent) {
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

	if a.processor != nil && a.processor.BirdImageCache != nil {
		if birdImage, err := a.processor.BirdImageCache.Get(a.Result.Species.ScientificName); err == nil && birdImage.URL != "" {
			metadata["image_url"] = birdImage.URL
		}
	}
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

// publishNewSpeciesDetectionEvent publishes a detection event for new species.
// This helper method orchestrates notification suppression, event creation, and publishing.
func (a *DatabaseAction) publishNewSpeciesDetectionEvent(isNewSpecies bool, daysSinceFirstSeen int) {
	if !isNewSpecies || !events.IsInitialized() {
		return
	}

	suppress, notificationTime := a.shouldSuppressNewSpeciesNotification()
	if suppress {
		return
	}

	eventBus := events.GetEventBus()
	if eventBus == nil {
		return
	}

	detectionEvent := a.createDetectionEvent(isNewSpecies, daysSinceFirstSeen)
	if detectionEvent == nil {
		return
	}

	a.populateEventMetadata(detectionEvent)

	if published := eventBus.TryPublishDetection(detectionEvent); published {
		a.recordNotificationSent(notificationTime)
	}
}

// Execute saves the audio clip to a file
func (a *SaveAudioAction) Execute(_ context.Context, _ any) error {
	// Get the full path by joining the export path with the relative clip name
	outputPath := filepath.Join(a.Settings.Realtime.Audio.Export.Path, a.ClipName)

	// Ensure the directory exists
	// Note: Errors are logged by the caller (handleAudioExportError) with full context
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o750); err != nil {
		return err
	}

	if a.Settings.Realtime.Audio.Export.Type == "wav" {
		if err := myaudio.SavePCMDataToWAV(outputPath, a.pcmData); err != nil {
			return err
		}
	} else {
		if err := myaudio.ExportAudioWithFFmpeg(a.pcmData, outputPath, &a.Settings.Realtime.Audio); err != nil {
			return err
		}
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
		logger.String("clip_path", a.ClipName),
		logger.Int64("file_size_bytes", fileSize),
		logger.String("format", a.Settings.Realtime.Audio.Export.Type),
		logger.String("operation", "audio_export_success"))

	// Submit for pre-rendering if enabled
	if a.Settings.Realtime.Dashboard.Spectrogram.Enabled && a.PreRenderer != nil {
		// Create pre-render job using local DTO (avoids direct spectrogram dependency)
		job := PreRenderJob{
			PCMData:   a.pcmData,
			ClipPath:  outputPath, // Use full path to audio file
			NoteID:    a.NoteID,
			Timestamp: time.Now(),
		}

		// Non-blocking submission - errors logged but don't fail action
		if err := a.PreRenderer.Submit(job); err != nil {
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
