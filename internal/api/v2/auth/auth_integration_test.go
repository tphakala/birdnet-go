// auth_integration_test.go: Integration tests for V2 authentication flow.
// Tests the complete login flow using the /api/v2/auth handlers. After the
// auth domain split these build the auth Handler directly from an apitest core
// plus a real auth service (SecurityAdapter), rather than the full facade
// Controller, and invoke the handlers directly.
package authapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth/gothic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/auth"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/security"
	"github.com/tphakala/birdnet-go/internal/security/securitytest"
)

// setupAuthIntegrationTest creates a test environment with a real OAuth2Server
// for integration tests. It builds the auth Handler around an apitest core and
// a SecurityAdapter auth service, mirroring the facade wiring
// (authapi.New(core, authService)) without constructing the full Controller.
func setupAuthIntegrationTest(t *testing.T) (*echo.Echo, *Handler, *conf.Settings) {
	t.Helper()

	// Create Echo instance
	e := echo.New()

	// Create settings with BasicAuth enabled
	settings := &conf.Settings{
		WebServer: conf.WebServerSettings{
			Debug: true,
		},
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				Export: conf.ExportSettings{
					Path: t.TempDir(),
				},
			},
		},
		Security: conf.Security{
			SessionSecret: "test-session-secret-32-chars-long",
			BasicAuth: conf.BasicAuth{
				Enabled:        true,
				Password:       "testpassword123",
				ClientID:       "birdnet-client",
				AuthCodeExp:    5 * time.Minute,
				AccessTokenExp: 24 * time.Hour,
			},
		},
	}

	// Create OAuth2Server with test settings, then the auth service the handler uses.
	oauth2Server := createTestOAuth2Server(t, settings)
	authService := auth.NewSecurityAdapter(oauth2Server)

	// Initialize gothic session store for testing (required for session
	// operations). It is process-global, so restore it after the test.
	setTestGothicStore(t, settings.Security.SessionSecret)

	// Build the shared core (the auth handlers reach the logging helpers through
	// it) and construct the auth Handler exactly as the facade does.
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithSettings(settings))
	h := New(core, authService)

	return e, h, settings
}

// setTestGothicStore installs a cookie session store as the process-global
// gothic.Store and restores the previous store on cleanup.
func setTestGothicStore(t *testing.T, secret string) {
	t.Helper()
	prev := gothic.Store
	t.Cleanup(func() { gothic.Store = prev })
	gothic.Store = sessions.NewCookieStore([]byte(secret))
}

// createTestOAuth2Server creates an OAuth2Server with the provided settings for testing.
func createTestOAuth2Server(tb testing.TB, settings *conf.Settings) *security.OAuth2Server {
	tb.Helper()
	return securitytest.NewOAuth2ServerForTesting(tb, settings)
}

// signInThroughHandlers runs login and the callback for payload and returns the
// session cookie the callback sets, or nil when it sets none.
func signInThroughHandlers(t *testing.T, e *echo.Echo, h *Handler, payload string, prev *http.Cookie) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	require.NoError(t, h.Login(e.NewContext(req, rec)))
	require.Equal(t, http.StatusOK, rec.Code)

	var loginResp AuthResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &loginResp))
	require.True(t, loginResp.Success)
	assert.Contains(t, loginResp.RedirectURL, "/api/v2/auth/callback")
	assert.Contains(t, loginResp.RedirectURL, "code=")
	assert.NotContains(t, loginResp.RedirectURL, "remember", "the choice must ride in the server-side code, not the URL")

	callbackReq := httptest.NewRequest(http.MethodGet, loginResp.RedirectURL, http.NoBody)
	if prev != nil {
		callbackReq.AddCookie(prev)
	}
	callbackRec := httptest.NewRecorder()
	require.NoError(t, h.OAuthCallback(e.NewContext(callbackReq, callbackRec)))
	require.Equal(t, http.StatusFound, callbackRec.Code)
	assert.NotEmpty(t, callbackRec.Header().Get("Location"))

	for _, cookie := range callbackRec.Result().Cookies() {
		if cookie.Name == security.SessionCookieName {
			return cookie
		}
	}
	return nil
}

// isSignedIn reports whether a request carrying cookie passes the auth check.
func isSignedIn(e *echo.Echo, h *Handler, cookie *http.Cookie) bool {
	req := httptest.NewRequest(http.MethodGet, "/api/v2/anything", http.NoBody)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	return h.authService.CheckAccess(e.NewContext(req, httptest.NewRecorder())) == nil
}

