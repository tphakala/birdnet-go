// Package myaudio provides sound level analysis in 1/3rd octave bands
package myaudio

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// OctaveBandData represents sound level statistics for a single 1/3rd octave band
type OctaveBandData struct {
	CenterFreq  float64 `json:"center_frequency_hz"`
	Min         float64 `json:"min_db"`
	Max         float64 `json:"max_db"`
	Mean        float64 `json:"mean_db"`
	SampleCount int     `json:"-"` // Internal use only
}

// SoundLevelData represents complete sound level measurements for all octave bands
type SoundLevelData struct {
	Timestamp   time.Time                 `json:"timestamp"`
	Source      string                    `json:"source"`
	Name        string                    `json:"name"`
	Duration    int                       `json:"duration_seconds"`
	OctaveBands map[string]OctaveBandData `json:"octave_bands"`
}

// Standard 1/3rd octave band center frequencies (Hz) - ISO 266 standard
var octaveBandCenterFreqs = []float64{
	25, 31.5, 40, 50, 63, 80, 100, 125, 160, 200, 250, 315, 400, 500, 630, 800,
	1000, 1250, 1600, 2000, 2500, 3150, 4000, 5000, 6300, 8000, 10000, 12500, 16000, 20000,
}

// octaveBandFilter represents a digital filter for a 1/3rd octave band
type octaveBandFilter struct {
	centerFreq float64
	bandwidth  float64
	// Simplified butterworth filter coefficients
	a1, a2, b0, b1, b2 float64
	// Filter state variables
	x1, x2, y1, y2 float64
}

// soundLevelProcessor handles 1/3rd octave sound level analysis
type soundLevelProcessor struct {
	source     string
	name       string
	sampleRate int
	filters    []*octaveBandFilter

	// 1-second aggregation buffers
	secondBuffers map[string]*octaveBandBuffer

	// 10-second aggregation
	tenSecondBuffer *tenSecondAggregator

	mutex sync.RWMutex
}

// octaveBandBuffer accumulates samples for 1-second intervals
type octaveBandBuffer struct {
	samples     []float64
	sampleCount int
	startTime   time.Time
}

// tenSecondAggregator collects 1-second measurements to produce 10-second statistics
type tenSecondAggregator struct {
	secondMeasurements []map[string]float64 // Array of 10 second measurements
	startTime          time.Time
	currentIndex       int
	full               bool
}

// newSoundLevelProcessor creates a new sound level processor for the given source
func newSoundLevelProcessor(source, name string) (*soundLevelProcessor, error) {
	processor := &soundLevelProcessor{
		source:        source,
		name:          name,
		sampleRate:    conf.SampleRate,
		filters:       make([]*octaveBandFilter, 0, len(octaveBandCenterFreqs)),
		secondBuffers: make(map[string]*octaveBandBuffer),
		tenSecondBuffer: &tenSecondAggregator{
			secondMeasurements: make([]map[string]float64, 10),
			startTime:          time.Now(),
		},
	}

	// Initialize filters for each 1/3rd octave band
	for _, centerFreq := range octaveBandCenterFreqs {
		// Skip frequencies beyond Nyquist frequency
		if centerFreq >= float64(conf.SampleRate)/2 {
			continue
		}

		filter, err := newOctaveBandFilter(centerFreq, float64(conf.SampleRate))
		if err != nil {
			return nil, errors.New(err).
				Component("myaudio").
				Category(errors.CategorySystem).
				Context("operation", "create_octave_filter").
				Context("center_frequency", centerFreq).
				Context("sample_rate", conf.SampleRate).
				Build()
		}

		processor.filters = append(processor.filters, filter)

		// Initialize 1-second buffer for this band
		bandKey := formatBandKey(centerFreq)
		processor.secondBuffers[bandKey] = &octaveBandBuffer{
			samples:   make([]float64, 0, conf.SampleRate), // Pre-allocate for 1 second
			startTime: time.Now(),
		}
	}

	// Initialize 10-second aggregator arrays
	for i := range processor.tenSecondBuffer.secondMeasurements {
		processor.tenSecondBuffer.secondMeasurements[i] = make(map[string]float64)
	}

	return processor, nil
}

