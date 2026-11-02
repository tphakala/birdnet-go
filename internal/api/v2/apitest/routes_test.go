package apitest

import (
	"net/http"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
)

// errorRecorder counts Errorf calls instead of failing, so a test can watch
// AssertRoutesRequireAuth reject a route without failing itself. Every other
// method goes to the embedded real test.
type errorRecorder struct {
	testing.TB
	failures int
}

// Errorf records the failure and returns instead of failing the test.
func (r *errorRecorder) Errorf(string, ...any) {
	r.failures++
}

// okHandler answers 200 for a route registered by a test.
func okHandler(ctx echo.Context) error {
	return ctx.NoContent(http.StatusOK)
}

// TestAssertRoutesRequireAuth pins what the helper reports. The catch-all rows
// are the point: Echo answers 401 for an unregistered path under a gated group,
// so a helper that checked only the status would pass them, and every domain
// test built on it would pass vacuously.
func TestAssertRoutesRequireAuth(t *testing.T) {
	t.Parallel()

	const gatedRoute = "GET /api/v2/gated/item"

	tests := []struct {
		name       string
		register   func(*echo.Group, echo.MiddlewareFunc)
		routes     []string
		wantFailed bool
	}{
		{
			name: "route behind the gate passes",
			register: func(g *echo.Group, gate echo.MiddlewareFunc) {
				g.Group("/gated", gate).GET("/item", okHandler)
			},
			routes: []string{gatedRoute},
		},
		{
			name: "parameter route behind the gate passes",
			register: func(g *echo.Group, gate echo.MiddlewareFunc) {
				g.Group("/gated", gate).GET("/item/:id", okHandler)
			},
			routes: []string{"GET /api/v2/gated/item/:id"},
		},
		{
			name: "expected route that is not registered fails",
			register: func(g *echo.Group, gate echo.MiddlewareFunc) {
				g.Group("/gated", gate)
			},
			routes:     []string{gatedRoute},
			wantFailed: true,
		},
		{
			name: "route registered with another method fails",
			register: func(g *echo.Group, gate echo.MiddlewareFunc) {
				g.Group("/gated", gate).GET("/item", okHandler)
			},
			routes:     []string{gatedRoute, "POST /api/v2/gated/item"},
			wantFailed: true,
		},
		{
			name: "route registered outside the gated group fails",
			register: func(g *echo.Group, gate echo.MiddlewareFunc) {
				g.Group("/gated", gate)
				g.GET("/open/item", okHandler)
			},
			routes:     []string{"GET /api/v2/open/item"},
			wantFailed: true,
		},
		{
			name: "registered route missing from the list fails",
			register: func(g *echo.Group, gate echo.MiddlewareFunc) {
				gated := g.Group("/gated", gate)
				gated.GET("/item", okHandler)
				gated.GET("/extra", okHandler)
			},
			routes:     []string{gatedRoute},
			wantFailed: true,
		},
		{
			name: "route registered without the gate fails",
			register: func(g *echo.Group, _ echo.MiddlewareFunc) {
				g.Group("/gated").GET("/item", okHandler)
			},
			routes:     []string{gatedRoute},
			wantFailed: true,
		},
		{
			name:       "malformed entry fails",
			register:   func(*echo.Group, echo.MiddlewareFunc) {},
			routes:     []string{"GET"},
			wantFailed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			core := NewCore(t, WithoutSettingsPublish())
			rec := &errorRecorder{TB: t}

			AssertRoutesRequireAuth(rec, core, func(g *echo.Group) {
				// The helper installs the gate before calling register.
				tt.register(g, core.AuthMiddleware)
			}, tt.routes)

			assert.Equal(t, tt.wantFailed, rec.failures > 0, "failures recorded: %d", rec.failures)
		})
	}
}

// TestAssertRoutesRequireAuthIgnoresExistingRoutes tests that a route the Echo
// instance held before register ran is neither probed nor reported as missing
// from the expected list.
func TestAssertRoutesRequireAuthIgnoresExistingRoutes(t *testing.T) {
	t.Parallel()

	e := echo.New()
	e.GET("/preexisting", okHandler)
	core := NewCore(t, WithEcho(e), WithoutSettingsPublish())
	rec := &errorRecorder{TB: t}

	AssertRoutesRequireAuth(rec, core, func(g *echo.Group) {
		g.Group("/gated", core.AuthMiddleware).GET("/item", okHandler)
	}, []string{"GET /api/v2/gated/item"})

	assert.Zero(t, rec.failures)
}
