package api

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// gainTestSettings returns settings with the given source and stream gains.
func gainTestSettings(sourceGains, streamGains []float64) *conf.Settings {
	s := &conf.Settings{}
	for _, g := range sourceGains {
		s.Realtime.Audio.Sources = append(s.Realtime.Audio.Sources, conf.AudioSourceConfig{Name: "Mic", Device: "hw:1,0", Gain: g})
	}
	for _, g := range streamGains {
		s.Realtime.RTSP.Streams = append(s.Realtime.RTSP.Streams, conf.StreamConfig{Name: "Cam", URL: "rtsp://cam/stream", Gain: g})
	}
	return s
}

// TestSourceGainsChanged covers sound card sources and streams: a changed gain
// fires, an unchanged one does not, and a length mismatch does not (the device
// and stream detectors already trigger a reconfigure for added or removed
// entries, and that reconfigure applies the gain).
func TestSourceGainsChanged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		old, cur     *conf.Settings
		wantAnyFired bool
	}{
		{"source gain changed", gainTestSettings([]float64{0}, nil), gainTestSettings([]float64{6}, nil), true},
		{"source gain unchanged", gainTestSettings([]float64{6}, nil), gainTestSettings([]float64{6}, nil), false},
		{"stream gain changed", gainTestSettings(nil, []float64{0}), gainTestSettings(nil, []float64{-6}), true},
		{"stream gain unchanged", gainTestSettings(nil, []float64{3}), gainTestSettings(nil, []float64{3}), false},
		{"second stream gain changed", gainTestSettings(nil, []float64{0, 0}), gainTestSettings(nil, []float64{0, 4}), true},
		{"source length mismatch", gainTestSettings([]float64{0}, nil), gainTestSettings([]float64{0, 6}, nil), false},
		{"stream length mismatch", gainTestSettings(nil, []float64{0, 6}), gainTestSettings(nil, []float64{0}), false},
		{"no sources", gainTestSettings(nil, nil), gainTestSettings(nil, nil), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.wantAnyFired, sourceGainsChanged(tc.old, tc.cur))
		})
	}
}

// TestAudioDeviceSettingChanged_GainOnlyIsNotADeviceChange pins that a gain edit
// no longer reconfigures the audio sources; update_source_gains applies it.
func TestAudioDeviceSettingChanged_GainOnlyIsNotADeviceChange(t *testing.T) {
	t.Parallel()
	assert.False(t, audioDeviceSettingChanged(gainTestSettings([]float64{0}, nil), gainTestSettings([]float64{6}, nil)),
		"a gain-only edit must not count as a device change")
}

// TestHandleAudioSettingsChanges_GainOnlyQueuesUpdateSourceGains pins the action
// a gain edit emits for a sound card and for a stream: update_source_gains and
// not reconfigure_audio_sources, which probes streams and rebuilds routes.
func TestHandleAudioSettingsChanges_GainOnlyQueuesUpdateSourceGains(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		old, cur *conf.Settings
	}{
		{"sound card", gainTestSettings([]float64{0}, nil), gainTestSettings([]float64{6}, nil)},
		{"stream", gainTestSettings(nil, []float64{0}), gainTestSettings(nil, []float64{6})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := getTestController(t, echo.New())
			actions, err := c.handleAudioSettingsChanges(tc.old, tc.cur)
			require.NoError(t, err)
			assert.Contains(t, actions, actionUpdateSourceGains)
			assert.NotContains(t, actions, actionReconfigureAudioSources)
		})
	}
}

// TestHandleAudioSettingsChanges_UnchangedGainQueuesNoGainAction pins that an
// unrelated edit does not emit the gain action.
func TestHandleAudioSettingsChanges_UnchangedGainQueuesNoGainAction(t *testing.T) {
	t.Parallel()

	c := getTestController(t, echo.New())
	s := gainTestSettings([]float64{6}, []float64{3})
	actions, err := c.handleAudioSettingsChanges(s, gainTestSettings([]float64{6}, []float64{3}))
	require.NoError(t, err)
	assert.NotContains(t, actions, actionUpdateSourceGains)
}
