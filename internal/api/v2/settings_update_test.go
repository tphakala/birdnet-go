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

// TestDashboardPartialUpdate verifies dashboard settings preserve unmodified fields
func TestDashboardPartialUpdate(t *testing.T) {
	// Get initial settings and override some values for testing
	initialSettings := getTestSettings(t)
	initialSettings.Realtime.Dashboard.Thumbnails.ImageProvider = "testprovider"
	initialSettings.Realtime.Dashboard.SummaryLimit = 200

	// Capture initial values
	initialProvider := initialSettings.Realtime.Dashboard.Thumbnails.ImageProvider
	initialLimit := initialSettings.Realtime.Dashboard.SummaryLimit
	initialRecent := initialSettings.Realtime.Dashboard.Thumbnails.Recent

	// Create controller with settings
	e := echo.New()
	controller := &Controller{
		Echo:                e,
		Settings:            initialSettings,
		controlChan:         make(chan string, 10),
		DisableSaveSettings: true,
	}

	// Update only summary field
	update := map[string]any{
		"thumbnails": map[string]any{
			"summary": false,
		},
	}

	body, err := json.Marshal(update)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/dashboard", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues("dashboard")

	// Execute update
	err = controller.UpdateSectionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify the response
	var response map[string]any
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Contains(t, response["message"], "dashboard settings updated successfully")

	// Verify only the summary field changed, others preserved
	settings := controller.Settings
	assert.False(t, settings.Realtime.Dashboard.Thumbnails.Summary)                        // Changed
	assert.Equal(t, initialRecent, settings.Realtime.Dashboard.Thumbnails.Recent)          // Preserved
	assert.Equal(t, initialProvider, settings.Realtime.Dashboard.Thumbnails.ImageProvider) // Preserved
	assert.Equal(t, initialLimit, settings.Realtime.Dashboard.SummaryLimit)                // Preserved
}

// TestWeatherPartialUpdate verifies weather settings preserve unmodified fields
func TestWeatherPartialUpdate(t *testing.T) {
	// Get initial settings and override some values for testing
	initialSettings := getTestSettings(t)
	initialSettings.Realtime.Weather.Debug = true

	// Capture initial values
	initialPollInterval := initialSettings.Realtime.Weather.PollInterval
	initialDebug := initialSettings.Realtime.Weather.Debug

	// Create controller with settings
	e := echo.New()
	controller := &Controller{
		Echo:                e,
		Settings:            initialSettings,
		controlChan:         make(chan string, 10),
		DisableSaveSettings: true,
	}

	// Update only provider
	update := map[string]any{
		"provider": "openweather",
	}

	body, err := json.Marshal(update)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/weather", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues("weather")

	// Execute update
	err = controller.UpdateSectionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify settings were preserved
	settings := controller.Settings
	assert.Equal(t, "openweather", settings.Realtime.Weather.Provider)           // Changed
	assert.Equal(t, initialPollInterval, settings.Realtime.Weather.PollInterval) // Preserved
	assert.Equal(t, initialDebug, settings.Realtime.Weather.Debug)               // Preserved
}

// TestMQTTPartialUpdate verifies MQTT settings preserve unmodified fields
func TestMQTTPartialUpdate(t *testing.T) {
	// Get initial settings and override some values for testing
	initialSettings := getTestSettings(t)
	initialSettings.Realtime.MQTT.Enabled = true
	initialSettings.Realtime.MQTT.Retain = true
	initialSettings.Realtime.MQTT.Username = "testuser"

	// Capture initial values
	initialEnabled := initialSettings.Realtime.MQTT.Enabled
	initialTopic := initialSettings.Realtime.MQTT.Topic
	initialRetain := initialSettings.Realtime.MQTT.Retain
	initialUsername := initialSettings.Realtime.MQTT.Username

	// Create controller with settings
	e := echo.New()
	controller := &Controller{
		Echo:                e,
		Settings:            initialSettings,
		controlChan:         make(chan string, 10),
		DisableSaveSettings: true,
	}

	// Update only broker
	update := map[string]any{
		"broker": "tcp://newbroker:1883",
	}

	body, err := json.Marshal(update)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/mqtt", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues("mqtt")

	// Execute update
	err = controller.UpdateSectionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify settings were preserved
	settings := controller.Settings
	assert.Equal(t, "tcp://newbroker:1883", settings.Realtime.MQTT.Broker) // Changed
	assert.Equal(t, initialEnabled, settings.Realtime.MQTT.Enabled)        // Preserved
	assert.Equal(t, initialTopic, settings.Realtime.MQTT.Topic)            // Preserved
	assert.Equal(t, initialRetain, settings.Realtime.MQTT.Retain)          // Preserved
	assert.Equal(t, initialUsername, settings.Realtime.MQTT.Username)      // Preserved
}

