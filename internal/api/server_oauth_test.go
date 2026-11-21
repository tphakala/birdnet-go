package api

import (
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/faux"
	"github.com/markbates/goth/providers/microsoftonline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
	"github.com/tphakala/birdnet-go/internal/security"
)

func TestCanonicalOAuthProvider(t *testing.T) {
	t.Parallel()

	s := &Server{}

	tests := []struct {
		name         string
		pathName     string
		wantConfig   string
		wantGoth     string
		wantResolved bool
	}{
		{name: "google", pathName: security.ProviderGoogle, wantConfig: security.ConfigGoogle, wantGoth: security.ProviderGoogle, wantResolved: true},
		{name: "microsoftonline", pathName: security.ProviderMicrosoft, wantConfig: security.ConfigMicrosoft, wantGoth: security.ProviderMicrosoft, wantResolved: true},
		{name: "openid-connect", pathName: security.ProviderOIDC, wantConfig: security.ConfigOIDC, wantGoth: security.ProviderOIDC, wantResolved: true},
		{name: "mixed case microsoft", pathName: "MicrosoftOnline", wantConfig: security.ConfigMicrosoft, wantGoth: security.ProviderMicrosoft, wantResolved: true},
		{name: "unknown provider", pathName: "facebook"},
		{name: "empty string", pathName: ""},
		{name: "config name not goth name", pathName: "microsoft"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			configProvider, gothProvider, ok := s.canonicalOAuthProvider(tt.pathName)
			assert.Equal(t, tt.wantResolved, ok)
			assert.Equal(t, tt.wantConfig, configProvider)
			assert.Equal(t, tt.wantGoth, gothProvider)
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
	return &Server{settings: settings, oauth2Server: security.NewOAuth2ServerForTest(settings)}
}

// TestHandleOAuthBegin_UnregisteredProvider covers an enabled provider that goth
// does not know (OIDC discovery failed or has not finished). The visitor gets one fixed sentence and the cause goes to the log.
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
	// The cause returned by ProviderRegistrationError must reach the log.
	assert.Contains(t, logs.String(), "registration after startup or a settings change has not completed")
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

// TestHandleOAuthBegin_RegisteredProviderMixedCase pins that a path whose casing
// differs from the goth name (the provider checks before it match case
// insensitively) is not reported as an unregistered provider when goth has it.
// Not parallel: it swaps the settings, the goth registry and gothic.Store.
func TestHandleOAuthBegin_RegisteredProviderMixedCase(t *testing.T) {
	s := useOAuthSettings(t, security.ConfigMicrosoft)
	goth.ClearProviders()
	t.Cleanup(goth.ClearProviders)
	goth.UseProviders(microsoftonline.New("id", "secret", "http://localhost/auth/microsoftonline/callback"))
	prevStore := gothic.Store
	gothic.Store = sessions.NewCookieStore([]byte("test-session-secret-long-enough-32"))
	t.Cleanup(func() { gothic.Store = prevStore })

	rec := beginOAuth(t, s, "MicrosoftOnline")

	assert.Equal(t, http.StatusTemporaryRedirect, rec.Code)
	assert.Contains(t, rec.Header().Get("Location"), "login.microsoftonline.com")
}

// callbackOAuth runs handleOAuthCallback for the given provider path parameter.
func callbackOAuth(t *testing.T, s *Server, provider string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/auth/"+provider+"/callback", http.NoBody)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetParamNames("provider")
	c.SetParamValues(provider)
	require.NoError(t, s.handleOAuthCallback(c))
	return rec
}

// stubCompleteUserAuth replaces gothic.CompleteUserAuth for the test. It records
// the provider name gothic would resolve for each request.
func stubCompleteUserAuth(t *testing.T, user *goth.User) *[]string {
	t.Helper()
	var seen []string
	prev := gothic.CompleteUserAuth
	gothic.CompleteUserAuth = func(_ http.ResponseWriter, req *http.Request) (goth.User, error) {
		name, err := gothic.GetProviderName(req)
		if err != nil {
			return goth.User{}, err
		}
		seen = append(seen, name)
		return *user, nil
	}
	t.Cleanup(func() { gothic.CompleteUserAuth = prev })
	return &seen
}

// TestHandleOAuthCallback_MixedCaseProvider pins that a callback path whose casing
// differs from the goth name reaches gothic and the session under the canonical
// goth name, which the session readers compare exactly.
// Not parallel: it swaps the settings, gothic.Store and gothic.CompleteUserAuth.
func TestHandleOAuthCallback_MixedCaseProvider(t *testing.T) {
	s := useOAuthSettings(t, security.ConfigMicrosoft)
	goth.ClearProviders()
	t.Cleanup(goth.ClearProviders)
	goth.UseProviders(microsoftonline.New("id", "secret", "http://localhost/auth/microsoftonline/callback"))
	// Production keeps sessions in a filesystem store: the cookie carries a session
	// ID, so the writes of one callback accumulate in the session the begin step
	// created. Prime such a session and send its cookie with the callback.
	prevStore := gothic.Store
	gothic.Store = sessions.NewFilesystemStore(t.TempDir(), []byte("test-session-secret-long-enough-32"))
	t.Cleanup(func() { gothic.Store = prevStore })
	primeRec := httptest.NewRecorder()
	require.NoError(t, gothic.StoreInSession("state", "x", httptest.NewRequest(http.MethodGet, "/", http.NoBody), primeRec))
	seen := stubCompleteUserAuth(t, &goth.User{UserID: "ms-1", Email: "user@example.com"})

	rec := callbackOAuth(t, s, "MicrosoftOnline", primeRec.Result().Cookies()...)

	assert.Equal(t, []string{security.ProviderMicrosoft}, *seen)
	assert.Equal(t, http.StatusFound, rec.Code)
	assert.Contains(t, rec.Header().Get("Location"), "/ui/dashboard")

	// Apply every Set-Cookie of the response the way a browser would (an HTTPS
	// URL keeps Secure cookies; a later cookie replaces an earlier one of the same
	// name) and read the effective session back from the resulting request.
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	callbackURL := &url.URL{Scheme: "https", Host: "birds.example.com", Path: "/"}
	jar.SetCookies(callbackURL, rec.Result().Cookies())
	readBack := httptest.NewRequest(http.MethodGet, callbackURL.String(), http.NoBody)
	for _, cookie := range jar.Cookies(callbackURL) {
		readBack.AddCookie(cookie)
	}
	record, ok := s.oauth2Server.Session(readBack)
	require.True(t, ok, "the callback must start a session")
	assert.Equal(t, security.ProviderMicrosoft, record.Provider)
}

// TestHandleOAuthRememberChoice pins the remember choice across the begin and
// callback steps: ?remember=1 sets the choice cookie, its absence clears a stale
// one, and the callback turns the choice into a persistent or a browser-session
// cookie and clears the choice cookie.
// Not parallel: it swaps the settings, the goth registry, gothic.Store and gothic.CompleteUserAuth.
func TestHandleOAuthRememberChoice(t *testing.T) {
	const choiceCookie = "birdnet_remember_browser"
	s := useOAuthSettings(t, security.ConfigMicrosoft)
	goth.ClearProviders()
	t.Cleanup(goth.ClearProviders)
	goth.UseProviders(microsoftonline.New("id", "secret", "http://localhost/auth/microsoftonline/callback"))
	prevStore := gothic.Store
	gothic.Store = sessions.NewFilesystemStore(t.TempDir(), []byte("test-session-secret-long-enough-32"))
	t.Cleanup(func() { gothic.Store = prevStore })

	cookieNamed := func(rec *httptest.ResponseRecorder, name string) *http.Cookie {
		for _, cookie := range rec.Result().Cookies() {
			if cookie.Name == name {
				return cookie
			}
		}
		return nil
	}
	begin := func(query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/auth/"+security.ProviderMicrosoft+query, http.NoBody)
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		c.SetParamNames("provider")
		c.SetParamValues(security.ProviderMicrosoft)
		require.NoError(t, s.handleOAuthBegin(c))
		return rec
	}

	t.Run("begin sets the choice cookie only when ticked", func(t *testing.T) {
		ticked := cookieNamed(begin("?remember=1"), choiceCookie)
		require.NotNil(t, ticked)
		assert.Positive(t, ticked.MaxAge)

		unticked := cookieNamed(begin(""), choiceCookie)
		require.NotNil(t, unticked)
		assert.Negative(t, unticked.MaxAge, "a stale choice must be cleared")
	})

	for _, tt := range []struct {
		name       string
		choice     *http.Cookie
		wantMaxAge int
	}{
		{"ticked gives a persistent cookie", &http.Cookie{Name: choiceCookie, Value: security.RememberQueryValue}, int(security.RememberedSessionDuration.Seconds())},
		{"unticked gives a browser-session cookie", nil, 0},
	} {
		t.Run("callback "+tt.name, func(t *testing.T) {
			stubCompleteUserAuth(t, &goth.User{UserID: "ms-1", Email: "user@example.com"})
			var cookies []*http.Cookie
			if tt.choice != nil {
				cookies = append(cookies, tt.choice)
			}
			rec := callbackOAuth(t, s, security.ProviderMicrosoft, cookies...)
			require.Equal(t, http.StatusFound, rec.Code)

			session := cookieNamed(rec, security.SessionCookieName)
			require.NotNil(t, session)
			assert.Equal(t, tt.wantMaxAge, session.MaxAge)
			if tt.choice != nil {
				cleared := cookieNamed(rec, choiceCookie)
				require.NotNil(t, cleared)
				assert.Negative(t, cleared.MaxAge)
			}
		})
	}
}

// TestHandleOAuthCallback_UnknownProvider pins that an unknown provider in the
// callback path is rejected before gothic runs.
// Not parallel: it swaps gothic.CompleteUserAuth.
func TestHandleOAuthCallback_UnknownProvider(t *testing.T) {
	s := useOAuthSettings(t, security.ConfigMicrosoft)
	seen := stubCompleteUserAuth(t, &goth.User{})

	rec := callbackOAuth(t, s, "facebook")

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, *seen, "gothic must not be called for an unknown provider")
}

// namedStubProvider is a goth provider that reports a chosen name and counts
// FetchUser calls, so a test can tell which provider gothic authenticated with.
type namedStubProvider struct {
	*faux.Provider
	name   string
	user   goth.User
	fetchs int
}

func (p *namedStubProvider) Name() string { return p.name }

func (p *namedStubProvider) FetchUser(_ goth.Session) (goth.User, error) {
	p.fetchs++
	return p.user, nil
}

// TestHandleOAuthCallback_QueryProviderCannotOverridePath pins that the provider
// in the callback path is the only one gothic authenticates with: a request
// supplied "provider" query parameter must not select another registered one.
// Not parallel: it swaps the settings, the goth registry and gothic.Store.
func TestHandleOAuthCallback_QueryProviderCannotOverridePath(t *testing.T) {
	const victimEmail = "user@example.com"
	settings := &conf.Settings{
		Security: conf.Security{
			OAuthProviders: []conf.OAuthProviderConfig{
				{Provider: security.ConfigGoogle, Enabled: true, ClientID: "id", ClientSecret: "secret", UserID: victimEmail},
				{Provider: security.ConfigOIDC, Enabled: true, ClientID: "id", ClientSecret: "secret", UserID: victimEmail},
			},
		},
	}
	prev := conf.GetSettings()
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(prev) })
	s := &Server{settings: settings, oauth2Server: security.NewOAuth2ServerForTest(settings)}

	googleStub := &namedStubProvider{Provider: &faux.Provider{}, name: security.ProviderGoogle, user: goth.User{UserID: "google-1", Email: victimEmail}}
	oidcStub := &namedStubProvider{Provider: &faux.Provider{}, name: security.ProviderOIDC, user: goth.User{UserID: "oidc-1", Email: victimEmail}}
	goth.ClearProviders()
	t.Cleanup(goth.ClearProviders)
	goth.UseProviders(googleStub, oidcStub)
	prevStore := gothic.Store
	gothic.Store = sessions.NewFilesystemStore(t.TempDir(), []byte("test-session-secret-long-enough-32"))
	t.Cleanup(func() { gothic.Store = prevStore })

	// begin starts the flow at /auth/google and returns the state and cookies.
	begin := func() (state string, cookies []*http.Cookie) {
		rec := beginOAuth(t, s, security.ProviderGoogle)
		require.Equal(t, http.StatusTemporaryRedirect, rec.Code)
		location, err := url.Parse(rec.Header().Get("Location"))
		require.NoError(t, err)
		return location.Query().Get("state"), rec.Result().Cookies()
	}
	// callback runs handleOAuthCallback for pathProvider with the given query.
	callback := func(pathProvider, query string, cookies []*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/auth/"+pathProvider+"/callback?"+query, http.NoBody)
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		c.SetParamNames("provider")
		c.SetParamValues(pathProvider)
		require.NoError(t, s.handleOAuthCallback(c))
		return rec
	}
	// authProvider reads the active provider back from the session the callback left.
	authProvider := func(rec *httptest.ResponseRecorder, cookies []*http.Cookie) (string, error) {
		readBack := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		for _, cookie := range cookies {
			readBack.AddCookie(cookie)
		}
		for _, cookie := range rec.Result().Cookies() {
			readBack.AddCookie(cookie)
		}
		record, ok := s.oauth2Server.Session(readBack)
		if !ok {
			return "", errors.NewStd("no session")
		}
		return record.Provider, nil
	}

	state, cookies := begin()
	rec := callback(security.ProviderOIDC, "provider="+security.ProviderGoogle+"&state="+url.QueryEscape(state), cookies)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Zero(t, googleStub.fetchs, "the query parameter must not select the Google provider")
	_, err := authProvider(rec, cookies)
	require.Error(t, err, "no session may be established")

	// Control: the same flow finished under its own path name succeeds, whatever
	// the query says.
	state, cookies = begin()
	rec = callback(security.ProviderGoogle, "provider="+security.ProviderOIDC+"&state="+url.QueryEscape(state), cookies)

	assert.Equal(t, http.StatusFound, rec.Code)
	assert.Zero(t, oidcStub.fetchs)
	got, err := authProvider(rec, cookies)
	require.NoError(t, err)
	assert.Equal(t, security.ProviderGoogle, got)
}

