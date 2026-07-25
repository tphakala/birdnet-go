package api

import (
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
	c := newProfilingTestController(t)

	updated := &conf.Settings{}
	updated.Diagnostics.Profiling.Enabled = true
	require.Empty(t, updated.Diagnostics.Profiling.Token,
		"setup must start from the enabled-but-tokenless state")

	c.ensureProfilingTokenForSave(updated)

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
			c := newProfilingTestController(t)

			updated := &conf.Settings{}
			tt.mutate(updated)

			c.ensureProfilingTokenForSave(updated)

			assert.Empty(t, updated.Diagnostics.Profiling.Token,
				"no token should be minted for this configuration")
		})
	}
}

// TestDiagnosticsSectionIsNotPatchable pins that PATCH on the diagnostics
// section is refused, and records why, because the obvious "fix" reopens a hole.
//
// The section carries a generated credential that getBlockedFieldMap marks
// never-updatable-via-API. The PATCH merge path does NOT enforce that map:
// handleGenericSection merges the incoming JSON into the section and then only
// records that restrictions exist, so a client could set a token it chose. On a
// no-auth instance the settings API is itself unauthenticated, which is exactly
// the configuration where the token is the only thing gating pprof.
//
// PUT /api/v2/settings does enforce the blocked map, so enabling profiling at
// runtime still works. Re-add the PATCH case once the merge path enforces
// blocked fields.
func TestDiagnosticsSectionIsNotPatchable(t *testing.T) {
	t.Parallel()

	_, err := getSettingsSectionValue(&conf.Settings{}, "diagnostics")
	require.Error(t, err,
		"PATCH must refuse the diagnostics section while the merge path ignores blocked fields")
}

// TestProfilingTokenIsBlockedFromAPIWrites pins the blocked-field entry that
// keeps a client from choosing the token on the PUT path.
func TestProfilingTokenIsBlockedFromAPIWrites(t *testing.T) {
	t.Parallel()

	blocked := getBlockedFieldMap()

	diagnostics, ok := blocked["Diagnostics"].(map[string]any)
	require.True(t, ok, "Diagnostics must carry field-level restrictions: %#v", blocked)

	profiling, ok := diagnostics["Profiling"].(map[string]any)
	require.True(t, ok, "Profiling must carry field-level restrictions: %#v", diagnostics)

	assert.Equal(t, true, profiling["Token"],
		"the generated profiling token must never be settable through the API")
	assert.NotContains(t, profiling, "Enabled",
		"the enable flag must stay writable, or profiling could not be turned on at all")
}

// TestEnsureProfilingTokenForSave_KeepsExistingToken guards token stability: a
// settings save must not rotate a token the operator has already copied into a
// profiling command.
func TestEnsureProfilingTokenForSave_KeepsExistingToken(t *testing.T) {
	const existing = "an-already-issued-profiling-token"

	c := newProfilingTestController(t)

	updated := &conf.Settings{}
	updated.Diagnostics.Profiling.Enabled = true
	updated.Diagnostics.Profiling.Token = existing

	c.ensureProfilingTokenForSave(updated)

	assert.Equal(t, existing, updated.Diagnostics.Profiling.Token,
		"an existing token must survive unrelated settings saves")
}