// newOctaveBandFilter creates a bandpass filter for the specified 1/3rd octave band
func newOctaveBandFilter(centerFreq, sampleRate float64) (*octaveBandFilter, error) {
	if centerFreq <= 0 || sampleRate <= 0 {
		return nil, errors.Newf("invalid filter parameters: centerFreq=%f, sampleRate=%f", centerFreq, sampleRate).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "validate_filter_params").
			Build()
	}

	filter := &octaveBandFilter{
		centerFreq: centerFreq,
		bandwidth:  centerFreq / math.Pow(2, 1.0/6.0), // 1/3rd octave bandwidth
	}

	// Calculate normalized frequencies
	nyquist := sampleRate / 2.0
	lowFreq := centerFreq / math.Pow(2, 1.0/6.0)
	highFreq := centerFreq * math.Pow(2, 1.0/6.0)

	// Ensure frequencies are within valid range
	if lowFreq <= 0 || highFreq >= nyquist {
		return nil, errors.Newf("filter frequencies out of range: low=%f, high=%f, nyquist=%f", lowFreq, highFreq, nyquist).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "validate_filter_range").
			Context("center_frequency", centerFreq).
			Build()
	}

	// Normalize frequencies
	wl := 2.0 * math.Pi * lowFreq / sampleRate
	wh := 2.0 * math.Pi * highFreq / sampleRate

	// Simplified bandpass filter design (2nd order Butterworth)
	// This is a basic implementation - for production use, consider more sophisticated filter design
	wc := math.Sqrt(wl * wh) // Geometric mean
	bw := wh - wl            // Bandwidth

	r := 1 - 3*bw
	k := (1 - 2*r*math.Cos(wc) + r*r) / (2 - 2*math.Cos(wc))

	filter.a1 = 2 * r * math.Cos(wc)
	filter.a2 = -r * r
	filter.b0 = 1 - k
	filter.b1 = 2 * (k - r) * math.Cos(wc)
	filter.b2 = r*r - k

	return filter, nil
}

// processAudioSample processes a single audio sample through the filter
func (f *octaveBandFilter) processAudioSample(input float64) float64 {
	// Apply filter equation: y[n] = b0*x[n] + b1*x[n-1] + b2*x[n-2] - a1*y[n-1] - a2*y[n-2]
	output := f.b0*input + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2

	// Update state variables
	f.x2 = f.x1
	f.x1 = input
	f.y2 = f.y1
	f.y1 = output

	return output
}

