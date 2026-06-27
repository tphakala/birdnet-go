package integrations

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
)

// TestRegisterRoutesRegistration verifies the integrations domain registers
// exactly the routes (and the same method+path set) the monolithic
// initIntegrationsRoutes did, in its own -race test binary. The auth-protected
// integrations group and the nested MQTT TLS group are mounted with the
// AuthMiddleware promoted from the core; a pass-through is injected so the groups
// register the same way production wiring does.
func TestRegisterRoutesRegistration(t *testing.T) {
	e, h := newIntegrationsTestHandler(t)
	h.AuthMiddleware = func(next echo.HandlerFunc) echo.HandlerFunc { return next }

	h.RegisterRoutes(h.Group)

	apitest.AssertRoutesRegistered(t, e, []string{
		"GET /api/v2/integrations/mqtt/status",
		"POST /api/v2/integrations/mqtt/test",
		"POST /api/v2/integrations/mqtt/homeassistant/discovery",
		"GET /api/v2/integrations/mqtt/tls/certificate",
		"POST /api/v2/integrations/mqtt/tls/certificate",
		"DELETE /api/v2/integrations/mqtt/tls/certificate",
		"GET /api/v2/integrations/birdweather/status",
		"POST /api/v2/integrations/birdweather/test",
		"POST /api/v2/integrations/weather/test",
		"POST /api/v2/integrations/ebird/test",
	})
}
