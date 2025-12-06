package equalizer

import (
	"fmt"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Unit Tests for Filter
// =============================================================================

func TestFilter_IsZero(t *testing.T) {
	t.Run("uninitialized", func(t *testing.T) {
		f := &Filter{}
		assert.True(t, f.IsZero())
	})

	t.Run("initialized", func(t *testing.T) {
		f, err := NewLowPass(48000, 1000, 0.707, 1)
		require.NoError(t, err)
		assert.False(t, f.IsZero())
	})
}

func TestNewFilter_Coefficients(t *testing.T) {
	f := NewFilter(LowPass, 1.0, 0.5, 0.25, 0.1, 0.2, 0.3, 2)

	// Verify pre-computed coefficients
	assert.InDelta(t, 0.1/1.0, f.b0a0, 1e-10)
	assert.InDelta(t, 0.2/1.0, f.b1a0, 1e-10)
	assert.InDelta(t, 0.3/1.0, f.b2a0, 1e-10)
	assert.InDelta(t, 0.5/1.0, f.a1a0, 1e-10)
	assert.InDelta(t, 0.25/1.0, f.a2a0, 1e-10)

	// Verify state arrays are initialized
	assert.Len(t, f.in1, 2)
	assert.Len(t, f.in2, 2)
	assert.Len(t, f.out1, 2)
	assert.Len(t, f.out2, 2)
}

func TestFilter_ApplyBatch_InPlace(t *testing.T) {
	f, err := NewLowPass(48000, 1000, 0.707, 1)
	require.NoError(t, err)

	input := []float64{1.0, 0.5, 0.0, -0.5, -1.0}
	originalAddr := &input[0]

	f.ApplyBatch(input)

	// Verify it modified in place (same slice)
	assert.Equal(t, originalAddr, &input[0], "should modify slice in place")

	// Values should be changed (filtered)
	// For a lowpass filter, the output won't exactly match input
}

func TestFilter_ApplyBatch_DCSignal(t *testing.T) {
	// DC signal (constant) should pass through lowpass filter unchanged (at DC)
	f, err := NewLowPass(48000, 1000, 0.707, 1)
	require.NoError(t, err)

	// Need enough samples for filter to settle
	input := make([]float64, 1000)
	for i := range input {
		input[i] = 0.5
	}

	f.ApplyBatch(input)

	// After settling, output should be close to DC value
	// Check last 100 samples
	for i := 900; i < 1000; i++ {
		assert.InDelta(t, 0.5, input[i], 0.01, "DC should pass through lowpass (sample %d)", i)
	}
}

func TestFilter_ApplyBatch_HighFreqAttenuation(t *testing.T) {
	// High frequency should be attenuated by lowpass filter
	sampleRate := 48000.0
	cutoff := 1000.0
	highFreq := 10000.0 // Well above cutoff

	f, err := NewLowPass(sampleRate, cutoff, 0.707, 2) // 24dB/oct
	require.NoError(t, err)

	// Generate high frequency sine wave
	input := make([]float64, 48000)
	for i := range input {
		input[i] = math.Sin(2 * math.Pi * highFreq * float64(i) / sampleRate)
	}

	// Calculate RMS before filtering
	rmsBefore := calculateRMSFloat64(input)

	f.ApplyBatch(input)

	// Calculate RMS after filtering
	rmsAfter := calculateRMSFloat64(input[1000:]) // Skip transient

	// High frequency should be significantly attenuated
	attenuation := rmsBefore / rmsAfter
	assert.Greater(t, attenuation, 10.0, "high frequency should be attenuated by >20dB")
}

func TestNewLowPass(t *testing.T) {
	t.Run("valid_params", func(t *testing.T) {
		f, err := NewLowPass(48000, 1000, 0.707, 1)
		require.NoError(t, err)
		assert.NotNil(t, f)
		assert.Equal(t, LowPass, f.name)
	})

	t.Run("invalid_passes", func(t *testing.T) {
		f, err := NewLowPass(48000, 1000, 0.707, 0)
		require.Error(t, err)
		assert.Nil(t, f)
	})
}

func TestNewHighPass(t *testing.T) {
	t.Run("valid_params", func(t *testing.T) {
		f, err := NewHighPass(48000, 1000, 0.707, 1)
		require.NoError(t, err)
		assert.NotNil(t, f)
		assert.Equal(t, HighPass, f.name)
	})

	t.Run("attenuates_dc", func(t *testing.T) {
		f, err := NewHighPass(48000, 1000, 0.707, 2)
		require.NoError(t, err)

		// DC signal
		input := make([]float64, 10000)
		for i := range input {
			input[i] = 0.5
		}

		f.ApplyBatch(input)

		// DC should be attenuated to near zero
		avgLast := 0.0
		for i := 9000; i < 10000; i++ {
			avgLast += math.Abs(input[i])
		}
		avgLast /= 1000
		assert.Less(t, avgLast, 0.01, "DC should be attenuated by highpass")
	})
}

func TestNewBandPass(t *testing.T) {
	f, err := NewBandPass(48000, 1000, 1.0, 1)
	require.NoError(t, err)
	assert.NotNil(t, f)
	assert.Equal(t, BandPass, f.name)
}

func TestNewPeaking(t *testing.T) {
	f, err := NewPeaking(48000, 1000, 1.0, 6.0, 1) // +6dB boost
	require.NoError(t, err)
	assert.NotNil(t, f)
	assert.Equal(t, Peaking, f.name)
}

func TestFilterChain_Empty(t *testing.T) {
	fc := NewFilterChain()
	assert.Equal(t, 0, fc.Length())

	// Empty chain should not modify input
	input := []float64{1.0, 0.5, 0.0, -0.5, -1.0}
	expected := make([]float64, len(input))
	copy(expected, input)

	fc.ApplyBatch(input)

	assert.Equal(t, expected, input)
}

func TestFilterChain_AddFilter(t *testing.T) {
	fc := NewFilterChain()

	t.Run("add_valid_filter", func(t *testing.T) {
		f, err := NewLowPass(48000, 1000, 0.707, 1)
		require.NoError(t, err)

		err = fc.AddFilter(f)
		require.NoError(t, err)
		assert.Equal(t, 1, fc.Length())
	})

	t.Run("add_nil_filter", func(t *testing.T) {
		err := fc.AddFilter(nil)
		assert.Error(t, err)
	})

	t.Run("add_uninitialized_filter", func(t *testing.T) {
		err := fc.AddFilter(&Filter{})
		assert.Error(t, err)
	})
}

func TestFilterChain_ApplyBatch(t *testing.T) {
	fc := NewFilterChain()

	// Add lowpass and highpass (bandpass effect)
	lp, err := NewLowPass(48000, 2000, 0.707, 1)
	require.NoError(t, err)
	hp, err := NewHighPass(48000, 500, 0.707, 1)
	require.NoError(t, err)

	require.NoError(t, fc.AddFilter(lp))
	require.NoError(t, fc.AddFilter(hp))

	// Generate white noise
	input := make([]float64, 48000)
	for i := range input {
		input[i] = rand.Float64()*2 - 1
	}

	fc.ApplyBatch(input)

	// Output should be bandpass filtered (attenuated outside 500-2000Hz)
	// Just verify no NaN/Inf
	for i, v := range input {
		assert.False(t, math.IsNaN(v), "sample %d should not be NaN", i)
		assert.False(t, math.IsInf(v, 0), "sample %d should not be Inf", i)
	}
}

func TestFilter_MultiplePasses(t *testing.T) {
	sampleRate := 48000.0
	cutoff := 1000.0
	testFreq := 5000.0 // Above cutoff

	passes := []struct {
		name           string
		passes         int
		minAttenuation float64 // Expected minimum attenuation in dB
	}{
		{"1_pass_12dB", 1, 10},
		{"2_pass_24dB", 2, 20},
		{"4_pass_48dB", 4, 35},
	}

	for _, p := range passes {
		t.Run(p.name, func(t *testing.T) {
			f, err := NewLowPass(sampleRate, cutoff, 0.707, p.passes)
			require.NoError(t, err)

			// Generate test signal
			input := make([]float64, 48000)
			for i := range input {
				input[i] = math.Sin(2 * math.Pi * testFreq * float64(i) / sampleRate)
			}
			rmsBefore := calculateRMSFloat64(input)

			f.ApplyBatch(input)
			rmsAfter := calculateRMSFloat64(input[5000:]) // Skip transient

			attenuationDB := 20 * math.Log10(rmsBefore/rmsAfter)
			assert.Greater(t, attenuationDB, p.minAttenuation,
				"attenuation should be at least %.0fdB", p.minAttenuation)
		})
	}
}

// Helper function to calculate RMS
func calculateRMSFloat64(samples []float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	sum := 0.0
	for _, s := range samples {
		sum += s * s
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// =============================================================================
// Benchmarks for Filter and FilterChain
// =============================================================================

var benchFilterResult []float64

func BenchmarkFilter_ApplyBatch_Sizes(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"1000_samples", 1000},
		{"48000_samples_1sec", 48000},
		{"144000_samples_3sec", 144000},
	}

	for _, sz := range sizes {
		f, err := NewLowPass(48000, 1000, 0.707, 1)
		if err != nil {
			b.Fatal(err)
		}

		input := make([]float64, sz.size)
		for i := range input {
			input[i] = math.Sin(2 * math.Pi * 440.0 * float64(i) / 48000.0)
		}

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(sz.size * 8))

			for b.Loop() {
				// Reset filter state between iterations
				f.in1[0], f.in2[0], f.out1[0], f.out2[0] = 0, 0, 0, 0
				f.ApplyBatch(input)
				benchFilterResult = input
			}
		})
	}
}

