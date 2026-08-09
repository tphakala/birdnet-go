// private_mode_media_authz_test.go: regression tests for GHSA-c7jx-552f-94hh.
//
// The stored-detection-media routes GET /api/v2/audio/:id,
// GET /api/v2/spectrogram/:id, GET /api/v2/spectrogram/:id/status and
// POST /api/v2/spectrogram/:id/generate are registered directly on the root Echo
// instance (media.RegisterRoutes uses c.Echo, not the v2 group), so they do NOT
// inherit the group-level c.Group.Use(c.PrivateModeAuth) gate. They therefore
// carry PrivateModeAuth as per-route middleware. These tests drive real requests
// through the production route tree (NewWithOptions with a real auth middleware)
// to prove that, when Private Mode is enabled, an unauthenticated request is
// rejected before the handler runs, and that public (non-private) installs keep
// serving the same routes without authentication.

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth/gothic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/auth"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/security/securitytest"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// newMediaAuthzTestEnv builds a controller with the full /api/v2 route tree,
// BasicAuth enabled, and Security.PrivateMode set to privateMode. A pre-router
// hook forces the client IP off-subnet so the auth middleware's LAN bypass never
// kicks in and an unauthenticated request is a genuine guest. It mirrors
// newSettingsAuthTestEnv (settings_public_dashboard_test.go) but toggles
// PrivateMode, which is the setting that gates the media-by-ID routes.
func newMediaAuthzTestEnv(t *testing.T, privateMode bool) *echo.Echo {
	t.Helper()

	securityConfig := conf.Security{
		SessionSecret: "test-session-secret-32-chars-long",
		PrivateMode:   privateMode,
		BasicAuth: conf.BasicAuth{
			Enabled:        true,
			Password:       "testpassword",
			ClientID:       "test-client",
			AuthCodeExp:    5 * time.Minute,
			AccessTokenExp: 24 * time.Hour,
		},
	}

	e := echo.New()

	// Force the client IP off-subnet so IsAuthRequired() returns true regardless
	// of the SubnetBypass configuration (TEST-NET-3 per RFC 5737).
	e.Pre(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Request().RemoteAddr = "203.0.113.42:54321"
			return next(c)
		}
	})

	mockDS := mocks.NewMockInterface(t)
	mockDS.EXPECT().PruneAppEvents(mock.Anything, mock.Anything).Return(int64(0), nil).Maybe()

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
		Security: securityConfig,
		BirdNET: conf.BirdNETConfig{
			Latitude:  testHelsinkiLatitude,
			Longitude: testHelsinkiLongitude,
		},
	}

	mockImageProvider := &apitest.MockImageProvider{}
	mockImageProvider.On("Fetch", mock.Anything).
		Return(imageprovider.BirdImage{}, nil).Maybe()

	birdImageCache := &imageprovider.BirdImageCache{}
	birdImageCache.SetImageProvider(mockImageProvider)

	sunCalc := suncalc.NewSunCalc(testHelsinkiLatitude, testHelsinkiLongitude)
	controlChan := make(chan string, testControlChannelBuf)
	mockMetrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to create test metrics")

	oauth2Server := securitytest.NewOAuth2ServerForTesting(t, settings)
	authService := auth.NewSecurityAdapter(oauth2Server)
	authMw := auth.NewMiddleware(authService)

	// Save and restore gothic.Store to avoid leaking session state between tests.
	prevGothicStore := gothic.Store
	gothic.Store = sessions.NewCookieStore([]byte(settings.Security.SessionSecret))
	t.Cleanup(func() {
		gothic.Store = prevGothicStore
	})

	// Isolated per-test notification service so the controller never depends on
	// the process-global singleton. Stopped via t.Cleanup so its cleanupLoop
	// goroutine does not leak (TestMain runs a goleak gate).
	notifService := notification.NewService(notification.DefaultServiceConfig())
	t.Cleanup(notifService.Stop)

	controller, err := NewWithOptions(
		e, mockDS, settings, birdImageCache, sunCalc, controlChan, mockMetrics,
		true, // initializeRoutes - register full /api/v2 route tree
		WithAuthMiddleware(authMw.Authenticate),
		WithAuthService(authService),
		WithNotificationService(notifService),
	)
	require.NoError(t, err, "Failed to create test API controller with auth")

	t.Cleanup(func() {
		controller.Shutdown()
		close(controlChan)
	})

	return e
}

