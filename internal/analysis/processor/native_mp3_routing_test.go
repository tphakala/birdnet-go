package processor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/audioformat"
	"github.com/tphakala/birdnet-go/internal/audiocore/clipenc"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// A shape go-mp3 accepts is encoded natively and the written file is a bare MP3
// frame stream (no ID3 or container).
func TestEncodeClip_MP3EncodesNatively(t *testing.T) {
	a := newExportTestAction(t, "96k")

	out := filepath.Join(t.TempDir(), "clip.mp3")
	encoder, err := a.encodeClip(t.Context(), conf.SampleRate, audioformat.MP3, out)
	require.NoError(t, err)
	assert.Equal(t, clipenc.NativeMP3, encoder.Encoder, "the clip must record which encoder ran")
	assert.Equal(t, 96, encoder.BitrateKbps, "the recorded bitrate must be the effective one")

	b, err := os.ReadFile(out) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	require.Greater(t, len(b), 4, "MP3 stream truncated")
	assert.Equal(t, byte(0xFF), b[0], "native MP3 output must start with a frame sync byte")
	assert.Equal(t, byte(0xE0), b[1]&0xE0, "second byte must carry the 11-bit frame sync")
}

// A native MP3 clip encoded at a non-MPEG-1 configured bitrate records the rounded
// bitrate that was actually used, so the encoding log matches the file on disk.
func TestEncodeClip_NativeMP3RoundsBitrate(t *testing.T) {
	a := newExportTestAction(t, "100k")
	out := filepath.Join(t.TempDir(), "clip.mp3")
	encoder, err := a.encodeClip(t.Context(), conf.SampleRate, audioformat.MP3, out)
	require.NoError(t, err)
	assert.Equal(t, clipenc.NativeMP3, encoder.Encoder, "100k is rounded, so the clip stays native MP3")
	assert.Equal(t, 96, encoder.BitrateKbps, "100k rounds to the nearest MPEG-1 rate, 96k")
}

// A sub-48k rate go-mp3 cannot carry is resampled up to 48kHz and the MP3 format
// is kept, with or without FFmpeg. Supported rates are not resampled; the clip path
// extension follows the format.
func TestResolveExportParams_MP3SubRateResamplesInsteadOfStranding(t *testing.T) {
	for _, tc := range []struct {
		name       string
		bitrate    string
		ffmpegPath string
		rate       int
		wantRate   int
	}{
		{"unsupported sub-48k rate, no ffmpeg resamples and keeps mp3", "128k", "", 22050, conf.SampleRate},
		{"unsupported rate with ffmpeg present still resamples", "128k", "/usr/bin/ffmpeg", 22050, conf.SampleRate},
		{"carriable clip keeps its rate", "128k", "", conf.SampleRate, conf.SampleRate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newExportTestAction(t, tc.bitrate)
			a.sourceSampleRate = tc.rate
			a.Settings.Realtime.Audio.Export.Type = audioformat.MP3
			a.Settings.Realtime.Audio.FfmpegPath = tc.ffmpegPath

			rate, format, path := a.resolveExportParams("/clips/2026/07/19/clip.mp3")
			assert.Equal(t, tc.wantRate, rate, "resolved sample rate")
			assert.Equal(t, audioformat.MP3, format)
			assert.Equal(t, ".mp3", filepath.Ext(path),
				"the clip path extension must follow the resolved format")
		})
	}
}
