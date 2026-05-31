package conf

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAudioSettings_NeedsFfprobeWorkaround(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		audio          AudioSettings
		wantWorkaround bool
	}{
		{
			name: "FFmpeg 5.x needs workaround",
			audio: AudioSettings{
				FfmpegVersion: "5.1.7-0+deb12u1+rpt1",
				FfmpegMajor:   5,
				FfmpegMinor:   1,
			},
			wantWorkaround: true,
		},
		{
			name: "FFmpeg 7.x does not need workaround",
			audio: AudioSettings{
				FfmpegVersion: "7.1.2-0+deb13u1",
				FfmpegMajor:   7,
				FfmpegMinor:   1,
			},
			wantWorkaround: false,
		},
		{
			name: "FFmpeg 6.x does not need workaround",
			audio: AudioSettings{
				FfmpegVersion: "6.0",
				FfmpegMajor:   6,
				FfmpegMinor:   0,
			},
			wantWorkaround: false,
		},
		{
			name: "FFmpeg 4.x does not need workaround",
			audio: AudioSettings{
				FfmpegVersion: "4.4.2",
				FfmpegMajor:   4,
				FfmpegMinor:   4,
			},
			wantWorkaround: false,
		},
		{
			name: "Unknown version does not need workaround",
			audio: AudioSettings{
				FfmpegVersion: "",
				FfmpegMajor:   0,
				FfmpegMinor:   0,
			},
			wantWorkaround: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.audio.NeedsFfprobeWorkaround()
			assert.Equal(t, tt.wantWorkaround, got,
				"AudioSettings.NeedsFfprobeWorkaround() mismatch")
		})
	}
}

func TestAudioSettings_HasFfmpegVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		audio   AudioSettings
		wantHas bool
	}{
		{
			name: "Valid version detected",
			audio: AudioSettings{
				FfmpegVersion: "7.1.2",
				FfmpegMajor:   7,
				FfmpegMinor:   1,
			},
			wantHas: true,
		},
		{
			name: "No version detected",
			audio: AudioSettings{
				FfmpegVersion: "",
				FfmpegMajor:   0,
				FfmpegMinor:   0,
			},
			wantHas: false,
		},
		{
			name: "Version string but no major version",
			audio: AudioSettings{
				FfmpegVersion: "unknown",
				FfmpegMajor:   0,
				FfmpegMinor:   0,
			},
			wantHas: false,
		},
		{
			name: "Major version but no version string",
			audio: AudioSettings{
				FfmpegVersion: "",
				FfmpegMajor:   7,
				FfmpegMinor:   1,
			},
			wantHas: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.audio.HasFfmpegVersion()
			assert.Equal(t, tt.wantHas, got,
				"AudioSettings.HasFfmpegVersion() mismatch")
		})
	}
}

func TestBackwardCompatibility_RemovedBatFilterFields(t *testing.T) {
	t.Parallel()

	// YAML content containing the removed bat high-pass filter fields.
	yamlContent := `
bat:
  threshold: 0.75
  nighttimeonly: true
  filterenabled: true
  filtercutoffhz: 5000.0
  filterpasscount: 2
`

	v := viper.New()
	v.SetConfigType("yaml")

	err := v.ReadConfig(strings.NewReader(yamlContent))
	require.NoError(t, err)

	var settings Settings
	err = v.Unmarshal(&settings, viper.DecodeHook(DurationDecodeHook()))
	require.NoError(t, err)

	// Verify that the parser successfully ignored the removed filter fields,
	// but correctly loaded the other active BatConfig fields.
	assert.InDelta(t, 0.75, settings.Bat.Threshold, 1e-9)
	assert.True(t, settings.Bat.NighttimeOnly)
}
