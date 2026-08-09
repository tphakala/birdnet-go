package api

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/restart"
)

// TestRestartDetectors verifies the pure change-detector functions that decide
// whether a settings change requires a restart. These do not touch the global
// restart state, so they are safe to run in parallel.
func TestRestartDetectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(s *conf.Settings)
		changed bool
		detect  func(old, current *conf.Settings) bool
	}{
		{
			name:    "webserver port change requires restart",
			mutate:  func(s *conf.Settings) { s.WebServer.Port = "9999" },
			changed: true,
			detect:  webserverSettingsChanged,
		},
		{
			// Regression guard: Debug hot-reloads (registry category `fresh`) and
			// must NOT be treated as restart-requiring.
			name:    "webserver debug change does not require restart",
			mutate:  func(s *conf.Settings) { s.WebServer.Debug = !s.WebServer.Debug },
			changed: false,
			detect:  webserverSettingsChanged,
		},
		{
			name:    "webserver basepath change requires restart",
			mutate:  func(s *conf.Settings) { s.WebServer.BasePath = "/birdnet" },
			changed: true,
			detect:  webserverSettingsChanged,
		},
		{
			name:    "webserver enable terminal change requires restart",
			mutate:  func(s *conf.Settings) { s.WebServer.EnableTerminal = !s.WebServer.EnableTerminal },
			changed: true,
			detect:  webserverSettingsChanged,
		},
		{
			name:    "security TLS mode change requires restart",
			mutate:  func(s *conf.Settings) { s.Security.TLSMode = conf.TLSModeSelfSigned },
			changed: true,
			detect:  webserverSettingsChanged,
		},
		{
			// BaseURL feeds RedirectAuthority and the session-cookie Secure decision,
			// both captured at server start, so a change is restart-required.
			name:    "security base URL change requires restart",
			mutate:  func(s *conf.Settings) { s.Security.BaseURL = "https://birds.example.com" },
			changed: true,
			detect:  webserverSettingsChanged,
		},
		{
			name:    "sqlite path change requires restart",
			mutate:  func(s *conf.Settings) { s.Output.SQLite.Path = "other.db" },
			changed: true,
			detect:  outputSettingsChanged,
		},
		{
			name:    "mysql host change requires restart",
			mutate:  func(s *conf.Settings) { s.Output.MySQL.Host = "db.example.com" },
			changed: true,
			detect:  outputSettingsChanged,
		},
		{
			name:    "logging level change requires restart",
			mutate:  func(s *conf.Settings) { s.Logging.Level = "debug" },
			changed: true,
			detect:  loggingSettingsChanged,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			base := apitest.NewValidTestSettings()
			updated := conf.CloneSettings(base)
			tt.mutate(updated)

			assert.Equal(t, tt.changed, tt.detect(base, updated),
				"detector result mismatch for %q", tt.name)
			// Identical snapshots must always report no change.
			assert.False(t, tt.detect(base, conf.CloneSettings(base)),
				"detector reported a change for identical settings in %q", tt.name)
		})
	}
}

