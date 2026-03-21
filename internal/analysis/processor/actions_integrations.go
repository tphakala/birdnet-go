// processor/actions_integrations.go
// This file contains external service integration action implementations.

package processor

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tphakala/birdnet-go/internal/alerting"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/mqtt"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// NoteWithBirdImage wraps a Note with bird image data for MQTT publishing.
// The SourceID field enables Home Assistant to filter detections by source.
//
// IMPORTANT: JSON field names are part of the public MQTT API contract.
// Changing them breaks existing Home Assistant and other MQTT integrations.
// See: https://github.com/tphakala/birdnet-go/discussions/1759
type NoteWithBirdImage struct {
	datastore.Note
	DetectionID uint                    `json:"detectionId"` // Database ID for URL construction (e.g., /api/v2/audio/{id})
	SourceID    string                  `json:"sourceId"`    // Audio source ID for HA filtering (added for HA discovery)
	BirdImage   imageprovider.BirdImage `json:"BirdImage"`   // PascalCase for backward compatibility - DO NOT CHANGE
}

// Execute sends the note to the BirdWeather API
func (a *BirdWeatherAction) Execute(_ context.Context, data any) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Check event frequency (supports scientific name lookup)
	if !a.EventTracker.TrackEventWithNames(a.Result.Species.CommonName, a.Result.Species.ScientificName, BirdWeatherSubmit) {
		return nil
	}

	// Early check if BirdWeather is still enabled in settings
	if !a.Settings.Realtime.Birdweather.Enabled {
		return nil // Silently exit if BirdWeather was disabled after this action was created
	}

	// Add threshold check here
	if a.Result.Confidence < float64(a.Settings.Realtime.Birdweather.Threshold) {
		if a.Settings.Debug {
			// Add structured logging
			GetLogger().Debug("Skipping BirdWeather upload due to low confidence",
				logger.String("component", "analysis.processor.actions"),
				logger.String("detection_id", a.CorrelationID),
				logger.String("species", a.Result.Species.CommonName),
				logger.Float64("confidence", a.Result.Confidence),
				logger.Float64("threshold", float64(a.Settings.Realtime.Birdweather.Threshold)),
				logger.String("operation", "birdweather_threshold_check"))
		}
		return nil
	}

	// Safe check for nil BwClient
	if a.BwClient == nil {
		// Client initialization failures indicate configuration issues that require
		// manual intervention (e.g., missing API keys, disabled service)
		// Retrying won't fix these problems, so mark as non-retryable
		return errors.Newf("BirdWeather client is not initialized").
			Component("analysis.processor").
			Category(errors.CategoryIntegration).
			Context("operation", "birdweather_upload").
			Context("integration", "birdweather").
			Context("retryable", false). // Configuration error - not retryable
			Context("config_section", "realtime.birdweather").
			Build()
	}

	// Convert Result to Note for BirdWeather API (backward compatible)
	note := datastore.NoteFromResult(&a.Result)
	pcmData := a.pcmData

	// Try to publish with appropriate error handling
	if err := a.BwClient.Publish(&note, pcmData); err != nil {
		// Check if this is a CategoryNotFound error (e.g., species not recognized by Birdweather)
		// These are expected for non-bird species and should not be logged at error level
		if errors.IsNotFound(err) {
			// Log at debug level for expected validation failures (unknown species)
			GetLogger().Debug("BirdWeather upload skipped: species not recognized",
				logger.String("component", "analysis.processor.actions"),
				logger.String("detection_id", a.CorrelationID),
				logger.String("species", a.Result.Species.CommonName),
				logger.String("scientific_name", a.Result.Species.ScientificName),
				logger.String("operation", "birdweather_upload"))
			// Return nil - this is not an error condition worth retrying
			return nil
		}

		// Sanitize error before logging (only for actual errors, not expected conditions)
		sanitizedErr := privacy.WrapError(err)

		// Add structured logging for actual errors
		GetLogger().Error("Failed to upload to BirdWeather",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.Error(sanitizedErr),
			logger.String("species", a.Result.Species.CommonName),
			logger.String("scientific_name", a.Result.Species.ScientificName),
			logger.Float64("confidence", a.Result.Confidence),
			logger.String("clip_name", a.Result.ClipName),
			logger.Bool("retry_enabled", a.RetryConfig.Enabled),
			logger.String("operation", "birdweather_upload"))
		// BirdWeather failures are handled by the alerting rule engine
		// (integration.birdweather_failed), so no explicit notification here.
		// Network and API errors are typically transient and may succeed on retry:
		// - Temporary network outages
		// - API rate limiting
		// - Server-side temporary failures
		// The job queue will handle exponential backoff for these retryable errors
		return errors.New(err).
			Component("analysis.processor").
			Category(errors.CategoryIntegration).
			Context("operation", "birdweather_upload").
			Context("species", a.Result.Species.CommonName).
			Context("confidence", a.Result.Confidence).
			Context("clip_name", a.Result.ClipName).
			Context("integration", "birdweather").
			Context("retryable", true). // Network/API errors are typically retryable
			Build()
	}

	if a.Settings.Debug {
		GetLogger().Debug("Successfully uploaded to BirdWeather",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.String("species", a.Result.Species.CommonName),
			logger.String("scientific_name", a.Result.Species.ScientificName),
			logger.Float64("confidence", a.Result.Confidence),
			logger.String("clip_name", a.Result.ClipName),
			logger.String("operation", "birdweather_upload_success"))
	}
	return nil
}

