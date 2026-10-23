package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestHandleControlSignal_UpdateSourceGains pins that the update_source_gains
// signal runs the gain callback exactly once and neither reconfigures the
// sources (which probes every stream) nor re-plans the cadence.
func TestHandleControlSignal_UpdateSourceGains(t *testing.T) {
	t.Parallel()

	var gains, reconfigures, replans int
	cm := &ControlMonitor{
		updateSourceGainsFn:  func() { gains++ },
		reconfigureSourcesFn: func() { reconfigures++ },
		replanCadenceFn:      func() { replans++ },
	}
	cm.handleControlSignal("update_source_gains")

	assert.Equal(t, 1, gains, "the signal must run the gain callback once")
	assert.Zero(t, reconfigures, "the signal must not reconfigure the sources")
	assert.Zero(t, replans, "the signal must not re-plan the cadence")
}

// TestHandleControlSignal_UpdateSourceGainsWithoutCallbackIsSafe pins that a
// monitor built without the callback ignores the signal instead of panicking.
func TestHandleControlSignal_UpdateSourceGainsWithoutCallbackIsSafe(t *testing.T) {
	t.Parallel()

	cm := &ControlMonitor{}
	assert.NotPanics(t, func() { cm.handleControlSignal("update_source_gains") })
}
