package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/microsoftonline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
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

func TestConfigProviderFor(t *testing.T) {
	t.Parallel()

	s := &Server{}

	tests := []struct {
		name         string
		gothProvider string
		want         string
	}{
		{name: "google", gothProvider: security.ProviderGoogle, want: security.ConfigGoogle},
		{name: "oidc", gothProvider: security.ProviderOIDC, want: security.ConfigOIDC},
		{name: "case insensitive", gothProvider: "OpenID-Connect", want: security.ConfigOIDC},
		{name: "unknown goth provider", gothProvider: "facebook", want: ""},
		{name: "empty", gothProvider: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, s.configProviderFor(tt.gothProvider))
		})
	}
}

func TestOAuthIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		email  string
		userID string
		want   string
	}{
		{name: "prefers email", email: "user@example.com", userID: "sub-123", want: "user@example.com"},
		{name: "falls back to userID", email: "", userID: "sub-123", want: "sub-123"},
		{name: "both empty", email: "", userID: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, oauthIdentity(tt.email, tt.userID))
		})
	}
}

// Not parallel: each subtest publishes its own snapshot to the process-global
// settings (read by isAllowedOAuthUser via conf.GetSettings) and restores it on
// cleanup, so the subtests neither depend on the global being nil nor couple
// with the sibling TestIsAllowedOAuthUserHotReload.
func TestIsAllowedOAuthUser(t *testing.T) {
	tests := []struct {
		name         string
		gothProvider string
		userID       string
		email        string
		providers    []conf.OAuthProviderConfig
		want         bool
		wantReason   oauthDenyReason
	}{
		{
			name:         "OIDC user allowed by email",
			gothProvider: security.ProviderOIDC,
			userID:       "sub-123",
			email:        "user@example.com",
			providers: []conf.OAuthProviderConfig{
				{Provider: "oidc", Enabled: true, ClientID: "id", ClientSecret: "s", IssuerURL: "https://idp.example.com", UserID: "user@example.com"},
			},
			want:       true,
			wantReason: oauthAuthorized,
		},
		{
			name:         "OIDC user allowed by userID (sub claim)",
			gothProvider: security.ProviderOIDC,
			userID:       "sub-123",
			email:        "",
			providers: []conf.OAuthProviderConfig{
				{Provider: "oidc", Enabled: true, ClientID: "id", ClientSecret: "s", IssuerURL: "https://idp.example.com", UserID: "sub-123"},
			},
			want:       true,
			wantReason: oauthAuthorized,
		},
		{
			name:         "OIDC user not in allowed list",
			gothProvider: security.ProviderOIDC,
			userID:       "sub-456",
			email:        "other@example.com",
			providers: []conf.OAuthProviderConfig{
				{Provider: "oidc", Enabled: true, ClientID: "id", ClientSecret: "s", IssuerURL: "https://idp.example.com", UserID: "user@example.com"},
			},
			want:       false,
			wantReason: oauthDenyUserNotAllowed,
		},
		{
			name:         "OIDC provider disabled",
			gothProvider: security.ProviderOIDC,
			userID:       "sub-123",
			email:        "user@example.com",
			providers: []conf.OAuthProviderConfig{
				{Provider: "oidc", Enabled: false, ClientID: "id", ClientSecret: "s", IssuerURL: "https://idp.example.com", UserID: "user@example.com"},
			},
			want:       false,
			wantReason: oauthDenyProviderDisabled,
		},
		{
			name:         "OIDC empty UserID configured - deny all (issue #3381)",
			gothProvider: security.ProviderOIDC,
			userID:       "sub-123",
			email:        "user@example.com",
			providers: []conf.OAuthProviderConfig{
				{Provider: "oidc", Enabled: true, ClientID: "id", ClientSecret: "s", IssuerURL: "https://idp.example.com", UserID: ""},
			},
			want:       false,
			wantReason: oauthDenyAllowlistEmpty,
		},
		{
			name:         "OIDC whitespace-only UserID treated as empty allowlist",
			gothProvider: security.ProviderOIDC,
			userID:       "sub-123",
			email:        "user@example.com",
			providers: []conf.OAuthProviderConfig{
				{Provider: "oidc", Enabled: true, ClientID: "id", ClientSecret: "s", IssuerURL: "https://idp.example.com", UserID: "  ,  "},
			},
			want:       false,
			wantReason: oauthDenyAllowlistEmpty,
		},
		{
			name:         "Google user via OAuthProviders array",
			gothProvider: security.ProviderGoogle,
			userID:       "google-123",
			email:        "user@gmail.com",
			providers: []conf.OAuthProviderConfig{
				{Provider: "google", Enabled: true, ClientID: "id", ClientSecret: "s", UserID: "user@gmail.com"},
			},
			want:       true,
			wantReason: oauthAuthorized,
		},
		{
			name:         "multiple allowed users comma separated",
			gothProvider: security.ProviderOIDC,
			userID:       "sub-456",
			email:        "second@example.com",
			providers: []conf.OAuthProviderConfig{
				{Provider: "oidc", Enabled: true, ClientID: "id", ClientSecret: "s", IssuerURL: "https://idp.example.com", UserID: "first@example.com, second@example.com"},
			},
			want:       true,
			wantReason: oauthAuthorized,
		},
		{
			name:         "unknown goth provider name",
			gothProvider: "facebook",
			userID:       "fb-123",
			email:        "user@fb.com",
			providers:    []conf.OAuthProviderConfig{},
			want:         false,
			wantReason:   oauthDenyProviderUnknown,
		},
		{
			name:         "provider recognized but not configured in settings",
			gothProvider: security.ProviderGoogle,
			userID:       "google-123",
			email:        "user@gmail.com",
			providers:    []conf.OAuthProviderConfig{},
			want:         false,
			wantReason:   oauthDenyProviderMissing,
		},
		{
			name:         "nil settings",
			gothProvider: security.ProviderOIDC,
			userID:       "sub-123",
			email:        "user@example.com",
			providers:    nil,
			want:         false,
			wantReason:   oauthDenySettingsMissing,
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
			// Publish this subtest's snapshot so isAllowedOAuthUser (which reads
			// conf.GetSettings via currentSettings) resolves to it rather than
			// depending on the global being nil. Restored on cleanup.
			prev := conf.GetSettings()
			conftest.SetTestSettings(settings)
			t.Cleanup(func() { conftest.SetTestSettings(prev) })
			got, reason := s.isAllowedOAuthUser(tt.gothProvider, tt.userID, tt.email)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantReason, reason, "deny reason should classify the cause for security.log diagnostics")
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

	// Save and restore the prior global snapshot so this test does not leave the
	// process-global settings mutated for sibling tests, regardless of ordering.
	prev := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	// Sanity: with only the stale startup snapshot, the user is not allowed.
	conftest.SetTestSettings(startup)
	allowed, _ := s.isAllowedOAuthUser(security.ProviderGoogle, "google-123", "user@gmail.com")
	assert.False(t, allowed, "user should not be allowed under the startup snapshot")

	// Simulate a UI save: enable the provider and allow the user, published via
	// the atomic pointer.
	updated := &conf.Settings{
		Security: conf.Security{
			OAuthProviders: []conf.OAuthProviderConfig{
				{Provider: "google", Enabled: true, ClientID: "id", ClientSecret: "s", UserID: "user@gmail.com"},
			},
		},
	}
	conftest.SetTestSettings(updated)

	allowedAfter, _ := s.isAllowedOAuthUser(security.ProviderGoogle, "google-123", "user@gmail.com")
	assert.True(t, allowedAfter, "allowlist change made via UI must apply without a restart")
}

// beginOAuth runs handleOAuthBegin for the given provider path parameter.
func beginOAuth(t *testing.T, s *Server, provider string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/auth/"+provider, http.NoBody)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetParamNames("provider")
	c.SetParamValues(provider)
	require.NoError(t, s.handleOAuthBegin(c))
	return rec
}

// useOAuthSettings publishes settings with one enabled provider for the test.
func useOAuthSettings(t *testing.T, providerID string) *Server {
	t.Helper()
	settings := &conf.Settings{
		Security: conf.Security{
			OAuthProviders: []conf.OAuthProviderConfig{
				{Provider: providerID, Enabled: true, ClientID: "id", ClientSecret: "secret", UserID: "user@example.com"},
			},
		},
	}
	prev := conf.GetSettings()
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(prev) })
	return &Server{settings: settings}
}

