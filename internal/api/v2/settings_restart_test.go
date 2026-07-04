package api

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestWebserverSettingsChanged_BaseURL verifies that a security.baseURL change is
// detected as restart-requiring. BaseURL feeds the AutoTLS HTTP->HTTPS redirect
// authority and the session-cookie Secure decision, both captured once at server
// start, so editing it alone needs a restart to take effect.
//
// Ported from upstream #3786; the branch's older toast-based restart framework
// is used, so only the pure detector is asserted here.
func TestWebserverSettingsChanged_BaseURL(t *testing.T) {
	t.Parallel()

	base := getTestSettings(t)
	updated := conf.CloneSettings(base)
	updated.Security.BaseURL = "https://birds.example.com"

	assert.True(t, webserverSettingsChanged(base, updated),
		"a security.baseURL change must be flagged restart-required")
	assert.False(t, webserverSettingsChanged(base, conf.CloneSettings(base)),
		"identical settings must not be flagged restart-required")
}

// TestOAuthProvidersChanged verifies the OAuth provider registration detector.
// Registration fields (client credentials, callback URI, issuer, scopes, and the
// enabled/added/removed set) are restart-required; the per-provider UserID
// allowlist is excluded because it is read live by the auth check.
//
// Ported from upstream #3786 (apitest.NewValidTestSettings replaced with the
// branch's getTestSettings helper).
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
			base := getTestSettings(t)
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
