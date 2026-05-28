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

// TestResampler_BufferReuse verifies that internal buffers (outBuf and outFloats)
// are reused on subsequent calls once sized to accommodate the output.
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

	// Capture pointers after the buffers have reached steady-state size.
	steadyStatePtr := &r.outBuf[0]
	steadyStateCap := cap(r.outBuf)
	steadyFloatsPtr := &r.outFloats[0]
	steadyFloatsCap := cap(r.outFloats)

	// Third call: buffers must be reused without growing.
	out3, err := r.ResampleInto(input)
	require.NoError(t, err)
	require.NotEmpty(t, out3)

	// The backing arrays must not have changed.
	assert.Same(t, steadyStatePtr, &r.outBuf[0],
		"output buffer backing array must not change once at steady-state size")
	assert.Equal(t, steadyStateCap, cap(r.outBuf),
		"output buffer capacity must not grow once at steady-state size")
	assert.Same(t, steadyFloatsPtr, &r.outFloats[0],
		"outFloats backing array must not change once at steady-state size")
	assert.Equal(t, steadyFloatsCap, cap(r.outFloats),
		"outFloats capacity must not grow once at steady-state size")
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
	assert.Nil(t, r.outFloats, "outFloats should be nil after Close")
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

	_, err = r.ResampleInto([]byte{0x01, 0x02, 0x03}) // 3 bytes - not a multiple of 2
	assert.Error(t, err)
}

// TestResampleBytes_Downsamples verifies the one-shot helper correctly
// downsamples 256kHz to 48kHz, producing approximately 48/256 of the input samples.
func TestResampleBytes_Downsamples(t *testing.T) {
	const fromRate = 256000
	const toRate = 48000
	const inputSamples = 25600 // 100 ms at 256 kHz

	input := makePCM16(t, inputSamples)
	output, err := ResampleBytes(input, fromRate, toRate)
	require.NoError(t, err)

	assert.Equal(t, 0, len(output)%bytesPerSample, "output must contain complete 16-bit samples")

	outputSamples := len(output) / bytesPerSample
	expectedSamples := inputSamples * toRate / fromRate
	tolerance := expectedSamples / 10 // 10%
	assert.InDelta(t, expectedSamples, outputSamples, float64(tolerance),
		"output sample count should be approximately %d (got %d)", expectedSamples, outputSamples)
}

// TestResampleBytes_SameRate verifies that equal rates return the input unchanged.
func TestResampleBytes_SameRate(t *testing.T) {
	input := makePCM16(t, 100)
	output, err := ResampleBytes(input, 48000, 48000)
	require.NoError(t, err)
	assert.Equal(t, input, output)
}

// TestResampleBytes_ReturnsIndependentCopy verifies the output does not alias
// the internal resampler buffer (which would be freed on Close).
func TestResampleBytes_ReturnsIndependentCopy(t *testing.T) {
	input := makePCM16(t, 4800)
	output, err := ResampleBytes(input, 48000, 32000)
	require.NoError(t, err)
	require.NotEmpty(t, output)

	saved := make([]byte, len(output))
	copy(saved, output)

	// A second call with different data must not corrupt the first result.
	input2 := makePCM16(t, 9600)
	_, err = ResampleBytes(input2, 48000, 32000)
	require.NoError(t, err)

	assert.Equal(t, saved, output, "first result must remain unchanged after second call")
}

// TestResampleTo_WritesIntoDst verifies that ResampleTo writes output directly
// into the caller-provided buffer and the returned slice shares its backing array.
func TestResampleTo_WritesIntoDst(t *testing.T) {
	r, err := NewResampler(48000, 32000)
	require.NoError(t, err)
	require.NotNil(t, r)
	t.Cleanup(func() { require.NoError(t, r.Close()) })

	input := makePCM16(t, 4800)
	dstSize := r.EstimateOutputBytes(len(input))
	dst := make([]byte, dstSize)

	result, err := r.ResampleTo(input, dst)
	require.NoError(t, err)
	require.NotEmpty(t, result)

	// Result must alias the dst buffer.
	assert.Same(t, &dst[0], &result[0],
		"returned slice must share backing array with caller-provided dst")
}

