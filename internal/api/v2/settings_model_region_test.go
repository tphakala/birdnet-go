package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// patchBirdNET PATCHes the birdnet settings section with the given body and
// returns the recorder.
func patchBirdNET(t *testing.T, controller *Controller, e *echo.Echo, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/birdnet", bytes.NewReader(raw))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues("birdnet")

	require.NoError(t, controller.UpdateSectionSettings(ctx))
	return rec
}

// TestPatchModelRegionRoundTrip verifies a well-formed region slug persists
// through a PATCH of the birdnet section.
func TestPatchModelRegionRoundTrip(t *testing.T) {
	e := echo.New()
	controller := getTestController(t, e)

	rec := patchBirdNET(t, controller, e, map[string]any{"modelRegion": "iberia"})
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "iberia", controller.Settings.Load().BirdNET.ModelRegion,
		"modelRegion must persist after PATCH")

	// The mode values round-trip too.
	rec = patchBirdNET(t, controller, e, map[string]any{"modelRegion": "global"})
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "global", controller.Settings.Load().BirdNET.ModelRegion)
}

// TestPatchModelRegionRejectsMalformed verifies a malformed region value is a
// 400 through the API (unlike the startup validator, which normalizes it).
func TestPatchModelRegionRejectsMalformed(t *testing.T) {
	e := echo.New()
	controller := getTestController(t, e)
	controller.Settings.Load().WebServer.Debug = true // expose the raw error field

	rec := patchBirdNET(t, controller, e, map[string]any{"modelRegion": "Bad Region!"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var response map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Contains(t, response["error"], "modelRegion must be")
}

// TestPatchModelRegionRejectsCaseVariantKey guards the case-insensitive key
// match: encoding/json binds a case-variant key like "modelregion" to
// BirdNETConfig.ModelRegion on merge, so the validator must reject a malformed
// value under any casing, not only the exact "modelRegion" key.
func TestPatchModelRegionRejectsCaseVariantKey(t *testing.T) {
	e := echo.New()
	controller := getTestController(t, e)
	controller.Settings.Load().WebServer.Debug = true // expose the raw error field

	rec := patchBirdNET(t, controller, e, map[string]any{"modelregion": "Bad Region!"})
	assert.Equal(t, http.StatusBadRequest, rec.Code, "a malformed case-variant key must not bypass validation")

	var response map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Contains(t, response["error"], "modelRegion must be")
}

// TestPatchModelRegionRejectsDuplicateCaseKeys rejects a payload carrying the
// key under two different casings, whose bind order json resolves ambiguously.
func TestPatchModelRegionRejectsDuplicateCaseKeys(t *testing.T) {
	e := echo.New()
	controller := getTestController(t, e)
	controller.Settings.Load().WebServer.Debug = true // expose the raw error field

	rec := patchBirdNET(t, controller, e, map[string]any{"modelRegion": "auto", "modelregion": "iberia"})
	assert.Equal(t, http.StatusBadRequest, rec.Code, "duplicate case-variant keys must be rejected")

	var response map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Contains(t, response["error"], "multiple times")
}
