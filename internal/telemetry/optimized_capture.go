package telemetry

import (
	"sync/atomic"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// telemetryEnabled is an atomic flag for fast checking if telemetry is enabled
var telemetryEnabled atomic.Bool

// UpdateTelemetryEnabled updates the cached telemetry enabled state
func UpdateTelemetryEnabled() {
	// In test mode, telemetry is always enabled
	if testMode {
		telemetryEnabled.Store(true)
		return
	}
	
	settings := conf.GetSettings()
	if settings != nil && settings.Sentry.Enabled {
		telemetryEnabled.Store(true)
	} else {
		telemetryEnabled.Store(false)
	}
}

// IsTelemetryEnabled returns true if telemetry is enabled
// This is a fast atomic check that avoids calling conf.GetSettings()
func IsTelemetryEnabled() bool {
	return telemetryEnabled.Load()
}

// init sets up the initial telemetry state
func init() {
	// Set initial state
	UpdateTelemetryEnabled()
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