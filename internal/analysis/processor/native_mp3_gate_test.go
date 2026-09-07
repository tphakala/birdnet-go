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
	assert.False(t, nativeMP3Selected(conf.SampleRate), "MP3 must stay on FFmpeg without its gate")
}

// With the gate set and a shape go-mp3 accepts, the clip is encoded natively and
// the written file is a bare MP3 frame stream (no ID3 or container).
func TestEncodeClip_GateSelectsNativeMP3(t *testing.T) {
	t.Setenv(conf.EnvNativeMP3Encoder, "native")

	a := newGateTestAction(t, "96k")
	require.True(t, nativeMP3Selected(conf.SampleRate))

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

// A native MP3 clip encoded at a non-MPEG-1 configured bitrate records the rounded
// bitrate that was actually used, so the encoding log matches the file on disk.
func TestEncodeClip_NativeMP3RoundsBitrate(t *testing.T) {
	t.Setenv(conf.EnvNativeMP3Encoder, "native")

	a := newGateTestAction(t, "100k")
	out := filepath.Join(t.TempDir(), "clip.mp3")
	encoder, err := a.encodeClip(t.Context(), conf.SampleRate, ffmpeg.FormatMP3, out)
	require.NoError(t, err)
	assert.Equal(t, clipenc.NativeMP3, encoder.Encoder, "100k is rounded, so the clip stays native MP3")
	assert.Equal(t, 96, encoder.BitrateKbps, "100k rounds to the nearest MPEG-1 rate, 96k")
}

// The rounding is deliberately native-only: a clip that routes to FFmpeg must keep
// the unrounded configured bitrate, because FFmpeg accepts arbitrary rates and the
// log must report what FFmpeg was actually handed. This pins the else-arm of the
// conditional round in encodeClip so removing the "native only" guard is caught.
func TestEncodeClip_FFmpegMP3KeepsUnroundedBitrate(t *testing.T) {
	t.Setenv(conf.EnvNativeMP3Encoder, "") // gate off routes MP3 to FFmpeg

	a := newGateTestAction(t, "100k")
	out := filepath.Join(t.TempDir(), "clip.mp3")
	// The FFmpeg encode itself may fail when no ffmpeg binary is present; the
	// returned clipEncoding is populated before the encoder runs, and its
	// BitrateKbps is what this test asserts, so the encode error is irrelevant here.
	encoder, _ := a.encodeClip(t.Context(), conf.SampleRate, ffmpeg.FormatMP3, out)
	assert.Equal(t, clipenc.FFmpeg, encoder.Encoder, "gate off routes MP3 to FFmpeg")
	assert.Equal(t, 100, encoder.BitrateKbps, "the FFmpeg path keeps the unrounded configured bitrate")
}

// A clip go-mp3 cannot carry falls back to FFmpeg rather than failing. After the
// bitrate is rounded to a valid MPEG-1 rate, the only remaining reason MP3 cannot
// be carried natively is an unsupported capture rate.
func TestNativeMP3Selected_FallsBackToFFmpeg(t *testing.T) {
	t.Setenv(conf.EnvNativeMP3Encoder, "native")

	// 22.05 kHz is not an MPEG-1 Layer III rate, so it still falls back.
	assert.False(t, nativeMP3Selected(22050), "22.05 kHz must fall back to FFmpeg")
	// A supported capture rate is carried natively regardless of the configured
	// bitrate, which the encoder rounds to a valid MPEG-1 rate.
	assert.True(t, nativeMP3Selected(conf.SampleRate), "a supported rate is carried natively")
}

// selectEncoder routes MP3 to the native encoder only with the gate on and a
// carriable clip, and to FFmpeg otherwise. The bitrate no longer forks the
// decision: any configured value is rounded to a valid MPEG-1 rate, so only the
// gate and the capture rate matter.
func TestSelectEncoder_MP3Routing(t *testing.T) {
	for _, tc := range []struct {
		name string
		gate string
		rate int
		want string
	}{
		{"gate off routes to ffmpeg", "", conf.SampleRate, clipenc.FFmpeg},
		{"gate on with a carriable clip goes native", "native", conf.SampleRate, clipenc.NativeMP3},
		{"gate on with an unsupported rate falls back", "native", 22050, clipenc.FFmpeg},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Not parallel: t.Setenv, and the skip warning uses a package-level Once.
			t.Setenv(conf.EnvNativeMP3Encoder, tc.gate)
			resetNativeSkipOnce()
			assert.Equal(t, tc.want, selectEncoder(ffmpeg.FormatMP3, tc.rate))
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

// On an FFmpeg-less install, opting MP3 into the native encoder and feeding it a
// sub-48k rate go-mp3 cannot carry must now resample the clip up to 48kHz and keep
// the MP3 format, rather than stranding it to WAV. Supported rates and FFmpeg
// installs keep MP3 without resampling; the clip path extension follows the format.
func TestResolveExportParams_MP3SubRateResamplesInsteadOfStranding(t *testing.T) {
	for _, tc := range []struct {
		name       string
		gate       string
		bitrate    string
		ffmpegPath string
		rate       int
		wantRate   int
		wantFormat string
		wantExt    string
	}{
		{
			name: "gate on, unsupported sub-48k rate, no ffmpeg resamples and keeps mp3",
			gate: "native", bitrate: "128k", ffmpegPath: "", rate: 22050,
			wantRate: conf.SampleRate, wantFormat: ffmpeg.FormatMP3, wantExt: ".mp3",
		},
		{
			// 100k is not an MPEG-1 rate, but the encoder rounds it to 96k rather
			// than stranding the clip, so the format stays MP3 even without FFmpeg.
			name: "gate on, non-MPEG-1 bitrate, no ffmpeg keeps mp3 (rounded)",
			gate: "native", bitrate: "100k", ffmpegPath: "", rate: conf.SampleRate,
			wantRate: conf.SampleRate, wantFormat: ffmpeg.FormatMP3, wantExt: ".mp3",
		},
		{
			// FFmpeg present: it can still take the clip at its source rate, no resample.
			name: "gate on, unsupported rate, ffmpeg present keeps mp3",
			gate: "native", bitrate: "128k", ffmpegPath: "/usr/bin/ffmpeg", rate: 22050,
			wantRate: 22050, wantFormat: ffmpeg.FormatMP3, wantExt: ".mp3",
		},
		{
			// Carriable clip: the native encoder takes it at source rate, no fallback.
			name: "gate on, carriable clip, no ffmpeg keeps mp3",
			gate: "native", bitrate: "128k", ffmpegPath: "", rate: conf.SampleRate,
			wantRate: conf.SampleRate, wantFormat: ffmpeg.FormatMP3, wantExt: ".mp3",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(conf.EnvNativeMP3Encoder, tc.gate)
			resetNativeSkipOnce()

			a := newGateTestAction(t, tc.bitrate)
			a.sourceSampleRate = tc.rate
			a.Settings.Realtime.Audio.Export.Type = ffmpeg.FormatMP3
			a.Settings.Realtime.Audio.FfmpegPath = tc.ffmpegPath

			rate, format, path := a.resolveExportParams("/clips/2026/07/19/clip.mp3")
			assert.Equal(t, tc.wantRate, rate, "resolved sample rate")
			assert.Equal(t, tc.wantFormat, format)
			assert.Equal(t, tc.wantExt, filepath.Ext(path),
				"the clip path extension must follow the resolved format")
		})
	}
}
