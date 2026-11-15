package conf

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestValidateSecuritySettings_OAuthProviderDuplicates pins that two enabled
// entries with the same provider ID are rejected for every provider kind, with
// the offending index in the message, while a leftover disabled entry and a
// single entry of each kind still pass.
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
		wantErr   string
	}{
		{name: "exact duplicate google", providers: []OAuthProviderConfig{entry("google", true), entry("google", true)}, wantErr: "security.oauthProviders[1]: duplicate"},
		{name: "exact duplicate oidc", providers: []OAuthProviderConfig{entry("oidc", true), entry("oidc", true)}, wantErr: "security.oauthProviders[1]: duplicate"},
		{name: "enabled entry after a different kind", providers: []OAuthProviderConfig{entry("github", true), entry("google", true), entry("google", true)}, wantErr: "security.oauthProviders[2]: duplicate"},
		{name: "one enabled one disabled", providers: []OAuthProviderConfig{entry("google", true), entry("google", false)}},
		{name: "single entry of each kind", providers: []OAuthProviderConfig{
			entry("google", true), entry("github", true), entry("microsoft", true),
			entry("line", true), entry("kakao", true), entry("oidc", true),
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := Security{Host: "example.com", SessionDuration: 24 * time.Hour, OAuthProviders: tt.providers}

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

// TestLoad_RejectsCaseVariantOAuthProviderDuplicates pins that entries differing
// only in the case of the ID are duplicates once Load canonicalises them.
func TestLoad_RejectsCaseVariantOAuthProviderDuplicates(t *testing.T) {
	prepareConfigFromYAML(t, `
security:
  sessionsecret: "0123456789abcdef0123456789abcdef"
  baseurl: "https://birds.example.com"
  oauthProviders:
    - provider: google
      enabled: true
      clientId: id1
      clientSecret: secret1
      userId: user@example.com
    - provider: Google
      enabled: true
      clientId: id2
      clientSecret: secret2
      userId: user@example.com
`)

	_, err := Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}
