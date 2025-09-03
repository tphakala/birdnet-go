// Package telemetry - integration with the error handling system
package telemetry

import (
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// InitializeErrorIntegration sets up the error package to use telemetry when enabled
func InitializeErrorIntegration(settings *conf.Settings) {
	// Defensive check: ensure settings are available
	if settings == nil {
		// If settings are not available, disable telemetry
		reporter := errors.NewSentryReporter(false)
		errors.SetTelemetryReporter(reporter)
		errors.SetPrivacyScrubber(privacy.ScrubMessage)
		return
	}

	// Check if telemetry is enabled in settings
	enabled := settings.Sentry.Enabled

	// Create and set the telemetry reporter
	reporter := errors.NewSentryReporter(enabled)
	errors.SetTelemetryReporter(reporter)

	// Set the privacy scrubbing function
	errors.SetPrivacyScrubber(privacy.ScrubMessage)
}

// UpdateErrorIntegration updates the error integration when telemetry settings change
func UpdateErrorIntegration(enabled bool) {
	reporter := errors.NewSentryReporter(enabled)
	errors.SetTelemetryReporter(reporter)
	// Note: We don't update the privacy scrubber here as it remains the same
	// regardless of whether telemetry is enabled or disabled. The scrubbing
	// function itself checks if telemetry is enabled before processing.
}

// GetPrivacyScrubFunction returns a function used to scrub sensitive information
// from error messages to protect privacy in error handling and logging.
// The returned function removes or masks personal data, file paths, and other
// potentially sensitive information before errors are reported or logged.
func GetPrivacyScrubFunction() func(string) string {
	return privacy.ScrubMessage
}
