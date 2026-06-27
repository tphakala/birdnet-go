// audio_devices_test.go: tests for the /system/audio/* device handlers that
// stay in package api until the audio/streaming domain is extracted.
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// setupSystemTestEnvironment creates a test environment for system API tests
func setupSystemTestEnvironment(t *testing.T) (*echo.Echo, *Controller) {
	t.Helper()

	e := echo.New()

	settings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				Source: "test-device",
			},
		},
	}

	controller := &Controller{Core: &apicore.Core{Echo: e, Group: e.Group("/api/v2")}}
	controller.Settings.Store(settings)

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

	t.Run("With configured device", func(t *testing.T) {
		e, controller := setupSystemTestEnvironment(t)
		// Settings already have a device configured

		req := httptest.NewRequest(http.MethodGet, "/api/v2/system/audio/active", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/system/audio/active")

		err := controller.GetActiveAudioDevice(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var response map[string]any
		err = json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Should have device info
		assert.Contains(t, response, "device", "Response should contain device")
		assert.Contains(t, response, "active", "Response should contain active flag")
	})

	t.Run("No device configured", func(t *testing.T) {
		e := echo.New()
		settings := &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Audio: conf.AudioSettings{
					Source: "", // No device configured
				},
			},
		}
		controller := &Controller{Core: &apicore.Core{Echo: e, Group: e.Group("/api/v2")}}
		controller.Settings.Store(settings)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/system/audio/active", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/system/audio/active")

		err := controller.GetActiveAudioDevice(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var response map[string]any
		err = json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		// Should indicate no device active
		active, ok := response["active"].(bool)
		require.True(t, ok, "response should have 'active' field as bool")
		assert.False(t, active, "Should not be active when no device configured")
		msg, ok := response["message"].(string)
		require.True(t, ok, "response should have 'message' field as string")
		assert.Contains(t, msg, "No audio device", "Should have appropriate message")
	})
}
