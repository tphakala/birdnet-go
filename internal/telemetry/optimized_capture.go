package telemetry

import (
	"sync/atomic"

	"github.com/getsentry/sentry-go"
)

// telemetryEnabled is an atomic flag for fast checking if telemetry is enabled
var telemetryEnabled atomic.Bool

// UpdateTelemetryEnabled updates the cached telemetry enabled state
func UpdateTelemetryEnabled(enabled bool) {
	// In test mode, telemetry is always enabled
	if atomic.LoadInt32(&testMode) == 1 {
		telemetryEnabled.Store(true)
		return
	}
	
	telemetryEnabled.Store(enabled)
}

// IsTelemetryEnabled returns true if telemetry is enabled
// This is a fast atomic check that avoids calling conf.GetSettings()
func IsTelemetryEnabled() bool {
	return telemetryEnabled.Load()
}

// init sets up the initial telemetry state
func init() {
	// Set initial state to disabled (will be enabled later via explicit call)
	telemetryEnabled.Store(false)
}

// FastCaptureError is an optimized version that checks telemetry state first
func FastCaptureError(err error, component string) {
	// Fast path: check atomic flag first
	if !IsTelemetryEnabled() {
		return
	}
	
	// Slow path: actually capture
	CaptureError(err, component)
}

// FastCaptureMessage is an optimized version that checks telemetry state first
func FastCaptureMessage(message string, level sentry.Level, component string) {
	// Fast path: check atomic flag first
	if !IsTelemetryEnabled() {
		return
	}
	
	// Slow path: actually capture
	CaptureMessage(message, level, component)
}