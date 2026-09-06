package processor

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/clipenc"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
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

// With the AAC gate unset, AAC must resolve to the FFmpeg path. This is the
// default every existing install runs, so it is the case that must not change
// while the AAC encoder is still proving itself. Opus is no longer gated, so it
// stays native here regardless.
func TestEncodeClip_GateUnsetKeepsFFmpegRouting(t *testing.T) {
	t.Setenv(conf.EnvNativeAACEncoder, "")

	assert.False(t, nativeAACSelected(conf.SampleRate), "AAC must stay on FFmpeg by default")
	assert.True(t, nativeOpusSelected(conf.SampleRate), "Opus is native by default, no gate")
}

func TestEncodeClip_GateSelectsNativeAAC(t *testing.T) {
	t.Setenv(conf.EnvNativeAACEncoder, "native")

	a := newGateTestAction(t, "96k")
	require.True(t, nativeAACSelected(conf.SampleRate))

	out := filepath.Join(t.TempDir(), "clip.m4a")
	encoder, err := a.encodeClip(t.Context(), conf.SampleRate, ffmpeg.FormatAAC, out)
	require.NoError(t, err)
	assert.Equal(t, clipenc.NativeAAC, encoder.Encoder, "the clip must record which encoder ran")

	assertNonEmptyFileWithMagic(t, out, 4, "ftyp")
}

// Opus is encoded natively by default (go-opus), with no environment variable
// involved, and produces an Ogg container.
func TestEncodeClip_NativeOpusIsDefault(t *testing.T) {
	a := newGateTestAction(t, "64k")
	require.True(t, nativeOpusSelected(conf.SampleRate))

	out := filepath.Join(t.TempDir(), "clip.opus")
	encoder, err := a.encodeClip(t.Context(), conf.SampleRate, ffmpeg.FormatOpus, out)
	require.NoError(t, err)
	assert.Equal(t, clipenc.NativeOpus, encoder.Encoder)

	assertNonEmptyFileWithMagic(t, out, 0, "OggS")
}

// A clip the native encoder cannot carry falls back to FFmpeg rather than
// failing, so an unusual capture rate never costs a recording. 22050 Hz is
// rejected by both encoders; 44100 additionally separates them, since go-aac
// accepts it and go-opus does not. Opus needs no gate; AAC still does.
func TestEncodeClip_UnsupportedRateFallsBackToFFmpeg(t *testing.T) {
	t.Setenv(conf.EnvNativeAACEncoder, "native")

	assert.False(t, nativeAACSelected(22050), "22.05 kHz is not an AAC input rate")
	assert.False(t, nativeOpusSelected(22050), "22.05 kHz is not an Opus input rate")

	assert.True(t, nativeAACSelected(44100), "44.1 kHz is a valid AAC input rate")
	assert.False(t, nativeOpusSelected(44100), "44.1 kHz is not an Opus input rate")
}

// FLAC and WAV are unconditionally native and must not be affected by the
// AAC gate in either direction.
func TestEncodeClip_GatesDoNotAffectFLACOrWAV(t *testing.T) {
	t.Setenv(conf.EnvNativeAACEncoder, "native")

	a := newGateTestAction(t, "96k")
	dir := t.TempDir()

	flacEncoder, err := a.encodeClip(t.Context(), conf.SampleRate, ffmpeg.FormatFLAC, filepath.Join(dir, "clip.flac"))
	require.NoError(t, err)
	assert.Equal(t, clipenc.NativeFLAC, flacEncoder.Encoder)

	wavEncoder, err := a.encodeClip(t.Context(), conf.SampleRate, ffmpeg.FormatWAV, filepath.Join(dir, "clip.wav"))
	require.NoError(t, err)
	assert.Equal(t, clipenc.NativeWAV, wavEncoder.Encoder)
}

// Static Export.Gain must reach the native lossy encoders, not just FLAC.
func TestEncodeClip_NativeLossyAppliesStaticGain(t *testing.T) {
	t.Setenv(conf.EnvNativeAACEncoder, "native")

	a := newGateTestAction(t, "96k")
	a.Settings.Realtime.Audio.Export.Gain = -6

	gainDB, _, err := a.resolveExportGainDB(t.Context(), conf.SampleRate, ffmpeg.FormatAAC)
	require.NoError(t, err)
	assert.InDelta(t, -6.0, gainDB, 0.001, "static gain must pass through when normalization is off")
}

