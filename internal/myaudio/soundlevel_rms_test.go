package myaudio

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// =============================================================================
// Unit Tests for calculateRMS
// =============================================================================

func TestCalculateRMS_BasicCases(t *testing.T) {
	tests := []struct {
		name     string
		samples  []float64
		expected float64
		delta    float64
	}{
		{
			name:     "empty_slice",
			samples:  []float64{},
			expected: 0.0,
			delta:    0.0,
		},
		{
			name:     "single_positive",
			samples:  []float64{1.0},
			expected: 1.0,
			delta:    1e-10,
		},
		{
			name:     "single_negative",
			samples:  []float64{-1.0},
			expected: 1.0,
			delta:    1e-10,
		},
		{
			name:     "two_opposite_samples",
			samples:  []float64{1.0, -1.0},
			expected: 1.0,
			delta:    1e-10,
		},
		{
			name:     "four_identical_samples",
			samples:  []float64{0.5, 0.5, 0.5, 0.5},
			expected: 0.5,
			delta:    1e-10,
		},
		{
			name:     "all_zeros",
			samples:  []float64{0.0, 0.0, 0.0, 0.0},
			expected: 0.0,
			delta:    0.0,
		},
		{
			name:     "mixed_values",
			samples:  []float64{0.3, -0.4, 0.5, -0.6},
			expected: math.Sqrt((0.09 + 0.16 + 0.25 + 0.36) / 4), // sqrt(0.215) ≈ 0.4637
			delta:    1e-10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateRMS(tt.samples)
			assert.InDelta(t, tt.expected, result, tt.delta)
		})
	}
}

func TestCalculateRMS_SineWave(t *testing.T) {
	// For a pure sine wave, RMS = amplitude / sqrt(2)
	amplitudes := []float64{0.1, 0.5, 1.0}
	numSamples := 48000 // One full second

	for _, amplitude := range amplitudes {
		t.Run("amplitude_"+formatFloat(amplitude), func(t *testing.T) {
			samples := make([]float64, numSamples)
			// Generate full cycles of sine wave (use multiple cycles for accuracy)
			frequency := 1000.0 // 1kHz
			sampleRate := 48000.0
			for i := range numSamples {
				samples[i] = amplitude * math.Sin(2*math.Pi*frequency*float64(i)/sampleRate)
			}

			result := calculateRMS(samples)
			expected := amplitude / math.Sqrt(2)
			// Allow 0.1% error due to discrete sampling
			assert.InDelta(t, expected, result, expected*0.001)
		})
	}
}

func TestCalculateRMS_EdgeCases(t *testing.T) {
	t.Run("very_small_values", func(t *testing.T) {
		samples := []float64{1e-10, 1e-10, 1e-10, 1e-10}
		result := calculateRMS(samples)
		assert.InDelta(t, 1e-10, result, 1e-15)
	})

	t.Run("very_large_values", func(t *testing.T) {
		samples := []float64{1e5, 1e5, 1e5, 1e5}
		result := calculateRMS(samples)
		assert.InDelta(t, 1e5, result, 1e-5)
	})

	t.Run("subnormal_values", func(t *testing.T) {
		// Test with very small values (not truly subnormal to avoid precision issues)
		samples := []float64{1e-150, 1e-150, 1e-150, 1e-150}
		result := calculateRMS(samples)
		// Use relative tolerance for very small numbers
		assert.InDelta(t, 1e-150, result, 1e-150*0.001)
	})

	t.Run("alternating_large_small", func(t *testing.T) {
		samples := []float64{1.0, 0.001, 1.0, 0.001}
		// RMS = sqrt((1 + 0.000001 + 1 + 0.000001) / 4) = sqrt(0.5000005)
		expected := math.Sqrt((1.0 + 0.000001 + 1.0 + 0.000001) / 4)
		result := calculateRMS(samples)
		assert.InDelta(t, expected, result, 1e-10)
	})
}

func TestCalculateRMS_AudioRealistic(t *testing.T) {
	t.Run("silence", func(t *testing.T) {
		samples := make([]float64, 48000)
		result := calculateRMS(samples)
		assert.InDelta(t, 0.0, result, 1e-10)
	})

	t.Run("white_noise", func(t *testing.T) {
		// Generate random values in [-1, 1]
		samples := make([]float64, 48000)
		for i := range samples {
			samples[i] = rand.Float64()*2 - 1
		}
		result := calculateRMS(samples)
		// White noise RMS should be close to 1/sqrt(3) ≈ 0.577
		// Allow 5% tolerance due to randomness
		assert.InDelta(t, 1.0/math.Sqrt(3), result, 0.05)
	})

	t.Run("typical_audio_level", func(t *testing.T) {
		// -20dB signal (amplitude ~0.1)
		amplitude := 0.1
		samples := make([]float64, 48000)
		for i := range samples {
			samples[i] = amplitude * math.Sin(2*math.Pi*440.0*float64(i)/48000.0)
		}
		result := calculateRMS(samples)
		expected := amplitude / math.Sqrt(2)
		assert.InDelta(t, expected, result, expected*0.01)
	})
}

