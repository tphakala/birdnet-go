// processor/actions.go

package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/analysis/species"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/birdweather"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/mqtt"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/privacy"
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
	// Set high enough to handle high-activity periods (e.g., dawn chorus)
	DatabaseSearchLimit = 50

	// CompositeActionTimeout is the default timeout for each action in a composite action
	// This is generous to accommodate slow hardware (e.g., Raspberry Pi with SD cards)
	CompositeActionTimeout = 30 * time.Second

	// ExecuteCommandTimeout is the timeout for external command execution
	ExecuteCommandTimeout = 5 * time.Minute
)

// DetectionContext provides thread-safe shared state for detection pipeline actions.
// This enables downstream actions (MQTT, SSE) to access data set by upstream actions
// (Database) without polling, when used with CompositeAction for sequential execution.
//
// The context is created once per detection in getActionsForItem() and shared among
// all actions that need access to the database-assigned detection ID.
type DetectionContext struct {
	// NoteID holds the database primary key after successful save.
	// Use atomic operations: Store() in DatabaseAction, Load() in MqttAction/SSEAction.
	NoteID atomic.Uint64

	// AudioExportFailed indicates that audio export failed in DatabaseAction.
	// When true, downstream actions should skip waiting for the audio file.
	// This prevents the 5-second timeout delay when audio export fails.
	AudioExportFailed atomic.Bool
}

// Action is the base interface for all actions that can be executed
type Action interface {
	Execute(data any) error
	GetDescription() string
}

// ContextAction is an enhanced action interface that supports context-aware execution
// This allows for proper cancellation and timeout propagation
type ContextAction interface {
	Action
	ExecuteContext(ctx context.Context, data any) error
}

type LogAction struct {
	Settings      *conf.Settings
	Result        detection.Result // New domain model for logging
	Note          datastore.Note   // Deprecated: kept for backward compat during transition
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
	PreRenderer       PreRendererSubmit       // Spectrogram pre-renderer
	DetectionCtx      *DetectionContext       // Shared context for downstream actions (MQTT, SSE)
	Description       string
	CorrelationID     string     // Detection correlation ID for log tracking
	mu                sync.Mutex // Protect concurrent access to Note and Results
}

type SaveAudioAction struct {
	Settings      *conf.Settings
	ClipName      string
	pcmData       []byte
	NoteID        uint              // Note ID for correlation logging with pre-renderer
	PreRenderer   PreRendererSubmit // Injected from processor
	EventTracker  *EventTracker
	Description   string
	CorrelationID string     // Detection correlation ID for log tracking
	mu            sync.Mutex // Protect concurrent access to pcmData
}

// PreRenderJob represents a spectrogram pre-rendering task.
// This is a local DTO to avoid direct coupling to spectrogram package types.
type PreRenderJob struct {
	PCMData   []byte    // Raw PCM data from memory (s16le, 48kHz, mono)
	ClipPath  string    // Full absolute path to audio clip file
	NoteID    uint      // For logging correlation
	Timestamp time.Time // Job submission time
}

// Methods to expose fields (allows prerenderer to access without importing processor)
func (j PreRenderJob) GetPCMData() []byte      { return j.PCMData }
func (j PreRenderJob) GetClipPath() string     { return j.ClipPath }
func (j PreRenderJob) GetNoteID() uint         { return j.NoteID }
func (j PreRenderJob) GetTimestamp() time.Time { return j.Timestamp }

