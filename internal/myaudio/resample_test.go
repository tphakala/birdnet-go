package myaudio

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Unit Tests for ResampleAudio
// =============================================================================

func TestResampleAudio_SameRate(t *testing.T) {
	input := []float32{0.1, 0.2, 0.3, 0.4, 0.5}

	result, err := ResampleAudio(input, 48000, 48000)
	require.NoError(t, err)

	// Should return the same slice (no resampling needed)
	assert.Equal(t, input, result)
	// Verify it's the same underlying array (no allocation)
	assert.Equal(t, &input[0], &result[0], "should return same slice without allocation")
}

func TestResampleAudio_OutputLength(t *testing.T) {
	tests := []struct {
		name           string
		inputLen       int
		originalRate   int
		targetRate     int
		expectedOutLen int
	}{
		{
			name:           "44100_to_48000",
			inputLen:       44100,
			originalRate:   44100,
			targetRate:     48000,
			expectedOutLen: 48000,
		},
		{
			name:           "48000_to_44100",
			inputLen:       48000,
			originalRate:   48000,
			targetRate:     44100,
			expectedOutLen: 44100,
		},
		{
			name:           "16000_to_48000_3x",
			inputLen:       16000,
			originalRate:   16000,
			targetRate:     48000,
			expectedOutLen: 48000,
		},
		{
			name:           "96000_to_48000_half",
			inputLen:       96000,
			originalRate:   96000,
			targetRate:     48000,
			expectedOutLen: 48000,
		},
		{
			name:           "8000_to_48000_6x",
			inputLen:       8000,
			originalRate:   8000,
			targetRate:     48000,
			expectedOutLen: 48000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := make([]float32, tt.inputLen)
			// Fill with test pattern
			for i := range input {
				input[i] = float32(i) / float32(tt.inputLen)
			}

			result, err := ResampleAudio(input, tt.originalRate, tt.targetRate)
			require.NoError(t, err)
			assert.Len(t, result, tt.expectedOutLen)
		})
	}
}

func TestResampleAudio_DCSignal(t *testing.T) {
	// A DC signal (constant value) should remain constant after resampling
	dcValue := float32(0.5)
	inputLen := 48000
	input := make([]float32, inputLen)
	for i := range input {
		input[i] = dcValue
	}

	// Upsample
	t.Run("upsample_preserves_dc", func(t *testing.T) {
		result, err := ResampleAudio(input, 48000, 96000)
		require.NoError(t, err)

		// All output values should be close to DC value
		for i, v := range result {
			assert.InDelta(t, dcValue, v, 0.001, "sample %d should be close to DC value", i)
		}
	})

	// Downsample
	t.Run("downsample_preserves_dc", func(t *testing.T) {
		result, err := ResampleAudio(input, 48000, 24000)
		require.NoError(t, err)

		for i, v := range result {
			assert.InDelta(t, dcValue, v, 0.001, "sample %d should be close to DC value", i)
		}
	})
}

func TestResampleAudio_SineWaveFrequency(t *testing.T) {
	// A sine wave at a known frequency should preserve that frequency after resampling
	originalRate := 48000
	targetRate := 96000
	frequency := 1000.0 // 1kHz

	// Generate 1 second of sine wave
	inputLen := originalRate
	input := make([]float32, inputLen)
	for i := range input {
		input[i] = float32(math.Sin(2 * math.Pi * frequency * float64(i) / float64(originalRate)))
	}

	result, err := ResampleAudio(input, originalRate, targetRate)
	require.NoError(t, err)

	// Verify frequency is preserved by checking zero crossings
	// A 1kHz sine at 96kHz sample rate should have ~96 samples per cycle
	// Count positive-to-negative zero crossings
	crossings := 0
	for i := 1; i < len(result); i++ {
		if result[i-1] > 0 && result[i] <= 0 {
			crossings++
		}
	}

	// Should have approximately 1000 zero crossings (1kHz * 1 second)
	// Allow 1% tolerance
	assert.InDelta(t, 1000, crossings, 10, "frequency should be preserved after resampling")
}

func TestResampleAudio_BoundaryValues(t *testing.T) {
	// Test that boundary samples are handled correctly
	input := []float32{-1.0, -0.5, 0.0, 0.5, 1.0, 0.5, 0.0, -0.5}

	result, err := ResampleAudio(input, 8000, 16000)
	require.NoError(t, err)

	// First and last samples should be influenced by boundary handling
	// Just verify no NaN or Inf values
	for i, v := range result {
		assert.False(t, math.IsNaN(float64(v)), "sample %d should not be NaN", i)
		assert.False(t, math.IsInf(float64(v), 0), "sample %d should not be Inf", i)
	}
}