// TestV2AuthFlow_CompleteLogin tests the complete V2 login flow end-to-end,
// including the "Remember this browser" choice, logout and replay of the cookie.
func TestV2AuthFlow_CompleteLogin(t *testing.T) {
	const loginFmt = `{"username": "birdnet-client", "password": "testpassword123", "redirectUrl": "/ui/dashboard"%s}`
	rememberedMaxAge := int(security.RememberedCookieMaxAge.Seconds())

	tests := []struct {
		name       string
		extra      string
		wantMaxAge int
	}{
		{"rememberBrowser true", `, "rememberBrowser": true`, rememberedMaxAge},
		{"rememberBrowser false", `, "rememberBrowser": false`, 0},
		{"rememberBrowser omitted", ``, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, h, _ := setupAuthIntegrationTest(t)
			payload := fmt.Sprintf(loginFmt, tt.extra)

			session := signInThroughHandlers(t, e, h, payload, nil)
			require.NotNil(t, session, "the callback must set the session cookie")
			assert.Equal(t, tt.wantMaxAge, session.MaxAge, "persistence of the session cookie follows the choice")
			assert.NotEmpty(t, session.Value)
			assert.True(t, isSignedIn(e, h, session), "the cookie must authenticate")
			require.NoError(t, h.authService.ValidateToken(session.Value), "the session token is also accepted as a bearer token")

			// Signing in again on the same browser yields a different token and
			// revokes the first (session fixation regression).
			second := signInThroughHandlers(t, e, h, payload, session)
			require.NotNil(t, second)
			assert.NotEqual(t, session.Value, second.Value)
			assert.False(t, isSignedIn(e, h, session), "the previous session must be revoked")
			assert.True(t, isSignedIn(e, h, second))

			// Logout ends the session; a replay of the same cookie is not signed in.
			logoutReq := httptest.NewRequest(http.MethodPost, "/api/v2/auth/logout", http.NoBody)
			logoutReq.AddCookie(second)
			logoutRec := httptest.NewRecorder()
			require.NoError(t, h.Logout(e.NewContext(logoutReq, logoutRec)))
			assert.False(t, isSignedIn(e, h, second), "a replayed cookie must not be signed in after logout")
		})
	}
}

// TestV2AuthFlow_FilterQueryRedirectSurvives verifies that a post-login redirect
// to a filtered view whose query string legitimately contains path-like
// sequences (".." / "//") survives validation instead of being silently dropped
// back to the base path. Regression guard for the query-aware redirect fix:
// path-traversal heuristics must only apply to the path, not to the query.
func TestV2AuthFlow_FilterQueryRedirectSurvives(t *testing.T) {
	e, h, _ := setupAuthIntegrationTest(t)

	const filteredRedirect = "/detections?queryType=species&q=a..b//c"
	const wantFinalRedirect = "/ui/detections?queryType=species&q=a..b//c"

	loginPayload := `{
		"username": "birdnet-client",
		"password": "testpassword123",
		"redirectUrl": "` + filteredRedirect + `",
		"basePath": "/ui/"
	}`

	req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", strings.NewReader(loginPayload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	require.NoError(t, h.Login(c))
	require.Equal(t, http.StatusOK, rec.Code)

	var loginResp AuthResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &loginResp))
	require.True(t, loginResp.Success)

	// The callback URL must carry the full filtered target, not the bare base path.
	callbackURL, err := url.Parse(loginResp.RedirectURL)
	require.NoError(t, err)
	gotRedirect := callbackURL.Query().Get("redirect")
	assert.Equal(t, wantFinalRedirect, gotRedirect,
		"filtered redirect with '..'/'//' in the query must survive login validation")

	// Following the callback must 302 to the filtered view with the query intact.
	callbackReq := httptest.NewRequest(http.MethodGet, loginResp.RedirectURL, http.NoBody)
	callbackRec := httptest.NewRecorder()
	require.NoError(t, h.OAuthCallback(e.NewContext(callbackReq, callbackRec)))

	assert.Equal(t, http.StatusFound, callbackRec.Code)
	assert.Equal(t, wantFinalRedirect, callbackRec.Header().Get("Location"),
		"callback must redirect to the filtered view, not the base path")
}

// TestV2AuthFlow_ProxyPrefixedBasePathRedirect verifies that a reverse-proxy
// (Home Assistant Ingress) base path produced by the frontend's getUiBasePath()
// is accepted end-to-end: the redirect is re-rooted under the proxy prefix
// rather than dropped to the default base path. Regression guard for the
// proxy-aware base-path handling.
func TestV2AuthFlow_ProxyPrefixedBasePathRedirect(t *testing.T) {
	e, h, _ := setupAuthIntegrationTest(t)

	// HA Ingress tokens are base64url (alphanumeric, '-', '_'), accepted by the
	// backend basePath regex.
	const basePath = "/api/hassio_ingress/aBcD-_1234567890/ui/"
	const wantFinalRedirect = "/api/hassio_ingress/aBcD-_1234567890/ui/detections?queryType=species"

	loginPayload := `{
		"username": "birdnet-client",
		"password": "testpassword123",
		"redirectUrl": "/detections?queryType=species",
		"basePath": "` + basePath + `"
	}`

	req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", strings.NewReader(loginPayload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	require.NoError(t, h.Login(e.NewContext(req, rec)))
	require.Equal(t, http.StatusOK, rec.Code)

	var loginResp AuthResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &loginResp))
	require.True(t, loginResp.Success)

	callbackURL, err := url.Parse(loginResp.RedirectURL)
	require.NoError(t, err)
	assert.Equal(t, wantFinalRedirect, callbackURL.Query().Get("redirect"),
		"redirect must be re-rooted under the reverse-proxy base path")
}