// TestOAuthProvidersChanged verifies the OAuth provider registration detector.
// Registration fields (client credentials, callback URI, issuer, scopes, and the
// enabled/added/removed set) are restart-required; the per-provider UserID
// allowlist is excluded because it is read live by the auth check.
func TestOAuthProvidersChanged(t *testing.T) {
	t.Parallel()

	provider := func(id, clientID, userID string, enabled bool) conf.OAuthProviderConfig {
		return conf.OAuthProviderConfig{
			Provider:     id,
			Enabled:      enabled,
			ClientID:     clientID,
			ClientSecret: "secret",
			UserID:       userID,
		}
	}

	tests := []struct {
		name    string
		old     []conf.OAuthProviderConfig
		current []conf.OAuthProviderConfig
		changed bool
	}{
		{
			name:    "no providers, no change",
			old:     nil,
			current: nil,
			changed: false,
		},
		{
			name:    "provider added requires restart",
			old:     nil,
			current: []conf.OAuthProviderConfig{provider("google", "cid", "user@example.com", true)},
			changed: true,
		},
		{
			name:    "provider removed requires restart",
			old:     []conf.OAuthProviderConfig{provider("google", "cid", "user@example.com", true)},
			current: nil,
			changed: true,
		},
		{
			name:    "client id change requires restart",
			old:     []conf.OAuthProviderConfig{provider("google", "old", "user@example.com", true)},
			current: []conf.OAuthProviderConfig{provider("google", "new", "user@example.com", true)},
			changed: true,
		},
		{
			name:    "client secret change requires restart",
			old:     []conf.OAuthProviderConfig{{Provider: "google", Enabled: true, ClientID: "cid", ClientSecret: "old-secret"}},
			current: []conf.OAuthProviderConfig{{Provider: "google", Enabled: true, ClientID: "cid", ClientSecret: "new-secret"}},
			changed: true,
		},
		{
			name:    "redirect uri change requires restart",
			old:     []conf.OAuthProviderConfig{{Provider: "google", Enabled: true, ClientID: "cid", ClientSecret: "s"}},
			current: []conf.OAuthProviderConfig{{Provider: "google", Enabled: true, ClientID: "cid", ClientSecret: "s", RedirectURI: "https://birds.example.com/auth/google/callback"}},
			changed: true,
		},
		{
			name:    "oidc issuer url change requires restart",
			old:     []conf.OAuthProviderConfig{{Provider: "oidc", Enabled: true, ClientID: "cid", ClientSecret: "s", IssuerURL: "https://idp-a.example.com"}},
			current: []conf.OAuthProviderConfig{{Provider: "oidc", Enabled: true, ClientID: "cid", ClientSecret: "s", IssuerURL: "https://idp-b.example.com"}},
			changed: true,
		},
		{
			name:    "enabled toggle requires restart",
			old:     []conf.OAuthProviderConfig{provider("google", "cid", "user@example.com", false)},
			current: []conf.OAuthProviderConfig{provider("google", "cid", "user@example.com", true)},
			changed: true,
		},
		{
			name:    "allowlist-only change hot-reloads (no restart)",
			old:     []conf.OAuthProviderConfig{provider("google", "cid", "old@example.com", true)},
			current: []conf.OAuthProviderConfig{provider("google", "cid", "new@example.com", true)},
			changed: false,
		},
		{
			name: "reordering providers is not a change",
			old: []conf.OAuthProviderConfig{
				provider("google", "gid", "g@example.com", true),
				provider("github", "hid", "h@example.com", true),
			},
			current: []conf.OAuthProviderConfig{
				provider("github", "hid", "h@example.com", true),
				provider("google", "gid", "g@example.com", true),
			},
			changed: false,
		},
		{
			name:    "oidc scopes change requires restart",
			old:     []conf.OAuthProviderConfig{{Provider: "oidc", Enabled: true, ClientID: "cid", ClientSecret: "s", Scopes: []string{"openid"}}},
			current: []conf.OAuthProviderConfig{{Provider: "oidc", Enabled: true, ClientID: "cid", ClientSecret: "s", Scopes: []string{"openid", "email"}}},
			changed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			base := apitest.NewValidTestSettings()
			oldSettings := conf.CloneSettings(base)
			oldSettings.Security.OAuthProviders = tt.old
			newSettings := conf.CloneSettings(base)
			newSettings.Security.OAuthProviders = tt.current

			assert.Equal(t, tt.changed, oauthProvidersChanged(oldSettings, newSettings),
				"detector result mismatch for %q", tt.name)
			// Identical snapshots must always report no change.
			assert.False(t, oauthProvidersChanged(oldSettings, conf.CloneSettings(oldSettings)),
				"detector reported a change for identical settings in %q", tt.name)
		})
	}
}

// TestHandleSettingsChangesMarksRestart verifies that a restart-requiring change
// marks the global restart flag with the expected reason key. The restart state
// is process-global, so this test must run serially and reset around itself.
func TestHandleSettingsChangesMarksRestart(t *testing.T) {
	restart.Reset()
	t.Cleanup(restart.Reset)

	c := createTestController(t)
	base := apitest.NewValidTestSettings()
	updated := conf.CloneSettings(base)
	updated.WebServer.Port = "9999"

	require.NoError(t, c.handleSettingsChanges(base, updated))
	assert.True(t, restart.IsRestartRequired(), "expected restart to be marked after web server port change")
	assert.Contains(t, restart.GetRestartReasons(), reasonWebserverRestart)
}

