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
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestUpdateSpeciesSettingsWithZeroValues tests that zero values in species config are preserved
func TestUpdateSpeciesSettingsWithZeroValues(t *testing.T) {
	t.Parallel()

	// Setup
	e := echo.New()
	controller := createTestController(t)

	tests := []struct {
		name           string
		payload        map[string]any
		expectedConfig conf.SpeciesConfig
		description    string
	}{
		{
			name: "zero_threshold_and_interval",
			payload: map[string]any{
				"species": map[string]any{
					"include": []string{},
					"exclude": []string{},
					"config": map[string]any{
						"Test Bird": map[string]any{
							"threshold": 0.0,
							"interval":  0,
							"actions":   []any{},
						},
					},
				},
			},
			expectedConfig: conf.SpeciesConfig{
				Threshold: 0.0,
				Interval:  0,
				Actions:   []conf.SpeciesAction{},
			},
			description: "Zero values should be preserved",
		},
		{
			name: "only_threshold_zero",
			payload: map[string]any{
				"species": map[string]any{
					"config": map[string]any{
						"Rare Bird": map[string]any{
							"threshold": 0.0,
							"interval":  60,
							"actions":   []any{},
						},
					},
				},
			},
			expectedConfig: conf.SpeciesConfig{
				Threshold: 0.0,
				Interval:  60,
				Actions:   []conf.SpeciesAction{},
			},
			description: "Zero threshold with non-zero interval",
		},
		{
			name: "only_interval_zero",
			payload: map[string]any{
				"species": map[string]any{
					"config": map[string]any{
						"Common Bird": map[string]any{
							"threshold": 0.85,
							"interval":  0,
							"actions":   []any{},
						},
					},
				},
			},
			expectedConfig: conf.SpeciesConfig{
				Threshold: 0.85,
				Interval:  0,
				Actions:   []conf.SpeciesAction{},
			},
			description: "Non-zero threshold with zero interval",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Execute update
			rec := patchRealtime(t, e, controller, tt.payload)
			require.Equal(t, http.StatusOK, rec.Code, "Should return 200 OK")

			// Extract the bird name from the payload with safe type assertions
			speciesInterface, hasSpecies := tt.payload["species"]
			require.True(t, hasSpecies, "Payload should contain species field")
			speciesMap, ok := speciesInterface.(map[string]any)
			require.True(t, ok, "Species field should be a map")

			configInterface, hasConfig := speciesMap["config"]
			require.True(t, hasConfig, "Species should contain config field")
			configMap, ok := configInterface.(map[string]any)
			require.True(t, ok, "Config field should be a map")
			var birdName string
			for name := range configMap {
				birdName = name
				break
			}

			// Verify the settings were updated correctly
			actualConfig := controller.Settings.Realtime.Species.Config[birdName]
			assert.InDelta(t, tt.expectedConfig.Threshold, actualConfig.Threshold, 0.0001,
				"%s: Threshold should match", tt.description)
			assert.Equal(t, tt.expectedConfig.Interval, actualConfig.Interval,
				"%s: Interval should match", tt.description)

			// Verify Actions slice is empty but not nil (should match expected)
			assert.Empty(t, actualConfig.Actions, "%s: Actions should be empty", tt.description)
			assert.NotNil(t, actualConfig.Actions, "%s: Actions should be empty slice, not nil", tt.description)

			// Verify the response includes the updated values
			var response map[string]any
			err := json.Unmarshal(rec.Body.Bytes(), &response)
			require.NoError(t, err, "Response should be valid JSON")

			// Type-assert the message field to ensure it's a string
			messageField, exists := response["message"]
			require.True(t, exists, "Response should contain message field")
			message, ok := messageField.(string)
			require.True(t, ok, "Message field should be a string")
			assert.Equal(t, "realtime settings updated successfully", message)
		})
	}
}

