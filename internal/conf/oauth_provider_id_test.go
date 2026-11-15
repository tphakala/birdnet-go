package conf

import (
	"testing"

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
