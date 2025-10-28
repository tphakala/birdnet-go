// Package telemetry provides privacy-compliant error tracking and telemetry
package telemetry

import (
	"fmt"
	"log"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

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
		enableDebugLogging()
	}

	// Initialize Sentry SDK
	if err := initializeSentrySDK(settings); err != nil {
		return err
	}

	// Configure global scope
	configureSentryScope(settings)

	// Initialize attachment uploader
	attachmentUploader = NewAttachmentUploader(true)

	// Process deferred messages
	deferredCount := processDeferredMessages()

	// Log initialization success
	logInitializationSuccess(settings, deferredCount)

	// Event bus integration is deferred until after core services are initialized
	// to avoid circular dependencies and ensure proper logging
	
	return nil
}

// enableDebugLogging enables debug logging for telemetry
func enableDebugLogging() {
	serviceLevelVar.Set(slog.LevelDebug)
	logTelemetryInfo(nil, "telemetry debug logging enabled")
}

// initializeSentrySDK initializes the Sentry SDK with privacy-compliant options
func initializeSentrySDK(settings *conf.Settings) error {
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
		BeforeSend: createBeforeSendHook(settings),
	})

	if err != nil {
		return fmt.Errorf("sentry initialization failed: %w", err)
	}

	return nil
}

// createBeforeSendHook creates the BeforeSend hook for privacy filtering
func createBeforeSendHook(settings *conf.Settings) func(*sentry.Event, *sentry.EventHint) *sentry.Event {
	return func(event *sentry.Event, hint *sentry.EventHint) *sentry.Event {
		if serviceLogger != nil && settings.Sentry.Debug {
			return applyPrivacyFiltersWithLogging(event)
		}
		return applyPrivacyFilters(event)
	}
}

// applyPrivacyFilters applies privacy filters to a Sentry event
func applyPrivacyFilters(event *sentry.Event) *sentry.Event {
	// Clear user data and server name
	event.User = sentry.User{}
	event.ServerName = ""

	// Remove sensitive contexts
	if event.Contexts != nil {
		delete(event.Contexts, "device")
		delete(event.Contexts, "os")
		delete(event.Contexts, "runtime")
	}

	// Remove extra fields except allowed ones
	for k := range event.Extra {
		if k != "error_type" && k != "component" {
			delete(event.Extra, k)
		}
	}

	// Remove sensitive tags
	if event.Tags != nil {
		delete(event.Tags, "server_name")
		delete(event.Tags, "hostname")
	}

	return event
}

// applyPrivacyFiltersWithLogging applies privacy filters and logs what was removed
func applyPrivacyFiltersWithLogging(event *sentry.Event) *sentry.Event {
	var filtersApplied []string

	// Log before filtering
	logEventBeforeFiltering(event)

	// Track and apply user data removal
	if !event.User.IsEmpty() {
		filtersApplied = append(filtersApplied, "remove_user_data")
	}
	if event.ServerName != "" {
		filtersApplied = append(filtersApplied, "remove_server_name")
	}

	// Apply basic filters
	event.User = sentry.User{}
	event.ServerName = ""

	// Handle contexts with tracking
	if event.Contexts != nil {
		contextsRemoved := removePrivacyContexts(event.Contexts)
		filtersApplied = append(filtersApplied, contextsRemoved...)
	}

	// Handle extra fields with tracking
	if extraRemoved := removePrivacyExtraFields(event.Extra); extraRemoved > 0 {
		filtersApplied = append(filtersApplied, fmt.Sprintf("remove_%d_extra_fields", extraRemoved))
	}

	// Handle tags with tracking
	if event.Tags != nil {
		tagsRemoved := removePrivacyTags(event.Tags)
		filtersApplied = append(filtersApplied, tagsRemoved...)
	}

	// Log after filtering
	logEventAfterFiltering(event, filtersApplied)

	return event
}

// logEventBeforeFiltering logs event details before privacy filtering
func logEventBeforeFiltering(event *sentry.Event) {
	logTelemetryDebug(nil, "applying privacy filters to event",
		"event_id", event.EventID,
		"has_user_data", !event.User.IsEmpty(),
		"has_server_name", event.ServerName != "",
		"contexts_count", len(event.Contexts),
		"extra_count", len(event.Extra),
		"tags_count", len(event.Tags),
	)
}

