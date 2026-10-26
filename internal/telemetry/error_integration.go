// Package telemetry - integration with the error handling system
package telemetry

import (
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// InitializeErrorIntegration sets up the error package to use telemetry. The
// reporter consults the live consent gate on every report, so toggling Error
// Tracking at runtime needs no reporter swap.
func InitializeErrorIntegration() {
	errors.SetTelemetryReporter(errors.NewSentryReporter(reportingActive))

	// Set the privacy scrubbing function
	errors.SetPrivacyScrubber(privacy.ScrubMessage)

	// Register resource snapshot callback for error reporting
	errors.ResourceSnapshotFunc = func() map[string]any {
		snap := CollectResourceSnapshot()
		return map[string]any{
			"goroutine_count": snap.GoroutineCount,
			"heap_alloc_mb":   snap.HeapAllocMB,
			"heap_sys_mb":     snap.HeapSysMB,
			"num_gc":          snap.NumGC,
		}
	}
}

// GetPrivacyScrubFunction returns a function used to scrub sensitive information
// from error messages to protect privacy in error handling and logging.
// The returned function removes or masks personal data, file paths, and other
// potentially sensitive information before errors are reported or logged.
func GetPrivacyScrubFunction() func(string) string {
	return privacy.ScrubMessage
}
