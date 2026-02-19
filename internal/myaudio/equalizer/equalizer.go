// Package equalizer provides equalizers based on the Robert Bristow-Johnson's audio EQ cookbook.
//
// This package supports the following digital filters:
//
//   - Low-pass
//   - High-pass
//   - All-pass
//   - Band-pass
//   - Band-reject
//   - Low-shelf
//   - High-shelf
//   - Peaking
package equalizer

import (
	"fmt"
	"math"
	"sync"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// Mode represents the kind of digital filters.
type FilterName int

// FilterName constants are digital filter names.
const (
	Undefined FilterName = iota
	LowPass
	HighPass
	AllPass
	BandPass
	BandReject
	LowShelf
	HighShelf
	Peaking
)

// Pi value is used as the default pi value in this package.
const Pi = 3.1415926535897932384626433

var (
	p = Pi
)

// hzToOctaves converts a bandwidth in Hz to octaves for a given center frequency.
// This is needed because the RBJ cookbook formulas expect bandwidth in octaves,
// but the UI presents bandwidth in Hz.
//
// Formula: octaves = log2((freq + widthHz/2) / (freq - widthHz/2))
//
// The bandwidth is clamped so the lower band edge stays above 1 Hz to avoid
// division by zero or negative frequencies.
func hzToOctaves(centerFreq, widthHz float64) float64 {
	halfWidth := widthHz / 2.0
	// Clamp so lower edge doesn't go below 1 Hz
	if halfWidth >= centerFreq-1.0 {
		halfWidth = centerFreq - 1.0
	}
	if halfWidth <= 0 {
		halfWidth = 0.01
	}
	// Guard against sub-Hz center frequencies producing non-positive denominator
	lower := centerFreq - halfWidth
	if lower <= 0 {
		lower = 0.01
	}
	return math.Log2((centerFreq + halfWidth) / lower)
}

// Filter holds the digital filter parameters.
type Filter struct {
	name FilterName

	// state variables
	in1  []float64
	in2  []float64
	out1 []float64
	out2 []float64

	// digital filter parameters
	a0 float64
	a1 float64
	a2 float64
	b0 float64
	b1 float64
	b2 float64

	// number of passes
	passes int

	// Pre-computed coefficients for optimization
	b0a0, b1a0, b2a0, a1a0, a2a0 float64
}

// IsZero returns true when the f is not initialized.
func (f *Filter) IsZero() bool {
	return f.name == Undefined
}

// NewFilter creates a new Filter with the specified number of passes
func NewFilter(name FilterName, a0, a1, a2, b0, b1, b2 float64, passes int) *Filter {
	f := &Filter{
		name:   name,
		a0:     a0,
		a1:     a1,
		a2:     a2,
		b0:     b0,
		b1:     b1,
		b2:     b2,
		passes: passes,
		in1:    make([]float64, passes),
		in2:    make([]float64, passes),
		out1:   make([]float64, passes),
		out2:   make([]float64, passes),
	}

	// Pre-compute coefficients
	f.b0a0 = b0 / a0
	f.b1a0 = b1 / a0
	f.b2a0 = b2 / a0
	f.a1a0 = a1 / a0
	f.a2a0 = a2 / a0

	return f
}

// ApplyBatch applies the filter to a batch of samples
func (f *Filter) ApplyBatch(input []float64) {
	for p := range f.passes {
		for i := range input {
			output := f.b0a0*input[i] + f.b1a0*f.in1[p] + f.b2a0*f.in2[p] -
				f.a1a0*f.out1[p] - f.a2a0*f.out2[p]

			f.in2[p] = f.in1[p]
			f.in1[p] = input[i]
			f.out2[p] = f.out1[p]
			f.out1[p] = output

			input[i] = output
		}
	}
}

// NewLowPass returns the low-pass filter.
//
// Parameters:
//
//   - sampleRate ... sample rate in Hz. e.g. 44100.0
//   - frequency ... Cut off frequency in Hz.
//   - q ... Q value.
//   - passes ... Number of passes (1 = 12dB/oct, 2 = 24dB/oct, 4 = 48dB/oct)
//
// NOTE: q must be greater than 0. passes must be 1, 2, or 4.
func NewLowPass(sampleRate, frequency, q float64, passes int) (*Filter, error) {
	if passes < 1 {
		return nil, fmt.Errorf("passes must be 1 or greater")
	}

	w0 := 2.0 * p * frequency / sampleRate
	alpha := math.Sin(w0) / (2.0 * q)

	return NewFilter(
		LowPass,
		1.0+alpha,
		-2.0*math.Cos(w0),
		1.0-alpha,
		(1.0-math.Cos(w0))/2.0,
		1.0-math.Cos(w0),
		(1.0-math.Cos(w0))/2.0,
		passes,
	), nil
}

// NewHighPass returns the high-pass filter.
//
// Parameters:
//
//   - sampleRate ... sample rate in Hz. e.g. 44100.0
//   - frequency ... Cut off frequency in Hz.
//   - q ... Q value.
//   - passes ... Number of passes (1 = 12dB/oct, 2 = 24dB/oct, 4 = 48dB/oct)
//
// NOTE: q must be greater than 0. passes must be 1, 2, or 4.
func NewHighPass(sampleRate, frequency, q float64, passes int) (*Filter, error) {
	if passes < 1 {
		return nil, fmt.Errorf("passes must be 1 or greater")
	}

	w0 := 2.0 * p * frequency / sampleRate
	alpha := math.Sin(w0) / (2.0 * q)

	return NewFilter(
		HighPass,
		1.0+alpha,
		-2.0*math.Cos(w0),
		1.0-alpha,
		(1.0+math.Cos(w0))/2.0,
		-1.0*(1.0+math.Cos(w0)),
		(1.0+math.Cos(w0))/2.0,
		passes,
	), nil
}

// NewAllPass returns the all-pass filter.
//
// Parameters:
//
//   - sampleRate ... sample rate in Hz. e.g. 44100.0
//   - frequency ... Cut off frequency in Hz.
//   - q ... Q value.
//
// NOTE: q must be greater than 0. passes must be 1, 2, or 4.
func NewAllPass(sampleRate, frequency, q float64, passes int) (*Filter, error) {
	if passes < 1 {
		return nil, fmt.Errorf("passes must be 1 or greater")
	}

	w0 := 2.0 * p * frequency / sampleRate
	alpha := math.Sin(w0) / (2.0 * q)

	return NewFilter(
		AllPass,
		1.0+alpha,
		-2.0*math.Cos(w0),
		1.0-alpha,
		1.0-alpha,
		-2.0*math.Cos(w0),
		1.0+alpha,
		passes,
	), nil
}

// NewBandPass returns the band-pass filter.
//
// Parameters:
//
//   - sampleRate ... sample rate in Hz. e.g. 44100.0
//   - frequency ... Center frequency in Hz.
//   - widthHz ... Band width in Hz.
//   - passes ... Number of passes (1 = 12dB/oct, 2 = 24dB/oct, 4 = 48dB/oct)
//
// NOTE: widthHz must be greater than 0. passes must be 1, 2, or 4.
func NewBandPass(sampleRate, frequency, widthHz float64, passes int) (*Filter, error) {
	if passes < 1 {
		return nil, fmt.Errorf("passes must be 1 or greater")
	}

	w0 := 2.0 * p * frequency / sampleRate
	bwOctaves := hzToOctaves(frequency, widthHz)
	alpha := math.Sin(w0) * math.Sinh(math.Log(2.0)/2.0*bwOctaves*w0/math.Sin(w0))

	return NewFilter(
		BandPass,
		1.0+alpha,
		-2.0*math.Cos(w0),
		1.0-alpha,
		alpha,
		0.0,
		-1.0*alpha,
		passes,
	), nil
}

// NewBandReject returns the band-reject filter.
//
// Parameters:
//
//   - sampleRate ... sample rate in Hz. e.g. 44100.0
//   - frequency ... Center frequency in Hz.
//   - widthHz ... Band width in Hz.
//   - passes ... Number of passes (1 = 12dB/oct, 2 = 24dB/oct, 4 = 48dB/oct)
//
// NOTE: widthHz must be greater than 0. passes must be 1, 2, or 4.
func NewBandReject(sampleRate, frequency, widthHz float64, passes int) (*Filter, error) {
	if passes < 1 {
		return nil, fmt.Errorf("passes must be 1 or greater")
	}

	w0 := 2.0 * p * frequency / sampleRate
	bwOctaves := hzToOctaves(frequency, widthHz)
	alpha := math.Sin(w0) * math.Sinh(math.Log(2.0)/2.0*bwOctaves*w0/math.Sin(w0))

	return NewFilter(
		BandReject,
		1.0+alpha,
		-2.0*math.Cos(w0),
		1.0-alpha,
		1.0,
		-2.0*math.Cos(w0),
		1.0,
		passes,
	), nil
}

// NewLowShelf returns the low-shelf filter.
//
// Parameters:
//
//   - sampleRate ... sample rate in Hz. e.g. 44100.0
//   - frequency ... Cut off frequency in Hz.
//   - q ... Q value.
//   - gain ... Gain value in dB.
//
// NOTE: q must be greater than 0. passes must be 1, 2, or 4.
func NewLowShelf(sampleRate, frequency, q, gain float64, passes int) (*Filter, error) {
	if passes < 1 {
		return nil, fmt.Errorf("passes must be 1 or greater")
	}

	w0 := 2.0 * p * frequency / sampleRate
	a := math.Pow(10.0, (gain / 40.0))
	beta := math.Sqrt(a) / q

	return NewFilter(
		LowShelf,
		(a+1.0)+(a-1.0)*math.Cos(w0)+beta*math.Sin(w0),
		-2.0*((a-1.0)+(a+1.0)*math.Cos(w0)),
		(a+1.0)+(a-1.0)*math.Cos(w0)-beta*math.Sin(w0),
		a*((a+1.0)-(a-1.0)*math.Cos(w0)+beta*math.Sin(w0)),
		2.0*a*((a-1.0)-(a+1.0)*math.Cos(w0)),
		a*((a+1.0)-(a-1.0)*math.Cos(w0)-beta*math.Sin(w0)),
		passes,
	), nil
}

// NewHighShelf returns the high-shelf filter.
//
// Parameters:
//
//   - sampleRate ... sample rate in Hz. e.g. 44100.0
//   - frequency ... Cut off frequency in Hz.
//   - q ... Q value.
//   - gain ... Gain value in dB.
//
// NOTE: q must be greater than 0. passes must be 1, 2, or 4.
func NewHighShelf(sampleRate, frequency, q, gain float64, passes int) (*Filter, error) {
	if passes < 1 {
		return nil, fmt.Errorf("passes must be 1 or greater")
	}

	w0 := 2.0 * p * frequency / sampleRate
	a := math.Pow(10.0, (gain / 40.0))
	beta := math.Sqrt(a) / q

	return NewFilter(
		HighShelf,
		(a+1.0)-(a-1.0)*math.Cos(w0)+beta*math.Sin(w0),
		2.0*((a-1.0)-(a+1.0)*math.Cos(w0)),
		(a+1.0)-(a-1.0)*math.Cos(w0)-beta*math.Sin(w0),
		a*((a+1.0)+(a-1.0)*math.Cos(w0)+beta*math.Sin(w0)),
		-2.0*a*((a-1.0)+(a+1.0)*math.Cos(w0)),
		a*((a+1.0)+(a-1.0)*math.Cos(w0)-beta*math.Sin(w0)),
		passes,
	), nil
}

// NewPeaking returns the peaking-shelf filter.
//
// Parameters:
//
//   - sampleRate ... sample rate in Hz. e.g. 44100.0
//   - frequency ... Center frequency in Hz.
//   - widthHz ... Width in Hz.
//   - gain ... Gain value in dB.
//   - passes ... Number of passes (1 = 12dB/oct, 2 = 24dB/oct, 4 = 48dB/oct)
//
// NOTE: widthHz must be greater than 0. passes must be 1, 2, or 4.
func NewPeaking(sampleRate, frequency, widthHz, gain float64, passes int) (*Filter, error) {
	if passes < 1 {
		return nil, fmt.Errorf("filter passes must be 1 or greater")
	}

	w0 := 2.0 * p * frequency / sampleRate
	bwOctaves := hzToOctaves(frequency, widthHz)
	alpha := math.Sin(w0) * math.Sinh(math.Log(2.0)/2.0*bwOctaves*w0/math.Sin(w0))
	a := math.Pow(10.0, (gain / 40.0))

	return NewFilter(
		Peaking,
		1.0+alpha/a,
		-2.0*math.Cos(w0),
		1.0-alpha/a,
		1.0+alpha*a,
		-2.0*math.Cos(w0),
		1.0-alpha*a,
		passes,
	), nil
}

// FilterChain represents a chain of filters to be applied in sequence.
type FilterChain struct {
	filters []*Filter
	mu      sync.RWMutex
}

// NewFilterChain creates and returns a new FilterChain.
func NewFilterChain() *FilterChain {
	return &FilterChain{
		filters: make([]*Filter, 0), // Initialize with empty slice of pointers
	}
}

// AddFilter adds a new filter to the chain.
func (fc *FilterChain) AddFilter(f *Filter) error {
	if f == nil || f.IsZero() {
		return fmt.Errorf("cannot add nil or uninitialized audio EQ filter")
	}
	fc.mu.Lock()
	defer fc.mu.Unlock()

	fc.filters = append(fc.filters, f) // Append pointer to filter

	return nil
}

// Length returns the number of filters in the chain.
func (fc *FilterChain) Length() int {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	return len(fc.filters)
}

// ApplyBatch applies all filters in the chain to a batch of input signals.
func (fc *FilterChain) ApplyBatch(input []float64) {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	log := logger.Global().Module("audio").Module("equalizer")
	for _, filter := range fc.filters {
		if filter != nil {
			filter.ApplyBatch(input)
		} else {
			log.Warn("encountered nil filter in audio EQ filter chain")
		}
	}
}
