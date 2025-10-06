// processor/actions.go

package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/analysis/species"
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
)

// Timeout and interval constants
const (
	// SSEDatabaseIDTimeout is the maximum time to wait for database ID assignment
	SSEDatabaseIDTimeout = 10 * time.Second

	// SSEDatabaseCheckInterval is how often to check for database ID
	SSEDatabaseCheckInterval = 200 * time.Millisecond

	// SSEAudioFileTimeout is the maximum time to wait for audio file to be written
	SSEAudioFileTimeout = 5 * time.Second

	// SSEAudioCheckInterval is how often to check for audio file
	SSEAudioCheckInterval = 100 * time.Millisecond

	// MinAudioFileSize is the minimum size in bytes for a valid audio file
	// Typed as int64 to match os.FileInfo.Size() return type
	MinAudioFileSize int64 = 1024

	// MQTTPublishTimeout is the timeout for MQTT publish operations
	MQTTPublishTimeout = 10 * time.Second

	// DatabaseSearchLimit is the maximum number of results when searching for notes
	DatabaseSearchLimit = 10

	// CompositeActionTimeout is the default timeout for each action in a composite action
	// This is generous to accommodate slow hardware (e.g., Raspberry Pi with SD cards)
	CompositeActionTimeout = 30 * time.Second

	// ExecuteCommandTimeout is the timeout for external command execution
	ExecuteCommandTimeout = 5 * time.Minute
)

// Action is the base interface for all actions that can be executed
type Action interface {
	Execute(data interface{}) error
	GetDescription() string
}

// ContextAction is an enhanced action interface that supports context-aware execution
// This allows for proper cancellation and timeout propagation
type ContextAction interface {
	Action
	ExecuteContext(ctx context.Context, data interface{}) error
}

type LogAction struct {
	Settings      *conf.Settings
	Note          datastore.Note
	EventTracker  *EventTracker
	Description   string
	CorrelationID string     // Detection correlation ID for log tracking
	mu            sync.Mutex // Protect concurrent access to Note
}

type DatabaseAction struct {
	Settings          *conf.Settings
	Ds                datastore.Interface
	Note              datastore.Note
	Results           []datastore.Results
	EventTracker      *EventTracker
	NewSpeciesTracker *species.SpeciesTracker // Add reference to new species tracker
	processor         *Processor              // Add reference to processor for source name resolution
	Description       string
	CorrelationID     string     // Detection correlation ID for log tracking
	mu                sync.Mutex // Protect concurrent access to Note and Results
}

type SaveAudioAction struct {
	Settings      *conf.Settings
	ClipName      string
	pcmData       []byte
	EventTracker  *EventTracker
	Description   string
	CorrelationID string     // Detection correlation ID for log tracking
	mu            sync.Mutex // Protect concurrent access to pcmData
}

type BirdWeatherAction struct {
	Settings      *conf.Settings
	Note          datastore.Note
	pcmData       []byte
	BwClient      *birdweather.BwClient
	EventTracker  *EventTracker
	RetryConfig   jobqueue.RetryConfig // Configuration for retry behavior
	Description   string
	CorrelationID string     // Detection correlation ID for log tracking
	mu            sync.Mutex // Protect concurrent access to Note and pcmData
}

type MqttAction struct {
	Settings       *conf.Settings
	Note           datastore.Note
	BirdImageCache *imageprovider.BirdImageCache
	MqttClient     mqtt.Client
	EventTracker   *EventTracker
	RetryConfig    jobqueue.RetryConfig // Configuration for retry behavior
	Description    string
	CorrelationID  string     // Detection correlation ID for log tracking
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
	CorrelationID  string     // Detection correlation ID for log tracking
	mu             sync.Mutex // Protect concurrent access to Note
	// SSEBroadcaster is a function that broadcasts detection data
	// This allows the action to be independent of the specific API implementation
	SSEBroadcaster func(note *datastore.Note, birdImage *imageprovider.BirdImage) error
	// Datastore interface for querying the database to get the assigned ID
	Ds datastore.Interface
}

