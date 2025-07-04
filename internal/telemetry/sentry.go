// Package telemetry provides privacy-compliant error tracking and telemetry
package telemetry

import (
	"fmt"
	"log"
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
			// Remove any potentially sensitive information
			event.User = sentry.User{} // Clear user data
			event.ServerName = ""      // Ensure server name is not included

			// Clear sensitive contexts while preserving our privacy-safe platform info
			if event.Contexts != nil {
				// Remove potentially sensitive default Sentry contexts
				delete(event.Contexts, "device")  // Contains detailed device info
				delete(event.Contexts, "os")      // Contains detailed OS info
				delete(event.Contexts, "runtime") // Contains detailed runtime info
				// Keep our custom "platform" and "application" contexts as they're privacy-safe
			}

			// Only keep essential error information
			for k := range event.Extra {
				// Remove any extra data that might contain sensitive info
				if k != "error_type" && k != "component" {
					delete(event.Extra, k)
				}
			}

			// Remove hostname from tags if present
			if event.Tags != nil {
				delete(event.Tags, "server_name")
				delete(event.Tags, "hostname")
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

	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", component)
		scope.SetLevel(level)
		sentry.CaptureMessage(scrubbedMessage)
	})
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

