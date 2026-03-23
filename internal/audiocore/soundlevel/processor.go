package soundlevel

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// nyquistMarginFactor is the fraction of Nyquist below which octave bands are
// included. Bands whose upper edge (centerFreq * 2^(1/6)) exceeds this
// threshold are skipped because the biquad filter becomes unstable or
// inaccurate near Nyquist.
const nyquistMarginFactor = 0.95

// Standard 1/3 octave band center frequencies (Hz) per ISO 266.
var octaveBandCenterFreqs = []float64{
	25, 31.5, 40, 50, 63, 80, 100, 125, 160, 200, 250, 315, 400, 500, 630, 800,
	1000, 1250, 1600, 2000, 2500, 3150, 4000, 5000, 6300, 8000, 10000, 12500, 16000, 20000,
}

// Sentinel errors returned by Processor.

// ErrNoAudioData is returned when ProcessSamples receives an empty slice.
var ErrNoAudioData = errors.Newf("no audio data to process").
	Component("soundlevel").
	Category(errors.CategoryNotFound).
	Build()

// ErrIntervalIncomplete is returned when the aggregation interval has not yet
// accumulated enough 1-second measurements to produce a result.
var ErrIntervalIncomplete = errors.Newf("audio interval window not yet complete").
	Component("soundlevel").
	Category(errors.CategoryNotFound).
	Build()

// octaveBandFilter is a biquad bandpass filter for a single 1/3 octave band.
type octaveBandFilter struct {
	centerFreq float64
	// Normalized biquad coefficients (a0 already divided out).
	a1, a2, b0, b1, b2 float64
	// Filter state variables (direct form II transposed).
	x1, x2, y1, y2 float64
}

// octaveBandBuffer accumulates per-sample filtered output for a 1-second window.
type octaveBandBuffer struct {
	samples           []float64
	sampleCount       int
	targetSampleCount int // samples per 1-second window at the given sample rate
}

// intervalAggregator collects 1-second dB measurements and produces interval statistics.
type intervalAggregator struct {
	secondMeasurements []map[string]float64
	startTime          time.Time
	currentIndex       int
	measurementCount   int
	full               bool
}

// Processor is a standalone 1/3 octave band sound level processor.
// Each instance processes a single audio source; there is no global state.
type Processor struct {
	source     string
	name       string
	sampleRate int
	interval   int // aggregation interval in seconds
	filters    []*octaveBandFilter

	secondBuffers  map[string]*octaveBandBuffer
	intervalBuffer *intervalAggregator

	mu sync.Mutex
}

// NewProcessor creates a Processor for the given source.
//
// sampleRate is the PCM sample rate in Hz (e.g. 48000).
// interval is the aggregation window in seconds; values below 1 are clamped to 1.
func NewProcessor(source, name string, sampleRate, interval int) (*Processor, error) {
	if sampleRate <= 0 {
		return nil, errors.Newf("invalid sample rate: %d", sampleRate).
			Component("soundlevel").
			Category(errors.CategoryValidation).
			Context("operation", "create_processor").
			Build()
	}

	if interval < 1 {
		interval = 1
	}

	p := &Processor{
		source:        source,
		name:          name,
		sampleRate:    sampleRate,
		interval:      interval,
		filters:       make([]*octaveBandFilter, 0, len(octaveBandCenterFreqs)),
		secondBuffers: make(map[string]*octaveBandBuffer),
		intervalBuffer: &intervalAggregator{
			secondMeasurements: make([]map[string]float64, interval),
			startTime:          time.Now(),
		},
	}

	nyquist := float64(sampleRate) / 2.0
	// Apply a margin below Nyquist: the upper edge of each 1/3 octave band
	// extends above the centre frequency, so bands whose upper edge approaches
	// Nyquist produce unstable or inaccurate biquad coefficients.
	nyquistThreshold := nyquist * nyquistMarginFactor

	for _, centerFreq := range octaveBandCenterFreqs {
		// Skip bands whose upper edge is at or above the safe Nyquist threshold.
		// For 1/3 octave bands the upper edge is centerFreq * 2^(1/6).
		upperEdge := centerFreq * math.Pow(2, 1.0/6.0)
		if upperEdge >= nyquistThreshold {
			continue
		}

		f, err := newOctaveBandFilter(centerFreq, float64(sampleRate))
		if err != nil {
			return nil, errors.New(err).
				Component("soundlevel").
				Category(errors.CategorySystem).
				Context("operation", "create_octave_filter").
				Context("center_frequency", centerFreq).
				Context("sample_rate", sampleRate).
				Build()
		}

		p.filters = append(p.filters, f)

		bandKey := formatBandKey(centerFreq)
		p.secondBuffers[bandKey] = &octaveBandBuffer{
			samples:           make([]float64, 0, sampleRate),
			targetSampleCount: sampleRate,
		}
	}

	// Initialise interval aggregator maps.
	for i := range p.intervalBuffer.secondMeasurements {
		p.intervalBuffer.secondMeasurements[i] = make(map[string]float64)
	}

	// Warm up filters to avoid initial transients (100 samples of silence).
	for _, f := range p.filters {
		for range 100 {
			_ = f.processAudioSample(0.0)
		}
	}

	return p, nil
}

