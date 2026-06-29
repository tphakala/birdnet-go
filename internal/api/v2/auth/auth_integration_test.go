// auth_integration_test.go: Integration tests for V2 authentication flow.
// Tests the complete login flow using the /api/v2/auth handlers. After the
// auth domain split these build the auth Handler directly from an apitest core
// plus a real auth service (SecurityAdapter), rather than the full facade
// Controller, and invoke the handlers directly.
package authapi

import (
	"encoding/json"
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

	// Initialize gothic session store for testing (required for session operations)
	gothic.Store = sessions.NewCookieStore([]byte(settings.Security.SessionSecret))

	// Build the shared core (the auth handlers reach the logging helpers through
	// it) and construct the auth Handler exactly as the facade does.
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithSettings(settings))
	h := New(core, authService)

	return e, h, settings
}

// createTestOAuth2Server creates an OAuth2Server with the provided settings for testing.
func createTestOAuth2Server(tb testing.TB, settings *conf.Settings) *security.OAuth2Server {
	tb.Helper()
	return securitytest.NewOAuth2ServerForTesting(tb, settings)
}

// TestV2AuthFlow_CompleteLogin tests the complete V2 login flow end-to-end.
func TestV2AuthFlow_CompleteLogin(t *testing.T) {
	e, h, settings := setupAuthIntegrationTest(t)

	t.Run("complete login flow with V2 callback", func(t *testing.T) {
		// Step 1: POST /api/v2/auth/login with valid credentials
		loginPayload := `{
			"username": "birdnet-client",
			"password": "testpassword123",
			"redirectUrl": "/ui/dashboard"
		}`

		req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", strings.NewReader(loginPayload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		// Execute login handler
		err := h.Login(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		// Parse login response
		var loginResp AuthResponse
		err = json.Unmarshal(rec.Body.Bytes(), &loginResp)
		require.NoError(t, err)

		assert.True(t, loginResp.Success)
		assert.NotEmpty(t, loginResp.RedirectURL)
		assert.Contains(t, loginResp.RedirectURL, "/api/v2/auth/callback")
		assert.Contains(t, loginResp.RedirectURL, "code=")

		t.Logf("Login successful, redirect URL: %s", loginResp.RedirectURL)

		// Step 2: Follow callback URL
		// Extract code from redirect URL
		callbackURL := loginResp.RedirectURL
		assert.True(t, strings.HasPrefix(callbackURL, "/api/v2/auth/callback"))

		// Make callback request
		callbackReq := httptest.NewRequest(http.MethodGet, callbackURL, http.NoBody)
		callbackRec := httptest.NewRecorder()
		callbackCtx := e.NewContext(callbackReq, callbackRec)

		// Execute callback handler
		err = h.OAuthCallback(callbackCtx)
		require.NoError(t, err)

		// Should redirect (302) to final destination
		assert.Equal(t, http.StatusFound, callbackRec.Code)
		location := callbackRec.Header().Get("Location")
		assert.NotEmpty(t, location)
		t.Logf("Callback redirected to: %s", location)

		// Verify session cookie was set
		cookies := callbackRec.Result().Cookies()
		t.Logf("Cookies set: %d", len(cookies))
		for _, cookie := range cookies {
			t.Logf("Cookie: %s = %s", cookie.Name, cookie.Value[:min(len(cookie.Value), 20)]+"...")
		}
	})

	_ = settings // Use settings if needed for additional assertions
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
	gothic.Store = sessions.NewCookieStore([]byte(settings.Security.SessionSecret))

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

// TestV2AuthFlow_RedirectURLInResponse tests that the login response contains V2 callback URL.
func TestV2AuthFlow_RedirectURLInResponse(t *testing.T) {
	e, h, _ := setupAuthIntegrationTest(t)

	t.Run("login response contains V2 callback URL", func(t *testing.T) {
		loginPayload := `{
			"username": "birdnet-client",
			"password": "testpassword123"
		}`

		req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", strings.NewReader(loginPayload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := h.Login(c)
		require.NoError(t, err)

		var resp AuthResponse
		err = json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)

		// Verify redirect URL points to V2 callback, NOT V1
		assert.Contains(t, resp.RedirectURL, "/api/v2/auth/callback",
			"Should use V2 callback endpoint")
		assert.NotContains(t, resp.RedirectURL, "/api/v1/",
			"Should NOT use V1 callback endpoint")
	})
}

// TestV2AuthService_Interface tests that SecurityAdapter implements the Service interface correctly.
func TestV2AuthService_Interface(t *testing.T) {
	t.Run("SecurityAdapter implements all Service methods", func(t *testing.T) {
		settings := &conf.Settings{
			Security: conf.Security{
				SessionSecret: "test-secret-32-chars-long",
				BasicAuth: conf.BasicAuth{
					Enabled:        true,
					Password:       "test",
					ClientID:       "test-client",
					AuthCodeExp:    5 * time.Minute,
					AccessTokenExp: 24 * time.Hour,
				},
			},
		}

		oauth2Server := createTestOAuth2Server(t, settings)
		adapter := auth.NewSecurityAdapter(oauth2Server)

		// Verify adapter implements Service interface
		var _ auth.Service = adapter

		t.Log("SecurityAdapter correctly implements auth.Service interface including new methods")
	})
}
