package models

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestInstallModel_RejectsHiddenEntries verifies that hidden, foundation-only
// catalog entries (the DFT-truncated BirdNET v2.4 variants) cannot be installed
// or reinstalled by ID. They carry the permanent registry ID, which Uninstall
// refuses, so allowing an install-by-ID would leave an unremovable, unused model.
func TestInstallModel_RejectsHiddenEntries(t *testing.T) {
	core := apitest.NewCore(t)
	h := New(core)
	e := echo.New()

	hiddenIDs := []string{"birdnet-v2.4-fp32-dfttrunc", "birdnet-v2.4-int8-arm-dfttrunc"}
	for _, id := range hiddenIDs {
		t.Run(id, func(t *testing.T) {
			// Install must be rejected with 404 before touching the model manager.
			req := httptest.NewRequest(http.MethodPost, "/api/v2/models/install/"+id, http.NoBody)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			ctx.SetParamNames("id")
			ctx.SetParamValues(id)
			require.NoError(t, h.InstallModel(ctx))
			assert.Equal(t, http.StatusNotFound, rec.Code, "install of hidden entry %q must be rejected", id)
			assert.Contains(t, rec.Body.String(), "not available for installation")

			// Reinstall must be rejected the same way.
			req = httptest.NewRequest(http.MethodPost, "/api/v2/models/reinstall/"+id, http.NoBody)
			rec = httptest.NewRecorder()
			ctx = e.NewContext(req, rec)
			ctx.SetParamNames("id")
			ctx.SetParamValues(id)
			require.NoError(t, h.ReinstallModel(ctx))
			assert.Equal(t, http.StatusNotFound, rec.Code, "reinstall of hidden entry %q must be rejected", id)
			assert.Contains(t, rec.Body.String(), "not available for installation")
		})
	}
}
