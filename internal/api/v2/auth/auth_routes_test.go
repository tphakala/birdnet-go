package authapi

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
)

// TestRegisterRoutesRegistration verifies the auth domain registers exactly the
// routes (and the same method+path set) the monolithic initAuthRoutes did, in
// its own -race test binary. The protected logout/status group is mounted with
// the AuthMiddleware promoted from the core; a pass-through is injected so the
// empty-prefix protected group registers the same way production wiring does.
func TestRegisterRoutesRegistration(t *testing.T) {
	e := echo.New()
	core := apitest.NewCore(t, apitest.WithEcho(e))
	core.AuthMiddleware = func(next echo.HandlerFunc) echo.HandlerFunc { return next }

	// authService is not needed to register routes (the handlers are not invoked
	// here), so a nil service is sufficient for the route-enumeration gate.
	h := New(core, nil)
	h.RegisterRoutes(core.Group)

	apitest.AssertRoutesRegistered(t, e, []string{
		"POST /api/v2/auth/login",
		"GET /api/v2/auth/callback",
		"POST /api/v2/auth/logout",
		"GET /api/v2/auth/status",
	})
}
