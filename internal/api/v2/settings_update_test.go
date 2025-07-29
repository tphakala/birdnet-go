package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestDashboardPartialUpdate verifies dashboard settings preserve unmodified fields
func TestDashboardPartialUpdate(t *testing.T) {
	// Setup viper with initial settings
	viper.Reset()
	viper.Set("realtime.dashboard.thumbnails.summary", true)
	viper.Set("realtime.dashboard.thumbnails.recent", true)
	viper.Set("realtime.dashboard.thumbnails.imageProvider", "testprovider")
	viper.Set("realtime.dashboard.summaryLimit", 200)
	
	// Get initial settings to capture actual values
	initialSettings := conf.Setting()
	initialProvider := initialSettings.Realtime.Dashboard.Thumbnails.ImageProvider
	initialLimit := initialSettings.Realtime.Dashboard.SummaryLimit
	initialRecent := initialSettings.Realtime.Dashboard.Thumbnails.Recent
	
	// Create controller with settings
	e := echo.New()
	controller := &Controller{
		Echo:        e,
		Settings:    conf.Setting(),
		controlChan: make(chan string, 10),
	}
	
	// Update only summary field
	update := map[string]interface{}{
		"thumbnails": map[string]interface{}{
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
	var response map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Contains(t, response["message"], "dashboard settings updated successfully")
	
	// Verify only the summary field changed, others preserved
	settings := conf.Setting()
	assert.False(t, settings.Realtime.Dashboard.Thumbnails.Summary) // Changed
	assert.Equal(t, initialRecent, settings.Realtime.Dashboard.Thumbnails.Recent) // Preserved
	assert.Equal(t, initialProvider, settings.Realtime.Dashboard.Thumbnails.ImageProvider) // Preserved
	assert.Equal(t, initialLimit, settings.Realtime.Dashboard.SummaryLimit) // Preserved
}

// TestWeatherPartialUpdate verifies weather settings preserve unmodified fields
func TestWeatherPartialUpdate(t *testing.T) {
	// Setup viper with initial settings
	viper.Reset()
	viper.Set("realtime.weather.provider", "yrno")
	viper.Set("realtime.weather.pollInterval", 60)
	viper.Set("realtime.weather.debug", true)
	
	// Get initial settings
	initialSettings := conf.Setting()
	initialPollInterval := initialSettings.Realtime.Weather.PollInterval
	initialDebug := initialSettings.Realtime.Weather.Debug
	
	// Create controller with settings
	e := echo.New()
	controller := &Controller{
		Echo:        e,
		Settings:    conf.Setting(),
		controlChan: make(chan string, 10),
	}
	
	// Update only provider
	update := map[string]interface{}{
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
	settings := conf.Setting()
	assert.Equal(t, "openweather", settings.Realtime.Weather.Provider) // Changed
	assert.Equal(t, initialPollInterval, settings.Realtime.Weather.PollInterval) // Preserved
	assert.Equal(t, initialDebug, settings.Realtime.Weather.Debug) // Preserved
}

// TestMQTTPartialUpdate verifies MQTT settings preserve unmodified fields
func TestMQTTPartialUpdate(t *testing.T) {
	// Setup viper with initial settings
	viper.Reset()
	viper.Set("realtime.mqtt.enabled", true)
	viper.Set("realtime.mqtt.broker", "tcp://localhost:1883")
	viper.Set("realtime.mqtt.topic", "birdnet/detections")
	viper.Set("realtime.mqtt.retain", true)
	viper.Set("realtime.mqtt.username", "testuser")
	
	// Get initial settings
	initialSettings := conf.Setting()
	initialEnabled := initialSettings.Realtime.MQTT.Enabled
	initialTopic := initialSettings.Realtime.MQTT.Topic
	initialRetain := initialSettings.Realtime.MQTT.Retain
	initialUsername := initialSettings.Realtime.MQTT.Username
	
	// Create controller with settings
	e := echo.New()
	controller := &Controller{
		Echo:        e,
		Settings:    conf.Setting(),
		controlChan: make(chan string, 10),
	}
	
	// Update only broker
	update := map[string]interface{}{
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
	settings := conf.Setting()
	assert.Equal(t, "tcp://newbroker:1883", settings.Realtime.MQTT.Broker) // Changed
	assert.Equal(t, initialEnabled, settings.Realtime.MQTT.Enabled) // Preserved
	assert.Equal(t, initialTopic, settings.Realtime.MQTT.Topic) // Preserved
	assert.Equal(t, initialRetain, settings.Realtime.MQTT.Retain) // Preserved
	assert.Equal(t, initialUsername, settings.Realtime.MQTT.Username) // Preserved
}

// TestBirdNETCoordinatesUpdate verifies BirdNET settings preserve range filter
func TestBirdNETCoordinatesUpdate(t *testing.T) {
	// Setup viper with initial settings
	viper.Reset()
	viper.Set("birdnet.latitude", 40.7128)
	viper.Set("birdnet.longitude", -74.0060)
	viper.Set("birdnet.sensitivity", 1.0)
	viper.Set("birdnet.threshold", 0.8)
	viper.Set("birdnet.rangeFilter.model", "latest")
	viper.Set("birdnet.rangeFilter.threshold", 0.03)
	
	// Create controller with settings
	e := echo.New()
	controller := &Controller{
		Echo:        e,
		Settings:    conf.Setting(),
		controlChan: make(chan string, 10),
	}
	
	// Update only coordinates
	update := map[string]interface{}{
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
	settings := conf.Setting()
	assert.InDelta(t, 51.5074, settings.BirdNET.Latitude, 0.0001) // Changed
	assert.InDelta(t, -0.1278, settings.BirdNET.Longitude, 0.0001) // Changed
	assert.InDelta(t, 1.0, settings.BirdNET.Sensitivity, 0.0001) // Preserved
	assert.InDelta(t, 0.8, settings.BirdNET.Threshold, 0.0001) // Preserved
	assert.Equal(t, "latest", settings.BirdNET.RangeFilter.Model) // Preserved
	assert.InDelta(t, float32(0.03), settings.BirdNET.RangeFilter.Threshold, 0.0001) // Preserved
}

// TestNestedRangeFilterUpdate verifies nested updates preserve parent fields
func TestNestedRangeFilterUpdate(t *testing.T) {
	// Setup viper with initial settings
	viper.Reset()
	viper.Set("birdnet.latitude", 40.7128)
	viper.Set("birdnet.longitude", -74.0060)
	viper.Set("birdnet.rangeFilter.model", "latest")
	viper.Set("birdnet.rangeFilter.threshold", 0.03)
	
	// Create controller with settings
	e := echo.New()
	controller := &Controller{
		Echo:        e,
		Settings:    conf.Setting(),
		controlChan: make(chan string, 10),
	}
	
	// Update only range filter threshold
	update := map[string]interface{}{
		"rangeFilter": map[string]interface{}{
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
	settings := conf.Setting()
	assert.InDelta(t, float32(0.05), settings.BirdNET.RangeFilter.Threshold, 0.0001) // Changed
	assert.InDelta(t, 40.7128, settings.BirdNET.Latitude, 0.0001) // Preserved
	assert.InDelta(t, -74.0060, settings.BirdNET.Longitude, 0.0001) // Preserved
	assert.Equal(t, "latest", settings.BirdNET.RangeFilter.Model) // Preserved
}

// TestAudioExportPartialUpdate verifies audio export settings preserve unmodified fields
func TestAudioExportPartialUpdate(t *testing.T) {
	// Setup viper with initial settings
	viper.Reset()
	viper.Set("realtime.audio.source", "default")
	viper.Set("realtime.audio.export.enabled", true)
	viper.Set("realtime.audio.export.type", "wav")
	viper.Set("realtime.audio.export.path", "/clips")
	viper.Set("realtime.audio.export.bitrate", "192k")
	
	// Create controller with settings
	e := echo.New()
	controller := &Controller{
		Echo:        e,
		Settings:    conf.Setting(),
		controlChan: make(chan string, 10),
	}
	
	// Update only export type
	update := map[string]interface{}{
		"export": map[string]interface{}{
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
	settings := conf.Setting()
	assert.Equal(t, "mp3", settings.Realtime.Audio.Export.Type) // Changed
	assert.True(t, settings.Realtime.Audio.Export.Enabled) // Preserved
	assert.Equal(t, "/clips", settings.Realtime.Audio.Export.Path) // Preserved
	assert.Equal(t, "192k", settings.Realtime.Audio.Export.Bitrate) // Preserved
}

// TestSpeciesConfigUpdate verifies complex nested species config updates
func TestSpeciesConfigUpdate(t *testing.T) {
	// Setup viper with initial settings
	viper.Reset()
	viper.Set("realtime.species.include", []string{"American Robin"})
	viper.Set("realtime.species.config.American Robin.threshold", 0.8)
	viper.Set("realtime.species.config.American Robin.interval", 30)
	viper.Set("realtime.species.config.American Robin.actions", []map[string]interface{}{
		{
			"type":            "ExecuteCommand",
			"command":         "/usr/bin/notify",
			"parameters":      []string{"--species", "American Robin"},
			"executeDefaults": true,
		},
	})
	
	// Create controller with settings
	e := echo.New()
	controller := &Controller{
		Echo:        e,
		Settings:    conf.Setting(),
		controlChan: make(chan string, 10),
	}
	
	// Update only threshold
	update := map[string]interface{}{
		"config": map[string]interface{}{
			"American Robin": map[string]interface{}{
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
	
	// Verify settings were preserved
	settings := conf.Setting()
	robinConfig := settings.Realtime.Species.Config["American Robin"]
	assert.InDelta(t, 0.9, robinConfig.Threshold, 0.0001) // Changed
	assert.Equal(t, 30, robinConfig.Interval) // Preserved
	assert.Len(t, robinConfig.Actions, 1) // Preserved
	if len(robinConfig.Actions) > 0 {
		assert.Equal(t, "ExecuteCommand", robinConfig.Actions[0].Type) // Preserved
		assert.Equal(t, "/usr/bin/notify", robinConfig.Actions[0].Command) // Preserved
		assert.Equal(t, []string{"--species", "American Robin"}, robinConfig.Actions[0].Parameters) // Preserved
		assert.True(t, robinConfig.Actions[0].ExecuteDefaults) // Preserved
	}
}

// TestEmptyUpdatePreservesEverything verifies empty updates don't change anything
func TestEmptyUpdatePreservesEverything(t *testing.T) {
	// Setup viper with initial settings
	viper.Reset()
	viper.Set("realtime.dashboard.thumbnails.summary", true)
	viper.Set("realtime.dashboard.thumbnails.recent", true)
	viper.Set("realtime.dashboard.thumbnails.imageProvider", "wikimedia")
	viper.Set("realtime.dashboard.summaryLimit", 100)
	
	// Get initial state
	initialSettings := conf.Setting()
	initialJSON, err := json.Marshal(initialSettings.Realtime.Dashboard)
	require.NoError(t, err)
	
	// Create controller with settings
	e := echo.New()
	controller := &Controller{
		Echo:        e,
		Settings:    conf.Setting(),
		controlChan: make(chan string, 10),
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
	updatedSettings := conf.Setting()
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
		update        interface{}
		expectedError string
		expectedCode  int
	}{
		{
			name:          "Reject security section updates",
			section:       "security",
			update:        map[string]interface{}{"basicAuth": map[string]interface{}{"enabled": true}},
			expectedError: "direct updates to security section are not supported",
			expectedCode:  http.StatusBadRequest,
		},
		{
			name:          "Reject main section updates",
			section:       "main",
			update:        map[string]interface{}{"name": "New Name"},
			expectedError: "main settings cannot be updated via API",
			expectedCode:  http.StatusBadRequest,
		},
		{
			name:          "Validate MQTT broker required when enabled",
			section:       "mqtt",
			update:        map[string]interface{}{"enabled": true, "broker": ""},
			expectedError: "broker is required when MQTT is enabled",
			expectedCode:  http.StatusBadRequest,
		},
		{
			name:          "Handle unknown section",
			section:       "nonexistent",
			update:        map[string]interface{}{"foo": "bar"},
			expectedError: "unknown settings section",
			expectedCode:  http.StatusBadRequest,
		},
		{
			name:          "Reject invalid JSON",
			section:       "dashboard",
			update:        "{invalid json",
			expectedError: "Invalid JSON in request body",
			expectedCode:  http.StatusBadRequest,
		},
	}
	
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			viper.Reset()
			
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
			require.Error(t, err)
			
			// Check the error response
			httpErr, ok := err.(*echo.HTTPError)
			require.True(t, ok)
			assert.Equal(t, tt.expectedCode, httpErr.Code)
			assert.Contains(t, httpErr.Message, tt.expectedError)
		})
	}
}

// TestDeepNestedUpdates verifies deeply nested object updates preserve all levels
func TestDeepNestedUpdates(t *testing.T) {
	t.Parallel()

	// Setup viper with deeply nested initial values
	viper.Reset()
	viper.Set("realtime.mqtt.retrySettings.enabled", true)
	viper.Set("realtime.mqtt.retrySettings.maxRetries", 3)
	viper.Set("realtime.mqtt.retrySettings.initialDelay", 10)
	viper.Set("realtime.mqtt.retrySettings.maxDelay", 300)
	viper.Set("realtime.mqtt.retrySettings.backoffMultiplier", 2.0)
	viper.Set("realtime.mqtt.tls.enabled", true)
	viper.Set("realtime.mqtt.tls.insecureSkipVerify", false)

	// Capture initial values
	initialSettings := conf.Setting()
	initialMaxRetries := initialSettings.Realtime.MQTT.RetrySettings.MaxRetries
	initialBackoff := initialSettings.Realtime.MQTT.RetrySettings.BackoffMultiplier
	initialTLSEnabled := initialSettings.Realtime.MQTT.TLS.Enabled

	e := echo.New()
	controller := &Controller{
		Echo:        e,
		Settings:    conf.Setting(),
		controlChan: make(chan string, 10),
	}

	// Update only one deeply nested field
	update := map[string]interface{}{
		"retrySettings": map[string]interface{}{
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
	settings := conf.Setting()
	assert.Equal(t, 30, settings.Realtime.MQTT.RetrySettings.InitialDelay) // Changed
	assert.Equal(t, initialMaxRetries, settings.Realtime.MQTT.RetrySettings.MaxRetries) // Preserved
	assert.Equal(t, initialBackoff, settings.Realtime.MQTT.RetrySettings.BackoffMultiplier) // Preserved
	assert.Equal(t, initialTLSEnabled, settings.Realtime.MQTT.TLS.Enabled) // Preserved
}