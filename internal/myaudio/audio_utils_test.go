package myaudio

import (
	"encoding/binary"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Unit Tests for SumOfSquaresFloat64
// =============================================================================

func TestSumOfSquaresFloat64(t *testing.T) {
	tests := []struct {
		name     string
		samples  []float64
		expected float64
	}{
		{"empty", []float64{}, 0.0},
		{"single_one", []float64{1.0}, 1.0},
		{"single_two", []float64{2.0}, 4.0},
		{"two_values", []float64{3.0, 4.0}, 25.0}, // 9 + 16
		{"negative_values", []float64{-3.0, -4.0}, 25.0},
		{"mixed_signs", []float64{3.0, -4.0}, 25.0},
		{"zeros", []float64{0.0, 0.0, 0.0}, 0.0},
		{"half_values", []float64{0.5, 0.5}, 0.5}, // 0.25 + 0.25
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SumOfSquaresFloat64(tt.samples)
			assert.InDelta(t, tt.expected, result, 1e-10)
		})
	}
}

// =============================================================================
// Unit Tests for CalculateRMSFloat64
// =============================================================================

func TestCalculateRMSFloat64(t *testing.T) {
	tests := []struct {
		name     string
		samples  []float64
		expected float64
	}{
		{"empty", []float64{}, 0.0},
		{"single_one", []float64{1.0}, 1.0},
		{"two_same", []float64{2.0, 2.0}, 2.0},
		{"pythagorean", []float64{3.0, 4.0}, math.Sqrt(12.5)}, // sqrt((9+16)/2)
		{"all_zeros", []float64{0.0, 0.0, 0.0}, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateRMSFloat64(tt.samples)
			assert.InDelta(t, tt.expected, result, 1e-10)
		})
	}
}

// =============================================================================
// Unit Tests for ClampFloat64
// =============================================================================

func TestClampFloat64(t *testing.T) {
	tests := []struct {
		name     string
		input    float64
		expected float64
	}{
		{"zero", 0.0, 0.0},
		{"mid_positive", 0.5, 0.5},
		{"mid_negative", -0.5, -0.5},
		{"exactly_one", 1.0, 1.0},
		{"exactly_minus_one", -1.0, -1.0},
		{"above_one", 1.5, 1.0},
		{"below_minus_one", -1.5, -1.0},
		{"large_positive", 100.0, 1.0},
		{"large_negative", -100.0, -1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ClampFloat64(tt.input)
			assert.InDelta(t, tt.expected, result, 1e-10)
		})
	}
}

// =============================================================================
// Unit Tests for ClampFloat64Slice
// =============================================================================

func TestClampFloat64Slice(t *testing.T) {
	t.Run("all_in_range", func(t *testing.T) {
		samples := []float64{0.0, 0.5, -0.5, 0.9, -0.9}
		expected := []float64{0.0, 0.5, -0.5, 0.9, -0.9}
		ClampFloat64Slice(samples)
		assert.InDeltaSlice(t, expected, samples, 1e-10)
	})

	t.Run("all_above", func(t *testing.T) {
		samples := []float64{1.5, 2.0, 100.0}
		expected := []float64{1.0, 1.0, 1.0}
		ClampFloat64Slice(samples)
		assert.InDeltaSlice(t, expected, samples, 1e-10)
	})

	t.Run("all_below", func(t *testing.T) {
		samples := []float64{-1.5, -2.0, -100.0}
		expected := []float64{-1.0, -1.0, -1.0}
		ClampFloat64Slice(samples)
		assert.InDeltaSlice(t, expected, samples, 1e-10)
	})

	t.Run("mixed", func(t *testing.T) {
		samples := []float64{-2.0, -0.5, 0.0, 0.5, 2.0}
		expected := []float64{-1.0, -0.5, 0.0, 0.5, 1.0}
		ClampFloat64Slice(samples)
		assert.InDeltaSlice(t, expected, samples, 1e-10)
	})

	t.Run("empty", func(t *testing.T) {
		samples := []float64{}
		ClampFloat64Slice(samples) // Should not panic
		assert.Empty(t, samples)
	})
}

// =============================================================================
// Unit Tests for Min/Max/Sum/Mean
// =============================================================================

func TestMinFloat64(t *testing.T) {
	tests := []struct {
		name     string
		samples  []float64
		expected float64
	}{
		{"empty", []float64{}, 0.0},
		{"single", []float64{5.0}, 5.0},
		{"positive", []float64{1.0, 2.0, 3.0}, 1.0},
		{"negative", []float64{-1.0, -2.0, -3.0}, -3.0},
		{"mixed", []float64{-1.0, 0.0, 1.0}, -1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MinFloat64(tt.samples)
			assert.InDelta(t, tt.expected, result, 1e-10)
		})
	}
}

func TestMaxFloat64(t *testing.T) {
	tests := []struct {
		name     string
		samples  []float64
		expected float64
	}{
		{"empty", []float64{}, 0.0},
		{"single", []float64{5.0}, 5.0},
		{"positive", []float64{1.0, 2.0, 3.0}, 3.0},
		{"negative", []float64{-1.0, -2.0, -3.0}, -1.0},
		{"mixed", []float64{-1.0, 0.0, 1.0}, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MaxFloat64(tt.samples)
			assert.InDelta(t, tt.expected, result, 1e-10)
		})
	}
}