// logEventAfterFiltering logs event details after privacy filtering
func logEventAfterFiltering(event *sentry.Event, filtersApplied []string) {
	logTelemetryDebug(nil, "privacy filters applied",
		"event_id", event.EventID,
		"filters_applied", filtersApplied,
		"remaining_contexts", len(event.Contexts),
		"remaining_extra", len(event.Extra),
		"remaining_tags", len(event.Tags),
	)
}

// removePrivacyContexts removes sensitive contexts and returns what was removed
func removePrivacyContexts(contexts map[string]sentry.Context) []string {
	var removed []string
	sensitiveContexts := []string{"device", "os", "runtime"}

	for _, key := range sensitiveContexts {
		if _, exists := contexts[key]; exists {
			removed = append(removed, fmt.Sprintf("remove_%s_context", key))
			delete(contexts, key)
		}
	}

	return removed
}

// removePrivacyExtraFields removes sensitive extra fields and returns count
func removePrivacyExtraFields(extra map[string]any) int {
	removed := 0
	allowedFields := map[string]bool{
		"error_type": true,
		"component":  true,
	}

	for k := range extra {
		if !allowedFields[k] {
			removed++
			delete(extra, k)
		}
	}

	return removed
}

// removePrivacyTags removes sensitive tags and returns what was removed
func removePrivacyTags(tags map[string]string) []string {
	var removed []string
	sensitiveTags := map[string]string{
		"server_name": "remove_server_name_tag",
		"hostname":    "remove_hostname_tag",
	}

	for tag, filterName := range sensitiveTags {
		if _, exists := tags[tag]; exists {
			removed = append(removed, filterName)
			delete(tags, tag)
		}
	}

	return removed
}

// configureSentryScope configures the global Sentry scope with system information
func configureSentryScope(settings *conf.Settings) {
	platformInfo := collectPlatformInfo()

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
}

// processDeferredMessages processes any messages that were captured before Sentry was ready
func processDeferredMessages() int {
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

	return len(messagesToProcess)
}

// logInitializationSuccess logs the successful initialization of Sentry
func logInitializationSuccess(settings *conf.Settings, deferredCount int) {
	platformInfo := collectPlatformInfo()

	logTelemetryInfo(nil, "Sentry telemetry initialized",
		"system_id", settings.SystemID,
		"version", settings.Version,
		"debug", settings.Sentry.Debug,
		"platform", platformInfo.OS,
		"arch", platformInfo.Architecture,
		"deferred_messages", deferredCount,
	)

	if deferredCount > 0 {
		log.Printf("Sentry telemetry initialized successfully, processed %d deferred messages (System ID: %s)",
			deferredCount, settings.SystemID)
	} else {
		log.Printf("Sentry telemetry initialized successfully (opt-in enabled, System ID: %s)", settings.SystemID)
	}
}

// generateErrorTitle creates a meaningful error title for Sentry based on error type and component
// This function parses common runtime errors and panic messages to create human-readable titles
// IMPORTANT: Pass the scrubbed error message to prevent PII leakage in Sentry titles
func generateErrorTitle(scrubbedErrorMsg, component string) string {
	errorType := parseErrorType(scrubbedErrorMsg)

	// Normalize component: trim whitespace and check for "unknown" case-insensitively
	comp := strings.TrimSpace(component)
	if comp != "" && !strings.EqualFold(comp, "unknown") {
		return fmt.Sprintf("%s: %s", titleCaseComponent(comp), errorType)
	}

	return errorType
}

