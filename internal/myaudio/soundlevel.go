// Package myaudio provides sound level analysis in 1/3rd octave bands
package myaudio

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
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

	// Configurable interval aggregation
	intervalBuffer *intervalAggregator
	interval       int // interval in seconds

	mutex sync.RWMutex
}

// octaveBandBuffer accumulates samples for 1-second intervals
type octaveBandBuffer struct {
	samples           []float64
	sampleCount       int
	targetSampleCount int // Number of samples in 1 second based on sample rate
}

// intervalAggregator collects 1-second measurements to produce interval statistics
type intervalAggregator struct {
	secondMeasurements []map[string]float64 // Array of second measurements
	startTime          time.Time
	currentIndex       int
	measurementCount   int // Track number of completed 1-second measurements
	full               bool
}

// newSoundLevelProcessor creates a new sound level processor for the given source
func newSoundLevelProcessor(source, name string) (*soundLevelProcessor, error) {
	// Get configured interval, with minimum to prevent excessive CPU usage
	configuredInterval := conf.Setting().Realtime.Audio.SoundLevel.Interval
	interval := configuredInterval
	if interval < conf.MinSoundLevelInterval {
		interval = conf.MinSoundLevelInterval

		// Log when interval is clamped to minimum
		if log := getSoundLevelLogger(); log != nil {
			log.Info("sound level interval clamped to minimum",
				logger.String("source", source),
				logger.Int("configured_interval", configuredInterval),
				logger.Int("actual_interval", interval),
				logger.Int("minimum_interval", conf.MinSoundLevelInterval),
				logger.String("reason", "prevent excessive CPU usage"))
		}
	}

	processor := &soundLevelProcessor{
		source:        source,
		name:          name,
		sampleRate:    conf.SampleRate,
		filters:       make([]*octaveBandFilter, 0, len(octaveBandCenterFreqs)),
		secondBuffers: make(map[string]*octaveBandBuffer),
		interval:      interval,
		intervalBuffer: &intervalAggregator{
			secondMeasurements: make([]map[string]float64, interval),
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
			samples:           make([]float64, 0, conf.SampleRate), // Pre-allocate for 1 second
			targetSampleCount: conf.SampleRate,                     // Exactly 1 second of samples
		}
	}

	// Warm up filters to avoid initial transients
	// Process 100 samples of silence through each filter
	for _, filter := range processor.filters {
		for range 100 {
			_ = filter.processAudioSample(0.0)
		}
	}

	// Initialize interval aggregator arrays
	for i := range processor.intervalBuffer.secondMeasurements {
		processor.intervalBuffer.secondMeasurements[i] = make(map[string]float64)
	}

	return processor, nil
}

// newOctaveBandFilter creates a bandpass filter for the specified 1/3rd octave band.
// Uses Robert Bristow-Johnson's audio EQ cookbook formulas for stable biquad filters.
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
		bandwidth:  centerFreq / math.Pow(2, 1.0/6.0), // 1/3rd octave bandwidth per ISO 266
	}

	// Calculate normalized frequencies
	// For 1/3rd octave bands: f_low = f_center / 2^(1/6), f_high = f_center * 2^(1/6)
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

	// Implement biquad bandpass filter using RBJ cookbook formulas
	// This provides much better stability especially at low frequencies

	// Pre-warp the center frequency for bilinear transform
	omega := 2.0 * math.Pi * centerFreq / sampleRate
	sinOmega := math.Sin(omega)
	cosOmega := math.Cos(omega)

	// Q factor for 1/3 octave band (approximately 4.318)
	// Q = f_center / bandwidth = f_center / (f_high - f_low)
	Q := centerFreq / (highFreq - lowFreq)

	// For very low frequencies, ensure minimum Q to maintain stability
	if Q < 0.5 {
		Q = 0.5
	}

	alpha := sinOmega / (2.0 * Q)

	// Bandpass filter coefficients (constant 0 dB peak gain)
	b0 := alpha
	b1 := 0.0
	b2 := -alpha
	a0 := 1.0 + alpha
	a1 := -2.0 * cosOmega
	a2 := 1.0 - alpha

	// Normalize coefficients
	filter.b0 = b0 / a0
	filter.b1 = b1 / a0
	filter.b2 = b2 / a0
	filter.a1 = a1 / a0
	filter.a2 = a2 / a0

	// Validate filter stability (poles must be inside unit circle)
	// For a 2nd order system: |a2| < 1 and |a1| < 1 + a2
	if math.Abs(filter.a2) >= 1.0 || math.Abs(filter.a1) >= (1.0+filter.a2) {
		return nil, errors.Newf("unstable filter coefficients for centerFreq=%f: a1=%f, a2=%f", centerFreq, filter.a1, filter.a2).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "validate_filter_stability").
			Build()
	}

	return filter, nil
}

