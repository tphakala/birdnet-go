package conf

import (
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

// oauthProviderIDConfig configures an enabled OIDC provider written with an
// upper-case ID and no issuer URL, which the OIDC rules must reject.
const oauthProviderIDConfig = `
security:
  sessionsecret: "0123456789abcdef0123456789abcdef"
  baseurl: "https://birds.example.com"
  oauthProviders:
    - provider: OIDC
      enabled: true
      clientId: id
      clientSecret: secret
      userId: user@example.com
`

// TestLoad_OAuthProviderIDCaseDoesNotBypassOIDCRules pins that a provider ID
// written in a different case reaches the OIDC validation rules instead of being
// treated as an unknown provider that still forces authentication on.
func TestLoad_OAuthProviderIDCaseDoesNotBypassOIDCRules(t *testing.T) {
	prepareConfigFromYAML(t, oauthProviderIDConfig)

	_, err := Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "issuerUrl is required")
}

func TestNormalizeOAuthProviderIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "upper case", in: "OIDC", want: "oidc"},
		{name: "surrounding space", in: " Google ", want: "google"},
		{name: "already canonical", in: "microsoft", want: "microsoft"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &Settings{Security: Security{OAuthProviders: []OAuthProviderConfig{
				{Provider: tt.in, Enabled: true, ClientID: "id", ClientSecret: "secret"},
			}}}

			s.NormalizeOAuthProviderIDs()
			s.NormalizeOAuthProviderIDs() // idempotent

			require.Len(t, s.Security.OAuthProviders, 1)
			p := s.Security.OAuthProviders[0]
			assert.Equal(t, tt.want, p.Provider)
			assert.True(t, p.Enabled, "normalising must not disable the provider")
			assert.Equal(t, "id", p.ClientID)
			assert.Equal(t, "secret", p.ClientSecret)
		})
	}
}

