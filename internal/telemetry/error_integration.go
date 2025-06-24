// Package telemetry - integration with the error handling system
package telemetry

import (
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// InitializeErrorIntegration sets up the error package to use telemetry when enabled
func InitializeErrorIntegration() {
	// Check if telemetry is enabled
	settings := conf.GetSettings()
	enabled := settings != nil && settings.Sentry.Enabled

	// Create and set the telemetry reporter
	reporter := errors.NewSentryReporter(enabled)
	errors.SetTelemetryReporter(reporter)

	// Set the privacy scrubbing function
	errors.SetPrivacyScrubber(ScrubMessage)
}

// UpdateErrorIntegration updates the error integration when telemetry settings change
func UpdateErrorIntegration(enabled bool) {
	reporter := errors.NewSentryReporter(enabled)
	errors.SetTelemetryReporter(reporter)
}

// GetPrivacyScrubFunction returns the privacy scrubbing function for use by the error package
func GetPrivacyScrubFunction() func(string) string {
	return ScrubMessage
}