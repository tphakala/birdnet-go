// Package telemetry provides privacy-compliant error tracking and telemetry
package telemetry

import (
	"fmt"
	"log"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// DeferredMessage represents a message that was captured before Sentry initialization
type DeferredMessage struct {
	Message   string
	Level     sentry.Level
	Component string
	Timestamp time.Time
}

// sentryInitialized tracks whether Sentry has been initialized
var (
	sentryInitialized  bool
	deferredMessages   []DeferredMessage
	deferredMutex      sync.Mutex
	attachmentUploader *AttachmentUploader
	testMode           int32 // testMode allows tests to bypass settings checks (0=false, 1=true)
)

// PlatformInfo holds privacy-safe platform information for telemetry
type PlatformInfo struct {
	OS           string `json:"os"`
	Architecture string `json:"arch"`
	Container    bool   `json:"container"`
	BoardModel   string `json:"board_model,omitempty"`
	NumCPU       int    `json:"num_cpu"`
	GoVersion    string `json:"go_version"`
}

// collectPlatformInfo gathers privacy-safe platform information for telemetry
func collectPlatformInfo() PlatformInfo {
	info := PlatformInfo{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
		Container:    conf.RunningInContainer(),
		NumCPU:       runtime.NumCPU(),
		GoVersion:    runtime.Version(),
	}

	// Only collect board model for ARM64 Linux systems (SBCs like Raspberry Pi)
	// This helps understand deployment on edge devices without privacy concerns
	if conf.IsLinuxArm64() {
		if boardModel := conf.GetBoardModel(); boardModel != "" {
			info.BoardModel = boardModel
		}
	}

	return info
}

// InitSentry initializes Sentry SDK with privacy-compliant settings
// This function will only initialize Sentry if explicitly enabled by the user
func InitSentry(settings *conf.Settings) error {
	// Check if Sentry is explicitly enabled (opt-in)
	if !settings.Sentry.Enabled {
		log.Println("Sentry telemetry is disabled (opt-in required)")
		return nil
	}

	// Enable debug logging if configured
	if settings.Sentry.Debug {
		serviceLevelVar.Set(slog.LevelDebug)
		serviceLogger.Info("telemetry debug logging enabled")
	}

	// Use hardcoded DSN for BirdNET-Go project
	const sentryDSN = "https://b9269b6c0f8fae154df65be5a97e0435@o4509553065525248.ingest.de.sentry.io/4509553112186960"

	// Initialize Sentry with privacy-compliant options
	err := sentry.Init(sentry.ClientOptions{
		Dsn:        sentryDSN,
		SampleRate: 1.0,   // Capture all errors by default
		Debug:      false, // Keep debug off for production

		// Privacy-compliant settings
		AttachStacktrace: false, // Don't attach stack traces by default
		Environment:      "production",
		ServerName:       "", // Explicitly clear server name to prevent hostname leakage

		// Set release version if available
		Release: fmt.Sprintf("birdnet-go@%s", settings.Version),

		// BeforeSend allows us to filter sensitive data
		BeforeSend: func(event *sentry.Event, hint *sentry.EventHint) *sentry.Event {
			// Log what privacy filters are being applied if debug is enabled
			if serviceLogger != nil && settings.Sentry.Debug {
				var filtersApplied []string

				// Log before filtering
				serviceLogger.Debug("applying privacy filters to event",
					"event_id", event.EventID,
					"has_user_data", !event.User.IsEmpty(),
					"has_server_name", event.ServerName != "",
					"contexts_count", len(event.Contexts),
					"extra_count", len(event.Extra),
					"tags_count", len(event.Tags),
				)

				// Apply filters and track what was removed
				if !event.User.IsEmpty() {
					filtersApplied = append(filtersApplied, "remove_user_data")
				}
				if event.ServerName != "" {
					filtersApplied = append(filtersApplied, "remove_server_name")
				}

				// Remove any potentially sensitive information
				event.User = sentry.User{} // Clear user data
				event.ServerName = ""      // Ensure server name is not included

				// Clear sensitive contexts while preserving our privacy-safe platform info
				if event.Contexts != nil {
					// Track which contexts we're removing
					for key := range event.Contexts {
						if key == "device" || key == "os" || key == "runtime" {
							filtersApplied = append(filtersApplied, fmt.Sprintf("remove_%s_context", key))
						}
					}

					// Remove potentially sensitive default Sentry contexts
					delete(event.Contexts, "device")  // Contains detailed device info
					delete(event.Contexts, "os")      // Contains detailed OS info
					delete(event.Contexts, "runtime") // Contains detailed runtime info
					// Keep our custom "platform" and "application" contexts as they're privacy-safe
				}

				// Track extra fields being removed
				extraRemoved := 0
				for k := range event.Extra {
					// Remove any extra data that might contain sensitive info
					if k != "error_type" && k != "component" {
						extraRemoved++
						delete(event.Extra, k)
					}
				}
				if extraRemoved > 0 {
					filtersApplied = append(filtersApplied, fmt.Sprintf("remove_%d_extra_fields", extraRemoved))
				}

				// Remove hostname from tags if present
				if event.Tags != nil {
					if _, hasServerName := event.Tags["server_name"]; hasServerName {
						filtersApplied = append(filtersApplied, "remove_server_name_tag")
						delete(event.Tags, "server_name")
					}
					if _, hasHostname := event.Tags["hostname"]; hasHostname {
						filtersApplied = append(filtersApplied, "remove_hostname_tag")
						delete(event.Tags, "hostname")
					}
				}

				// Log after filtering
				serviceLogger.Debug("privacy filters applied",
					"event_id", event.EventID,
					"filters_applied", filtersApplied,
					"remaining_contexts", len(event.Contexts),
					"remaining_extra", len(event.Extra),
					"remaining_tags", len(event.Tags),
				)
			} else {
				// Apply filters without logging when debug is disabled
				event.User = sentry.User{}
				event.ServerName = ""

				if event.Contexts != nil {
					delete(event.Contexts, "device")
					delete(event.Contexts, "os")
					delete(event.Contexts, "runtime")
				}

				for k := range event.Extra {
					if k != "error_type" && k != "component" {
						delete(event.Extra, k)
					}
				}

				if event.Tags != nil {
					delete(event.Tags, "server_name")
					delete(event.Tags, "hostname")
				}
			}

			return event
		},
	})

	if err != nil {
		return fmt.Errorf("sentry initialization failed: %w", err)
	}

	// Collect platform information for telemetry
	platformInfo := collectPlatformInfo()

	// Configure global scope with system ID and platform information
	sentry.ConfigureScope(func(scope *sentry.Scope) {
		// Set system ID as a tag for all events
		scope.SetTag("system_id", settings.SystemID)

		// Set platform tags for easy filtering in Sentry
		scope.SetTag("os", platformInfo.OS)
		scope.SetTag("arch", platformInfo.Architecture)
		scope.SetTag("container", fmt.Sprintf("%t", platformInfo.Container))
		if platformInfo.BoardModel != "" {
			scope.SetTag("board_model", platformInfo.BoardModel)
		}

		// Set application context
		scope.SetContext("application", map[string]any{
			"name":      "BirdNET-Go",
			"version":   settings.Version,
			"system_id": settings.SystemID,
		})

		// Set platform context for detailed telemetry
		scope.SetContext("platform", map[string]any{
			"os":           platformInfo.OS,
			"architecture": platformInfo.Architecture,
			"container":    platformInfo.Container,
			"board_model":  platformInfo.BoardModel,
			"num_cpu":      platformInfo.NumCPU,
			"go_version":   platformInfo.GoVersion,
		})
	})

	// Initialize attachment uploader
	attachmentUploader = NewAttachmentUploader(true)

	// Mark Sentry as initialized and process any deferred messages
	deferredMutex.Lock()
	sentryInitialized = true
	messagesToProcess := make([]DeferredMessage, len(deferredMessages))
	copy(messagesToProcess, deferredMessages)
	deferredMessages = nil // Clear the deferred messages
	deferredMutex.Unlock()

	// Process any messages that were captured before Sentry was ready
	for _, msg := range messagesToProcess {
		CaptureMessage(msg.Message, msg.Level, msg.Component)
	}

	// Log initialization success with debug details
	if serviceLogger != nil {
		serviceLogger.Info("Sentry telemetry initialized",
			"system_id", settings.SystemID,
			"version", settings.Version,
			"debug", settings.Sentry.Debug,
			"platform", platformInfo.OS,
			"arch", platformInfo.Architecture,
			"deferred_messages", len(messagesToProcess),
		)
	}

	if len(messagesToProcess) > 0 {
		log.Printf("Sentry telemetry initialized successfully, processed %d deferred messages (System ID: %s)",
			len(messagesToProcess), settings.SystemID)
	} else {
		log.Printf("Sentry telemetry initialized successfully (opt-in enabled, System ID: %s)", settings.SystemID)
	}

	// Initialize event bus integration for async telemetry
	if err := InitializeEventBusIntegration(); err != nil {
		log.Printf("Failed to initialize telemetry event bus integration: %v", err)
		// Don't fail initialization if event bus integration fails
		// Telemetry will still work synchronously
	}

	return nil
}

// CaptureError captures an error with privacy-compliant context
func CaptureError(err error, component string) {
	// Skip settings check in test mode
	if atomic.LoadInt32(&testMode) == 0 {
		settings := conf.GetSettings()
		if settings == nil || !settings.Sentry.Enabled {
			return
		}
	}

	// Create a scrubbed error for privacy
	scrubbedErrorMsg := privacy.ScrubMessage(err.Error())

	// Log the error being sent (privacy-safe)
	if serviceLogger != nil {
		serviceLogger.Debug("sending error event",
			"event_type", "error",
			"component", component,
			"error_type", fmt.Sprintf("%T", err),
			"scrubbed_message", scrubbedErrorMsg,
		)
	}

	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", component)
		scope.SetContext("error", map[string]any{
			"type":             fmt.Sprintf("%T", err),
			"scrubbed_message": scrubbedErrorMsg,
		})

		// Create a new error with scrubbed message to avoid exposing sensitive data
		scrubbedErr := fmt.Errorf("%s", scrubbedErrorMsg)
		sentry.CaptureException(scrubbedErr)
	})

	// Log successful submission
	if serviceLogger != nil {
		serviceLogger.Debug("error event sent successfully",
			"component", component,
		)
	}
}

