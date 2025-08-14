// processor/actions.go

package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/birdweather"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/mqtt"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/observation"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

type Action interface {
	Execute(data interface{}) error
	GetDescription() string
}

type LogAction struct {
	Settings     *conf.Settings
	Note         datastore.Note
	EventTracker *EventTracker
	Description  string
	mu           sync.Mutex // Protect concurrent access to Note
}

type DatabaseAction struct {
	Settings          *conf.Settings
	Ds                datastore.Interface
	Note              datastore.Note
	Results           []datastore.Results
	EventTracker      *EventTracker
	NewSpeciesTracker *NewSpeciesTracker // Add reference to new species tracker
	Description       string
	mu                sync.Mutex // Protect concurrent access to Note and Results
}

type SaveAudioAction struct {
	Settings     *conf.Settings
	ClipName     string
	pcmData      []byte
	EventTracker *EventTracker
	Description  string
	mu           sync.Mutex // Protect concurrent access to pcmData
}

type BirdWeatherAction struct {
	Settings     *conf.Settings
	Note         datastore.Note
	pcmData      []byte
	BwClient     *birdweather.BwClient
	EventTracker *EventTracker
	RetryConfig  jobqueue.RetryConfig // Configuration for retry behavior
	Description  string
	mu           sync.Mutex // Protect concurrent access to Note and pcmData
}

type MqttAction struct {
	Settings       *conf.Settings
	Note           datastore.Note
	BirdImageCache *imageprovider.BirdImageCache
	MqttClient     mqtt.Client
	EventTracker   *EventTracker
	RetryConfig    jobqueue.RetryConfig // Configuration for retry behavior
	Description    string
	mu             sync.Mutex // Protect concurrent access to Note
}

type UpdateRangeFilterAction struct {
	Bn          *birdnet.BirdNET
	Settings    *conf.Settings
	Description string
	mu          sync.Mutex // Protect concurrent access to Settings
}

type SSEAction struct {
	Settings       *conf.Settings
	Note           datastore.Note
	BirdImageCache *imageprovider.BirdImageCache
	EventTracker   *EventTracker
	RetryConfig    jobqueue.RetryConfig // Configuration for retry behavior
	Description    string
	mu             sync.Mutex // Protect concurrent access to Note
	// SSEBroadcaster is a function that broadcasts detection data
	// This allows the action to be independent of the specific API implementation
	SSEBroadcaster func(note *datastore.Note, birdImage *imageprovider.BirdImage) error
	// Datastore interface for querying the database to get the assigned ID
	Ds datastore.Interface
}

// GetDescription returns a human-readable description of the LogAction
func (a *LogAction) GetDescription() string {
	if a.Description != "" {
		return a.Description
	}
	return "Log bird detection to file"
}

// GetDescription returns a human-readable description of the DatabaseAction
func (a *DatabaseAction) GetDescription() string {
	if a.Description != "" {
		return a.Description
	}
	return "Save bird detection to database"
}

// GetDescription returns a human-readable description of the SaveAudioAction
func (a *SaveAudioAction) GetDescription() string {
	if a.Description != "" {
		return a.Description
	}
	return "Save audio clip to file"
}

// GetDescription returns a human-readable description of the BirdWeatherAction
func (a *BirdWeatherAction) GetDescription() string {
	if a.Description != "" {
		return a.Description
	}
	return "Upload detection to BirdWeather"
}

// GetDescription returns a human-readable description of the MqttAction
func (a *MqttAction) GetDescription() string {
	if a.Description != "" {
		return a.Description
	}
	return "Publish detection to MQTT"
}

// GetDescription returns a human-readable description of the UpdateRangeFilterAction
func (a *UpdateRangeFilterAction) GetDescription() string {
	if a.Description != "" {
		return a.Description
	}
	return "Update BirdNET range filter"
}

// GetDescription returns a human-readable description of the SSEAction
func (a *SSEAction) GetDescription() string {
	if a.Description != "" {
		return a.Description
	}
	return "Broadcast detection via Server-Sent Events"
}

