package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateDashboardSettings_PlaybackDenoiseStrength verifies the playback
// denoise strength heals an empty value and rejects an unknown preset, whether
// or not the setting is enabled.
func TestValidateDashboardSettings_PlaybackDenoiseStrength(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		enabled bool
		in      string
		wantErr bool
		want    string
	}{
		{"empty heals when on", true, "", false, DefaultDenoisePreset},
		{"empty heals when off", false, "", false, DefaultDenoisePreset},
		{"light", true, DenoisePresetLight, false, DenoisePresetLight},
		{"heavy when off", false, DenoisePresetHeavy, false, DenoisePresetHeavy},
		{"unknown rejected when on", true, "extreme", true, ""},
		{"unknown rejected when off", false, "extreme", true, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dashboard := &Dashboard{
				DefaultAudioGain: 0,
				PlaybackDenoise:  DenoiseSettings{Enabled: tc.enabled, Strength: tc.in},
			}
			err := validateDashboardSettings(dashboard)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, dashboard.PlaybackDenoise.Strength)
			assert.Equal(t, tc.enabled, dashboard.PlaybackDenoise.Enabled)
		})
	}
}

// TestEffectivePlaybackDenoise pins the playback default's precedence: it is
// the configured strength only while the playback setting is on and export-stage
// denoise is off.
func TestEffectivePlaybackDenoise(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		playbackOn     bool
		playbackLevel  string
		exportDenoised bool
		want           string
	}{
		{"off", false, DenoisePresetLightMedium, false, ""},
		{"on", true, DenoisePresetMedium, false, DenoisePresetMedium},
		{"export denoise takes precedence", true, DenoisePresetMedium, true, ""},
		{"export denoise alone", false, DenoisePresetMedium, true, ""},
		{"unknown strength gives none", true, "extreme", false, ""},
		{"empty strength gives none", true, "", false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &Settings{}
			s.Realtime.Dashboard.PlaybackDenoise = DenoiseSettings{Enabled: tc.playbackOn, Strength: tc.playbackLevel}
			s.Realtime.Audio.Export.Denoise.Enabled = tc.exportDenoised
			assert.Equal(t, tc.want, s.EffectivePlaybackDenoise())
		})
	}
}
