package conf

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateSecuritySettings_OIDC(t *testing.T) {
	t.Parallel()

	validBase := func() Security {
		return Security{
			Host:            "example.com",
			SessionDuration: 24 * time.Hour,
		}
	}

	tests := []struct {
		name    string
		modify  func(s *Security)
		wantErr bool
		errMsg  string
	}{
		{
			name: "OIDC enabled with valid issuerUrl",
			modify: func(s *Security) {
				s.OAuthProviders = []OAuthProviderConfig{
					{Provider: "oidc", Enabled: true, ClientID: "id", ClientSecret: "secret", IssuerURL: "https://idp.example.com", UserID: "user@example.com"},
				}
			},
			wantErr: false,
		},
		{
			name: "OIDC enabled without issuerUrl - should fail",
			modify: func(s *Security) {
				s.OAuthProviders = []OAuthProviderConfig{
					{Provider: "oidc", Enabled: true, ClientID: "id", ClientSecret: "secret", UserID: "user@example.com"},
				}
			},
			wantErr: true,
			errMsg:  "issuerUrl",
		},
		{
			name: "OIDC enabled with empty issuerUrl - should fail",
			modify: func(s *Security) {
				s.OAuthProviders = []OAuthProviderConfig{
					{Provider: "oidc", Enabled: true, ClientID: "id", ClientSecret: "secret", IssuerURL: "", UserID: "user@example.com"},
				}
			},
			wantErr: true,
			errMsg:  "issuerUrl",
		},
		{
			name: "OIDC enabled with invalid issuerUrl - should fail",
			modify: func(s *Security) {
				s.OAuthProviders = []OAuthProviderConfig{
					{Provider: "oidc", Enabled: true, ClientID: "id", ClientSecret: "secret", IssuerURL: "://bad", UserID: "user@example.com"},
				}
			},
			wantErr: true,
			errMsg:  "issuerUrl",
		},
		{
			name: "OIDC disabled with missing issuerUrl - should pass",
			modify: func(s *Security) {
				s.OAuthProviders = []OAuthProviderConfig{
					{Provider: "oidc", Enabled: false, ClientID: "id", ClientSecret: "secret"},
				}
			},
			wantErr: false,
		},
		{
			name: "duplicate OIDC providers - should fail",
			modify: func(s *Security) {
				s.OAuthProviders = []OAuthProviderConfig{
					{Provider: "oidc", Enabled: true, ClientID: "id1", ClientSecret: "secret1", IssuerURL: "https://idp1.example.com", UserID: "user@example.com"},
					{Provider: "oidc", Enabled: true, ClientID: "id2", ClientSecret: "secret2", IssuerURL: "https://idp2.example.com", UserID: "user@example.com"},
				}
			},
			wantErr: true,
			errMsg:  "duplicate",
		},
		{
			name: "OIDC with HTTP issuerUrl - should pass with warning",
			modify: func(s *Security) {
				s.OAuthProviders = []OAuthProviderConfig{
					{Provider: "oidc", Enabled: true, ClientID: "id", ClientSecret: "secret", IssuerURL: "http://localhost:8080", UserID: "user@example.com"},
				}
			},
			wantErr: false,
		},
		{
			name: "OIDC coexists with social auth",
			modify: func(s *Security) {
				s.OAuthProviders = []OAuthProviderConfig{
					{Provider: "google", Enabled: true, ClientID: "gid", ClientSecret: "gsecret", UserID: "user@gmail.com"},
					{Provider: "oidc", Enabled: true, ClientID: "oid", ClientSecret: "osecret", IssuerURL: "https://idp.example.com", UserID: "user@example.com"},
				}
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := validBase()
			tt.modify(&s)
			err := validateSecuritySettings(&s)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
