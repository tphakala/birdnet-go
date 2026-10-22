// audio_devices_test.go: tests for the /system/audio/* device handlers that
// stay in package api until the audio/streaming domain is extracted.
package audio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// setupSystemTestEnvironment creates a test environment for system API tests
func setupSystemTestEnvironment(t *testing.T) (*echo.Echo, *Handler) {
	t.Helper()

	e := echo.New()

	settings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				Sources: []conf.AudioSourceConfig{{Device: "test-device"}},
			},
		},
	}

	controller := &Handler{Core: &apicore.Core{Echo: e, Group: e.Group("/api/v2")}}
	controller.Settings.Store(settings)
	// No test may reach real audio hardware through this fixture; tests that
	// need devices replace the lister.
	controller.listCaptureDevices = func() ([]audiocore.DeviceInfo, error) { return nil, nil }

	return e, controller
}

// TestGetEqualizerConfig tests the GetEqualizerConfig endpoint
func TestGetEqualizerConfig(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "integration")
	t.Attr("feature", "equalizer-config")

	e, controller := setupSystemTestEnvironment(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/audio/equalizer/config", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/system/audio/equalizer/config")

	err := controller.GetEqualizerConfig(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify cache headers are set
	assert.Contains(t, rec.Header().Get("Cache-Control"), "public", "Should have public cache header")

	// Response should be valid JSON
	var response any
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err, "Response should be valid JSON")
}

// TestGetActiveAudioDevice tests the GetActiveAudioDevice endpoint
func TestGetActiveAudioDevice(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "integration")
	t.Attr("feature", "audio-device")

	const (
		configuredName = "test-device"
		configuredID   = ":1,0"
	)

	tests := []struct {
		name         string
		sources      []conf.AudioSourceConfig
		devices      []audiocore.DeviceInfo
		wantActive   bool
		wantVerified bool
		wantMessage  string
		wantName     string
		wantID       string
	}{
		{
			name:    "configured device listed by name",
			sources: []conf.AudioSourceConfig{{Device: configuredName}},
			devices: []audiocore.DeviceInfo{
				{Name: "other-device", ID: ":0,0"},
				{Name: configuredName, ID: configuredID},
			},
			wantActive:   true,
			wantVerified: true,
			wantName:     configuredName,
			wantID:       configuredID,
		},
		{
			name:    "configured device not listed",
			sources: []conf.AudioSourceConfig{{Device: configuredName}},
			devices: []audiocore.DeviceInfo{
				{Name: "other-device", ID: ":0,0"},
			},
			wantActive:   true,
			wantVerified: false,
			wantMessage:  "Configured audio device not found on the system",
			wantName:     configuredName,
		},
		{
			name:        "no device configured",
			sources:     nil,
			wantMessage: "No audio device currently active",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e, controller := setupSystemTestEnvironment(t)
			settings := controller.Settings.Load()
			settings.Realtime.Audio.Sources = tt.sources
			controller.listCaptureDevices = func() ([]audiocore.DeviceInfo, error) { return tt.devices, nil }

			req := httptest.NewRequest(http.MethodGet, "/api/v2/system/audio/active", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath("/api/v2/system/audio/active")

			err := controller.GetActiveAudioDevice(c)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, rec.Code)

			var response struct {
				Device   *ActiveAudioDevice `json:"device"`
				Active   bool               `json:"active"`
				Verified bool               `json:"verified"`
				Message  string             `json:"message"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))

			assert.Equal(t, tt.wantActive, response.Active)
			assert.Equal(t, tt.wantVerified, response.Verified)
			assert.Equal(t, tt.wantMessage, response.Message)
			if tt.wantName == "" {
				assert.Nil(t, response.Device, "no device is reported when none is configured")
				return
			}
			require.NotNil(t, response.Device)
			assert.Equal(t, tt.wantName, response.Device.Name)
			assert.Equal(t, tt.wantID, response.Device.ID)
		})
	}
}
