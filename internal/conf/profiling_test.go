package conf

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// settingsWithProfiling builds settings with the profiling section configured
// and, optionally, an authentication provider enabled.
func settingsWithProfiling(enabled bool, token string, withAuth bool) *Settings {
	settings := &Settings{}
	settings.Diagnostics.Profiling.Enabled = enabled
	settings.Diagnostics.Profiling.Token = token
	settings.Security.BasicAuth.Enabled = withAuth
	return settings
}

// TestEnsureProfilingToken covers when a token is minted and, more importantly,
// when it is not. Minting one on an instance that already has authentication
// would add a second, weaker way into the profiling endpoints for no benefit.
func TestEnsureProfilingToken(t *testing.T) {
	tests := []struct {
		name          string
		settings      *Settings
		wantGenerated bool
	}{
		{
			name:          "enabled without an auth provider mints a token",
			settings:      settingsWithProfiling(true, "", false),
			wantGenerated: true,
		},
		{
			name:     "disabled mints nothing",
			settings: settingsWithProfiling(false, "", false),
		},
		{
			name:     "an existing token is left alone",
			settings: settingsWithProfiling(true, "already-set", false),
		},
		{
			name:     "a configured auth provider means no token is needed",
			settings: settingsWithProfiling(true, "", true),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// viper.Set is global state, so these cannot run in parallel.
			viper.Reset()
			t.Cleanup(viper.Reset)

			before := tt.settings.Diagnostics.Profiling.Token

			generated, err := EnsureProfilingToken(tt.settings)
			require.NoError(t, err)
			assert.Equal(t, tt.wantGenerated, generated)

			got := tt.settings.Diagnostics.Profiling.Token
			if !tt.wantGenerated {
				assert.Equal(t, before, got, "the token must not change")
				return
			}

			assert.NotEmpty(t, got)
			// Length is derived, not hardcoded, so raising the entropy in
			// GenerateRandomSecret does not silently leave this assertion
			// pinning the old size.
			expected, err := GenerateRandomSecret()
			require.NoError(t, err)
			assert.Len(t, got, len(expected),
				"a token should carry the same entropy as any other generated secret")

			// The token is deliberately NOT mirrored into viper: nothing in
			// this repository persists through viper (SaveYAMLConfig marshals
			// the struct), and viper.Set is not goroutine-safe. Asserting the
			// absence keeps a well-meaning "mirror it like ensureSessionSecret
			// does" from creeping back in.
			assert.Empty(t, viper.GetString("diagnostics.profiling.token"),
				"the token must not be staged into viper")
		})
	}
}

// TestEnsureProfilingTokenIsUnique guards against a fixed or seeded value: two
// instances must never end up sharing a credential.
func TestEnsureProfilingTokenIsUnique(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	first := settingsWithProfiling(true, "", false)
	second := settingsWithProfiling(true, "", false)

	generated, err := EnsureProfilingToken(first)
	require.NoError(t, err)
	require.True(t, generated)

	generated, err = EnsureProfilingToken(second)
	require.NoError(t, err)
	require.True(t, generated)

	assert.NotEqual(t, first.Diagnostics.Profiling.Token, second.Diagnostics.Profiling.Token)
}

// TestEnsureProfilingTokenIsIdempotent verifies a second pass over already
// backfilled settings is a no-op, so restarts do not churn the config file.
func TestEnsureProfilingTokenIsIdempotent(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	settings := settingsWithProfiling(true, "", false)

	generated, err := EnsureProfilingToken(settings)
	require.NoError(t, err)
	require.True(t, generated)
	minted := settings.Diagnostics.Profiling.Token

	generated, err = EnsureProfilingToken(settings)
	require.NoError(t, err)
	assert.False(t, generated, "a second pass must report no change")
	assert.Equal(t, minted, settings.Diagnostics.Profiling.Token,
		"the token must be stable across restarts; a profiling session outlives one")
}

// TestIsAuthProviderConfigured pins the predicate that decides whether the
// profiling routes rely on the auth middleware or on their own token.
func TestIsAuthProviderConfigured(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mutate   func(*Settings)
		expected bool
	}{
		{
			name:   "nothing configured",
			mutate: func(*Settings) {},
		},
		{
			name:     "basic auth enabled",
			mutate:   func(s *Settings) { s.Security.BasicAuth.Enabled = true },
			expected: true,
		},
		{
			name: "fully configured OAuth provider",
			mutate: func(s *Settings) {
				s.Security.OAuthProviders = []OAuthProviderConfig{{
					Provider:     "google",
					Enabled:      true,
					ClientID:     "id",
					ClientSecret: "secret",
				}}
			},
			expected: true,
		},
		{
			name: "OAuth provider enabled but missing credentials does not count",
			mutate: func(s *Settings) {
				s.Security.OAuthProviders = []OAuthProviderConfig{{
					Provider: "google",
					Enabled:  true,
				}}
			},
		},
		{
			name: "fully credentialed but disabled OAuth provider does not count",
			mutate: func(s *Settings) {
				s.Security.OAuthProviders = []OAuthProviderConfig{{
					Provider:     "google",
					ClientID:     "id",
					ClientSecret: "secret",
				}}
			},
		},
		{
			name: "subnet bypass is a per-request concern and does not count",
			mutate: func(s *Settings) {
				s.Security.AllowSubnetBypass.Enabled = true
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &Settings{}
			tt.mutate(settings)
			assert.Equal(t, tt.expected, settings.IsAuthProviderConfigured())
		})
	}
}