// PreRendererSubmit is an interface for submitting pre-render jobs.
// Callers create PreRenderJob instances, and the implementation adapts them
// to spectrogram-specific types at the boundary.
type PreRendererSubmit interface {
	Submit(job interface {
		GetPCMData() []byte
		GetClipPath() string
		GetNoteID() uint
		GetTimestamp() time.Time
	}) error
	Stop() // Graceful shutdown
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
	DetectionCtx   *DetectionContext    // Shared context from DatabaseAction
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
	DetectionCtx   *DetectionContext    // Shared context from DatabaseAction
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

// getBirdImageFromCache retrieves a bird image from cache with proper error handling and logging.
// This helper consolidates duplicate image retrieval logic used by MqttAction and SSEAction.
// Returns an empty BirdImage if the cache is nil or if retrieval fails.
func getBirdImageFromCache(cache *imageprovider.BirdImageCache, scientificName, commonName, correlationID string) imageprovider.BirdImage {
	if cache == nil {
		GetLogger().Warn("BirdImageCache is nil, cannot fetch image",
			logger.String("detection_id", correlationID),
			logger.String("species", commonName),
			logger.String("scientific_name", scientificName),
			logger.String("operation", "check_bird_image_cache"))
		return imageprovider.BirdImage{}
	}

	birdImage, err := cache.Get(scientificName)
	if err != nil {
		GetLogger().Warn("Error getting bird image from cache",
			logger.String("detection_id", correlationID),
			logger.Error(err),
			logger.String("species", commonName),
			logger.String("scientific_name", scientificName),
			logger.String("operation", "get_bird_image"))
		return imageprovider.BirdImage{}
	}

	return birdImage
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
func (a *CompositeAction) Execute(data any) error {
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
func (a *CompositeAction) executeActionWithRecovery(action Action, data any, step, total int) error {
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
					logger.String("component", "analysis.processor.actions"),
					logger.String("detection_id", a.CorrelationID),
					logger.Int("step", step),
					logger.Int("total_steps", total),
					logger.String("action_description", action.GetDescription()),
					logger.Float64("timeout_seconds", timeout.Seconds()),
					logger.String("operation", "composite_action_timeout"))
				return timeoutErr
			}
			// Log other errors
			GetLogger().Error("Composite action failed",
				logger.String("component", "analysis.processor.actions"),
				logger.String("detection_id", a.CorrelationID),
				logger.Int("step", step),
				logger.Int("total_steps", total),
				logger.String("action_description", action.GetDescription()),
				logger.Error(res.err),
				logger.String("operation", "composite_action_execute"))
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
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.Int("step", step),
			logger.Int("total_steps", total),
			logger.String("action_description", action.GetDescription()),
			logger.Float64("timeout_seconds", timeout.Seconds()),
			logger.String("operation", "composite_action_context_timeout"))
		return timeoutErr
	}
}

// Execute logs the note to the chag log file
func (a *LogAction) Execute(data any) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Check if the event should be handled for this species (supports scientific name lookup)
	if !a.EventTracker.TrackEventWithNames(a.Result.Species.CommonName, a.Result.Species.ScientificName, LogToFile) {
		return nil
	}

	// Log detection result to file using new detection package
	if err := detection.LogToFile(a.Settings, &a.Result); err != nil {
		// If an error occurs when logging to a file, wrap and return the error.
		// Add structured logging
		GetLogger().Error("Failed to log detection to file",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.Error(err),
			logger.String("species", a.Result.Species.CommonName),
			logger.Float64("confidence", a.Result.Confidence),
			logger.String("clip_name", a.Result.ClipName),
			logger.String("operation", "log_to_file"))
	}
	// Add structured logging for console output
	GetLogger().Info("Detection logged",
		logger.String("component", "analysis.processor.actions"),
		logger.String("detection_id", a.CorrelationID),
		logger.String("species", a.Result.Species.CommonName),
		logger.Float64("confidence", a.Result.Confidence),
		logger.String("time", a.Result.Time()),
		logger.String("operation", "console_output"))
	fmt.Printf("%s %s %.2f\n", a.Result.Time(), a.Result.Species.CommonName, a.Result.Confidence)

	return nil
}