// TestHandleSettingsChangesMarksOAuthRestart verifies that adding an OAuth provider
// marks the global restart flag with the OAuth reason key. Serial because it
// inspects the process-global restart state.
func TestHandleSettingsChangesMarksOAuthRestart(t *testing.T) {
	restart.Reset()
	t.Cleanup(restart.Reset)

	c := createTestController(t)
	base := apitest.NewValidTestSettings()
	updated := conf.CloneSettings(base)
	updated.Security.OAuthProviders = append(updated.Security.OAuthProviders, conf.OAuthProviderConfig{
		Provider:     "google",
		Enabled:      true,
		ClientID:     "cid",
		ClientSecret: "secret",
	})

	require.NoError(t, c.handleSettingsChanges(base, updated))
	assert.True(t, restart.IsRestartRequired(), "expected restart to be marked after adding an OAuth provider")
	assert.Contains(t, restart.GetRestartReasons(), reasonOAuthRestart)
}

// TestHandleSettingsChangesNoRestartForHotReloadField verifies that changing a
// hot-reloadable field (WebServer.Debug) does NOT mark a restart. Serial because
// it inspects the process-global restart state.
func TestHandleSettingsChangesNoRestartForHotReloadField(t *testing.T) {
	restart.Reset()
	t.Cleanup(restart.Reset)

	c := createTestController(t)
	base := apitest.NewValidTestSettings()
	updated := conf.CloneSettings(base)
	updated.WebServer.Debug = !base.WebServer.Debug

	require.NoError(t, c.handleSettingsChanges(base, updated))
	assert.False(t, restart.IsRestartRequired(), "hot-reloadable Debug change must not mark a restart")
}

// TestRestartRequiringChecksMatchTable guards against name drift: every key in
// restartRequiringChecks must correspond to an actual settingsChangeChecks entry,
// otherwise a renamed table entry would silently stop marking restart.
func TestRestartRequiringChecksMatchTable(t *testing.T) {
	t.Parallel()

	names := make(map[string]bool, len(settingsChangeChecks))
	for i := range settingsChangeChecks {
		names[settingsChangeChecks[i].name] = true
	}
	for name := range restartRequiringChecks {
		assert.True(t, names[name],
			"restartRequiringChecks key %q has no matching settingsChangeChecks entry", name)
	}
}

// TestHotReloadRestartFieldsCovered is the drift guard: every settings field the
// hot-reload registry classifies as `restart` must be either covered by a
// restart-marking detector (restartRequiringChecks) or explicitly exempted with
// a reason. This is a one-way implication: restartRequiringChecks may also cover
// `notify`/`fresh` fields (web server, TLS) that are not in the `restart`
// category, so we do not assert the reverse.
func TestHotReloadRestartFieldsCovered(t *testing.T) {
	t.Parallel()

	// registry restart-category field path -> the settingsChangeChecks entry
	// (by name) that marks restart when it changes.
	restartCovered := map[string]string{
		"Logging": "Logging",
		"Output":  "Database",
	}
	// registry restart-category field paths intentionally NOT wired to a restart
	// marker yet, with the reason. See docs/superpowers/specs/2026-06-16-restart-required-tracking.md.
	restartExempt := map[string]string{
		"BirdNET.ONNXRuntimePath": "model/runtime path; model changes already route through reload_birdnet",
		"BirdNET.OpenVINOPath":    "OpenVINO library path; loaded once at init and not safely unloadable, so it takes effect on restart (mirrors ONNXRuntimePath)",
		"Models":                  "model registry path; restart-vs-reload undecided",
		"Perch":                   "perch model path; not wired",
		"BirdNETV3":               "BirdNET v3.0 model path; not wired",
		"BSG":                     "BSG model path; not wired",
		"Realtime.Audio.Watchdog": "no UI controls (project decision)",
		"LowMemory":               "startup-only memory policy; not exposed via the live settings API",
	}

	for path, entry := range hotReloadRegistry {
		if !slices.Contains(entry.categories, hotReloadRestart) {
			continue
		}
		detector, covered := restartCovered[path]
		_, exempt := restartExempt[path]
		assert.True(t, covered || exempt,
			"registry path %q is category `restart` but is neither covered by a restart-marking "+
				"detector nor exempt; wire it into restartRequiringChecks or add a restartExempt entry", path)
		if covered {
			assert.NotEmpty(t, restartRequiringChecks[detector],
				"restartCovered maps %q to detector %q which is not present in restartRequiringChecks", path, detector)
		}
	}
}
