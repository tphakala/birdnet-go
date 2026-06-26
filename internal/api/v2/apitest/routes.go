package apitest

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
)

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
