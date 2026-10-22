package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestUpdateSpeciesSettingsWithZeroValues tests that zero values in species config are preserved
func TestUpdateSpeciesSettingsWithZeroValues(t *testing.T) {
	tests := []struct {
		name      string
		bird      string
		threshold float64
		interval  int
	}{
		{name: "zero_threshold_and_interval", bird: "Test Bird", threshold: 0.0, interval: 0},
		{name: "only_threshold_zero", bird: "Rare Bird", threshold: 0.0, interval: 60},
		{name: "only_interval_zero", bird: "Common Bird", threshold: 0.85, interval: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			controller := createTestController(t)

			rec := patchRealtime(t, e, controller, map[string]any{
				"species": map[string]any{
					"config": map[string]any{
						tt.bird: map[string]any{
							"threshold": tt.threshold,
							"interval":  tt.interval,
							"actions":   []any{},
						},
					},
				},
			})
			require.Equal(t, http.StatusOK, rec.Code, "Should return 200 OK")

			// Keys are normalized to lowercase after an API update
			actualConfig := controller.Settings.Load().Realtime.Species.Config[strings.ToLower(tt.bird)]
			assert.InDelta(t, tt.threshold, actualConfig.Threshold, 0.0001, "Threshold should match")
			assert.Equal(t, tt.interval, actualConfig.Interval, "Interval should match")
			// An emptied actions list stays an empty slice, not nil
			assert.Empty(t, actualConfig.Actions, "Actions should be empty")
			assert.NotNil(t, actualConfig.Actions, "Actions should be an empty slice, not nil")
		})
	}
}