// With normalization enabled the measured EBU R128 gain replaces the static
// gain rather than compounding with it, matching the old FFmpeg loudnorm
// behaviour and the FLAC path.
func TestResolveExportGainDB_NormalizationReplacesStaticGain(t *testing.T) {
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
	gainDB, _, err := a.resolveExportGainDB(t.Context(), conf.SampleRate, ffmpeg.FormatAAC)
	require.NoError(t, err)
	assert.InDelta(t, testTargetLUFS-measured, gainDB, 0.5,
		"the measured loudness gain must supersede the static gain")
}

// When normalization is enabled but its targets fall outside the range audionorm
// can honour, the clip is encoded with the static gain rather than being fed
// values audionorm would mishandle. The bit-depth half of that guard is not
// reachable from a test (conf.BitDepth is a build constant), but the
// out-of-range-targets half is.
func TestResolveExportGainDB_OutOfRangeTargetsFallBackToStaticGain(t *testing.T) {
	t.Parallel()
	a := newGateTestAction(t, "96k")
	a.Settings.Realtime.Audio.Export.Gain = -4
	a.Settings.Realtime.Audio.Export.Normalization = conf.NormalizationSettings{
		Enabled: true,
		// Below audionorm's absolute gate, so it cannot produce a usable measurement.
		TargetLUFS: -80,
		TruePeak:   testTruePeakDBTP,
	}

	gainDB, _, err := a.resolveExportGainDB(t.Context(), conf.SampleRate, ffmpeg.FormatAAC)
	require.NoError(t, err)
	assert.InDelta(t, -4.0, gainDB, 0.001, "static gain must survive an unusable normalization config")
}

// The promise this whole change rests on is that an install with no environment
// variables set behaves exactly as it did before. Asserting only that the gate
// predicates return false does not pin that: the FFmpeg options are rebuilt in a
// new helper, and a dropped field there would be invisible. This pins every
// field, so the default path keeps a guard after the gate is eventually removed.
//
// With normalization on, GainDB is the field that matters: FFmpeg no longer runs
// loudnorm, so if the measured gain does not reach it the clip is exported
// unnormalised and nothing else would notice.
func TestEncodeClipFFmpeg_BuildsCompleteExportOptions(t *testing.T) {
	t.Parallel()
	a := newGateTestAction(t, "96k")
	exportSettings := &a.Settings.Realtime.Audio.Export
	exportSettings.Gain = -2.5
	exportSettings.Normalization = conf.NormalizationSettings{
		Enabled:    true,
		TargetLUFS: testTargetLUFS,
		TruePeak:   testTruePeakDBTP,
	}
	a.Settings.Realtime.Audio.FfmpegPath = "/usr/bin/ffmpeg"

	gainDB, _, err := a.resolveExportGainDB(t.Context(), conf.SampleRate, ffmpeg.FormatMP3)
	require.NoError(t, err)
	opts := a.buildFFmpegExportOptions(conf.SampleRate, ffmpeg.FormatMP3, "/clips/x.mp3", gainDB)

	assert.Equal(t, a.pcmData, opts.PCMData)
	assert.Equal(t, "/clips/x.mp3", opts.OutputPath)
	assert.Equal(t, ffmpeg.FormatMP3, opts.Format)
	assert.Equal(t, "96k", opts.Bitrate)
	assert.Equal(t, conf.SampleRate, opts.SampleRate)
	assert.Equal(t, conf.NumChannels, opts.Channels)
	assert.Equal(t, conf.BitDepth, opts.BitDepth)
	assert.Equal(t, "/usr/bin/ffmpeg", opts.FFmpegPath)

	// Derive the expected gain from the clip rather than hardcoding it, so the
	// assertion survives someone retuning the test tone. The clip is a 1 kHz sine
	// a second long, comfortably clear of the 400 ms R128 gate and well under the
	// true-peak ceiling, so the gain is the distance from measured to target.
	measured := measureLUFS(t, a.pcmData)
	assert.InDelta(t, testTargetLUFS-measured, opts.GainDB, 0.5,
		"the measured loudness gain must reach FFmpeg through GainDB")
	assert.Greater(t, math.Abs(opts.GainDB-exportSettings.Gain), 1.0,
		"normalization must supersede the static gain, not pass it through")
}

