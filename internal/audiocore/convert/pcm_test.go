package convert_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/convert"
)

// TestBytesToFloat64PCM16 verifies conversion of known PCM16 byte sequences to float64 values.
func TestBytesToFloat64PCM16(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    []byte
		expected []float64
	}{
		{
			name:     "zero sample",
			input:    []byte{0x00, 0x00},
			expected: []float64{0.0},
		},
		{
			name:  "max positive int16 = 32767 → ~0.99997",
			input: []byte{0xFF, 0x7F}, // little-endian 32767
			// 32767 / 32768.0 = 0.9999694824...
			expected: []float64{32767.0 / 32768.0},
		},
		{
			name:  "min int16 = -32768 → -1.0",
			input: []byte{0x00, 0x80}, // little-endian -32768
			// -32768 / 32768.0 = -1.0
			expected: []float64{-1.0},
		},
		{
			name:     "positive value 256 → 0.0078125",
			input:    []byte{0x00, 0x01}, // little-endian 256
			expected: []float64{256.0 / 32768.0},
		},
		{
			name:     "negative value -1 → small negative",
			input:    []byte{0xFF, 0xFF}, // little-endian -1 in int16
			expected: []float64{-1.0 / 32768.0},
		},
		{
			name:     "empty input returns empty slice",
			input:    []byte{},
			expected: []float64{},
		},
		{
			name:     "single byte (too short) returns empty slice",
			input:    []byte{0x01},
			expected: []float64{},
		},
		{
			name: "multiple samples",
			// 0, 16384, -16384 in little-endian
			input: []byte{
				0x00, 0x00, // 0
				0x00, 0x40, // 16384
				0x00, 0xC0, // -16384
			},
			expected: []float64{
				0.0,
				16384.0 / 32768.0,
				-16384.0 / 32768.0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := convert.BytesToFloat64PCM16(tt.input)
			require.Len(t, result, len(tt.expected))
			for i, want := range tt.expected {
				assert.InDelta(t, want, result[i], 1e-9, "index %d", i)
			}
		})
	}
}

// TestBytesToFloat64PCM16Into verifies in-place conversion with a pre-allocated buffer.
func TestBytesToFloat64PCM16Into(t *testing.T) {
	t.Parallel()

	input := []byte{
		0x00, 0x00, // 0
		0xFF, 0x7F, // 32767
		0x00, 0x80, // -32768
	}
	dst := make([]float64, 3)
	convert.BytesToFloat64PCM16Into(dst, input)

	assert.InDelta(t, 0.0, dst[0], 1e-9)
	assert.InDelta(t, 32767.0/32768.0, dst[1], 1e-9)
	assert.InDelta(t, -1.0, dst[2], 1e-9)
}

// TestFloat64ToBytesPCM16 verifies round-trip conversion from float64 back to PCM16 bytes.
func TestFloat64ToBytesPCM16(t *testing.T) {
	t.Parallel()

	t.Run("round-trip zero", func(t *testing.T) {
		t.Parallel()
		input := []byte{0x00, 0x00}
		floats := convert.BytesToFloat64PCM16(input)
		output := make([]byte, 2)
		err := convert.Float64ToBytesPCM16(floats, output)
		require.NoError(t, err)
		assert.Equal(t, input, output)
	})

	t.Run("round-trip positive max", func(t *testing.T) {
		t.Parallel()
		// 32767 → float → should encode back as 32767
		floats := []float64{32767.0 / 32768.0}
		output := make([]byte, 2)
		err := convert.Float64ToBytesPCM16(floats, output)
		require.NoError(t, err)
		// 32767/32768 * 32767 ≈ 32766 (one LSB loss documented)
		got := int16(output[0]) | int16(output[1])<<8
		assert.InDelta(t, int16(32766), got, 1, "one LSB loss is acceptable")
	})

	t.Run("clamp above 1.0", func(t *testing.T) {
		t.Parallel()
		floats := []float64{1.5}
		output := make([]byte, 2)
		err := convert.Float64ToBytesPCM16(floats, output)
		require.NoError(t, err)
		got := int16(output[0]) | int16(output[1])<<8
		assert.Equal(t, int16(32767), got, "1.5 should clamp to 32767")
	})

	t.Run("clamp below -1.0", func(t *testing.T) {
		t.Parallel()
		floats := []float64{-1.5}
		output := make([]byte, 2)
		err := convert.Float64ToBytesPCM16(floats, output)
		require.NoError(t, err)
		got := int16(output[0]) | int16(output[1])<<8
		assert.Equal(t, int16(-32767), got, "-1.5 should clamp to -32767")
	})

	t.Run("empty input is no-op", func(t *testing.T) {
		t.Parallel()
		err := convert.Float64ToBytesPCM16([]float64{}, make([]byte, 0))
		require.NoError(t, err)
	})

	t.Run("output buffer too small returns error", func(t *testing.T) {
		t.Parallel()
		floats := []float64{0.5, 0.5}
		output := make([]byte, 2) // need 4, got 2
		err := convert.Float64ToBytesPCM16(floats, output)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "output buffer too small")
	})
}