func TestSumFloat64(t *testing.T) {
	tests := []struct {
		name     string
		samples  []float64
		expected float64
	}{
		{"empty", []float64{}, 0.0},
		{"single", []float64{5.0}, 5.0},
		{"multiple", []float64{1.0, 2.0, 3.0}, 6.0},
		{"negative", []float64{-1.0, -2.0}, -3.0},
		{"mixed", []float64{-1.0, 1.0}, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SumFloat64(tt.samples)
			assert.InDelta(t, tt.expected, result, 1e-10)
		})
	}
}

func TestMeanFloat64(t *testing.T) {
	tests := []struct {
		name     string
		samples  []float64
		expected float64
	}{
		{"empty", []float64{}, 0.0},
		{"single", []float64{5.0}, 5.0},
		{"multiple", []float64{1.0, 2.0, 3.0}, 2.0},
		{"mixed", []float64{-1.0, 1.0}, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MeanFloat64(tt.samples)
			assert.InDelta(t, tt.expected, result, 1e-10)
		})
	}
}

func TestMinMaxSumFloat64(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		minVal, maxVal, sum := MinMaxSumFloat64([]float64{})
		assert.InDelta(t, 0.0, minVal, 1e-10)
		assert.InDelta(t, 0.0, maxVal, 1e-10)
		assert.InDelta(t, 0.0, sum, 1e-10)
	})

	t.Run("single", func(t *testing.T) {
		minVal, maxVal, sum := MinMaxSumFloat64([]float64{5.0})
		assert.InDelta(t, 5.0, minVal, 1e-10)
		assert.InDelta(t, 5.0, maxVal, 1e-10)
		assert.InDelta(t, 5.0, sum, 1e-10)
	})

	t.Run("multiple", func(t *testing.T) {
		minVal, maxVal, sum := MinMaxSumFloat64([]float64{-1.0, 0.0, 2.0, 5.0})
		assert.InDelta(t, -1.0, minVal, 1e-10)
		assert.InDelta(t, 5.0, maxVal, 1e-10)
		assert.InDelta(t, 6.0, sum, 1e-10)
	})
}

// =============================================================================
// Unit Tests for BytesToFloat64PCM16 and Float64ToBytesPCM16
// =============================================================================

func TestBytesToFloat64PCM16(t *testing.T) {
	t.Run("zero", func(t *testing.T) {
		result := BytesToFloat64PCM16([]byte{0x00, 0x00})
		assert.Len(t, result, 1)
		assert.InDelta(t, 0.0, result[0], 1e-10)
	})

	t.Run("max_positive", func(t *testing.T) {
		result := BytesToFloat64PCM16([]byte{0xFF, 0x7F}) // 32767
		assert.Len(t, result, 1)
		assert.InDelta(t, 32767.0/32768.0, result[0], 1e-5)
	})

	t.Run("max_negative", func(t *testing.T) {
		result := BytesToFloat64PCM16([]byte{0x00, 0x80}) // -32768
		assert.Len(t, result, 1)
		assert.InDelta(t, -1.0, result[0], 1e-5)
	})

	t.Run("empty", func(t *testing.T) {
		result := BytesToFloat64PCM16([]byte{})
		assert.Empty(t, result)
	})

	t.Run("single_byte", func(t *testing.T) {
		result := BytesToFloat64PCM16([]byte{0x00})
		assert.Empty(t, result)
	})

	t.Run("odd_length_ignores_trailing_byte", func(t *testing.T) {
		// 5 bytes: 2 complete samples + 1 trailing byte that should be ignored
		result := BytesToFloat64PCM16([]byte{0x00, 0x00, 0x00, 0x40, 0xFF})
		assert.Len(t, result, 2) // Only 2 samples, trailing byte ignored
		assert.InDelta(t, 0.0, result[0], 1e-10)
		assert.InDelta(t, 0.5, result[1], 1e-5) // 0x4000 = 16384 -> 0.5
	})
}

func TestFloat64ToBytesPCM16(t *testing.T) {
	t.Run("zero", func(t *testing.T) {
		output := make([]byte, 2)
		err := Float64ToBytesPCM16([]float64{0.0}, output)
		require.NoError(t, err)
		assert.Equal(t, []byte{0x00, 0x00}, output)
	})

	t.Run("positive_one", func(t *testing.T) {
		output := make([]byte, 2)
		err := Float64ToBytesPCM16([]float64{1.0}, output)
		require.NoError(t, err)
		// Should be clamped to 32767
		val := int16(binary.LittleEndian.Uint16(output)) //nolint:gosec // G115: intentional uint16→int16 for PCM test verification
		assert.Equal(t, int16(32767), val)
	})

	t.Run("negative_one", func(t *testing.T) {
		output := make([]byte, 2)
		err := Float64ToBytesPCM16([]float64{-1.0}, output)
		require.NoError(t, err)
		val := int16(binary.LittleEndian.Uint16(output)) //nolint:gosec // G115: intentional uint16→int16 for PCM test verification
		assert.Equal(t, int16(-32767), val)
	})

	t.Run("clamps_above_one", func(t *testing.T) {
		output := make([]byte, 2)
		err := Float64ToBytesPCM16([]float64{1.5}, output)
		require.NoError(t, err)
		val := int16(binary.LittleEndian.Uint16(output)) //nolint:gosec // G115: intentional uint16→int16 for PCM test verification
		assert.Equal(t, int16(32767), val)               // Clamped to 1.0
	})

	t.Run("buffer_too_small", func(t *testing.T) {
		output := make([]byte, 1) // Too small for 1 sample
		err := Float64ToBytesPCM16([]float64{0.5}, output)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "output buffer too small")
	})
}