// The other half of the pair: with normalization off, the static Export.Gain is
// what reaches FFmpeg untouched.
func TestEncodeClipFFmpeg_StaticGainWithoutNormalization(t *testing.T) {
	t.Parallel()
	a := newGateTestAction(t, "96k")
	a.Settings.Realtime.Audio.Export.Gain = -2.5
	a.Settings.Realtime.Audio.FfmpegPath = "/usr/bin/ffmpeg"

	gainDB, _, err := a.resolveExportGainDB(t.Context(), conf.SampleRate, ffmpeg.FormatMP3)
	require.NoError(t, err)
	opts := a.buildFFmpegExportOptions(conf.SampleRate, ffmpeg.FormatMP3, "/clips/x.mp3", gainDB)

	assert.InDelta(t, -2.5, opts.GainDB, 0.001)
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

// sinePCMAtRate builds a mono 16-bit LE sine of the given duration AT the given
// sample rate. sinePCMBytes always generates at conf.SampleRate, so reusing it
// for a high-rate export would produce a buffer whose real duration shrinks as
// the rate rises: at 384 kHz a 48000-sample buffer is 125 ms, below the 400 ms
// EBU R128 gate, and the normalization under test would silently no-op.
func sinePCMAtRate(sampleRate int, seconds, freqHz float64, amp int16) []byte {
	n := int(float64(sampleRate) * seconds)
	buf := make([]byte, n*2)
	for i := range n {
		v := float64(amp) * math.Sin(2*math.Pi*freqHz*float64(i)/float64(sampleRate))
		//nolint:gosec // G115: rounded sine within int16 range, then LE bit-write
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(int16(math.Round(v))))
	}
	return buf
}

// wavSampleRate reads the sample rate the WAV writer recorded in the fmt chunk
// (canonical RIFF/WAVE puts it at byte offset 24).
func wavSampleRate(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	require.Greater(t, len(b), 28, "WAV header truncated")
	require.Equal(t, "RIFF", string(b[0:4]))
	require.Equal(t, "WAVE", string(b[8:12]))
	return int(binary.LittleEndian.Uint32(b[24:28]))
}

// flacSampleRate reads the sample rate from the FLAC STREAMINFO block. It is a
// 20-bit big-endian field starting 18 bytes in: 4 magic + 4 metadata header +
// 2+2 blocksizes + 3+3 framesizes.
func flacSampleRate(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	require.Greater(t, len(b), 21, "FLAC STREAMINFO truncated")
	require.Equal(t, "fLaC", string(b[0:4]))
	return int(b[18])<<12 | int(b[19])<<4 | int(b[20])>>4
}

// Ultrasonic capture for bat detection runs at 96 kHz, 192 kHz and above. Those
// clips are exported as WAV or FLAC (needsBatFormatFallback forces WAV for the
// lossy formats, which cannot carry the rate), so the lossy-format gates must
// not disturb them at any capture rate. Asserting only that the export succeeds
// would not catch a writer that clamped or dropped the rate, so the rate is read
// back out of the written file.
func TestEncodeClip_UltrasonicRatesUnaffectedByLossyGates(t *testing.T) {
	// Not parallel: t.Setenv.
	t.Setenv(conf.EnvNativeAACEncoder, "native")

	for _, rate := range []int{48000, 96000, 192000, 256000, 384000} {
		for _, tc := range []struct {
			format      string
			ext         string
			wantEncoder string
			readRate    func(*testing.T, string) int
		}{
			{ffmpeg.FormatWAV, "wav", clipenc.NativeWAV, wavSampleRate},
			{ffmpeg.FormatFLAC, "flac", clipenc.NativeFLAC, flacSampleRate},
		} {
			t.Run(fmt.Sprintf("%s_%dHz", tc.format, rate), func(t *testing.T) {
				a := newGateTestAction(t, "96k")
				a.pcmData = sinePCMAtRate(rate, 0.5, 1000, 8000)
				out := filepath.Join(t.TempDir(), "clip."+tc.ext)

				encoder, err := a.encodeClip(t.Context(), rate, tc.format, out)
				require.NoError(t, err, "%s export must work at %d Hz", tc.format, rate)
				assert.Equal(t, tc.wantEncoder, encoder.Encoder, "must stay on the native encoder")

				assert.Equal(t, rate, tc.readRate(t, out),
					"the written file must record the capture rate, not a clamped one")
			})
		}
	}
}