// Execute logs the note to the chag log file
func (a *LogAction) Execute(data interface{}) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	species := strings.ToLower(a.Note.CommonName)

	// Check if the event should be handled for this species
	if !a.EventTracker.TrackEvent(species, LogToFile) {
		return nil
	}

	// Log note to file
	if err := observation.LogNoteToFile(a.Settings, &a.Note); err != nil {
		// If an error occurs when logging to a file, wrap and return the error.
		// Add structured logging
		GetLogger().Error("Failed to log note to file",
			"error", err,
			"species", a.Note.CommonName,
			"confidence", a.Note.Confidence,
			"clip_name", a.Note.ClipName,
			"operation", "log_to_file")
		log.Printf("❌ Failed to log note to file: %v", err)
	}
	// Add structured logging for console output
	GetLogger().Info("Detection logged",
		"species", a.Note.CommonName,
		"confidence", a.Note.Confidence,
		"time", a.Note.Time,
		"operation", "console_output")
	fmt.Printf("%s %s %.2f\n", a.Note.Time, a.Note.CommonName, a.Note.Confidence)

	return nil
}

// Execute saves the note to the database
func (a *DatabaseAction) Execute(data interface{}) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	species := strings.ToLower(a.Note.CommonName)

	// Check event frequency
	if !a.EventTracker.TrackEvent(species, DatabaseSave) {
		return nil
	}

	// Check if this is a new species and update atomically to prevent race conditions
	var isNewSpecies bool
	var daysSinceFirstSeen int
	if a.NewSpeciesTracker != nil {
		// Use atomic check-and-update to prevent duplicate "new species" notifications
		// when multiple detections of the same species arrive concurrently
		isNewSpecies, daysSinceFirstSeen = a.NewSpeciesTracker.CheckAndUpdateSpecies(a.Note.ScientificName, time.Now())
	}
	
	// Save note to database
	if err := a.Ds.Save(&a.Note, a.Results); err != nil {
		// Add structured logging
		GetLogger().Error("Failed to save note and results to database",
			"error", err,
			"species", a.Note.CommonName,
			"scientific_name", a.Note.ScientificName,
			"confidence", a.Note.Confidence,
			"clip_name", a.Note.ClipName,
			"operation", "database_save")
		log.Printf("❌ Failed to save note and results to database: %v", err)
		return err
	}
	
	// After successful save, publish detection event for new species
	a.publishNewSpeciesDetectionEvent(isNewSpecies, daysSinceFirstSeen)

	// Save audio clip to file if enabled
	if a.Settings.Realtime.Audio.Export.Enabled {
		// export audio clip from capture buffer
		pcmData, err := myaudio.ReadSegmentFromCaptureBuffer(a.Note.Source, a.Note.BeginTime, 15)
		if err != nil {
			// Add structured logging
			GetLogger().Error("Failed to read audio segment from buffer",
				"error", err,
				"species", a.Note.CommonName,
				"source", privacy.SanitizeRTSPUrls(a.Note.Source),
				"begin_time", a.Note.BeginTime,
				"duration_seconds", 15,
				"operation", "read_audio_segment")
			log.Printf("❌ Failed to read audio segment from buffer: %v", err)
			return err
		}

		// Create a SaveAudioAction and execute it
		saveAudioAction := &SaveAudioAction{
			Settings: a.Settings,
			ClipName: a.Note.ClipName,
			pcmData:  pcmData,
		}

		if err := saveAudioAction.Execute(nil); err != nil {
			// Add structured logging
			GetLogger().Error("Failed to save audio clip",
				"error", err,
				"species", a.Note.CommonName,
				"clip_name", a.Note.ClipName,
				"operation", "save_audio_clip")
			log.Printf("❌ Failed to save audio clip: %v", err)
			return err
		}

		if a.Settings.Debug {
			// Add structured logging
			GetLogger().Debug("Saved audio clip successfully",
				"species", a.Note.CommonName,
				"clip_name", a.Note.ClipName,
				"detection_time", a.Note.Time,
				"begin_time", a.Note.BeginTime,
				"end_time", time.Now(),
				"operation", "save_audio_clip_debug")
			log.Printf("✅ Saved audio clip to %s\n", a.Note.ClipName)
			log.Printf("detection time %v, begin time %v, end time %v\n", a.Note.Time, a.Note.BeginTime, time.Now())
		}
	}

	return nil
}