// Execute sends the note to the MQTT broker.
// Transient connection errors (EOF, not connected) are logged as warnings and
// do NOT fail the CompositeAction — the detection is already saved to the database.
// This eliminates the TOCTOU race at Layer 2 (GitHub #2397).
func (a *MqttAction) Execute(_ context.Context, data any) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// NOTE: We intentionally do NOT pre-check IsConnected() here.
	// The check-then-use pattern is the core of the TOCTOU race (GitHub #2397).
	// Instead, we attempt the publish directly and handle errors gracefully.
	// The Publish() method returns a clear error if the client is disconnected.

	// Check event frequency (supports scientific name lookup)
	if !a.EventTracker.TrackEventWithNames(a.Result.Species.CommonName, a.Result.Species.ScientificName, MQTTPublish) {
		return nil
	}

	// Early check if MQTT is still enabled in settings.
	// Supports hot-reload: disabling MQTT in the UI takes effect immediately,
	// even for actions already queued. Matches BirdWeatherAction pattern.
	if !a.Settings.Realtime.MQTT.Enabled {
		return nil
	}

	// Validate MQTT settings
	if a.Settings.Realtime.MQTT.Topic == "" {
		return errors.Newf("MQTT topic is not specified").
			Component("analysis.processor").
			Category(errors.CategoryConfiguration).
			Context("operation", "mqtt_publish").
			Context("integration", "mqtt").
			Context("retryable", false). // Configuration error - not retryable
			Context("config_section", "realtime.mqtt.topic").
			Build()
	}

	// Get bird image of detected bird using the shared helper
	birdImage := getBirdImageFromCache(a.BirdImageCache, a.Result.Species.ScientificName, a.Result.Species.CommonName, a.CorrelationID)

	// Get detection ID from shared context (set by DatabaseAction in CompositeAction sequence)
	var detectionID uint
	if a.DetectionCtx != nil {
		detectionID = uint(a.DetectionCtx.NoteID.Load())
	}

	// Convert Result to Note for JSON marshaling (backward compatible MQTT payload)
	note := datastore.NoteFromResult(&a.Result)

	// Update the Note's ID field for consistency in embedded JSON
	if detectionID > 0 {
		note.ID = detectionID
	}

	// Wrap note with bird image and include detection ID and SourceID
	noteWithBirdImage := NoteWithBirdImage{
		Note:        note,
		DetectionID: detectionID, // Explicit field for URL construction (e.g., /api/v2/audio/{id})
		SourceID:    note.Source.ID,
		BirdImage:   birdImage,
	}

	// Create a JSON representation of the note
	noteJson, err := json.Marshal(noteWithBirdImage)
	if err != nil {
		GetLogger().Error("Failed to marshal note to JSON",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.Error(err),
			logger.String("species", a.Result.Species.CommonName),
			logger.String("scientific_name", a.Result.Species.ScientificName),
			logger.String("operation", "json_marshal"))
		return err
	}

	// Create a context with timeout for publishing
	ctx, cancel := context.WithTimeout(context.Background(), MQTTPublishTimeout)
	defer cancel()

	// Publish the note to the MQTT broker.
	// The detection is already saved to the database (CompositeAction order: DB -> SSE -> MQTT).
	// Transient connection errors are non-fatal — we log a warning and return nil to avoid
	// failing the CompositeAction and generating noisy Sentry events (GitHub #2397).
	err = a.MqttClient.Publish(ctx, a.Settings.Realtime.MQTT.Topic, string(noteJson))
	if err != nil {
		sanitizedErr := privacy.WrapError(err)
		isConnErr := mqtt.IsTransientConnectionError(err)

		if isConnErr {
			// Transient connection error — detection is safe in DB, downgrade to warning.
			// This is the key fix for GitHub #2397: the TOCTOU race between IsConnected()
			// and Publish() produces these errors. Since the detection is persisted,
			// a missed MQTT notification is not data loss.
			GetLogger().Warn("MQTT publish skipped due to transient connection issue (detection saved to database)",
				logger.String("component", "analysis.processor.actions"),
				logger.String("detection_id", a.CorrelationID),
				logger.Error(sanitizedErr),
				logger.String("species", a.Result.Species.CommonName),
				logger.Float64("confidence", a.Result.Confidence),
				logger.String("topic", a.Settings.Realtime.MQTT.Topic),
				logger.String("operation", "mqtt_publish_transient_skip"))
			return nil // Non-fatal: don't fail the CompositeAction
		}

		// Non-transient error (config issue, JSON error, etc.) — this is a real problem
		GetLogger().Error("Failed to publish to MQTT",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.Error(sanitizedErr),
			logger.String("species", a.Result.Species.CommonName),
			logger.String("scientific_name", a.Result.Species.ScientificName),
			logger.Float64("confidence", a.Result.Confidence),
			logger.String("clip_name", a.Result.ClipName),
			logger.String("topic", a.Settings.Realtime.MQTT.Topic),
			logger.Bool("retry_enabled", a.RetryConfig.Enabled),
			logger.String("operation", "mqtt_publish"))

		alerting.TryPublish(&alerting.AlertEvent{
			ObjectType: alerting.ObjectTypeIntegration,
			EventName:  alerting.EventMQTTPublishFailed,
			Properties: map[string]any{
				alerting.PropertyBroker: a.Settings.Realtime.MQTT.Broker,
				alerting.PropertyError:  sanitizedErr.Error(),
			},
		})

		return errors.New(err).
			Component("analysis.processor").
			Category(errors.CategoryMQTTPublish).
			Context("operation", "mqtt_publish").
			Context("species", a.Result.Species.CommonName).
			Context("confidence", a.Result.Confidence).
			Context("topic", a.Settings.Realtime.MQTT.Topic).
			Context("clip_name", a.Result.ClipName).
			Context("integration", "mqtt").
			Context("retryable", true).
			Build()
	}

	if a.Settings.Debug {
		GetLogger().Debug("Successfully published to MQTT",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.String("species", a.Result.Species.CommonName),
			logger.String("scientific_name", a.Result.Species.ScientificName),
			logger.Float64("confidence", a.Result.Confidence),
			logger.String("topic", a.Settings.Realtime.MQTT.Topic),
			logger.String("operation", "mqtt_publish_success"))
	}
	return nil
}