// TestBirdNETCoordinatesUpdate verifies BirdNET settings preserve range filter
func TestBirdNETCoordinatesUpdate(t *testing.T) {
	// Get initial settings (already has the values we need from getTestSettings)
	initialSettings := getTestSettings(t)

	// Create controller with settings
	e := echo.New()
	controller := &Controller{
		Echo:                e,
		Settings:            initialSettings,
		controlChan:         make(chan string, 10),
		DisableSaveSettings: true,
	}

	// Update only coordinates
	update := map[string]any{
		"latitude":  51.5074,
		"longitude": -0.1278,
	}

	body, err := json.Marshal(update)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/birdnet", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues("birdnet")

	// Execute update
	err = controller.UpdateSectionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify settings were preserved
	settings := controller.Settings
	assert.InDelta(t, 51.5074, settings.BirdNET.Latitude, 0.0001)                    // Changed
	assert.InDelta(t, -0.1278, settings.BirdNET.Longitude, 0.0001)                   // Changed
	assert.InDelta(t, 1.0, settings.BirdNET.Sensitivity, 0.0001)                     // Preserved
	assert.InDelta(t, 0.8, settings.BirdNET.Threshold, 0.0001)                       // Preserved
	assert.Equal(t, "latest", settings.BirdNET.RangeFilter.Model)                    // Preserved
	assert.InDelta(t, float32(0.03), settings.BirdNET.RangeFilter.Threshold, 0.0001) // Preserved
}

// TestNestedRangeFilterUpdate verifies nested updates preserve parent fields
func TestNestedRangeFilterUpdate(t *testing.T) {
	// Get initial settings (already has the values we need from getTestSettings)
	initialSettings := getTestSettings(t)

	// Create controller with settings
	e := echo.New()
	controller := &Controller{
		Echo:                e,
		Settings:            initialSettings,
		controlChan:         make(chan string, 10),
		DisableSaveSettings: true,
	}

	// Update only range filter threshold
	update := map[string]any{
		"rangeFilter": map[string]any{
			"threshold": 0.05,
		},
	}

	body, err := json.Marshal(update)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/birdnet", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues("birdnet")

	// Execute update
	err = controller.UpdateSectionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify settings were preserved
	settings := controller.Settings
	assert.InDelta(t, float32(0.05), settings.BirdNET.RangeFilter.Threshold, 0.0001) // Changed
	assert.InDelta(t, 40.7128, settings.BirdNET.Latitude, 0.0001)                    // Preserved
	assert.InDelta(t, -74.0060, settings.BirdNET.Longitude, 0.0001)                  // Preserved
	assert.Equal(t, "latest", settings.BirdNET.RangeFilter.Model)                    // Preserved
}

// TestAudioExportPartialUpdate verifies audio export settings preserve unmodified fields
func TestAudioExportPartialUpdate(t *testing.T) {
	// Get initial settings (already has the values we need from getTestSettings)
	initialSettings := getTestSettings(t)

	// Create controller with settings
	e := echo.New()
	controller := &Controller{
		Echo:                e,
		Settings:            initialSettings,
		controlChan:         make(chan string, 10),
		DisableSaveSettings: true,
	}

	// Update only export type
	update := map[string]any{
		"export": map[string]any{
			"type": "mp3",
		},
	}

	body, err := json.Marshal(update)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/audio", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues("audio")

	// Execute update
	err = controller.UpdateSectionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify settings were preserved
	settings := controller.Settings
	assert.Equal(t, "mp3", settings.Realtime.Audio.Export.Type)     // Changed
	assert.True(t, settings.Realtime.Audio.Export.Enabled)          // Preserved
	assert.Equal(t, "/clips", settings.Realtime.Audio.Export.Path)  // Preserved
	assert.Equal(t, "192k", settings.Realtime.Audio.Export.Bitrate) // Preserved
}

// TestSpeciesConfigUpdate verifies complex nested species config updates
func TestSpeciesConfigUpdate(t *testing.T) {
	// Get initial settings and setup species config
	// Use lowercase key since that's what a real config would have after load normalization
	initialSettings := getTestSettings(t)
	initialSettings.Realtime.Species.Config["american robin"] = conf.SpeciesConfig{
		Threshold: 0.8,
		Interval:  30,
		Actions: []conf.SpeciesAction{{
			Type:            "ExecuteCommand",
			Command:         "/usr/bin/notify",
			Parameters:      []string{"--species", "American Robin"},
			ExecuteDefaults: true,
		}},
	}

	// Create controller with settings
	e := echo.New()
	controller := &Controller{
		Echo:                e,
		Settings:            initialSettings,
		controlChan:         make(chan string, 10),
		DisableSaveSettings: true,
	}

	// Update only threshold
	update := map[string]any{
		"config": map[string]any{
			"American Robin": map[string]any{
				"threshold": 0.9,
			},
		},
	}

	body, err := json.Marshal(update)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/species", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues("species")

	// Execute update
	err = controller.UpdateSectionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify settings were preserved (keys normalized to lowercase after API update)
	settings := controller.Settings
	robinConfig := settings.Realtime.Species.Config["american robin"]
	assert.InDelta(t, 0.9, robinConfig.Threshold, 0.0001) // Changed
	assert.Equal(t, 30, robinConfig.Interval)             // Preserved
	assert.Len(t, robinConfig.Actions, 1)                 // Preserved
	if len(robinConfig.Actions) > 0 {
		assert.Equal(t, "ExecuteCommand", robinConfig.Actions[0].Type)                              // Preserved
		assert.Equal(t, "/usr/bin/notify", robinConfig.Actions[0].Command)                          // Preserved
		assert.Equal(t, []string{"--species", "American Robin"}, robinConfig.Actions[0].Parameters) // Preserved
		assert.True(t, robinConfig.Actions[0].ExecuteDefaults)                                      // Preserved
	}
}

