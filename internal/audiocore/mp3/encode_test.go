package mp3

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mp3pcm "github.com/tphakala/go-mp3/pcm"

	"github.com/tphakala/birdnet-go/internal/audiocore/audiotemp"
)

const (
	testSampleRate = 48000
	testToneHz     = 1000.0
	testBitrate    = 96
)

// tonePCM builds interleaved little-endian mono int16 PCM holding a sine wave at
// freq Hz. A pure tone survives lossy coding well enough that its energy stays
// concentrated in one bin, which is what the round-trip assertions check.
func tonePCM(t *testing.T, sampleRate int, seconds, freq float64) []byte {
	t.Helper()
	n := int(float64(sampleRate) * seconds)
	b := make([]byte, n*2)
	for i := range n {
		v := math.Sin(2 * math.Pi * freq * float64(i) / float64(sampleRate))
		binary.LittleEndian.PutUint16(b[i*2:], uint16(int16(v*20000)))
	}
	return b
}

// samplesFrom decodes interleaved little-endian int16 PCM bytes.
func samplesFrom(b []byte) []int16 {
	out := make([]int16, len(b)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(b[i*2:]))
	}
	return out
}

// toneEnergyRatio returns the share of total signal energy sitting at freq,
// computed with a Goertzel filter. It needs no sample alignment, so it works
// across a codec that adds priming delay. A clean tone scores near 1; silence or
// noise scores far below.
func toneEnergyRatio(samples []int16, sampleRate int, freq float64) float64 {
	n := len(samples)
	if n == 0 {
		return 0
	}
	k := 2 * math.Cos(2*math.Pi*freq/float64(sampleRate))
	var s0, s1, s2, total float64
	for _, v := range samples {
		x := float64(v)
		total += x * x
		s0 = x + k*s1 - s2
		s2, s1 = s1, s0
	}
	if total == 0 {
		return 0
	}
	power := s1*s1 + s2*s2 - k*s1*s2
	return power / (total * float64(n) / 2)
}

// decodeMP3 reads back a written .mp3 and returns its decoded PCM samples plus
// the stream metadata.
func decodeMP3(t *testing.T, path string) (samples []int16, sampleRate, channels int) {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	defer func() { assert.NoError(t, f.Close()) }()

	pcm, info, err := mp3pcm.DecodeInterleaved(f)
	require.NoError(t, err, "written file must be a decodable MP3")
	return samplesFrom(pcm), info.SampleRate, info.Channels
}

func TestEncodePCM_RoundTripsAudibleTone(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "clip.mp3")
	pcm := tonePCM(t, testSampleRate, 1.0, testToneHz)

	err := EncodePCM(t.Context(), &Options{
		PCMData:     pcm,
		OutputPath:  out,
		SampleRate:  testSampleRate,
		Channels:    1,
		BitDepth:    16,
		BitrateKbps: testBitrate,
	})
	require.NoError(t, err)

	st, err := os.Stat(out)
	require.NoError(t, err)
	assert.Positive(t, st.Size(), "encoded clip must not be empty")

	got, rate, channels := decodeMP3(t, out)
	assert.Equal(t, testSampleRate, rate)
	assert.Equal(t, 1, channels)
	// go-mp3 emits a tagless stream, so decode adds priming rather than trimming
	// it: the decoded clip is at least as long as the source second.
	assert.Greater(t, len(got), testSampleRate-4096, "decoded clip is too short")
	assert.Greater(t, toneEnergyRatio(got, testSampleRate, testToneHz), 0.8,
		"decoded audio should still be dominated by the source tone")
}

// The MP3 output is a bare frame stream with no container: it must start with an
// MPEG-1 Layer III frame sync (0xFF 0xFB), not an ID3 or Xing tag.
func TestEncodePCM_WritesMP3FrameStream(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "clip.mp3")
	require.NoError(t, EncodePCM(t.Context(), &Options{
		PCMData:     tonePCM(t, testSampleRate, 0.25, testToneHz),
		OutputPath:  out,
		SampleRate:  testSampleRate,
		Channels:    1,
		BitDepth:    16,
		BitrateKbps: testBitrate,
	}))

	b, err := os.ReadFile(out) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	require.Greater(t, len(b), 4, "MP3 stream truncated")
	assert.Equal(t, byte(0xFF), b[0], "MP3 stream must start with a frame sync byte")
	assert.Equal(t, byte(0xE0), b[1]&0xE0, "second byte must carry the 11-bit frame sync")
}