// publishNewSpeciesDetectionEvent publishes a detection event for new species
// This helper method handles event bus retrieval, event creation, publishing, and debug logging
func (a *DatabaseAction) publishNewSpeciesDetectionEvent(isNewSpecies bool, daysSinceFirstSeen int) {
	if !isNewSpecies || !events.IsInitialized() {
		return
	}

	eventBus := events.GetEventBus()
	if eventBus == nil {
		return
	}

	detectionEvent, err := events.NewDetectionEvent(
		a.Note.CommonName,
		a.Note.ScientificName,
		float64(a.Note.Confidence),
		a.Note.Source,
		isNewSpecies,
		daysSinceFirstSeen,
	)
	if err != nil {
		if a.Settings.Debug {
			// Add structured logging
			GetLogger().Debug("Failed to create detection event",
				"error", err,
				"species", a.Note.CommonName,
				"scientific_name", a.Note.ScientificName,
				"is_new_species", isNewSpecies,
				"days_since_first_seen", daysSinceFirstSeen,
				"operation", "create_detection_event")
			log.Printf("❌ Failed to create detection event: %v", err)
		}
		return
	}

	// Publish the detection event
	if published := eventBus.TryPublishDetection(detectionEvent); published {
		if a.Settings.Debug {
			// Add structured logging
			GetLogger().Debug("Published new species detection event",
				"species", a.Note.CommonName,
				"scientific_name", a.Note.ScientificName,
				"confidence", a.Note.Confidence,
				"is_new_species", isNewSpecies,
				"days_since_first_seen", daysSinceFirstSeen,
				"operation", "publish_detection_event")
			log.Printf("🌟 Published new species detection event: %s", a.Note.CommonName)
		}
	}
}

// Execute saves the audio clip to a file
func (a *SaveAudioAction) Execute(data interface{}) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Get the full path by joining the export path with the relative clip name
	outputPath := filepath.Join(a.Settings.Realtime.Audio.Export.Path, a.ClipName)

	// Ensure the directory exists
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		// Add structured logging
		GetLogger().Error("Failed to create directory for audio clip",
			"error", err,
			"output_path", outputPath,
			"clip_name", a.ClipName,
			"operation", "create_directory")
		log.Printf("❌ error creating directory for audio clip: %s\n", err)
		return err
	}

	if a.Settings.Realtime.Audio.Export.Type == "wav" {
		if err := myaudio.SavePCMDataToWAV(outputPath, a.pcmData); err != nil {
			// Add structured logging
			GetLogger().Error("Failed to save audio clip to WAV",
				"error", err,
				"output_path", outputPath,
				"clip_name", a.ClipName,
				"format", "wav",
				"operation", "save_wav")
			log.Printf("❌ error saving audio clip to WAV: %s\n", err)
			return err
		}
	} else {
		if err := myaudio.ExportAudioWithFFmpeg(a.pcmData, outputPath, &a.Settings.Realtime.Audio); err != nil {
			// Add structured logging
			GetLogger().Error("Failed to export audio clip with FFmpeg",
				"error", err,
				"output_path", outputPath,
				"clip_name", a.ClipName,
				"format", a.Settings.Realtime.Audio.Export.Type,
				"operation", "ffmpeg_export")
			log.Printf("❌ error exporting audio clip with FFmpeg: %s\n", err)
			return err
		}
	}

	return nil
}

