package birdweather

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/audionorm"
	"github.com/tphakala/birdnet-go/internal/conf"
	goflac "github.com/tphakala/go-flac/pcm"
)

// sinePCM builds mono 48 kHz 16-bit LE PCM of a 440 Hz sine at the given peak
// dBFS, as interleaved bytes.
func sinePCM(samples int, dbfs float64) []byte {
	amp := math.Pow(10, dbfs/20) * 32767
	b := make([]byte, samples*2)
	for i := range samples {
		v := int16(amp * math.Sin(2*math.Pi*440*float64(i)/float64(conf.SampleRate)))
		binary.LittleEndian.PutUint16(b[i*2:], uint16(v)) //nolint:gosec // G115: sample within int16 range
	}
	return b
}

// decodeToInt16 decodes a FLAC byte stream back to []int16.
func decodeToInt16(t *testing.T, flacBytes []byte) []int16 {
	t.Helper()
	dec, err := goflac.NewDecoder(bytes.NewReader(flacBytes))
	require.NoError(t, err)
	decoded, err := io.ReadAll(dec)
	require.NoError(t, err)
	out := make([]int16, len(decoded)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(decoded[i*2:])) //nolint:gosec // G115: PCM bit reinterpretation
	}
	return out
}

// TestEncodeWithNativeFLAC verifies the native upload path produces a valid,
// loudness-normalized FLAC stream without any FFmpeg dependency.
func TestEncodeWithNativeFLAC(t *testing.T) {
	t.Parallel()
	// Explicitly no FFmpeg path: the native path must not need it.
	client := &BwClient{Settings: &conf.Settings{}}

	// A quiet (-30 dBFS) 1 s sine; normalization should boost it toward -23 LUFS.
	pcm := sinePCM(conf.SampleRate, -30)

	res, err := client.encodeWithNativeFLAC(pcm, testTimestamp)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, res.buffer)
	assert.Equal(t, "flac", res.ext)

	flacBytes := res.buffer.Bytes()
	require.GreaterOrEqual(t, len(flacBytes), 4)
	assert.Equal(t, "fLaC", string(flacBytes[:4]), "FLAC signature not found")

	// Re-measure the decoded output: it should sit near the target loudness.
	out := decodeToInt16(t, flacBytes)
	meas, err := audionorm.MeasureInt16(out, conf.SampleRate, conf.NumChannels)
	require.NoError(t, err)
	assert.InDelta(t, targetIntegratedLoudnessLUFS, meas.IntegratedLUFS, 1.5,
		"native path should normalize the decoded clip toward the target loudness")
}

// TestEncodeWithNativeFLAC_DoesNotMutateInput proves the gain is applied to a
// scratch copy, never to the caller's PCM buffer.
func TestEncodeWithNativeFLAC_DoesNotMutateInput(t *testing.T) {
	t.Parallel()
	client := &BwClient{Settings: &conf.Settings{}}

	pcm := sinePCM(20000, -30) // quiet -> non-zero boost, exercising the gain path
	orig := bytes.Clone(pcm)

	_, err := client.encodeWithNativeFLAC(pcm, testTimestamp)
	require.NoError(t, err)
	assert.Equal(t, orig, pcm, "native encode must not mutate the caller's PCM buffer")
}

// TestEncodeWithNativeFLAC_Silence keeps silent input unchanged (gain 0) rather
// than boosting noise, the deliberate behavior change from the FFmpeg path.
func TestEncodeWithNativeFLAC_Silence(t *testing.T) {
	t.Parallel()
	client := &BwClient{Settings: &conf.Settings{}}

	pcm := make([]byte, conf.SampleRate*2) // all-zero (silent) 1 s clip
	res, err := client.encodeWithNativeFLAC(pcm, testTimestamp)
	require.NoError(t, err)
	require.NotNil(t, res.buffer)

	out := decodeToInt16(t, res.buffer.Bytes())
	for _, s := range out {
		require.Zero(t, s, "silent input must stay silent (no gain applied)")
	}
}

// TestPCMInt16FromBytes_SignedRoundTrip is the regression guard for the signed
// cast: a Uint16-only read would turn negative samples into large positives and
// corrupt the loudness measurement.
func TestPCMInt16FromBytes_SignedRoundTrip(t *testing.T) {
	t.Parallel()
	want := []int16{0, -1, 1, 32767, -32768, 1234, -1234}
	b := make([]byte, len(want)*2)
	for i, s := range want {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(s)) //nolint:gosec // G115: building signed test PCM
	}
	assert.Equal(t, want, pcmInt16FromBytes(b))
}

// TestPCMInt16FromBytes_OddTrailingByte drops a trailing odd byte (never present
// in real int16 PCM) instead of panicking.
func TestPCMInt16FromBytes_OddTrailingByte(t *testing.T) {
	t.Parallel()
	got := pcmInt16FromBytes([]byte{0x01, 0x02, 0x03})
	assert.Len(t, got, 1)
	assert.Equal(t, int16(0x0201), got[0])
}