// Helper to format float for test names (e.g., 0.5 -> "0p5", 1.0 -> "1p0")
func formatFloat(f float64) string {
	return strings.ReplaceAll(fmt.Sprintf("%.1f", f), ".", "p")
}

// =============================================================================
// Benchmarks for calculateRMS
// =============================================================================

// Prevent compiler optimization
var benchRMSResult float64

func BenchmarkCalculateRMS_Sizes(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"1000_samples", 1000},
		{"48000_samples_1sec", 48000},
		{"144000_samples_3sec", 144000},
		{"288000_samples_6sec", 288000},
	}

	for _, sz := range sizes {
		// Pre-generate test data outside the benchmark loop
		samples := make([]float64, sz.size)
		for i := range samples {
			samples[i] = math.Sin(2 * math.Pi * 440.0 * float64(i) / 48000.0)
		}

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(sz.size * 8)) // 8 bytes per float64

			for b.Loop() {
				benchRMSResult = calculateRMS(samples)
			}
		})
	}
}

func BenchmarkCalculateRMS_DataPatterns(b *testing.B) {
	const size = 48000

	patterns := []struct {
		name   string
		create func() []float64
	}{
		{
			name: "zeros",
			create: func() []float64 {
				return make([]float64, size)
			},
		},
		{
			name: "sequential",
			create: func() []float64 {
				samples := make([]float64, size)
				for i := range samples {
					samples[i] = float64(i) / float64(size)
				}
				return samples
			},
		},
		{
			name: "random",
			create: func() []float64 {
				samples := make([]float64, size)
				for i := range samples {
					samples[i] = rand.Float64()*2 - 1
				}
				return samples
			},
		},
		{
			name: "sine_wave",
			create: func() []float64 {
				samples := make([]float64, size)
				for i := range samples {
					samples[i] = math.Sin(2 * math.Pi * 440.0 * float64(i) / 48000.0)
				}
				return samples
			},
		},
		{
			name: "alternating_signs",
			create: func() []float64 {
				samples := make([]float64, size)
				for i := range samples {
					if i%2 == 0 {
						samples[i] = 0.5
					} else {
						samples[i] = -0.5
					}
				}
				return samples
			},
		},
	}

	for _, pattern := range patterns {
		samples := pattern.create()

		b.Run(pattern.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size * 8))

			for b.Loop() {
				benchRMSResult = calculateRMS(samples)
			}
		})
	}
}

func BenchmarkCalculateRMS_MultipleBands(b *testing.B) {
	// Simulate processing multiple octave bands
	const size = 48000
	const numBands = 27 // Typical number of 1/3 octave bands

	// Pre-generate test data for all bands
	bands := make([][]float64, numBands)
	for i := range bands {
		bands[i] = make([]float64, size)
		freq := 25.0 * math.Pow(2, float64(i)/3) // 1/3 octave frequencies
		for j := range bands[i] {
			bands[i][j] = math.Sin(2 * math.Pi * freq * float64(j) / 48000.0)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		// Process all bands sequentially (current implementation)
		for _, band := range bands {
			benchRMSResult = calculateRMS(band)
		}
	}
}

// BenchmarkCalculateRMS_ThroughputPerSecond measures realistic throughput
func BenchmarkCalculateRMS_ThroughputPerSecond(b *testing.B) {
	// Simulate 1 second of real-time processing:
	// - 27 octave bands
	// - Each with 48000 samples (1 second buffer)
	const numBands = 27
	const samplesPerBand = 48000

	bands := make([][]float64, numBands)
	for i := range bands {
		bands[i] = make([]float64, samplesPerBand)
		for j := range bands[i] {
			bands[i][j] = rand.Float64()*2 - 1
		}
	}

	b.ResetTimer()
	b.ReportAllocs()
	// Total samples processed per iteration
	b.SetBytes(int64(numBands * samplesPerBand * 8))

	for b.Loop() {
		for _, band := range bands {
			benchRMSResult = calculateRMS(band)
		}
	}
}