func TestEncodePCM_AppliesGain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pcm := tonePCM(t, testSampleRate, 0.5, testToneHz)

	base := func(name string, gainDB float64) []int16 {
		out := filepath.Join(dir, name)
		require.NoError(t, EncodePCM(t.Context(), &Options{
			PCMData:     pcm,
			OutputPath:  out,
			SampleRate:  testSampleRate,
			Channels:    1,
			BitDepth:    16,
			BitrateKbps: testBitrate,
			GainDB:      gainDB,
		}))
		got, _, _ := decodeMP3(t, out)
		return got
	}

	plain := rms(base("plain.mp3", 0))
	attenuated := rms(base("quiet.mp3", -6))
	assert.Less(t, attenuated, plain*0.75, "-6 dB should audibly attenuate the clip")
}

// The source buffer belongs to the caller and must survive encoding unchanged,
// including on the gain path.
func TestEncodePCM_DoesNotMutateSource(t *testing.T) {
	t.Parallel()
	pcm := tonePCM(t, testSampleRate, 0.25, testToneHz)
	original := make([]byte, len(pcm))
	copy(original, pcm)

	// Both gain paths: -3 dB copies the buffer, 0 dB hands the caller's slice
	// straight to the library (pcmgain.Applied is zero-copy there).
	for _, gainDB := range []float64{-3, 0} {
		require.NoError(t, EncodePCM(t.Context(), &Options{
			PCMData:     pcm,
			OutputPath:  filepath.Join(t.TempDir(), "clip.mp3"),
			SampleRate:  testSampleRate,
			Channels:    1,
			BitDepth:    16,
			BitrateKbps: testBitrate,
			GainDB:      gainDB,
		}))
		assert.Equal(t, original, pcm, "source PCM must not be modified at %g dB", gainDB)
	}
}

// A successful encode leaves only the final file: the temp file is renamed, not
// left behind for the disk manager to trip over.
func TestEncodePCM_LeavesNoTempFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "clip.mp3")
	require.NoError(t, EncodePCM(t.Context(), &Options{
		PCMData:     tonePCM(t, testSampleRate, 0.25, testToneHz),
		OutputPath:  out,
		SampleRate:  testSampleRate,
		Channels:    1,
		BitDepth:    16,
		BitrateKbps: testBitrate,
	}))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "clip.mp3", entries[0].Name())
	assert.False(t, strings.HasSuffix(entries[0].Name(), audiotemp.Ext))
}

// A rejected encode must not leave a partial clip or a temp file behind.
func TestEncodePCM_CleansUpOnFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	err := EncodePCM(t.Context(), &Options{
		PCMData:     []byte{1, 2, 3},
		OutputPath:  filepath.Join(dir, "clip.mp3"),
		SampleRate:  testSampleRate,
		Channels:    2,
		BitDepth:    16,
		BitrateKbps: testBitrate,
	})
	require.Error(t, err)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "a rejected encode must leave the directory untouched")
}

// Forces a failure AFTER the temp file exists and has been written: a directory
// sitting at the output path makes the final rename fail. This is the only test
// that exercises the remove-temp-unless-committed contract.
func TestEncodePCM_RemovesTempFileWhenFinalizeFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "clip.mp3")
	require.NoError(t, os.Mkdir(out, 0o750))

	err := EncodePCM(t.Context(), &Options{
		PCMData:     tonePCM(t, testSampleRate, 0.25, testToneHz),
		OutputPath:  out,
		SampleRate:  testSampleRate,
		Channels:    1,
		BitDepth:    16,
		BitrateKbps: testBitrate,
	})
	require.Error(t, err, "rename onto a directory must fail")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "only the blocking directory should remain")
	assert.Equal(t, "clip.mp3", entries[0].Name())
	assert.True(t, entries[0].IsDir(), "the encoder must not have replaced the directory")

	for _, e := range entries {
		assert.False(t, strings.HasSuffix(e.Name(), audiotemp.Ext),
			"temp file %s leaked after a failed encode", e.Name())
	}
}