// The same high capture rates with normalization enabled, which is the path the
// resolveExportGainDB refactor actually changed. The clip is half a second at
// every rate so it clears the 400 ms EBU R128 gate and normalization genuinely
// runs, rather than short-circuiting to a no-op gain.
func TestEncodeClip_UltrasonicRatesWithNormalization(t *testing.T) {
	// Not parallel: t.Setenv.
	t.Setenv(conf.EnvNativeAACEncoder, "native")

	for _, rate := range []int{96000, 192000, 384000} {
		t.Run(fmt.Sprintf("flac_%dHz", rate), func(t *testing.T) {
			a := newGateTestAction(t, "96k")
			a.pcmData = sinePCMAtRate(rate, 0.5, 1000, 8000)
			a.Settings.Realtime.Audio.Export.Normalization = conf.NormalizationSettings{
				Enabled:    true,
				TargetLUFS: testTargetLUFS,
				TruePeak:   testTruePeakDBTP,
			}

			// Confirm normalization actually engages rather than silently
			// returning a zero gain, which is what a sub-gate-length clip would do.
			gainDB, _, err := a.resolveExportGainDB(t.Context(), rate, ffmpeg.FormatFLAC)
			require.NoError(t, err)
			assert.Greater(t, math.Abs(gainDB), 0.01,
				"normalization must produce a real gain at %d Hz, not the no-op zero "+
					"a sub-gate-length clip would yield", rate)

			dir := t.TempDir()
			out := filepath.Join(dir, "clip.flac")
			encoder, err := a.encodeClip(t.Context(), rate, ffmpeg.FormatFLAC, out)
			require.NoError(t, err, "normalized FLAC export must work at %d Hz", rate)
			assert.Equal(t, clipenc.NativeFLAC, encoder.Encoder)
			assert.Equal(t, rate, flacSampleRate(t, out))

			// Asserting the planned gain is not enough: a typo passing GainDB 0
			// to the encoder would leave that assertion green. Encode the same
			// PCM with normalization off and require the bytes to differ, which
			// only holds if the gain actually reached the encoder.
			plain := newGateTestAction(t, "96k")
			plain.pcmData = sinePCMAtRate(rate, 0.5, 1000, 8000)
			plainOut := filepath.Join(dir, "plain.flac")
			_, err = plain.encodeClip(t.Context(), rate, ffmpeg.FormatFLAC, plainOut)
			require.NoError(t, err)

			normalized, err := os.ReadFile(out) //nolint:gosec // test-controlled path
			require.NoError(t, err)
			unnormalized, err := os.ReadFile(plainOut) //nolint:gosec // test-controlled path
			require.NoError(t, err)
			assert.NotEqual(t, unnormalized, normalized,
				"the normalization gain must reach the encoder, not just be computed")
		})
	}
}

