package apitest

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
)

// routeParamValue replaces ":name" path parameters when probing a route.
const routeParamValue = "1"

// routeParamPattern matches an Echo ":name" path parameter.
var routeParamPattern = regexp.MustCompile(`:[A-Za-z_]+`)

// AssertRoutesRegistered verifies that all expected routes are registered on the
// Echo instance. Each expected entry is a "METHOD PATH" string (for example
// "GET /api/v2/control/actions"). It asserts every expected route is present;
// extra registered routes are ignored.
func AssertRoutesRegistered(t *testing.T, e *echo.Echo, expectedRoutes []string) {
	t.Helper()

	// Track which expected routes were found.
	routeFound := make(map[string]bool, len(expectedRoutes))
	for _, route := range expectedRoutes {
		routeFound[route] = false
	}

	for _, r := range e.Routes() {
		routePath := r.Method + " " + r.Path
		if _, exists := routeFound[routePath]; exists {
			routeFound[routePath] = true
		}
	}

	for route, found := range routeFound {
		assert.True(t, found, "Route not registered: %s", route)
	}
}

// AssertRoutesRequireAuth verifies that the routes registered by the register
// func are exactly the expected routes and that each sits behind the handler's
// auth middleware. Each expected entry is a "METHOD PATH" string with the full
// path (for example "POST /api/v2/control/restart"); ":name" parameters are
// filled in for the probe request. A route added by register that is not listed
// fails, so a new route has to be listed here and gated. Routes the Echo
// instance already held before register ran are ignored.
//
// It installs a rejecting middleware as core.AuthMiddleware before calling
// register, so the group is built with it, then sends a request to each route
// and asserts that the middleware saw the request under the route's own
// template. The response status cannot be the check: Echo adds a catch-all route
// behind a group's middleware, so a deleted route, or one registered with
// another method, still answers 401. A route registered outside the gated group
// never reaches the middleware at all.
func AssertRoutesRequireAuth(tb testing.TB, core *apicore.Core, register func(*echo.Group), routes []string) {
	tb.Helper()

	existing := routeKeys(core.Echo)
	reached := make(map[string]bool, len(routes))
	core.AuthMiddleware = func(echo.HandlerFunc) echo.HandlerFunc {
		return func(ctx echo.Context) error {
			reached[ctx.Request().Method+" "+ctx.Path()] = true
			return ctx.NoContent(http.StatusUnauthorized)
		}
	}
	register(core.Group)

	expected := make(map[string]bool, len(routes))
	for _, route := range routes {
		expected[route] = true
	}
	for key := range routeKeys(core.Echo) {
		if !existing[key] {
			assert.True(tb, expected[key], "%s is registered but missing from the expected routes", key)
		}
	}

	for _, route := range routes {
		method, template, ok := strings.Cut(route, " ")
		if !ok {
			assert.Fail(tb, "malformed route entry, want \"METHOD PATH\"", route)
			continue
		}

		path := routeParamPattern.ReplaceAllString(template, routeParamValue)
		req := httptest.NewRequestWithContext(tb.Context(), method, path, http.NoBody)
		core.Echo.ServeHTTP(httptest.NewRecorder(), req)

		assert.True(tb, reached[route], "%s must reach the auth middleware as its own route", route)
	}
}

// routeKeys returns the "METHOD PATH" key of every route registered on e,
// leaving out the not-found handlers Echo adds behind a group's middleware.
func routeKeys(e *echo.Echo) map[string]bool {
	keys := make(map[string]bool)
	for _, r := range e.Routes() {
		if r.Method != echo.RouteNotFound {
			keys[r.Method+" "+r.Path] = true
		}
	}
	return keys
}