// Execute sends the note to the BirdWeather API
func (a *BirdWeatherAction) Execute(data interface{}) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	species := strings.ToLower(a.Note.CommonName)

	// Check event frequency
	if !a.EventTracker.TrackEvent(species, BirdWeatherSubmit) {
		return nil
	}

	// Early check if BirdWeather is still enabled in settings
	if !a.Settings.Realtime.Birdweather.Enabled {
		return nil // Silently exit if BirdWeather was disabled after this action was created
	}

	// Add threshold check here
	if a.Note.Confidence < float64(a.Settings.Realtime.Birdweather.Threshold) {
		if a.Settings.Debug {
			// Add structured logging
			GetLogger().Debug("Skipping BirdWeather upload due to low confidence",
				"species", species,
				"confidence", a.Note.Confidence,
				"threshold", a.Settings.Realtime.Birdweather.Threshold,
				"operation", "birdweather_threshold_check")
			log.Printf("⛔ Skipping BirdWeather upload for %s: confidence %.2f below threshold %.2f\n",
				species, a.Note.Confidence, a.Settings.Realtime.Birdweather.Threshold)
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

	// Copy data locally to reduce lock duration if needed
	note := a.Note
	pcmData := a.pcmData

	// Try to publish with appropriate error handling
	if err := a.BwClient.Publish(&note, pcmData); err != nil {
		// Log the error with retry information if retries are enabled
		// Sanitize error before logging
		sanitizedErr := sanitizeError(err)
		// Add structured logging
		GetLogger().Error("Failed to upload to BirdWeather",
			"error", sanitizedErr,
			"species", note.CommonName,
			"scientific_name", note.ScientificName,
			"confidence", note.Confidence,
			"clip_name", note.ClipName,
			"retry_enabled", a.RetryConfig.Enabled,
			"operation", "birdweather_upload")
		if a.RetryConfig.Enabled {
			log.Printf("❌ Error uploading %s (%s) to BirdWeather (confidence: %.2f, clip: %s) (will retry): %v\n",
				note.CommonName, note.ScientificName, note.Confidence, note.ClipName, sanitizedErr)
		} else {
			log.Printf("❌ Error uploading %s (%s) to BirdWeather (confidence: %.2f, clip: %s): %v\n",
				note.CommonName, note.ScientificName, note.Confidence, note.ClipName, sanitizedErr)
			// Send notification for non-retryable failures
			notification.NotifyIntegrationFailure("BirdWeather", err)
		}
		// Network and API errors are typically transient and may succeed on retry:
		// - Temporary network outages
		// - API rate limiting
		// - Server-side temporary failures
		// The job queue will handle exponential backoff for these retryable errors
		return errors.New(err).
			Component("analysis.processor").
			Category(errors.CategoryIntegration).
			Context("operation", "birdweather_upload").
			Context("species", note.CommonName).
			Context("confidence", note.Confidence).
			Context("clip_name", note.ClipName).
			Context("integration", "birdweather").
			Context("retryable", true). // Network/API errors are typically retryable
			Build()
	}

	if a.Settings.Debug {
		// Add structured logging
		GetLogger().Debug("Successfully uploaded to BirdWeather",
			"species", a.Note.CommonName,
			"scientific_name", a.Note.ScientificName,
			"confidence", a.Note.Confidence,
			"clip_name", a.Note.ClipName,
			"operation", "birdweather_upload_success")
		log.Printf("✅ Successfully uploaded %s to BirdWeather\n", a.Note.ClipName)
	}
	return nil
}

type NoteWithBirdImage struct {
	datastore.Note
	BirdImage imageprovider.BirdImage
}

// Execute sends the note to the MQTT broker
func (a *MqttAction) Execute(data interface{}) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Rely on background reconnect; fail action if not currently connected.
	if !a.MqttClient.IsConnected() {
		// Log slightly differently to indicate it's waiting for background reconnect
		// Add structured logging
		GetLogger().Warn("MQTT client not connected, skipping publish",
			"species", a.Note.CommonName,
			"scientific_name", a.Note.ScientificName,
			"confidence", a.Note.Confidence,
			"operation", "mqtt_connection_check",
			"status", "waiting_reconnect")
		log.Printf("🟡 MQTT client is not connected, skipping publish for %s (%s). Waiting for automatic reconnect.", a.Note.CommonName, a.Note.ScientificName)
		// MQTT connection failures are retryable because:
		// - The MQTT client has automatic reconnection logic
		// - Connection may be temporarily lost due to network issues
		// - Broker may be temporarily unavailable
		// The job queue retry mechanism complements the client's own reconnection
		return errors.Newf("MQTT client not connected").
			Component("analysis.processor").
			Category(errors.CategoryMQTTConnection).
			Context("operation", "mqtt_publish").
			Context("integration", "mqtt").
			Context("retryable", true). // Connection issues are retryable
			Build()
	}

	species := strings.ToLower(a.Note.CommonName)

	// Check event frequency
	if !a.EventTracker.TrackEvent(species, MQTTPublish) {
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

	// Get bird image of detected bird
	birdImage := imageprovider.BirdImage{} // Default to empty image
	// Add nil check for BirdImageCache before calling Get
	if a.BirdImageCache != nil {
		var err error
		birdImage, err = a.BirdImageCache.Get(a.Note.ScientificName)
		if err != nil {
			// Add structured logging
			GetLogger().Warn("Error getting bird image from cache",
				"error", err,
				"species", a.Note.CommonName,
				"scientific_name", a.Note.ScientificName,
				"operation", "get_bird_image")
			log.Printf("⚠️ Error getting bird image from cache for %s: %v", a.Note.ScientificName, err)
			// Continue with the default empty image
		}
	} else {
		// Log if the cache is nil, maybe helpful for debugging setup issues
		// Add structured logging
		GetLogger().Warn("BirdImageCache is nil, cannot fetch image",
			"species", a.Note.CommonName,
			"scientific_name", a.Note.ScientificName,
			"operation", "check_bird_image_cache")
		log.Printf("🟡 BirdImageCache is nil, cannot fetch image for %s", a.Note.ScientificName)
	}

	// Create a copy of the Note with sanitized RTSP URL
	noteCopy := a.Note
	noteCopy.Source = conf.SanitizeRTSPUrl(noteCopy.Source)

	// Wrap note with bird image
	noteWithBirdImage := NoteWithBirdImage{Note: a.Note, BirdImage: birdImage}

	// Create a JSON representation of the note
	noteJson, err := json.Marshal(noteWithBirdImage)
	if err != nil {
		// Add structured logging
		GetLogger().Error("Failed to marshal note to JSON",
			"error", err,
			"species", a.Note.CommonName,
			"scientific_name", a.Note.ScientificName,
			"operation", "json_marshal")
		log.Printf("❌ Error marshalling note to JSON: %s\n", err)
		return err
	}

	// Create a context with timeout for publishing
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Publish the note to the MQTT broker
	err = a.MqttClient.Publish(ctx, a.Settings.Realtime.MQTT.Topic, string(noteJson))
	if err != nil {
		// Log the error with retry information if retries are enabled
		// Sanitize error before logging
		sanitizedErr := sanitizeError(err)
		// Add structured logging
		GetLogger().Error("Failed to publish to MQTT",
			"error", sanitizedErr,
			"species", a.Note.CommonName,
			"scientific_name", a.Note.ScientificName,
			"confidence", a.Note.Confidence,
			"clip_name", a.Note.ClipName,
			"topic", a.Settings.Realtime.MQTT.Topic,
			"retry_enabled", a.RetryConfig.Enabled,
			"operation", "mqtt_publish")
		if a.RetryConfig.Enabled {
			log.Printf("❌ Error publishing %s (%s) to MQTT topic %s (confidence: %.2f, clip: %s) (will retry): %v\n",
				a.Note.CommonName, a.Note.ScientificName, a.Settings.Realtime.MQTT.Topic, a.Note.Confidence, a.Note.ClipName, sanitizedErr)
		} else {
			log.Printf("❌ Error publishing %s (%s) to MQTT topic %s (confidence: %.2f, clip: %s): %v\n",
				a.Note.CommonName, a.Note.ScientificName, a.Settings.Realtime.MQTT.Topic, a.Note.Confidence, a.Note.ClipName, sanitizedErr)
			// Send notification for non-retryable failures
			notification.NotifyIntegrationFailure("MQTT", err)
		}
		return errors.New(err).
			Component("analysis.processor").
			Category(errors.CategoryMQTTPublish).
			Context("operation", "mqtt_publish").
			Context("species", a.Note.CommonName).
			Context("confidence", a.Note.Confidence).
			Context("topic", a.Settings.Realtime.MQTT.Topic).
			Context("clip_name", a.Note.ClipName).
			Context("integration", "mqtt").
			Context("retryable", true). // MQTT publish failures are typically retryable
			Build()
	}

	if a.Settings.Debug {
		// Add structured logging
		GetLogger().Debug("Successfully published to MQTT",
			"species", a.Note.CommonName,
			"scientific_name", a.Note.ScientificName,
			"confidence", a.Note.Confidence,
			"topic", a.Settings.Realtime.MQTT.Topic,
			"operation", "mqtt_publish_success")
		log.Printf("✅ Successfully published %s to MQTT topic %s\n",
			a.Note.CommonName, a.Settings.Realtime.MQTT.Topic)
	}
	return nil
}

// Execute updates the range filter species list, this is run every day
func (a *UpdateRangeFilterAction) Execute(data interface{}) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	today := time.Now().Truncate(24 * time.Hour)
	if today.After(a.Settings.BirdNET.RangeFilter.LastUpdated) {
		// Update location based species list
		speciesScores, err := a.Bn.GetProbableSpecies(today, 0.0)
		if err != nil {
			return err
		}

		// Convert the speciesScores slice to a slice of species labels
		var includedSpecies []string
		for _, speciesScore := range speciesScores {
			includedSpecies = append(includedSpecies, speciesScore.Label)
		}

		a.Settings.UpdateIncludedSpecies(includedSpecies)
	}
	return nil
}

