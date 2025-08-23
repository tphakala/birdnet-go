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
			require.NoError(t, err, "Update should succeed")
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
			assert.InDelta(t, tt.expectedConfig.Threshold, actualConfig.Threshold, 0.0001, 
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

// TestSpeciesSettingsUpdate tests that species config updates preserve zero values correctly  
func TestSpeciesSettingsUpdate(t *testing.T) {
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
	updatePayload := map[string]interface{}{
		"interval": 15,
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
					"threshold": 0.0, // Add new bird with zero values
					"interval":  0,   
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
	require.NoError(t, err, "Update should succeed")
	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify zero values are preserved in the controller's settings
	// This is the core fix - zero values should be preserved
	initialBird := controller.Settings.Realtime.Species.Config["Initial Bird"]
	assert.InDelta(t, 0.0, initialBird.Threshold, 0.0001, "Initial Bird threshold should be zero")
	assert.Equal(t, 0, initialBird.Interval, "Initial Bird interval should be zero")

	newBird := controller.Settings.Realtime.Species.Config["New Bird"] 
	assert.InDelta(t, 0.0, newBird.Threshold, 0.0001, "New Bird threshold should be zero")
	assert.Equal(t, 0, newBird.Interval, "New Bird interval should be zero")

	// Verify the API response indicates success
	var response map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err, "Response should be valid JSON")
	assert.Equal(t, "realtime settings updated successfully", response["message"])
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
	assert.InDelta(t, 0.0, birdA.Threshold, 0.0001, "Bird A threshold should be zero")
	assert.Equal(t, 0, birdA.Interval, "Bird A interval should be zero")
	assert.Empty(t, birdA.Actions, "Bird A actions should be cleared")

	// Verify Bird B remains unchanged
	birdB := controller.Settings.Realtime.Species.Config["Bird B"]
	assert.InDelta(t, 0.8, birdB.Threshold, 0.0001, "Bird B threshold should be unchanged")
	assert.Equal(t, 60, birdB.Interval, "Bird B interval should be unchanged")
}