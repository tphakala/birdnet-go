package api

import (
	"runtime"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// newProfilingTestController builds a controller that publishes only to its own
// snapshot, so these tests never touch the global settings singleton or disk.
func newProfilingTestController(t *testing.T) *Controller {
	t.Helper()

	c := &Controller{
		Core:                &apicore.Core{Echo: echo.New()},
		controlChan:         make(chan string, 1),
		DisableSaveSettings: true,
	}
	c.Settings.Store(&conf.Settings{})
	return c
}

// TestEnsureProfilingTokenForSave_MintsOnRuntimeEnable is the regression test
// for the gap review found: the token used to be minted only on the config load
// path, so switching profiling on through the settings API left
// diagnostics.profiling.enabled true with an empty token, and the gate then
// refused every request until the process restarted.
//
// The mint now runs at each settings publish point, so a runtime enable
// produces a usable credential in the same save.
func TestEnsureProfilingTokenForSave_MintsOnRuntimeEnable(t *testing.T) {
	updated := &conf.Settings{}
	updated.Diagnostics.Profiling.Enabled = true
	require.Empty(t, updated.Diagnostics.Profiling.Token,
		"setup must start from the enabled-but-tokenless state")

	ensureProfilingTokenForSave(updated)

	assert.NotEmpty(t, updated.Diagnostics.Profiling.Token,
		"enabling profiling at runtime must mint a token, not defer it to a restart")
}

// TestEnsureProfilingTokenForSave_NoOpCases pins the cases where nothing should
// be minted, so the mint cannot start handing out credentials an instance does
// not need.
func TestEnsureProfilingTokenForSave_NoOpCases(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*conf.Settings)
	}{
		{
			name:   "profiling disabled",
			mutate: func(*conf.Settings) {},
		},
		{
			name: "auth provider configured, so the middleware is the gate",
			mutate: func(s *conf.Settings) {
				s.Diagnostics.Profiling.Enabled = true
				s.Security.BasicAuth.Enabled = true
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updated := &conf.Settings{}
			tt.mutate(updated)

			ensureProfilingTokenForSave(updated)

			assert.Empty(t, updated.Diagnostics.Profiling.Token,
				"no token should be minted for this configuration")
		})
	}
}

// TestPatchDiagnosticsSectionIsWritable pins that the diagnostics section is
// PATCHable. It was deliberately withheld from getSettingsSectionValue while the
// PATCH merge path ignored getBlockedFieldMap, because adding it would have made
// the generated profiling token client-settable. UpdateSectionSettings now
// enforces the map via restoreBlockedFields, so the section is writable again.
//
// This replaces TestDiagnosticsSectionIsNotPatchable, which pinned the 400.
//
// Deliberately changes only Enabled, not the sampling rates: the rates are
// process-global runtime state (see the note further down this file), and this
// test needs neither of them to prove the section is writable.
func TestPatchDiagnosticsSectionIsWritable(t *testing.T) {
	e := echo.New()
	controller := getTestController(t, e)
	require.False(t, controller.Settings.Load().Diagnostics.Profiling.Enabled,
		"setup must start from profiling disabled")

	patchSection(t, e, controller, "diagnostics", map[string]any{
		"profiling": map[string]any{"enabled": true},
	})

	assert.True(t, controller.Settings.Load().Diagnostics.Profiling.Enabled,
		"the enable flag must be writable, or profiling could not be turned on through PATCH at all")
}

// TestPatchDiagnosticsCannotSetProfilingToken is the reason the section case was
// withheld in the first place: with no auth provider configured the settings API
// is itself unauthenticated, so a client that could choose the token could then
// read pprof profiles with a value it already knew.
//
// The general enforcement is covered by TestPatchCannotChangeBlockedFields; this
// keeps the profiling-specific case next to the code it protects, since that is
// where the reasoning lives.
func TestPatchDiagnosticsCannotSetProfilingToken(t *testing.T) {
	const minted = "server-minted-profiling-token"

	e := echo.New()
	controller := getTestController(t, e)
	controller.Settings.Load().Diagnostics.Profiling.Token = minted

	rec := patchSection(t, e, controller, "diagnostics", map[string]any{
		"profiling": map[string]any{
			"enabled": true,
			"token":   "attacker-known-token",
		},
	})

	updated := controller.Settings.Load()
	assert.Equal(t, minted, updated.Diagnostics.Profiling.Token,
		"PATCH must not let a client pin the profiling token")
	assert.True(t, updated.Diagnostics.Profiling.Enabled,
		"rejecting the token must not also reject the enable flag beside it")
	assert.Contains(t, skippedFieldsOf(t, rec), "Diagnostics.Profiling.Token",
		"the rejected token must be reported, not silently dropped")
}