// Execute broadcasts the detection via Server-Sent Events
func (a *SSEAction) Execute(data interface{}) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Check if SSE broadcaster is available
	if a.SSEBroadcaster == nil {
		return nil // Silently skip if no broadcaster is configured
	}

	species := strings.ToLower(a.Note.CommonName)

	// Check event frequency
	if !a.EventTracker.TrackEvent(species, SSEBroadcast) {
		return nil
	}

	// Wait for audio file to be available if this detection has an audio clip assigned
	// This properly handles per-species audio settings and avoids false positives
	if a.Note.ClipName != "" {
		if err := a.waitForAudioFile(); err != nil {
			// Log warning but don't fail the SSE broadcast
			// Add structured logging
			GetLogger().Warn("Audio file not ready for SSE broadcast",
				"error", err,
				"species", a.Note.CommonName,
				"clip_name", a.Note.ClipName,
				"operation", "sse_wait_audio_file")
			log.Printf("⚠️ Audio file not ready for %s, broadcasting without waiting: %v", a.Note.CommonName, err)
		}
	}

	// Wait for database ID to be assigned if Note.ID is 0 (new detection)
	// This ensures the frontend can properly load audio/spectrogram via API endpoints
	if a.Note.ID == 0 {
		if err := a.waitForDatabaseID(); err != nil {
			// Log warning but don't fail the SSE broadcast
			// Add structured logging
			GetLogger().Warn("Database ID not ready for SSE broadcast",
				"error", err,
				"species", a.Note.CommonName,
				"note_id", a.Note.ID,
				"operation", "sse_wait_database_id")
			log.Printf("⚠️ Database ID not ready for %s, broadcasting with ID=0: %v", a.Note.CommonName, err)
		}
	}

	// Get bird image of detected bird
	birdImage := imageprovider.BirdImage{} // Default to empty image
	// Add nil check for BirdImageCache before calling Get
	if a.BirdImageCache != nil {
		var err error
		birdImage, err = a.BirdImageCache.Get(a.Note.ScientificName)
		if err != nil {
			// Add structured logging
			GetLogger().Warn("Error getting bird image from cache",
				"error", err,
				"species", a.Note.CommonName,
				"scientific_name", a.Note.ScientificName,
				"operation", "get_bird_image")
			log.Printf("⚠️ Error getting bird image from cache for %s: %v", a.Note.ScientificName, err)
			// Continue with the default empty image
		}
	} else {
		// Log if the cache is nil, maybe helpful for debugging setup issues
		// Add structured logging
		GetLogger().Warn("BirdImageCache is nil, cannot fetch image",
			"species", a.Note.CommonName,
			"scientific_name", a.Note.ScientificName,
			"operation", "check_bird_image_cache")
		log.Printf("🟡 BirdImageCache is nil, cannot fetch image for %s", a.Note.ScientificName)
	}

	// Create a copy of the Note with sanitized RTSP URL
	noteCopy := a.Note
	noteCopy.Source = conf.SanitizeRTSPUrl(noteCopy.Source)

	// Broadcast the detection with error handling
	if err := a.SSEBroadcaster(&noteCopy, &birdImage); err != nil {
		// Log the error with retry information if retries are enabled
		// Sanitize error before logging
		sanitizedErr := sanitizeError(err)
		// Add structured logging
		GetLogger().Error("Failed to broadcast via SSE",
			"error", sanitizedErr,
			"species", a.Note.CommonName,
			"scientific_name", a.Note.ScientificName,
			"confidence", a.Note.Confidence,
			"clip_name", a.Note.ClipName,
			"retry_enabled", a.RetryConfig.Enabled,
			"operation", "sse_broadcast")
		if a.RetryConfig.Enabled {
			log.Printf("❌ Error broadcasting %s (%s) via SSE (confidence: %.2f, clip: %s) (will retry): %v\n",
				a.Note.CommonName, a.Note.ScientificName, a.Note.Confidence, a.Note.ClipName, sanitizedErr)
		} else {
			log.Printf("❌ Error broadcasting %s (%s) via SSE (confidence: %.2f, clip: %s): %v\n",
				a.Note.CommonName, a.Note.ScientificName, a.Note.Confidence, a.Note.ClipName, sanitizedErr)
		}
		return errors.New(err).
			Component("analysis.processor").
			Category(errors.CategoryBroadcast).
			Context("operation", "sse_broadcast").
			Context("species", a.Note.CommonName).
			Context("confidence", a.Note.Confidence).
			Context("clip_name", a.Note.ClipName).
			Context("retryable", true). // SSE broadcast failures are typically retryable
			Build()
	}

	if a.Settings.Debug {
		// Add structured logging
		GetLogger().Debug("Successfully broadcasted via SSE",
			"species", a.Note.CommonName,
			"scientific_name", a.Note.ScientificName,
			"confidence", a.Note.Confidence,
			"clip_name", a.Note.ClipName,
			"operation", "sse_broadcast_success")
		log.Printf("✅ Successfully broadcasted %s via SSE\n", a.Note.CommonName)
	}

	return nil
}