// TestSpeciesSettingsUpdate tests that species config updates preserve zero values correctly
func TestSpeciesSettingsUpdate(t *testing.T) {
	t.Parallel()

	// Setup
	e := echo.New()
	controller := &Controller{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Interval: 15,
				Species: conf.SpeciesSettings{
					Include: []string{"Robin"},
					Exclude: []string{"Crow"},
					Config: map[string]conf.SpeciesConfig{
						"Initial Bird": {
							Threshold: 0.5,
							Interval:  30,
							Actions:   []conf.SpeciesAction{},
						},
					},
				},
			},
		},
		DisableSaveSettings: true,
	}

	// Update with zero values
	updatePayload := map[string]any{
		"interval": 15,
		"species": map[string]any{
			"include": []string{"Robin"},
			"exclude": []string{"Crow"},
			"config": map[string]any{
				"Initial Bird": map[string]any{
					"threshold": 0.0, // Update to zero
					"interval":  0,   // Update to zero
					"actions":   []any{},
				},
				"New Bird": map[string]any{
					"threshold": 0.0, // Add new bird with zero values
					"interval":  0,
					"actions":   []any{},
				},
			},
		},
	}

	rec := patchRealtime(t, e, controller, updatePayload)
	require.Equal(t, http.StatusOK, rec.Code)

	// Verify zero values are preserved in the controller's settings
	// Zero threshold and interval values should persist after update operations
	initialBird := controller.Settings.Realtime.Species.Config["Initial Bird"]
	assert.InDelta(t, 0.0, initialBird.Threshold, 0.0001, "Initial Bird threshold should be zero")
	assert.Equal(t, 0, initialBird.Interval, "Initial Bird interval should be zero")
	assert.Empty(t, initialBird.Actions, "Initial Bird actions should be empty")
	assert.NotNil(t, initialBird.Actions, "Initial Bird actions should be empty slice, not nil")

	newBird := controller.Settings.Realtime.Species.Config["New Bird"]
	assert.InDelta(t, 0.0, newBird.Threshold, 0.0001, "New Bird threshold should be zero")
	assert.Equal(t, 0, newBird.Interval, "New Bird interval should be zero")
	assert.Empty(t, newBird.Actions, "New Bird actions should be empty")
	assert.NotNil(t, newBird.Actions, "New Bird actions should be empty slice, not nil")

	// Verify the API response indicates success
	var response map[string]any
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err, "Response should be valid JSON")

	// Type-assert the message field to ensure it's a string
	messageField, exists := response["message"]
	require.True(t, exists, "Response should contain message field")
	message, ok := messageField.(string)
	require.True(t, ok, "Message field should be a string")
	assert.Equal(t, "realtime settings updated successfully", message)
}

// TestPartialSpeciesConfigUpdate tests that partial updates don't lose existing data
func TestPartialSpeciesConfigUpdate(t *testing.T) {
	t.Parallel()

	// Setup with existing configs
	e := echo.New()
	controller := &Controller{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Species: conf.SpeciesSettings{
					Config: map[string]conf.SpeciesConfig{
						"Bird A": {
							Threshold: 0.7,
							Interval:  30,
							Actions: []conf.SpeciesAction{
								{
									Type:       "ExecuteCommand",
									Command:    "/bin/notify",
									Parameters: []string{"CommonName"},
								},
							},
						},
						"Bird B": {
							Threshold: 0.8,
							Interval:  60,
							Actions:   []conf.SpeciesAction{},
						},
					},
				},
			},
		},
		DisableSaveSettings: true,
	}

	// Update only Bird A with zero values, Bird B should remain unchanged
	updatePayload := map[string]any{
		"species": map[string]any{
			"config": map[string]any{
				"Bird A": map[string]any{
					"threshold": 0.0,
					"interval":  0,
					"actions":   []any{}, // Clear actions
				},
				"Bird B": map[string]any{
					"threshold": 0.8,
					"interval":  60,
					"actions":   []any{},
				},
			},
		},
	}

	rec := patchRealtime(t, e, controller, updatePayload)
	require.Equal(t, http.StatusOK, rec.Code)

	// Verify Bird A was updated with zero values
	birdA := controller.Settings.Realtime.Species.Config["Bird A"]
	assert.InDelta(t, 0.0, birdA.Threshold, 0.0001, "Bird A threshold should be zero")
	assert.Equal(t, 0, birdA.Interval, "Bird A interval should be zero")
	assert.Empty(t, birdA.Actions, "Bird A actions should be cleared")
	assert.NotNil(t, birdA.Actions, "Bird A actions should be empty slice, not nil")

	// Verify Bird B remains unchanged
	birdB := controller.Settings.Realtime.Species.Config["Bird B"]
	assert.InDelta(t, 0.8, birdB.Threshold, 0.0001, "Bird B threshold should be unchanged")
	assert.Equal(t, 60, birdB.Interval, "Bird B interval should be unchanged")
	assert.NotNil(t, birdB.Actions, "Bird B actions should be empty slice, not nil")
}

