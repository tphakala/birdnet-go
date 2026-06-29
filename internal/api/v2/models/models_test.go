package models

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
)

// TestModelsRouteRegistration verifies the models handler registers exactly the
// model endpoints, with the same methods and paths the monolithic facade used
// before the domain was extracted.
func TestModelsRouteRegistration(t *testing.T) {
	e := echo.New()
	core := apitest.NewCore(t, apitest.WithEcho(e))
	h := New(core)

	h.RegisterRoutes(core.Group)

	expectedRoutes := []string{
		"GET /api/v2/models",
		"GET /api/v2/models/catalog",
		"GET /api/v2/models/installed",
		"POST /api/v2/models/install/:id",
		"POST /api/v2/models/reinstall/:id",
		"DELETE /api/v2/models/installed/:id",
		"GET /api/v2/models/install/:id/progress",
	}
	apitest.AssertRoutesRegistered(t, e, expectedRoutes)
}
