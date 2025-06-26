// Package errors - telemetry integration (optional)
package errors

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/getsentry/sentry-go"
)

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

// ReportError reports an enhanced error to Sentry with privacy protection
func (sr *SentryReporter) ReportError(ee *EnhancedError) {
	if !sr.enabled || ee.IsReported() {
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
		scope.SetTag("component", ee.Component)
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
		scope.SetFingerprint([]string{errorTitle, ee.Component, string(ee.Category)})

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
	if ee.Component != "" {
		component := titleCase(ee.Component)
		titleParts = append(titleParts, component)
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
	default:
		return sentry.LevelError
	}
}

// Global telemetry reporter (can be nil if telemetry is disabled)
var globalTelemetryReporter TelemetryReporter

// SetTelemetryReporter sets the global telemetry reporter
func SetTelemetryReporter(reporter TelemetryReporter) {
	globalTelemetryReporter = reporter
}

// GetTelemetryReporter returns the current telemetry reporter
func GetTelemetryReporter() TelemetryReporter {
	return globalTelemetryReporter
}

// reportToTelemetry reports an error to the configured telemetry system
func reportToTelemetry(ee *EnhancedError) {
	if globalTelemetryReporter != nil && globalTelemetryReporter.IsEnabled() {
		globalTelemetryReporter.ReportError(ee)
	}
}

// PrivacyScrubber is a function type for privacy scrubbing
type PrivacyScrubber func(string) string

// Global privacy scrubber function (set by telemetry package)
var globalPrivacyScrubber PrivacyScrubber

// SetPrivacyScrubber sets the global privacy scrubbing function
func SetPrivacyScrubber(scrubber PrivacyScrubber) {
	globalPrivacyScrubber = scrubber
}

// scrubMessageForPrivacy applies privacy protection to error messages
func scrubMessageForPrivacy(message string) string {
	if globalPrivacyScrubber != nil {
		return globalPrivacyScrubber(message)
	}

	// Fallback to basic scrubbing if no privacy scrubber is set
	return basicURLScrub(message)
}

// basicURLScrub provides basic URL anonymization as fallback
func basicURLScrub(message string) string {
	// Regex to match URLs with query parameters
	urlRegex := regexp.MustCompile(`(https?://[^?\s]+)\?\S*`)

	// Replace query parameters with [REDACTED]
	scrubbed := urlRegex.ReplaceAllString(message, "$1?[REDACTED]")

	// Also scrub any standalone query parameters that might appear
	queryParamRegex := regexp.MustCompile(`[?&]([^=\s]+)=([^&\s]+)`)
	scrubbed = queryParamRegex.ReplaceAllString(scrubbed, "?[REDACTED]")

	// Specific patterns for sensitive data
	// API keys in various formats
	apiKeyPatterns := []string{
		`api[_-]?key[=:]\S+`,     // api_key=xxx, apikey:xxx
		`token[=:]\S+`,           // token=xxx
		`auth[=:]\S+`,            // auth=xxx
		`key[=:][0-9a-fA-F]{8,}`, // key=hexstring
		`[0-9a-fA-F]{32,}`,       // Long hex strings (likely API keys)
	}

	// Station IDs and other identifiers
	idPatterns := []string{
		`station[_-]?id[=:]\S+`, // station_id=xxx, stationid:xxx
		`user[_-]?id[=:]\S+`,    // user_id=xxx
		`device[_-]?id[=:]\S+`,  // device_id=xxx
		`client[_-]?id[=:]\S+`,  // client_id=xxx
	}

	// Apply API key patterns
	for _, pattern := range apiKeyPatterns {
		regex := regexp.MustCompile(pattern)
		scrubbed = regex.ReplaceAllString(scrubbed, "[API_KEY_REDACTED]")
	}

	// Apply ID patterns
	for _, pattern := range idPatterns {
		regex := regexp.MustCompile(pattern)
		scrubbed = regex.ReplaceAllString(scrubbed, "[ID_REDACTED]")
	}

	return scrubbed
}
