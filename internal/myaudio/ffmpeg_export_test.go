package myaudio

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestBuildAudioFilter(t *testing.T) {
	tests := []struct {
		name     string
		settings *conf.AudioSettings
		want     string
	}{
		{
			name: "no filter when gain is zero and normalization disabled",
			settings: &conf.AudioSettings{
				Export: conf.ExportSettings{
					Gain: 0,
					Normalization: conf.NormalizationSettings{
						Enabled: false,
					},
				},
			},
			want: "",
		},
		{
			name: "positive gain filter",
			settings: &conf.AudioSettings{
				Export: conf.ExportSettings{
					Gain: 6.5,
					Normalization: conf.NormalizationSettings{
						Enabled: false,
					},
				},
			},
			want: "volume=+6.5dB",
		},
		{
			name: "negative gain filter",
			settings: &conf.AudioSettings{
				Export: conf.ExportSettings{
					Gain: -3.0,
					Normalization: conf.NormalizationSettings{
						Enabled: false,
					},
				},
			},
			want: "volume=-3.0dB",
		},
		{
			name: "normalization filter",
			settings: &conf.AudioSettings{
				Export: conf.ExportSettings{
					Gain: 0,
					Normalization: conf.NormalizationSettings{
						Enabled:       true,
						TargetLUFS:    -23.0,
						TruePeak:      -2.0,
						LoudnessRange: 7.0,
					},
				},
			},
			want: "loudnorm=I=-23.0:TP=-2.0:LRA=7.0",
		},
		{
			name: "normalization takes precedence over gain",
			settings: &conf.AudioSettings{
				Export: conf.ExportSettings{
					Gain: 10.0, // This should be ignored
					Normalization: conf.NormalizationSettings{
						Enabled:       true,
						TargetLUFS:    -16.0,
						TruePeak:      -1.0,
						LoudnessRange: 5.0,
					},
				},
			},
			want: "loudnorm=I=-16.0:TP=-1.0:LRA=5.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildAudioFilter(tt.settings)
			assert.Equal(t, tt.want, got)

			// Additional edge case assertions
			switch {
			case tt.settings.Export.Normalization.Enabled:
				// When normalization is enabled, ensure NO volume filter is present
				assert.NotContains(t, got, "volume=",
					"Expected no 'volume=' when normalization is enabled")
				// Ensure loudnorm filter IS present
				assert.Contains(t, got, "loudnorm=",
					"Expected 'loudnorm=' when normalization is enabled")
			case tt.settings.Export.Gain != 0:
				// When only gain is set (normalization disabled), ensure NO loudnorm filter
				assert.NotContains(t, got, "loudnorm=",
					"Expected no 'loudnorm=' when only gain is set")
				// Ensure volume filter IS present
				assert.Contains(t, got, "volume=",
					"Expected 'volume=' when gain is non-zero")
			default:
				// When neither normalization nor gain is set, ensure NO filters
				assert.Empty(t, got,
					"Expected empty filter when no audio processing is needed")
			}
		})
	}
}

func TestBuildFFmpegArgs(t *testing.T) {
	tempFile := "/tmp/test.mp3"
	settings := &conf.AudioSettings{
		Export: conf.ExportSettings{
			Type:    "mp3",
			Bitrate: "128k",
			Gain:    3.0,
			Normalization: conf.NormalizationSettings{
				Enabled: false,
			},
		},
	}

	args := buildFFmpegArgs(tempFile, settings)

	// Verify -hide_banner is the first argument
	assert.NotEmpty(t, args, "args should not be empty")
	assert.Equal(t, "-hide_banner", args[0], "Expected -hide_banner as first argument")

	// Check that audio filter is included
	foundAF := false
	foundVolume := false
	for i, arg := range args {
		if arg == "-af" {
			foundAF = true
			if i+1 < len(args) && strings.Contains(args[i+1], "volume=+3.0dB") {
				foundVolume = true
			}
		}
	}

	assert.True(t, foundAF, "Expected -af flag in FFmpeg arguments")
	assert.True(t, foundVolume, "Expected volume filter in FFmpeg arguments")

	// Test with normalization enabled
	settings.Export.Normalization.Enabled = true
	settings.Export.Normalization.TargetLUFS = -23.0
	settings.Export.Normalization.TruePeak = -2.0
	settings.Export.Normalization.LoudnessRange = 7.0

	args = buildFFmpegArgs(tempFile, settings)

	foundLoudnorm := false
	for i, arg := range args {
		if arg == "-af" && i+1 < len(args) {
			if strings.Contains(args[i+1], "loudnorm=") {
				foundLoudnorm = true
			}
		}
	}

	assert.True(t, foundLoudnorm,
		"Expected loudnorm filter in FFmpeg arguments when normalization is enabled")

	// Test with no audio filters (gain = 0, normalization disabled)
	settings.Export.Gain = 0
	settings.Export.Normalization.Enabled = false

	args = buildFFmpegArgs(tempFile, settings)

	// Ensure -af flag is NOT present when no filters are needed
	hasAudioFilter := slices.Contains(args, "-af")
	assert.False(t, hasAudioFilter,
		"Expected no -af flag in FFmpeg arguments when no audio filters are needed")

	// Additional check: ensure neither volume nor loudnorm appears anywhere
	argsStr := strings.Join(args, " ")
	assert.NotContains(t, argsStr, "volume=",
		"Unexpected 'volume=' filter found when no filters should be present")
	assert.NotContains(t, argsStr, "loudnorm=",
		"Unexpected 'loudnorm=' filter found when no filters should be present")
}
