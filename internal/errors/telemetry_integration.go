// Package errors - telemetry integration (optional)
package errors

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"

	"github.com/getsentry/sentry-go"
)

// Pre-compiled regex patterns for privacy scrubbing
var (
	// URL patterns
	urlRegex        = regexp.MustCompile(`(https?://[^?\s]+)\?\S*`)
	queryParamRegex = regexp.MustCompile(`[?&]([^=\s]+)=([^&\s]+)`)

	// API key patterns
	apiKeyRegexes = []*regexp.Regexp{
		regexp.MustCompile(`api[_-]?key[=:]\S+`),     // api_key=xxx, apikey:xxx
		regexp.MustCompile(`token[=:]\S+`),           // token=xxx
		regexp.MustCompile(`auth[=:]\S+`),            // auth=xxx
		regexp.MustCompile(`key[=:][0-9a-fA-F]{8,}`), // key=hexstring
		regexp.MustCompile(`\b[0-9a-fA-F]{32}\b`),    // 32-char hex strings with word boundaries (MD5-like API keys)
	}

	// ID patterns
	idPatternRegexes = []*regexp.Regexp{
		regexp.MustCompile(`station[_-]?id[=:]\S+`), // station_id=xxx, stationid:xxx
		regexp.MustCompile(`user[_-]?id[=:]\S+`),    // user_id=xxx
		regexp.MustCompile(`device[_-]?id[=:]\S+`),  // device_id=xxx
		regexp.MustCompile(`client[_-]?id[=:]\S+`),  // client_id=xxx
	}
)

// Initialize package state
func init() {
	// Initialize hasActiveReporting to false (no telemetry or hooks by default)
	hasActiveReporting.Store(false)
}

// TelemetryReporter is an interface for reporting errors to telemetry systems
type TelemetryReporter interface {
	ReportError(err *EnhancedError)
	IsEnabled() bool
}

// SentryReporter implements TelemetryReporter for Sentry
type SentryReporter struct {
	enabled bool
}

// NewSentryReporter creates a new Sentry telemetry reporter
func NewSentryReporter(enabled bool) *SentryReporter {
	return &SentryReporter{
		enabled: enabled,
	}
}

// IsEnabled returns whether Sentry telemetry is enabled
func (sr *SentryReporter) IsEnabled() bool {
	return sr.enabled
}

// shouldReportToSentry determines if an error should be sent to Sentry
// It filters out operational/configuration errors that aren't code bugs
func shouldReportToSentry(ee *EnhancedError) bool {
	errorMsg := strings.ToLower(ee.Err.Error())

	// Check for MQTT authentication/authorization errors (user config issues)
	if ee.Category == CategoryMQTTConnection || ee.Category == CategoryMQTTAuth {
		// Common MQTT authentication/authorization error patterns
		authPatterns := []string{
			"not authorized",
			"authentication failed",
			"bad username or password",
			"connection refused, not authorized",
			"connection refused, bad user name or password",
			"access denied",
			"unauthorized",
		}

		for _, pattern := range authPatterns {
			if strings.Contains(errorMsg, pattern) {
				return false // Don't report auth errors - these are config issues
			}
		}
	}

	// Add more filters here for other operational errors as needed
	// Examples that could be added in future:
	// - DNS resolution failures (user's network/config issue)
	// - "connection refused" (service not running)
	// - "no route to host" (network issue)

	return true // Report everything else
}

// ReportError reports an enhanced error to Sentry with privacy protection
func (sr *SentryReporter) ReportError(ee *EnhancedError) {
	if !sr.enabled || ee.IsReported() {
		return
	}

	// Skip operational/configuration errors that aren't code bugs
	if !shouldReportToSentry(ee) {
		ee.MarkReported() // Mark as reported to prevent retry
		return
	}

	// Create enhanced error message with category
	enhancedMessage := fmt.Sprintf("[%s] %s", ee.Category, ee.Err.Error())

	// Scrub the message for privacy (using local function)
	scrubbedMessage := scrubMessageForPrivacy(enhancedMessage)

	sentry.WithScope(func(scope *sentry.Scope) {
		// Create a meaningful error title for Sentry
		errorTitle := generateErrorTitle(ee)

		// Set the error title as a tag that Sentry can use for grouping
		scope.SetTag("error_title", errorTitle)
		scope.SetTag("component", ee.GetComponent())
		scope.SetTag("category", string(ee.Category))
		scope.SetTag("error_type", fmt.Sprintf("%T", ee.Err))

		// Add context data with privacy scrubbing
		for key, value := range ee.Context {
			// Scrub string values for privacy
			scrubbedValue := value
			if strValue, ok := value.(string); ok {
				scrubbedValue = scrubMessageForPrivacy(strValue)
			}
			scope.SetContext(key, map[string]any{"value": scrubbedValue})
		}

		// Set error level based on category
		level := getErrorLevel(ee.Category)
		scope.SetLevel(level)

		// Set custom fingerprint for better grouping using the error title
		scope.SetFingerprint([]string{errorTitle, ee.GetComponent(), string(ee.Category)})

		// Use the error title as the exception type by creating a custom exception
		event := sentry.NewEvent()
		event.Message = scrubbedMessage
		event.Level = level

		// Create exception with custom type (this is what Sentry displays as the title)
		exception := sentry.Exception{
			Type:  errorTitle,
			Value: scrubbedMessage,
		}
		event.Exception = []sentry.Exception{exception}

		// Capture the event instead of the error
		sentry.CaptureEvent(event)
	})

	// Mark as reported
	ee.MarkReported()
}

