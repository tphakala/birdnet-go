package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

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

// eqLocation names the three places an equalizer lives in the settings.
type eqLocation string

const (
	eqGlobal eqLocation = "global"
	eqSource eqLocation = "Sources[0]"
	eqStream eqLocation = "Streams[0]"
)

// Fixture identity shared by the seed and the PATCH bodies below. A PATCH
// replaces the sources and streams arrays as a whole, so its elements must
// repeat these values.
const (
	eqTestSourceName   = "Test Sound Card"
	eqTestSourceDevice = "default"
	eqTestStreamName   = "Test Stream"
	eqTestStreamURL    = "rtsp://192.168.1.100/stream"
	eqTestQ            = 0.707
)

// eqPutPaths is where a PUT payload carries each equalizer, as a JSON path for
// patchSettingsEQ.
var eqPutPaths = map[eqLocation][]string{
	eqGlobal: {"realtime", "audio"},
	eqSource: {"realtime", "audio", "sources", "[0]"},
	eqStream: {"realtime", "rtsp", "streams", "[0]"},
}

// eqFilterSpec is one filter a round-trip row sends and expects back.
type eqFilterSpec struct {
	filterType string
	frequency  float64
	passes     int
}

// eqSettingsAt returns the equalizer at loc, failing the test when the
// location holds none.
func eqSettingsAt(t *testing.T, s *conf.Settings, loc eqLocation) conf.EqualizerSettings {
	t.Helper()
	switch loc {
	case eqSource:
		require.Len(t, s.Realtime.Audio.Sources, 1)
		require.NotNil(t, s.Realtime.Audio.Sources[0].Equalizer, "per-source equalizer should not be nil")
		return *s.Realtime.Audio.Sources[0].Equalizer
	case eqStream:
		require.Len(t, s.Realtime.RTSP.Streams, 1)
		require.NotNil(t, s.Realtime.RTSP.Streams[0].Equalizer, "per-stream equalizer should not be nil")
		return *s.Realtime.RTSP.Streams[0].Equalizer
	case eqGlobal:
		return s.Realtime.Audio.Equalizer
	}
	require.FailNow(t, "unknown equalizer location", "%q", loc)
	return conf.EqualizerSettings{}
}

// TestEQFilterRoundTrip verifies that equalizer filters survive both save paths
// the frontend uses, a full PUT /api/v2/settings and a section PATCH, at each
// place an equalizer lives: the global one, a sound card source and a stream.
// PUT runs the same omission-preserving deep merge as PATCH (mergeFullSettings).
// The last row sends the extra "id" field the frontend adds to each filter, which
// is not in the Go struct and must not break decoding.
//
// Not parallel: both paths reach process-global state (see the note in
// settings_blocked_fields_test.go).
func TestEQFilterRoundTrip(t *testing.T) {
	highPassLowPass := []eqFilterSpec{
		{filterType: "HighPass", frequency: 200, passes: 1},
		{filterType: "LowPass", frequency: 14000, passes: 2},
	}
	highPass := []eqFilterSpec{{filterType: "HighPass", frequency: 500, passes: 2}}

	tests := []struct {
		name       string
		location   eqLocation
		method     string
		filters    []eqFilterSpec
		frontendID bool
	}{
		{name: "global via PUT", location: eqGlobal, method: http.MethodPut, filters: highPassLowPass},
		{name: "global via PATCH", location: eqGlobal, method: http.MethodPatch, filters: highPassLowPass},
		{name: "source via PUT", location: eqSource, method: http.MethodPut, filters: highPass},
		{name: "source via PATCH", location: eqSource, method: http.MethodPatch, filters: highPass},
		{name: "stream via PUT", location: eqStream, method: http.MethodPut, filters: highPass},
		{name: "stream via PATCH", location: eqStream, method: http.MethodPatch, filters: highPass},
		{name: "global via PUT with the frontend id field", location: eqGlobal, method: http.MethodPut, filters: highPass, frontendID: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			controller := newMergeController(t, e, func(s *conf.Settings) {
				s.Realtime.Audio.Equalizer = conf.EqualizerSettings{}
				s.Realtime.Audio.Sources = []conf.AudioSourceConfig{{
					Name:   eqTestSourceName,
					Device: eqTestSourceDevice,
					Models: []string{"birdnet"},
				}}
				s.Realtime.RTSP.Streams = []conf.StreamConfig{{
					Name:    eqTestStreamName,
					URL:     eqTestStreamURL,
					Enabled: true,
					Type:    "rtsp",
				}}
			})

			filters := make([]map[string]any, 0, len(tt.filters))
			for i, f := range tt.filters {
				filter := map[string]any{
					"type":      f.filterType,
					"frequency": f.frequency,
					"q":         eqTestQ,
					"gain":      0,
					"width":     0,
					"passes":    f.passes,
				}
				if tt.frontendID {
					filter["id"] = fmt.Sprintf("filter-1714567890-%d", i)
				}
				filters = append(filters, filter)
			}
			eq := map[string]any{"enabled": true, "filters": filters}

			if tt.method == http.MethodPut {
				body := patchSettingsEQ(t, controller.Settings.Load(), eq, eqPutPaths[tt.location]...)
				putRawSettings(t, e, controller, string(body))
			} else {
				switch tt.location {
				case eqGlobal:
					patchSection(t, e, controller, "realtime", map[string]any{"audio": map[string]any{"equalizer": eq}})
				case eqSource:
					patchSection(t, e, controller, "audio", map[string]any{"sources": []map[string]any{{
						"name": eqTestSourceName, "device": eqTestSourceDevice, "models": []string{"birdnet"}, "equalizer": eq,
					}}})
				case eqStream:
					patchSection(t, e, controller, "rtsp", map[string]any{"streams": []map[string]any{{
						"name": eqTestStreamName, "url": eqTestStreamURL, "enabled": true, "type": "rtsp", "equalizer": eq,
					}}})
				}
			}

			saved := controller.Settings.Load()
			got := eqSettingsAt(t, saved, tt.location)
			assert.True(t, got.Enabled, "equalizer should be enabled")
			require.Len(t, got.Filters, len(tt.filters))
			for i, want := range tt.filters {
				assert.Equal(t, want.filterType, got.Filters[i].Type)
				assert.InDelta(t, want.frequency, got.Filters[i].Frequency, 0.01)
				assert.InDelta(t, eqTestQ, got.Filters[i].Q, 0.001)
				assert.Equal(t, want.passes, got.Filters[i].Passes)
			}
			if tt.location != eqGlobal {
				assert.False(t, saved.Realtime.Audio.Equalizer.Enabled, "the global equalizer must stay as it was")
			}
		})
	}
}
