// auth_integration_test.go: Integration tests for V2 authentication flow.
// Tests the complete login flow using /api/v2/auth endpoints.
package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth/gothic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/auth"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/security"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// setupAuthIntegrationTest creates a test environment with real OAuth2Server for integration tests.
func setupAuthIntegrationTest(t *testing.T) (*echo.Echo, *Controller, *conf.Settings) {
	t.Helper()

	// Create Echo instance
	e := echo.New()

	// Create mock datastore
	mockDS := mocks.NewMockInterface(t)

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

	// Create a test logger
	logger := log.New(io.Discard, "API TEST: ", log.LstdFlags)

	// Create mock ImageProvider
	mockImageProvider := &MockImageProvider{}
	mockImageProvider.On("Fetch", mock.Anything).Return(imageprovider.BirdImage{}, nil).Maybe()

	// Create bird image cache with mock provider
	birdImageCache := &imageprovider.BirdImageCache{}
	birdImageCache.SetImageProvider(mockImageProvider)

	// Create sun calculator
	sunCalc := suncalc.NewSunCalc(60.1699, 24.9384)

	// Create control channel
	controlChan := make(chan string, 10)

	// Create mock metrics
	mockMetrics, _ := observability.NewMetrics()

	// Create OAuth2Server with test settings
	oauth2Server := createTestOAuth2Server(settings)

	// Create auth service and middleware for testing
	authService := auth.NewSecurityAdapter(oauth2Server, nil)
	authMw := auth.NewMiddleware(authService, nil)

	// Initialize gothic session store for testing (required for session operations)
	gothic.Store = sessions.NewCookieStore([]byte(settings.Security.SessionSecret))

	// Create API controller with OAuth2Server via functional options
	controller, err := NewWithOptions(e, mockDS, settings, birdImageCache, sunCalc, controlChan, logger, mockMetrics, true,
		WithAuthMiddleware(authMw.Authenticate), WithAuthService(authService))
	require.NoError(t, err, "Failed to create test API controller")

	// Register cleanup
	t.Cleanup(func() {
		controller.Shutdown()
		close(controlChan)
	})

	return e, controller, settings
}

// createTestOAuth2Server creates an OAuth2Server with the provided settings for testing.
func createTestOAuth2Server(settings *conf.Settings) *security.OAuth2Server {
	// Create OAuth2Server manually for testing (bypasses conf.GetSettings())
	// Must call NewOAuth2ServerForTesting to properly initialize internal maps
	return security.NewOAuth2ServerForTesting(settings)
}

// TestV2AuthFlow_CompleteLogin tests the complete V2 login flow end-to-end.
func TestV2AuthFlow_CompleteLogin(t *testing.T) {
	e, controller, settings := setupAuthIntegrationTest(t)

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
		err := controller.Login(c)
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
		err = controller.OAuthCallback(callbackCtx)
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

// TestV2AuthFlow_InvalidCredentials tests login with wrong password.
func TestV2AuthFlow_InvalidCredentials(t *testing.T) {
	e, controller, _ := setupAuthIntegrationTest(t)

	t.Run("login fails with wrong password", func(t *testing.T) {
		loginPayload := `{
			"username": "birdnet-client",
			"password": "wrongpassword"
		}`

		req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", strings.NewReader(loginPayload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := controller.Login(c)
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
	mockDS := mocks.NewMockInterface(t)

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

	logger := log.New(io.Discard, "", 0)
	mockImageProvider := &MockImageProvider{}
	mockImageProvider.On("Fetch", mock.Anything).Return(imageprovider.BirdImage{}, nil).Maybe()
	birdImageCache := &imageprovider.BirdImageCache{}
	birdImageCache.SetImageProvider(mockImageProvider)
	sunCalc := suncalc.NewSunCalc(60.1699, 24.9384)
	controlChan := make(chan string, 10)
	mockMetrics, _ := observability.NewMetrics()
	oauth2Server := createTestOAuth2Server(settings)

	// Create auth service and middleware for testing
	authService := auth.NewSecurityAdapter(oauth2Server, nil)
	authMw := auth.NewMiddleware(authService, nil)

	controller, err := NewWithOptions(e, mockDS, settings, birdImageCache, sunCalc, controlChan, logger, mockMetrics, true,
		WithAuthMiddleware(authMw.Authenticate), WithAuthService(authService))
	require.NoError(t, err)

	t.Cleanup(func() {
		controller.Shutdown()
		close(controlChan)
	})

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

		err := controller.Login(c)
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
	e, controller, _ := setupAuthIntegrationTest(t)

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

			err := controller.OAuthCallback(c)
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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := validateAndSanitizeRedirect(tc.maliciousURL)
			assert.Equal(t, tc.expectedResult, result,
				"Redirect sanitization failed for: %s", tc.maliciousURL)
		})
	}
}

// TestV2AuthFlow_MissingCredentials tests login with missing username or password.
func TestV2AuthFlow_MissingCredentials(t *testing.T) {
	e, controller, _ := setupAuthIntegrationTest(t)

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

			err := controller.Login(c)
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
	e, controller, _ := setupAuthIntegrationTest(t)

	t.Run("login fails with wrong username when ClientID is set", func(t *testing.T) {
		loginPayload := `{
			"username": "wrong-username",
			"password": "testpassword123"
		}`

		req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", strings.NewReader(loginPayload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := controller.Login(c)
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
	e, controller, _ := setupAuthIntegrationTest(t)

	t.Run("login response contains V2 callback URL", func(t *testing.T) {
		loginPayload := `{
			"username": "birdnet-client",
			"password": "testpassword123"
		}`

		req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", strings.NewReader(loginPayload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := controller.Login(c)
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

		oauth2Server := createTestOAuth2Server(settings)
		adapter := auth.NewSecurityAdapter(oauth2Server, nil)

		// Verify adapter implements Service interface
		var _ auth.Service = adapter

		t.Log("SecurityAdapter correctly implements auth.Service interface including new methods")
	})
}

