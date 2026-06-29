package notifications

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
)

// TestRegisterRoutesRegistration verifies the notifications domain registers
// exactly the routes (and the same method+path set) the monolithic
// initNotificationRoutes/SetupNotificationRoutes pair did, in its own -race test
// binary. The auth-protected mutation/probe group is mounted with the
// AuthMiddleware promoted from the core; a pass-through is injected so the empty
// service group and the auth group register the same way production wiring does.
func TestRegisterRoutesRegistration(t *testing.T) {
	e := echo.New()
	core := apitest.NewCore(t, apitest.WithEcho(e))
	core.AuthMiddleware = func(next echo.HandlerFunc) echo.HandlerFunc { return next }

	// The handlers are not invoked here, so nil services are sufficient for the
	// route-enumeration gate.
	h := New(core, nil, nil)
	h.RegisterRoutes(core.Group)

	apitest.AssertRoutesRegistered(t, e, []string{
		"GET /api/v2/notifications",
		"GET /api/v2/notifications/unread/count",
		"GET /api/v2/notifications/stream",
		"PUT /api/v2/notifications/read-all",
		"GET /api/v2/notifications/:id",
		"PUT /api/v2/notifications/:id/read",
		"PUT /api/v2/notifications/:id/acknowledge",
		"DELETE /api/v2/notifications/:id",
		"POST /api/v2/notifications/test/new-species",
		"GET /api/v2/notifications/check-ntfy-server",
	})
}
