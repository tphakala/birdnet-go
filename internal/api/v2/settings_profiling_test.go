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

// TestDiagnosticsSectionIsWritable closes the read/write asymmetry review
// found: GET /api/v2/settings/diagnostics resolved by reflection over the
// Settings fields and worked, while PATCH went through a hardcoded switch that
// had no diagnostics case and answered 400. The section was readable but not
// writable, so a client could see the feature's config and never change it.
func TestDiagnosticsSectionIsWritable(t *testing.T) {
	t.Parallel()

	settings := &conf.Settings{}
	settings.Diagnostics.Profiling.Enabled = true

	value, err := getSettingsSectionValue(settings, "diagnostics")
	require.NoError(t, err, "the diagnostics section must be writable via PATCH")

	section, ok := value.(*conf.DiagnosticsConfig)
	require.True(t, ok, "expected the diagnostics section, got %T", value)
	assert.True(t, section.Profiling.Enabled,
		"the returned section must alias the live settings, not a copy")
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