// waitForAudioFile waits for the audio file to be written to disk with a timeout
func (a *SSEAction) waitForAudioFile() error {
	if a.Note.ClipName == "" {
		return nil // No audio file expected
	}

	// Build the full path to the audio file using the configured export path
	audioPath := filepath.Join(a.Settings.Realtime.Audio.Export.Path, a.Note.ClipName)
	
	// Wait up to 5 seconds for file to be written
	timeout := 5 * time.Second
	deadline := time.Now().Add(timeout)
	checkInterval := 100 * time.Millisecond

	for time.Now().Before(deadline) {
		// Check if file exists and has content
		if info, err := os.Stat(audioPath); err == nil {
			// File exists, check if it has reasonable size (audio files should be > 1KB)
			if info.Size() > 1024 {
				if a.Settings.Debug {
					// Add structured logging
				GetLogger().Debug("Audio file ready for SSE broadcast",
					"clip_name", a.Note.ClipName,
					"file_size_bytes", info.Size(),
					"species", a.Note.CommonName,
					"operation", "wait_audio_file_success")
				log.Printf("🎵 Audio file ready for SSE broadcast: %s (size: %d bytes)", a.Note.ClipName, info.Size())
				}
				return nil
			}
			// File exists but might still be writing, wait a bit more
		}
		
		time.Sleep(checkInterval)
	}

	// Timeout reached
	return errors.Newf("audio file %s not ready after %v timeout", a.Note.ClipName, timeout).
		Component("analysis.processor").
		Category(errors.CategoryTimeout).
		Context("operation", "wait_for_audio_file").
		Context("clip_name", a.Note.ClipName).
		Context("timeout_seconds", timeout.Seconds()).
		Build()
}

