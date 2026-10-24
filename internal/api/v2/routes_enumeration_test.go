package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

const (
	// routeParamValue replaces ":name" path parameters when probing a route.
	routeParamValue = "1"
	// routeWildcardValue replaces a "*" path segment when probing a route.
	routeWildcardValue = "x"
	// routeProbeTimeout bounds one probe request. It only matters when a route
	// that should be gated is not and its handler blocks (an SSE stream, for
	// example), so the failure is reported instead of hanging the run.
	routeProbeTimeout = 5 * time.Second
	// routeTestMetricsPoints sizes the in-memory metrics store the controller is
	// given so the metrics history routes register.
	routeTestMetricsPoints = 10
	// routeTestV2DBName is the SQLite file the route tests create in a temp dir.
	routeTestV2DBName = "routes_v2.db"
)

var routeParamPattern = regexp.MustCompile(`:[A-Za-z_]+`)

// criticalRoutes must stay registered: at least one route per initializer in
// initRoutes, plus health and ping. The media initializer's entry is the greedy
// audio route registered directly on the Echo instance.
// initRoutes recovers a panicking initializer and carries on, so a broken
// RegisterRoutes would otherwise drop its routes silently and the auth tests
// below would simply have fewer routes to check. Prefer the last route an
// initializer registers, because a panic aborts everything after it.
//
// When you add an initializer to initRoutes, add one of its routes here.
var criticalRoutes = []string{
	"GET /api/v2/health",
	"GET /api/v2/ping",
	"GET /api/v2/app/config",
	"POST /api/v2/search",
	"GET /api/v2/detections/:id",
	"GET /api/v2/analytics/sources",
	"GET /api/v2/weather/latest",
	"POST /api/v2/system/database/legacy/cleanup",
	"GET /api/v2/system/audio/devices",
	"GET /api/v2/terminal/ws",
	"PATCH /api/v2/settings/:section",
	"GET /api/v2/filesystem/browse",
	"GET /api/v2/streams/health",
	"POST /api/v2/streams/test",
	"GET /api/v2/health/audio",
	"GET /api/v2/streams/quiet-hours/status",
	"GET /api/v2/streams/audio-level",
	"POST /api/v2/streams/hls/:sourceID/start",
	"POST /api/v2/integrations/mqtt/test",
	"POST /api/v2/control/restart",
	"POST /api/v2/auth/login",
	"GET /api/v2/audio/:id",
	"POST /api/v2/range/rebuild",
	"GET /api/v2/range/heatmap",
	"GET /api/v2/detections/stream",
	"POST /api/v2/system/diagnostics/run",
	"GET /api/v2/system/events/operational",
	"GET /api/v2/system/metrics/stream",
	"GET /api/v2/notifications",
	"POST /api/v2/support/generate",
	"POST /api/v2/debug/trigger-error",
	"GET /api/v2/species/taxonomy",
	"DELETE /api/v2/dynamic-thresholds",
	"POST /api/v2/alerts/rules",
	"POST /api/v2/models/install/:id",
	"GET /api/v2/insights/migration",
	"POST /api/v2/tls/certificate",
	"POST /api/v2/import/birdnet-pi",
}

// publicMutationRoutes lists the routes that change state (any method other
// than GET and HEAD) without requiring authentication. Add an entry only for a
// route that is meant to be public, with the reason next to it.
var publicMutationRoutes = map[string]bool{
	// Login has to be reachable before a session exists.
	"POST /api/v2/auth/login": true,
	// Registered as publicly accessible (detections/handler.go, RegisterSearchRoutes).
	"POST /api/v2/search": true,
	// Registered with PrivateModeAuth only (media/media.go), so public outside private mode.
	"POST /api/v2/spectrogram/:id/generate": true,
}

// authOnlyPrefixes are path prefixes where every route requires authentication
// whatever its method, because even the reads expose operator-only data. Each
// prefix must match at least one registered route.
var authOnlyPrefixes = []string{
	apiV2Prefix + "/control",
	apiV2Prefix + "/debug",
	apiV2Prefix + "/filesystem",
	apiV2Prefix + "/import",
	apiV2Prefix + "/integrations",
	apiV2Prefix + "/settings",
	apiV2Prefix + "/support",
	apiV2Prefix + "/system",
	apiV2Prefix + "/terminal",
	apiV2Prefix + "/tls",
}