// Execute updates the range filter species list, this is run every day
// Note: The ShouldUpdateRangeFilterToday() check in processor.go ensures this action
// is only created once per day, preventing duplicate concurrent updates (GitHub issue #1357)
func (a *UpdateRangeFilterAction) Execute(_ context.Context, data any) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Get current date for the range filter calculation
	today := time.Now().Truncate(24 * time.Hour)

	// Update location based species list
	speciesScores, err := a.Bn.GetProbableSpecies(today, 0.0)
	if err != nil {
		// Reset the update flag to allow retry on next detection
		// This prevents the issue where a failed update would block retries until tomorrow
		a.Settings.ResetRangeFilterUpdateFlag()

		GetLogger().Error("Failed to get probable species for range filter",
			logger.Error(err),
			logger.String("date", today.Format(time.DateOnly)),
			logger.String("operation", "update_range_filter"))
		return err
	}

	// Convert the speciesScores slice to a slice of species labels
	includedSpecies := make([]string, 0, len(speciesScores))
	for _, speciesScore := range speciesScores {
		includedSpecies = append(includedSpecies, speciesScore.Label)
	}

	// Update the species list (this also updates LastUpdated timestamp atomically)
	a.Settings.UpdateIncludedSpecies(includedSpecies)

	if a.Settings.Debug {
		GetLogger().Info("Range filter updated successfully",
			logger.Int("species_count", len(includedSpecies)),
			logger.String("date", today.Format(time.DateOnly)),
			logger.String("operation", "update_range_filter_success"))
	}

	return nil
}