// waitForDatabaseID waits for the Note to be saved to database and ID assigned
func (a *SSEAction) waitForDatabaseID() error {
	// We need to query the database to find this note by unique characteristics
	// Since we don't have the ID yet, we'll search by time, species, and confidence
	timeout := 10 * time.Second
	deadline := time.Now().Add(timeout)
	checkInterval := 200 * time.Millisecond

	for time.Now().Before(deadline) {
		// Query database for a note matching our characteristics
		// Use a small time window around the detection time to find the record
		if updatedNote, err := a.findNoteInDatabase(); err == nil && updatedNote.ID > 0 {
			// Found the note with an ID, update our copy
			a.Note.ID = updatedNote.ID
			if a.Settings.Debug {
				// Add structured logging
				GetLogger().Debug("Found database ID for SSE broadcast",
					"database_id", updatedNote.ID,
					"species", a.Note.CommonName,
					"scientific_name", a.Note.ScientificName,
					"operation", "wait_database_id_success")
				log.Printf("🔍 Found database ID %d for SSE broadcast: %s", updatedNote.ID, a.Note.CommonName)
			}
			return nil
		}
		
		time.Sleep(checkInterval)
	}

	// Timeout reached
	return errors.Newf("database ID not assigned for %s after %v timeout", a.Note.CommonName, timeout).
		Component("analysis.processor").
		Category(errors.CategoryTimeout).
		Context("operation", "wait_for_database_id").
		Context("species", a.Note.CommonName).
		Context("timeout_seconds", timeout.Seconds()).
		Build()
}