// CompositeAction executes multiple actions sequentially, ensuring proper dependency management.
//
// This action type was introduced to fix a critical race condition between DatabaseAction
// and SSEAction (GitHub issue #1158). The SSEAction depends on DatabaseAction completing
// first to ensure database IDs are assigned before SSE broadcasts occur.
//
// Key Features:
//   - Sequential execution: Actions execute in order, each waiting for the previous to complete
//   - Configurable timeout: Per-action timeout can be overridden (default: 30 seconds)
//   - Context support: Actions implementing ContextAction get proper context propagation
//   - Panic recovery: Panics in individual actions are caught and converted to errors
//   - Thread-safe: Mutex protects the Actions slice during access
//   - Nil-safe: Handles nil actions and empty action lists gracefully
//
// Usage:
//
//	timeout := 45 * time.Second
//	composite := &CompositeAction{
//	    Actions: []Action{databaseAction, sseAction},
//	    Description: "Save to database then broadcast",
//	    Timeout: &timeout,  // Optional: override default timeout
//	}
//	err := composite.Execute(data)
//
// This pattern ensures that dependent actions execute in the correct order, preventing
// timeout errors like "database ID not assigned after 10s" that occur when actions
// execute concurrently on resource-constrained hardware.
type CompositeAction struct {
	Actions       []Action       // Actions to execute in sequence
	Description   string         // Human-readable description
	Timeout       *time.Duration // Optional: per-action timeout override (nil = use default)
	CorrelationID string         // Detection correlation ID for log tracking
	mu            sync.Mutex     // Protects concurrent access to Actions
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

// GetDescription returns a human-readable description of the CompositeAction
func (a *CompositeAction) GetDescription() string {
	if a.Description != "" {
		return a.Description
	}
	return "Composite action (sequential execution)"
}

// Execute runs all actions sequentially, stopping on first error
// This method is designed to prevent deadlocks and handle timeouts properly
func (a *CompositeAction) Execute(data interface{}) error {
	// Handle nil or empty actions gracefully
	if a == nil || a.Actions == nil || len(a.Actions) == 0 {
		return nil // Nothing to execute
	}

	// Only lock while accessing the Actions slice, not during execution
	a.mu.Lock()
	actions := make([]Action, len(a.Actions))
	copy(actions, a.Actions)
	a.mu.Unlock()

	// Count non-nil actions for accurate progress reporting
	nonNilCount := 0
	for _, action := range actions {
		if action != nil {
			nonNilCount++
		}
	}

	if nonNilCount == 0 {
		return nil // All actions are nil
	}

	// Execute each action in order without holding the mutex
	currentStep := 0
	for _, action := range actions {
		if action == nil {
			continue
		}
		currentStep++

		// Add panic recovery for each action to prevent crashes
		err := a.executeActionWithRecovery(action, data, currentStep, nonNilCount)
		if err != nil {
			return err
		}
	}

	return nil
}

// executeActionWithRecovery executes a single action with panic recovery and proper context handling
func (a *CompositeAction) executeActionWithRecovery(action Action, data interface{}, step, total int) error {
	// Determine the timeout to use
	timeout := CompositeActionTimeout
	if a.Timeout != nil {
		timeout = *a.Timeout
	}

	// Create context with timeout for proper cancellation
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel() // Ensure cancel is always called to prevent context leak

	// Use a channel to capture the result
	type result struct {
		err error
	}
	resultChan := make(chan result, 1)

	// Check if action supports context-aware execution
	if contextAction, ok := action.(ContextAction); ok {
		// Use context-aware execution path
		go func() {
			// Recover from panics to prevent goroutine crashes
			defer func() {
				if r := recover(); r != nil {
					panicErr := errors.Newf("action panicked: %v", r).
						Component("analysis.processor").
						Category(errors.CategoryProcessing).
						Context("action_type", fmt.Sprintf("%T", action)).
						Context("action_description", action.GetDescription()).
						Context("panic_value", fmt.Sprintf("%v", r)).
						Context("step", step).
						Context("total_steps", total).
						Build()
					select {
					case resultChan <- result{err: panicErr}:
					case <-ctx.Done():
						// Context cancelled, exit gracefully
					}
				}
			}()

			// Execute the action with context
			err := contextAction.ExecuteContext(ctx, data)
			select {
			case resultChan <- result{err: err}:
			case <-ctx.Done():
				// Context cancelled, exit gracefully
			}
		}()
	} else {
		// Fall back to legacy execution with goroutine management
		go func() {
			// Recover from panics to prevent goroutine crashes
			defer func() {
				if r := recover(); r != nil {
					panicErr := errors.Newf("action panicked: %v", r).
						Component("analysis.processor").
						Category(errors.CategoryProcessing).
						Context("action_type", fmt.Sprintf("%T", action)).
						Context("action_description", action.GetDescription()).
						Context("panic_value", fmt.Sprintf("%v", r)).
						Context("step", step).
						Context("total_steps", total).
						Build()
					select {
					case resultChan <- result{err: panicErr}:
					case <-ctx.Done():
						// Context cancelled, exit gracefully
					}
				}
			}()

			// Create a channel to signal completion
			done := make(chan struct{})
			var execErr error

			// Execute the action in a separate goroutine
			go func() {
				defer close(done)
				// Add panic recovery for the execution goroutine
				defer func() {
					if r := recover(); r != nil {
						// Convert panic to error
						execErr = errors.Newf("action panicked: %v", r).
							Component("analysis.processor").
							Category(errors.CategoryProcessing).
							Context("action_type", fmt.Sprintf("%T", action)).
							Context("action_description", action.GetDescription()).
							Context("panic_value", fmt.Sprintf("%v", r)).
							Context("step", step).
							Context("total_steps", total).
							Build()
					}
				}()
				execErr = action.Execute(data)
			}()

			// Wait for either completion or context cancellation
			select {
			case <-done:
				// Action completed, send result
				select {
				case resultChan <- result{err: execErr}:
				case <-ctx.Done():
					// Context cancelled while sending result
				}
			case <-ctx.Done():
				// Context cancelled/timed out
				// The Execute goroutine will continue but we won't wait for it
				// This prevents blocking but the goroutine will complete eventually
				select {
				case resultChan <- result{err: ctx.Err()}:
				default:
				}
			}
		}()
	}

	// Wait for result or timeout
	select {
	case res := <-resultChan:
		if res.err != nil {
			// Check if it was a context error
			if errors.Is(res.err, context.DeadlineExceeded) {
				timeoutErr := errors.Newf("action timed out after %v", timeout).
					Component("analysis.processor").
					Category(errors.CategoryTimeout).
					Context("action_type", fmt.Sprintf("%T", action)).
					Context("action_description", action.GetDescription()).
					Context("timeout_seconds", timeout.Seconds()).
					Context("step", step).
					Context("total_steps", total).
					Build()
				GetLogger().Error("Composite action timed out",
					"component", "analysis.processor.actions",
					"detection_id", a.CorrelationID,
					"step", step,
					"total_steps", total,
					"action_description", action.GetDescription(),
					"timeout_seconds", timeout.Seconds(),
					"operation", "composite_action_timeout")
				return timeoutErr
			}
			// Log other errors
			GetLogger().Error("Composite action failed",
				"component", "analysis.processor.actions",
				"detection_id", a.CorrelationID,
				"step", step,
				"total_steps", total,
				"action_description", action.GetDescription(),
				"error", res.err,
				"operation", "composite_action_execute")
			return res.err
		}
		return nil
	case <-ctx.Done():
		// Context timeout or cancellation
		timeoutErr := errors.Newf("action timed out after %v", timeout).
			Component("analysis.processor").
			Category(errors.CategoryTimeout).
			Context("action_type", fmt.Sprintf("%T", action)).
			Context("action_description", action.GetDescription()).
			Context("timeout_seconds", timeout.Seconds()).
			Context("step", step).
			Context("total_steps", total).
			Build()
		GetLogger().Error("Composite action timed out",
			"component", "analysis.processor.actions",
			"detection_id", a.CorrelationID,
			"step", step,
			"total_steps", total,
			"action_description", action.GetDescription(),
			"timeout_seconds", timeout.Seconds(),
			"operation", "composite_action_context_timeout")
		return timeoutErr
	}
}

// Execute logs the note to the chag log file
func (a *LogAction) Execute(data interface{}) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	speciesName := strings.ToLower(a.Note.CommonName)

	// Check if the event should be handled for this species
	if !a.EventTracker.TrackEvent(speciesName, LogToFile) {
		return nil
	}

	// Log note to file
	if err := observation.LogNoteToFile(a.Settings, &a.Note); err != nil {
		// If an error occurs when logging to a file, wrap and return the error.
		// Add structured logging
		GetLogger().Error("Failed to log note to file",
			"component", "analysis.processor.actions",
			"detection_id", a.CorrelationID,
			"error", err,
			"species", a.Note.CommonName,
			"confidence", a.Note.Confidence,
			"clip_name", a.Note.ClipName,
			"operation", "log_to_file")
		log.Printf("❌ Failed to log note to file")
	}
	// Add structured logging for console output
	GetLogger().Info("Detection logged",
		"component", "analysis.processor.actions",
		"detection_id", a.CorrelationID,
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

	speciesName := strings.ToLower(a.Note.CommonName)

	// Check event frequency
	if !a.EventTracker.TrackEvent(speciesName, DatabaseSave) {
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
			"component", "analysis.processor.actions",
			"detection_id", a.CorrelationID,
			"error", err,
			"species", a.Note.CommonName,
			"scientific_name", a.Note.ScientificName,
			"confidence", a.Note.Confidence,
			"clip_name", a.Note.ClipName,
			"operation", "database_save")
		log.Printf("❌ Failed to save note and results to database")
		return err
	}

	// After successful save, publish detection event for new species
	a.publishNewSpeciesDetectionEvent(isNewSpecies, daysSinceFirstSeen)

	// Save audio clip to file if enabled
	if a.Settings.Realtime.Audio.Export.Enabled {
		captureLength := a.Settings.Realtime.Audio.Export.Length

		// debug log note begin, end and capture length
		GetLogger().Debug("Saving detection audio clip",
			"component", "analysis.processor.actions",
			"detection_id", a.CorrelationID,
			"begin_time", a.Note.BeginTime,
			"end_time", a.Note.EndTime,
			"capture_length", captureLength,
			"operation", "note_begin_end_capture_length")

		// export audio clip from capture buffer
		pcmData, err := myaudio.ReadSegmentFromCaptureBuffer(a.Note.Source.ID, a.Note.BeginTime, captureLength)
		if err != nil {
			// Add structured logging
			GetLogger().Error("Failed to read audio segment from buffer",
				"component", "analysis.processor.actions",
				"detection_id", a.CorrelationID,
				"error", err,
				"species", a.Note.CommonName,
				"source", a.Note.Source.SafeString,
				"begin_time", a.Note.BeginTime,
				"duration_seconds", 15,
				"operation", "read_audio_segment")
			log.Printf("❌ Failed to read audio segment from buffer")
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
				"component", "analysis.processor.actions",
				"detection_id", a.CorrelationID,
				"error", err,
				"species", a.Note.CommonName,
				"clip_name", a.Note.ClipName,
				"operation", "save_audio_clip")
			log.Printf("❌ Failed to save audio clip")
			return err
		}

		if a.Settings.Debug {
			// Add structured logging
			GetLogger().Debug("Saved audio clip successfully",
				"component", "analysis.processor.actions",
				"detection_id", a.CorrelationID,
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

// publishNewSpeciesDetectionEvent publishes a detection event for new species
// This helper method handles event bus retrieval, event creation, publishing, and debug logging
func (a *DatabaseAction) publishNewSpeciesDetectionEvent(isNewSpecies bool, daysSinceFirstSeen int) {
	if !isNewSpecies || !events.IsInitialized() {
		return
	}

	// Store current time for consistent use throughout
	var notificationTime time.Time

	// Check notification suppression if tracker is available
	if a.NewSpeciesTracker != nil {
		notificationTime = time.Now()

		// Check if notification should be suppressed for this species
		if a.NewSpeciesTracker.ShouldSuppressNotification(a.Note.ScientificName, notificationTime) {
			if a.Settings.Debug {
				GetLogger().Debug("Suppressing duplicate new species notification",
					"component", "analysis.processor.actions",
					"detection_id", a.CorrelationID,
					"species", a.Note.CommonName,
					"scientific_name", a.Note.ScientificName,
					"operation", "suppress_notification")
			}
			return
		}
	}

	eventBus := events.GetEventBus()
	if eventBus == nil {
		return
	}

	// Use display name directly from the AudioSource struct for user-facing notifications
	displayLocation := a.Note.Source.DisplayName

	detectionEvent, err := events.NewDetectionEvent(
		a.Note.CommonName,
		a.Note.ScientificName,
		float64(a.Note.Confidence),
		displayLocation,
		isNewSpecies,
		daysSinceFirstSeen,
	)
	if err != nil {
		if a.Settings.Debug {
			// Add structured logging
			GetLogger().Debug("Failed to create detection event",
				"component", "analysis.processor.actions",
				"detection_id", a.CorrelationID,
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

	// Add location, time, and note ID to metadata for template rendering
	metadata := detectionEvent.GetMetadata()
	metadata["note_id"] = a.Note.ID
	metadata["latitude"] = a.Note.Latitude
	metadata["longitude"] = a.Note.Longitude
	metadata["begin_time"] = a.Note.BeginTime

	// Get bird image URL from cache and add to metadata
	if a.processor != nil && a.processor.BirdImageCache != nil {
		birdImage, err := a.processor.BirdImageCache.Get(a.Note.ScientificName)
		if err == nil && birdImage.URL != "" {
			metadata["image_url"] = birdImage.URL
		}
	}

	// Publish the detection event
	if published := eventBus.TryPublishDetection(detectionEvent); published {
		// Only record notification as sent if publishing succeeded
		if a.NewSpeciesTracker != nil && !notificationTime.IsZero() {
			a.NewSpeciesTracker.RecordNotificationSent(a.Note.ScientificName, notificationTime)
		}

		if a.Settings.Debug {
			// Add structured logging
			GetLogger().Debug("Published new species detection event",
				"component", "analysis.processor.actions",
				"detection_id", a.CorrelationID,
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
			"component", "analysis.processor.actions",
			"detection_id", a.CorrelationID,
			"error", err,
			"output_path", outputPath,
			"clip_name", a.ClipName,
			"operation", "create_directory")
		log.Printf("❌ Error creating directory for audio clip")
		return err
	}

	if a.Settings.Realtime.Audio.Export.Type == "wav" {
		if err := myaudio.SavePCMDataToWAV(outputPath, a.pcmData); err != nil {
			// Add structured logging
			GetLogger().Error("Failed to save audio clip to WAV",
				"component", "analysis.processor.actions",
				"detection_id", a.CorrelationID,
				"error", err,
				"output_path", outputPath,
				"clip_name", a.ClipName,
				"format", "wav",
				"operation", "save_wav")
			log.Printf("❌ Error saving audio clip to WAV")
			return err
		}
	} else {
		if err := myaudio.ExportAudioWithFFmpeg(a.pcmData, outputPath, &a.Settings.Realtime.Audio); err != nil {
			// Add structured logging
			GetLogger().Error("Failed to export audio clip with FFmpeg",
				"component", "analysis.processor.actions",
				"detection_id", a.CorrelationID,
				"error", err,
				"output_path", outputPath,
				"clip_name", a.ClipName,
				"format", a.Settings.Realtime.Audio.Export.Type,
				"operation", "ffmpeg_export")
			log.Printf("❌ Error exporting audio clip with FFmpeg")
			return err
		}
	}

	return nil
}

// Execute sends the note to the BirdWeather API
func (a *BirdWeatherAction) Execute(data interface{}) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	speciesName := strings.ToLower(a.Note.CommonName)

	// Check event frequency
	if !a.EventTracker.TrackEvent(speciesName, BirdWeatherSubmit) {
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
				"component", "analysis.processor.actions",
				"detection_id", a.CorrelationID,
				"species", speciesName,
				"confidence", a.Note.Confidence,
				"threshold", a.Settings.Realtime.Birdweather.Threshold,
				"operation", "birdweather_threshold_check")
			log.Printf("⛔ Skipping BirdWeather upload for %s: confidence %.2f below threshold %.2f\n",
				speciesName, a.Note.Confidence, a.Settings.Realtime.Birdweather.Threshold)
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
			"component", "analysis.processor.actions",
			"detection_id", a.CorrelationID,
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
			"component", "analysis.processor.actions",
			"detection_id", a.CorrelationID,
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
			"component", "analysis.processor.actions",
			"detection_id", a.CorrelationID,
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

	speciesName := strings.ToLower(a.Note.CommonName)

	// Check event frequency
	if !a.EventTracker.TrackEvent(speciesName, MQTTPublish) {
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
				"component", "analysis.processor.actions",
				"detection_id", a.CorrelationID,
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
			"component", "analysis.processor.actions",
			"detection_id", a.CorrelationID,
			"species", a.Note.CommonName,
			"scientific_name", a.Note.ScientificName,
			"operation", "check_bird_image_cache")
		log.Printf("🟡 BirdImageCache is nil, cannot fetch image for %s", a.Note.ScientificName)
	}

	// Create a copy of the Note (source is already sanitized in SafeString field)
	noteCopy := a.Note

	// Wrap note with bird image (using copy)
	noteWithBirdImage := NoteWithBirdImage{Note: noteCopy, BirdImage: birdImage}

	// Create a JSON representation of the note
	noteJson, err := json.Marshal(noteWithBirdImage)
	if err != nil {
		// Add structured logging
		GetLogger().Error("Failed to marshal note to JSON",
			"component", "analysis.processor.actions",
			"detection_id", a.CorrelationID,
			"error", err,
			"species", a.Note.CommonName,
			"scientific_name", a.Note.ScientificName,
			"operation", "json_marshal")
		log.Printf("❌ Error marshalling note to JSON")
		return err
	}

	// Create a context with timeout for publishing
	ctx, cancel := context.WithTimeout(context.Background(), MQTTPublishTimeout)
	defer cancel()

	// Publish the note to the MQTT broker
	err = a.MqttClient.Publish(ctx, a.Settings.Realtime.MQTT.Topic, string(noteJson))
	if err != nil {
		// Log the error with retry information if retries are enabled
		// Sanitize error before logging
		sanitizedErr := sanitizeError(err)

		// Check if this is an EOF error which indicates connection was closed unexpectedly
		// This is a common issue with MQTT brokers and should be treated as retryable
		isEOFErr := isEOFError(err)

		// Add structured logging
		GetLogger().Error("Failed to publish to MQTT",
			"component", "analysis.processor.actions",
			"detection_id", a.CorrelationID,
			"error", sanitizedErr,
			"species", a.Note.CommonName,
			"scientific_name", a.Note.ScientificName,
			"confidence", a.Note.Confidence,
			"clip_name", a.Note.ClipName,
			"topic", a.Settings.Realtime.MQTT.Topic,
			"retry_enabled", a.RetryConfig.Enabled,
			"is_eof_error", isEOFErr,
			"operation", "mqtt_publish")
		if a.RetryConfig.Enabled {
			log.Printf("❌ Error publishing %s (%s) to MQTT topic %s (confidence: %.2f, clip: %s) (will retry): %v\n",
				a.Note.CommonName, a.Note.ScientificName, a.Settings.Realtime.MQTT.Topic, a.Note.Confidence, a.Note.ClipName, sanitizedErr)
		} else {
			log.Printf("❌ Error publishing %s (%s) to MQTT topic %s (confidence: %.2f, clip: %s): %v\n",
				a.Note.CommonName, a.Note.ScientificName, a.Settings.Realtime.MQTT.Topic, a.Note.Confidence, a.Note.ClipName, sanitizedErr)
			// Only send notification for non-EOF errors when retries are disabled
			// EOF errors are typically transient connection issues
			if !isEOFErr {
				notification.NotifyIntegrationFailure("MQTT", err)
			}
		}

		// Enhance error context with EOF detection
		enhancedErr := errors.New(err).
			Component("analysis.processor").
			Category(errors.CategoryMQTTPublish).
			Context("operation", "mqtt_publish").
			Context("species", a.Note.CommonName).
			Context("confidence", a.Note.Confidence).
			Context("topic", a.Settings.Realtime.MQTT.Topic).
			Context("clip_name", a.Note.ClipName).
			Context("integration", "mqtt").
			Context("retryable", true). // MQTT publish failures are typically retryable
			Context("is_eof_error", isEOFErr).
			Build()

		return enhancedErr
	}

	if a.Settings.Debug {
		// Add structured logging
		GetLogger().Debug("Successfully published to MQTT",
			"component", "analysis.processor.actions",
			"detection_id", a.CorrelationID,
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

	speciesName := strings.ToLower(a.Note.CommonName)

	// Check event frequency
	if !a.EventTracker.TrackEvent(speciesName, SSEBroadcast) {
		return nil
	}

	// FIXME: delay SSE broadcasts by sleeping here for a moment, this should be
	// fixed by proper synchronization of audio file writer, database ID assignment
	// and SSE broadcast.
	const sleepTime = 5 * time.Second
	time.Sleep(sleepTime)

	// Wait for audio file to be available if this detection has an audio clip assigned
	// This properly handles per-species audio settings and avoids false positives
	if a.Note.ClipName != "" {
		if err := a.waitForAudioFile(); err != nil {
			// Log warning but don't fail the SSE broadcast
			// Add structured logging
			GetLogger().Warn("Audio file not ready for SSE broadcast",
				"component", "analysis.processor.actions",
				"detection_id", a.CorrelationID,
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
				"component", "analysis.processor.actions",
				"detection_id", a.CorrelationID,
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
				"component", "analysis.processor.actions",
				"detection_id", a.CorrelationID,
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
			"component", "analysis.processor.actions",
			"detection_id", a.CorrelationID,
			"species", a.Note.CommonName,
			"scientific_name", a.Note.ScientificName,
			"operation", "check_bird_image_cache")
		log.Printf("🟡 BirdImageCache is nil, cannot fetch image for %s", a.Note.ScientificName)
	}

	// Create a copy of the Note (source is already sanitized in SafeString field)
	noteCopy := a.Note

	// Broadcast the detection with error handling
	if err := a.SSEBroadcaster(&noteCopy, &birdImage); err != nil {
		// Log the error with retry information if retries are enabled
		// Sanitize error before logging
		sanitizedErr := sanitizeError(err)
		// Add structured logging
		GetLogger().Error("Failed to broadcast via SSE",
			"component", "analysis.processor.actions",
			"detection_id", a.CorrelationID,
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
			"component", "analysis.processor.actions",
			"detection_id", a.CorrelationID,
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

	// Wait for file to be written
	deadline := time.Now().Add(SSEAudioFileTimeout)

	for time.Now().Before(deadline) {
		// Check if file exists and has content
		if info, err := os.Stat(audioPath); err == nil {
			// File exists, check if it has reasonable size
			if info.Size() > MinAudioFileSize {
				if a.Settings.Debug {
					// Add structured logging
					GetLogger().Debug("Audio file ready for SSE broadcast",
						"component", "analysis.processor.actions",
						"detection_id", a.CorrelationID,
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

		time.Sleep(SSEAudioCheckInterval)
	}

	// Timeout reached
	return errors.Newf("audio file %s not ready after %v timeout", a.Note.ClipName, SSEAudioFileTimeout).
		Component("analysis.processor").
		Category(errors.CategoryTimeout).
		Context("operation", "wait_for_audio_file").
		Context("clip_name", a.Note.ClipName).
		Context("timeout_seconds", SSEAudioFileTimeout.Seconds()).
		Build()
}

// waitForDatabaseID waits for the Note to be saved to database and ID assigned
func (a *SSEAction) waitForDatabaseID() error {
	// We need to query the database to find this note by unique characteristics
	// Since we don't have the ID yet, we'll search by time, species, and confidence
	deadline := time.Now().Add(SSEDatabaseIDTimeout)

	for time.Now().Before(deadline) {
		// Query database for a note matching our characteristics
		// Use a small time window around the detection time to find the record
		if updatedNote, err := a.findNoteInDatabase(); err == nil && updatedNote.ID > 0 {
			// Found the note with an ID, update our copy
			a.Note.ID = updatedNote.ID
			if a.Settings.Debug {
				// Add structured logging
				GetLogger().Debug("Found database ID for SSE broadcast",
					"component", "analysis.processor.actions",
					"detection_id", a.CorrelationID,
					"database_id", updatedNote.ID,
					"species", a.Note.CommonName,
					"scientific_name", a.Note.ScientificName,
					"operation", "wait_database_id_success")
				log.Printf("🔍 Found database ID %d for SSE broadcast: %s", updatedNote.ID, a.Note.CommonName)
			}
			return nil
		}

		time.Sleep(SSEDatabaseCheckInterval)
	}

	// Timeout reached
	return errors.Newf("database ID not assigned for %s after %v timeout", a.Note.CommonName, SSEDatabaseIDTimeout).
		Component("analysis.processor").
		Category(errors.CategoryTimeout).
		Context("operation", "wait_for_database_id").
		Context("species", a.Note.CommonName).
		Context("timeout_seconds", SSEDatabaseIDTimeout.Seconds()).
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
	notes, err := a.Ds.SearchNotes(query, false, DatabaseSearchLimit, 0) // false = sort descending, offset 0
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
