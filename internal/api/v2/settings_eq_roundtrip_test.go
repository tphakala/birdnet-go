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

// putTestSettingsJSON marshals a full Settings struct to JSON, then patches
// the equalizer section with the provided config. This mimics what the
// frontend does: send the complete settings object with modifications.
func putTestSettingsJSON(t *testing.T, settings *conf.Settings, eqOverride map[string]any) []byte {
	t.Helper()

	// Marshal the full settings to JSON (same as what GetAllSettings returns)
	fullJSON, err := json.Marshal(settings)
	require.NoError(t, err)

	// Unmarshal into a generic map so we can patch the equalizer
	var payload map[string]any
	require.NoError(t, json.Unmarshal(fullJSON, &payload))

	// Patch the audio equalizer
	realtime, ok := payload["realtime"].(map[string]any)
	require.True(t, ok, "payload must contain realtime section")
	audio, ok := realtime["audio"].(map[string]any)
	require.True(t, ok, "realtime must contain audio section")
	audio["equalizer"] = eqOverride

	body, err := json.Marshal(payload)
	require.NoError(t, err)
	return body
}

// TestEQFilterRoundTrip_PUT verifies that equalizer filters survive the full
// PUT /api/v2/settings path that the frontend save button uses. This tests
// the ctx.Bind -> updateAllowedFieldsRecursivelyWithTracking flow, not the
// PATCH mergeJSONIntoStruct flow.
func TestEQFilterRoundTrip_PUT(t *testing.T) {
	t.Parallel()

	initial := getTestSettings(t)
	// Start with no EQ filters
	initial.Realtime.Audio.Equalizer = conf.EqualizerSettings{
		Enabled: false,
		Filters: nil,
	}

	e := echo.New()
	controller := &Controller{
		Echo:                e,
		Settings:            initial,
		controlChan:         make(chan string, testControlChanBuffer),
		DisableSaveSettings: true,
	}

	eqConfig := map[string]any{
		"enabled": true,
		"filters": []map[string]any{
			{
				"type":      "HighPass",
				"frequency": 200,
				"q":         0.707,
				"gain":      0,
				"width":     0,
				"passes":    1,
			},
			{
				"type":      "LowPass",
				"frequency": 14000,
				"q":         0.707,
				"gain":      0,
				"width":     0,
				"passes":    2,
			},
		},
	}

	body := putTestSettingsJSON(t, initial, eqConfig)

	req := httptest.NewRequest(http.MethodPut, "/api/v2/settings", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.UpdateSettings(ctx)
	require.NoError(t, err)
	t.Logf("PUT response (status %d): %s", rec.Code, rec.Body.String())
	assert.Equal(t, http.StatusOK, rec.Code, "PUT should succeed")

	// Verify global EQ filters persisted
	eq := controller.Settings.Realtime.Audio.Equalizer
	assert.True(t, eq.Enabled, "Equalizer should be enabled")
	require.Len(t, eq.Filters, 2, "Should have 2 filters")
	assert.Equal(t, "HighPass", eq.Filters[0].Type)
	assert.InDelta(t, float64(200), eq.Filters[0].Frequency, 0.01)
	assert.InDelta(t, 0.707, eq.Filters[0].Q, 0.001)
	assert.Equal(t, 1, eq.Filters[0].Passes)
	assert.Equal(t, "LowPass", eq.Filters[1].Type)
	assert.InDelta(t, float64(14000), eq.Filters[1].Frequency, 0.01)
	assert.InDelta(t, 0.707, eq.Filters[1].Q, 0.001)
	assert.Equal(t, 2, eq.Filters[1].Passes)
}

// putTestSettingsWithSourceEQ marshals a full Settings struct to JSON, then
// patches the sources array to include per-source equalizer config.
func putTestSettingsWithSourceEQ(t *testing.T, settings *conf.Settings, sourceEQ map[string]any) []byte {
	t.Helper()

	fullJSON, err := json.Marshal(settings)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(fullJSON, &payload))

	realtime, ok := payload["realtime"].(map[string]any)
	require.True(t, ok, "payload must contain realtime section")
	audio, ok := realtime["audio"].(map[string]any)
	require.True(t, ok, "realtime must contain audio section")
	sources, ok := audio["sources"].([]any)
	require.True(t, ok, "audio must contain sources section")
	require.NotEmpty(t, sources, "sources must be non-empty")
	src, ok := sources[0].(map[string]any)
	require.True(t, ok, "sources[0] must be an object")
	src["equalizer"] = sourceEQ

	body, err := json.Marshal(payload)
	require.NoError(t, err)
	return body
}