// TestV2AuthFlow_InvalidCredentials tests login with wrong password.
func TestV2AuthFlow_InvalidCredentials(t *testing.T) {
	e, h, _ := setupAuthIntegrationTest(t)

	t.Run("login fails with wrong password", func(t *testing.T) {
		loginPayload := `{
			"username": "birdnet-client",
			"password": "wrongpassword"
		}`

		req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", strings.NewReader(loginPayload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := h.Login(c)
		require.NoError(t, err) // Handler should not return error, but set appropriate response

		assert.Equal(t, http.StatusUnauthorized, rec.Code)

		var resp AuthResponse
		err = json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)

		assert.False(t, resp.Success)
		assert.Empty(t, resp.RedirectURL)
	})
}

// TestV2AuthFlow_EmptyClientID_V1Compatible tests backward compatibility when ClientID is empty.
func TestV2AuthFlow_EmptyClientID_V1Compatible(t *testing.T) {
	// Create custom test environment with empty ClientID
	e := echo.New()

	settings := &conf.Settings{
		WebServer: conf.WebServerSettings{Debug: true},
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				Export: conf.ExportSettings{Path: t.TempDir()},
			},
		},
		Security: conf.Security{
			SessionSecret: "test-session-secret-32-chars-long",
			BasicAuth: conf.BasicAuth{
				Enabled:        true,
				Password:       "testpassword123",
				ClientID:       "", // Empty - V1 compatible mode
				AuthCodeExp:    5 * time.Minute,
				AccessTokenExp: 24 * time.Hour,
			},
		},
	}

	oauth2Server := createTestOAuth2Server(t, settings)
	authService := auth.NewSecurityAdapter(oauth2Server)
	setTestGothicStore(t, settings.Security.SessionSecret)

	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithSettings(settings))
	h := New(core, authService)

	t.Run("empty ClientID allows any username (V1 compatible)", func(t *testing.T) {
		// Any username should work when ClientID is empty
		loginPayload := `{
			"username": "any-random-username",
			"password": "testpassword123"
		}`

		req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", strings.NewReader(loginPayload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := h.Login(c)
		require.NoError(t, err)

		assert.Equal(t, http.StatusOK, rec.Code)

		var resp AuthResponse
		err = json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)

		assert.True(t, resp.Success)
		assert.NotEmpty(t, resp.RedirectURL)
		t.Log("V1 compatible mode: login succeeded with any username when ClientID is empty")
	})
}

// TestV2AuthFlow_CallbackErrors tests callback error scenarios.
func TestV2AuthFlow_CallbackErrors(t *testing.T) {
	e, h, _ := setupAuthIntegrationTest(t)

	testCases := []struct {
		name           string
		url            string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "invalid code",
			url:            "/api/v2/auth/callback?code=invalid-code&redirect=/ui/",
			expectedStatus: http.StatusUnauthorized,
			expectedBody:   "Unable to complete login",
		},
		{
			name:           "missing code",
			url:            "/api/v2/auth/callback?redirect=/ui/",
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "Missing authorization code",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.url, http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := h.OAuthCallback(c)
			require.NoError(t, err)

			assert.Equal(t, tc.expectedStatus, rec.Code)
			assert.Contains(t, rec.Body.String(), tc.expectedBody)
		})
	}
}

