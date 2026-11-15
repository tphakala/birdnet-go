package conf

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

// TestValidateSecuritySettings_OIDC covers the OIDC rules that block startup. An
// unusable issuer URL and a missing redirect source both stay fatal, because
// enabling OIDC is the setting that can lock an operator out: the provider counts
// towards IsAuthenticationEnabled as soon as it has credentials, so accepting either
// shape would make authentication required while no sign-in can complete. The
// settings API validates without normalizing, so this is what answers such a save
// with an error rather than a 200 and a locked door.
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
			name: "one enabled and one disabled OIDC provider - should pass",
			modify: func(s *Security) {
				s.OAuthProviders = []OAuthProviderConfig{
					{Provider: "oidc", Enabled: true, ClientID: "id1", ClientSecret: "secret1", IssuerURL: "https://idp1.example.com"},
					{Provider: "oidc", Enabled: false, ClientID: "id2", ClientSecret: "secret2", IssuerURL: "https://idp2.example.com"},
				}
			},
			wantErr: false,
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
		{
			name: "OIDC enabled without host or redirectUri - should fail",
			modify: func(s *Security) {
				s.Host = ""
				s.BaseURL = ""
				s.OAuthProviders = []OAuthProviderConfig{
					{Provider: "oidc", Enabled: true, ClientID: "id", ClientSecret: "secret", IssuerURL: "https://idp.example.com", UserID: "user@example.com"},
				}
			},
			wantErr: true,
			errMsg:  "redirect URL",
		},
		{
			name: "OIDC enabled with explicit redirectUri but no host - should pass",
			modify: func(s *Security) {
				s.Host = ""
				s.BaseURL = ""
				s.OAuthProviders = []OAuthProviderConfig{
					{Provider: "oidc", Enabled: true, ClientID: "id", ClientSecret: "secret", IssuerURL: "https://idp.example.com", RedirectURI: "https://myapp.example.com/auth/openid-connect/callback", UserID: "user@example.com"},
				}
			},
			wantErr: false,
		},
		{
			name: "OIDC enabled with baseUrl but no host - should pass",
			modify: func(s *Security) {
				s.Host = ""
				s.BaseURL = "https://myapp.example.com"
				s.OAuthProviders = []OAuthProviderConfig{
					{Provider: "oidc", Enabled: true, ClientID: "id", ClientSecret: "secret", IssuerURL: "https://idp.example.com", UserID: "user@example.com"},
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

// TestValidateSecuritySettings_OIDCIssuerNeverEchoesCredentials pins that a
// rejected issuer URL reaches neither the error text nor the error context
// (which is reported to telemetry) with its userinfo.
func TestValidateSecuritySettings_OIDCIssuerNeverEchoesCredentials(t *testing.T) {
	t.Parallel()

	const password = "s3cr3tpw"

	tests := []struct {
		name   string
		issuer string
		// username is asserted absent too.
		username string
	}{
		{name: "unparseable with userinfo", issuer: "https://admin:" + password + "@idp.example.com:notaport", username: "admin"},
		{name: "password with a slash", issuer: "https://admin:s3cr3t/pw@idp.example.com:notaport", username: "admin"},
		{name: "scheme-less with userinfo", issuer: "admin:" + password + "@idp.example.com", username: "admin"},
		{name: "unsupported scheme with userinfo", issuer: "ftp://admin:" + password + "@idp.example.com", username: "admin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := Security{
				Host:            "example.com",
				SessionDuration: 24 * time.Hour,
				OAuthProviders: []OAuthProviderConfig{
					{Provider: "oidc", Enabled: true, ClientID: "id", ClientSecret: "secret", IssuerURL: tt.issuer, UserID: "user@example.com"},
				},
			}
			err := validateSecuritySettings(&s)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "s3cr3t")
			if tt.username != "" {
				assert.NotContains(t, err.Error(), tt.username)
			}

			enhanced, ok := errors.AsType[*errors.EnhancedError](err)
			require.True(t, ok, "issuer errors must be enhanced errors")
			issuerCtx, found := enhanced.GetContext()["issuer_url"].(string)
			require.True(t, found, "the issuer_url context must be set")
			assert.NotContains(t, issuerCtx, "s3cr3t")
			if tt.username != "" {
				assert.NotContains(t, issuerCtx, tt.username)
			}
		})
	}
}

// TestValidateSecuritySettings_OIDCHTTPWarningNeverEchoesCredentials pins that
// the warning for a plain HTTP issuer does not log the issuer's userinfo.
func TestValidateSecuritySettings_OIDCHTTPWarningNeverEchoesCredentials(t *testing.T) {
	// Not parallel: captures the process-global logger.
	buf := logtest.CaptureBuffer(t)

	s := Security{
		Host:            "example.com",
		SessionDuration: 24 * time.Hour,
		OAuthProviders: []OAuthProviderConfig{
			{Provider: "oidc", Enabled: true, ClientID: "id", ClientSecret: "secret", IssuerURL: "http://admin:s3cr3tpw@localhost:8080", UserID: "user@example.com"},
		},
	}
	require.NoError(t, validateSecuritySettings(&s))

	out := buf.String()
	require.Contains(t, out, "OIDC issuerUrl uses HTTP", "the HTTP warning must be logged")
	assert.NotContains(t, out, "s3cr3tpw")
	assert.NotContains(t, out, "admin")
}
