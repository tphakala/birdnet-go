package processor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/clipenc"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// With the MP3 gate unset, MP3 must resolve to the FFmpeg path. This is the
// default every existing install runs, and the case that must not change while
// the native MP3 encoder is still proving itself.
func TestNativeMP3Selected_GateUnsetKeepsFFmpeg(t *testing.T) {
	t.Setenv(conf.EnvNativeMP3Encoder, "")
	assert.False(t, nativeMP3Selected(conf.SampleRate, 128), "MP3 must stay on FFmpeg without its gate")
}

// With the gate set and a shape go-mp3 accepts, the clip is encoded natively and
// the written file is a bare MP3 frame stream (no ID3 or container).
func TestEncodeClip_GateSelectsNativeMP3(t *testing.T) {
	t.Setenv(conf.EnvNativeMP3Encoder, "native")

	a := newGateTestAction(t, "96k")
	require.True(t, nativeMP3Selected(conf.SampleRate, 96))

	out := filepath.Join(t.TempDir(), "clip.mp3")
	encoder, err := a.encodeClip(t.Context(), conf.SampleRate, ffmpeg.FormatMP3, out)
	require.NoError(t, err)
	assert.Equal(t, clipenc.NativeMP3, encoder.Encoder, "the clip must record which encoder ran")
	assert.Equal(t, 96, encoder.BitrateKbps, "the recorded bitrate must be the effective one")

	b, err := os.ReadFile(out) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	require.Greater(t, len(b), 4, "MP3 stream truncated")
	assert.Equal(t, byte(0xFF), b[0], "native MP3 output must start with a frame sync byte")
	assert.Equal(t, byte(0xE0), b[1]&0xE0, "second byte must carry the 11-bit frame sync")
}

// A clip go-mp3 cannot carry falls back to FFmpeg rather than failing. MP3 has
// two ways to be unable to carry a clip the other codecs do not: an unsupported
// capture rate, and a bitrate outside the 14 MPEG-1 Layer III rates, which
// BirdNET-Go config otherwise allows anywhere in 32-320k.
func TestNativeMP3Selected_FallsBackToFFmpeg(t *testing.T) {
	t.Setenv(conf.EnvNativeMP3Encoder, "native")

	// 22.05 kHz is not an MPEG-1 Layer III rate.
	assert.False(t, nativeMP3Selected(22050, 128), "22.05 kHz must fall back to FFmpeg")
	// The bitrate half: 128 kbps is a valid MPEG-1 rate, 100 kbps is in the
	// configurable range but not one go-mp3 codes.
	assert.True(t, nativeMP3Selected(conf.SampleRate, 128), "128 kbps is a valid MPEG-1 rate")
	assert.False(t, nativeMP3Selected(conf.SampleRate, 100), "100 kbps must fall back to FFmpeg")
}

// selectEncoder routes MP3 to the native encoder only with the gate on and a
// carriable clip, and to FFmpeg otherwise. This pins the bitrate-driven fork the
// other lossy formats do not have.
func TestSelectEncoder_MP3Routing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		gate    string
		rate    int
		bitrate int
		want    string
	}{
		{"gate off routes to ffmpeg", "", conf.SampleRate, 128, clipenc.FFmpeg},
		{"gate on with a carriable clip goes native", "native", conf.SampleRate, 128, clipenc.NativeMP3},
		{"gate on with an unsupported rate falls back", "native", 22050, 128, clipenc.FFmpeg},
		{"gate on with a non-MPEG-1 bitrate falls back", "native", conf.SampleRate, 100, clipenc.FFmpeg},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Not parallel: t.Setenv, and the skip warning uses a package-level Once.
			t.Setenv(conf.EnvNativeMP3Encoder, tc.gate)
			resetNativeSkipOnce()
			assert.Equal(t, tc.want, selectEncoder(ffmpeg.FormatMP3, tc.rate, tc.bitrate))
		})
	}
}

// The MP3 gate must not disturb the AAC or Opus routing in either direction.
func TestEncodeClip_MP3GateDoesNotAffectOtherFormats(t *testing.T) {
	t.Setenv(conf.EnvNativeMP3Encoder, "native")
	t.Setenv(conf.EnvNativeAACEncoder, "")

	assert.False(t, nativeAACSelected(conf.SampleRate), "AAC stays on FFmpeg without its own gate")
	assert.True(t, nativeOpusSelected(conf.SampleRate), "Opus is native by default, unaffected by the MP3 gate")
}

// On an FFmpeg-less install, opting MP3 into the native encoder must not strand a
// clip go-mp3 cannot carry: the format is downgraded to WAV so the recording
// survives, and the clip path extension follows the resolved format.
func TestResolveExportParams_MP3StrandedClipFallsBackToWAV(t *testing.T) {
	for _, tc := range []struct {
		name       string
		gate       string
		bitrate    string
		ffmpegPath string
		rate       int
		wantFormat string
		wantExt    string
	}{
		{
			name: "gate on, unsupported rate, no ffmpeg strands the clip",
			gate: "native", bitrate: "128k", ffmpegPath: "", rate: 22050,
			wantFormat: "wav", wantExt: ".wav",
		},
		{
			name: "gate on, non-MPEG-1 bitrate, no ffmpeg strands the clip",
			gate: "native", bitrate: "100k", ffmpegPath: "", rate: conf.SampleRate,
			wantFormat: "wav", wantExt: ".wav",
		},
		{
			// FFmpeg present: it can still take the clip, so keep the format.
			name: "gate on, unsupported rate, ffmpeg present keeps mp3",
			gate: "native", bitrate: "128k", ffmpegPath: "/usr/bin/ffmpeg", rate: 22050,
			wantFormat: ffmpeg.FormatMP3, wantExt: ".mp3",
		},
		{
			// Carriable clip: the native encoder takes it, no fallback needed.
			name: "gate on, carriable clip, no ffmpeg keeps mp3",
			gate: "native", bitrate: "128k", ffmpegPath: "", rate: conf.SampleRate,
			wantFormat: ffmpeg.FormatMP3, wantExt: ".mp3",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(conf.EnvNativeMP3Encoder, tc.gate)
			resetNativeSkipOnce()

			a := newGateTestAction(t, tc.bitrate)
			a.sourceSampleRate = tc.rate
			a.Settings.Realtime.Audio.Export.Type = ffmpeg.FormatMP3
			a.Settings.Realtime.Audio.FfmpegPath = tc.ffmpegPath

			_, format, path := a.resolveExportParams("/clips/2026/07/19/clip.mp3")
			assert.Equal(t, tc.wantFormat, format)
			assert.Equal(t, tc.wantExt, filepath.Ext(path),
				"the clip path extension must follow the resolved format")
		})
	}
}