func BenchmarkFilter_ApplyBatch_Passes(b *testing.B) {
	const size = 48000
	passes := []int{1, 2, 4}

	for _, p := range passes {
		f, err := NewLowPass(48000, 1000, 0.707, p)
		if err != nil {
			b.Fatal(err)
		}

		input := make([]float64, size)
		for i := range input {
			input[i] = math.Sin(2 * math.Pi * 440.0 * float64(i) / 48000.0)
		}

		b.Run(fmt.Sprintf("passes_%d", p), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size * 8))

			for b.Loop() {
				// Reset filter state
				for i := range f.in1 {
					f.in1[i], f.in2[i], f.out1[i], f.out2[i] = 0, 0, 0, 0
				}
				f.ApplyBatch(input)
				benchFilterResult = input
			}
		})
	}
}

func BenchmarkFilterChain_ApplyBatch(b *testing.B) {
	filterCounts := []int{1, 3, 5, 10}
	const size = 48000

	for _, count := range filterCounts {
		fc := NewFilterChain()
		for i := range count {
			// Alternate between lowpass, highpass, and peaking
			var f *Filter
			var err error
			freq := 500.0 + float64(i)*200.0

			switch i % 3 {
			case 0:
				f, err = NewLowPass(48000, freq, 0.707, 1)
			case 1:
				f, err = NewHighPass(48000, freq, 0.707, 1)
			case 2:
				f, err = NewPeaking(48000, freq, 1.0, 3.0, 1)
			}
			if err != nil {
				b.Fatal(err)
			}
			if err := fc.AddFilter(f); err != nil {
				b.Fatal(err)
			}
		}

		input := make([]float64, size)
		for i := range input {
			input[i] = math.Sin(2 * math.Pi * 440.0 * float64(i) / 48000.0)
		}

		b.Run(fmt.Sprintf("filters_%02d", count), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size * 8))

			for b.Loop() {
				// Reset all filter states
				for _, f := range fc.filters {
					for i := range f.in1 {
						f.in1[i], f.in2[i], f.out1[i], f.out2[i] = 0, 0, 0, 0
					}
				}
				fc.ApplyBatch(input)
				benchFilterResult = input
			}
		})
	}
}