// CaptureMessage captures a message with privacy-compliant context
func CaptureMessage(message string, level sentry.Level, component string) {
	// Skip settings check in test mode
	if atomic.LoadInt32(&testMode) == 0 {
		settings := conf.GetSettings()
		if settings == nil || !settings.Sentry.Enabled {
			return
		}
	}

	// Scrub sensitive information from the message
	scrubbedMessage := privacy.ScrubMessage(message)

	// Log the message being sent (privacy-safe)
	if serviceLogger != nil {
		serviceLogger.Debug("sending message event",
			"event_type", "message",
			"level", string(level),
			"component", component,
			"scrubbed_message", scrubbedMessage,
		)
	}

	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", component)
		scope.SetLevel(level)
		sentry.CaptureMessage(scrubbedMessage)
	})

	// Log successful submission
	if serviceLogger != nil {
		serviceLogger.Debug("message event sent successfully",
			"component", component,
			"level", string(level),
		)
	}
}

// CaptureMessageDeferred captures a message for later processing if Sentry is not yet initialized
// If Sentry is already initialized, it immediately sends the message
func CaptureMessageDeferred(message string, level sentry.Level, component string) {
	// Skip settings check in test mode
	if atomic.LoadInt32(&testMode) == 0 {
		settings := conf.GetSettings()
		if settings == nil || !settings.Sentry.Enabled {
			return
		}
	}

	deferredMutex.Lock()
	defer deferredMutex.Unlock()

	if sentryInitialized {
		// Sentry is already initialized, send immediately
		CaptureMessage(message, level, component)
		return
	}

	// Sentry not yet initialized, store for later processing
	deferredMessage := DeferredMessage{
		Message:   message,
		Level:     level,
		Component: component,
		Timestamp: time.Now(),
	}

	deferredMessages = append(deferredMessages, deferredMessage)

	// Log deferred message
	if serviceLogger != nil {
		scrubbedMessage := privacy.ScrubMessage(message)
		serviceLogger.Debug("deferring message for later processing",
			"event_type", "deferred_message",
			"level", string(level),
			"component", component,
			"scrubbed_message", scrubbedMessage,
			"deferred_count", len(deferredMessages),
		)
	}
}

// Flush ensures all buffered events are sent to Sentry
func Flush(timeout time.Duration) {
	// Skip settings check in test mode
	if atomic.LoadInt32(&testMode) == 0 {
		settings := conf.GetSettings()
		if settings == nil || !settings.Sentry.Enabled {
			return
		}
	}

	sentry.Flush(timeout)
}

// GetAttachmentUploader returns the global attachment uploader instance
func GetAttachmentUploader() *AttachmentUploader {
	deferredMutex.Lock()
	defer deferredMutex.Unlock()

	if attachmentUploader == nil {
		// Create a disabled uploader if Sentry is not initialized
		attachmentUploader = NewAttachmentUploader(false)
	}

	return attachmentUploader
}
