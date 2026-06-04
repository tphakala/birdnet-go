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

// TestDashboardLayoutWidthPersistence verifies that changing element width from
// "half" to "full" (omitted in JSON) correctly clears the old width value.
// Regression test for: json.Unmarshal reuses existing slice elements, so
// omitted fields (like width via omitempty) retained their old values.
func TestDashboardLayoutWidthPersistence(t *testing.T) {
	initialSettings := getTestSettings(t)
	initialSettings.Realtime.Dashboard.Layout = conf.DashboardLayout{
		Elements: []conf.DashboardElement{
			{ID: "daily-summary-0", Type: "daily-summary", Enabled: true},
			{ID: "currently-hearing-0", Type: "currently-hearing", Enabled: true, Width: "half"},
			{ID: "detections-grid-0", Type: "detections-grid", Enabled: true, Width: "half"},
		},
	}

	e := echo.New()
	controller := &Controller{
		Echo:                e,
		Settings:            initialSettings,
		controlChan:         make(chan string, 10),
		DisableSaveSettings: true,
	}

	// Simulate the frontend save: elements without width field (user set to full)
	update := map[string]any{
		"layout": map[string]any{
			"elements": []map[string]any{
				{"id": "daily-summary-0", "type": "daily-summary", "enabled": true},
				{"id": "currently-hearing-0", "type": "currently-hearing", "enabled": true},
				{"id": "detections-grid-0", "type": "detections-grid", "enabled": true},
			},
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

	err = controller.UpdateSectionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// The width fields must be cleared (empty string = full width default)
	elements := controller.Settings.Realtime.Dashboard.Layout.Elements
	require.Len(t, elements, 3)
	assert.Empty(t, elements[0].Width, "daily-summary width should be empty")
	assert.Empty(t, elements[1].Width, "currently-hearing width should be empty (was 'half')")
	assert.Empty(t, elements[2].Width, "detections-grid width should be empty (was 'half')")

	// Also verify: sending explicit "full" works the same way
	update2 := map[string]any{
		"layout": map[string]any{
			"elements": []map[string]any{
				{"id": "daily-summary-0", "type": "daily-summary", "enabled": true},
				{"id": "currently-hearing-0", "type": "currently-hearing", "enabled": true, "width": "full"},
				{"id": "detections-grid-0", "type": "detections-grid", "enabled": true, "width": "half"},
			},
		},
	}

	body2, err := json.Marshal(update2)
	require.NoError(t, err)

	req2 := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/dashboard", bytes.NewReader(body2))
	req2.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec2 := httptest.NewRecorder()
	ctx2 := e.NewContext(req2, rec2)
	ctx2.SetParamNames("section")
	ctx2.SetParamValues("dashboard")

	err = controller.UpdateSectionSettings(ctx2)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec2.Code)

	elements2 := controller.Settings.Realtime.Dashboard.Layout.Elements
	require.Len(t, elements2, 3)
	assert.Equal(t, "full", elements2[1].Width, "currently-hearing should have explicit 'full'")
	assert.Equal(t, "half", elements2[2].Width, "detections-grid should have 'half'")
}

// TestMergePreservesJSONDashFields verifies that mergeJSONIntoStruct preserves
// fields tagged json:"-" (runtime values invisible to JSON) when zeroing slices.
func TestMergePreservesJSONDashFields(t *testing.T) {
	initialSettings := getTestSettings(t)
	// Set runtime-only field (json:"-") that must survive a merge
	initialSettings.BirdNET.Labels = []string{"species1", "species2"}

	e := echo.New()
	controller := &Controller{
		Echo:                e,
		Settings:            initialSettings,
		controlChan:         make(chan string, 10),
		DisableSaveSettings: true,
	}

	// PATCH birdnet section — Labels (json:"-") must survive
	update := map[string]any{"sensitivity": 1.0}
	body, err := json.Marshal(update)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/birdnet", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues("birdnet")

	err = controller.UpdateSectionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Runtime field must be preserved — Labels is json:"-" and would be
	// destroyed if zeroJSONSliceFields zeroed json:"-" tagged slices.
	assert.Equal(t, []string{"species1", "species2"}, controller.Settings.BirdNET.Labels,
		"BirdNET.Labels (json:\"-\") must survive merge")
}

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
	assert.Equal(t, "clips", settings.Realtime.Audio.Export.Path)   // Preserved
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

// TestStreamsSettingsChanged_DetectsModelEdits verifies that per-stream model
// list changes trigger reconfigure_rtsp_sources. Without this, a user who
// adds a classifier to a stream (e.g., enables Perch v2 alongside BirdNET)
// would see the save persist to disk but the running pipeline keep using
// the old model set — silently breaking the hot-reload contract.
func TestStreamsSettingsChanged_DetectsModelEdits(t *testing.T) {
	t.Parallel()

	makeSettings := func(models []string) *conf.Settings {
		s := &conf.Settings{}
		s.Realtime.RTSP.Streams = []conf.StreamConfig{
			{
				Name:      "Front Yard",
				URL:       "rtsp://192.168.1.10/stream",
				Type:      conf.StreamTypeRTSP,
				Transport: "tcp",
				Enabled:   true,
				Models:    models,
			},
		}
		return s
	}

	tests := []struct {
		name string
		old  []string
		new  []string
		want bool
	}{
		{"identical model list", []string{"birdnet"}, []string{"birdnet"}, false},
		{"model added", []string{"birdnet"}, []string{"birdnet", "perch_v2"}, true},
		{"model removed", []string{"birdnet", "perch_v2"}, []string{"birdnet"}, true},
		{"model reordered", []string{"birdnet", "perch_v2"}, []string{"perch_v2", "birdnet"}, true},
		{"replaced entirely", []string{"birdnet"}, []string{"perch_v2"}, true},
		{"both empty", nil, nil, false},
		{"nil vs empty slice", nil, []string{}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			old := makeSettings(tc.old)
			cur := makeSettings(tc.new)
			assert.Equal(t, tc.want, streamsSettingsChanged(old, cur))
		})
	}
}