// mediaByIDRoutes is the set of stored-detection-media endpoints that the
// advisory found reachable without authentication in Private Mode. They are
// registered on c.Echo (not the v2 group) and so must carry PrivateModeAuth
// per-route. A numeric ID is used so the request reaches the auth gate on the
// exact path an attacker would iterate (e.g. /api/v2/audio/1239).
var mediaByIDRoutes = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/api/v2/audio/1"},
	{http.MethodGet, "/api/v2/spectrogram/1"},
	{http.MethodGet, "/api/v2/spectrogram/1/status"},
	{http.MethodPost, "/api/v2/spectrogram/1/generate"},
}

// TestPrivateMode_MediaByIDRoutes_RequireAuth asserts that with Private Mode
// enabled, an unauthenticated request to each media-by-ID route is rejected with
// 401 before the handler runs. This is the core regression guard for
// GHSA-c7jx-552f-94hh: before the fix these routes bypassed PrivateModeAuth and
// returned stored detection media (or, for /generate, spawned server-side work)
// to anonymous clients. Auth short-circuits ahead of the handler, so no
// datastore interaction occurs.
func TestPrivateMode_MediaByIDRoutes_RequireAuth(t *testing.T) {
	e := newMediaAuthzTestEnv(t, true)

	for _, tc := range mediaByIDRoutes {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, http.NoBody)
			// No session cookie, no Authorization header, off-subnet IP -> guest.
			// httptest.NewRequest sets no Accept header, so the auth middleware
			// takes its API-client branch and returns 401 (a browser request with
			// Accept: text/html would instead get a 302 login redirect). This models
			// the attacker iterating numeric IDs (e.g. /api/v2/audio/1239).
			rec := httptest.NewRecorder()

			e.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusUnauthorized, rec.Code,
				"%s %s must return 401 to an unauthenticated request when Private Mode is enabled, got: %s",
				tc.method, tc.path, rec.Body.String())
		})
	}
}

// TestPublicMode_MediaByIDRoutes_RemainPublic guards the other direction: when
// Private Mode is OFF the same routes must stay reachable without authentication,
// so the fix does not break the default public read-only dashboard. Using a
// non-numeric ID makes each handler fail its numeric-ID validation with 400
// before any datastore access, which proves the request reached the handler
// (i.e. was NOT auth-gated). A 401 here would mean the routes were wrongly gated
// with AuthMiddleware instead of PrivateModeAuth.
func TestPublicMode_MediaByIDRoutes_RemainPublic(t *testing.T) {
	e := newMediaAuthzTestEnv(t, false)

	for _, tc := range mediaByIDRoutes {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			// Non-numeric ID: rejected by numeric validation (400) before any DS call.
			path := replaceLastIDSegment(tc.path)
			req := httptest.NewRequest(tc.method, path, http.NoBody)
			rec := httptest.NewRecorder()

			e.ServeHTTP(rec, req)

			assert.NotEqual(t, http.StatusUnauthorized, rec.Code,
				"%s %s must NOT require auth when Private Mode is off (public dashboard), got: %s",
				tc.method, path, rec.Body.String())
			assert.Equal(t, http.StatusBadRequest, rec.Code,
				"%s %s with a non-numeric ID should reach the handler and return 400, got: %s",
				tc.method, path, rec.Body.String())
		})
	}
}

// replaceLastIDSegment swaps the numeric "1" id segment for a non-numeric value
// so the handler's numeric validation rejects it (400) before touching the
// datastore. The routes are /api/v2/audio/1, /api/v2/spectrogram/1,
// /api/v2/spectrogram/1/status and /api/v2/spectrogram/1/generate.
func replaceLastIDSegment(path string) string {
	switch path {
	case "/api/v2/audio/1":
		return "/api/v2/audio/not-a-number"
	case "/api/v2/spectrogram/1":
		return "/api/v2/spectrogram/not-a-number"
	case "/api/v2/spectrogram/1/status":
		return "/api/v2/spectrogram/not-a-number/status"
	case "/api/v2/spectrogram/1/generate":
		return "/api/v2/spectrogram/not-a-number/generate"
	default:
		return path
	}
}