// A clip with no encoder left still falls back to WAV so the recording survives.
// A non-bat sub-48k clip now resamples to 48k instead of stranding (see
// TestResolveExportParams_SubRateResamplesForNativeEncoder), so the remaining
// strand cases are a bat clip (never resampled: ultrasonic handling is out of
// scope) whose format cannot carry its rate. The format must resolve to WAV, and
// the clip path must be corrected with it so the file on disk matches the name
// recorded in the database.
func TestResolveExportParams_StrandedClipFallsBackToWAV(t *testing.T) {
	// Not parallel: t.Setenv.
	tests := []struct {
		name       string
		envVar     string // native gate to set; empty for the ungated Opus cases
		modelName  string // a bat classifier name opts the clip out of resampling
		format     string
		inExt      string // the input clip extension (the configured format's), so the .wav rewrite is genuinely exercised
		ffmpegPath string
		rate       int
		wantFormat string
		wantExt    string
	}{
		{
			// A bat clip is never resampled, so a sub-48k bat clip the native encoder
			// cannot carry still strands to WAV rather than being converted. The input
			// is .opus so the rewrite to .wav is actually exercised, not a no-op.
			name:      "bat opus at an unsupported sub-48k rate with no ffmpeg strands the clip",
			modelName: batModelName, format: ffmpeg.FormatOpus, inExt: ".opus",
			ffmpegPath: "", rate: 44100,
			wantFormat: "wav", wantExt: ".wav",
		},
		{
			// FFmpeg present: it can still take the clip, so keep the format.
			name:   "opus at an unsupported rate keeps opus when ffmpeg exists",
			format: ffmpeg.FormatOpus, inExt: ".opus",
			ffmpegPath: "/usr/bin/ffmpeg", rate: 44100,
			wantFormat: ffmpeg.FormatOpus, wantExt: ".opus",
		},
		{
			// Supported rate: the native encoder carries it, no fallback needed.
			name:   "opus at a supported rate with no ffmpeg keeps opus",
			format: ffmpeg.FormatOpus, inExt: ".opus",
			ffmpegPath: "", rate: conf.SampleRate,
			wantFormat: ffmpeg.FormatOpus, wantExt: ".opus",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(conf.EnvNativeAACEncoder, "")
			if tt.envVar != "" {
				t.Setenv(tt.envVar, "native")
			}

			a := newGateTestAction(t, "96k")
			a.modelName = tt.modelName
			a.sourceSampleRate = tt.rate
			a.Settings.Realtime.Audio.Export.Type = tt.format
			a.Settings.Realtime.Audio.FfmpegPath = tt.ffmpegPath

			_, format, path := a.resolveExportParams("/clips/2026/07/19/clip" + tt.inExt)
			assert.Equal(t, tt.wantFormat, format)
			assert.Equal(t, tt.wantExt, filepath.Ext(path),
				"the clip path extension must follow the resolved format")
		})
	}
}

// A non-bat clip whose source rate is below 48kHz and whose lossy native encoder
// cannot carry that rate is resampled UP to 48kHz (accepted by every native lossy
// encoder) instead of stranding to WAV, so the configured format is preserved.
// A rate the encoder already supports is kept as-is (no needless resample), and an
// install that still has FFmpeg lets FFmpeg take the source rate directly.
func TestResolveExportParams_SubRateResamplesForNativeEncoder(t *testing.T) {
	// Not parallel: t.Setenv.
	tests := []struct {
		name       string
		envVar     string // native gate to set; empty for the ungated Opus cases
		format     string
		ffmpegPath string
		rate       int
		wantRate   int
		wantFormat string
	}{
		// Above 48k (non-bat): downsampled to 48k, format kept (the sibling branch).
		{"opus 96k downsamples to 48k", "", ffmpeg.FormatOpus, "", 96000, conf.SampleRate, ffmpeg.FormatOpus},
		// Unsupported sub-48k rate + no FFmpeg: resample up to 48k, keep the format.
		{"opus 44.1k resamples to 48k", "", ffmpeg.FormatOpus, "", 44100, conf.SampleRate, ffmpeg.FormatOpus},
		{"aac 22.05k resamples to 48k", conf.EnvNativeAACEncoder, ffmpeg.FormatAAC, "", 22050, conf.SampleRate, ffmpeg.FormatAAC},
		{"mp3 22.05k resamples to 48k", conf.EnvNativeMP3Encoder, ffmpeg.FormatMP3, "", 22050, conf.SampleRate, ffmpeg.FormatMP3},
		// Rate the native encoder already supports: encode at the source rate.
		{"opus 8k stays at source", "", ffmpeg.FormatOpus, "", 8000, 8000, ffmpeg.FormatOpus},
		{"opus 16k stays at source", "", ffmpeg.FormatOpus, "", 16000, 16000, ffmpeg.FormatOpus},
		{"aac 44.1k stays at source", conf.EnvNativeAACEncoder, ffmpeg.FormatAAC, "", 44100, 44100, ffmpeg.FormatAAC},
		{"mp3 32k stays at source", conf.EnvNativeMP3Encoder, ffmpeg.FormatMP3, "", 32000, 32000, ffmpeg.FormatMP3},
		{"mp3 44.1k stays at source", conf.EnvNativeMP3Encoder, ffmpeg.FormatMP3, "", 44100, 44100, ffmpeg.FormatMP3},
		// FFmpeg present: it carries the source rate, so nothing is resampled.
		{"opus 44.1k with ffmpeg stays at source", "", ffmpeg.FormatOpus, "/usr/bin/ffmpeg", 44100, 44100, ffmpeg.FormatOpus},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(conf.EnvNativeAACEncoder, "")
			t.Setenv(conf.EnvNativeMP3Encoder, "")
			if tt.envVar != "" {
				t.Setenv(tt.envVar, "native")
			}
			resetNativeSkipOnce()

			a := newGateTestAction(t, "96k")
			a.sourceSampleRate = tt.rate
			a.Settings.Realtime.Audio.Export.Type = tt.format
			a.Settings.Realtime.Audio.FfmpegPath = tt.ffmpegPath

			rate, format, _ := a.resolveExportParams("/clips/2026/07/19/clip")
			assert.Equal(t, tt.wantRate, rate, "resolved sample rate")
			assert.Equal(t, tt.wantFormat, format, "the configured lossy format must not be downgraded to WAV")
		})
	}
}