// TestHandleOAuthCallback_ProviderChecks pins that the callback applies the
// enabled and registered checks the begin step applies, before gothic runs.
// Not parallel: it swaps the settings, the goth registry and gothic.CompleteUserAuth.
func TestHandleOAuthCallback_ProviderChecks(t *testing.T) {
	tests := []struct {
		name       string
		enabled    bool
		wantStatus int
		wantBody   string
	}{
		{name: "disabled provider", enabled: false, wantStatus: http.StatusBadRequest, wantBody: "OAuth provider not available"},
		{name: "enabled but unregistered provider", enabled: true, wantStatus: http.StatusServiceUnavailable, wantBody: oauthProviderUnavailableMessage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &conf.Settings{
				Security: conf.Security{
					OAuthProviders: []conf.OAuthProviderConfig{
						{Provider: security.ConfigMicrosoft, Enabled: tt.enabled, ClientID: "id", ClientSecret: "secret", UserID: "user@example.com"},
					},
				},
			}
			prev := conf.GetSettings()
			conftest.SetTestSettings(settings)
			t.Cleanup(func() { conftest.SetTestSettings(prev) })
			goth.ClearProviders()
			t.Cleanup(goth.ClearProviders)
			seen := stubCompleteUserAuth(t, &goth.User{UserID: "ms-1", Email: "user@example.com"})

			rec := callbackOAuth(t, &Server{settings: settings}, security.ProviderMicrosoft)

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantBody, rec.Body.String())
			assert.Empty(t, *seen, "gothic must not be called")
		})
	}
}