// Execute saves the note to the database
func (a *DatabaseAction) Execute(data any) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Check event frequency (supports scientific name lookup)
	if !a.EventTracker.TrackEventWithNames(a.Note.CommonName, a.Note.ScientificName, DatabaseSave) {
		return nil
	}

	// Check if this is a new species and update atomically to prevent race conditions
	var isNewSpecies bool
	var daysSinceFirstSeen int
	if a.NewSpeciesTracker != nil {
		// Use atomic check-and-update to prevent duplicate "new species" notifications
		// when multiple detections of the same species arrive concurrently
		isNewSpecies, daysSinceFirstSeen = a.NewSpeciesTracker.CheckAndUpdateSpecies(a.Note.ScientificName, a.Note.BeginTime)
	}

	// Save note to database
	if err := a.Ds.Save(&a.Note, a.Results); err != nil {
		// Add structured logging
		GetLogger().Error("Failed to save note and results to database",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.Error(err),
			logger.String("species", a.Note.CommonName),
			logger.String("scientific_name", a.Note.ScientificName),
			logger.Float64("confidence", a.Note.Confidence),
			logger.String("clip_name", a.Note.ClipName),
			logger.String("operation", "database_save"))
		return err
	}

	// Share the database ID with downstream actions (MQTT, SSE) immediately.
	// This must happen before audio export so downstream actions get the ID
	// even if audio export fails.
	if a.DetectionCtx != nil {
		a.DetectionCtx.NoteID.Store(uint64(a.Note.ID))
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
			logger.Time("begin_time", a.Note.BeginTime),
			logger.Time("end_time", a.Note.EndTime),
			logger.Int("capture_length", captureLength),
			logger.String("operation", "note_begin_end_capture_length"))

		// handleAudioExportError logs the error and signals downstream actions.
		// This helper reduces duplication between buffer read and save failures.
		handleAudioExportError := func(err error, extraFields ...logger.Field) {
			fields := []logger.Field{
				logger.String("component", "analysis.processor.actions"),
				logger.String("detection_id", a.CorrelationID),
				logger.Error(err),
				logger.String("species", a.Note.CommonName),
				logger.String("operation", "audio_export_non_fatal"),
			}
			fields = append(fields, extraFields...)
			GetLogger().Error("Audio export failed (continuing with detection broadcast)", fields...)

			// Signal to downstream actions that audio export failed
			// This prevents SSEAction from waiting 5 seconds for a file that won't appear
			if a.DetectionCtx != nil {
				a.DetectionCtx.AudioExportFailed.Store(true)
			}
		}

		// export audio clip from capture buffer
		pcmData, err := myaudio.ReadSegmentFromCaptureBuffer(a.Note.Source.ID, a.Note.BeginTime, captureLength)
		if err != nil {
			handleAudioExportError(err,
				logger.String("source", a.Note.Source.SafeString),
				logger.Time("begin_time", a.Note.BeginTime),
				logger.Int("duration_seconds", captureLength))
		} else {
			// Create a SaveAudioAction and execute it
			saveAudioAction := &SaveAudioAction{
				Settings:      a.Settings,
				ClipName:      a.Note.ClipName,
				pcmData:       pcmData,
				NoteID:        a.Note.ID,
				PreRenderer:   a.PreRenderer,
				CorrelationID: a.CorrelationID,
			}

			if err := saveAudioAction.Execute(nil); err != nil {
				handleAudioExportError(err, logger.String("clip_name", a.Note.ClipName))
			} else if a.Settings.Debug {
				// Add structured logging
				GetLogger().Debug("Saved audio clip successfully",
					logger.String("component", "analysis.processor.actions"),
					logger.String("detection_id", a.CorrelationID),
					logger.String("species", a.Note.CommonName),
					logger.String("clip_name", a.Note.ClipName),
					logger.String("detection_time", a.Note.Time),
					logger.Time("begin_time", a.Note.BeginTime),
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
		notificationTime = a.Note.BeginTime

		// Check if notification should be suppressed for this species
		if a.NewSpeciesTracker.ShouldSuppressNotification(a.Note.ScientificName, notificationTime) {
			if a.Settings.Debug {
				GetLogger().Debug("Suppressing duplicate new species notification",
					logger.String("component", "analysis.processor.actions"),
					logger.String("detection_id", a.CorrelationID),
					logger.String("species", a.Note.CommonName),
					logger.String("scientific_name", a.Note.ScientificName),
					logger.String("operation", "suppress_notification"))
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
				logger.String("component", "analysis.processor.actions"),
				logger.String("detection_id", a.CorrelationID),
				logger.Error(err),
				logger.String("species", a.Note.CommonName),
				logger.String("scientific_name", a.Note.ScientificName),
				logger.Bool("is_new_species", isNewSpecies),
				logger.Int("days_since_first_seen", daysSinceFirstSeen),
				logger.String("operation", "create_detection_event"))
		}
		return
	}

	// Add location, time, and note ID to metadata for template rendering
	// Only add metadata if the map is non-nil to prevent panic
	metadata := detectionEvent.GetMetadata()
	if metadata != nil {
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
	} else {
		// Log error if metadata is nil (shouldn't happen in normal operation)
		GetLogger().Error("Detection event metadata is nil",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.String("species", a.Note.CommonName),
			logger.String("scientific_name", a.Note.ScientificName),
			logger.String("operation", "publish_detection_event"))
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
				logger.String("component", "analysis.processor.actions"),
				logger.String("detection_id", a.CorrelationID),
				logger.String("species", a.Note.CommonName),
				logger.String("scientific_name", a.Note.ScientificName),
				logger.Float64("confidence", a.Note.Confidence),
				logger.Bool("is_new_species", isNewSpecies),
				logger.Int("days_since_first_seen", daysSinceFirstSeen),
				logger.String("operation", "publish_detection_event"))
		}
	}
}