// parseErrorType extracts a human-readable error type from the error message
// Uses case-insensitive matching for robustness and trims multi-line content
func parseErrorType(errMsg string) string {
	// Normalize for case-insensitive matching
	lower := strings.ToLower(errMsg)

	// Check for common runtime panic patterns
	switch {
	case strings.Contains(lower, "nil pointer dereference"):
		return "Nil Pointer Dereference"
	case strings.Contains(lower, "index out of range"):
		return "Index Out of Range"
	case strings.Contains(lower, "slice bounds out of range"):
		return "Slice Bounds Out of Range"
	case strings.Contains(lower, "integer divide by zero"):
		return "Integer Divide by Zero"
	case strings.Contains(lower, "invalid memory address"):
		return "Invalid Memory Access"
	case strings.Contains(lower, "send on closed channel"):
		return "Send on Closed Channel"
	case strings.Contains(lower, "close of closed channel"):
		return "Close of Closed Channel"
	case strings.Contains(lower, "concurrent map"):
		// Check for "read" first to handle "concurrent map read and map write"
		if strings.Contains(lower, "read") {
			return "Concurrent Map Access"
		}
		if strings.Contains(lower, "write") {
			return "Concurrent Map Write"
		}
		return "Concurrent Map Access"
	case strings.Contains(lower, "interface conversion"):
		if strings.Contains(lower, "is nil") {
			return "Interface Conversion: Nil Value"
		}
		return "Interface Conversion Failed"
	case strings.HasPrefix(lower, "panic:"):
		// Extract panic message after "panic:" and handle optional space
		const panicPrefix = "panic:"
		panicMsg := errMsg[len(panicPrefix):]
		// Trim leading whitespace (handles both "panic: " and "panic:")
		panicMsg = strings.TrimLeft(panicMsg, " \t")
		// Trim at first newline to exclude stack traces
		if idx := strings.IndexByte(panicMsg, '\n'); idx >= 0 {
			panicMsg = panicMsg[:idx]
		}
		// Truncate if still too long
		if len(panicMsg) > 50 {
			panicMsg = panicMsg[:50] + "..."
		}
		return fmt.Sprintf("Panic: %s", panicMsg)
	default:
		// For unknown errors, use a generic title
		// Trim at first newline
		if idx := strings.IndexByte(errMsg, '\n'); idx >= 0 {
			errMsg = errMsg[:idx]
		}
		// Truncate very long messages
		if len(errMsg) > 60 {
			return errMsg[:60] + "..."
		}
		return errMsg
	}
}

