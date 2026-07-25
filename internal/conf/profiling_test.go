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
				assert.Nil(t, viper.Get("diagnostics.profiling.token"),
					"nothing should be staged for persistence")
				return
			}

			assert.NotEmpty(t, got)
			assert.Len(t, got, 43,
				"a token should carry the same 256 bits of entropy as the session secret")
			assert.Equal(t, got, viper.GetString("diagnostics.profiling.token"),
				"the generated token must be staged for persistence")
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
}