// newOctaveBandFilter creates a biquad bandpass filter for the specified 1/3 octave band.
// Uses the RBJ audio EQ cookbook bandpass formula for stable digital filters.
func newOctaveBandFilter(centerFreq, sampleRate float64) (*octaveBandFilter, error) {
	if centerFreq <= 0 || sampleRate <= 0 {
		return nil, errors.Newf("invalid filter parameters: centerFreq=%f, sampleRate=%f", centerFreq, sampleRate).
			Component("soundlevel").
			Category(errors.CategoryValidation).
			Context("operation", "validate_filter_params").
			Build()
	}

	nyquist := sampleRate / 2.0
	lowFreq := centerFreq / math.Pow(2, 1.0/6.0)
	highFreq := centerFreq * math.Pow(2, 1.0/6.0)

	if lowFreq <= 0 || highFreq >= nyquist {
		return nil, errors.Newf("filter frequencies out of range: low=%f, high=%f, nyquist=%f", lowFreq, highFreq, nyquist).
			Component("soundlevel").
			Category(errors.CategoryValidation).
			Context("operation", "validate_filter_range").
			Context("center_frequency", centerFreq).
			Build()
	}

	omega := 2.0 * math.Pi * centerFreq / sampleRate
	sinOmega := math.Sin(omega)
	cosOmega := math.Cos(omega)

	// Q for a 1/3 octave bandpass filter (≈ 4.318).
	Q := centerFreq / (highFreq - lowFreq)
	if Q < 0.5 {
		Q = 0.5
	}

	alpha := sinOmega / (2.0 * Q)

	// RBJ bandpass (constant 0 dB peak gain).
	b0Raw := alpha
	b1Raw := 0.0
	b2Raw := -alpha
	a0 := 1.0 + alpha
	a1Raw := -2.0 * cosOmega
	a2Raw := 1.0 - alpha

	filter := &octaveBandFilter{
		centerFreq: centerFreq,
		b0:         b0Raw / a0,
		b1:         b1Raw / a0,
		b2:         b2Raw / a0,
		a1:         a1Raw / a0,
		a2:         a2Raw / a0,
	}

	// Validate stability: poles must lie inside the unit circle.
	if math.Abs(filter.a2) >= 1.0 || math.Abs(filter.a1) >= (1.0+filter.a2) {
		return nil, errors.Newf("unstable filter coefficients for centerFreq=%f: a1=%f, a2=%f",
			centerFreq, filter.a1, filter.a2).
			Component("soundlevel").
			Category(errors.CategoryValidation).
			Context("operation", "validate_filter_stability").
			Build()
	}

	return filter, nil
}

// maxFilterAmplitude is the safety threshold for biquad filter output.
// Outputs exceeding this value indicate numerical instability (e.g.,
// coefficient drift or extreme input), so the filter state is reset.
const maxFilterAmplitude = 100.0

// processAudioSample applies the biquad difference equation to a single sample.
func (f *octaveBandFilter) processAudioSample(input float64) float64 {
	output := f.b0*input + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2

	if math.IsNaN(output) || math.IsInf(output, 0) || math.Abs(output) > maxFilterAmplitude {
		logger.Global().Module("soundlevel").Debug("filter state reset due to numerical instability",
			logger.Float64("center_freq", f.centerFreq),
			logger.Float64("output", output),
			logger.Float64("input", input))
		f.x1, f.x2, f.y1, f.y2 = 0, 0, 0, 0
		output = input * 0.1
	}

	f.x2 = f.x1
	f.x1 = input
	f.y2 = f.y1
	f.y1 = output

	return output
}