func BenchmarkFilter_ApplyBatch_FilterTypes(b *testing.B) {
	const size = 48000

	types := []struct {
		name   string
		create func() (*Filter, error)
	}{
		{
			name: "lowpass",
			create: func() (*Filter, error) {
				return NewLowPass(48000, 1000, 0.707, 1)
			},
		},
		{
			name: "highpass",
			create: func() (*Filter, error) {
				return NewHighPass(48000, 1000, 0.707, 1)
			},
		},
		{
			name: "bandpass",
			create: func() (*Filter, error) {
				return NewBandPass(48000, 1000, 1.0, 1)
			},
		},
		{
			name: "peaking",
			create: func() (*Filter, error) {
				return NewPeaking(48000, 1000, 1.0, 6.0, 1)
			},
		},
		{
			name: "lowshelf",
			create: func() (*Filter, error) {
				return NewLowShelf(48000, 200, 0.707, 6.0, 1)
			},
		},
		{
			name: "highshelf",
			create: func() (*Filter, error) {
				return NewHighShelf(48000, 8000, 0.707, 6.0, 1)
			},
		},
	}

	for _, ft := range types {
		f, err := ft.create()
		if err != nil {
			b.Fatal(err)
		}

		input := make([]float64, size)
		for i := range input {
			input[i] = math.Sin(2 * math.Pi * 440.0 * float64(i) / 48000.0)
		}

		b.Run(ft.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size * 8))

			for b.Loop() {
				f.in1[0], f.in2[0], f.out1[0], f.out2[0] = 0, 0, 0, 0
				f.ApplyBatch(input)
				benchFilterResult = input
			}
		})
	}
}
