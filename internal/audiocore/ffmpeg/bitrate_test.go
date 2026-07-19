package ffmpeg

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// EffectiveBitrateKbps is what the native AAC and Opus encoders code at, so it
// has to agree with the string the FFmpeg command line receives for the same
// settings. These cases pin that agreement.
func TestEffectiveBitrateKbps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		format  string
		bitrate string
		want    int
	}{
		{name: "opus default", format: FormatOpus, bitrate: "64k", want: 64},
		{name: "aac default", format: FormatAAC, bitrate: "96k", want: 96},
		{name: "plain digits without suffix", format: FormatAAC, bitrate: "128000", want: 128000},
		{name: "uppercase suffix", format: FormatAAC, bitrate: "128K", want: 128},
		{name: "opus clamped to its ceiling", format: FormatOpus, bitrate: "320k", want: maxBitrateOpusKbps},
		{name: "opus at its ceiling is unchanged", format: FormatOpus, bitrate: "256k", want: maxBitrateOpusKbps},
		{name: "mp3 clamped to its ceiling", format: FormatMP3, bitrate: "512k", want: maxBitrateMP3Kbps},
		{name: "aac is not clamped here", format: FormatAAC, bitrate: "320k", want: 320},
		// An unparseable or empty setting yields 0, which every encoder reads as
		// "use the codec default" rather than as silence.
		{name: "empty string falls back to the codec default", format: FormatAAC, bitrate: "", want: 0},
		{name: "garbage falls back to the codec default", format: FormatAAC, bitrate: "high", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, EffectiveBitrateKbps(tt.format, tt.bitrate))
		})
	}
}

// The numeric helper must agree with the string the FFmpeg argument builder
// uses, otherwise a clip would code at a different rate depending on which
// encoder ran.
func TestEffectiveBitrateKbps_MatchesFFmpegArgument(t *testing.T) {
	t.Parallel()

	for _, format := range []string{FormatAAC, FormatOpus, FormatMP3} {
		for _, bitrate := range []string{"32k", "64k", "96k", "192k", "320k", "512k"} {
			assert.Equal(t, parseBitrateKbps(getMaxBitrate(format, bitrate)),
				EffectiveBitrateKbps(format, bitrate),
				"format %s bitrate %s", format, bitrate)
		}
	}
}
