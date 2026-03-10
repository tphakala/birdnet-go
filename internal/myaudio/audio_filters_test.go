package myaudio

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

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
// Unit Tests for BytesToFloat64PCM16Into (in-place conversion)
// =============================================================================

func TestBytesToFloat64PCM16Into(t *testing.T) {
	t.Run("matches_allocating_version", func(t *testing.T) {
		input := []byte{0x00, 0x40, 0x00, 0xC0, 0xFF, 0x7F, 0x00, 0x80}
		expected := BytesToFloat64PCM16(input)

		dst := make([]float64, len(input)/2)
		BytesToFloat64PCM16Into(dst, input)

		assert.InDeltaSlice(t, expected, dst, 1e-10)
	})

	t.Run("single_sample", func(t *testing.T) {
		input := []byte{0x00, 0x00} // zero
		dst := make([]float64, 1)
		BytesToFloat64PCM16Into(dst, input)
		assert.InDelta(t, 0.0, dst[0], 1e-10)
	})
}

// =============================================================================
// Unit Tests for ApplySourceFilters (per-source filter chains)
// =============================================================================

// registerTestSource is a helper that registers a test RTSP source and returns its ID.
// It handles cleanup via t.Cleanup.
func registerTestSource(t *testing.T) string {
	t.Helper()
	testURL := fmt.Sprintf("rtsp://test-source-%d.example.com/stream", time.Now().UnixNano())
	registry := GetRegistry()
	source, err := registry.RegisterSource(testURL, SourceConfig{Type: SourceTypeRTSP})
	require.NoError(t, err)
	require.NotNil(t, source)
	t.Cleanup(func() {
		_ = registry.RemoveSource(source.ID)
	})
	return source.ID
}

func TestApplySourceFilters_EmptyInput(t *testing.T) {
	sourceID := registerTestSource(t)
	err := ApplySourceFilters(sourceID, []byte{})
	assert.Error(t, err)
}

func TestApplySourceFilters_OddByteCount(t *testing.T) {
	sourceID := registerTestSource(t)
	err := ApplySourceFilters(sourceID, []byte{0x00, 0x00, 0x00}) // 3 bytes
	assert.Error(t, err)
}

func TestApplySourceFilters_UnknownSource(t *testing.T) {
	// Unknown source should be a no-op (not an error)
	samples := []byte{0x00, 0x40, 0x00, 0xC0}
	original := make([]byte, len(samples))
	copy(original, samples)

	err := ApplySourceFilters("nonexistent-source-id", samples)
	require.NoError(t, err)
	assert.Equal(t, original, samples, "samples should be unchanged for unknown source")
}

func TestApplySourceFilters_NoFilterChain(t *testing.T) {
	// Register a source but set its filter chain to nil
	sourceID := registerTestSource(t)
	registry := GetRegistry()
	source, ok := registry.GetSourceByID(sourceID)
	require.True(t, ok)
	source.SetFilterChain(nil) // explicitly nil

	samples := []byte{0x00, 0x40, 0x00, 0xC0}
	original := make([]byte, len(samples))
	copy(original, samples)

	err := ApplySourceFilters(sourceID, samples)
	require.NoError(t, err)
	assert.Equal(t, original, samples, "samples should be unchanged when filter chain is nil")
}

func TestApplySourceFilters_EmptyFilterChain(t *testing.T) {
	// Register a source with equalizer disabled (empty chain)
	sourceID := registerTestSource(t)
	registry := GetRegistry()
	source, ok := registry.GetSourceByID(sourceID)
	require.True(t, ok)

	settings := &conf.Settings{}
	settings.Realtime.Audio.Equalizer.Enabled = false
	chain, err := NewFilterChainFromSettings(settings)
	require.NoError(t, err)
	source.SetFilterChain(chain)

	samples := []byte{0x00, 0x40, 0x00, 0xC0}
	original := make([]byte, len(samples))
	copy(original, samples)

	err = ApplySourceFilters(sourceID, samples)
	require.NoError(t, err)
	assert.Equal(t, original, samples, "samples should be unchanged with empty filter chain")
}