// processAudioSample processes a single audio sample through the filter
func (f *octaveBandFilter) processAudioSample(input float64) float64 {
	// Apply filter equation: y[n] = b0*x[n] + b1*x[n-1] + b2*x[n-2] - a1*y[n-1] - a2*y[n-2]
	output := f.b0*input + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2

	// Prevent numerical instability: check for non-finite values or excessive amplitude
	const maxAmplitude = 100.0 // Reasonable maximum amplitude for filtered output

	if math.IsNaN(output) || math.IsInf(output, 0) || math.Abs(output) > maxAmplitude {
		// Reset filter state to prevent instability propagation
		f.x1, f.x2, f.y1, f.y2 = 0, 0, 0, 0
		// Return attenuated input to maintain signal flow
		output = input * 0.1
	}

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
		return nil, ErrNoAudioData
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

	// Track input signal statistics for debugging
	minSample, maxSample := 1.0, -1.0
	var sumSquares float64

	for i := range sampleCount {
		// Convert 16-bit little-endian to float64
		sample := int16(samples[i*2]) | int16(samples[i*2+1])<<8
		audioSamples[i] = float64(sample) / 32768.0 // Normalize to [-1, 1]

		// Collect statistics
		if audioSamples[i] < minSample {
			minSample = audioSamples[i]
		}
		if audioSamples[i] > maxSample {
			maxSample = audioSamples[i]
		}
		sumSquares += audioSamples[i] * audioSamples[i]
	}

	// Log input signal statistics if debug is enabled and realtime logging is on
	if conf.Setting().Realtime.Audio.SoundLevel.Debug && conf.Setting().Realtime.Audio.SoundLevel.DebugRealtimeLogging {
		if log := getSoundLevelLogger(); log != nil {
			inputRMS := math.Sqrt(sumSquares / float64(sampleCount))
			inputDB := 20 * math.Log10(inputRMS+1e-10) // Add small value to avoid log(0)
			log.Debug("processing audio samples",
				logger.String("source", p.source),
				logger.String("name", p.name),
				logger.Int("sample_count", sampleCount),
				logger.Float64("min_sample", minSample),
				logger.Float64("max_sample", maxSample),
				logger.Float64("input_rms", inputRMS),
				logger.Float64("input_db", inputDB))
		}
	}

	// Track if any band completed a 1-second measurement in this call
	measurementCompleted := false

	// Process samples through each octave band filter
	for _, filter := range p.filters {
		bandKey := formatBandKey(filter.centerFreq)
		buffer := p.secondBuffers[bandKey]

		// Process each audio sample through this filter
		for _, sample := range audioSamples {
			filteredSample := filter.processAudioSample(sample)
			buffer.samples = append(buffer.samples, filteredSample)
			buffer.sampleCount++
		}

		// Check if we have accumulated 1 second of data based on sample count
		if buffer.sampleCount >= buffer.targetSampleCount {
			// Calculate RMS for this 1-second window
			rms := calculateRMS(buffer.samples[:buffer.targetSampleCount])
			// Ensure we have a valid RMS value before calculating dB level
			// Clamp RMS to a reasonable range to avoid +Inf/-Inf in dB calculation
			// 1e-10 corresponds to -200 dB (effectively silence)
			// 10.0 corresponds to +20 dB (very loud, but finite)
			const minRMS = 1e-10
			const maxRMS = 10.0

			if rms < minRMS {
				rms = minRMS
			} else if rms > maxRMS {
				rms = maxRMS
			}

			levelDB := 20 * math.Log10(rms)

			// Additional safety check for non-finite values
			if math.IsInf(levelDB, 0) || math.IsNaN(levelDB) {
				levelDB = -100.0 // Default to noise floor
			}

			// Log band measurement if debug is enabled and realtime logging is on
			if conf.Setting().Realtime.Audio.SoundLevel.Debug && conf.Setting().Realtime.Audio.SoundLevel.DebugRealtimeLogging {
				if log := getSoundLevelLogger(); log != nil {
					log.Debug("calculated band level",
						logger.String("source", p.source),
						logger.String("band", bandKey),
						logger.Float64("center_freq", filter.centerFreq),
						logger.Float64("rms", rms),
						logger.Float64("level_db", levelDB),
						logger.Int("sample_count", buffer.targetSampleCount))
				}
			}

			// Store 1-second measurement in interval aggregator
			currentIdx := p.intervalBuffer.currentIndex
			p.intervalBuffer.secondMeasurements[currentIdx][bandKey] = levelDB
			measurementCompleted = true

			// Handle any overflow samples by keeping them for the next window
			overflowSamples := buffer.sampleCount - buffer.targetSampleCount
			if overflowSamples > 0 {
				// Move overflow samples to the beginning of the buffer
				copy(buffer.samples[:overflowSamples], buffer.samples[buffer.targetSampleCount:buffer.sampleCount])
				buffer.samples = buffer.samples[:overflowSamples]
				buffer.sampleCount = overflowSamples
			} else {
				// Reset buffer completely
				buffer.samples = buffer.samples[:0] // Keep capacity, reset length
				buffer.sampleCount = 0
			}
		}
	}

	// If a 1-second measurement was completed, update aggregator state
	if measurementCompleted {
		// Move to next index after all bands have stored their measurements
		p.intervalBuffer.currentIndex = (p.intervalBuffer.currentIndex + 1) % p.interval
		p.intervalBuffer.measurementCount++

		// Mark as full once we've collected enough measurements
		if p.intervalBuffer.measurementCount >= p.interval && !p.intervalBuffer.full {
			p.intervalBuffer.full = true
		}
	}

	// Check if interval window is complete based on measurement count
	if p.intervalBuffer.measurementCount >= p.interval {
		soundLevelData := p.generateSoundLevelData()

		// Log interval measurement completion if debug is enabled
		if conf.Setting().Realtime.Audio.SoundLevel.Debug {
			if log := getSoundLevelLogger(); log != nil {
				log.Debug("completed sound level interval measurement",
					logger.String("source", p.source),
					logger.String("name", p.name),
					logger.Int("interval", p.interval),
					logger.Time("timestamp", soundLevelData.Timestamp),
					logger.Int("octave_bands", len(soundLevelData.OctaveBands)))
			}
		}

		p.resetIntervalBuffer()
		return soundLevelData, nil
	}

	return nil, ErrIntervalIncomplete // interval window not yet complete
}