// TestEQFilterRoundTrip_PUT_PerSource verifies that per-source equalizer
// filters survive the PUT path when attached to an AudioSourceConfig.
func TestEQFilterRoundTrip_PUT_PerSource(t *testing.T) {
	t.Parallel()

	initial := getTestSettings(t)
	initial.Realtime.Audio.Sources = []conf.AudioSourceConfig{{
		Name:   "Test Sound Card",
		Device: "default",
		Models: []string{"birdnet"},
	}}

	e := echo.New()
	controller := &Controller{
		Echo:                e,
		Settings:            initial,
		controlChan:         make(chan string, testControlChanBuffer),
		DisableSaveSettings: true,
	}

	sourceEQ := map[string]any{
		"enabled": true,
		"filters": []map[string]any{
			{
				"type":      "HighPass",
				"frequency": 500,
				"q":         0.707,
				"gain":      0,
				"width":     0,
				"passes":    2,
			},
		},
	}

	body := putTestSettingsWithSourceEQ(t, initial, sourceEQ)

	req := httptest.NewRequest(http.MethodPut, "/api/v2/settings", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.UpdateSettings(ctx)
	require.NoError(t, err)
	t.Logf("PUT response (status %d): %s", rec.Code, rec.Body.String())
	assert.Equal(t, http.StatusOK, rec.Code, "PUT should succeed")

	// Verify per-source EQ
	require.Len(t, controller.Settings.Realtime.Audio.Sources, 1)
	srcEQ := controller.Settings.Realtime.Audio.Sources[0].Equalizer
	require.NotNil(t, srcEQ, "Per-source equalizer should not be nil")
	assert.True(t, srcEQ.Enabled, "Per-source equalizer should be enabled")
	require.Len(t, srcEQ.Filters, 1, "Per-source should have 1 filter")
	assert.Equal(t, "HighPass", srcEQ.Filters[0].Type)
	assert.InDelta(t, float64(500), srcEQ.Filters[0].Frequency, 0.01)
	assert.Equal(t, 2, srcEQ.Filters[0].Passes)

	// Global EQ should remain unchanged
	assert.False(t, controller.Settings.Realtime.Audio.Equalizer.Enabled)
}

// TestEQFilterRoundTrip_PATCH verifies the PATCH path (section update) for
// comparison with the PUT path.
func TestEQFilterRoundTrip_PATCH(t *testing.T) {
	t.Parallel()

	initial := getTestSettings(t)
	initial.Realtime.Audio.Equalizer = conf.EqualizerSettings{
		Enabled: false,
		Filters: nil,
	}

	e := echo.New()
	controller := &Controller{
		Echo:                e,
		Settings:            initial,
		controlChan:         make(chan string, testControlChanBuffer),
		DisableSaveSettings: true,
	}

	payload := map[string]any{
		"audio": map[string]any{
			"equalizer": map[string]any{
				"enabled": true,
				"filters": []map[string]any{
					{
						"type":      "HighPass",
						"frequency": 200,
						"q":         0.707,
						"gain":      0,
						"width":     0,
						"passes":    1,
					},
					{
						"type":      "LowPass",
						"frequency": 14000,
						"q":         0.707,
						"gain":      0,
						"width":     0,
						"passes":    2,
					},
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/realtime", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues("realtime")

	err = controller.UpdateSectionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code, "PATCH should succeed")

	eq := controller.Settings.Realtime.Audio.Equalizer
	assert.True(t, eq.Enabled, "Equalizer should be enabled")
	require.Len(t, eq.Filters, 2, "Should have 2 filters")
	assert.Equal(t, "HighPass", eq.Filters[0].Type)
	assert.InDelta(t, float64(200), eq.Filters[0].Frequency, 0.01)
	assert.Equal(t, "LowPass", eq.Filters[1].Type)
	assert.InDelta(t, float64(14000), eq.Filters[1].Frequency, 0.01)
}

// TestEQFilterRoundTrip_PUT_WithFrontendIDField verifies that the extra
// "id" field the frontend sends (not present in the Go struct) does not
// break deserialization of equalizer filters.
func TestEQFilterRoundTrip_PUT_WithFrontendIDField(t *testing.T) {
	t.Parallel()

	initial := getTestSettings(t)
	e := echo.New()
	controller := &Controller{
		Echo:                e,
		Settings:            initial,
		controlChan:         make(chan string, testControlChanBuffer),
		DisableSaveSettings: true,
	}

	eqConfig := map[string]any{
		"enabled": true,
		"filters": []map[string]any{
			{
				"id":        "filter-1714567890-0",
				"type":      "HighPass",
				"frequency": 300,
				"q":         0.707,
				"gain":      0,
				"width":     0,
				"passes":    1,
			},
		},
	}

	body := putTestSettingsJSON(t, initial, eqConfig)

	req := httptest.NewRequest(http.MethodPut, "/api/v2/settings", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.UpdateSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	eq := controller.Settings.Realtime.Audio.Equalizer
	assert.True(t, eq.Enabled)
	require.Len(t, eq.Filters, 1)
	assert.Equal(t, "HighPass", eq.Filters[0].Type)
	assert.InDelta(t, float64(300), eq.Filters[0].Frequency, 0.01)
}
