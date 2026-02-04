package myaudio

import (
	"encoding/binary"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// =============================================================================
// Unit Tests for Clamping (using exported SIMD functions)
// Note: Basic ClampFloat64 tests are in audio_utils_test.go
// =============================================================================

func TestClampFloat64_BoundaryPrecision(t *testing.T) {
	t.Run("just_above_1", func(t *testing.T) {
		result := ClampFloat64(1.0000001)
		assert.InDelta(t, 1.0, result, 1e-10)
	})

	t.Run("just_below_minus1", func(t *testing.T) {
		result := ClampFloat64(-1.0000001)
		assert.InDelta(t, -1.0, result, 1e-10)
	})

	t.Run("just_below_1", func(t *testing.T) {
		input := 0.9999999
		result := ClampFloat64(input)
		assert.InDelta(t, input, result, 1e-10, "value just below 1 should not be clamped")
	})

	t.Run("just_above_minus1", func(t *testing.T) {
		input := -0.9999999
		result := ClampFloat64(input)
		assert.InDelta(t, input, result, 1e-10, "value just above -1 should not be clamped")
	})
}

func TestClampFloat64Slice_BasicCases(t *testing.T) {
	t.Run("all_within_range", func(t *testing.T) {
		samples := []float64{0.0, 0.5, -0.5, 0.9, -0.9}
		expected := make([]float64, len(samples))
		copy(expected, samples)

		ClampFloat64Slice(samples)
		assert.InDeltaSlice(t, expected, samples, 1e-10)
	})

	t.Run("all_above_range", func(t *testing.T) {
		samples := []float64{1.5, 2.0, 3.0, 100.0}
		expected := []float64{1.0, 1.0, 1.0, 1.0}

		ClampFloat64Slice(samples)
		assert.InDeltaSlice(t, expected, samples, 1e-10)
	})

	t.Run("all_below_range", func(t *testing.T) {
		samples := []float64{-1.5, -2.0, -3.0, -100.0}
		expected := []float64{-1.0, -1.0, -1.0, -1.0}

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
// Unit Tests for Byte-to-Float64 Conversion (using exported SIMD functions)
// =============================================================================

func TestBytesToFloat64_BasicCases(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected []float64
		delta    float64
	}{
		{
			name:     "zero",
			input:    []byte{0x00, 0x00},
			expected: []float64{0.0},
			delta:    1e-10,
		},
		{
			name:     "max_positive",
			input:    []byte{0xFF, 0x7F}, // 32767
			expected: []float64{32767.0 / 32768.0},
			delta:    1e-5,
		},
		{
			name:     "max_negative",
			input:    []byte{0x00, 0x80}, // -32768
			expected: []float64{-1.0},
			delta:    1e-10,
		},
		{
			name:     "half_positive",
			input:    []byte{0x00, 0x40}, // 16384
			expected: []float64{0.5},
			delta:    1e-5,
		},
		{
			name:     "half_negative",
			input:    []byte{0x00, 0xC0}, // -16384
			expected: []float64{-0.5},
			delta:    1e-5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BytesToFloat64PCM16(tt.input)
			require.Len(t, result, len(tt.expected))
			for i := range tt.expected {
				assert.InDelta(t, tt.expected[i], result[i], tt.delta)
			}
		})
	}
}

func TestBytesToFloat64_RoundTrip(t *testing.T) {
	// Test that conversion to float64 and back preserves values
	original := []byte{0x00, 0x40, 0x00, 0xC0, 0xFF, 0x7F, 0x00, 0x80}

	floatSamples := BytesToFloat64PCM16(original)
	output := make([]byte, len(original))
	err := Float64ToBytesPCM16(floatSamples, output)
	require.NoError(t, err)

	// The round trip may lose 1 LSB due to 32767 vs 32768 scaling
	for i := 0; i < len(original); i += 2 {
		origVal := int16(binary.LittleEndian.Uint16(original[i:])) //nolint:gosec // G115: intentional uint16→int16 for PCM test verification
		outVal := int16(binary.LittleEndian.Uint16(output[i:]))    //nolint:gosec // G115: intentional uint16→int16 for PCM test verification
		assert.InDelta(t, origVal, outVal, 1, "sample %d should round-trip within 1 LSB", i/2)
	}
}

// =============================================================================
// Unit Tests for ApplyFilters
// =============================================================================

func TestApplyFilters_EmptyInput(t *testing.T) {
	err := ApplyFilters([]byte{})
	assert.Error(t, err)
}

func TestApplyFilters_OddByteCount(t *testing.T) {
	err := ApplyFilters([]byte{0x00, 0x00, 0x00}) // 3 bytes
	assert.Error(t, err)
}

func TestApplyFilters_NoFilterChain(t *testing.T) {
	// Reset filter chain
	filterMutex.Lock()
	oldChain := filterChain
	filterChain = nil
	filterMutex.Unlock()

	t.Cleanup(func() {
		filterMutex.Lock()
		filterChain = oldChain
		filterMutex.Unlock()
	})

	samples := make([]byte, 100)
	err := ApplyFilters(samples)
	assert.Error(t, err)
}

func TestApplyFilters_EmptyFilterChain(t *testing.T) {
	// Initialize empty filter chain
	settings := conf.Setting()
	if settings == nil {
		t.Skip("Settings not available")
	}

	// Save original state
	oldEnabled := settings.Realtime.Audio.Equalizer.Enabled
	settings.Realtime.Audio.Equalizer.Enabled = false

	t.Cleanup(func() {
		settings.Realtime.Audio.Equalizer.Enabled = oldEnabled
	})

	err := InitializeFilterChain(settings)
	require.NoError(t, err)

	// Apply filters with empty chain (should pass through unchanged)
	samples := []byte{0x00, 0x40, 0x00, 0xC0} // Two samples
	original := make([]byte, len(samples))
	copy(original, samples)

	err = ApplyFilters(samples)
	require.NoError(t, err)

	// With empty filter chain, samples should be unchanged
	assert.Equal(t, original, samples)
}

// =============================================================================
// Benchmarks for Clamping
// =============================================================================

var benchClampResult []float64

func BenchmarkClampFloat64Slice_Sizes(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"1000_samples", 1000},
		{"48000_samples_1sec", 48000},
		{"144000_samples_3sec", 144000},
	}

	for _, sz := range sizes {
		// All within range (no clamping needed - best case)
		b.Run(sz.name+"_in_range", func(b *testing.B) {
			samples := make([]float64, sz.size)
			for i := range samples {
				samples[i] = math.Sin(2 * math.Pi * 440.0 * float64(i) / 48000.0)
			}

			b.ReportAllocs()
			b.SetBytes(int64(sz.size * 8))

			for b.Loop() {
				ClampFloat64Slice(samples)
				benchClampResult = samples
			}
		})

		// All above range (all need clamping - worst case)
		b.Run(sz.name+"_all_clamp", func(b *testing.B) {
			samples := make([]float64, sz.size)
			for i := range samples {
				samples[i] = 1.5 + float64(i)/float64(sz.size)
			}

			b.ReportAllocs()
			b.SetBytes(int64(sz.size * 8))

			for b.Loop() {
				ClampFloat64Slice(samples)
				benchClampResult = samples
			}
		})

		// Mixed (realistic - some need clamping)
		b.Run(sz.name+"_mixed", func(b *testing.B) {
			samples := make([]float64, sz.size)
			for i := range samples {
				// ~20% of samples need clamping (realistic for distorted audio)
				//nolint:gosec // G404: math/rand is fine for test data generation
				if rand.Float64() < 0.2 {
					samples[i] = 1.0 + rand.Float64() //nolint:gosec // G404: test data
				} else {
					samples[i] = rand.Float64()*2 - 1 //nolint:gosec // G404: test data
				}
			}

			b.ReportAllocs()
			b.SetBytes(int64(sz.size * 8))

			for b.Loop() {
				ClampFloat64Slice(samples)
				benchClampResult = samples
			}
		})
	}
}

