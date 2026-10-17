// range_routes_test.go: route-level authentication tests for the range domain.
// The mutating routes (test and rebuild) must be gated by the core's
// AuthMiddleware regardless of PrivateMode, while the read routes stay public
// unless PrivateMode is on.
package rangeapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// authedHeader marks a request as authenticated for the stub auth middleware.
const authedHeader = "X-Test-Authed"

// newRangeRouteServer registers the range routes on an echo instance. The group
// carries PrivateModeAuth (as the facade does) and the core's AuthMiddleware is
// a stub that answers 401 unless authedHeader is present.
func newRangeRouteServer(t *testing.T, privateMode bool) *echo.Echo {
	t.Helper()
	e := echo.New()
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithSettingsFunc(func(s *conf.Settings) {
		s.Security.PrivateMode = privateMode
	}))
	core.AuthMiddleware = func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(ctx echo.Context) error {
			if ctx.Request().Header.Get(authedHeader) == "" {
				return ctx.NoContent(http.StatusUnauthorized)
			}
			return next(ctx)
		}
	}
	core.Group.Use(core.PrivateModeAuth)
	New(core).RegisterRoutes(core.Group)
	return e
}

func serve(e *echo.Echo, method, path string, authed bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if authed {
		req.Header.Set(authedHeader, "1")
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestRangeRoutesRegistered(t *testing.T) {
	// NOT parallel: apitest.NewCore publishes to the process-global settings snapshot.
	e := newRangeRouteServer(t, false)
	apitest.AssertRoutesRegistered(t, e, []string{
		"GET /api/v2/range/status",
		"GET /api/v2/range/species/scores",
		"GET /api/v2/range/species/count",
		"GET /api/v2/range/species/list",
		"GET /api/v2/range/species/csv",
		"POST /api/v2/range/species/test",
		"POST /api/v2/range/rebuild",
	})
}

// TestRangeMutatingRoutesRequireAuth verifies the mutating range routes reject
// anonymous callers with or without PrivateMode, and reach the handler once
// authenticated. With no BirdNET instance wired in the test core, a handler that
// ran answers 500, which cannot come from the auth stub (401).
func TestRangeMutatingRoutesRequireAuth(t *testing.T) {
	// NOT parallel: apitest.NewCore publishes to the process-global settings snapshot.
	routes := []string{"/api/v2/range/species/test", "/api/v2/range/rebuild"}
	for _, privateMode := range []bool{false, true} {
		for _, path := range routes {
			name := path
			if privateMode {
				name += " private"
			}
			t.Run(name, func(t *testing.T) {
				e := newRangeRouteServer(t, privateMode)

				anon := serve(e, http.MethodPost, path, false)
				assert.Equal(t, http.StatusUnauthorized, anon.Code, "anonymous request must be rejected")

				authed := serve(e, http.MethodPost, path, true)
				assert.Equal(t, http.StatusInternalServerError, authed.Code,
					"authenticated request must reach the handler (no BirdNET instance in the test core)")
			})
		}
	}
}

// TestRangeReadRoutesStayPublic verifies the GET range routes are not newly
// gated: anonymous callers reach the handler when PrivateMode is off and are
// rejected by PrivateModeAuth when it is on.
func TestRangeReadRoutesStayPublic(t *testing.T) {
	// NOT parallel: apitest.NewCore publishes to the process-global settings snapshot.
	paths := []string{
		"/api/v2/range/status",
		"/api/v2/range/species/count",
		"/api/v2/range/species/list",
		"/api/v2/range/species/scores",
		"/api/v2/range/species/csv",
	}
	for _, path := range paths {
		t.Run(path+" public", func(t *testing.T) {
			e := newRangeRouteServer(t, false)
			rec := serve(e, http.MethodGet, path, false)
			assert.NotEqual(t, http.StatusUnauthorized, rec.Code)
			assert.NotEqual(t, http.StatusForbidden, rec.Code)
		})
		t.Run(path+" private", func(t *testing.T) {
			e := newRangeRouteServer(t, true)
			rec := serve(e, http.MethodGet, path, false)
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
		})
	}
}
