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
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
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
	controller := &Controller{Core: &apicore.Core{Echo: e}, controlChan: make(chan string, testControlChanBuffer), DisableSaveSettings: true}
	controller.Settings.Store(initial)

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
	eq := controller.Settings.Load().Realtime.Audio.Equalizer
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
// patchSettingsEQ patches the equalizer field at the given JSON path within a
// full settings payload. The path is a sequence of keys (and "[0]" for the first
// array element) leading to the target object whose "equalizer" field is set.
func patchSettingsEQ(t *testing.T, settings *conf.Settings, eqOverride map[string]any, path ...string) []byte {
	t.Helper()

	fullJSON, err := json.Marshal(settings)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(fullJSON, &payload))

	var current any = payload
	for _, key := range path {
		switch key {
		case "[0]":
			arr, ok := current.([]any)
			require.True(t, ok, "expected array at path segment [0]")
			require.NotEmpty(t, arr, "array must be non-empty")
			current = arr[0]
		default:
			m, ok := current.(map[string]any)
			require.True(t, ok, "expected object at path segment %q", key)
			current = m[key]
		}
	}
	target, ok := current.(map[string]any)
	require.True(t, ok, "final path target must be an object")
	target["equalizer"] = eqOverride

	body, err := json.Marshal(payload)
	require.NoError(t, err)
	return body
}

func putTestSettingsWithSourceEQ(t *testing.T, settings *conf.Settings, sourceEQ map[string]any) []byte {
	t.Helper()
	return patchSettingsEQ(t, settings, sourceEQ, "realtime", "audio", "sources", "[0]")
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
	controller := &Controller{Core: &apicore.Core{Echo: e}, controlChan: make(chan string, testControlChanBuffer), DisableSaveSettings: true}
	controller.Settings.Store(initial)

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
	require.Len(t, controller.Settings.Load().Realtime.Audio.Sources, 1)
	srcEQ := controller.Settings.Load().Realtime.Audio.Sources[0].Equalizer
	require.NotNil(t, srcEQ, "Per-source equalizer should not be nil")
	assert.True(t, srcEQ.Enabled, "Per-source equalizer should be enabled")
	require.Len(t, srcEQ.Filters, 1, "Per-source should have 1 filter")
	assert.Equal(t, "HighPass", srcEQ.Filters[0].Type)
	assert.InDelta(t, float64(500), srcEQ.Filters[0].Frequency, 0.01)
	assert.Equal(t, 2, srcEQ.Filters[0].Passes)

	// Global EQ should remain unchanged
	assert.False(t, controller.Settings.Load().Realtime.Audio.Equalizer.Enabled)
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
	controller := &Controller{Core: &apicore.Core{Echo: e}, controlChan: make(chan string, testControlChanBuffer), DisableSaveSettings: true}
	controller.Settings.Store(initial)

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

	eq := controller.Settings.Load().Realtime.Audio.Equalizer
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

// TestEQFilterRoundTrip_PUT_WithFrontendIDField verifies that the extra
// "id" field the frontend sends (not present in the Go struct) does not
// break deserialization of equalizer filters.
func putTestSettingsWithStreamEQ(t *testing.T, settings *conf.Settings, streamEQ map[string]any) []byte {
	t.Helper()
	return patchSettingsEQ(t, settings, streamEQ, "realtime", "rtsp", "streams", "[0]")
}

// TestEQFilterRoundTrip_PUT_PerStream verifies that per-stream equalizer
// filters survive the PUT path when attached to a StreamConfig.
func TestEQFilterRoundTrip_PUT_PerStream(t *testing.T) {
	t.Parallel()

	initial := getTestSettings(t)
	initial.Realtime.RTSP.Streams = []conf.StreamConfig{{
		Name:    "Test Stream",
		URL:     "rtsp://192.168.1.100/stream",
		Enabled: true,
		Type:    "rtsp",
	}}

	e := echo.New()
	controller := &Controller{Core: &apicore.Core{Echo: e}, controlChan: make(chan string, testControlChanBuffer), DisableSaveSettings: true}
	controller.Settings.Store(initial)

	streamEQ := map[string]any{
		"enabled": true,
		"filters": []map[string]any{
			{
				"type":      "HighPass",
				"frequency": 400,
				"q":         0.707,
				"gain":      0,
				"width":     0,
				"passes":    2,
			},
		},
	}

	body := putTestSettingsWithStreamEQ(t, initial, streamEQ)

	req := httptest.NewRequest(http.MethodPut, "/api/v2/settings", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.UpdateSettings(ctx)
	require.NoError(t, err)
	t.Logf("PUT response (status %d): %s", rec.Code, rec.Body.String())
	assert.Equal(t, http.StatusOK, rec.Code, "PUT should succeed")

	// Verify per-stream EQ survived the round-trip
	require.Len(t, controller.Settings.Load().Realtime.RTSP.Streams, 1)
	stEQ := controller.Settings.Load().Realtime.RTSP.Streams[0].Equalizer
	require.NotNil(t, stEQ, "Per-stream equalizer should not be nil")
	assert.True(t, stEQ.Enabled, "Per-stream equalizer should be enabled")
	require.Len(t, stEQ.Filters, 1, "Per-stream should have 1 filter")
	assert.Equal(t, "HighPass", stEQ.Filters[0].Type)
	assert.InDelta(t, float64(400), stEQ.Filters[0].Frequency, 0.01)
	assert.Equal(t, 2, stEQ.Filters[0].Passes)

	// Global EQ should remain unchanged
	assert.False(t, controller.Settings.Load().Realtime.Audio.Equalizer.Enabled)
}

func TestEQFilterRoundTrip_PUT_WithFrontendIDField(t *testing.T) {
	t.Parallel()

	initial := getTestSettings(t)
	e := echo.New()
	controller := &Controller{Core: &apicore.Core{Echo: e}, controlChan: make(chan string, testControlChanBuffer), DisableSaveSettings: true}
	controller.Settings.Store(initial)

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

	eq := controller.Settings.Load().Realtime.Audio.Equalizer
	assert.True(t, eq.Enabled)
	require.Len(t, eq.Filters, 1)
	assert.Equal(t, "HighPass", eq.Filters[0].Type)
	assert.InDelta(t, float64(300), eq.Filters[0].Frequency, 0.01)
}