// ProcessAudioData processes audio samples and returns sound level data when 10-second window is complete
func (p *soundLevelProcessor) ProcessAudioData(samples []byte) (*SoundLevelData, error) {
	if len(samples) == 0 {
		return nil, nil // No data to process
	}

	// Ensure we have an even number of bytes (16-bit samples)
	if len(samples)%2 != 0 {
		samples = samples[:len(samples)-1]
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()

	// Convert bytes to float64 samples
	sampleCount := len(samples) / 2
	audioSamples := make([]float64, sampleCount)

	for i := 0; i < sampleCount; i++ {
		// Convert 16-bit little-endian to float64
		sample := int16(samples[i*2]) | int16(samples[i*2+1])<<8
		audioSamples[i] = float64(sample) / 32768.0 // Normalize to [-1, 1]
	}

	// Process samples through each octave band filter
	now := time.Now()
	for _, filter := range p.filters {
		bandKey := formatBandKey(filter.centerFreq)
		buffer := p.secondBuffers[bandKey]

		// Process each audio sample through this filter
		for _, sample := range audioSamples {
			filteredSample := filter.processAudioSample(sample)
			buffer.samples = append(buffer.samples, filteredSample)
			buffer.sampleCount++
		}

		// Check if we have accumulated 1 second of data
		if time.Since(buffer.startTime) >= time.Second {
			// Calculate RMS for this 1-second window
			rms := calculateRMS(buffer.samples)
			levelDB := 20 * math.Log10(math.Max(rms, 1e-10)) // Avoid log(0)

			// Store 1-second measurement in 10-second aggregator
			currentIdx := p.tenSecondBuffer.currentIndex
			p.tenSecondBuffer.secondMeasurements[currentIdx][bandKey] = levelDB

			// Reset 1-second buffer
			buffer.samples = buffer.samples[:0] // Keep capacity, reset length
			buffer.sampleCount = 0
			buffer.startTime = now
		}
	}

	// Check if 10-second window is complete
	if time.Since(p.tenSecondBuffer.startTime) >= 10*time.Second {
		soundLevelData := p.generateSoundLevelData()
		p.resetTenSecondBuffer()
		return soundLevelData, nil
	}

	return nil, nil // 10-second window not yet complete
}

// calculateRMS calculates Root Mean Square of audio samples
func calculateRMS(samples []float64) float64 {
	if len(samples) == 0 {
		return 0.0
	}

	sum := 0.0
	for _, sample := range samples {
		sum += sample * sample
	}

	return math.Sqrt(sum / float64(len(samples)))
}

// generateSoundLevelData creates SoundLevelData from 10-second aggregated measurements
func (p *soundLevelProcessor) generateSoundLevelData() *SoundLevelData {
	octaveBands := make(map[string]OctaveBandData)

	// For each octave band, calculate min/max/mean from the 10 one-second measurements
	for _, filter := range p.filters {
		bandKey := formatBandKey(filter.centerFreq)

		var values []float64
		for _, secondMeasurement := range p.tenSecondBuffer.secondMeasurements {
			if val, exists := secondMeasurement[bandKey]; exists {
				values = append(values, val)
			}
		}

		if len(values) > 0 {
			minVal := values[0]
			maxVal := values[0]
			sum := 0.0

			for _, val := range values {
				if val < minVal {
					minVal = val
				}
				if val > maxVal {
					maxVal = val
				}
				sum += val
			}

			mean := sum / float64(len(values))

			octaveBands[bandKey] = OctaveBandData{
				CenterFreq:  filter.centerFreq,
				Min:         minVal,
				Max:         maxVal,
				Mean:        mean,
				SampleCount: len(values),
			}
		}
	}

	return &SoundLevelData{
		Timestamp:   time.Now(),
		Source:      p.source,
		Name:        p.name,
		Duration:    10, // Always 10 seconds
		OctaveBands: octaveBands,
	}
}

// resetTenSecondBuffer resets the 10-second aggregation buffer
func (p *soundLevelProcessor) resetTenSecondBuffer() {
	p.tenSecondBuffer.startTime = time.Now()
	p.tenSecondBuffer.currentIndex = 0
	p.tenSecondBuffer.full = false

	// Clear all measurements
	for i := range p.tenSecondBuffer.secondMeasurements {
		for k := range p.tenSecondBuffer.secondMeasurements[i] {
			delete(p.tenSecondBuffer.secondMeasurements[i], k)
		}
	}
}

// formatBandKey creates a consistent key for octave band data
func formatBandKey(centerFreq float64) string {
	if centerFreq < 1000 {
		return fmt.Sprintf("%.1f_Hz", centerFreq)
	}
	return fmt.Sprintf("%.1f_kHz", centerFreq/1000)
}

// Global sound level processor registry
var (
	soundLevelProcessors     = make(map[string]*soundLevelProcessor)
	soundLevelProcessorMutex sync.RWMutex
)

// RegisterSoundLevelProcessor registers a sound level processor for a source
func RegisterSoundLevelProcessor(source, name string) error {
	soundLevelProcessorMutex.Lock()
	defer soundLevelProcessorMutex.Unlock()

	processor, err := newSoundLevelProcessor(source, name)
	if err != nil {
		return errors.New(err).
			Component("myaudio").
			Category(errors.CategorySystem).
			Context("operation", "register_sound_level_processor").
			Context("source", source).
			Context("name", name).
			Build()
	}

	soundLevelProcessors[source] = processor
	return nil
}

// UnregisterSoundLevelProcessor removes a sound level processor for a source
func UnregisterSoundLevelProcessor(source string) {
	soundLevelProcessorMutex.Lock()
	defer soundLevelProcessorMutex.Unlock()

	delete(soundLevelProcessors, source)
}

// ProcessSoundLevelData processes audio data for sound level analysis
func ProcessSoundLevelData(source string, audioData []byte) (*SoundLevelData, error) {
	soundLevelProcessorMutex.RLock()
	processor, exists := soundLevelProcessors[source]
	soundLevelProcessorMutex.RUnlock()

	if !exists {
		return nil, errors.Newf("no sound level processor registered for source: %s", source).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "process_sound_level_data").
			Context("source", source).
			Build()
	}

	return processor.ProcessAudioData(audioData)
}