// TestValidateSecuritySettings_OAuthProviderDuplicates pins that two entries with
// the same provider ID are rejected for every provider kind, enabled or not, with
// the offending index in the message, while a single entry of each kind passes.
func TestValidateSecuritySettings_OAuthProviderDuplicates(t *testing.T) {
	t.Parallel()

	entry := func(provider string, enabled bool) OAuthProviderConfig {
		return OAuthProviderConfig{
			Provider: provider, Enabled: enabled, ClientID: "id", ClientSecret: "secret",
			UserID: "user@example.com", IssuerURL: "https://idp.example.com",
		}
	}

	tests := []struct {
		name      string
		providers []OAuthProviderConfig
		noHost    bool // no Host, so the redirect rule fails as well
		wantErr   string
	}{
		{name: "exact duplicate google", providers: []OAuthProviderConfig{entry("google", true), entry("google", true)}, wantErr: "security.oauthProviders[1]: duplicate"},
		{name: "exact duplicate oidc", providers: []OAuthProviderConfig{entry("oidc", true), entry("oidc", true)}, wantErr: "security.oauthProviders[1]: duplicate"},
		{name: "enabled entry after a different kind", providers: []OAuthProviderConfig{entry("github", true), entry("google", true), entry("google", true)}, wantErr: "security.oauthProviders[2]: duplicate"},
		{name: "one enabled one disabled", providers: []OAuthProviderConfig{entry("google", true), entry("google", false)}, wantErr: "security.oauthProviders[1]: duplicate"},
		{name: "two disabled", providers: []OAuthProviderConfig{entry("github", false), entry("github", false)}, wantErr: "security.oauthProviders[1]: duplicate"},
		{name: "duplicate is reported before the redirect rule", providers: []OAuthProviderConfig{entry("google", true), entry("google", true)}, noHost: true, wantErr: "security.oauthProviders[1]: duplicate"},
		{name: "single entry of each kind", providers: []OAuthProviderConfig{
			entry("google", true), entry("github", true), entry("microsoft", true),
			entry("line", true), entry("kakao", true), entry("oidc", true),
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := Security{Host: "example.com", SessionDuration: 24 * time.Hour, OAuthProviders: tt.providers}
			if tt.noHost {
				s.Host = ""
			}

			err := validateSecuritySettings(&s)

			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// loadWithOAuthProviders loads a config with the given oauthProviders YAML entries
// and returns the providers that survive loading.
func loadWithOAuthProviders(t *testing.T, entries string) []OAuthProviderConfig {
	t.Helper()
	prepareConfigFromYAML(t, `
security:
  sessionsecret: "0123456789abcdef0123456789abcdef"
  baseurl: "https://birds.example.com"
  oauthProviders:
`+entries)
	settings, err := Load()
	require.NoError(t, err, "a duplicate provider ID must never stop the server from starting")
	return settings.Security.OAuthProviders
}

// TestLoad_ReconcilesSameIDOAuthProviders pins that entries sharing a canonical
// provider ID collapse to one at load: the first enabled entry, else the first.
func TestLoad_ReconcilesSameIDOAuthProviders(t *testing.T) {
	entry := func(provider, clientID string, enabled bool, extra string) string {
		return fmt.Sprintf(`    - provider: %s
      enabled: %t
      clientId: %s
      clientSecret: secret
      userId: user@example.com
%s`, provider, enabled, clientID, extra)
	}
	// unfinished is enabled but has no client secret, which incomplete-feature
	// normalisation would disable.
	const unfinished = `    - provider: google
      enabled: true
      clientId: unfinished
      userId: user@example.com
`
	const issuer = "      issuerUrl: https://idp.example.com\n"

	tests := []struct {
		name         string
		entries      string
		wantProvider string
		wantClientID string
		wantEnabled  bool
	}{
		{name: "two enabled, case variant", entries: entry("google", "first", true, "") + entry("Google", "second", true, ""), wantProvider: "google", wantClientID: "first", wantEnabled: true},
		{name: "two enabled oidc", entries: entry("oidc", "first", true, issuer) + entry("OIDC", "second", true, issuer), wantProvider: "oidc", wantClientID: "first", wantEnabled: true},
		{name: "disabled first, enabled second", entries: entry("google", "leftover", false, "") + entry("google", "live", true, ""), wantProvider: "google", wantClientID: "live", wantEnabled: true},
		{name: "enabled entry without credentials first", entries: unfinished + entry("Google", "working", true, ""), wantProvider: "google", wantClientID: "working", wantEnabled: true},
		{name: "disabled complete entry first, enabled unfinished second", entries: entry("google", "complete", false, "") + strings.Replace(unfinished, "google", "Google", 1), wantProvider: "google", wantClientID: "complete", wantEnabled: false},
		{name: "two disabled", entries: entry("github", "first", false, "") + entry("GitHub", "second", false, ""), wantProvider: "github", wantClientID: "first", wantEnabled: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := loadWithOAuthProviders(t, tt.entries)

			require.Len(t, got, 1)
			assert.Equal(t, tt.wantProvider, got[0].Provider)
			assert.Equal(t, tt.wantClientID, got[0].ClientID)
			assert.Equal(t, tt.wantEnabled, got[0].Enabled)
		})
	}
}

// TestReconcileOAuthProviderDuplicates_LogsDroppedEntries pins the dropped entry's
// index, provider ID and enabled state in the warning, and that no secret is logged.
func TestReconcileOAuthProviderDuplicates_LogsDroppedEntries(t *testing.T) {
	logs := logtest.CaptureBufferAt(t, slog.LevelWarn)
	s := &Settings{Security: Security{OAuthProviders: []OAuthProviderConfig{
		{Provider: "google", Enabled: false, ClientID: "a", ClientSecret: "dropped-secret"},
		{Provider: "github", Enabled: true, ClientID: "g", ClientSecret: "kept-secret"},
		{Provider: "google", Enabled: true, ClientID: "b", ClientSecret: "kept-secret"},
	}}}

	s.ReconcileOAuthProviderDuplicates()

	require.Len(t, s.Security.OAuthProviders, 2)
	assert.Equal(t, []string{"github", "google"}, []string{s.Security.OAuthProviders[0].Provider, s.Security.OAuthProviders[1].Provider})
	out := logs.String()
	assert.Contains(t, out, "index=0")
	assert.Contains(t, out, "provider=google")
	assert.NotContains(t, out, "secret")
}

// TestGetOAuthProvider pins the lookup by canonical provider ID.
func TestGetOAuthProvider(t *testing.T) {
	t.Parallel()

	s := &Settings{Security: Security{OAuthProviders: []OAuthProviderConfig{
		{Provider: "github", ClientID: "h"},
		{Provider: "google", ClientID: "g"},
	}}}

	got := s.GetOAuthProvider("google")
	require.NotNil(t, got)
	assert.Equal(t, "g", got.ClientID)
	assert.Nil(t, s.GetOAuthProvider("kakao"))
}

// TestLoad_ReconcileDoesNotRewriteDuplicatesOnDisk pins that dropping a duplicate
// provider entry in memory leaves the file as the user wrote it, even when a later
// migration saves the config.
func TestLoad_ReconcileDoesNotRewriteDuplicatesOnDisk(t *testing.T) {
	_, configPath := loadConfigFromYAML(t, `
security:
  sessionsecret: "0123456789abcdef0123456789abcdef"
  baseurl: "https://birds.example.com"
  oauthProviders:
    - provider: google
      enabled: false
      clientId: leftover
      clientSecret: leftover-secret
    - provider: Google
      enabled: true
      clientId: live
      clientSecret: live-secret
      userId: user@example.com
realtime:
  audio:
    export:
      enabled: true
      type: aac
      path: clips/
      length: 15
      bitrate: ""
`)

	persisted := readPersistedSettings(t, configPath)

	require.Len(t, persisted.Security.OAuthProviders, 2)
	assert.Equal(t, "google", persisted.Security.OAuthProviders[0].Provider)
	assert.Equal(t, "Google", persisted.Security.OAuthProviders[1].Provider)
	assert.Equal(t, "leftover-secret", persisted.Security.OAuthProviders[0].ClientSecret)
	assert.Equal(t, DefaultAudioExportBitrate, persisted.Realtime.Audio.Export.Bitrate,
		"the migration that saves the file must have run")
}