func TestEncodePCM_RejectsInvalidOptions(t *testing.T) {
	t.Parallel()
	valid := func() *Options {
		return &Options{
			PCMData:     tonePCM(t, testSampleRate, 0.1, testToneHz),
			OutputPath:  filepath.Join(t.TempDir(), "clip.mp3"),
			SampleRate:  testSampleRate,
			Channels:    1,
			BitDepth:    16,
			BitrateKbps: testBitrate,
		}
	}

	tests := []struct {
		name   string
		mutate func(*Options)
	}{
		{name: "empty pcm", mutate: func(o *Options) { o.PCMData = nil }},
		{name: "empty output path", mutate: func(o *Options) { o.OutputPath = "" }},
		{name: "unsupported sample rate", mutate: func(o *Options) { o.SampleRate = 22050 }},
		{name: "unsupported bit depth 8", mutate: func(o *Options) { o.BitDepth = 8 }},
		{name: "unsupported bit depth 24", mutate: func(o *Options) { o.BitDepth = 24 }},
		{name: "unsupported bit depth 32", mutate: func(o *Options) { o.BitDepth = 32 }},
		{name: "unsupported channel count", mutate: func(o *Options) { o.Channels = 3 }},
		{name: "negative bitrate", mutate: func(o *Options) { o.BitrateKbps = -1 }},
		{name: "partial trailing sample", mutate: func(o *Options) { o.PCMData = append(o.PCMData, 0) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			opts := valid()
			tt.mutate(opts)
			assert.Error(t, EncodePCM(t.Context(), opts))
		})
	}

	t.Run("nil options", func(t *testing.T) {
		t.Parallel()
		assert.Error(t, EncodePCM(t.Context(), nil))
	})
}

func TestEncodePCM_HonoursCancelledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	dir := t.TempDir()
	err := EncodePCM(ctx, &Options{
		PCMData:     tonePCM(t, testSampleRate, 0.1, testToneHz),
		OutputPath:  filepath.Join(dir, "clip.mp3"),
		SampleRate:  testSampleRate,
		Channels:    1,
		BitDepth:    16,
		BitrateKbps: testBitrate,
	})
	require.ErrorIs(t, err, context.Canceled)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestSupports_SampleRate(t *testing.T) {
	t.Parallel()
	// Every MPEG-1 Layer III rate go-mp3 accepts.
	require.NoError(t, Supports(48000, 16, 1))
	require.NoError(t, Supports(44100, 16, 1))
	require.NoError(t, Supports(32000, 16, 1))
	// Rates FFmpeg handles by resampling but go-mp3 rejects outright.
	require.Error(t, Supports(22050, 16, 1))
	require.Error(t, Supports(16000, 16, 1))
	require.Error(t, Supports(0, 16, 1))
}

func TestSupports_BitDepthAndChannels(t *testing.T) {
	t.Parallel()
	require.NoError(t, Supports(48000, 16, 1))
	require.Error(t, Supports(48000, 8, 1))
	// go-mp3 reads int16 only, and the shared gain/loudness path is int16-only
	// too, so wider depths must be rejected rather than silently reinterpreted.
	require.Error(t, Supports(48000, 24, 1), "24-bit must be rejected: pcmgain is int16-only")
	require.Error(t, Supports(48000, 32, 1), "32-bit must be rejected: pcmgain is int16-only")
	require.NoError(t, Supports(48000, 16, 2))
	require.Error(t, Supports(48000, 16, 0))
	require.Error(t, Supports(48000, 16, 3))
}