// findNoteInDatabase searches for the note in database by unique characteristics
func (a *SSEAction) findNoteInDatabase() (*datastore.Note, error) {
	if a.Ds == nil {
		return nil, errors.Newf("datastore not available").
			Component("analysis.processor").
			Category(errors.CategoryDatabase).
			Context("operation", "find_note_in_database").
			Context("retryable", false). // System configuration issue - not retryable
			Build()
	}

	// Search for notes with matching characteristics
	// The SearchNotes method expects a search query string that will match against
	// common_name or scientific_name fields
	query := a.Note.ScientificName
	
	// Search for notes, sorted by ID descending to get the most recent
	notes, err := a.Ds.SearchNotes(query, false, 10, 0) // false = sort descending, limit 10, offset 0
	if err != nil {
		return nil, errors.New(err).
			Component("analysis.processor").
			Category(errors.CategoryDatabase).
			Context("operation", "search_notes").
			Context("query", query).
			Build()
	}

	// Filter results to find the exact match based on date and time
	for i := range notes {
		note := &notes[i]
		// Check if this note matches our expected characteristics
		if note.Date == a.Note.Date && 
		   note.ScientificName == a.Note.ScientificName &&
		   note.Time == a.Note.Time { // Exact time match
			return note, nil
		}
	}

	return nil, errors.Newf("note not found in database").
		Component("analysis.processor").
		Category(errors.CategoryNotFound).
		Context("operation", "find_note_in_database").
		Context("species", a.Note.ScientificName).
		Context("date", a.Note.Date).
		Context("time", a.Note.Time).
		Build()
}
