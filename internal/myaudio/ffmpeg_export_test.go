package myaudio

import (
	"strings"
	"testing"

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
			if got != tt.want {
				t.Errorf("buildAudioFilter() = %v, want %v", got, tt.want)
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

	if !foundAF {
		t.Error("Expected -af flag in FFmpeg arguments")
	}
	if !foundVolume {
		t.Error("Expected volume filter in FFmpeg arguments")
	}

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

	if !foundLoudnorm {
		t.Error("Expected loudnorm filter in FFmpeg arguments when normalization is enabled")
	}
}