func TestRoundBitrateKbps(t *testing.T) {
	t.Parallel()
	type roundCase struct {
		name string
		in   int
		want int
	}
	specials := []roundCase{
		// Zero and any non-positive value map to go-mp3's default (0).
		{"zero maps to the go-mp3 default", 0, 0},
		{"negative maps to the go-mp3 default", -1, 0},
		// In-range non-MPEG-1 values snap to the nearest rate instead of being rejected.
		{"in-range rounds down to 96", 100, 96},
		{"in-range rounds down to 192", 200, 192},
		{"in-range rounds up to 224", 210, 224},
		// Below the lowest rate snaps up; above the highest snaps down.
		{"below the lowest rate snaps up to 32", 10, 32},
		{"above the highest rate snaps down to 320", 500, 320},
		{"just above 320 snaps to 320", 321, 320},
		// An exact midpoint resolves to the higher rate.
		{"exact midpoint rounds up (128 vs 160)", 144, 160},
		{"exact midpoint rounds up (32 vs 40)", 36, 40},
	}
	validRates := []int{32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
	cases := make([]roundCase, 0, len(specials)+len(validRates))
	cases = append(cases, specials...)
	// Every valid MPEG-1 Layer III rate maps to itself.
	for _, kbps := range validRates {
		cases = append(cases, roundCase{strconv.Itoa(kbps) + "k maps to itself", kbps, kbps})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, RoundBitrateKbps(tc.in))
		})
	}
}

func TestEncodePCM_RoundsNonStandardBitrate(t *testing.T) {
	t.Parallel()
	// 100 kbps is in BirdNET-Go's configurable range but not an MPEG-1 rate. It must
	// encode natively (rounded to 96k) rather than error, so a clip is never lost to
	// a bitrate the config allowed.
	out := filepath.Join(t.TempDir(), "clip.mp3")
	err := EncodePCM(t.Context(), &Options{
		PCMData:     tonePCM(t, testSampleRate, 0.1, testToneHz),
		OutputPath:  out,
		SampleRate:  testSampleRate,
		Channels:    1,
		BitDepth:    16,
		BitrateKbps: 100,
	})
	require.NoError(t, err)
	b, err := os.ReadFile(out) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	require.Greater(t, len(b), 4, "MP3 stream truncated")
	assert.Equal(t, byte(0xFF), b[0], "output must start with a frame sync byte")
	assert.Equal(t, byte(0xE0), b[1]&0xE0, "second byte must carry the 11-bit frame sync")
	// Prove the rounded rate actually reached the stream, not just that encoding
	// succeeded: byte 2's top nibble is the MPEG-1 Layer III bitrate index, and
	// 96 kbps is index 7 (0b0111). This fails if the wrapper ever passed 0 and let
	// go-mp3 default to 128 kbps (index 9) instead of rounding 100 to 96.
	assert.Equal(t, byte(0x70), b[2]&0xF0, "third byte must carry the 96 kbps bitrate index")
}

// Cross-validate the stream against an external decoder when ffprobe is present.
func TestEncodePCM_FFprobeAcceptsOutput(t *testing.T) {
	t.Parallel()
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed")
	}

	out := filepath.Join(t.TempDir(), "clip.mp3")
	require.NoError(t, EncodePCM(t.Context(), &Options{
		PCMData:     tonePCM(t, testSampleRate, 1.0, testToneHz),
		OutputPath:  out,
		SampleRate:  testSampleRate,
		Channels:    1,
		BitDepth:    16,
		BitrateKbps: testBitrate,
	}))

	probe, err := exec.CommandContext(t.Context(), ffprobe, //nolint:gosec // fixed args, resolved binary
		"-v", "error",
		"-select_streams", "a:0",
		"-show_entries", "stream=codec_name,sample_rate,channels",
		"-of", "default=noprint_wrappers=1",
		out).Output()
	require.NoError(t, err, "ffprobe must parse the written stream")

	got := map[string]string{}
	for field := range strings.FieldsSeq(string(probe)) {
		if k, v, ok := strings.Cut(field, "="); ok {
			got[k] = v
		}
	}
	assert.Equal(t, "mp3", got["codec_name"])
	assert.Equal(t, "48000", got["sample_rate"])
	assert.Equal(t, "1", got["channels"])
}

// rms returns the root mean square amplitude of the samples.
func rms(samples []int16) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, v := range samples {
		sum += float64(v) * float64(v)
	}
	return math.Sqrt(sum / float64(len(samples)))
}