// TestHandleOAuthBegin_UnregisteredProvider covers an enabled provider that goth
// does not know (OIDC discovery failed, or the provider was enabled after
// startup). The visitor gets one fixed sentence and the cause goes to the log.
// Not parallel: it swaps the global logger, settings and goth registry.
func TestHandleOAuthBegin_UnregisteredProvider(t *testing.T) {
	s := useOAuthSettings(t, security.ConfigOIDC)
	goth.ClearProviders()
	t.Cleanup(goth.ClearProviders)
	logs := logtest.CaptureBufferAt(t, slog.LevelWarn)

	rec := beginOAuth(t, s, security.ProviderOIDC)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, oauthProviderUnavailableMessage, rec.Body.String(), "the body must be the fixed sentence and nothing else")
	assert.Contains(t, logs.String(), security.ProviderOIDC)
	assert.Contains(t, logs.String(), "not registered")
}

// TestHandleOAuthBegin_RegisteredProviderRedirects pins that the registration
// check is keyed on the goth name from the URL, not the config ID: Microsoft is
// configured as "microsoft" but registered as "microsoftonline".
// Not parallel: it swaps the settings, the goth registry and gothic.Store.
func TestHandleOAuthBegin_RegisteredProviderRedirects(t *testing.T) {
	s := useOAuthSettings(t, security.ConfigMicrosoft)
	goth.ClearProviders()
	t.Cleanup(goth.ClearProviders)
	goth.UseProviders(microsoftonline.New("id", "secret", "http://localhost/auth/microsoftonline/callback"))
	prevStore := gothic.Store
	gothic.Store = sessions.NewCookieStore([]byte("test-session-secret-long-enough-32"))
	t.Cleanup(func() { gothic.Store = prevStore })

	rec := beginOAuth(t, s, security.ProviderMicrosoft)

	assert.Equal(t, http.StatusTemporaryRedirect, rec.Code)
	assert.Contains(t, rec.Header().Get("Location"), "login.microsoftonline.com")
}