// =============================================================================
// Benchmarks for Byte-to-Float Conversion
// =============================================================================

var benchConvResult []float64
var benchByteResult []byte

func BenchmarkBytesToFloat64_Sizes(b *testing.B) {
	sizes := []struct {
		name string
		size int // Number of samples
	}{
		{"1000_samples", 1000},
		{"48000_samples_1sec", 48000},
		{"144000_samples_3sec", 144000},
	}

	for _, sz := range sizes {
		bytes := make([]byte, sz.size*2)
		// Fill with realistic audio pattern
		for i := range sz.size {
			val := int16(math.Sin(2*math.Pi*440.0*float64(i)/48000.0) * 32767) //nolint:gosec // G115: sin*32767 is always in int16 range
			binary.LittleEndian.PutUint16(bytes[i*2:], uint16(val))            //nolint:gosec // G115: intentional int16→uint16 for PCM test data
		}

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(sz.size * 2))

			for b.Loop() {
				benchConvResult = BytesToFloat64PCM16(bytes)
			}
		})
	}
}

func BenchmarkFloat64ToBytes_Sizes(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"1000_samples", 1000},
		{"48000_samples_1sec", 48000},
		{"144000_samples_3sec", 144000},
	}

	for _, sz := range sizes {
		floatSamples := make([]float64, sz.size)
		for i := range floatSamples {
			floatSamples[i] = math.Sin(2 * math.Pi * 440.0 * float64(i) / 48000.0)
		}
		output := make([]byte, sz.size*2)

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(sz.size * 2))

			for b.Loop() {
				_ = Float64ToBytesPCM16(floatSamples, output)
				benchByteResult = output
			}
		})
	}
}