// TestV2AuthFlow_OpenRedirectPrevention tests that malicious redirects are blocked.
func TestV2AuthFlow_OpenRedirectPrevention(t *testing.T) {
	testCases := []struct {
		name           string
		maliciousURL   string
		expectedResult string // Expected sanitized redirect
	}{
		{
			name:           "protocol-relative URL",
			maliciousURL:   "//evil.com/steal-cookies",
			expectedResult: "/",
		},
		{
			name:           "absolute URL with scheme",
			maliciousURL:   "https://evil.com/phishing",
			expectedResult: "/",
		},
		{
			name:           "backslash protocol-relative",
			maliciousURL:   "/\\evil.com",
			expectedResult: "/",
		},
		{
			name:           "javascript URL",
			maliciousURL:   "javascript:alert('xss')",
			expectedResult: "/",
		},
		{
			name:           "data URL",
			maliciousURL:   "data:text/html,<script>alert('xss')</script>",
			expectedResult: "/",
		},
		{
			name:           "triple slash",
			maliciousURL:   "///evil.com",
			expectedResult: "/",
		},
		{
			name:           "mixed slash and backslash protocol-relative",
			maliciousURL:   `/\/evil.com`,
			expectedResult: "/",
		},
		{
			name:           "vbscript URL",
			maliciousURL:   "vbscript:msgbox(1)",
			expectedResult: "/",
		},
		{
			name:           "file URL",
			maliciousURL:   "file:///etc/passwd",
			expectedResult: "/",
		},
		{
			name:           "protocol-relative URL with userinfo",
			maliciousURL:   "//@evil.com",
			expectedResult: "/",
		},
		{
			name:           "tab before the scheme",
			maliciousURL:   "\thttp://evil.com",
			expectedResult: "/",
		},
		{
			name:           "newline before the scheme",
			maliciousURL:   "\nhttp://evil.com",
			expectedResult: "/",
		},
		{
			name:           "root path",
			maliciousURL:   "/",
			expectedResult: "/",
		},
		{
			name:           "valid relative path",
			maliciousURL:   "/ui/dashboard",
			expectedResult: "/ui/dashboard",
		},
		{
			name:           "valid path with query",
			maliciousURL:   "/ui/settings?tab=main",
			expectedResult: "/ui/settings?tab=main",
		},
		{
			name:           "path traversal in the path component is rejected",
			maliciousURL:   "/ui/../../etc/passwd?x=1",
			expectedResult: "/",
		},
		{
			name:           "encoded path traversal in the path component is rejected",
			maliciousURL:   "/ui/%2e%2e/secret",
			expectedResult: "/",
		},
		{
			name:           "path-like sequences in the query are preserved",
			maliciousURL:   "/ui/detections?queryType=search&q=a..b//c",
			expectedResult: "/ui/detections?queryType=search&q=a..b//c",
		},
		{
			name:           "special characters in the path are emitted percent-encoded",
			maliciousURL:   "/ui/a%20b",
			expectedResult: "/ui/a%20b",
		},
		{
			name:           "double-encoded CRLF in the query is rejected",
			maliciousURL:   "/ui/detections?x=a%250d%250aSet-Cookie:evil",
			expectedResult: "/",
		},
		{
			name:           "redirect over the length budget is rejected",
			maliciousURL:   "/ui/detections?q=" + strings.Repeat("a", 3000),
			expectedResult: "/",
		},
		{
			name:           "backslashes in the query value are preserved (not mangled to slashes)",
			maliciousURL:   `/ui/search?q=C:\Temp\bird.wav`,
			expectedResult: `/ui/search?q=C:\Temp\bird.wav`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := ValidateAndSanitizeRedirect(tc.maliciousURL)
			assert.Equal(t, tc.expectedResult, result,
				"Redirect sanitization failed for: %s", tc.maliciousURL)
		})
	}
}

// TestV2AuthFlow_MissingCredentials tests login with missing username or password.
func TestV2AuthFlow_MissingCredentials(t *testing.T) {
	e, h, _ := setupAuthIntegrationTest(t)

	testCases := []struct {
		name    string
		payload string
	}{
		{
			name:    "missing username",
			payload: `{"password": "testpassword123"}`,
		},
		{
			name:    "missing password",
			payload: `{"username": "birdnet-client"}`,
		},
		{
			name:    "empty credentials",
			payload: `{"username": "", "password": ""}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", strings.NewReader(tc.payload))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := h.Login(c)
			require.NoError(t, err)

			assert.Equal(t, http.StatusBadRequest, rec.Code)

			var resp AuthResponse
			err = json.Unmarshal(rec.Body.Bytes(), &resp)
			require.NoError(t, err)

			assert.False(t, resp.Success)
			assert.Contains(t, resp.Message, "required")
		})
	}
}

// TestV2AuthFlow_WrongUsername tests login with wrong username when ClientID is set.
func TestV2AuthFlow_WrongUsername(t *testing.T) {
	e, h, _ := setupAuthIntegrationTest(t)

	t.Run("login fails with wrong username when ClientID is set", func(t *testing.T) {
		loginPayload := `{
			"username": "wrong-username",
			"password": "testpassword123"
		}`

		req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", strings.NewReader(loginPayload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := h.Login(c)
		require.NoError(t, err)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)

		var resp AuthResponse
		err = json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)

		assert.False(t, resp.Success)
		t.Log("V2 correctly rejects wrong username when ClientID is configured")
	})
}