// End to end: a sub-48k clip that resolveExportParams resamples up to 48k must then
// actually encode in the configured native format and produce a non-empty file, not
// merely avoid the WAV downgrade.
func TestResolveExportParams_ResampledSubRateEncodesNatively(t *testing.T) {
	// Not parallel: t.Setenv.
	tests := []struct {
		name    string
		envVar  string
		format  string
		ext     string
		rate    int
		wantEnc string
	}{
		{"opus 44.1k encodes native opus", "", ffmpeg.FormatOpus, ".opus", 44100, clipenc.NativeOpus},
		{"aac 22.05k encodes native aac", conf.EnvNativeAACEncoder, ffmpeg.FormatAAC, ".m4a", 22050, clipenc.NativeAAC},
		{"mp3 22.05k encodes native mp3", conf.EnvNativeMP3Encoder, ffmpeg.FormatMP3, ".mp3", 22050, clipenc.NativeMP3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(conf.EnvNativeAACEncoder, "")
			t.Setenv(conf.EnvNativeMP3Encoder, "")
			if tt.envVar != "" {
				t.Setenv(tt.envVar, "native")
			}
			resetNativeSkipOnce()

			a := newGateTestAction(t, "96k")
			a.sourceSampleRate = tt.rate
			a.Settings.Realtime.Audio.Export.Type = tt.format
			a.Settings.Realtime.Audio.FfmpegPath = ""

			out := filepath.Join(t.TempDir(), "clip"+tt.ext)
			rate, format, path := a.resolveExportParams(out)
			require.Equal(t, conf.SampleRate, rate, "the sub-48k clip must be resampled to 48k")
			require.Equal(t, tt.format, format, "the configured format must be kept")

			enc, err := a.encodeClip(t.Context(), rate, format, path)
			require.NoError(t, err)
			assert.Equal(t, tt.wantEnc, enc.Encoder, "the resampled clip must encode with the native encoder")

			info, statErr := os.Stat(path)
			require.NoError(t, statErr)
			assert.Positive(t, info.Size(), "the encoded clip must not be empty")
		})
	}
}

// When resampling a sub-48k clip up to 48k fails, the export must fall through to
// the WAV safety net so the recording survives rather than being lost. An odd byte
// count is not whole 16-bit samples, so ResampleBytes rejects it deterministically,
// exercising the resampleExportTo failure path for the upsample branch.
func TestResolveExportParams_SubRateResampleFailureStrandsToWAV(t *testing.T) {
	// Not parallel: touches package-level log guards shared with sibling tests.
	resetNativeSkipOnce()

	a := newGateTestAction(t, "96k")
	a.pcmData = []byte{0x01, 0x02, 0x03} // odd length: ResampleBytes returns an error
	a.sourceSampleRate = 44100           // go-opus cannot carry 44.1k, so an upsample is attempted
	a.Settings.Realtime.Audio.Export.Type = ffmpeg.FormatOpus
	a.Settings.Realtime.Audio.FfmpegPath = "" // native only: no FFmpeg to take the source rate

	rate, format, path := a.resolveExportParams("/clips/2026/07/19/clip.opus")
	assert.Equal(t, 44100, rate, "a failed resample leaves the clip at its source rate")
	assert.Equal(t, ffmpeg.FormatWAV, format, "a failed resample must strand to WAV, not lose the recording")
	assert.Equal(t, ".wav", filepath.Ext(path), "the clip path extension must follow the WAV fallback")
}
