package telemetry

import (
	"sync"
	"sync/atomic"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// telemetryEnabled is an atomic flag for fast checking if telemetry is enabled
var telemetryEnabled atomic.Bool

// telemetryFlagsMu serializes computing and storing the cached flags, so the
// value stored last is the one computed last. Without it a refresh that
// computed false before a client was bound could store after the refresh that
// computed true and leave the flag stuck false. It is a leaf lock: it is never
// held while taking clientLifecycleMu, and nothing under it takes that lock.
var telemetryFlagsMu sync.Mutex

// UpdateTelemetryEnabled refreshes the cached telemetry enabled state and the
// error package's cached active-reporting flag. The fast-path flag mirrors
// reportingActive: opt-in, build age, a bound client and test mode. Call it
// after a settings change and after a client is bound or unbound.
func UpdateTelemetryEnabled() {
	telemetryFlagsMu.Lock()
	defer telemetryFlagsMu.Unlock()

	telemetryEnabled.Store(reportingActive())
	errors.RefreshActiveReporting()
}

// IsTelemetryEnabled returns true if telemetry is enabled
// This is a fast atomic check that avoids calling conf.GetSettings()
func IsTelemetryEnabled() bool {
	return telemetryEnabled.Load()
}

// EnableTestMode enables test mode and updates the telemetry enabled state
// This is useful for tests that need to enable telemetry without real settings
func EnableTestMode() {
	testMode.Store(testModeEnabled)
	UpdateTelemetryEnabled()
}

// DisableTestMode disables test mode and updates the telemetry enabled state
func DisableTestMode() {
	testMode.Store(testModeDisabled)
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

	// Filter out info/debug-level messages: these are operational signals,
	// not errors. They create noise issues in Sentry's issue list.
	if level == sentry.LevelInfo || level == sentry.LevelDebug {
		return
	}

	// Slow path: actually capture
	CaptureMessage(message, level, component)
}

// FastCaptureMessageWithExtras is an optimized version of CaptureMessageWithExtras
// that checks telemetry state first via atomic flag.
func FastCaptureMessageWithExtras(message string, level sentry.Level, component string, extras map[string]any) {
	if !IsTelemetryEnabled() {
		return
	}

	if level == sentry.LevelInfo || level == sentry.LevelDebug {
		return
	}

	CaptureMessageWithExtras(message, level, component, extras)
}
