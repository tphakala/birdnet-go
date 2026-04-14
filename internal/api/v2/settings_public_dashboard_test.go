// settings_public_dashboard_test.go: Tests for the publicly accessible
// GET /api/v2/settings/dashboard endpoint.
//
// Context: the Dashboard section is rendered by the SPA even for
// unauthenticated guests (see Settings > Layout which is already public
// via /api/v2/app/config). Protecting it behind auth caused the Dashboard
// Daily Activity panel and guest species limit to break (#2758, #2734, #2744).
// The dashboard section contains no secrets/tokens/PII, so it is safe to
// expose for read-only access. Mutations (PUT/PATCH) remain auth-protected.

package api

import (
	"bytes"
	"encoding/json"
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

// newSettingsAuthTestEnv creates a controller with fully registered routes,
// BasicAuth enabled, and a remote-IP rewrite middleware that forces requests
// to look like they come from a non-LAN client (so the auth middleware does
// NOT bypass auth for the test's loopback address).
func newSettingsAuthTestEnv(t *testing.T) *echo.Echo {
	t.Helper()

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

	e := echo.New()

	// Force the client IP to an off-subnet address so IsAuthRequired()
	// returns true regardless of the SubnetBypass configuration.
	e.Pre(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Request().RemoteAddr = "203.0.113.42:54321" // TEST-NET-3 per RFC 5737
			return next(c)
		}
	})

	mockDS := mocks.NewMockInterface(t)

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
			Dashboard: conf.Dashboard{
				SummaryLimit: 42,
				Thumbnails: conf.Thumbnails{
					ImageProvider: "avicommons",
					Summary:       true,
					Recent:        true,
				},
				Locale: "en",
			},
		},
		Security: securityConfig,
		BirdNET: conf.BirdNETConfig{
			Latitude:  60.1699,
			Longitude: 24.9384,
		},
	}

	mockImageProvider := &MockImageProvider{}
	mockImageProvider.On("Fetch", mock.Anything).
		Return(imageprovider.BirdImage{}, nil).Maybe()

	birdImageCache := &imageprovider.BirdImageCache{}
	birdImageCache.SetImageProvider(mockImageProvider)

	sunCalc := suncalc.NewSunCalc(testHelsinkiLatitude, testHelsinkiLongitude)
	controlChan := make(chan string, testControlChannelBuf)
	mockMetrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to create test metrics")

	oauth2Server := security.NewOAuth2ServerForTesting(settings)
	authService := auth.NewSecurityAdapter(oauth2Server)
	authMw := auth.NewMiddleware(authService)

	// Save and restore gothic.Store to avoid leaking state between tests.
	prevGothicStore := gothic.Store
	gothic.Store = sessions.NewCookieStore([]byte(settings.Security.SessionSecret))
	t.Cleanup(func() {
		gothic.Store = prevGothicStore
	})

	controller, err := NewWithOptions(
		e, mockDS, settings, birdImageCache, sunCalc, controlChan, mockMetrics,
		true, // initializeRoutes - register full /api/v2 route tree
		WithAuthMiddleware(authMw.Authenticate),
		WithAuthService(authService),
	)
	require.NoError(t, err, "Failed to create test API controller with auth")

	t.Cleanup(func() {
		controller.Shutdown()
		close(controlChan)
	})

	return e
}

// TestGetDashboardSettings_PublicWithAuthEnabled verifies that GET
// /api/v2/settings/dashboard returns 200 without authentication when
// BasicAuth is enabled. This is the core fix for #2758, #2734, #2744.
func TestGetDashboardSettings_PublicWithAuthEnabled(t *testing.T) {
	e := newSettingsAuthTestEnv(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/settings/dashboard", http.NoBody)
	// No Authorization header, no session cookie -> guest request.
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code,
		"guest must be able to read dashboard settings, got: %s", rec.Body.String())

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	assert.EqualValues(t, 42, payload["summaryLimit"],
		"response must contain dashboard settings from config")
	assert.NotNil(t, payload["thumbnails"], "response must include thumbnails struct")
}

// TestGetDashboardSettings_DoesNotLeakSecrets verifies that the guest-accessible
// dashboard payload contains only non-sensitive Dashboard fields. This guards
// against future struct additions that might leak tokens/credentials.
func TestGetDashboardSettings_DoesNotLeakSecrets(t *testing.T) {
	e := newSettingsAuthTestEnv(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/settings/dashboard", http.NoBody)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	// The redaction marker must not appear in the dashboard response. Its
	// presence would indicate a secret-bearing field slipped into the Dashboard
	// struct and was redacted by sanitizeSettingsForAPI but still serialized.
	assert.NotContains(t, body, redactedValue,
		"dashboard response must not contain the redaction marker %q", redactedValue)

	bodyLower := strings.ToLower(body)
	// None of these substrings should appear in the dashboard payload.
	forbidden := []string{"password", "secret", "token", "apikey", "api_key", "client_secret"}
	for _, s := range forbidden {
		assert.NotContains(t, bodyLower, s,
			"dashboard response must not leak field name %q", s)
	}
}

// TestPatchDashboardSettings_RequiresAuth verifies that mutating the dashboard
// section still requires authentication even though reads are public.
func TestPatchDashboardSettings_RequiresAuth(t *testing.T) {
	e := newSettingsAuthTestEnv(t)

	body := bytes.NewBufferString(`{"summaryLimit": 99}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/dashboard", body)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code,
		"PATCH /settings/dashboard must remain auth-protected")
}

// TestGetBirdnetSettings_StillRequiresAuth is a regression guard ensuring that
// exposing /settings/dashboard publicly did not accidentally expose other
// settings sections. /settings/birdnet remains auth-protected.
func TestGetBirdnetSettings_StillRequiresAuth(t *testing.T) {
	e := newSettingsAuthTestEnv(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/settings/birdnet", http.NoBody)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code,
		"GET /settings/birdnet must still require auth")
}

// TestGetAllSettings_StillRequiresAuth guards that the full /settings endpoint
// (which returns potentially-sensitive data) remains auth-protected.
func TestGetAllSettings_StillRequiresAuth(t *testing.T) {
	e := newSettingsAuthTestEnv(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/settings", http.NoBody)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code,
		"GET /settings must still require auth")
}