// titleCaseComponent converts component names to title case for better readability
// Examples: "httpcontroller" -> "HTTP Controller", "datastore" -> "Datastore"
// Uses prefix-only matching to avoid mid-word replacements (e.g., "capistrano" stays intact)
func titleCaseComponent(component string) string {
	// Normalize: trim, lowercase, split on separators
	component = strings.TrimSpace(component)
	component = strings.ToLower(component)
	component = strings.ReplaceAll(component, "_", " ")
	component = strings.ReplaceAll(component, "-", " ")

	words := strings.Fields(component)
	result := make([]string, 0, len(words))

	// Process each word, checking for known abbreviation prefixes
	for _, word := range words {
		if word == "" {
			continue
		}

		// Check if entire word is a known abbreviation
		switch word {
		case "http", "rtsp", "mqtt", "api", "db":
			result = append(result, strings.ToUpper(word))
			continue
		}

		// Check for abbreviation prefix and handle separately
		handled := false
		for _, prefix := range []string{"http", "rtsp", "mqtt", "api", "db"} {
			if !strings.HasPrefix(word, prefix) || len(word) <= len(prefix) {
				continue
			}
			// Split: prefix as uppercase + remainder as title case
			result = append(result, strings.ToUpper(prefix))
			remainder := word[len(prefix):]
			runes := []rune(remainder)
			runes[0] = unicode.ToUpper(runes[0])
			result = append(result, string(runes))
			handled = true
			break
		}

		if !handled {
			// Regular title case
			runes := []rune(word)
			runes[0] = unicode.ToUpper(runes[0])
			result = append(result, string(runes))
		}
	}

	return strings.Join(result, " ")
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

	// Create a scrubbed error for privacy - this prevents PII leakage
	scrubbedErrorMsg := privacy.ScrubMessage(err.Error())

	// Log the error being sent (privacy-safe)
	logTelemetryDebug(nil, "sending error event",
		"event_type", "error",
		"component", component,
		"error_type", fmt.Sprintf("%T", err),
		"scrubbed_message", scrubbedErrorMsg,
	)

	sentry.WithScope(func(scope *sentry.Scope) {
		// Generate meaningful error title from scrubbed message to prevent PII leakage
		errorTitle := generateErrorTitle(scrubbedErrorMsg, component)
		errorType := parseErrorType(scrubbedErrorMsg)

		scope.SetTag("component", component)
		scope.SetTag("error_title", errorTitle)
		// Add parsed error type to extras for easier filtering in Sentry
		scope.SetExtra("error_type", errorType)
		scope.SetContext("error", map[string]any{
			"type":             fmt.Sprintf("%T", err),
			"scrubbed_message": scrubbedErrorMsg,
		})

		// Create event with custom title to replace generic error type prefix
		event := sentry.NewEvent()
		event.Level = sentry.LevelError
		event.Message = scrubbedErrorMsg
		event.Exception = []sentry.Exception{{
			Type:  errorTitle, // Use human-readable title instead of Go type
			Value: scrubbedErrorMsg,
		}}

		// Set custom fingerprint for better grouping
		// Exclude empty/unknown components to avoid noisy fingerprints
		comp := strings.TrimSpace(component)
		if comp == "" || strings.EqualFold(comp, "unknown") {
			scope.SetFingerprint([]string{errorTitle})
		} else {
			scope.SetFingerprint([]string{errorTitle, comp})
		}

		sentry.CaptureEvent(event)
	})

	// Log successful submission
	logTelemetryDebug(nil, "error event sent successfully",
		"component", component,
	)
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
	logTelemetryDebug(nil, "sending message event",
		"event_type", "message",
		"sentry_level", string(level),
		"component", component,
		"scrubbed_message", scrubbedMessage,
	)

	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", component)
		scope.SetLevel(level)
		sentry.CaptureMessage(scrubbedMessage)
	})

	// Log successful submission
	logTelemetryDebug(nil, "message event sent successfully",
		"component", component,
		"sentry_level", string(level),
	)
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
	scrubbedMessage := privacy.ScrubMessage(message)
	logTelemetryDebug(nil, "deferring message for later processing",
		"event_type", "deferred_message",
		"sentry_level", string(level),
		"component", component,
		"scrubbed_message", scrubbedMessage,
		"deferred_count", len(deferredMessages),
	)
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

// InitMinimalSentryForSupport initializes a minimal Sentry client just for support uploads
// This allows support bundle uploads without enabling full telemetry
func InitMinimalSentryForSupport(systemID, version string) error {
	deferredMutex.Lock()
	defer deferredMutex.Unlock()

	// If already initialized (either minimal or full), return
	if sentryInitialized {
		return nil
	}

	// Use the same DSN as full initialization
	const sentryDSN = "https://b9269b6c0f8fae154df65be5a97e0435@o4509553065525248.ingest.de.sentry.io/4509553112186960"

	// Initialize with minimal configuration
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              sentryDSN,
		SampleRate:       0, // Don't capture any errors automatically
		TracesSampleRate: 0, // No performance monitoring
		Debug:            false,
		AttachStacktrace: false,
		Environment:      "production",
		ServerName:       "", // No server identification
		Release:          fmt.Sprintf("birdnet-go@%s", version),
		// Only allow support dump events
		BeforeSend: func(event *sentry.Event, hint *sentry.EventHint) *sentry.Event {
			// Only allow events tagged as support dumps
			if event.Tags == nil || event.Tags["type"] != "support_dump" {
				return nil // Drop all non-support events
			}
			// Apply privacy filters
			event.Message = privacy.ScrubMessage(event.Message)
			event.User = sentry.User{ID: systemID} // Only include system ID
			event.ServerName = ""
			event.Modules = nil
			event.Request = nil
			return event
		},
	})

	if err != nil {
		return fmt.Errorf("failed to initialize minimal Sentry: %w", err)
	}

	// Mark as initialized but with limited functionality
	sentryInitialized = true
	
	// Create an enabled attachment uploader
	attachmentUploader = NewAttachmentUploader(true)

	logTelemetryInfo(nil, "telemetry: minimal Sentry initialized for support uploads only",
		"system_id", systemID)

	return nil
}
