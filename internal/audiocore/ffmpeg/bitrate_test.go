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
		// A bare integer is out of contract (conf requires the "NNNk" form). The
		// helper reads it as kbit/s while FFmpeg's -b:a would read bit/s; this
		// pins the current behaviour so the divergence is visible if the
		// validator is ever relaxed. See EffectiveBitrateKbps's precondition.
		{name: "bare integer is read as kbps, outside the validated contract", format: FormatAAC, bitrate: "128000", want: 128000},
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

// The numeric helper and the FFmpeg argument builder must resolve the same
// configured string to the same rate, or a clip would code differently depending
// on which encoder ran. Asserting against getMaxBitrate would be circular (it is
// EffectiveBitrateKbps's own body), so this pins the FFmpeg-side string and the
// native-side integer independently.
func TestEffectiveBitrateKbps_AgreesWithFFmpegArgument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		format      string
		bitrate     string
		wantArg     string // what the FFmpeg command line receives
		wantNumeric int    // what the native encoders receive
	}{
		{format: FormatAAC, bitrate: "96k", wantArg: "96k", wantNumeric: 96},
		{format: FormatOpus, bitrate: "64k", wantArg: "64k", wantNumeric: 64},
		{format: FormatOpus, bitrate: "320k", wantArg: "256k", wantNumeric: 256},
		{format: FormatMP3, bitrate: "512k", wantArg: "320k", wantNumeric: 320},
		{format: FormatMP3, bitrate: "128k", wantArg: "128k", wantNumeric: 128},
	}

	for _, tt := range tests {
		t.Run(tt.format+"_"+tt.bitrate, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.wantArg, getMaxBitrate(tt.format, tt.bitrate),
				"FFmpeg argument")
			assert.Equal(t, tt.wantNumeric, EffectiveBitrateKbps(tt.format, tt.bitrate),
				"native encoder bitrate")
		})
	}
}
