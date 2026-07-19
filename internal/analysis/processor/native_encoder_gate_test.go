package processor

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/audiocore/nativeenc"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// newGateTestAction builds the minimum SaveAudioAction encodeClip reads: a
// settings tree with an export bitrate and no normalization, plus a second of
// PCM. Normalization is left off so these tests measure routing only.
func newGateTestAction(t *testing.T, bitrate string) *SaveAudioAction {
	t.Helper()
	return &SaveAudioAction{
		CorrelationID: "gate-test",
		pcmData:       sinePCMBytes(8000, 1.0, 1000),
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Audio: conf.AudioSettings{
					Export: conf.ExportSettings{
						Bitrate: bitrate,
					},
				},
			},
		},
	}
}

// With the gate unset, AAC and Opus must resolve to the FFmpeg path. This is
// the default every existing install runs, so it is the case that must not
// change while the native encoders are still proving themselves.
func TestEncodeClip_GateUnsetKeepsFFmpegRouting(t *testing.T) {
	t.Setenv(nativeenc.EnvAACEncoder, "")
	t.Setenv(nativeenc.EnvOpusEncoder, "")

	a := newGateTestAction(t, "96k")
	assert.False(t, a.nativeAACSelected(conf.SampleRate), "AAC must stay on FFmpeg by default")
	assert.False(t, a.nativeOpusSelected(conf.SampleRate), "Opus must stay on FFmpeg by default")
}

func TestEncodeClip_GateSelectsNativeAAC(t *testing.T) {
	t.Setenv(nativeenc.EnvAACEncoder, "native")
	t.Setenv(nativeenc.EnvOpusEncoder, "")

	a := newGateTestAction(t, "96k")
	require.True(t, a.nativeAACSelected(conf.SampleRate))
	assert.False(t, a.nativeOpusSelected(conf.SampleRate), "the AAC gate must not enable Opus")

	out := filepath.Join(t.TempDir(), "clip.m4a")
	encoder, err := a.encodeClip(t.Context(), conf.SampleRate, ffmpeg.FormatAAC, out)
	require.NoError(t, err)
	assert.Equal(t, encoderNativeAAC, encoder, "the clip must record which encoder ran")

	assertNonEmptyFileWithMagic(t, out, 4, "ftyp")
}

func TestEncodeClip_GateSelectsNativeOpus(t *testing.T) {
	t.Setenv(nativeenc.EnvAACEncoder, "")
	t.Setenv(nativeenc.EnvOpusEncoder, "native")

	a := newGateTestAction(t, "64k")
	require.True(t, a.nativeOpusSelected(conf.SampleRate))
	assert.False(t, a.nativeAACSelected(conf.SampleRate), "the Opus gate must not enable AAC")

	out := filepath.Join(t.TempDir(), "clip.opus")
	encoder, err := a.encodeClip(t.Context(), conf.SampleRate, ffmpeg.FormatOpus, out)
	require.NoError(t, err)
	assert.Equal(t, encoderNativeOpus, encoder)

	assertNonEmptyFileWithMagic(t, out, 0, "OggS")
}

// An opted-in clip the native encoder cannot carry falls back to FFmpeg rather
// than failing, so an unusual capture rate never costs a recording. 22050 Hz is
// rejected by both encoders; 44100 additionally separates them, since go-aac
// accepts it and go-opus does not.
func TestEncodeClip_UnsupportedRateFallsBackToFFmpeg(t *testing.T) {
	t.Setenv(nativeenc.EnvAACEncoder, "native")
	t.Setenv(nativeenc.EnvOpusEncoder, "native")

	a := newGateTestAction(t, "96k")

	assert.False(t, a.nativeAACSelected(22050), "22.05 kHz is not an AAC input rate")
	assert.False(t, a.nativeOpusSelected(22050), "22.05 kHz is not an Opus input rate")

	assert.True(t, a.nativeAACSelected(44100), "44.1 kHz is a valid AAC input rate")
	assert.False(t, a.nativeOpusSelected(44100), "44.1 kHz is not an Opus input rate")
}

