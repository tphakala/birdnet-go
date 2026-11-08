package analysis

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleReconfigureSpeciesGuide_MissingDepsIsSafe verifies the guard: with no
// API controller or metrics wired, the reconfigure handler logs and returns
// without panicking (rather than dereferencing a nil controller). The full swap
// orchestration is exercised by the QA hot-reload integration suite, since it
// requires a live *apiv2.Controller; its building blocks (initGuideCacheIfNeeded,
// guideProviderSetChanged, warmGuideCacheWithTopSpecies) are unit-tested here and
// in guide_cache_init_test.go.
func TestHandleReconfigureSpeciesGuide_MissingDepsIsSafe(t *testing.T) {
	t.Parallel()
	assert.NotPanics(t, func() {
		(&ControlMonitor{}).handleReconfigureSpeciesGuide() // apiController + metrics are nil
	})
}

// TestMonitor_LiteralControlMonitorDerivesItsContext pins that monitor() works on a
// ControlMonitor assembled as a struct literal, the way package-internal tests
// assemble it (TestHandleReconfigureSpeciesGuide_MissingDepsIsSafe above, and
// upstream's restart-signal test).
//
// NewControlMonitor derives monitorCtx/monitorCancel, so for a literal both are
// nil. The deferred cancel in monitor() then dereferenced a nil CancelFunc and the
// goroutine panicked as it exited, and any handler that passes monitorCtx on — the
// guide cache warm does — would have handed it a nil context.
//
// Both fields are read only after the monitor goroutine has exited, so the channel
// close orders the read after the monitor's write.
func TestMonitor_LiteralControlMonitorDerivesItsContext(t *testing.T) {
	t.Parallel()

	quit := make(chan struct{})
	cm := &ControlMonitor{
		controlChan: make(chan string, 1),
		quitChan:    quit,
	}
	require.Nil(t, cm.monitorCancel, "a literal ControlMonitor starts without a CancelFunc")

	done := make(chan struct{})
	go func() {
		defer close(done)
		cm.monitor()
	}()

	close(quit)
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("monitor did not return after quitChan was closed")
	}

	require.NotNil(t, cm.monitorCtx, "monitor must derive monitorCtx so a handler never receives a nil context")
	assert.Error(t, cm.monitorCtx.Err(), "leaving the loop must cancel monitorCtx, so detached handler work cannot outlive the monitor")
}
