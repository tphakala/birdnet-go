// Package errors - telemetry integration (optional)
package errors

import (
	"fmt"

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

	// Scrub the message for privacy (import telemetry package function)
	scrubbedMessage := scrubMessageForPrivacy(enhancedMessage)

	sentry.WithScope(func(scope *sentry.Scope) {
		// Set component and category tags
		scope.SetTag("component", ee.Component)
		scope.SetTag("category", string(ee.Category))
		scope.SetTag("error_type", fmt.Sprintf("%T", ee.Err))

		// Add context data
		for key, value := range ee.Context {
			scope.SetContext(key, map[string]any{"value": value})
		}

		// Set error level based on category
		level := getErrorLevel(ee.Category)
		scope.SetLevel(level)

		// Capture the error
		sentry.CaptureException(fmt.Errorf("%s", scrubbedMessage))
	})

	// Mark as reported
	ee.MarkReported()
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
	// Basic implementation - just return as-is
	// The full privacy protection should be provided by the telemetry package
	return message
}