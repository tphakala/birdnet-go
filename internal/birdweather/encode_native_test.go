package birdweather

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/loudness"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/go-audio-dsp/loudnorm"
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
	sampleCount := conf.SampleRate
	pcm := sinePCM(sampleCount, -30)

	res, err := client.encodeWithNativeFLAC(pcm, testTimestamp)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, res.buffer)
	assert.Equal(t, "flac", res.ext)

	flacBytes := res.buffer.Bytes()
	require.GreaterOrEqual(t, len(flacBytes), 4)
	assert.Equal(t, "fLaC", string(flacBytes[:4]), "FLAC signature not found")
	sampleRate, totalSamples := flacStreamInfo(t, flacBytes)
	assert.Equal(t, uint64(conf.SampleRate), sampleRate, "FLAC STREAMINFO sample rate mismatch")
	assert.Equal(t, uint64(sampleCount), totalSamples, "FLAC STREAMINFO total samples should be finalized")

	// STREAMINFO must carry a finalized, non-zero block size on this non-seekable
	// upload path (#3965). A zero max_blocksize makes browsers and CoreAudio reject
	// the soundscape; go-flac >= v0.4.1 writes the fixed 4096 block size up front.
	const flacBlockSize = 4096 // go-flac's fixed encoder block size
	minBlock, maxBlock := flacBlockSizes(t, flacBytes)
	assert.Equal(t, uint16(flacBlockSize), minBlock, "FLAC STREAMINFO min block size must be finalized, not the 0 sentinel")
	assert.Equal(t, uint16(flacBlockSize), maxBlock, "FLAC STREAMINFO max block size must be finalized, not the 0 sentinel")

	// Re-measure the decoded output: it should sit near the target loudness.
	out := decodeToInt16(t, flacBytes)
	meas, err := loudnorm.MeasureInt16(out, conf.SampleRate, conf.NumChannels)
	require.NoError(t, err)
	assert.InDelta(t, loudness.SoundscapeTargetLUFS, meas.IntegratedLUFS, 1.5,
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

// TestEncodeWithNativeFLAC_SubGateStaysUntouched pins that the soundscape policy
// has no gate fallback: a clip under the -70 LUFS absolute gate is encoded with
// gain 0, unlike detection clips, which are lifted.
func TestEncodeWithNativeFLAC_SubGateStaysUntouched(t *testing.T) {
	t.Parallel()
	client := &BwClient{Settings: &conf.Settings{}}

	pcm := sinePCM(conf.SampleRate, -75) // about -79 LUFS: every block under the gate
	meas, err := loudnorm.MeasureBytes(pcm, conf.SampleRate, conf.NumChannels)
	require.NoError(t, err)
	require.True(t, math.IsInf(meas.IntegratedLUFS, -1), "sanity: clip must be under the absolute gate")
	require.False(t, math.IsInf(meas.TruePeakDBTP, -1), "sanity: clip must still have a finite true peak")

	res, err := client.encodeWithNativeFLAC(pcm, testTimestamp)
	require.NoError(t, err)

	out := decodeToInt16(t, res.buffer.Bytes())
	require.Len(t, out, len(pcm)/2)
	for i, s := range out {
		want := int16(binary.LittleEndian.Uint16(pcm[i*2:])) //nolint:gosec // G115: PCM bit reinterpretation
		require.Equal(t, want, s, "sample %d: a sub-gate soundscape must not be lifted", i)
	}
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

// TestEncodeWithNativeFLAC_MeasureFailureIsReported pins that a planning
// failure (an odd-length buffer is not whole 16-bit samples) fails the encode
// instead of producing audio, and takes the logging path shared with the
// encode pass.
func TestEncodeWithNativeFLAC_MeasureFailureIsReported(t *testing.T) {
	t.Parallel()
	client := &BwClient{Settings: &conf.Settings{}}

	res, err := client.encodeWithNativeFLAC(make([]byte, conf.SampleRate*2+1), testTimestamp)
	require.Error(t, err)
	assert.Nil(t, res)
	require.ErrorIs(t, err, loudnorm.ErrOddByteLength)
}

// The signed-cast and odd-trailing-byte regression guards live with the
// go-audio-dsp byte decoders (loudnorm.PlanBytes and MeasureBytes), which this
// path calls instead of a local helper.