// generateErrorTitle creates a meaningful error title for Sentry based on enhanced error context
func generateErrorTitle(ee *EnhancedError) string {
	// Extract operation from context if available
	operation, hasOperation := ee.Context["operation"].(string)

	// Create title based on component, category, and operation
	var titleParts []string

	// Add component (capitalize first letter)
	component := ee.GetComponent()
	if component != "" && component != ComponentUnknown {
		titleParts = append(titleParts, titleCase(component))
	}

	// Add category (human-readable format)
	categoryTitle := formatCategoryForTitle(ee.Category)
	if categoryTitle != "" {
		titleParts = append(titleParts, categoryTitle)
	}

	// Add operation context if available
	if hasOperation && operation != "" {
		operationTitle := formatOperationForTitle(operation)
		if operationTitle != "" {
			titleParts = append(titleParts, operationTitle)
		}
	}

	// Fallback to error type if no meaningful title can be constructed
	if len(titleParts) == 0 {
		return fmt.Sprintf("%T", ee.Err)
	}

	return strings.Join(titleParts, " ")
}

// formatCategoryForTitle converts error categories to human-readable titles
func formatCategoryForTitle(category ErrorCategory) string {
	switch category {
	case CategoryValidation:
		return "Validation Error"
	case CategoryImageFetch:
		return "Image Fetch Error"
	case CategoryImageCache:
		return "Image Cache Error"
	case CategoryImageProvider:
		return "Image Provider Error"
	case CategoryNetwork:
		return "Network Error"
	case CategoryDatabase:
		return "Database Error"
	case CategoryFileIO:
		return "File I/O Error"
	case CategoryModelInit:
		return "Model Initialization Error"
	case CategoryModelLoad:
		return "Model Loading Error"
	case CategoryConfiguration:
		return "Configuration Error"
	case CategorySystem:
		return "System Error"
	default:
		return string(category)
	}
}

// formatOperationForTitle converts operation context to human-readable format
func formatOperationForTitle(operation string) string {
	// Replace underscores with spaces and title case
	formatted := strings.ReplaceAll(operation, "_", " ")
	words := strings.Fields(formatted)
	for i, word := range words {
		words[i] = titleCase(word)
	}
	return strings.Join(words, " ")
}

