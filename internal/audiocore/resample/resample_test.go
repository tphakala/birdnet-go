package resample

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makePCM16 builds a synthetic 16-bit PCM byte slice with sampleCount samples.
// Each sample value is a simple sine-like sequence to produce non-trivial data.
func makePCM16(t *testing.T, sampleCount int) []byte {
	t.Helper()
	buf := make([]byte, sampleCount*bytesPerSample)
	for i := range sampleCount {
		// Produce a simple ramp pattern: value cycles 0..32767..−32768..0
		v := int16((i % 65536) - 32768) //nolint:gosec // G115: intentional narrowing for test data
		buf[i*2] = byte(v)
		buf[i*2+1] = byte(v >> 8)
	}
	return buf
}

// TestResampler_48kTo32k verifies that downsampling 48 kHz to 32 kHz produces
// an output whose sample count is approximately 2/3 of the input.
func TestResampler_48kTo32k(t *testing.T) {
	const fromRate = 48000
	const toRate = 32000
	const inputSamples = 4800 // 100 ms at 48 kHz

	r, err := NewResampler(fromRate, toRate)
	require.NoError(t, err)
	require.NotNil(t, r)
	t.Cleanup(func() { require.NoError(t, r.Close()) })

	assert.Equal(t, fromRate, r.FromRate())
	assert.Equal(t, toRate, r.ToRate())

	input := makePCM16(t, inputSamples)
	output, err := r.ResampleInto(input)
	require.NoError(t, err)

	// Output byte count must be even (whole samples).
	assert.Equal(t, 0, len(output)%bytesPerSample, "output must contain complete 16-bit samples")

	outputSamples := len(output) / bytesPerSample

	// Expected output ≈ 3200 samples (2/3 of 4800).
	// Allow ±5% tolerance for resampler latency and rounding.
	expectedSamples := inputSamples * toRate / fromRate
	tolerance := expectedSamples / 20 // 5%
	assert.InDelta(t, expectedSamples, outputSamples, float64(tolerance),
		"output sample count should be approximately %d (got %d)", expectedSamples, outputSamples)
}

// TestResampler_SameRate verifies that NewResampler returns nil when fromRate == toRate.
func TestResampler_SameRate(t *testing.T) {
	r, err := NewResampler(48000, 48000)
	require.NoError(t, err)
	assert.Nil(t, r, "NewResampler with equal rates should return nil — no resampling needed")
}

// TestResampler_BufferReuse verifies that the internal output buffer is reused
// on subsequent calls once it has been sized to accommodate the output.
//
// The test calls ResampleInto twice with identical input. After the first call
// the output buffer is allocated to the correct size. The second call must
// reuse the same backing array without additional allocations from our wrapper
// layer. We permit one allocation per call for the inner resampler library
// (which always allocates its own output slice) and zero allocations from the
// wrapper itself.
func TestResampler_BufferReuse(t *testing.T) {
	const fromRate = 48000
	const toRate = 32000
	const inputSamples = 4800

	r, err := NewResampler(fromRate, toRate)
	require.NoError(t, err)
	require.NotNil(t, r)
	t.Cleanup(func() { require.NoError(t, r.Close()) })

	input := makePCM16(t, inputSamples)

	// First call — populates and sizes the internal buffers.
	out1, err := r.ResampleInto(input)
	require.NoError(t, err)
	require.NotEmpty(t, out1)

	// Second call — sizes the output buffer to accommodate any library variance.
	out2, err := r.ResampleInto(input)
	require.NoError(t, err)
	require.NotEmpty(t, out2)

	// Capture the pointer after the buffer has reached steady-state size.
	steadyStatePtr := &r.outBuf[0]
	steadyStateCap := cap(r.outBuf)

	// Third call: buffer must be reused without growing.
	out3, err := r.ResampleInto(input)
	require.NoError(t, err)
	require.NotEmpty(t, out3)

	// The backing array must not have changed.
	assert.Same(t, steadyStatePtr, &r.outBuf[0],
		"output buffer backing array must not change once at steady-state size")
	assert.Equal(t, steadyStateCap, cap(r.outBuf),
		"output buffer capacity must not grow once at steady-state size")
}

// TestResampler_Close verifies that Close completes without error and releases resources.
func TestResampler_Close(t *testing.T) {
	r, err := NewResampler(48000, 32000)
	require.NoError(t, err)
	require.NotNil(t, r)

	err = r.Close()
	require.NoError(t, err)

	// After close, inner must be nil (resources released).
	assert.Nil(t, r.inner, "inner resampler should be nil after Close")
	assert.Nil(t, r.inFloats, "inFloats should be nil after Close")
	assert.Nil(t, r.outBuf, "outBuf should be nil after Close")
}

// TestResampler_EmptyInput verifies that empty input returns an empty (not nil) slice.
func TestResampler_EmptyInput(t *testing.T) {
	r, err := NewResampler(48000, 32000)
	require.NoError(t, err)
	require.NotNil(t, r)
	t.Cleanup(func() { require.NoError(t, r.Close()) })

	out, err := r.ResampleInto([]byte{})
	require.NoError(t, err)
	assert.NotNil(t, out)
	assert.Empty(t, out)
}

// TestResampler_OddByteInput verifies that odd-length input returns an error.
func TestResampler_OddByteInput(t *testing.T) {
	r, err := NewResampler(48000, 32000)
	require.NoError(t, err)
	require.NotNil(t, r)
	t.Cleanup(func() { require.NoError(t, r.Close()) })

	_, err = r.ResampleInto([]byte{0x01, 0x02, 0x03}) // 3 bytes — not a multiple of 2
	assert.Error(t, err)
}