// Execute broadcasts the detection via Server-Sent Events
func (a *SSEAction) Execute(_ context.Context, data any) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Check if SSE broadcaster is available
	if a.SSEBroadcaster == nil {
		return nil // Silently skip if no broadcaster is configured
	}

	// Check event frequency (supports scientific name lookup)
	if !a.EventTracker.TrackEventWithNames(a.Result.Species.CommonName, a.Result.Species.ScientificName, SSEBroadcast) {
		return nil
	}

	// Get detection ID from shared context (set by DatabaseAction in CompositeAction sequence).
	// DetectionContext provides the database ID without polling via atomic.Uint64.
	var noteID uint
	if a.DetectionCtx != nil {
		noteID = uint(a.DetectionCtx.NoteID.Load())
		if noteID > 0 {
			a.Result.ID = noteID
		}
	}

	// Get bird image of detected bird using the shared helper
	birdImage := getBirdImageFromCache(a.BirdImageCache, a.Result.Species.ScientificName, a.Result.Species.CommonName, a.CorrelationID)

	// Convert Result to Note for SSEBroadcaster (backward compatible SSE payload)
	note := datastore.NoteFromResult(&a.Result)

	// Broadcast the detection with error handling
	if err := a.SSEBroadcaster(&note, &birdImage); err != nil {
		// Log the error with retry information if retries are enabled
		// Sanitize error before logging
		sanitizedErr := privacy.WrapError(err)
		GetLogger().Error("Failed to broadcast via SSE",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.Error(sanitizedErr),
			logger.String("species", a.Result.Species.CommonName),
			logger.String("scientific_name", a.Result.Species.ScientificName),
			logger.Float64("confidence", a.Result.Confidence),
			logger.String("clip_name", a.Result.ClipName),
			logger.Bool("retry_enabled", a.RetryConfig.Enabled),
			logger.String("operation", "sse_broadcast"))
		return errors.New(err).
			Component("analysis.processor").
			Category(errors.CategoryBroadcast).
			Context("operation", "sse_broadcast").
			Context("species", a.Result.Species.CommonName).
			Context("confidence", a.Result.Confidence).
			Context("clip_name", a.Result.ClipName).
			Context("retryable", true). // SSE broadcast failures are typically retryable
			Build()
	}

	if a.Settings.Debug {
		GetLogger().Debug("Successfully broadcasted via SSE",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.String("species", a.Result.Species.CommonName),
			logger.String("scientific_name", a.Result.Species.ScientificName),
			logger.Float64("confidence", a.Result.Confidence),
			logger.String("clip_name", a.Result.ClipName),
			logger.String("operation", "sse_broadcast_success"))
	}

	return nil
}
