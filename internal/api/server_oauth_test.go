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

// Not parallel: isAllowedOAuthUser reads the live settings snapshot
// (conf.GetSettings), and the sibling TestIsAllowedOAuthUserHotReload mutates that
// global snapshot. Running serially keeps the two tests from coupling.
func TestIsAllowedOAuthUser(t *testing.T) {
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

// TestIsAllowedOAuthUserHotReload reproduces the issue #3370 class for the OAuth
// allowed-users check: the provider/user allowlist edited through the web UI must
// apply without a restart. The construction-time Server.settings holds a startup
// snapshot where the provider is disabled with no allowed users, while the live
// snapshot (published via the atomic pointer) enables it and adds the user.
// Not parallel: it mutates the global settings snapshot.
func TestIsAllowedOAuthUserHotReload(t *testing.T) {
	// Startup snapshot: provider disabled, no allowed users.
	startup := &conf.Settings{
		Security: conf.Security{
			OAuthProviders: []conf.OAuthProviderConfig{
				{Provider: "google", Enabled: false, ClientID: "id", ClientSecret: "s", UserID: ""},
			},
		},
	}
	s := &Server{settings: startup}

	// Sanity: with only the stale startup snapshot, the user is not allowed.
	conf.SetTestSettings(startup)
	assert.False(t, s.isAllowedOAuthUser(security.ProviderGoogle, "google-123", "user@gmail.com"),
		"user should not be allowed under the startup snapshot")

	// Simulate a UI save: enable the provider and allow the user, published via
	// the atomic pointer.
	updated := &conf.Settings{
		Security: conf.Security{
			OAuthProviders: []conf.OAuthProviderConfig{
				{Provider: "google", Enabled: true, ClientID: "id", ClientSecret: "s", UserID: "user@gmail.com"},
			},
		},
	}
	conf.SetTestSettings(updated)
	t.Cleanup(func() { conf.SetTestSettings(nil) })

	assert.True(t, s.isAllowedOAuthUser(security.ProviderGoogle, "google-123", "user@gmail.com"),
		"allowlist change made via UI must apply without a restart")
}
