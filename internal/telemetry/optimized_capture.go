package telemetry

import (
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// telemetryEnabled is an atomic flag for fast checking if telemetry is enabled
var telemetryEnabled atomic.Bool

// UpdateTelemetryEnabled updates the cached telemetry enabled state
func UpdateTelemetryEnabled() {
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