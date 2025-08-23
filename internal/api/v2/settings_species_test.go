package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestUpdateSpeciesSettingsWithZeroValues tests that zero values in species config are preserved
func TestUpdateSpeciesSettingsWithZeroValues(t *testing.T) {
	// Setup
	e := echo.New()
	controller := &Controller{
		Settings: &conf.Settings{
			Main: struct {
				Name      string         `json:"name"`
				TimeAs24h bool           `json:"timeAs24h"`
				Log       conf.LogConfig `json:"log"`
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

	tests := []struct {
		name           string
		payload        map[string]interface{}
		expectedConfig conf.SpeciesConfig
		description    string
	}{
		{
			name: "zero_threshold_and_interval",
			payload: map[string]interface{}{
				"species": map[string]interface{}{
					"include": []string{},
					"exclude": []string{},
					"config": map[string]interface{}{
						"Test Bird": map[string]interface{}{
							"threshold": 0.0,
							"interval":  0,
							"actions":   []interface{}{},
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
			payload: map[string]interface{}{
				"species": map[string]interface{}{
					"config": map[string]interface{}{
						"Rare Bird": map[string]interface{}{
							"threshold": 0.0,
							"interval":  60,
							"actions":   []interface{}{},
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
			payload: map[string]interface{}{
				"species": map[string]interface{}{
					"config": map[string]interface{}{
						"Common Bird": map[string]interface{}{
							"threshold": 0.85,
							"interval":  0,
							"actions":   []interface{}{},
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
			// Create request
			jsonData, err := json.Marshal(tt.payload)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/realtime", bytes.NewReader(jsonData))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			ctx.SetPath("/api/v2/settings/realtime")
			ctx.SetParamNames("section")
			ctx.SetParamValues("realtime")

			// Execute update
			err = controller.UpdateSectionSettings(ctx)
			assert.NoError(t, err, "Update should succeed")
			assert.Equal(t, http.StatusOK, rec.Code, "Should return 200 OK")

			// Extract the bird name from the payload
			speciesMap := tt.payload["species"].(map[string]interface{})
			configMap := speciesMap["config"].(map[string]interface{})
			var birdName string
			for name := range configMap {
				birdName = name
				break
			}

			// Verify the settings were updated correctly
			actualConfig := controller.Settings.Realtime.Species.Config[birdName]
			assert.Equal(t, tt.expectedConfig.Threshold, actualConfig.Threshold, 
				"%s: Threshold should match", tt.description)
			assert.Equal(t, tt.expectedConfig.Interval, actualConfig.Interval, 
				"%s: Interval should match", tt.description)

			// Verify the response includes the updated values
			var response map[string]interface{}
			err = json.Unmarshal(rec.Body.Bytes(), &response)
			require.NoError(t, err, "Response should be valid JSON")
			assert.Equal(t, "realtime settings updated successfully", response["message"])
		})
	}
}

// TestSpeciesSettingsRoundTrip tests GET -> UPDATE -> GET cycle
func TestSpeciesSettingsRoundTrip(t *testing.T) {
	// Setup
	e := echo.New()
	
	// Initialize with a complete Settings structure
	initialSettings := &conf.Settings{
		Main: struct {
			Name      string         `json:"name"`
			TimeAs24h bool           `json:"timeAs24h"`
			Log       conf.LogConfig `json:"log"`
		}{
			Name: "TestNode",
		},
		BirdNET: conf.BirdNETConfig{
			Threshold: 0.3,
			Locale:    "en",
		},
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
	}
	
	controller := &Controller{
		Settings:            initialSettings,
		DisableSaveSettings: true,
		settingsMutex:       sync.RWMutex{},
	}

	// Step 1: GET current settings
	req := httptest.NewRequest(http.MethodGet, "/api/v2/settings/realtime", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/settings/realtime")
	ctx.SetParamNames("section")
	ctx.SetParamValues("realtime")

	err := controller.GetSectionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var originalSettings map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &originalSettings)
	require.NoError(t, err)

	// Step 2: UPDATE with zero values
	// Note: We need to send the complete realtime section for PATCH
	updatePayload := map[string]interface{}{
		"interval": 15,  // Include other realtime fields
		"species": map[string]interface{}{
			"include": []string{"Robin"},
			"exclude": []string{"Crow"},
			"config": map[string]interface{}{
				"Initial Bird": map[string]interface{}{
					"threshold": 0.0, // Update to zero
					"interval":  0,   // Update to zero
					"actions":   []interface{}{},
				},
				"New Bird": map[string]interface{}{
					"threshold": 0.0,
					"interval":  0,
					"actions":   []interface{}{},
				},
			},
		},
	}

	jsonData, err := json.Marshal(updatePayload)
	require.NoError(t, err)

	req = httptest.NewRequest(http.MethodPatch, "/api/v2/settings/realtime", bytes.NewReader(jsonData))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec = httptest.NewRecorder()
	ctx = e.NewContext(req, rec)
	ctx.SetPath("/api/v2/settings/realtime")
	ctx.SetParamNames("section")
	ctx.SetParamValues("realtime")

	err = controller.UpdateSectionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Step 3: GET settings again to verify persistence
	req = httptest.NewRequest(http.MethodGet, "/api/v2/settings/realtime", http.NoBody)
	rec = httptest.NewRecorder()
	ctx = e.NewContext(req, rec)
	ctx.SetPath("/api/v2/settings/realtime")
	ctx.SetParamNames("section")
	ctx.SetParamValues("realtime")

	err = controller.GetSectionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var updatedSettings map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &updatedSettings)
	require.NoError(t, err)

	// Debug: Print the response to see what we actually got
	responseJSON, _ := json.MarshalIndent(updatedSettings, "", "  ")
	t.Logf("Response after update: %s", responseJSON)

	// Verify the zero values are present in the response
	// The response contains the full realtime settings, not just species
	species, ok := updatedSettings["species"].(map[string]interface{})
	require.True(t, ok, "Response should contain species field")
	
	// Check if config exists and is not nil
	configInterface, hasConfig := species["config"]
	if !hasConfig {
		t.Logf("Species object: %+v", species)
	}
	require.True(t, hasConfig, "Species should have a config field")
	require.NotNil(t, configInterface, "Config field should not be nil")
	
	configMap, ok := configInterface.(map[string]interface{})
	require.True(t, ok, "Config should be a map")
	
	// Check Initial Bird was updated to zero values
	initialBird, ok := configMap["Initial Bird"].(map[string]interface{})
	require.True(t, ok, "Initial Bird should exist in config")
	assert.Equal(t, 0.0, initialBird["threshold"], "Initial Bird threshold should be zero")
	assert.Equal(t, float64(0), initialBird["interval"], "Initial Bird interval should be zero")

	// Check New Bird has zero values
	newBird, ok := configMap["New Bird"].(map[string]interface{})
	require.True(t, ok, "New Bird should exist in config")
	assert.Equal(t, 0.0, newBird["threshold"], "New Bird threshold should be zero")
	assert.Equal(t, float64(0), newBird["interval"], "New Bird interval should be zero")
}

// TestPartialSpeciesConfigUpdate tests that partial updates don't lose existing data
func TestPartialSpeciesConfigUpdate(t *testing.T) {
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
	updatePayload := map[string]interface{}{
		"species": map[string]interface{}{
			"config": map[string]interface{}{
				"Bird A": map[string]interface{}{
					"threshold": 0.0,
					"interval":  0,
					"actions":   []interface{}{}, // Clear actions
				},
				"Bird B": map[string]interface{}{
					"threshold": 0.8,
					"interval":  60,
					"actions":   []interface{}{},
				},
			},
		},
	}

	jsonData, err := json.Marshal(updatePayload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/realtime", bytes.NewReader(jsonData))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/settings/realtime")
	ctx.SetParamNames("section")
	ctx.SetParamValues("realtime")

	err = controller.UpdateSectionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify Bird A was updated with zero values
	birdA := controller.Settings.Realtime.Species.Config["Bird A"]
	assert.Equal(t, 0.0, birdA.Threshold, "Bird A threshold should be zero")
	assert.Equal(t, 0, birdA.Interval, "Bird A interval should be zero")
	assert.Empty(t, birdA.Actions, "Bird A actions should be cleared")

	// Verify Bird B remains unchanged
	birdB := controller.Settings.Realtime.Species.Config["Bird B"]
	assert.Equal(t, 0.8, birdB.Threshold, "Bird B threshold should be unchanged")
	assert.Equal(t, 60, birdB.Interval, "Bird B interval should be unchanged")
}