// TestEnsureProfilingTokenForSave_KeepsExistingToken guards token stability: a
// settings save must not rotate a token the operator has already copied into a
// profiling command.
func TestEnsureProfilingTokenForSave_KeepsExistingToken(t *testing.T) {
	const existing = "an-already-issued-profiling-token"

	updated := &conf.Settings{}
	updated.Diagnostics.Profiling.Enabled = true
	updated.Diagnostics.Profiling.Token = existing

	ensureProfilingTokenForSave(updated)

	assert.Equal(t, existing, updated.Diagnostics.Profiling.Token,
		"an existing token must survive unrelated settings saves")
}

// The tests below that APPLY a rate must not call t.Parallel(): the block and
// mutex profile rates are process-global runtime state, shared by every test in
// this binary rather than just this file. TestProfilingRatesChangedScope is
// parallel and may stay that way, since it only evaluates the change predicate
// and never reaches the runtime.

// resetProfileRates restores both runtime sampling rates to off after a test.
func resetProfileRates(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		runtime.SetBlockProfileRate(0)
		runtime.SetMutexProfileFraction(0)
	})
}

// TestProfilingRatesHotReload verifies the rates take effect through the real
// settings-change path, in both directions, with no restart.
//
// The mutex fraction carries the assertion because it is the only one of the
// two the runtime will read back (a negative argument to
// SetMutexProfileFraction reports the current value and leaves it alone). There
// is no equivalent for the block rate; internal/profiling covers that one by
// showing samples appear.
func TestProfilingRatesHotReload(t *testing.T) {
	resetProfileRates(t)

	c := newProfilingTestController(t)

	off := &conf.Settings{}
	on := &conf.Settings{}
	on.Diagnostics.Profiling.BlockRate = conf.RecommendedBlockProfileRate
	on.Diagnostics.Profiling.MutexFraction = conf.RecommendedMutexProfileFraction

	require.NoError(t, c.handleSettingsChanges(off, on))
	assert.Equal(t, conf.RecommendedMutexProfileFraction, runtime.SetMutexProfileFraction(-1),
		"turning the rates on must take effect without a restart")

	require.NoError(t, c.handleSettingsChanges(on, off))
	assert.Zero(t, runtime.SetMutexProfileFraction(-1),
		"turning the rates back off must take effect without a restart")
}

// TestProfilingRatesUnchangedByUnrelatedSave pins the change gate. Applying
// unconditionally would be harmless to the runtime but would log the applied
// rates on every settings save, so an operator saving a node name would see
// profiling chatter.
func TestProfilingRatesUnchangedByUnrelatedSave(t *testing.T) {
	resetProfileRates(t)

	c := newProfilingTestController(t)

	const sentinel = 777
	runtime.SetMutexProfileFraction(sentinel)

	before := &conf.Settings{}
	after := &conf.Settings{}
	after.Main.Name = "renamed node"

	require.NoError(t, c.handleSettingsChanges(before, after))
	assert.Equal(t, sentinel, runtime.SetMutexProfileFraction(-1),
		"a settings change that does not touch the rates must not reapply them")
}

