package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/security"
)

func TestIsValidOAuthProvider(t *testing.T) {
	t.Parallel()

	s := &Server{}

	tests := []struct {
		name     string
		provider string
		want     bool
	}{
		{name: "google", provider: security.ProviderGoogle, want: true},
		{name: "github", provider: security.ProviderGitHub, want: true},
		{name: "microsoftonline", provider: security.ProviderMicrosoft, want: true},
		{name: "line", provider: security.ProviderLine, want: true},
		{name: "kakao", provider: security.ProviderKakao, want: true},
		{name: "openid-connect", provider: security.ProviderOIDC, want: true},
		{name: "case insensitive", provider: "Google", want: true},
		{name: "unknown provider", provider: "facebook", want: false},
		{name: "empty string", provider: "", want: false},
		{name: "config name not goth name", provider: "microsoft", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := s.isValidOAuthProvider(tt.provider)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsAllowedOAuthUser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		gothProvider string
		userID       string
		email        string
		providers    []conf.OAuthProviderConfig
		want         bool
	}{
		{
			name:         "OIDC user allowed by email",
			gothProvider: security.ProviderOIDC,
			userID:       "sub-123",
			email:        "user@example.com",
			providers: []conf.OAuthProviderConfig{
				{Provider: "oidc", Enabled: true, ClientID: "id", ClientSecret: "s", IssuerURL: "https://idp.example.com", UserID: "user@example.com"},
			},
			want: true,
		},
		{
			name:         "OIDC user allowed by userID (sub claim)",
			gothProvider: security.ProviderOIDC,
			userID:       "sub-123",
			email:        "",
			providers: []conf.OAuthProviderConfig{
				{Provider: "oidc", Enabled: true, ClientID: "id", ClientSecret: "s", IssuerURL: "https://idp.example.com", UserID: "sub-123"},
			},
			want: true,
		},
		{
			name:         "OIDC user not in allowed list",
			gothProvider: security.ProviderOIDC,
			userID:       "sub-456",
			email:        "other@example.com",
			providers: []conf.OAuthProviderConfig{
				{Provider: "oidc", Enabled: true, ClientID: "id", ClientSecret: "s", IssuerURL: "https://idp.example.com", UserID: "user@example.com"},
			},
			want: false,
		},
		{
			name:         "OIDC provider disabled",
			gothProvider: security.ProviderOIDC,
			userID:       "sub-123",
			email:        "user@example.com",
			providers: []conf.OAuthProviderConfig{
				{Provider: "oidc", Enabled: false, ClientID: "id", ClientSecret: "s", IssuerURL: "https://idp.example.com", UserID: "user@example.com"},
			},
			want: false,
		},
		{
			name:         "OIDC empty UserID configured - deny all",
			gothProvider: security.ProviderOIDC,
			userID:       "sub-123",
			email:        "user@example.com",
			providers: []conf.OAuthProviderConfig{
				{Provider: "oidc", Enabled: true, ClientID: "id", ClientSecret: "s", IssuerURL: "https://idp.example.com", UserID: ""},
			},
			want: false,
		},
		{
			name:         "Google user via OAuthProviders array",
			gothProvider: security.ProviderGoogle,
			userID:       "google-123",
			email:        "user@gmail.com",
			providers: []conf.OAuthProviderConfig{
				{Provider: "google", Enabled: true, ClientID: "id", ClientSecret: "s", UserID: "user@gmail.com"},
			},
			want: true,
		},
		{
			name:         "multiple allowed users comma separated",
			gothProvider: security.ProviderOIDC,
			userID:       "sub-456",
			email:        "second@example.com",
			providers: []conf.OAuthProviderConfig{
				{Provider: "oidc", Enabled: true, ClientID: "id", ClientSecret: "s", IssuerURL: "https://idp.example.com", UserID: "first@example.com, second@example.com"},
			},
			want: true,
		},
		{
			name:         "unknown goth provider name",
			gothProvider: "facebook",
			userID:       "fb-123",
			email:        "user@fb.com",
			providers:    []conf.OAuthProviderConfig{},
			want:         false,
		},
		{
			name:         "nil settings",
			gothProvider: security.ProviderOIDC,
			userID:       "sub-123",
			email:        "user@example.com",
			providers:    nil,
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var settings *conf.Settings
			if tt.providers != nil {
				settings = &conf.Settings{
					Security: conf.Security{
						OAuthProviders: tt.providers,
					},
				}
			}
			s := &Server{settings: settings}
			got := s.isAllowedOAuthUser(tt.gothProvider, tt.userID, tt.email)
			assert.Equal(t, tt.want, got)
		})
	}
}
