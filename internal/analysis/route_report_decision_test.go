package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRouteReportDecision covers the "survives a reconfigure" suppression for
// #4208. The alarm is suppressed only on the FIRST failure of a RECONFIGURE pass
// (suppressTransient), because a transient AddRoute race is repaired by the next
// reconfigure. A start/restart failure (suppressTransient=false) and a failure that
// persists across reconfigures (failedLastPass) are always reported, so a permanent
// route outage is never silently swallowed.
func TestRouteReportDecision(t *testing.T) {
	t.Parallel()

	allocated := map[string]bool{"BirdNET_GLOBAL_6K_V2.4": true}

	tests := []struct {
		name              string
		bufferRouteOK     bool
		failedLastPass    bool
		suppressTransient bool
		// wantSuppressed true => resolved models treated as registered (no alarm);
		// false => surfaced as not-analyzing (registered == nil).
		wantSuppressed bool
	}{
		{"route up reports normally", true, false, true, true},
		{"route up after prior failure reports normally", true, true, false, true},
		{"start/restart first failure reported immediately", false, false, false, false},
		{"start/restart persistent failure reported", false, true, false, false},
		{"reconfigure first transient failure suppressed", false, false, true, true},
		{"reconfigure failure surviving a pass reported", false, true, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := routeReportDecision(tt.bufferRouteOK, tt.failedLastPass, tt.suppressTransient, allocated)
			if tt.wantSuppressed {
				assert.NotNil(t, got, "resolved models should be treated as registered (not reported)")
				assert.Equal(t, allocated, got)
			} else {
				assert.Nil(t, got, "resolved models should be surfaced as not-analyzing")
			}
		})
	}
}

// TestIsReconfigureOperation pins which passes get the first-failure grace. Only
// reconfigure-family operations do; start and explicit restarts report immediately
// so a startup route outage is not silenced (#4208 regression guard).
func TestIsReconfigureOperation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		op   string
		want bool
	}{
		{"reconfigure_diff suppresses first failure", operationReconfigureDiff, true},
		{"reconfigure_params suppresses first failure", operationReconfigureParams, true},
		{"gain_change suppresses first failure", operationGainChange, true},
		{"model_change suppresses first failure", operationModelChange, true},
		{"start reports immediately", operationStart, false},
		{"restart reports immediately", "restart", false},
		{"restart_source reports immediately", operationRestartSource, false},
		{"empty reports immediately", "", false},
		{"unknown reports immediately", "unknown", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isReconfigureOperation(tt.op))
		})
	}
}

// TestReportSourceRegistration_RouteFailureMemory exercises the stateful wrapper
// (not just the pure decision): routeFailedLastPass is lazily created, set when a
// route fails, and cleared when it recovers. mm is nil and the notification service
// is unset in tests, so reportUnregisteredModels/notify are safe no-ops, leaving the
// map transitions as the observable behavior.
func TestReportSourceRegistration_RouteFailureMemory(t *testing.T) {
	t.Parallel()
	p := &AudioPipelineService{} // routeFailedLastPass is nil; must be lazily created
	const sid = "mic-1"
	alloc := map[string]bool{"BirdNET_GLOBAL_6K_V2.4": true}

	// First reconfigure-pass route failure marks the source (and lazily inits the map).
	p.reportSourceRegistration(nil, sid, sid, operationReconfigureDiff, false, nil, nil, alloc)
	assert.NotNil(t, p.routeFailedLastPass, "map must be lazily initialized")
	assert.True(t, p.routeFailedLastPass[sid], "a route failure records the source")

	// Route recovers: the entry is cleared.
	p.reportSourceRegistration(nil, sid, sid, operationReconfigureDiff, true, nil, nil, alloc)
	_, present := p.routeFailedLastPass[sid]
	assert.False(t, present, "a recovered route clears the source's failure memory")

	// A start/restart route failure must NOT populate the reconfigure-failure memory:
	// it is reported immediately, so carrying it forward would make the next reconfigure's
	// first (transient) failure look like a repeat and skip the #4208 grace.
	p.reportSourceRegistration(nil, sid, sid, operationStart, false, nil, nil, alloc)
	_, present = p.routeFailedLastPass[sid]
	assert.False(t, present, "a start failure must not populate reconfigure failure memory")

	p.reportSourceRegistration(nil, sid, sid, operationRestartSource, false, nil, nil, alloc)
	_, present = p.routeFailedLastPass[sid]
	assert.False(t, present, "a restart_source failure must not populate reconfigure failure memory")

	// A start/restart failure also CLEARS a stale reconfigure-failure entry, so a
	// prior reconfigure failure followed by a restart cannot poison the next pass.
	p.routeFailedLastPass[sid] = true
	p.reportSourceRegistration(nil, sid, sid, operationStart, false, nil, nil, alloc)
	_, present = p.routeFailedLastPass[sid]
	assert.False(t, present, "a start pass clears any stale reconfigure-failure entry")
}
