package opus

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/go-opus/oggopus"

	"github.com/tphakala/birdnet-go/internal/audiocore/audiotemp"
)

const (
	testSampleRate = 48000
	testToneHz     = 1000.0
	testBitrate    = 64
)

// tonePCM builds interleaved little-endian mono int16 PCM holding a sine wave
// at freq Hz. A pure tone survives lossy coding well enough that its energy
// stays concentrated in one bin, which is what the round-trip assertions check.
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
// across a codec that adds pre-skip. A clean tone scores near 1; silence or
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

// decodeOgg reads back a written .opus file and returns its decoded PCM plus
// the stream info.
func decodeOgg(t *testing.T, path string) ([]int16, oggopus.Info) {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	dec, err := oggopus.NewDecoder(f)
	require.NoError(t, err, "written file must be a readable Ogg Opus stream")

	pcm, err := io.ReadAll(dec)
	require.NoError(t, err)
	return samplesFrom(pcm), dec.Info()
}

func TestEncodePCM_RoundTripsAudibleTone(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "clip.opus")
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

	got, info := decodeOgg(t, out)
	assert.Equal(t, 1, info.Channels)
	assert.Equal(t, uint32(48000), info.OutputSampleRate)
	// One second in, minus pre-skip and 20 ms frame padding slack.
	assert.Greater(t, len(got), testSampleRate-4096, "decoded clip is too short")
	assert.Greater(t, toneEnergyRatio(got, testSampleRate, testToneHz), 0.8,
		"decoded audio should still be dominated by the source tone")
}

func TestEncodePCM_WritesOggContainer(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "clip.opus")
	require.NoError(t, EncodePCM(t.Context(), &Options{
		PCMData:     tonePCM(t, testSampleRate, 0.25, testToneHz),
		OutputPath:  out,
		SampleRate:  testSampleRate,
		Channels:    1,
		BitDepth:    16,
		BitrateKbps: testBitrate,
	}))

	head := make([]byte, 4)
	f, err := os.Open(out) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	_, err = io.ReadFull(f, head)
	require.NoError(t, err)
	assert.Equal(t, "OggS", string(head), "output must be an Ogg stream")
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
		got, _ := decodeOgg(t, out)
		return got
	}

	plain := rms(base("plain.opus", 0))
	attenuated := rms(base("quiet.opus", -6))
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
	// straight to the library (pcmgain.Applied is zero-copy there), which is the
	// case that would corrupt the spectrogram pre-render if a library ever wrote
	// into its input.
	for _, gainDB := range []float64{-3, 0} {
		require.NoError(t, EncodePCM(t.Context(), &Options{
			PCMData:     pcm,
			OutputPath:  filepath.Join(t.TempDir(), "clip.opus"),
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
	out := filepath.Join(dir, "clip.opus")
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
	assert.Equal(t, "clip.opus", entries[0].Name())
	assert.False(t, strings.HasSuffix(entries[0].Name(), audiotemp.Ext))
}

// A rejected encode must not leave a partial clip or a temp file behind.
func TestEncodePCM_CleansUpOnFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	err := EncodePCM(t.Context(), &Options{
		PCMData:     []byte{1, 2, 3},
		OutputPath:  filepath.Join(dir, "clip.opus"),
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

// The cleanup test above is rejected during validation, before any file is
// created, so on its own it would pass even if the temp file were never removed.
// This one forces a failure AFTER the temp file exists and has been written:
// a directory sitting at the output path makes the final rename fail. It is the
// only test that actually exercises the remove-temp-unless-committed contract.
func TestEncodePCM_RemovesTempFileWhenFinalizeFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "clip.opus")
	// A directory cannot be replaced by a file rename, so Finalize fails.
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
	assert.Equal(t, "clip.opus", entries[0].Name())
	assert.True(t, entries[0].IsDir(), "the encoder must not have replaced the directory")

	// Nothing ending in the temp suffix may survive a failed encode.
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
			OutputPath:  filepath.Join(t.TempDir(), "clip.opus"),
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
		{name: "unsupported sample rate", mutate: func(o *Options) { o.SampleRate = 44100 }},
		{name: "unsupported bit depth", mutate: func(o *Options) { o.BitDepth = 24 }},
		{name: "unsupported channel count", mutate: func(o *Options) { o.Channels = 3 }},
		{name: "negative bitrate", mutate: func(o *Options) { o.BitrateKbps = -1 }},
		{name: "partial trailing frame", mutate: func(o *Options) { o.PCMData = append(o.PCMData, 0) }},
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
		OutputPath:  filepath.Join(dir, "clip.opus"),
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
	require.NoError(t, Supports(48000, 16, 1))
	require.NoError(t, Supports(16000, 16, 1))
	require.NoError(t, Supports(8000, 16, 1))
	// 44100 is not an Opus input rate; FFmpeg resamples, go-opus rejects.
	require.Error(t, Supports(44100, 16, 1))
	require.Error(t, Supports(0, 16, 1))
}

func TestSupports_BitDepthAndChannels(t *testing.T) {
	t.Parallel()
	require.NoError(t, Supports(48000, 16, 1))
	require.Error(t, Supports(48000, 24, 1))
	require.Error(t, Supports(48000, 8, 1))
	require.NoError(t, Supports(48000, 16, 1))
	require.NoError(t, Supports(48000, 16, 2))
	require.Error(t, Supports(48000, 16, 0))
	require.Error(t, Supports(48000, 16, 3))
}

// Cross-validate the stream against an external demuxer, so a malformed header
// or granule position surfaces here rather than in a user's browser.
func TestEncodePCM_FFprobeAcceptsOutput(t *testing.T) {
	t.Parallel()
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed")
	}

	out := filepath.Join(t.TempDir(), "clip.opus")
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

	// Parse key=value rather than indexing positionally: ffprobe emits fields in
	// its own internal order, not the order requested, so positions are not a
	// contract.
	got := map[string]string{}
	for field := range strings.FieldsSeq(string(probe)) {
		if k, v, ok := strings.Cut(field, "="); ok {
			got[k] = v
		}
	}
	assert.Equal(t, "opus", got["codec_name"])
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