// publicReadRoutes lists the GET routes under authOnlyPrefixes that are public
// on purpose.
var publicReadRoutes = map[string]bool{
	// Display preferences the SPA needs before login (settings.go, "/settings/dashboard").
	"GET /api/v2/settings/dashboard": true,
}

// authGate stands in for the real auth middleware. It records the matched
// route template of every request that reaches it and answers 401 without
// calling the next handler, so a route is gated exactly when it shows up here.
type authGate struct {
	mu   sync.Mutex
	hits map[string]bool
}

func newAuthGate() *authGate {
	return &authGate{hits: map[string]bool{}}
}

// middleware is the echo.MiddlewareFunc handed to WithAuthMiddleware.
func (g *authGate) middleware(_ echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) error {
		g.mu.Lock()
		g.hits[ctx.Request().Method+" "+ctx.Path()] = true
		g.mu.Unlock()
		return ctx.NoContent(http.StatusUnauthorized)
	}
}

// reached reports whether a request routed to the "METHOD /template" key
// reached the gate.
func (g *authGate) reached(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.hits[key]
}

// buildFullyRoutedController builds a controller the way production does (every
// route initializer runs, a v2 database manager is present and in enhanced
// mode) with gate standing in for the auth middleware. mutate adjusts the test
// settings before they are published. The tests that call it must not run in
// parallel: they publish the process-global settings snapshot and set the
// process-global enhanced-database flag.
func buildFullyRoutedController(t *testing.T, mutate func(*conf.Settings)) (*echo.Echo, *authGate) {
	t.Helper()

	// The alerts routes answer 409 before auth unless a v2 manager is set and
	// the enhanced-database flag is on, so both are needed to reach their gate.
	// The metrics history routes register only when a metrics store is set.
	// Cleanups run last-in first-out, so the controller shuts down before the
	// manager closes and the manager closes before its temp dir is removed.
	mgr, err := datastoreV2.NewSQLiteManager(datastoreV2.Config{
		DirectPath: filepath.Join(t.TempDir(), routeTestV2DBName),
	})
	require.NoError(t, err)
	require.NoError(t, mgr.Initialize())
	t.Cleanup(func() { assert.NoError(t, mgr.Close()) })
	datastoreV2.SetEnhancedDatabaseMode()
	t.Cleanup(datastoreV2.ResetDatabaseMode)

	e := echo.New()
	// Production has a global Recover; the root-level media routes sit outside
	// the group's own Recover, so install it here too.
	e.Use(middleware.Recover())

	// A bare mock, not NewMockInterface(t): an ungated handler that reaches the
	// datastore panics into Recover and the assertion names the route, instead
	// of the mock aborting the whole loop with t.FailNow.
	mockDS := &mocks.MockInterface{}
	mockDS.On("PruneAppEvents", mock.Anything, mock.Anything).Return(int64(0), nil).Maybe()

	settings := apitest.NewValidTestSettings()
	// Debug routes register only in debug mode.
	settings.Debug = true
	settings.Realtime.Audio.Export.Path = t.TempDir()
	if mutate != nil {
		mutate(settings)
	}
	apitest.PublishTestSettings(t, settings)

	gate := newAuthGate()
	c, err := NewWithOptions(e, mockDS, settings, &imageprovider.BirdImageCache{},
		suncalc.NewSunCalc(testHelsinkiLatitude, testHelsinkiLongitude),
		make(chan string, testControlChannelBuf), apitest.NewTestMetrics(t), true,
		WithAuthMiddleware(gate.middleware), WithV2Manager(mgr),
		WithMetricsStore(observability.NewMemoryStore(routeTestMetricsPoints)))
	require.NoError(t, err)
	t.Cleanup(c.Shutdown)
	return e, gate
}

