package conf

import (
	"math"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateDashboardSettings_PlaybackGain pins the default playback gain
// range: finite values clamp into it, and a value that is not a finite number
// falls back to the safe default instead of a range edge, so a corrupt value
// never attenuates every clip.
func TestValidateDashboardSettings_PlaybackGain(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   float64
		want float64
	}{
		{"in range", 6.5, 6.5},
		{"negative in range", -6.5, -6.5},
		{"minimum kept", MinPlaybackGain, MinPlaybackGain},
		{"maximum kept", MaxPlaybackGain, MaxPlaybackGain},
		{"below minimum clamped", -20, MinPlaybackGain},
		{"above maximum clamped", 40, MaxPlaybackGain},
		{"NaN uses default", math.NaN(), DefaultPlaybackGain},
		{"positive infinity uses default", math.Inf(1), DefaultPlaybackGain},
		{"negative infinity uses default", math.Inf(-1), DefaultPlaybackGain},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dashboard := &Dashboard{DefaultAudioGain: tc.in, PlaybackDenoise: DenoiseSettings{Strength: DefaultDenoisePreset}}
			require.NoError(t, validateDashboardSettings(dashboard))
			assert.InDelta(t, tc.want, dashboard.DefaultAudioGain, 1e-9)
		})
	}
}

// TestValidateDashboardSettings_PlaybackHighPass pins the default high-pass
// cutoff: 0 is off, 1 to 19 Hz normalize to off (the client shows anything
// below the floor as off), 20 to the maximum are kept, and a larger value
// clamps to the maximum. A zero value Dashboard, as an old config or a test
// builds it, means off.
func TestValidateDashboardSettings_PlaybackHighPass(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   int
		want int
	}{
		{"zero is off", 0, PlaybackHighPassOffHz},
		{"negative is off", -5, PlaybackHighPassOffHz},
		{"1 Hz is off", 1, PlaybackHighPassOffHz},
		{"just below the lowest active cutoff is off", MinPlaybackHighPassHz - 1, PlaybackHighPassOffHz},
		{"lowest active cutoff kept", MinPlaybackHighPassHz, MinPlaybackHighPassHz},
		{"in range", 300, 300},
		{"maximum kept", MaxPlaybackHighPassHz, MaxPlaybackHighPassHz},
		{"above maximum clamped", MaxPlaybackHighPassHz + 1, MaxPlaybackHighPassHz},
		{"far above maximum clamped", 100000, MaxPlaybackHighPassHz},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dashboard := &Dashboard{Playback: PlaybackDefaults{HighPassHz: tc.in}, PlaybackDenoise: DenoiseSettings{Strength: DefaultDenoisePreset}}
			require.NoError(t, validateDashboardSettings(dashboard))
			assert.Equal(t, tc.want, dashboard.Playback.HighPassHz)
		})
	}
}

// TestPlaybackHighPassViperDefault verifies an old config without the key
// loads with the filter off.
func TestPlaybackHighPassViperDefault(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	setDefaultConfig()
	require.True(t, viper.IsSet("realtime.dashboard.playback.highpasshz"), "the default must be registered")
	assert.Equal(t, PlaybackHighPassOffHz, viper.GetInt("realtime.dashboard.playback.highpasshz"))
}
