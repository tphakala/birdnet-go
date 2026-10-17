package audioformat

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEffectiveBitrateKbps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		format  string
		bitrate string
		want    int
	}{
		{name: "opus default", format: Opus, bitrate: "64k", want: 64},
		{name: "aac default", format: AAC, bitrate: "96k", want: 96},
		// A bare integer is out of contract (conf requires the "NNNk" form). It is
		// read as kbit/s; this pins the behaviour so a change is visible.
		{name: "bare integer is read as kbps, outside the validated contract", format: AAC, bitrate: "128000", want: 128000},
		{name: "uppercase suffix", format: AAC, bitrate: "128K", want: 128},
		{name: "opus clamped to its ceiling", format: Opus, bitrate: "320k", want: maxBitrateOpusKbps},
		{name: "opus at its ceiling is unchanged", format: Opus, bitrate: "256k", want: maxBitrateOpusKbps},
		{name: "mp3 clamped to its ceiling", format: MP3, bitrate: "512k", want: maxBitrateMP3Kbps},
		{name: "aac is not clamped here", format: AAC, bitrate: "320k", want: 320},
		// An unparseable or empty setting yields 0, which every encoder reads as
		// "use the codec default" rather than as silence.
		{name: "empty string falls back to the codec default", format: AAC, bitrate: "", want: 0},
		{name: "garbage falls back to the codec default", format: AAC, bitrate: "high", want: 0},
		{name: "unparseable stays 0 for a clamped format", format: Opus, bitrate: "high", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, EffectiveBitrateKbps(tt.format, tt.bitrate))
		})
	}
}

// TestFileExtension verifies format-to-extension mapping.
func TestFileExtension(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "m4a", FileExtension(AAC))
	assert.Equal(t, "wav", FileExtension(WAV))
	assert.Equal(t, "mp3", FileExtension(MP3))
	assert.Equal(t, "flac", FileExtension(FLAC))
	assert.Equal(t, "opus", FileExtension(Opus))
}