// ProcessSamples feeds float64 PCM samples (normalised to [-1, 1]) through the
// octave band filter bank, accumulates 1-second RMS measurements, and returns
// a SoundLevelData when the configured interval is complete.
//
// Returns (nil, ErrNoAudioData) for empty input.
// Returns (nil, ErrIntervalIncomplete) while the interval window is not yet full.
// Returns (*SoundLevelData, nil) when a complete interval is ready.
func (p *Processor) ProcessSamples(samples []float64) (*SoundLevelData, error) {
	if len(samples) == 0 {
		return nil, ErrNoAudioData
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	measurementCompleted := false

	for _, f := range p.filters {
		bandKey := formatBandKey(f.centerFreq)
		buf := p.secondBuffers[bandKey]

		for _, s := range samples {
			buf.samples = append(buf.samples, f.processAudioSample(s))
			buf.sampleCount++
		}

		if buf.sampleCount >= buf.targetSampleCount {
			rms := calculateRMS(buf.samples[:buf.targetSampleCount])

			const (
				minRMS = 1e-10
				maxRMS = 10.0
			)

			if rms < minRMS {
				rms = minRMS
			} else if rms > maxRMS {
				rms = maxRMS
			}

			levelDB := 20 * math.Log10(rms)
			if math.IsInf(levelDB, 0) || math.IsNaN(levelDB) {
				levelDB = -100.0
			}

			currentIdx := p.intervalBuffer.currentIndex
			p.intervalBuffer.secondMeasurements[currentIdx][bandKey] = levelDB
			measurementCompleted = true

			overflow := buf.sampleCount - buf.targetSampleCount
			if overflow > 0 {
				copy(buf.samples[:overflow], buf.samples[buf.targetSampleCount:buf.sampleCount])
				buf.samples = buf.samples[:overflow]
				buf.sampleCount = overflow
			} else {
				buf.samples = buf.samples[:0]
				buf.sampleCount = 0
			}
		}
	}

	if measurementCompleted {
		p.intervalBuffer.currentIndex = (p.intervalBuffer.currentIndex + 1) % p.interval
		p.intervalBuffer.measurementCount++
		if p.intervalBuffer.measurementCount >= p.interval && !p.intervalBuffer.full {
			p.intervalBuffer.full = true
		}
	}

	if p.intervalBuffer.measurementCount >= p.interval {
		data := p.generateSoundLevelData()
		p.resetIntervalBuffer()
		return data, nil
	}

	return nil, ErrIntervalIncomplete
}

// Reset clears all accumulated state, allowing the processor to be reused
// for a new measurement session without reallocation.
func (p *Processor) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.resetIntervalBuffer()

	for _, buf := range p.secondBuffers {
		buf.samples = buf.samples[:0]
		buf.sampleCount = 0
	}

	for _, f := range p.filters {
		f.x1, f.x2, f.y1, f.y2 = 0, 0, 0, 0
	}
}

// generateSoundLevelData builds a SoundLevelData from the accumulated interval
// measurements. Must be called with p.mu held.
func (p *Processor) generateSoundLevelData() *SoundLevelData {
	octaveBands := make(map[string]OctaveBandData, len(p.filters))

	for _, f := range p.filters {
		bandKey := formatBandKey(f.centerFreq)

		values := make([]float64, 0, p.interval)
		for _, secondMeasurement := range p.intervalBuffer.secondMeasurements {
			if val, exists := secondMeasurement[bandKey]; exists {
				values = append(values, val)
			}
		}

		if len(values) == 0 {
			continue
		}

		minVal := values[0]
		maxVal := values[0]
		sum := 0.0

		for _, val := range values {
			if math.IsInf(val, 0) || math.IsNaN(val) {
				continue
			}
			if val < minVal {
				minVal = val
			}
			if val > maxVal {
				maxVal = val
			}
			sum += val
		}

		mean := sum / float64(len(values))

		if math.IsInf(minVal, 0) || math.IsNaN(minVal) {
			minVal = -100.0
		}
		if math.IsInf(maxVal, 0) || math.IsNaN(maxVal) {
			maxVal = -100.0
		}
		if math.IsInf(mean, 0) || math.IsNaN(mean) {
			mean = -100.0
		}

		octaveBands[bandKey] = OctaveBandData{
			CenterFreq:  f.centerFreq,
			Min:         minVal,
			Max:         maxVal,
			Mean:        mean,
			SampleCount: len(values),
		}
	}

	return &SoundLevelData{
		Timestamp:   time.Now(),
		Source:      p.source,
		Name:        p.name,
		Duration:    p.interval,
		OctaveBands: octaveBands,
	}
}

// resetIntervalBuffer resets the interval aggregation state.
// Must be called with p.mu held.
func (p *Processor) resetIntervalBuffer() {
	p.intervalBuffer.startTime = time.Now()
	p.intervalBuffer.currentIndex = 0
	p.intervalBuffer.measurementCount = 0
	p.intervalBuffer.full = false

	for i := range p.intervalBuffer.secondMeasurements {
		clear(p.intervalBuffer.secondMeasurements[i])
	}
}

// calculateRMS returns the Root Mean Square of the given sample slice.
func calculateRMS(samples []float64) float64 {
	if len(samples) == 0 {
		return 0.0
	}
	var sum float64
	for _, s := range samples {
		sum += s * s
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// formatBandKey returns a consistent map key for a given centre frequency.
// Frequencies below 1000 Hz use Hz units; 1000 Hz and above use kHz.
func formatBandKey(centerFreq float64) string {
	if centerFreq < 1000 {
		return fmt.Sprintf("%.1f_Hz", centerFreq)
	}
	return fmt.Sprintf("%.1f_kHz", centerFreq/1000)
}