// TestSpeciesSettingsPatchGetSync tests that PATCH -> GET works correctly
// This ensures that updates to controller.Settings are reflected in GET responses
func TestSpeciesSettingsPatchGetSync(t *testing.T) {
	// Setup controller with its own settings (simulating real usage)
	// Use lowercase keys since they're set directly
	e := echo.New()
	settings := apitest.NewValidTestSettings()
	settings.Realtime.Interval = 15
	settings.Realtime.Species = conf.SpeciesSettings{
		Include: []string{},
		Exclude: []string{},
		Config: map[string]conf.SpeciesConfig{
			"test bird": {
				Threshold: 0.5,
				Interval:  30,
				Actions:   []conf.SpeciesAction{},
			},
		},
	}
	controller := &Controller{Core: &apicore.Core{}, DisableSaveSettings: true}
	controller.Settings.Store(settings)

	// PATCH zero values over the seeded 0.5 and 30. Keys in the payload can be any
	// case; they are normalized to lowercase.
	rec := patchRealtime(t, e, controller, map[string]any{
		"species": map[string]any{
			"config": map[string]any{
				"Test Bird": map[string]any{
					"threshold": 0.0,
					"interval":  0,
					"actions":   []any{},
				},
			},
		},
	})
	require.Equal(t, http.StatusOK, rec.Code)

	// GET must return the zero values, not the seeded ones. The fields are
	// pointers so an absent key fails instead of decoding as zero.
	rec = getRealtime(t, e, controller)
	require.Equal(t, http.StatusOK, rec.Code)

	var response struct {
		Species struct {
			Config map[string]struct {
				Threshold *float64 `json:"threshold"`
				Interval  *int     `json:"interval"`
			} `json:"config"`
		} `json:"species"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response), "Response should be valid JSON")

	testBird, ok := response.Species.Config["test bird"]
	require.True(t, ok, "test bird should exist in GET response")
	require.NotNil(t, testBird.Threshold, "test bird should have threshold")
	require.NotNil(t, testBird.Interval, "test bird should have interval")
	assert.InDelta(t, 0.0, *testBird.Threshold, 0.0001, "GET should return updated zero threshold")
	assert.Equal(t, 0, *testBird.Interval, "GET should return updated zero interval")
}

// TestSpeciesSettingsRejectInvalid tests that invalid species config is rejected by the API
func TestSpeciesSettingsRejectInvalid(t *testing.T) {
	// Setup
	e := echo.New()
	controller := createTestController(t)

	// Test with invalid threshold (1.5 > 1.0 max allowed) and invalid interval (-1 < 0 min allowed)
	invalidPayload := map[string]any{
		"species": map[string]any{
			"config": map[string]any{
				"Invalid Bird": map[string]any{
					"threshold": 1.5, // Invalid: threshold > 1.0
					"interval":  -1,  // Invalid: interval < 0
					"actions":   []any{},
				},
			},
		},
	}

	rec := patchRealtime(t, e, controller, invalidPayload)

	// Should return client error status (400 Bad Request)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "Should reject invalid species config")

	// Unmarshal response body to check validation error
	var response map[string]any
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err, "Response should be valid JSON")

	// Assert the presence/shape of the validation error
	assert.Contains(t, response, "error", "Response should contain error field for validation failure")

	// Verify error message mentions the failed settings update
	if errorField, exists := response["error"]; exists {
		errorString, ok := errorField.(string)
		require.True(t, ok, "Error field should be a string")
		assert.Equal(t, "Failed to update realtime settings", errorString, "Error should be the sanitized message")
	}
}

// Test helper functions

// patchRealtime creates a PATCH request context for the realtime section
func patchRealtime(t *testing.T, e *echo.Echo, c *Controller, payload any) *httptest.ResponseRecorder {
	t.Helper()

	data, err := json.Marshal(payload)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/realtime", bytes.NewReader(data))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/settings/:section")
	ctx.SetParamNames("section")
	ctx.SetParamValues("realtime")

	require.NoError(t, c.UpdateSectionSettings(ctx))
	return rec
}

// getRealtime creates a GET request context for the realtime section
func getRealtime(t *testing.T, e *echo.Echo, c *Controller) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/settings/realtime", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/settings/:section")
	ctx.SetParamNames("section")
	ctx.SetParamValues("realtime")

	require.NoError(t, c.GetSectionSettings(ctx))
	return rec
}

// TestSpeciesConfigNormalizationOnAPIUpdate tests that species config keys
// are normalized to lowercase when updated via API
func TestSpeciesConfigNormalizationOnAPIUpdate(t *testing.T) {
	e := echo.New()
	settings := apitest.NewValidTestSettings()
	settings.Realtime.Species = conf.SpeciesSettings{
		Include: []string{},
		Exclude: []string{},
		Config:  make(map[string]conf.SpeciesConfig),
	}
	controller := &Controller{Core: &apicore.Core{}, DisableSaveSettings: true}
	controller.Settings.Store(settings)

	// Update with mixed-case species names (as UI would send)
	updatePayload := map[string]any{
		"species": map[string]any{
			"config": map[string]any{
				"American Robin": map[string]any{
					"threshold": 0.75,
					"interval":  30,
					"actions":   []any{},
				},
				"House Sparrow": map[string]any{
					"threshold": 0.85,
					"interval":  0,
					"actions":   []any{},
				},
			},
		},
	}

	body, err := json.Marshal(updatePayload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/realtime", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues("realtime")

	err = controller.UpdateSectionSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)

	// Verify keys are normalized to lowercase in controller settings
	_, hasLowercaseRobin := controller.Settings.Load().Realtime.Species.Config["american robin"]
	assert.True(t, hasLowercaseRobin, "should have lowercase 'american robin' key after API update")

	_, hasLowercaseSparrow := controller.Settings.Load().Realtime.Species.Config["house sparrow"]
	assert.True(t, hasLowercaseSparrow, "should have lowercase 'house sparrow' key after API update")

	// Verify mixed-case keys don't exist
	_, hasMixedCaseRobin := controller.Settings.Load().Realtime.Species.Config["American Robin"]
	assert.False(t, hasMixedCaseRobin, "should not have mixed-case 'American Robin' key")

	// Verify config values are preserved
	robin := controller.Settings.Load().Realtime.Species.Config["american robin"]
	assert.InDelta(t, 0.75, robin.Threshold, 0.0001)
	assert.Equal(t, 30, robin.Interval)
}

// createTestController creates a test controller with initialized settings for species tests
func createTestController(t *testing.T) *Controller {
	t.Helper()

	settings := apitest.NewValidTestSettings()
	settings.Realtime.Species = conf.SpeciesSettings{
		Include: []string{},
		Exclude: []string{},
		Config:  map[string]conf.SpeciesConfig{},
	}

	c := &Controller{Core: &apicore.Core{}, DisableSaveSettings: true}
	c.Settings.Store(settings)
	return c
}