// TestResampleTo_DstTooSmall verifies that an undersized dst returns an error
// without corrupting resampler state.
func TestResampleTo_DstTooSmall(t *testing.T) {
	r, err := NewResampler(48000, 32000)
	require.NoError(t, err)
	require.NotNil(t, r)
	t.Cleanup(func() { require.NoError(t, r.Close()) })

	input := makePCM16(t, 4800)

	// Pass a buffer that is clearly too small.
	tinyDst := make([]byte, 4)
	_, err = r.ResampleTo(input, tinyDst)
	require.Error(t, err, "should fail with undersized dst")

	// Verify state is not corrupted: a subsequent call with correct buffer works.
	dstSize := r.EstimateOutputBytes(len(input))
	dst := make([]byte, dstSize)
	result, err := r.ResampleTo(input, dst)
	require.NoError(t, err)
	assert.NotEmpty(t, result)
}

// TestResampleTo_MatchesResampleInto verifies that ResampleTo and ResampleInto
// produce byte-identical output for the same input.
func TestResampleTo_MatchesResampleInto(t *testing.T) {
	// Use two separate resamplers to avoid shared state between calls.
	r1, err := NewResampler(48000, 32000)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, r1.Close()) })

	r2, err := NewResampler(48000, 32000)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, r2.Close()) })

	input := makePCM16(t, 4800)

	intoResult, err := r1.ResampleInto(input)
	require.NoError(t, err)

	dstSize := r2.EstimateOutputBytes(len(input))
	dst := make([]byte, dstSize)
	toResult, err := r2.ResampleTo(input, dst)
	require.NoError(t, err)

	assert.Equal(t, intoResult, toResult,
		"ResampleTo and ResampleInto must produce identical output")
}

// TestResampleTo_HighRatio verifies the bat detector path (256kHz → 48kHz).
func TestResampleTo_HighRatio(t *testing.T) {
	const fromRate = 256000
	const toRate = 48000
	const inputSamples = 25600 // 100 ms at 256 kHz

	r, err := NewResampler(fromRate, toRate)
	require.NoError(t, err)
	require.NotNil(t, r)
	t.Cleanup(func() { require.NoError(t, r.Close()) })

	input := makePCM16(t, inputSamples)
	dstSize := r.EstimateOutputBytes(len(input))
	dst := make([]byte, dstSize)

	result, err := r.ResampleTo(input, dst)
	require.NoError(t, err)

	outputSamples := len(result) / bytesPerSample
	expectedSamples := inputSamples * toRate / fromRate
	tolerance := expectedSamples / 10 // 10%
	assert.InDelta(t, expectedSamples, outputSamples, float64(tolerance),
		"output sample count should be approximately %d (got %d)", expectedSamples, outputSamples)
}

// resampleBenchCases defines the rate pairs used across benchmarks.
var resampleBenchCases = []struct {
	name       string
	fromRate   int
	toRate     int
	inputMs    int // input duration in milliseconds
}{
	{"48k_to_32k", 48000, 32000, 100},
	{"256k_to_48k", 256000, 48000, 100},
	{"192k_to_48k", 192000, 48000, 100},
}

func BenchmarkResampleTo(b *testing.B) {
	for _, tc := range resampleBenchCases {
		b.Run(tc.name, func(b *testing.B) {
			r, err := NewResampler(tc.fromRate, tc.toRate)
			require.NoError(b, err)
			b.Cleanup(func() { _ = r.Close() })

			inputSamples := tc.fromRate * tc.inputMs / 1000
			input := make([]byte, inputSamples*bytesPerSample)
			for i := range inputSamples {
				v := int16((i % 65536) - 32768)
				input[i*2] = byte(v)
				input[i*2+1] = byte(v >> 8)
			}

			dstSize := r.EstimateOutputBytes(len(input))
			dst := make([]byte, dstSize)

			// Warm up.
			_, _ = r.ResampleTo(input, dst)

			b.ResetTimer()
			b.ReportAllocs()
			for b.Loop() {
				_, _ = r.ResampleTo(input, dst)
			}
		})
	}
}

func BenchmarkResampleInto(b *testing.B) {
	for _, tc := range resampleBenchCases {
		b.Run(tc.name, func(b *testing.B) {
			r, err := NewResampler(tc.fromRate, tc.toRate)
			require.NoError(b, err)
			b.Cleanup(func() { _ = r.Close() })

			inputSamples := tc.fromRate * tc.inputMs / 1000
			input := make([]byte, inputSamples*bytesPerSample)
			for i := range inputSamples {
				v := int16((i % 65536) - 32768)
				input[i*2] = byte(v)
				input[i*2+1] = byte(v >> 8)
			}

			// Warm up.
			_, _ = r.ResampleInto(input)

			b.ResetTimer()
			b.ReportAllocs()
			for b.Loop() {
				_, _ = r.ResampleInto(input)
			}
		})
	}
}