// requestRoute sends a request with no body through e for the registered route r.
func requestRoute(t *testing.T, e *echo.Echo, r *echo.Route) {
	t.Helper()
	path := routeParamPattern.ReplaceAllString(r.Path, routeParamValue)
	path = strings.ReplaceAll(path, "*", routeWildcardValue)
	ctx, cancel := context.WithTimeout(t.Context(), routeProbeTimeout)
	defer cancel()
	req := httptest.NewRequest(r.Method, path, http.NoBody).WithContext(ctx)
	e.ServeHTTP(httptest.NewRecorder(), req)
}

func hasPathPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// requiresAuth is the rule the route tests enforce: a route requires auth when
// it is not a GET or HEAD and is not a listed public mutation, or when it sits
// under an authOnlyPrefixes entry and is not a listed public read.
func requiresAuth(method, path string) bool {
	key := method + " " + path
	if method != http.MethodGet && method != http.MethodHead {
		return !publicMutationRoutes[key]
	}
	if publicReadRoutes[key] {
		return false
	}
	_, ok := authOnlyPrefix(path)
	return ok
}

// authOnlyPrefix returns the authOnlyPrefixes entry that path falls under.
func authOnlyPrefix(path string) (string, bool) {
	for _, prefix := range authOnlyPrefixes {
		if hasPathPrefix(path, prefix) {
			return prefix, true
		}
	}
	return "", false
}

// registeredRoutes returns the registered routes keyed by "METHOD /template",
// leaving out the route-not-found stubs Echo adds for group prefixes.
func registeredRoutes(e *echo.Echo) map[string]*echo.Route {
	out := map[string]*echo.Route{}
	for _, r := range e.Routes() {
		if r.Method == echo.RouteNotFound {
			continue
		}
		out[r.Method+" "+r.Path] = r
	}
	return out
}

// TestCriticalRoutesRegistered fails when an initializer's route disappears,
// for example because its RegisterRoutes panicked and initRoutes carried on.
func TestCriticalRoutesRegistered(t *testing.T) {
	// NOT parallel: see buildFullyRoutedController.
	e, _ := buildFullyRoutedController(t, nil)
	apitest.AssertRoutesRegistered(t, e, criticalRoutes)
}

// TestProtectedRoutesRequireAuth sends a request through every route that
// requiresAuth and fails, naming the route, when it does not reach the auth
// middleware. A new mutating route is checked without any edit here.
func TestProtectedRoutesRequireAuth(t *testing.T) {
	// NOT parallel: see buildFullyRoutedController.
	e, gate := buildFullyRoutedController(t, nil)
	reg := registeredRoutes(e)

	// An allow-list entry for a route that no longer exists would hide a rename.
	for key := range publicMutationRoutes {
		assert.Contains(t, reg, key, "stale publicMutationRoutes entry")
	}
	for key := range publicReadRoutes {
		assert.Contains(t, reg, key, "stale publicReadRoutes entry")
	}

	perPrefix := map[string]int{}
	checked := 0
	for key, r := range reg {
		if !requiresAuth(r.Method, r.Path) {
			continue
		}
		if prefix, ok := authOnlyPrefix(r.Path); ok {
			perPrefix[prefix]++
		}
		checked++
		requestRoute(t, e, r)
		assert.True(t, gate.reached(key), "%s must reach the auth middleware", key)
	}

	// A prefix with no routes would make its check pass vacuously.
	for _, prefix := range authOnlyPrefixes {
		assert.Positive(t, perPrefix[prefix], "no registered route under %s", prefix)
	}
	t.Logf("checked %d routes that require auth", checked)
}

// TestPrivateModeGatesNonExemptRoutes turns PrivateMode on and sends a request
// through every route outside isPrivateModeExempt; each must reach the auth
// middleware, including routes registered directly on the Echo instance.
func TestPrivateModeGatesNonExemptRoutes(t *testing.T) {
	// NOT parallel: see buildFullyRoutedController.
	e, gate := buildFullyRoutedController(t, func(s *conf.Settings) { s.Security.PrivateMode = true })
	checked := 0
	for key, r := range registeredRoutes(e) {
		if isPrivateModeExempt(r.Method, r.Path) {
			continue
		}
		checked++
		requestRoute(t, e, r)
		assert.True(t, gate.reached(key), "%s must reach the auth middleware in private mode", key)
	}
	t.Logf("checked %d non-exempt routes", checked)
}