// titleCase capitalizes the first letter of a string (replacement for deprecated strings.Title)
func titleCase(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// getErrorLevel returns appropriate Sentry level based on category
func getErrorLevel(category ErrorCategory) sentry.Level {
	switch category {
	case CategoryModelInit, CategoryModelLoad:
		return sentry.LevelError // Critical for app functionality
	case CategoryValidation:
		return sentry.LevelError // Usually indicates serious issues
	case CategoryDatabase:
		return sentry.LevelError // Data integrity issues
	case CategoryNetwork, CategoryRTSP:
		return sentry.LevelWarning // Often transient
	case CategoryFileIO:
		return sentry.LevelWarning // Could be config issues
	case CategoryAudio, CategoryHTTP:
		return sentry.LevelWarning // Usually recoverable
	case CategoryConfiguration, CategorySystem:
		return sentry.LevelError // Environment issues
	case CategoryNotFound:
		return sentry.LevelInfo // Expected condition for unknown species/taxonomy lookups
	default:
		return sentry.LevelError
	}
}

// ErrorHook is a function that gets called when an error is reported
type ErrorHook func(ee *EnhancedError)

// Global telemetry reporter (can be nil if telemetry is disabled)
var globalTelemetryReporter TelemetryReporter

// Global error hooks and mutex for thread safety
var (
	errorHooks      []ErrorHook
	errorHooksMutex sync.RWMutex
	hasActiveReporting atomic.Bool // true if telemetry is enabled OR hooks exist
)

// SetTelemetryReporter sets the global telemetry reporter
func SetTelemetryReporter(reporter TelemetryReporter) {
	globalTelemetryReporter = reporter
	updateActiveReportingStatus()
}

// GetTelemetryReporter returns the current telemetry reporter
func GetTelemetryReporter() TelemetryReporter {
	return globalTelemetryReporter
}

// AddErrorHook adds a hook function that will be called when errors are reported
func AddErrorHook(hook ErrorHook) {
	errorHooksMutex.Lock()
	errorHooks = append(errorHooks, hook)
	hooksExist := len(errorHooks) > 0
	errorHooksMutex.Unlock()
	
	// Update status after releasing the lock
	telemetryActive := globalTelemetryReporter != nil && globalTelemetryReporter.IsEnabled()
	hasActiveReporting.Store(hooksExist || telemetryActive)
}

// ClearErrorHooks removes all error hooks
func ClearErrorHooks() {
	errorHooksMutex.Lock()
	errorHooks = nil
	errorHooksMutex.Unlock()
	
	// Update status after releasing the lock
	telemetryActive := globalTelemetryReporter != nil && globalTelemetryReporter.IsEnabled()
	hasActiveReporting.Store(false || telemetryActive)
}

// updateActiveReportingStatus updates the flag indicating if any reporting is active
// This must be called without holding errorHooksMutex to avoid deadlock
func updateActiveReportingStatus() {
	errorHooksMutex.RLock()
	hooksExist := len(errorHooks) > 0
	errorHooksMutex.RUnlock()
	
	telemetryActive := globalTelemetryReporter != nil && globalTelemetryReporter.IsEnabled()
	hasActiveReporting.Store(hooksExist || telemetryActive)
}

// reportToTelemetry reports an error to the configured telemetry system
// NOTE: This function is replaced by event bus integration when async processing is enabled
// It remains here for backward compatibility when event bus is not initialized
func reportToTelemetryLegacy(ee *EnhancedError) {
	// Skip entirely if nothing to do
	if !hasActiveReporting.Load() {
		return
	}

	// Report to telemetry reporter
	if globalTelemetryReporter != nil && globalTelemetryReporter.IsEnabled() {
		globalTelemetryReporter.ReportError(ee)
	}

	// Skip hook processing if no hooks exist
	errorHooksMutex.RLock()
	hooksExist := len(errorHooks) > 0
	if !hooksExist {
		errorHooksMutex.RUnlock()
		return
	}
	
	// Copy hooks while holding lock
	hooks := make([]ErrorHook, len(errorHooks))
	copy(hooks, errorHooks)
	errorHooksMutex.RUnlock()

	// Call hooks outside of lock to avoid deadlock
	for _, hook := range hooks {
		if hook != nil {
			// Wrap hook call in panic recovery
			func() {
				defer func() {
					if r := recover(); r != nil {
						// Log the panic but don't let it crash the program
						// We can't use our own error system here to avoid recursion
						fmt.Printf("Error hook panicked: %v\n", r)
					}
				}()
				hook(ee)
			}()
		}
	}
}

// PrivacyScrubber is a function type for privacy scrubbing
type PrivacyScrubber func(string) string

// Global privacy scrubber function (set by telemetry package)
// Using atomic.Value for thread-safe access
var globalPrivacyScrubber atomic.Value

// SetPrivacyScrubber sets the global privacy scrubbing function
func SetPrivacyScrubber(scrubber PrivacyScrubber) {
	if scrubber != nil {
		globalPrivacyScrubber.Store(scrubber)
	}
}

// scrubMessageForPrivacy applies privacy protection to error messages
func scrubMessageForPrivacy(message string) string {
	// Load the scrubber atomically
	if scrubber := globalPrivacyScrubber.Load(); scrubber != nil {
		if fn, ok := scrubber.(PrivacyScrubber); ok {
			return fn(message)
		}
	}

	// Fallback to basic scrubbing if no privacy scrubber is set
	return basicURLScrub(message)
}

// basicURLScrub provides basic URL anonymization as fallback
func basicURLScrub(message string) string {
	// Replace query parameters with [REDACTED]
	scrubbed := urlRegex.ReplaceAllString(message, "$1?[REDACTED]")

	// Also scrub any standalone query parameters that might appear
	scrubbed = queryParamRegex.ReplaceAllString(scrubbed, "?[REDACTED]")

	// Apply pre-compiled API key patterns
	for _, regex := range apiKeyRegexes {
		scrubbed = regex.ReplaceAllString(scrubbed, "[API_KEY_REDACTED]")
	}

	// Apply pre-compiled ID patterns
	for _, regex := range idPatternRegexes {
		scrubbed = regex.ReplaceAllString(scrubbed, "[ID_REDACTED]")
	}

	return scrubbed
}
