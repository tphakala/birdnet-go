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
		{"minimum kept", -12, -12},
		{"maximum kept", 24, 24},
		{"just below minimum clamped", -12.5, -12},
		{"below minimum clamped", -20, -12},
		{"just above maximum clamped", 24.5, 24},
		{"above maximum clamped", 40, 24},
		{"NaN uses default", math.NaN(), 0},
		{"positive infinity uses default", math.Inf(1), 0},
		{"negative infinity uses default", math.Inf(-1), 0},
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
		{"zero is off", 0, 0},
		{"negative is off", -5, 0},
		{"1 Hz is off", 1, 0},
		{"19 Hz is off", 19, 0},
		{"lowest active cutoff kept", 20, 20},
		{"in range", 300, 300},
		{"maximum kept", 5000, 5000},
		{"above maximum clamped", 5001, 5000},
		{"far above maximum clamped", 100000, 5000},
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
	assert.Equal(t, 0, viper.GetInt("realtime.dashboard.playback.highpasshz"))
}
