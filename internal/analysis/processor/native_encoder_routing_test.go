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

	"github.com/tphakala/birdnet-go/internal/audiocore/aac"
	"github.com/tphakala/birdnet-go/internal/audiocore/audioformat"
	"github.com/tphakala/birdnet-go/internal/audiocore/clipenc"
	"github.com/tphakala/birdnet-go/internal/audiocore/mp3"
	"github.com/tphakala/birdnet-go/internal/audiocore/opus"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

// newExportTestAction builds the minimum SaveAudioAction encodeClip reads: a
// settings tree with an export bitrate and no normalization, plus a second of
// PCM. Normalization is left off so these tests measure routing only.
func newExportTestAction(t *testing.T, bitrate string) *SaveAudioAction {
	t.Helper()
	return &SaveAudioAction{
		CorrelationID: "export-test",
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

func TestEncodeClip_AACEncodesNatively(t *testing.T) {
	a := newExportTestAction(t, "96k")

	out := filepath.Join(t.TempDir(), "clip.m4a")
	encoder, err := a.encodeClip(t.Context(), conf.SampleRate, audioformat.AAC, out)
	require.NoError(t, err)
	assert.Equal(t, clipenc.NativeAAC, encoder.Encoder, "the clip must record which encoder ran")

	assertNonEmptyFileWithMagic(t, out, 4, "ftyp")
}

// Opus is encoded natively (go-opus) and produces an Ogg container.
func TestEncodeClip_OpusEncodesNatively(t *testing.T) {
	a := newExportTestAction(t, "64k")

	out := filepath.Join(t.TempDir(), "clip.opus")
	encoder, err := a.encodeClip(t.Context(), conf.SampleRate, audioformat.Opus, out)
	require.NoError(t, err)
	assert.Equal(t, clipenc.NativeOpus, encoder.Encoder)

	assertNonEmptyFileWithMagic(t, out, 0, "OggS")
}

// Every export format routes to a native encoder; an unrecognized format is an
// error, never an FFmpeg route.
func TestSelectEncoder_AlwaysNative(t *testing.T) {
	for format, want := range map[string]string{
		audioformat.WAV:  clipenc.NativeWAV,
		audioformat.FLAC: clipenc.NativeFLAC,
		audioformat.AAC:  clipenc.NativeAAC,
		audioformat.MP3:  clipenc.NativeMP3,
		audioformat.Opus: clipenc.NativeOpus,
	} {
		got, err := selectEncoder(format)
		require.NoError(t, err, format)
		assert.Equal(t, want, got, format)
	}
	_, err := selectEncoder("invalid")
	require.Error(t, err)
}

// FLAC and WAV are unconditionally native.
func TestEncodeClip_FLACAndWAVAlwaysNative(t *testing.T) {
	a := newExportTestAction(t, "96k")
	dir := t.TempDir()

	flacEncoder, err := a.encodeClip(t.Context(), conf.SampleRate, audioformat.FLAC, filepath.Join(dir, "clip.flac"))
	require.NoError(t, err)
	assert.Equal(t, clipenc.NativeFLAC, flacEncoder.Encoder)

	wavEncoder, err := a.encodeClip(t.Context(), conf.SampleRate, audioformat.WAV, filepath.Join(dir, "clip.wav"))
	require.NoError(t, err)
	assert.Equal(t, clipenc.NativeWAV, wavEncoder.Encoder)
}

// Static Export.Gain must reach the native lossy encoders, not just FLAC.
func TestEncodeClip_NativeLossyAppliesStaticGain(t *testing.T) {
	a := newExportTestAction(t, "96k")
	a.Settings.Realtime.Audio.Export.Gain = -6

	gainDB, _, err := a.resolveExportGainDB(t.Context(), conf.SampleRate, audioformat.AAC)
	require.NoError(t, err)
	assert.InDelta(t, -6.0, gainDB, 0.001, "static gain must pass through when normalization is off")
}

// With normalization enabled the measured EBU R128 gain replaces the static
// gain rather than compounding with it, matching the old FFmpeg loudnorm
// behaviour and the FLAC path.
func TestResolveExportGainDB_NormalizationReplacesStaticGain(t *testing.T) {
	a := newExportTestAction(t, "96k")
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
	gainDB, _, err := a.resolveExportGainDB(t.Context(), conf.SampleRate, audioformat.AAC)
	require.NoError(t, err)
	assert.InDelta(t, testTargetLUFS-measured, gainDB, 0.5,
		"the measured loudness gain must supersede the static gain")
}

// When normalization is enabled but its targets fall outside the range loudnorm
// can honour, the clip is encoded with the static gain rather than being fed
// values loudnorm would mishandle. The bit-depth half of that guard is not
// reachable from a test (conf.BitDepth is a build constant), but the
// out-of-range-targets half is.
func TestResolveExportGainDB_OutOfRangeTargetsFallBackToStaticGain(t *testing.T) {
	t.Parallel()
	a := newExportTestAction(t, "96k")
	a.Settings.Realtime.Audio.Export.Gain = -4
	a.Settings.Realtime.Audio.Export.Normalization = conf.NormalizationSettings{
		Enabled: true,
		// Below loudnorm's absolute gate, so it cannot produce a usable measurement.
		TargetLUFS: -80,
		TruePeak:   testTruePeakDBTP,
	}

	gainDB, _, err := a.resolveExportGainDB(t.Context(), conf.SampleRate, audioformat.AAC)
	require.NoError(t, err)
	assert.InDelta(t, -4.0, gainDB, 0.001, "static gain must survive an unusable normalization config")
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
// clips are stored as WAV or FLAC (the ultrasonic export format), the two
// containers that carry any rate, so they must keep the capture rate. Asserting only that the export succeeds would not catch a
// writer that clamped or dropped the rate, so the rate is read back out of the
// written file.
func TestEncodeClip_UltrasonicRatesKeepCaptureRate(t *testing.T) {
	for _, rate := range []int{48000, 96000, 192000, 256000, 384000} {
		for _, tc := range []struct {
			format      string
			ext         string
			wantEncoder string
			readRate    func(*testing.T, string) int
		}{
			{audioformat.WAV, "wav", clipenc.NativeWAV, wavSampleRate},
			{audioformat.FLAC, "flac", clipenc.NativeFLAC, flacSampleRate},
		} {
			t.Run(fmt.Sprintf("%s_%dHz", tc.format, rate), func(t *testing.T) {
				a := newExportTestAction(t, "96k")
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
	for _, rate := range []int{96000, 192000, 384000} {
		t.Run(fmt.Sprintf("flac_%dHz", rate), func(t *testing.T) {
			a := newExportTestAction(t, "96k")
			a.pcmData = sinePCMAtRate(rate, 0.5, 1000, 8000)
			a.Settings.Realtime.Audio.Export.Normalization = conf.NormalizationSettings{
				Enabled:    true,
				TargetLUFS: testTargetLUFS,
				TruePeak:   testTruePeakDBTP,
			}

			// Confirm normalization actually engages rather than silently
			// returning a zero gain, which is what a sub-gate-length clip would do.
			gainDB, _, err := a.resolveExportGainDB(t.Context(), rate, audioformat.FLAC)
			require.NoError(t, err)
			assert.Greater(t, math.Abs(gainDB), 0.01,
				"normalization must produce a real gain at %d Hz, not the no-op zero "+
					"a sub-gate-length clip would yield", rate)

			dir := t.TempDir()
			out := filepath.Join(dir, "clip.flac")
			encoder, err := a.encodeClip(t.Context(), rate, audioformat.FLAC, out)
			require.NoError(t, err, "normalized FLAC export must work at %d Hz", rate)
			assert.Equal(t, clipenc.NativeFLAC, encoder.Encoder)
			assert.Equal(t, rate, flacSampleRate(t, out))

			// Asserting the planned gain is not enough: a typo passing GainDB 0
			// to the encoder would leave that assertion green. Encode the same
			// PCM with normalization off and require the bytes to differ, which
			// only holds if the gain actually reached the encoder.
			plain := newExportTestAction(t, "96k")
			plain.pcmData = sinePCMAtRate(rate, 0.5, 1000, 8000)
			plainOut := filepath.Join(dir, "plain.flac")
			_, err = plain.encodeClip(t.Context(), rate, audioformat.FLAC, plainOut)
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

// Opus is never downgraded to WAV, with or without FFmpeg and for bat-model clips
// too: a sub-48k rate go-opus cannot carry is resampled to 48k and the extension
// stays .opus.
func TestResolveExportParams_OpusIsNeverDowngraded(t *testing.T) {
	tests := []struct {
		name       string
		modelName  string
		ffmpegPath string
		rate       int
		wantRate   int
	}{
		{"opus at an unsupported rate with no ffmpeg resamples", "", "", 44100, conf.SampleRate},
		{"opus at an unsupported rate with ffmpeg resamples", "", "/usr/bin/ffmpeg", 44100, conf.SampleRate},
		{"bat opus at an unsupported sub-48k rate resamples", batModelName, "", 44100, conf.SampleRate},
		{"bat opus at an unsupported sub-48k rate with ffmpeg resamples", batModelName, "/usr/bin/ffmpeg", 22050, conf.SampleRate},
		{"opus at a supported rate is kept as is", "", "", conf.SampleRate, conf.SampleRate},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newExportTestAction(t, "96k")
			a.modelName = tt.modelName
			a.pcmData = sinePCMAtRate(tt.rate, 0.2, 1000, 8000)
			a.sourceSampleRate = tt.rate
			a.Settings.Realtime.Audio.Export.Type = audioformat.Opus
			a.Settings.Realtime.Audio.FfmpegPath = tt.ffmpegPath

			rate, format, path := a.resolveExportParams("/clips/2026/07/19/clip.opus")
			assert.Equal(t, tt.wantRate, rate)
			assert.Equal(t, audioformat.Opus, format)
			assert.Equal(t, ".opus", filepath.Ext(path))
		})
	}
}

// A clip whose source rate is below 48kHz and whose lossy native encoder
// cannot carry that rate is resampled UP to 48kHz (accepted by every native lossy
// encoder), so the configured format is preserved.
// A rate the encoder already supports is kept as-is (no needless resample), and
// FfmpegPath changes nothing for any lossy format.
func TestResolveExportParams_SubRateResamplesForNativeEncoder(t *testing.T) {
	tests := []struct {
		name       string
		format     string
		ffmpegPath string
		rate       int
		wantRate   int
		wantFormat string
	}{
		// Above 48k (non-bat): downsampled to 48k, format kept (the sibling branch).
		{"opus 96k downsamples to 48k", audioformat.Opus, "", 96000, conf.SampleRate, audioformat.Opus},
		// Unsupported sub-48k rate: resample up to 48k, keep the format, whether or
		// not FFmpeg is configured.
		{"opus 44.1k resamples to 48k", audioformat.Opus, "", 44100, conf.SampleRate, audioformat.Opus},
		{"opus 44.1k with ffmpeg still resamples to 48k", audioformat.Opus, "/usr/bin/ffmpeg", 44100, conf.SampleRate, audioformat.Opus},
		{"aac 22.05k resamples to 48k", audioformat.AAC, "", 22050, conf.SampleRate, audioformat.AAC},
		{"mp3 22.05k resamples to 48k", audioformat.MP3, "", 22050, conf.SampleRate, audioformat.MP3},
		{"aac 22.05k with ffmpeg still resamples to 48k", audioformat.AAC, "/usr/bin/ffmpeg", 22050, conf.SampleRate, audioformat.AAC},
		{"mp3 22.05k with ffmpeg still resamples to 48k", audioformat.MP3, "/usr/bin/ffmpeg", 22050, conf.SampleRate, audioformat.MP3},
		{"aac 16k with ffmpeg resamples to 48k", audioformat.AAC, "/usr/bin/ffmpeg", 16000, conf.SampleRate, audioformat.AAC},
		{"mp3 8k with ffmpeg resamples to 48k", audioformat.MP3, "/usr/bin/ffmpeg", 8000, conf.SampleRate, audioformat.MP3},
		// Rate the native encoder already supports: encode at the source rate.
		{"opus 8k stays at source", audioformat.Opus, "", 8000, 8000, audioformat.Opus},
		{"opus 16k stays at source", audioformat.Opus, "", 16000, 16000, audioformat.Opus},
		{"opus 48k stays at source", audioformat.Opus, "", conf.SampleRate, conf.SampleRate, audioformat.Opus},
		{"aac 44.1k stays at source", audioformat.AAC, "", 44100, 44100, audioformat.AAC},
		{"mp3 32k stays at source", audioformat.MP3, "", 32000, 32000, audioformat.MP3},
		{"mp3 44.1k stays at source", audioformat.MP3, "", 44100, 44100, audioformat.MP3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			a := newExportTestAction(t, "96k")
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
	tests := []struct {
		name       string
		format     string
		ext        string
		rate       int
		ffmpegPath string
		wantEnc    string
	}{
		{"opus 44.1k encodes native opus", audioformat.Opus, ".opus", 44100, "", clipenc.NativeOpus},
		{"aac 22.05k encodes native aac", audioformat.AAC, ".m4a", 22050, "", clipenc.NativeAAC},
		{"mp3 22.05k encodes native mp3", audioformat.MP3, ".mp3", 22050, "", clipenc.NativeMP3},
		{"aac 22.05k with ffmpeg encodes native aac", audioformat.AAC, ".m4a", 22050, "/usr/bin/ffmpeg", clipenc.NativeAAC},
		{"mp3 22.05k with ffmpeg encodes native mp3", audioformat.MP3, ".mp3", 22050, "/usr/bin/ffmpeg", clipenc.NativeMP3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			a := newExportTestAction(t, "96k")
			a.sourceSampleRate = tt.rate
			a.Settings.Realtime.Audio.Export.Type = tt.format
			a.Settings.Realtime.Audio.FfmpegPath = tt.ffmpegPath

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

// ffmpegPathOptions are the two FfmpegPath states lossy export must treat
// identically: it never depends on FFmpeg.
var ffmpegPathOptions = []string{"", "/usr/bin/ffmpeg"}

// lossyNativeFormats pairs each lossy export format with its file extension and
// native encoder tag.
var lossyNativeFormats = []struct {
	format  string
	ext     string
	encoder string
}{
	{audioformat.AAC, ".m4a", clipenc.NativeAAC},
	{audioformat.MP3, ".mp3", clipenc.NativeMP3},
	{audioformat.Opus, ".opus", clipenc.NativeOpus},
}

// Fixture values shared by the odd-length export tests.
const (
	// testBitsPerByte converts conf.BitDepth to bytes.
	testBitsPerByte = 8
	// testPCMFrameBytes is the size of one interleaved PCM frame in the export
	// pipeline, the unit trimPartialSample trims to.
	testPCMFrameBytes = conf.BitDepth / testBitsPerByte * conf.NumChannels
	// strayPCMByte is appended to a whole-frame buffer to leave a partial frame.
	strayPCMByte = 0x7f
	// testSubAnalysisRate is a capture rate below the analysis rate.
	testSubAnalysisRate = 22050
	// testUltrasonicRate is a capture rate above the analysis rate.
	testUltrasonicRate = 96000
	// testSineSeconds, testSineFreqHz and testSineAmp shape the sine fixture.
	testSineSeconds = 0.5
	testSineFreqHz  = 1000
	testSineAmp     = 8000
)

// losslessNativeFormats lists the lossless clip export formats with their file
// extension and native encoder tag, mirroring lossyNativeFormats.
var losslessNativeFormats = []struct {
	format  string
	ext     string
	encoder string
}{
	{audioformat.WAV, ".wav", clipenc.NativeWAV},
	{audioformat.FLAC, ".flac", clipenc.NativeFLAC},
}

// AAC and MP3 clips at a rate their encoder rejects are resampled to 48 kHz, for a
// bat-model clip below 48 kHz too, and FFmpeg's presence changes nothing.
func TestResolveExportParams_BatSubRateUpsamplesForLossy(t *testing.T) {
	for _, lf := range lossyNativeFormats {
		for _, ffmpegPath := range ffmpegPathOptions {
			t.Run(fmt.Sprintf("%s_ffmpeg=%q", lf.format, ffmpegPath), func(t *testing.T) {
				a := newExportTestAction(t, "96k")
				a.modelName = batModelName
				a.pcmData = sinePCMAtRate(22050, 0.5, 1000, 8000)
				a.sourceSampleRate = 22050
				a.Settings.Realtime.Audio.Export.Type = lf.format
				a.Settings.Realtime.Audio.FfmpegPath = ffmpegPath

				out := filepath.Join(t.TempDir(), "clip"+lf.ext)
				rate, format, path := a.resolveExportParams(out)
				require.Equal(t, conf.SampleRate, rate)
				require.Equal(t, lf.format, format)
				assert.Equal(t, lf.ext, filepath.Ext(path))

				enc, err := a.encodeClip(t.Context(), rate, format, path)
				require.NoError(t, err)
				assert.Equal(t, lf.encoder, enc.Encoder)
				assert.FileExists(t, path)
			})
		}
	}
}

// A capture buffer ending in half a sample is trimmed before the resample, so the
// resampler's odd-length error cannot strand an AAC or MP3 clip.
func TestResolveExportParams_OddLengthPCMResamplesAfterTrimForLossy(t *testing.T) {
	for _, lf := range lossyNativeFormats {
		for _, ffmpegPath := range ffmpegPathOptions {
			t.Run(fmt.Sprintf("%s_ffmpeg=%q", lf.format, ffmpegPath), func(t *testing.T) {
				a := newExportTestAction(t, "96k")
				a.pcmData = append(sinePCMAtRate(testSubAnalysisRate, testSineSeconds, testSineFreqHz, testSineAmp), strayPCMByte)
				require.Equal(t, 1, len(a.pcmData)%testPCMFrameBytes)
				a.sourceSampleRate = testSubAnalysisRate
				a.Settings.Realtime.Audio.Export.Type = lf.format
				a.Settings.Realtime.Audio.FfmpegPath = ffmpegPath

				out := filepath.Join(t.TempDir(), "clip"+lf.ext)
				rate, format, path := a.resolveExportParams(out)
				require.Equal(t, conf.SampleRate, rate)
				require.Equal(t, lf.format, format)
				require.Equal(t, 0, len(a.pcmData)%testPCMFrameBytes)

				enc, err := a.encodeClip(t.Context(), rate, format, path)
				require.NoError(t, err)
				assert.Equal(t, lf.encoder, enc.Encoder)
			})
		}
	}
}

// The same trim covers the downsample branch (a non-bat clip above 48 kHz).
func TestResolveExportParams_OddLengthPCMDownsamplesAfterTrimForLossy(t *testing.T) {
	for _, lf := range lossyNativeFormats {
		t.Run(lf.format, func(t *testing.T) {
			a := newExportTestAction(t, "96k")
			a.pcmData = append(sinePCMAtRate(testUltrasonicRate, testSineSeconds, testSineFreqHz, testSineAmp), strayPCMByte)
			require.Equal(t, 1, len(a.pcmData)%testPCMFrameBytes)
			a.sourceSampleRate = testUltrasonicRate
			a.Settings.Realtime.Audio.Export.Type = lf.format

			rate, format, path := a.resolveExportParams(filepath.Join(t.TempDir(), "clip"+lf.ext))
			require.Equal(t, conf.SampleRate, rate)
			require.Equal(t, lf.format, format)
			require.Equal(t, 0, len(a.pcmData)%testPCMFrameBytes)

			enc, err := a.encodeClip(t.Context(), rate, format, path)
			require.NoError(t, err)
			assert.Equal(t, lf.encoder, enc.Encoder)
		})
	}
}

// The lossless formats get the same trim: the WAV writer and go-flac both reject a
// buffer that is not a whole number of frames, so a trailing partial sample must
// not cost a WAV or FLAC clip at any rate, bat or not, resampled or not.
func TestResolveExportParams_OddLengthPCMEncodesForLossless(t *testing.T) {
	cases := []struct {
		name     string
		bat      bool
		rate     int
		wantRate int
	}{
		{"at the analysis rate", false, conf.SampleRate, conf.SampleRate},
		{"below the analysis rate", false, testSubAnalysisRate, testSubAnalysisRate},
		{"above the analysis rate downsamples", false, testUltrasonicRate, conf.SampleRate},
		{"bat below the analysis rate", true, testSubAnalysisRate, testSubAnalysisRate},
		{"bat above the analysis rate keeps its rate", true, testUltrasonicRate, testUltrasonicRate},
	}
	for _, lf := range losslessNativeFormats {
		for _, tc := range cases {
			t.Run(lf.format+"_"+tc.name, func(t *testing.T) {
				a := newExportTestAction(t, "96k")
				if tc.bat {
					a.modelName = batModelName
				}
				a.pcmData = append(sinePCMAtRate(tc.rate, testSineSeconds, testSineFreqHz, testSineAmp), strayPCMByte)
				require.Equal(t, 1, len(a.pcmData)%testPCMFrameBytes)
				oddLen := len(a.pcmData)
				a.sourceSampleRate = tc.rate
				a.Settings.Realtime.Audio.Export.Type = lf.format
				a.Settings.Realtime.Audio.Export.UltrasonicType = lf.format

				rate, format, path := a.resolveExportParams(filepath.Join(t.TempDir(), "clip"+lf.ext))
				require.Equal(t, tc.wantRate, rate)
				require.Equal(t, lf.format, format)
				require.Equal(t, 0, len(a.pcmData)%testPCMFrameBytes)
				if tc.wantRate == tc.rate {
					// No resample replaced the buffer, so only the stray byte is gone.
					require.Len(t, a.pcmData, oddLen-1)
				}

				enc, err := a.encodeClip(t.Context(), rate, format, path)
				require.NoError(t, err)
				assert.Equal(t, lf.encoder, enc.Encoder)
				assert.FileExists(t, path)
			})
		}
	}
}

// trimPartialSample drops only a trailing partial frame: whole-frame and empty
// buffers come back unchanged, and nothing beyond the partial frame is cut.
func TestTrimPartialSample(t *testing.T) {
	const manyFrames = 10
	for _, tc := range []struct {
		name    string
		in, out int
	}{
		{"empty", 0, 0},
		{"one byte", 1, 0},
		{"one frame", testPCMFrameBytes, testPCMFrameBytes},
		{"one frame and a byte", testPCMFrameBytes + 1, testPCMFrameBytes},
		{"many frames", manyFrames * testPCMFrameBytes, manyFrames * testPCMFrameBytes},
		{"many frames and a byte", manyFrames*testPCMFrameBytes + 1, manyFrames * testPCMFrameBytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Distinct bytes, so a trim taken from the wrong end shows up.
			in := make([]byte, tc.in)
			for i := range in {
				in[i] = byte(i + 1)
			}
			assert.Equal(t, in[:tc.out], trimPartialSample(in))
		})
	}
}

// When the resample cannot run at all (a source rate the resampler rejects), an AAC
// or MP3 clip is neither routed to FFmpeg nor written as WAV: the format and
// extension stay, the encoder rejects the unsupported rate, and the export fails
// through the normal failure path with no file on disk.
func TestExecute_UnrecoverableResampleFailsLossyWithoutWAV(t *testing.T) {
	const unresamplableRate = 100 // ratio to 48 kHz is outside the resampler's range
	for _, lf := range lossyNativeFormats {
		for _, ffmpegPath := range ffmpegPathOptions {
			t.Run(fmt.Sprintf("%s_ffmpeg=%q", lf.format, ffmpegPath), func(t *testing.T) {
				logs := logtest.CaptureBuffer(t)
				dir := t.TempDir()
				a := newExportLogAction(t, dir, "clip"+lf.ext, lf.format)
				a.sourceSampleRate = unresamplableRate
				a.Settings.Realtime.Audio.FfmpegPath = ffmpegPath

				rate, format, path := a.resolveExportParams(filepath.Join(dir, "clip"+lf.ext))
				assert.Equal(t, unresamplableRate, rate)
				assert.Equal(t, lf.format, format, "never downgraded to WAV")
				assert.Equal(t, lf.ext, filepath.Ext(path))

				err := a.Execute(t.Context(), nil)
				require.Error(t, err)

				out := logs.String()
				assert.Contains(t, out, "Audio clip export failed")
				assert.Contains(t, out, "encoder="+lf.encoder)
				assert.NotContains(t, out, "encoder="+clipenc.NativeWAV)

				entries, readErr := os.ReadDir(dir)
				require.NoError(t, readErr)
				assert.Empty(t, entries, "no clip, WAV or otherwise, may be written")
			})
		}
	}
}

// AAC, MP3 and Opus resolve identically with and without FFmpeg, never strand to WAV, and
// always end on a rate their native encoder carries (or in a lossless format for
// ultrasonic bat clips).
func TestResolveExportParams_LossyNativeIndependentOfFFmpeg(t *testing.T) {
	rates := []int{8000, 11025, 16000, 22050, 24000, 32000, 44100, 48000, 96000, 192000}
	for _, lf := range lossyNativeFormats {
		for _, rate := range rates {
			for _, isBat := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s_%dHz_bat=%t", lf.format, rate, isBat), func(t *testing.T) {
					type result struct {
						rate         int
						format, path string
					}
					results := make([]result, 0, len(ffmpegPathOptions))
					for _, ffmpegPath := range ffmpegPathOptions {
						a := newExportTestAction(t, "96k")
						if isBat {
							a.modelName = batModelName
						}
						a.pcmData = sinePCMAtRate(rate, 0.1, 1000, 8000)
						a.sourceSampleRate = rate
						a.Settings.Realtime.Audio.Export.Type = lf.format
						a.Settings.Realtime.Audio.Export.UltrasonicType = audioformat.FLAC
						a.Settings.Realtime.Audio.FfmpegPath = ffmpegPath

						r, f, p := a.resolveExportParams("/clips/clip" + lf.ext)
						results = append(results, result{r, f, p})
						assert.NotEqual(t, audioformat.WAV, f, "lossy formats must never strand to WAV")
						if f == lf.format {
							enc, err := selectEncoder(f)
							require.NoError(t, err)
							assert.Equal(t, lf.encoder, enc)
							switch lf.format {
							case audioformat.AAC:
								assert.NoError(t, aac.Supports(r, conf.BitDepth, conf.NumChannels))
							case audioformat.MP3:
								assert.NoError(t, mp3.Supports(r, conf.BitDepth, conf.NumChannels))
							default:
								assert.NoError(t, opus.Supports(r, conf.BitDepth, conf.NumChannels))
							}
						} else {
							assert.Equal(t, audioformat.FLAC, f, "only the ultrasonic format may replace it")
						}
					}
					assert.Equal(t, results[0], results[1], "FfmpegPath must not change AAC/MP3 resolution")
				})
			}
		}
	}
}

// runEncoder rejects an encoder tag it has no native writer for, rather than
// reaching for FFmpeg.
func TestRunEncoder_UnknownEncoderIsAnError(t *testing.T) {
	a := newExportTestAction(t, "96k")
	out := filepath.Join(t.TempDir(), "clip.invalid")

	err := a.runEncoder(t.Context(), "unknown-encoder", conf.SampleRate, 0, "invalid", out, 0)

	require.Error(t, err)
	assert.NoFileExists(t, out)
}

// A capture buffer ending in half a sample at a rate the encoder already accepts
// takes neither resample branch. The partial frame is still trimmed, so the
// encoder's whole-frame check cannot cost the clip.
func TestResolveExportParams_OddLengthPCMAtSupportedRateEncodesForLossy(t *testing.T) {
	for _, lf := range lossyNativeFormats {
		t.Run(lf.format, func(t *testing.T) {
			a := newExportTestAction(t, "96k")
			a.pcmData = append(sinePCMAtRate(conf.SampleRate, testSineSeconds, testSineFreqHz, testSineAmp), strayPCMByte)
			require.Equal(t, 1, len(a.pcmData)%testPCMFrameBytes)
			a.sourceSampleRate = conf.SampleRate
			a.Settings.Realtime.Audio.Export.Type = lf.format

			out := filepath.Join(t.TempDir(), "clip"+lf.ext)
			rate, format, path := a.resolveExportParams(out)
			require.Equal(t, conf.SampleRate, rate)

			enc, err := a.encodeClip(t.Context(), rate, format, path)
			require.NoError(t, err)
			assert.Equal(t, lf.encoder, enc.Encoder)
			assert.FileExists(t, path)
		})
	}
}