func TestResampleAudio_EdgeCases(t *testing.T) {
	t.Run("minimum_samples_for_cubic", func(t *testing.T) {
		// Cubic interpolation needs at least 4 samples
		input := []float32{0.1, 0.2, 0.3, 0.4}

		result, err := ResampleAudio(input, 4000, 8000)
		require.NoError(t, err)
		assert.Len(t, result, 8)

		// Should not panic or produce NaN
		for i, v := range result {
			assert.False(t, math.IsNaN(float64(v)), "sample %d should not be NaN", i)
		}
	})

	t.Run("short_input", func(t *testing.T) {
		input := []float32{0.5, 0.5, 0.5, 0.5, 0.5}

		result, err := ResampleAudio(input, 5000, 10000)
		require.NoError(t, err)
		assert.Len(t, result, 10)
	})

	t.Run("empty_input", func(t *testing.T) {
		input := []float32{}

		result, err := ResampleAudio(input, 48000, 96000)
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("extreme_values", func(t *testing.T) {
		input := []float32{-1.0, 1.0, -1.0, 1.0, -1.0, 1.0, -1.0, 1.0}

		result, err := ResampleAudio(input, 8000, 48000)
		require.NoError(t, err)

		// Cubic interpolation may overshoot, but values should be finite
		for i, v := range result {
			assert.False(t, math.IsNaN(float64(v)), "sample %d should not be NaN", i)
			assert.False(t, math.IsInf(float64(v), 0), "sample %d should not be Inf", i)
		}
	})
}

func TestResampleAudio_Monotonic(t *testing.T) {
	// A monotonically increasing signal should generally remain monotonic
	// (cubic interpolation may have slight overshoot at boundaries)
	input := make([]float32, 1000)
	for i := range input {
		input[i] = float32(i) / 1000.0
	}

	result, err := ResampleAudio(input, 48000, 96000)
	require.NoError(t, err)

	// Check that the trend is generally increasing
	// Allow some tolerance for cubic interpolation artifacts
	increasing := 0
	for i := 1; i < len(result); i++ {
		if result[i] >= result[i-1]-0.01 {
			increasing++
		}
	}
	// At least 95% should be non-decreasing
	assert.Greater(t, float64(increasing)/float64(len(result)-1), 0.95)
}

// =============================================================================
// Benchmarks for ResampleAudio
// =============================================================================

var benchResampleResult []float32

func BenchmarkResampleAudio_CommonRates(b *testing.B) {
	rates := []struct {
		name       string
		origRate   int
		targetRate int
	}{
		{"44100_to_48000", 44100, 48000},
		{"48000_to_44100", 48000, 44100},
		{"16000_to_48000", 16000, 48000},
		{"96000_to_48000", 96000, 48000},
		{"8000_to_48000", 8000, 48000},
	}

	for _, rate := range rates {
		// Generate 1 second of audio at original rate
		input := make([]float32, rate.origRate)
		for i := range input {
			input[i] = float32(math.Sin(2 * math.Pi * 440.0 * float64(i) / float64(rate.origRate)))
		}

		b.Run(rate.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(rate.origRate * 4)) // Input bytes

			for b.Loop() {
				var err error
				benchResampleResult, err = ResampleAudio(input, rate.origRate, rate.targetRate)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkResampleAudio_Sizes(b *testing.B) {
	sizes := []struct {
		name     string
		duration float64 // seconds
	}{
		{"100ms", 0.1},
		{"1s", 1.0},
		{"3s_standard", 3.0},
		{"10s_file", 10.0},
	}

	// Common resampling scenario: 44100 -> 48000
	origRate := 44100
	targetRate := 48000

	for _, sz := range sizes {
		inputLen := int(float64(origRate) * sz.duration)
		input := make([]float32, inputLen)
		for i := range input {
			input[i] = float32(math.Sin(2 * math.Pi * 440.0 * float64(i) / float64(origRate)))
		}

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(inputLen * 4))

			for b.Loop() {
				var err error
				benchResampleResult, err = ResampleAudio(input, origRate, targetRate)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkResampleAudio_SameRate(b *testing.B) {
	// Benchmark the fast path when no resampling is needed
	input := make([]float32, 48000)
	for i := range input {
		input[i] = float32(math.Sin(2 * math.Pi * 440.0 * float64(i) / 48000.0))
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(input) * 4))

	for b.Loop() {
		var err error
		benchResampleResult, err = ResampleAudio(input, 48000, 48000)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkResampleAudio_LargeRatio(b *testing.B) {
	// Extreme upsampling case: 8kHz telephone quality to 48kHz
	origRate := 8000
	targetRate := 48000
	input := make([]float32, origRate) // 1 second

	for i := range input {
		input[i] = float32(math.Sin(2 * math.Pi * 440.0 * float64(i) / float64(origRate)))
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(input) * 4))

	for b.Loop() {
		var err error
		benchResampleResult, err = ResampleAudio(input, origRate, targetRate)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkResampleAudio_ThroughputMBps measures throughput in MB/s
func BenchmarkResampleAudio_ThroughputMBps(b *testing.B) {
	// Standard file processing scenario: 44.1kHz stereo file being resampled
	origRate := 44100
	targetRate := 48000
	duration := 3.0 // 3 seconds (standard buffer)

	inputLen := int(float64(origRate) * duration)
	input := make([]float32, inputLen)
	for i := range input {
		input[i] = float32(math.Sin(2 * math.Pi * 440.0 * float64(i) / float64(origRate)))
	}

	b.ReportAllocs()
	b.SetBytes(int64(inputLen * 4))

	for b.Loop() {
		var err error
		benchResampleResult, err = ResampleAudio(input, origRate, targetRate)
		if err != nil {
			b.Fatal(err)
		}
	}
}