// Execute saves the audio clip to a file
func (a *SaveAudioAction) Execute(data any) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Get the full path by joining the export path with the relative clip name
	outputPath := filepath.Join(a.Settings.Realtime.Audio.Export.Path, a.ClipName)

	// Ensure the directory exists
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o750); err != nil {
		// Add structured logging
		GetLogger().Error("Failed to create directory for audio clip",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.Error(err),
			logger.String("output_path", outputPath),
			logger.String("clip_name", a.ClipName),
			logger.String("operation", "create_directory"))
		return err
	}

	if a.Settings.Realtime.Audio.Export.Type == "wav" {
		if err := myaudio.SavePCMDataToWAV(outputPath, a.pcmData); err != nil {
			// Add structured logging
			GetLogger().Error("Failed to save audio clip to WAV",
				logger.String("component", "analysis.processor.actions"),
				logger.String("detection_id", a.CorrelationID),
				logger.Error(err),
				logger.String("output_path", outputPath),
				logger.String("clip_name", a.ClipName),
				logger.String("format", "wav"),
				logger.String("operation", "save_wav"))
			return err
		}
	} else {
		if err := myaudio.ExportAudioWithFFmpeg(a.pcmData, outputPath, &a.Settings.Realtime.Audio); err != nil {
			// Add structured logging
			GetLogger().Error("Failed to export audio clip with FFmpeg",
				logger.String("component", "analysis.processor.actions"),
				logger.String("detection_id", a.CorrelationID),
				logger.Error(err),
				logger.String("output_path", outputPath),
				logger.String("clip_name", a.ClipName),
				logger.String("format", a.Settings.Realtime.Audio.Export.Type),
				logger.String("operation", "ffmpeg_export"))
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

// Execute sends the note to the BirdWeather API
func (a *BirdWeatherAction) Execute(data any) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Check event frequency (supports scientific name lookup)
	if !a.EventTracker.TrackEventWithNames(a.Note.CommonName, a.Note.ScientificName, BirdWeatherSubmit) {
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
				logger.String("component", "analysis.processor.actions"),
				logger.String("detection_id", a.CorrelationID),
				logger.String("species", a.Note.CommonName),
				logger.Float64("confidence", a.Note.Confidence),
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

	// Copy data locally to reduce lock duration if needed
	note := a.Note
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
				logger.String("species", note.CommonName),
				logger.String("scientific_name", note.ScientificName),
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
			logger.String("species", note.CommonName),
			logger.String("scientific_name", note.ScientificName),
			logger.Float64("confidence", note.Confidence),
			logger.String("clip_name", note.ClipName),
			logger.Bool("retry_enabled", a.RetryConfig.Enabled),
			logger.String("operation", "birdweather_upload"))
		if !a.RetryConfig.Enabled {
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
		GetLogger().Debug("Successfully uploaded to BirdWeather",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.String("species", a.Note.CommonName),
			logger.String("scientific_name", a.Note.ScientificName),
			logger.Float64("confidence", a.Note.Confidence),
			logger.String("clip_name", a.Note.ClipName),
			logger.String("operation", "birdweather_upload_success"))
	}
	return nil
}

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

// Execute sends the note to the MQTT broker
func (a *MqttAction) Execute(data any) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Rely on background reconnect; fail action if not currently connected.
	if !a.MqttClient.IsConnected() {
		// Log slightly differently to indicate it's waiting for background reconnect
		GetLogger().Warn("MQTT client not connected, skipping publish",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.String("species", a.Note.CommonName),
			logger.String("scientific_name", a.Note.ScientificName),
			logger.Float64("confidence", a.Note.Confidence),
			logger.String("operation", "mqtt_connection_check"),
			logger.String("status", "waiting_reconnect"))
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

	// Check event frequency (supports scientific name lookup)
	if !a.EventTracker.TrackEventWithNames(a.Note.CommonName, a.Note.ScientificName, MQTTPublish) {
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
	birdImage := getBirdImageFromCache(a.BirdImageCache, a.Note.ScientificName, a.Note.CommonName, a.CorrelationID)

	// Get detection ID from shared context (set by DatabaseAction in CompositeAction sequence)
	var detectionID uint
	if a.DetectionCtx != nil {
		detectionID = uint(a.DetectionCtx.NoteID.Load())
	}

	// Create a copy of the Note (source is already sanitized in SafeString field)
	noteCopy := a.Note

	// Update the Note's ID field for consistency in embedded JSON
	if detectionID > 0 {
		noteCopy.ID = detectionID
	}

	// Wrap note with bird image (using copy) and include detection ID and SourceID
	noteWithBirdImage := NoteWithBirdImage{
		Note:        noteCopy,
		DetectionID: detectionID, // Explicit field for URL construction (e.g., /api/v2/audio/{id})
		SourceID:    noteCopy.Source.ID,
		BirdImage:   birdImage,
	}

	// Create a JSON representation of the note
	noteJson, err := json.Marshal(noteWithBirdImage)
	if err != nil {
		GetLogger().Error("Failed to marshal note to JSON",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.Error(err),
			logger.String("species", a.Note.CommonName),
			logger.String("scientific_name", a.Note.ScientificName),
			logger.String("operation", "json_marshal"))
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
		sanitizedErr := privacy.WrapError(err)

		// Check if this is an EOF error which indicates connection was closed unexpectedly
		// This is a common issue with MQTT brokers and should be treated as retryable
		isEOFErr := isEOFError(err)

		GetLogger().Error("Failed to publish to MQTT",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.Error(sanitizedErr),
			logger.String("species", a.Note.CommonName),
			logger.String("scientific_name", a.Note.ScientificName),
			logger.Float64("confidence", a.Note.Confidence),
			logger.String("clip_name", a.Note.ClipName),
			logger.String("topic", a.Settings.Realtime.MQTT.Topic),
			logger.Bool("retry_enabled", a.RetryConfig.Enabled),
			logger.Bool("is_eof_error", isEOFErr),
			logger.String("operation", "mqtt_publish"))
		// Only send notification for non-EOF errors when retries are disabled
		// EOF errors are typically transient connection issues
		if !a.RetryConfig.Enabled && !isEOFErr {
			notification.NotifyIntegrationFailure("MQTT", err)
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
		GetLogger().Debug("Successfully published to MQTT",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.String("species", a.Note.CommonName),
			logger.String("scientific_name", a.Note.ScientificName),
			logger.Float64("confidence", a.Note.Confidence),
			logger.String("topic", a.Settings.Realtime.MQTT.Topic),
			logger.String("operation", "mqtt_publish_success"))
	}
	return nil
}

// Execute updates the range filter species list, this is run every day
// Note: The ShouldUpdateRangeFilterToday() check in processor.go ensures this action
// is only created once per day, preventing duplicate concurrent updates (GitHub issue #1357)
func (a *UpdateRangeFilterAction) Execute(data any) error {
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
			logger.String("date", today.Format("2006-01-02")),
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
			logger.String("date", today.Format("2006-01-02")),
			logger.String("operation", "update_range_filter_success"))
	}

	return nil
}

// Execute broadcasts the detection via Server-Sent Events
func (a *SSEAction) Execute(data any) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Check if SSE broadcaster is available
	if a.SSEBroadcaster == nil {
		return nil // Silently skip if no broadcaster is configured
	}

	// Check event frequency (supports scientific name lookup)
	if !a.EventTracker.TrackEventWithNames(a.Note.CommonName, a.Note.ScientificName, SSEBroadcast) {
		return nil
	}

	// Get detection ID from shared context (set by DatabaseAction in CompositeAction sequence).
	// This replaces the old polling-based waitForDatabaseID() approach with proper synchronization.
	if a.DetectionCtx != nil {
		noteID := uint(a.DetectionCtx.NoteID.Load())
		if noteID > 0 {
			a.Note.ID = noteID
		}
	}

	// Fallback to polling if DetectionCtx wasn't set (legacy compatibility)
	// This should only happen if SSEAction runs outside of CompositeAction
	if a.Note.ID == 0 && a.DetectionCtx == nil {
		if err := a.waitForDatabaseID(); err != nil {
			// Cannot broadcast without database ID - frontend would fail to load audio/spectrogram
			// Skip SSE broadcast gracefully - the detection is still saved and will appear on page refresh
			GetLogger().Warn("Skipping SSE broadcast - database ID not available within timeout",
				logger.String("component", "analysis.processor.actions"),
				logger.String("detection_id", a.CorrelationID),
				logger.String("species", a.Note.CommonName),
				logger.Error(err),
				logger.Duration("timeout", SSEDatabaseIDTimeout),
				logger.String("operation", "sse_broadcast_skipped"))
			return nil // Not an error - graceful degradation
		}
	}

	// Wait for audio file to be available if this detection has an audio clip assigned
	// AND audio export didn't fail in DatabaseAction.
	// This properly handles per-species audio settings and avoids false positives.
	if a.Note.ClipName != "" {
		// Skip waiting if audio export failed - the file will never appear
		// This prevents a 5-second timeout delay when DatabaseAction couldn't export audio
		if a.DetectionCtx != nil && a.DetectionCtx.AudioExportFailed.Load() {
			GetLogger().Debug("Skipping audio file wait - export failed in DatabaseAction",
				logger.String("component", "analysis.processor.actions"),
				logger.String("detection_id", a.CorrelationID),
				logger.String("species", a.Note.CommonName),
				logger.String("clip_name", a.Note.ClipName),
				logger.String("operation", "sse_skip_audio_wait"))
		} else if err := a.waitForAudioFile(); err != nil {
			// Log warning but don't fail the SSE broadcast
			GetLogger().Warn("Audio file not ready for SSE broadcast",
				logger.String("component", "analysis.processor.actions"),
				logger.String("detection_id", a.CorrelationID),
				logger.Error(err),
				logger.String("species", a.Note.CommonName),
				logger.String("clip_name", a.Note.ClipName),
				logger.String("operation", "sse_wait_audio_file"))
		}
	}

	// Get bird image of detected bird using the shared helper
	birdImage := getBirdImageFromCache(a.BirdImageCache, a.Note.ScientificName, a.Note.CommonName, a.CorrelationID)

	// Create a copy of the Note (source is already sanitized in SafeString field)
	noteCopy := a.Note

	// Broadcast the detection with error handling
	if err := a.SSEBroadcaster(&noteCopy, &birdImage); err != nil {
		// Log the error with retry information if retries are enabled
		// Sanitize error before logging
		sanitizedErr := privacy.WrapError(err)
		GetLogger().Error("Failed to broadcast via SSE",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.Error(sanitizedErr),
			logger.String("species", a.Note.CommonName),
			logger.String("scientific_name", a.Note.ScientificName),
			logger.Float64("confidence", a.Note.Confidence),
			logger.String("clip_name", a.Note.ClipName),
			logger.Bool("retry_enabled", a.RetryConfig.Enabled),
			logger.String("operation", "sse_broadcast"))
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
		GetLogger().Debug("Successfully broadcasted via SSE",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.String("species", a.Note.CommonName),
			logger.String("scientific_name", a.Note.ScientificName),
			logger.Float64("confidence", a.Note.Confidence),
			logger.String("clip_name", a.Note.ClipName),
			logger.String("operation", "sse_broadcast_success"))
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
					GetLogger().Debug("Audio file ready for SSE broadcast",
						logger.String("component", "analysis.processor.actions"),
						logger.String("detection_id", a.CorrelationID),
						logger.String("clip_name", a.Note.ClipName),
						logger.Int64("file_size_bytes", info.Size()),
						logger.String("species", a.Note.CommonName),
						logger.String("operation", "wait_audio_file_success"))
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

// waitForDatabaseID waits for the Note to be saved to database and ID assigned.
//
// Deprecated: This polling-based approach is a legacy fallback. When SSEAction is
// created via getDefaultActions(), it always receives a DetectionContext which
// provides the database ID without polling via atomic.Uint64. This method is retained
// for backward compatibility with custom action configurations that may create SSEAction
// without a DetectionContext. Consider removing in a future major version.
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
				GetLogger().Debug("Found database ID for SSE broadcast",
					logger.String("component", "analysis.processor.actions"),
					logger.String("detection_id", a.CorrelationID),
					logger.Any("database_id", updatedNote.ID),
					logger.String("species", a.Note.CommonName),
					logger.String("scientific_name", a.Note.ScientificName),
					logger.String("operation", "wait_database_id_success"))
			}
			return nil
		}

		time.Sleep(SSEDatabaseCheckInterval)
	}

	// Timeout reached - intentionally using fmt.Errorf instead of errors.Newf to avoid
	// triggering user notifications. This is a transient timing issue, not a user-actionable
	// problem. The structured error system converts errors to notifications, which we want
	// to avoid for this non-critical timeout.
	// Note: Species name is NOT included in error message to prevent log injection.
	// The species is already captured in structured log fields by the caller.
	return fmt.Errorf("database ID not found after %v", SSEDatabaseIDTimeout)
}