func TestFloat64ToBytesPCM16_RoundTrip(t *testing.T) {
	original := []float64{0.0, 0.5, -0.5, 0.999, -0.999}
	bytes := make([]byte, len(original)*2)
	err := Float64ToBytesPCM16(original, bytes)
	require.NoError(t, err)

	result := BytesToFloat64PCM16(bytes)
	assert.Len(t, result, len(original))

	for i := range original {
		// Allow small error due to quantization
		assert.InDelta(t, original[i], result[i], 0.001, "sample %d", i)
	}
}

// =============================================================================
// Unit Tests for ScaleFloat64Slice
// =============================================================================

func TestScaleFloat64Slice(t *testing.T) {
	t.Run("scale_by_two", func(t *testing.T) {
		samples := []float64{1.0, 2.0, 3.0}
		ScaleFloat64Slice(samples, 2.0)
		assert.Equal(t, []float64{2.0, 4.0, 6.0}, samples)
	})

	t.Run("scale_by_half", func(t *testing.T) {
		samples := []float64{2.0, 4.0, 6.0}
		ScaleFloat64Slice(samples, 0.5)
		assert.Equal(t, []float64{1.0, 2.0, 3.0}, samples)
	})

	t.Run("scale_by_zero", func(t *testing.T) {
		samples := []float64{1.0, 2.0, 3.0}
		ScaleFloat64Slice(samples, 0.0)
		assert.Equal(t, []float64{0.0, 0.0, 0.0}, samples)
	})

	t.Run("empty", func(t *testing.T) {
		samples := []float64{}
		ScaleFloat64Slice(samples, 2.0) // Should not panic
		assert.Empty(t, samples)
	})
}

// =============================================================================
// Benchmarks for Audio Utility Functions
// =============================================================================

var benchUtilResult float64
var benchUtilSlice []float64

func BenchmarkSumOfSquaresFloat64_Sizes(b *testing.B) {
	sizes := []int{1000, 48000, 144000}

	for _, size := range sizes {
		samples := make([]float64, size)
		for i := range samples {
			samples[i] = math.Sin(2 * math.Pi * 440.0 * float64(i) / 48000.0)
		}

		b.Run(formatSize(size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size * 8))

			for b.Loop() {
				benchUtilResult = SumOfSquaresFloat64(samples)
			}
		})
	}
}

func BenchmarkMinMaxSumFloat64_Sizes(b *testing.B) {
	sizes := []int{10, 60, 1000}

	for _, size := range sizes {
		samples := make([]float64, size)
		for i := range samples {
			samples[i] = rand.Float64()*200 - 100 //nolint:gosec // G404: math/rand is fine for test data
		}

		b.Run(formatSize(size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size * 8))

			for b.Loop() {
				_, _, benchUtilResult = MinMaxSumFloat64(samples)
			}
		})
	}
}

func BenchmarkScaleFloat64Slice_Sizes(b *testing.B) {
	sizes := []int{1000, 48000, 144000}

	for _, size := range sizes {
		samples := make([]float64, size)
		for i := range samples {
			samples[i] = rand.Float64()*2 - 1 //nolint:gosec // G404: math/rand is fine for test data
		}

		b.Run(formatSize(size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size * 8))

			for b.Loop() {
				ScaleFloat64Slice(samples, 0.5)
				benchUtilSlice = samples
			}
		})
	}
}

func BenchmarkBytesToFloat64PCM16_Sizes(b *testing.B) {
	sizes := []int{1000, 48000, 144000}

	for _, size := range sizes {
		bytes := make([]byte, size*2)
		for i := range size {
			val := int16(math.Sin(2*math.Pi*440.0*float64(i)/48000.0) * 32767) //nolint:gosec // G115: sin*32767 is always in int16 range
			binary.LittleEndian.PutUint16(bytes[i*2:], uint16(val))            //nolint:gosec // G115: intentional int16→uint16 for PCM test data
		}

		b.Run(formatSize(size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size * 2))

			for b.Loop() {
				benchUtilSlice = BytesToFloat64PCM16(bytes)
			}
		})
	}
}

func formatSize(size int) string {
	switch {
	case size >= 144000:
		return "144000_3sec"
	case size >= 48000:
		return "48000_1sec"
	default:
		return "1000"
	}
}