// BenchmarkApplyFilters_FullPipeline benchmarks the complete filter pipeline
func BenchmarkApplyFilters_FullPipeline(b *testing.B) {
	settings := conf.Setting()
	if settings == nil {
		b.Skip("Settings not available")
	}

	// Save original state
	oldEnabled := settings.Realtime.Audio.Equalizer.Enabled
	oldFilters := settings.Realtime.Audio.Equalizer.Filters

	// Setup a simple filter chain
	settings.Realtime.Audio.Equalizer.Enabled = true
	settings.Realtime.Audio.Equalizer.Filters = []conf.EqualizerFilter{
		{Type: "HighPass", Frequency: 100, Q: 0.707, Passes: 2},
		{Type: "LowPass", Frequency: 15000, Q: 0.707, Passes: 2},
	}

	defer func() {
		settings.Realtime.Audio.Equalizer.Enabled = oldEnabled
		settings.Realtime.Audio.Equalizer.Filters = oldFilters
	}()

	err := InitializeFilterChain(settings)
	if err != nil {
		b.Fatalf("Failed to initialize filter chain: %v", err)
	}

	sizes := []struct {
		name string
		size int // Number of samples
	}{
		{"1000_samples", 1000},
		{"48000_samples_1sec", 48000},
		{"144000_samples_3sec", 144000},
	}

	for _, sz := range sizes {
		samples := make([]byte, sz.size*2)
		for i := range sz.size {
			val := int16(math.Sin(2*math.Pi*440.0*float64(i)/48000.0) * 32767) //nolint:gosec // G115: sin*32767 is always in int16 range
			binary.LittleEndian.PutUint16(samples[i*2:], uint16(val))          //nolint:gosec // G115: intentional int16→uint16 for PCM test data
		}

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(sz.size * 2))

			for b.Loop() {
				// Need to reset filter state for fair comparison
				err := ApplyFilters(samples)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