// TestProfilingRatesChangedScope pins which fields the detector watches.
// enabled and token deliberately do not trigger it: the pprof routes are gated
// by middleware reading the live snapshot per request, so they need nothing
// applied, and treating them as a rate change would reapply sampling every time
// somebody toggled the endpoint.
func TestProfilingRatesChangedScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// base seeds BOTH snapshots, so a case can start from a non-zero rate.
		// Without it every case starts from zero, and a detector written as
		// "either new rate is non-zero" would pass the whole table while
		// reapplying sampling on every save that merely kept it on.
		base    func(*conf.Settings)
		mutate  func(*conf.Settings)
		changed bool
	}{
		{
			name:   "no change",
			mutate: func(*conf.Settings) {},
		},
		{
			name: "equal non-zero rates are not a change",
			base: func(s *conf.Settings) {
				s.Diagnostics.Profiling.BlockRate = conf.RecommendedBlockProfileRate
				s.Diagnostics.Profiling.MutexFraction = conf.RecommendedMutexProfileFraction
			},
			mutate: func(s *conf.Settings) {
				s.Diagnostics.Profiling.BlockRate = conf.RecommendedBlockProfileRate
				s.Diagnostics.Profiling.MutexFraction = conf.RecommendedMutexProfileFraction
			},
		},
		{
			name: "turning a rate off is a change",
			base: func(s *conf.Settings) {
				s.Diagnostics.Profiling.BlockRate = conf.RecommendedBlockProfileRate
			},
			mutate:  func(s *conf.Settings) { s.Diagnostics.Profiling.BlockRate = 0 },
			changed: true,
		},
		{
			name: "two negatives both meaning off are not a change",
			base: func(s *conf.Settings) {
				s.Diagnostics.Profiling.BlockRate = -1
			},
			mutate: func(s *conf.Settings) {
				s.Diagnostics.Profiling.BlockRate = -5
			},
		},
		{
			name:    "block rate",
			mutate:  func(s *conf.Settings) { s.Diagnostics.Profiling.BlockRate = conf.RecommendedBlockProfileRate },
			changed: true,
		},
		{
			name:    "mutex fraction",
			mutate:  func(s *conf.Settings) { s.Diagnostics.Profiling.MutexFraction = conf.RecommendedMutexProfileFraction },
			changed: true,
		},
		{
			name:   "endpoint enabled",
			mutate: func(s *conf.Settings) { s.Diagnostics.Profiling.Enabled = true },
		},
		{
			name:   "token minted",
			mutate: func(s *conf.Settings) { s.Diagnostics.Profiling.Token = "generated-secret" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			old := &conf.Settings{}
			updated := &conf.Settings{}
			if tt.base != nil {
				tt.base(old)
				tt.base(updated)
			}
			tt.mutate(updated)

			assert.Equal(t, tt.changed, profilingRatesChanged(old, updated))
		})
	}
}

// TestRestoreProfilingRatesUndoesAFailedSave is the closure test for the
// rollback gap.
//
// The failure it pins is not "the rates are briefly wrong". It is that they
// stay wrong forever: a save that applies a rate and then fails its disk write
// rolls the snapshot back to the old value, and the change gate then compares
// the rolled-back config against the next save's config, finds them equal, and
// never reapplies. The process keeps sampling on the audio path at a rate no
// config records and no later save can clear, which is precisely the cost this
// whole change exists to remove, made invisible.
//
// The other side effects handleSettingsChanges triggers do not have this shape:
// the reconfigure_* actions re-read the live snapshot when the control monitor
// processes them, so a rollback makes them converge on their own.
func TestRestoreProfilingRatesUndoesAFailedSave(t *testing.T) {
	resetProfileRates(t)

	c := newProfilingTestController(t)

	current := &conf.Settings{}
	updated := &conf.Settings{}
	updated.Diagnostics.Profiling.MutexFraction = conf.RecommendedMutexProfileFraction

	// The save-succeeded path: the requested rate is live.
	require.NoError(t, c.handleSettingsChanges(current, updated))
	require.Equal(t, conf.RecommendedMutexProfileFraction, runtime.SetMutexProfileFraction(-1),
		"setup: the rate must be applied before the rollback is exercised")

	// Now the disk write fails and the handler republishes the old snapshot.
	restoreProfilingRates(current)

	assert.Zero(t, runtime.SetMutexProfileFraction(-1),
		"a rolled-back save must leave the runtime matching the config that survived, not the one that failed to persist")

	// And the process is not wedged: because the runtime now agrees with the
	// persisted config, an ordinary later save can still turn sampling on.
	require.NoError(t, c.handleSettingsChanges(current, updated))
	assert.Equal(t, conf.RecommendedMutexProfileFraction, runtime.SetMutexProfileFraction(-1),
		"a later save must still be able to apply the rate")
}