// TestSumOfSquaresFloat64 verifies dot product computation.
func TestSumOfSquaresFloat64(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    []float64
		expected float64
	}{
		{
			name:     "empty slice",
			input:    []float64{},
			expected: 0.0,
		},
		{
			name:     "single value",
			input:    []float64{3.0},
			expected: 9.0,
		},
		{
			name:     "multiple values",
			input:    []float64{1.0, 2.0, 3.0},
			expected: 14.0, // 1+4+9
		},
		{
			name:     "negative values (squares are positive)",
			input:    []float64{-2.0, -3.0},
			expected: 13.0, // 4+9
		},
		{
			name:     "unit values",
			input:    []float64{1.0, 1.0, 1.0, 1.0},
			expected: 4.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := convert.SumOfSquaresFloat64(tt.input)
			assert.InDelta(t, tt.expected, result, 1e-9)
		})
	}
}

// TestClampFloat64Slice verifies in-place clamping to [-1.0, 1.0].
func TestClampFloat64Slice(t *testing.T) {
	t.Parallel()

	t.Run("values above 1.0 are clamped", func(t *testing.T) {
		t.Parallel()
		samples := []float64{1.5, 2.0, 100.0}
		convert.ClampFloat64Slice(samples)
		for i, v := range samples {
			assert.LessOrEqualf(t, v, 1.0, "index %d should be <= 1.0", i)
		}
	})

	t.Run("values below -1.0 are clamped", func(t *testing.T) {
		t.Parallel()
		samples := []float64{-1.5, -2.0, -100.0}
		convert.ClampFloat64Slice(samples)
		for i, v := range samples {
			assert.GreaterOrEqualf(t, v, -1.0, "index %d should be >= -1.0", i)
		}
	})

	t.Run("values within range are unchanged", func(t *testing.T) {
		t.Parallel()
		samples := []float64{-1.0, -0.5, 0.0, 0.5, 1.0}
		original := make([]float64, len(samples))
		copy(original, samples)
		convert.ClampFloat64Slice(samples)
		assert.Equal(t, original, samples)
	})

	t.Run("empty slice is no-op", func(t *testing.T) {
		t.Parallel()
		convert.ClampFloat64Slice([]float64{})
	})

	t.Run("exact boundary values remain", func(t *testing.T) {
		t.Parallel()
		samples := []float64{-1.0, 1.0}
		convert.ClampFloat64Slice(samples)
		assert.InDelta(t, -1.0, samples[0], 1e-9)
		assert.InDelta(t, 1.0, samples[1], 1e-9)
	})
}