// TestEmptyUpdatePreservesEverything verifies empty updates don't change anything
func TestEmptyUpdatePreservesEverything(t *testing.T) {
	// Get initial settings and override some values for testing
	initialSettings := getTestSettings(t)
	initialSettings.Realtime.Dashboard.Thumbnails.ImageProvider = "wikimedia"

	// Get initial state
	initialJSON, err := json.Marshal(initialSettings.Realtime.Dashboard)
	require.NoError(t, err)

	// Create controller with settings
	e := echo.New()
	controller := &Controller{
		Echo:                e,
		Settings:            initialSettings,
		controlChan:         make(chan string, 10),
		DisableSaveSettings: true,
	}

	// Send empty update
	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/dashboard", bytes.NewReader([]byte("{}")))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues("dashboard")

	// Execute update
	err = controller.UpdateSectionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify nothing changed
	updatedSettings := controller.Settings
	updatedJSON, err := json.Marshal(updatedSettings.Realtime.Dashboard)
	require.NoError(t, err)

	assert.JSONEq(t, string(initialJSON), string(updatedJSON))
}

// TestValidationErrors verifies validation rules are enforced
func TestValidationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		section       string
		update        any
		expectedError string
		expectedCode  int
	}{
		{
			name:          "Validate MQTT broker required when enabled",
			section:       "mqtt",
			update:        map[string]any{"enabled": true, "broker": ""},
			expectedError: "Failed to update mqtt settings",
			expectedCode:  http.StatusBadRequest,
		},
		{
			name:          "Handle unknown section",
			section:       "nonexistent",
			update:        map[string]any{"foo": "bar"},
			expectedError: "Failed to update nonexistent settings",
			expectedCode:  http.StatusBadRequest,
		},
		{
			name:          "Reject invalid JSON",
			section:       "dashboard",
			update:        "{invalid json",
			expectedError: "Failed to parse request body",
			expectedCode:  http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := echo.New()
			controller := &Controller{
				Echo:        e,
				controlChan: make(chan string, 10),
			}

			var body []byte
			var err error
			if str, ok := tt.update.(string); ok {
				body = []byte(str)
			} else {
				body, err = json.Marshal(tt.update)
				require.NoError(t, err)
			}

			req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/"+tt.section, bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			ctx.SetParamNames("section")
			ctx.SetParamValues(tt.section)

			err = controller.UpdateSectionSettings(ctx)

			// Use helper function to assert error response
			assertControllerError(t, err, rec, tt.expectedCode, tt.expectedError)
		})
	}
}

// TestDeepNestedUpdates verifies deeply nested object updates preserve all levels
func TestDeepNestedUpdates(t *testing.T) {
	t.Parallel()

	// Get initial settings and override some values for testing
	initialSettings := getTestSettings(t)
	initialSettings.Realtime.MQTT.TLS.Enabled = true
	initialSettings.Realtime.MQTT.TLS.InsecureSkipVerify = false

	// Capture initial values
	initialMaxRetries := initialSettings.Realtime.MQTT.RetrySettings.MaxRetries
	initialBackoff := initialSettings.Realtime.MQTT.RetrySettings.BackoffMultiplier
	initialTLSEnabled := initialSettings.Realtime.MQTT.TLS.Enabled

	e := echo.New()
	controller := &Controller{
		Echo:                e,
		Settings:            initialSettings,
		controlChan:         make(chan string, 10),
		DisableSaveSettings: true,
	}

	// Update only one deeply nested field
	update := map[string]any{
		"retrySettings": map[string]any{
			"initialDelay": 30,
		},
	}

	body, err := json.Marshal(update)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/mqtt", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues("mqtt")

	err = controller.UpdateSectionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify only the targeted field changed
	settings := controller.Settings
	assert.Equal(t, 30, settings.Realtime.MQTT.RetrySettings.InitialDelay)                           // Changed
	assert.Equal(t, initialMaxRetries, settings.Realtime.MQTT.RetrySettings.MaxRetries)              // Preserved
	assert.InDelta(t, initialBackoff, settings.Realtime.MQTT.RetrySettings.BackoffMultiplier, 0.001) // Preserved
	assert.Equal(t, initialTLSEnabled, settings.Realtime.MQTT.TLS.Enabled)                           // Preserved
}