// FLAC and WAV are unconditionally native and must not be affected by the
// lossy-format gates in either direction.
func TestEncodeClip_GatesDoNotAffectFLACOrWAV(t *testing.T) {
	t.Setenv(nativeenc.EnvAACEncoder, "native")
	t.Setenv(nativeenc.EnvOpusEncoder, "native")

	a := newGateTestAction(t, "96k")
	dir := t.TempDir()

	flacEncoder, err := a.encodeClip(t.Context(), conf.SampleRate, ffmpeg.FormatFLAC, filepath.Join(dir, "clip.flac"))
	require.NoError(t, err)
	assert.Equal(t, encoderNativeFLAC, flacEncoder)

	wavEncoder, err := a.encodeClip(t.Context(), conf.SampleRate, ffmpeg.FormatWAV, filepath.Join(dir, "clip.wav"))
	require.NoError(t, err)
	assert.Equal(t, encoderNativeWAV, wavEncoder)
}

// Static Export.Gain must reach the native lossy encoders, not just FLAC.
func TestEncodeClip_NativeLossyAppliesStaticGain(t *testing.T) {
	t.Setenv(nativeenc.EnvAACEncoder, "native")
	t.Setenv(nativeenc.EnvOpusEncoder, "native")

	a := newGateTestAction(t, "96k")
	a.Settings.Realtime.Audio.Export.Gain = -6

	gainDB, err := a.resolveNativeGainDB(t.Context(), conf.SampleRate, ffmpeg.FormatAAC, true)
	require.NoError(t, err)
	assert.InDelta(t, -6.0, gainDB, 0.001, "static gain must pass through when normalization is off")
}

// With normalization enabled the measured EBU R128 gain replaces the static
// gain rather than compounding with it, matching the old FFmpeg loudnorm
// behaviour and the FLAC path.
func TestResolveNativeGainDB_NormalizationReplacesStaticGain(t *testing.T) {
	a := newGateTestAction(t, "96k")
	a.Settings.Realtime.Audio.Export.Gain = -6
	a.Settings.Realtime.Audio.Export.Normalization = conf.NormalizationSettings{
		Enabled:    true,
		TargetLUFS: testTargetLUFS,
		TruePeak:   testTruePeakDBTP,
	}

	// The clip is a 1 kHz sine well under the true-peak ceiling, so neither the
	// ceiling nor the +/-30 dB clamp binds and the gain is exactly the distance
	// from measured to target loudness. That value is not the static -6 dB.
	measured := measureLUFS(t, a.pcmData)
	gainDB, err := a.resolveNativeGainDB(t.Context(), conf.SampleRate, ffmpeg.FormatAAC, true)
	require.NoError(t, err)
	assert.InDelta(t, testTargetLUFS-measured, gainDB, 0.5,
		"the measured loudness gain must supersede the static gain")
}

// An encoder that cannot handle the capture bit depth encodes with the static
// gain instead of being fed a measurement it would mishandle.
func TestResolveNativeGainDB_UnsupportedDepthFallsBackToStaticGain(t *testing.T) {
	a := newGateTestAction(t, "96k")
	a.Settings.Realtime.Audio.Export.Gain = -4
	a.Settings.Realtime.Audio.Export.Normalization = conf.NormalizationSettings{
		Enabled:    true,
		TargetLUFS: testTargetLUFS,
		TruePeak:   testTruePeakDBTP,
	}

	gainDB, err := a.resolveNativeGainDB(t.Context(), conf.SampleRate, ffmpeg.FormatAAC, false)
	require.NoError(t, err)
	assert.InDelta(t, -4.0, gainDB, 0.001)
}

func assertNonEmptyFileWithMagic(t *testing.T, path string, offset int, magic string) {
	t.Helper()
	st, err := os.Stat(path)
	require.NoError(t, err)
	assert.Positive(t, st.Size(), "encoded clip must not be empty")

	f, err := os.Open(path) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	head := make([]byte, offset+len(magic))
	_, err = io.ReadFull(f, head)
	require.NoError(t, err)
	assert.Equal(t, magic, string(head[offset:]), "unexpected container magic in %s", path)
}
