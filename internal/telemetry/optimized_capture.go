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
	if atomic.LoadInt32(&testMode) == 1 {
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

// EnableTestMode enables test mode and updates the telemetry enabled state
// This is useful for tests that need to enable telemetry without real settings
func EnableTestMode() {
	atomic.StoreInt32(&testMode, 1)
	UpdateTelemetryEnabled()
}

// DisableTestMode disables test mode and updates the telemetry enabled state
func DisableTestMode() {
	atomic.StoreInt32(&testMode, 0)
	UpdateTelemetryEnabled()
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