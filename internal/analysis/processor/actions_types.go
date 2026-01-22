// processor/actions_types.go
// This file contains interfaces, types, and constants for processor actions.

package processor

import (
	"context"
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
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/mqtt"
)

// Timeout and interval constants
const (
	// SSEAudioFileTimeout is the maximum time to wait for audio file to be written
	SSEAudioFileTimeout = 5 * time.Second

	// SSEAudioCheckInterval is how often to check for audio file
	SSEAudioCheckInterval = 100 * time.Millisecond

	// MinAudioFileSize is the minimum size in bytes for a valid audio file
	// Typed as int64 to match os.FileInfo.Size() return type
	MinAudioFileSize int64 = 1024

	// MQTTPublishTimeout is the timeout for MQTT publish operations
	MQTTPublishTimeout = 10 * time.Second

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

// Action is the base interface for all actions that can be executed.
// The context parameter allows for cancellation and timeout propagation.
type Action interface {
	Execute(ctx context.Context, data any) error
	GetDescription() string
}

// ContextAction is deprecated but kept for backward compatibility.
// Action now includes context directly.
type ContextAction interface {
	Action
	ExecuteContext(ctx context.Context, data any) error
}

type LogAction struct {
	Settings      *conf.Settings
	Result        detection.Result // Domain model (single source of truth)
	EventTracker  *EventTracker
	Description   string
	CorrelationID string     // Detection correlation ID for log tracking
	mu            sync.Mutex // Protect concurrent access to Result
}

type DatabaseAction struct {
	Settings          *conf.Settings
	Ds                datastore.Interface           // Legacy - to be removed after migration
	Repo              datastore.DetectionRepository // New - preferred for database operations
	Result            detection.Result              // Domain model (single source of truth)
	Results           []detection.AdditionalResult  // Secondary predictions (converted to legacy format at save time)
	EventTracker      *EventTracker
	NewSpeciesTracker *species.SpeciesTracker // Add reference to new species tracker
	processor         *Processor              // Add reference to processor for source name resolution
	PreRenderer       PreRendererSubmit       // Spectrogram pre-renderer
	DetectionCtx      *DetectionContext       // Shared context for downstream actions (MQTT, SSE)
	Description       string
	CorrelationID     string     // Detection correlation ID for log tracking
	mu                sync.Mutex // Protect concurrent access to Result and Results
}

type SaveAudioAction struct {
	Settings      *conf.Settings
	ClipName      string
	pcmData       []byte
	NoteID        uint              // Note ID for correlation logging with pre-renderer
	PreRenderer   PreRendererSubmit // Injected from processor
	EventTracker  *EventTracker
	Description   string
	CorrelationID string // Detection correlation ID for log tracking
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
	Result        detection.Result // Domain model (single source of truth)
	pcmData       []byte
	BwClient      *birdweather.BwClient
	EventTracker  *EventTracker
	RetryConfig   jobqueue.RetryConfig // Configuration for retry behavior
	Description   string
	CorrelationID string     // Detection correlation ID for log tracking
	mu            sync.Mutex // Protect concurrent access to Result and pcmData
}

type MqttAction struct {
	Settings       *conf.Settings
	Result         detection.Result // Domain model (single source of truth)
	BirdImageCache *imageprovider.BirdImageCache
	MqttClient     mqtt.Client
	EventTracker   *EventTracker
	DetectionCtx   *DetectionContext    // Shared context from DatabaseAction
	RetryConfig    jobqueue.RetryConfig // Configuration for retry behavior
	Description    string
	CorrelationID  string     // Detection correlation ID for log tracking
	mu             sync.Mutex // Protect concurrent access to Result
}

type UpdateRangeFilterAction struct {
	Bn          *birdnet.BirdNET
	Settings    *conf.Settings
	Description string
	mu          sync.Mutex // Protect concurrent access to Settings
}

type SSEAction struct {
	Settings       *conf.Settings
	Result         detection.Result // Domain model (single source of truth)
	BirdImageCache *imageprovider.BirdImageCache
	EventTracker   *EventTracker
	DetectionCtx   *DetectionContext    // Shared context from DatabaseAction (provides database ID)
	RetryConfig    jobqueue.RetryConfig // Configuration for retry behavior
	Description    string
	CorrelationID  string     // Detection correlation ID for log tracking
	mu             sync.Mutex // Protect concurrent access to Result
	// SSEBroadcaster is a function that broadcasts detection data
	// This allows the action to be independent of the specific API implementation
	SSEBroadcaster func(note *datastore.Note, birdImage *imageprovider.BirdImage) error
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