// TestConfigTemplateShipsProfilingDisabled guards the embedded config template.
// A token committed to the repository would be identical on every install, which
// is worse than having none.
func TestConfigTemplateShipsProfilingDisabled(t *testing.T) {
	t.Parallel()

	template, err := configFiles.ReadFile("config.yaml")
	require.NoError(t, err)

	var settings Settings
	require.NoError(t, yaml.Unmarshal(template, &settings))

	assert.False(t, settings.Diagnostics.Profiling.Enabled,
		"profiling must ship disabled")
	assert.Empty(t, settings.Diagnostics.Profiling.Token,
		"the shipped template must not carry a profiling token")
	assert.Zero(t, settings.Diagnostics.Profiling.BlockRate,
		"block profiling must ship off; sampling costs CPU whether or not a profile is ever fetched")
	assert.Zero(t, settings.Diagnostics.Profiling.MutexFraction,
		"mutex profiling must ship off for the same reason")
}

// TestResolvedRates covers the clamping the two runtime setters need.
//
// The mutex case is the one that matters. runtime.SetMutexProfileFraction reads
// the current fraction and leaves it alone when handed a negative, so passing a
// negative config value through unclamped would quietly keep mutex profiling at
// whatever it already was rather than turning it off.
func TestResolvedRates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		blockRate         int
		mutexFraction     int
		wantBlockRate     int
		wantMutexFraction int
	}{
		{
			name: "unset resolves to off",
		},
		{
			name:              "explicit values pass through",
			blockRate:         10000,
			mutexFraction:     100,
			wantBlockRate:     10000,
			wantMutexFraction: 100,
		},
		{
			name:              "the recommended defaults pass through unchanged",
			blockRate:         DefaultBlockProfileRate,
			mutexFraction:     DefaultMutexProfileFraction,
			wantBlockRate:     DefaultBlockProfileRate,
			wantMutexFraction: DefaultMutexProfileFraction,
		},
		{
			name:              "rate 1 is honoured rather than overridden",
			blockRate:         1,
			mutexFraction:     1,
			wantBlockRate:     1,
			wantMutexFraction: 1,
		},
		{
			name:          "negatives clamp to off",
			blockRate:     -1,
			mutexFraction: -1,
		},
		{
			name:          "large negatives clamp to off",
			blockRate:     -999999,
			mutexFraction: -999999,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			profiling := &ProfilingConfig{
				BlockRate:     tt.blockRate,
				MutexFraction: tt.mutexFraction,
			}

			assert.Equal(t, tt.wantBlockRate, profiling.ResolvedBlockRate())
			assert.Equal(t, tt.wantMutexFraction, profiling.ResolvedMutexFraction())
		})
	}
}

// TestResolvedRatesNilReceiver pins the nil guard. Both resolvers are called
// from an apply path that walks in from a settings pointer, so a nil section
// must read as "off" rather than panicking on a diagnostics setting.
func TestResolvedRatesNilReceiver(t *testing.T) {
	t.Parallel()

	var profiling *ProfilingConfig

	assert.Zero(t, profiling.ResolvedBlockRate())
	assert.Zero(t, profiling.ResolvedMutexFraction())
}

// TestSamplingDefaultsAreNotRateOne is the regression guard for the change that
// introduced these constants. Both rates used to be set to 1 by debug: true,
// which records essentially every blocking and contention event. Anyone tempted
// to "simplify" these constants back toward the Go documentation's rate 1 has to
// delete this test to do it.
func TestSamplingDefaultsAreNotRateOne(t *testing.T) {
	t.Parallel()

	assert.Greater(t, DefaultBlockProfileRate, 1,
		"the recommended block rate must sample, not record every blocking event")
	assert.Greater(t, DefaultMutexProfileFraction, 1,
		"the recommended mutex fraction must sample, not record every contention event")
}

// TestSamplingDefaultsMatchDocumentedValues keeps the constants in step with the
// numbers quoted to users.
//
// The struct field comments become the generated config schema and the wiki's
// configuration reference, and config.yaml repeats them, so those places spell
// the numbers out rather than naming these constants. That is three copies that
// can drift apart silently; changing a constant without this test would leave
// the documentation quietly recommending the old value.
func TestSamplingDefaultsMatchDocumentedValues(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 10000, DefaultBlockProfileRate,
		"update the blockrate field comment, config.yaml and doc/PROFILING.md together with this constant")
	assert.Equal(t, 100, DefaultMutexProfileFraction,
		"update the mutexfraction field comment, config.yaml and doc/PROFILING.md together with this constant")
}