func TestApplySourceFilters_WithFilter(t *testing.T) {
	// Register a source with an active high-pass filter
	sourceID := registerTestSource(t)
	registry := GetRegistry()
	source, ok := registry.GetSourceByID(sourceID)
	require.True(t, ok)

	settings := &conf.Settings{}
	settings.Realtime.Audio.Equalizer.Enabled = true
	settings.Realtime.Audio.Equalizer.Filters = []conf.EqualizerFilter{
		{Type: "HighPass", Frequency: 1000, Q: 0.707, Passes: 4},
	}
	chain, err := NewFilterChainFromSettings(settings)
	require.NoError(t, err)
	source.SetFilterChain(chain)

	// Generate a 100Hz sine wave (below the 1000Hz cutoff)
	sampleRate := float64(conf.SampleRate)
	numSamples := int(sampleRate / 10) // 0.1 seconds
	samples := make([]byte, numSamples*2)
	for i := range numSamples {
		val := int16(math.Sin(2*math.Pi*100.0*float64(i)/sampleRate) * 16384) //nolint:gosec // G115: sin*16384 is always in int16 range
		binary.LittleEndian.PutUint16(samples[i*2:], uint16(val))             //nolint:gosec // G115: intentional int16→uint16 for PCM test data
	}

	originalRMS := calculateTestRMS(samples)
	err = ApplySourceFilters(sourceID, samples)
	require.NoError(t, err)
	filteredRMS := calculateTestRMS(samples)

	// The high-pass filter should significantly attenuate the 100Hz signal
	attenuation := filteredRMS / originalRMS
	assert.Less(t, attenuation, 0.5,
		"100Hz signal should be attenuated by high-pass filter at 1000Hz (got %.2f%% of original)",
		attenuation*100)
}

func TestApplySourceFilters_ConcurrentSources(t *testing.T) {
	// Verify that concurrent filter application on different sources
	// with independent filter chains doesn't corrupt audio data.
	const numSources = 4
	const numIterations = 50

	sampleRate := float64(conf.SampleRate)
	numSamples := int(sampleRate / 10)

	// Create sources with different filter configurations
	sourceIDs := make([]string, numSources)
	for i := range numSources {
		sourceIDs[i] = registerTestSource(t)
		registry := GetRegistry()
		source, ok := registry.GetSourceByID(sourceIDs[i])
		require.True(t, ok)

		settings := &conf.Settings{}
		settings.Realtime.Audio.Equalizer.Enabled = true
		// Each source gets a different cutoff frequency
		freq := 500.0 + float64(i)*500.0
		settings.Realtime.Audio.Equalizer.Filters = []conf.EqualizerFilter{
			{Type: "HighPass", Frequency: freq, Q: 0.707, Passes: 2},
		}
		chain, err := NewFilterChainFromSettings(settings)
		require.NoError(t, err)
		source.SetFilterChain(chain)
	}

	// Run concurrent filter applications
	var wg sync.WaitGroup
	errs := make([]error, numSources)

	for i := range numSources {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for range numIterations {
				samples := make([]byte, numSamples*2)
				for j := range numSamples {
					val := int16(math.Sin(2*math.Pi*100.0*float64(j)/sampleRate) * 16384) //nolint:gosec // G115: sin*16384 is always in int16 range
					binary.LittleEndian.PutUint16(samples[j*2:], uint16(val))             //nolint:gosec // G115: intentional int16→uint16 for PCM test data
				}
				if err := ApplySourceFilters(sourceIDs[idx], samples); err != nil {
					errs[idx] = err
					return
				}
			}
		}(i)
	}

	wg.Wait()
	for i, err := range errs {
		assert.NoError(t, err, "source %d should not error during concurrent processing", i)
	}
}

// =============================================================================
// Shared test helper
// =============================================================================

// calculateTestRMS computes the root mean square of PCM16 audio samples from byte slice.
// Shared between audio_filters_test.go and ffmpeg_audio_filters_test.go.
func calculateTestRMS(samples []byte) float64 {
	if len(samples) < 2 {
		return 0
	}

	var sumSquares float64
	numSamples := len(samples) / 2

	for i := range numSamples {
		val := int16(binary.LittleEndian.Uint16(samples[i*2:])) //nolint:gosec // G115: intentional uint16→int16 for PCM test verification
		normalized := float64(val) / 32768.0
		sumSquares += normalized * normalized
	}

	return math.Sqrt(sumSquares / float64(numSamples))
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

// BenchmarkApplySourceFilters_FullPipeline benchmarks the complete per-source filter pipeline
func BenchmarkApplySourceFilters_FullPipeline(b *testing.B) {
	// Register a test source
	testURL := fmt.Sprintf("rtsp://bench-source-%d.example.com/stream", time.Now().UnixNano())
	registry := GetRegistry()
	source, err := registry.RegisterSource(testURL, SourceConfig{Type: SourceTypeRTSP})
	if err != nil {
		b.Fatalf("Failed to register source: %v", err)
	}
	defer func() { _ = registry.RemoveSource(source.ID) }()

	// Configure a filter chain
	settings := &conf.Settings{}
	settings.Realtime.Audio.Equalizer.Enabled = true
	settings.Realtime.Audio.Equalizer.Filters = []conf.EqualizerFilter{
		{Type: "HighPass", Frequency: 100, Q: 0.707, Passes: 2},
		{Type: "LowPass", Frequency: 15000, Q: 0.707, Passes: 2},
	}

	chain, err := NewFilterChainFromSettings(settings)
	if err != nil {
		b.Fatalf("Failed to create filter chain: %v", err)
	}
	source.SetFilterChain(chain)

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
				err := ApplySourceFilters(source.ID, samples)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