// TestConvertToFloat32 verifies 16-bit PCM to float32 conversion.
func TestConvertToFloat32(t *testing.T) {
	t.Parallel()

	t.Run("16-bit zero sample", func(t *testing.T) {
		t.Parallel()
		input := []byte{0x00, 0x00}
		result, err := convert.ConvertToFloat32(input, 16)
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.Len(t, result[0], 1)
		assert.InDelta(t, float32(0.0), result[0][0], 1e-6)
	})

	t.Run("16-bit positive max", func(t *testing.T) {
		t.Parallel()
		input := []byte{0xFF, 0x7F} // 32767 little-endian
		result, err := convert.ConvertToFloat32(input, 16)
		require.NoError(t, err)
		require.Len(t, result[0], 1)
		assert.InDelta(t, float32(32767)/float32(32768), result[0][0], 1e-5)
	})

	t.Run("16-bit min value -32768", func(t *testing.T) {
		t.Parallel()
		input := []byte{0x00, 0x80} // -32768 little-endian
		result, err := convert.ConvertToFloat32(input, 16)
		require.NoError(t, err)
		require.Len(t, result[0], 1)
		assert.InDelta(t, float32(-1.0), result[0][0], 1e-6)
	})

	t.Run("24-bit zero sample", func(t *testing.T) {
		t.Parallel()
		input := []byte{0x00, 0x00, 0x00}
		result, err := convert.ConvertToFloat32(input, 24)
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.Len(t, result[0], 1)
		assert.InDelta(t, float32(0.0), result[0][0], 1e-6)
	})

	t.Run("32-bit zero sample", func(t *testing.T) {
		t.Parallel()
		input := []byte{0x00, 0x00, 0x00, 0x00}
		result, err := convert.ConvertToFloat32(input, 32)
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.Len(t, result[0], 1)
		assert.InDelta(t, float32(0.0), result[0][0], 1e-6)
	})

	t.Run("unsupported bit depth returns error", func(t *testing.T) {
		t.Parallel()
		_, err := convert.ConvertToFloat32([]byte{0x00}, 8)
		require.Error(t, err)
	})

	t.Run("16-bit multiple samples", func(t *testing.T) {
		t.Parallel()
		// 0, 16384, -16384 in little-endian int16
		input := []byte{
			0x00, 0x00,
			0x00, 0x40, // 16384
			0x00, 0xC0, // -16384 (0xC000 as uint16)
		}
		result, err := convert.ConvertToFloat32(input, 16)
		require.NoError(t, err)
		require.Len(t, result[0], 3)
		assert.InDelta(t, float32(0.0), result[0][0], 1e-6)
		assert.InDelta(t, float32(16384)/float32(32768), result[0][1], 1e-5)
		assert.InDelta(t, float32(-16384)/float32(32768), result[0][2], 1e-5)
	})

	t.Run("24-bit sign extension for negative", func(t *testing.T) {
		t.Parallel()
		// -1 in 24-bit little-endian = 0xFF, 0xFF, 0xFF
		input := []byte{0xFF, 0xFF, 0xFF}
		result, err := convert.ConvertToFloat32(input, 24)
		require.NoError(t, err)
		// -1 / 8388608 ≈ -1.19e-7
		assert.Less(t, result[0][0], float32(0.0), "should be negative")
	})
}

// TestCalculateRMSFloat64 verifies root mean square calculation.
func TestCalculateRMSFloat64(t *testing.T) {
	t.Parallel()

	t.Run("empty slice", func(t *testing.T) {
		t.Parallel()
		assert.InDelta(t, 0.0, convert.CalculateRMSFloat64([]float64{}), 1e-9)
	})

	t.Run("unit values", func(t *testing.T) {
		t.Parallel()
		samples := []float64{1.0, 1.0, 1.0, 1.0}
		assert.InDelta(t, 1.0, convert.CalculateRMSFloat64(samples), 1e-9)
	})

	t.Run("known RMS", func(t *testing.T) {
		t.Parallel()
		// [3, 4] → sqrt((9+16)/2) = sqrt(12.5) ≈ 3.5355...
		samples := []float64{3.0, 4.0}
		expected := math.Sqrt(12.5)
		assert.InDelta(t, expected, convert.CalculateRMSFloat64(samples), 1e-9)
	})
}

// TestClampFloat64 verifies scalar clamp function.
func TestClampFloat64(t *testing.T) {
	t.Parallel()

	assert.InDelta(t, 1.0, convert.ClampFloat64(1.5), 1e-9)
	assert.InDelta(t, -1.0, convert.ClampFloat64(-1.5), 1e-9)
	assert.InDelta(t, 0.5, convert.ClampFloat64(0.5), 1e-9)
	assert.InDelta(t, 1.0, convert.ClampFloat64(1.0), 1e-9)
	assert.InDelta(t, -1.0, convert.ClampFloat64(-1.0), 1e-9)
}