// calculateRMS calculates Root Mean Square of audio samples using SIMD-accelerated operations.
// This delegates to CalculateRMSFloat64 in audio_utils.go which uses f64.DotProduct for the sum of squares.
func calculateRMS(samples []float64) float64 {
	return CalculateRMSFloat64(samples)
}

// generateSoundLevelData creates SoundLevelData from interval aggregated measurements
func (p *soundLevelProcessor) generateSoundLevelData() *SoundLevelData {
	octaveBands := make(map[string]OctaveBandData)

	// For each octave band, calculate min/max/mean from the interval one-second measurements
	for _, filter := range p.filters {
		bandKey := formatBandKey(filter.centerFreq)

		var values []float64
		for _, secondMeasurement := range p.intervalBuffer.secondMeasurements {
			if val, exists := secondMeasurement[bandKey]; exists {
				values = append(values, val)
			}
		}

		if len(values) > 0 {
			minVal := values[0]
			maxVal := values[0]
			sum := 0.0

			for _, val := range values {
				// Skip non-finite values
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

			// Final safety check for all values
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
		Duration:    p.interval, // Use configured interval
		OctaveBands: octaveBands,
	}
}

// resetIntervalBuffer resets the interval aggregation buffer
func (p *soundLevelProcessor) resetIntervalBuffer() {
	p.intervalBuffer.startTime = time.Now()
	p.intervalBuffer.currentIndex = 0
	p.intervalBuffer.measurementCount = 0
	p.intervalBuffer.full = false

	// Clear all measurements
	for i := range p.intervalBuffer.secondMeasurements {
		clear(p.intervalBuffer.secondMeasurements[i])
	}
}

// formatBandKey creates a consistent key for octave band data
func formatBandKey(centerFreq float64) string {
	if centerFreq < 1000 {
		return fmt.Sprintf("%.1f_Hz", centerFreq)
	}
	return fmt.Sprintf("%.1f_kHz", centerFreq/1000)
}

// Global sound level processor registry and logger
var (
	soundLevelProcessors     = make(map[string]*soundLevelProcessor)
	soundLevelProcessorMutex sync.RWMutex
)

// getSoundLevelLogger returns the sound level logger.
// Fetched dynamically to ensure it uses the current centralized logger.
func getSoundLevelLogger() logger.Logger {
	return logger.Global().Module("audio").Module("soundlevel")
}

// UpdateSoundLevelDebugSetting updates the debug log level for sound level processing
// Note: With the new logger, debug level is controlled by the global logger configuration
func UpdateSoundLevelDebugSetting(debug bool) {
	// Debug level is now controlled by global logger configuration
	// This function is kept for API compatibility but is a no-op
}

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

	// Log registration if debug is enabled
	if log := getSoundLevelLogger(); log != nil && conf.Setting().Realtime.Audio.SoundLevel.Debug {
		log.Debug("registered sound level processor",
			logger.String("source", source),
			logger.String("name", name),
			logger.Int("total_processors", len(soundLevelProcessors)))
	}

	return nil
}

// UnregisterSoundLevelProcessor removes a sound level processor for a source
func UnregisterSoundLevelProcessor(source string) {
	soundLevelProcessorMutex.Lock()
	defer soundLevelProcessorMutex.Unlock()

	// Log unregistration if debug is enabled and processor exists
	if _, exists := soundLevelProcessors[source]; exists {
		if log := getSoundLevelLogger(); log != nil && conf.Setting().Realtime.Audio.SoundLevel.Debug {
			log.Debug("unregistering sound level processor",
				logger.String("source", source),
				logger.Int("remaining_processors", len(soundLevelProcessors)-1))
		}
	}

	delete(soundLevelProcessors, source)
}

// ProcessSoundLevelData processes audio data for sound level analysis
func ProcessSoundLevelData(source string, audioData []byte) (*SoundLevelData, error) {
	soundLevelProcessorMutex.RLock()
	processor, exists := soundLevelProcessors[source]
	soundLevelProcessorMutex.RUnlock()

	if !exists {
		return nil, errors.New(ErrSoundLevelProcessorNotRegistered).
			Context("source", source).
			Build()
	}

	return processor.ProcessAudioData(audioData)
}