// TestAudioDeviceSettingChanged_DetectsModelsEdits verifies the analogous fix
// for sound-card audio sources: editing AudioSourceConfig.Models must trigger
// reconfigure_audio_sources. Pre-fix, only the deprecated singular Model
// string was compared, so adding a classifier via the new list field would
// silently no-op until restart.
func TestAudioDeviceSettingChanged_DetectsModelsEdits(t *testing.T) {
	t.Parallel()

	makeSettings := func(models []string) *conf.Settings {
		s := &conf.Settings{}
		s.Realtime.Audio.Sources = []conf.AudioSourceConfig{{
			Name:   "Garden Mic",
			Device: "hw:1,0",
			Gain:   0,
			Models: models,
		}}
		return s
	}

	tests := []struct {
		name string
		old  []string
		new  []string
		want bool
	}{
		{"identical model list", []string{"birdnet"}, []string{"birdnet"}, false},
		{"model added", []string{"birdnet"}, []string{"birdnet", "perch_v2"}, true},
		{"model removed", []string{"birdnet", "perch_v2"}, []string{"birdnet"}, true},
		{"model reordered", []string{"birdnet", "perch_v2"}, []string{"perch_v2", "birdnet"}, true},
		{"replaced entirely", []string{"birdnet"}, []string{"perch_v2"}, true},
		{"both empty", nil, nil, false},
		{"nil vs empty slice", nil, []string{}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			old := makeSettings(tc.old)
			cur := makeSettings(tc.new)
			assert.Equal(t, tc.want, audioDeviceSettingChanged(old, cur))
		})
	}
}

// TestStreamsSettingsChanged_BackwardCompatibility verifies that the existing
// fields (Name, URL, Enabled, Type, Transport) still trigger the change
// detector — the Models check is additive and must not regress prior coverage.
func TestStreamsSettingsChanged_BackwardCompatibility(t *testing.T) {
	t.Parallel()

	base := func() *conf.Settings {
		s := &conf.Settings{}
		s.Realtime.RTSP.Streams = []conf.StreamConfig{{
			Name:      "Front Yard",
			URL:       "rtsp://192.168.1.10/stream",
			Type:      conf.StreamTypeRTSP,
			Transport: "tcp",
			Enabled:   true,
			Models:    []string{"birdnet"},
		}}
		return s
	}

	tests := []struct {
		name   string
		mutate func(*conf.Settings)
		want   bool
	}{
		{"no change", func(*conf.Settings) {}, false},
		{"name changed", func(s *conf.Settings) { s.Realtime.RTSP.Streams[0].Name = "Back Yard" }, true},
		{"url changed", func(s *conf.Settings) { s.Realtime.RTSP.Streams[0].URL = "rtsp://192.168.1.11/stream" }, true},
		{"transport changed", func(s *conf.Settings) { s.Realtime.RTSP.Streams[0].Transport = "udp" }, true},
		{"disabled", func(s *conf.Settings) { s.Realtime.RTSP.Streams[0].Enabled = false }, true},
		{"stream added", func(s *conf.Settings) {
			s.Realtime.RTSP.Streams = append(s.Realtime.RTSP.Streams, conf.StreamConfig{
				Name: "Garden", URL: "rtsp://192.168.1.12/stream", Type: conf.StreamTypeRTSP, Transport: "tcp",
			})
		}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			old := base()
			cur := base()
			tc.mutate(cur)
			assert.Equal(t, tc.want, streamsSettingsChanged(old, cur))
		})
	}
}

// TestStreamsSettingsChanged_ChannelMode verifies that a real channel-mode edit
// triggers reconfiguration while a no-op transition between the empty default and
// its canonical form ("" <-> "downmix") does NOT. An unset mode and an explicit
// "downmix" produce identical FFmpeg arguments, so treating them as a change would
// restart the stream (a brief audio drop) for no functional reason. The frontend
// now sends an explicit "downmix" where it previously sent "", which is exactly
// when this no-op transition occurs on the first save.
func TestStreamsSettingsChanged_ChannelMode(t *testing.T) {
	t.Parallel()

	makeSettings := func(mode conf.ChannelMode) *conf.Settings {
		s := &conf.Settings{}
		s.Realtime.RTSP.Streams = []conf.StreamConfig{{
			Name:        "Front Yard",
			URL:         "rtsp://192.168.1.10/stream",
			Type:        conf.StreamTypeRTSP,
			Transport:   "tcp",
			Enabled:     true,
			ChannelMode: mode,
			Models:      []string{"birdnet"},
		}}
		return s
	}

	tests := []struct {
		name string
		old  conf.ChannelMode
		new  conf.ChannelMode
		want bool
	}{
		{"unset to explicit downmix is a no-op", "", conf.ChannelModeDownmix, false},
		{"explicit downmix to unset is a no-op", conf.ChannelModeDownmix, "", false},
		{"identical left", conf.ChannelModeLeft, conf.ChannelModeLeft, false},
		{"unset to left changes", "", conf.ChannelModeLeft, true},
		{"downmix to left changes", conf.ChannelModeDownmix, conf.ChannelModeLeft, true},
		{"left to right changes", conf.ChannelModeLeft, conf.ChannelModeRight, true},
		{"left to downmix changes", conf.ChannelModeLeft, conf.ChannelModeDownmix, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			old := makeSettings(tc.old)
			cur := makeSettings(tc.new)
			assert.Equal(t, tc.want, streamsSettingsChanged(old, cur))
		})
	}
}