// TestMinFloat64 verifies minimum value with SIMD.
func TestMinFloat64(t *testing.T) {
	t.Parallel()

	assert.InDelta(t, 0.0, convert.MinFloat64([]float64{}), 1e-9)
	assert.InDelta(t, -3.0, convert.MinFloat64([]float64{1.0, -3.0, 2.0}), 1e-9)
	assert.InDelta(t, 5.0, convert.MinFloat64([]float64{5.0}), 1e-9)
}

// TestMaxFloat64 verifies maximum value with SIMD.
func TestMaxFloat64(t *testing.T) {
	t.Parallel()

	assert.InDelta(t, 0.0, convert.MaxFloat64([]float64{}), 1e-9)
	assert.InDelta(t, 3.0, convert.MaxFloat64([]float64{1.0, 3.0, -2.0}), 1e-9)
	assert.InDelta(t, 5.0, convert.MaxFloat64([]float64{5.0}), 1e-9)
}

// TestSumFloat64 verifies sum with SIMD.
func TestSumFloat64(t *testing.T) {
	t.Parallel()

	assert.InDelta(t, 0.0, convert.SumFloat64([]float64{}), 1e-9)
	assert.InDelta(t, 6.0, convert.SumFloat64([]float64{1.0, 2.0, 3.0}), 1e-9)
	assert.InDelta(t, -1.0, convert.SumFloat64([]float64{1.0, -2.0}), 1e-9)
}

// TestMeanFloat64 verifies arithmetic mean with SIMD.
func TestMeanFloat64(t *testing.T) {
	t.Parallel()

	assert.InDelta(t, 0.0, convert.MeanFloat64([]float64{}), 1e-9)
	assert.InDelta(t, 2.0, convert.MeanFloat64([]float64{1.0, 2.0, 3.0}), 1e-9)
}

// TestMinMaxSumFloat64 verifies combined min/max/sum operation.
func TestMinMaxSumFloat64(t *testing.T) {
	t.Parallel()

	t.Run("empty slice", func(t *testing.T) {
		t.Parallel()
		mn, mx, sum := convert.MinMaxSumFloat64([]float64{})
		assert.InDelta(t, 0.0, mn, 1e-9)
		assert.InDelta(t, 0.0, mx, 1e-9)
		assert.InDelta(t, 0.0, sum, 1e-9)
	})

	t.Run("multiple values", func(t *testing.T) {
		t.Parallel()
		mn, mx, sum := convert.MinMaxSumFloat64([]float64{1.0, -2.0, 3.0})
		assert.InDelta(t, -2.0, mn, 1e-9)
		assert.InDelta(t, 3.0, mx, 1e-9)
		assert.InDelta(t, 2.0, sum, 1e-9)
	})
}

// TestScaleFloat64Slice verifies in-place scalar multiplication.
func TestScaleFloat64Slice(t *testing.T) {
	t.Parallel()

	t.Run("scale by 2", func(t *testing.T) {
		t.Parallel()
		samples := []float64{1.0, 2.0, 3.0}
		convert.ScaleFloat64Slice(samples, 2.0)
		assert.InDelta(t, 2.0, samples[0], 1e-9)
		assert.InDelta(t, 4.0, samples[1], 1e-9)
		assert.InDelta(t, 6.0, samples[2], 1e-9)
	})

	t.Run("empty slice is no-op", func(t *testing.T) {
		t.Parallel()
		convert.ScaleFloat64Slice([]float64{}, 2.0)
	})
}

// TestGetFileExtension verifies format-to-extension mapping.
func TestGetFileExtension(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "m4a", convert.GetFileExtension("aac"))
	assert.Equal(t, "wav", convert.GetFileExtension("wav"))
	assert.Equal(t, "mp3", convert.GetFileExtension("mp3"))
	assert.Equal(t, "flac", convert.GetFileExtension("flac"))
}
