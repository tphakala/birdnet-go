package equalizer

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyBatchFloat32_HighPass_AttenuatesLowFreq(t *testing.T) {
	t.Parallel()
	const sampleRate = 48000.0
	const cutoff = 4000.0
	const q = 0.707
	const numSamples = 48000

	filter, err := NewHighPass(sampleRate, cutoff, q, 1)
	require.NoError(t, err)

	// Generate a 500Hz sine wave (should be strongly attenuated)
	input := make([]float32, numSamples)
	for i := range input {
		input[i] = float32(math.Sin(2.0 * math.Pi * 500.0 * float64(i) / sampleRate))
	}

	filter.ApplyBatchFloat32(input)

	// Measure RMS of the filtered output (skip transient at start)
	var sum float64
	start := numSamples / 2
	for i := start; i < numSamples; i++ {
		sum += float64(input[i]) * float64(input[i])
	}
	rms := math.Sqrt(sum / float64(numSamples-start))

	// 500Hz is well below 4kHz cutoff, should be heavily attenuated
	assert.Less(t, rms, 0.1, "500Hz tone should be attenuated by high-pass at 4kHz")
}

func TestApplyBatchFloat32_HighPass_PassesHighFreq(t *testing.T) {
	t.Parallel()
	const sampleRate = 48000.0
	const cutoff = 4000.0
	const q = 0.707
	const numSamples = 48000

	filter, err := NewHighPass(sampleRate, cutoff, q, 1)
	require.NoError(t, err)

	// Generate a 10kHz sine wave (should pass through)
	input := make([]float32, numSamples)
	for i := range input {
		input[i] = float32(math.Sin(2.0 * math.Pi * 10000.0 * float64(i) / sampleRate))
	}

	filter.ApplyBatchFloat32(input)

	// Measure RMS of the filtered output (skip transient)
	var sum float64
	start := numSamples / 2
	for i := start; i < numSamples; i++ {
		sum += float64(input[i]) * float64(input[i])
	}
	rms := math.Sqrt(sum / float64(numSamples-start))

	// 10kHz is above 4kHz cutoff, should mostly pass
	assert.Greater(t, rms, 0.5, "10kHz tone should pass through high-pass at 4kHz")
}

func TestApplyBatchFloat32_EmptyInput(t *testing.T) {
	t.Parallel()
	filter, err := NewHighPass(48000.0, 4000.0, 0.707, 1)
	require.NoError(t, err)

	input := []float32{}
	filter.ApplyBatchFloat32(input)
	assert.Empty(t, input)
}

func TestFilterChain_ApplyBatchFloat32(t *testing.T) {
	t.Parallel()
	const sampleRate = 48000.0
	const numSamples = 48000

	filter, err := NewHighPass(sampleRate, 4000.0, 0.707, 1)
	require.NoError(t, err)

	chain := NewFilterChain()
	require.NoError(t, chain.AddFilter(filter))

	input := make([]float32, numSamples)
	for i := range input {
		input[i] = float32(math.Sin(2.0 * math.Pi * 500.0 * float64(i) / sampleRate))
	}

	chain.ApplyBatchFloat32(input)

	var sum float64
	start := numSamples / 2
	for i := start; i < numSamples; i++ {
		sum += float64(input[i]) * float64(input[i])
	}
	rms := math.Sqrt(sum / float64(numSamples-start))
	assert.Less(t, rms, 0.1, "chain should attenuate 500Hz with high-pass at 4kHz")
}