// findNoteInDatabase searches for the note in database by unique characteristics.
//
// Deprecated: This is a helper for the deprecated waitForDatabaseID() method.
// See waitForDatabaseID() deprecation notice for details.
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

	// Filter results to find the exact match based on BeginTime and source
	// Using BeginTime (time.Time) is more robust than Date+Time strings as it's the
	// actual detection timestamp and avoids string formatting/precision issues
	for i := range notes {
		note := &notes[i]
		// Check if this note matches our expected characteristics
		// Include SourceNode to prevent matching wrong detection in high-activity periods
		// Only compare SourceNode if both have values (handles legacy data)
		sourceMatches := a.Note.SourceNode == "" || note.SourceNode == "" || note.SourceNode == a.Note.SourceNode
		// Truncate to millisecond precision for comparison to handle potential database precision loss
		if note.ScientificName == a.Note.ScientificName &&
			note.BeginTime.Truncate(time.Millisecond).Equal(a.Note.BeginTime.Truncate(time.Millisecond)) &&
			sourceMatches {
			return note, nil
		}
	}

	return nil, errors.Newf("note not found in database").
		Component("analysis.processor").
		Category(errors.CategoryNotFound).
		Context("operation", "find_note_in_database").
		Context("species", a.Note.ScientificName).
		Context("begin_time", a.Note.BeginTime.Format("2006-01-02 15:04:05.000")).
		Build()
}