// TestSpeciesSettingsPatchGetSync tests that PATCH -> GET works correctly
// This ensures that updates to controller.Settings are reflected in GET responses
func TestSpeciesSettingsPatchGetSync(t *testing.T) {
	t.Parallel()

	// Setup controller with its own settings (simulating real usage)
	e := echo.New()
	controller := &Controller{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Interval: 15,
				Species: conf.SpeciesSettings{
					Include: []string{},
					Exclude: []string{},
					Config: map[string]conf.SpeciesConfig{
						"Test Bird": {
							Threshold: 0.5,
							Interval:  30,
							Actions:   []conf.SpeciesAction{},
						},
					},
				},
			},
		},
		DisableSaveSettings: true,
	}

	// Step 1: PATCH update with zero values
	updatePayload := map[string]any{
		"species": map[string]any{
			"config": map[string]any{
				"Test Bird": map[string]any{
					"threshold": 0.0, // Zero value that should persist
					"interval":  0,   // Zero value that should persist
					"actions":   []any{},
				},
			},
		},
	}

	rec := patchRealtime(t, e, controller, updatePayload)
	require.Equal(t, http.StatusOK, rec.Code)

	// Verify controller settings were updated
	testBird := controller.Settings.Realtime.Species.Config["Test Bird"]
	assert.InDelta(t, 0.0, testBird.Threshold, 0.0001, "Controller should have zero threshold")
	assert.Equal(t, 0, testBird.Interval, "Controller should have zero interval")

	// Step 2: GET to verify the updated values are returned
	rec = getRealtime(t, e, controller)
	require.Equal(t, http.StatusOK, rec.Code)

	var response map[string]any
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err, "Response should be valid JSON")

	// Verify GET endpoint returns the updated zero values after PATCH operation
	speciesInterface, hasSpecies := response["species"]
	require.True(t, hasSpecies, "Response should contain species field")
	species, ok := speciesInterface.(map[string]any)
	require.True(t, ok, "Species field should be a map")

	configInterface, hasConfig := species["config"]
	require.True(t, hasConfig, "Species should contain config field")
	require.NotNil(t, configInterface, "Config should not be nil")
	config, ok := configInterface.(map[string]any)
	require.True(t, ok, "Config field should be a map")

	testBirdInterface, hasTestBird := config["Test Bird"]
	require.True(t, hasTestBird, "Test Bird should exist in GET response")
	testBirdResponse, ok := testBirdInterface.(map[string]any)
	require.True(t, ok, "Test Bird should be a map")

	// Extract and validate threshold (JSON numbers decode as float64)
	thresholdInterface, hasThreshold := testBirdResponse["threshold"]
	require.True(t, hasThreshold, "Test Bird should have threshold")
	threshold, ok := thresholdInterface.(float64)
	require.True(t, ok, "Threshold should be a number")
	assert.InDelta(t, 0.0, threshold, 0.0001, "GET should return updated zero threshold")

	// Extract and validate interval (JSON numbers decode as float64)
	intervalInterface, hasInterval := testBirdResponse["interval"]
	require.True(t, hasInterval, "Test Bird should have interval")
	intervalFloat, ok := intervalInterface.(float64)
	require.True(t, ok, "Interval should be a number")
	assert.InDelta(t, float64(0), intervalFloat, 0.0001, "GET should return updated zero interval")
}

// TestSpeciesSettingsRejectInvalid tests that invalid species config is rejected by the API
func TestSpeciesSettingsRejectInvalid(t *testing.T) {
	t.Parallel()

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

	// Verify error message mentions validation failure
	if errorField, exists := response["error"]; exists {
		errorString, ok := errorField.(string)
		require.True(t, ok, "Error field should be a string")
		assert.Contains(t, errorString, "validation", "Error should mention validation failure")
	}
}

// Test helper functions

// newAPIContext creates an Echo instance, controller, and context for testing
func newAPIContext(t *testing.T, e *echo.Echo, method, path string, body any) (echo.Context, *httptest.ResponseRecorder, *Controller) {
	t.Helper()

	controller := &Controller{
		Settings: &conf.Settings{
			Main: struct {
				Name      string `json:"name"`
				TimeAs24h bool   `json:"timeAs24h"`
			}{
				Name: "TestNode",
			},
			Realtime: conf.RealtimeSettings{
				Species: conf.SpeciesSettings{
					Include: []string{},
					Exclude: []string{},
					Config:  map[string]conf.SpeciesConfig{},
				},
			},
		},
		DisableSaveSettings: true, // Disable file save for testing
	}

	// Use default values if method or path are empty
	if method == "" {
		method = http.MethodGet
	}
	if path == "" {
		path = "/api/v2/test"
	}

	var req *http.Request
	if body != nil {
		jsonData, err := json.Marshal(body)
		require.NoError(t, err)
		req = httptest.NewRequest(method, path, bytes.NewReader(jsonData))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	} else {
		req = httptest.NewRequest(method, path, http.NoBody)
	}

	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	return ctx, rec, controller
}

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
	t.Parallel()

	e := echo.New()
	controller := &Controller{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Species: conf.SpeciesSettings{
					Include: []string{},
					Exclude: []string{},
					Config:  make(map[string]conf.SpeciesConfig),
				},
			},
		},
		DisableSaveSettings: true,
	}

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
	_, hasLowercaseRobin := controller.Settings.Realtime.Species.Config["american robin"]
	assert.True(t, hasLowercaseRobin, "should have lowercase 'american robin' key after API update")

	_, hasLowercaseSparrow := controller.Settings.Realtime.Species.Config["house sparrow"]
	assert.True(t, hasLowercaseSparrow, "should have lowercase 'house sparrow' key after API update")

	// Verify mixed-case keys don't exist
	_, hasMixedCaseRobin := controller.Settings.Realtime.Species.Config["American Robin"]
	assert.False(t, hasMixedCaseRobin, "should not have mixed-case 'American Robin' key")

	// Verify config values are preserved
	robin := controller.Settings.Realtime.Species.Config["american robin"]
	assert.InDelta(t, 0.75, robin.Threshold, 0.0001)
	assert.Equal(t, 30, robin.Interval)
}

// createTestController creates a test controller with initialized settings for species tests
func createTestController(t *testing.T) *Controller {
	t.Helper()

	return &Controller{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Species: conf.SpeciesSettings{
					Include: []string{},
					Exclude: []string{},
					Config:  map[string]conf.SpeciesConfig{},
				},
			},
		},
		DisableSaveSettings: true,
	}
}
