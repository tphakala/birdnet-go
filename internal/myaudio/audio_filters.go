package myaudio

import (
	"encoding/binary"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/myaudio/equalizer"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// Global variables for filter chain and mutex
var (
	filterChain         *equalizer.FilterChain
	filterMutex         sync.RWMutex
	filterMetrics       *metrics.MyAudioMetrics // Global metrics instance for filter operations
	filterMetricsMutex  sync.RWMutex            // Mutex for thread-safe access to filterMetrics
	filterMetricsOnce   sync.Once               // Ensures metrics are only set once
)

// Sentinel errors for audio filter operations
var (
	ErrFilterDisabled = errors.NewStd("filter is disabled")
)

// SetFilterMetrics sets the metrics instance for filter operations.
// This function is thread-safe and ensures metrics are only set once per process lifetime.
// Subsequent calls will be ignored due to sync.Once (idempotent behavior).
func SetFilterMetrics(myAudioMetrics *metrics.MyAudioMetrics) {
	filterMetricsOnce.Do(func() {
		filterMetricsMutex.Lock()
		defer filterMetricsMutex.Unlock()
		filterMetrics = myAudioMetrics
	})
}

// getFilterMetrics returns the current metrics instance in a thread-safe manner
func getFilterMetrics() *metrics.MyAudioMetrics {
	filterMetricsMutex.RLock()
	defer filterMetricsMutex.RUnlock()
	return filterMetrics
}

// InitializeFilterChain sets up the initial filter chain based on settings
func InitializeFilterChain(settings *conf.Settings) error {
	start := time.Now()

	// Validate input
	if settings == nil {
		enhancedErr := errors.Newf("settings parameter is nil").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "initialize_filter_chain").
			Build()

		if m := getFilterMetrics(); m != nil {
			m.RecordAudioProcessing("initialize_filters", "system", "error")
			m.RecordAudioProcessingError("initialize_filters", "system", "nil_settings")
		}
		return enhancedErr
	}

	filterMutex.Lock()
	defer filterMutex.Unlock()

	// Create a new filter chain
	filterChain = equalizer.NewFilterChain()
	if filterChain == nil {
		enhancedErr := errors.Newf("failed to create new filter chain").
			Component("myaudio").
			Category(errors.CategorySystem).
			Context("operation", "initialize_filter_chain").
			Build()

		if m := getFilterMetrics(); m != nil {
			m.RecordAudioProcessing("initialize_filters", "system", "error")
			m.RecordAudioProcessingError("initialize_filters", "system", "chain_creation_failed")
		}
		return enhancedErr
	}

	filterCount := 0
	// If equalizer is enabled in settings, add filters
	if settings.Realtime.Audio.Equalizer.Enabled {
		for i, filterConfig := range settings.Realtime.Audio.Equalizer.Filters {
			// Create and add each filter
			filter, err := createFilter(filterConfig, float64(conf.SampleRate))
			if err != nil {
				enhancedErr := errors.New(err).
					Component("myaudio").
					Category(errors.CategoryConfiguration).
					Context("operation", "initialize_filter_chain").
					Context("filter_index", i).
					Context("filter_type", filterConfig.Type).
					Context("filter_frequency", filterConfig.Frequency).
					Build()

				if m := getFilterMetrics(); m != nil {
					m.RecordAudioProcessing("initialize_filters", "system", "error")
					m.RecordAudioProcessingError("initialize_filters", "system", "filter_creation_failed")
				}
				return enhancedErr
			}
			if filter != nil {
				if err := filterChain.AddFilter(filter); err != nil {
					enhancedErr := errors.New(err).
						Component("myaudio").
						Category(errors.CategorySystem).
						Context("operation", "initialize_filter_chain").
						Context("filter_index", i).
						Context("filter_type", filterConfig.Type).
						Build()

					if m := getFilterMetrics(); m != nil {
						m.RecordAudioProcessing("initialize_filters", "system", "error")
						m.RecordAudioProcessingError("initialize_filters", "system", "filter_add_failed")
					}
					return enhancedErr
				}
				filterCount++
			}
		}
	}

	// Record successful initialization
	if m := getFilterMetrics(); m != nil {
		duration := time.Since(start).Seconds()
		m.RecordAudioProcessing("initialize_filters", "system", "success")
		m.RecordAudioProcessingDuration("initialize_filters", "system", duration)
	}

	return nil
}

// UpdateFilterChain updates the filter chain based on new settings
func UpdateFilterChain(settings *conf.Settings) error {
	start := time.Now()

	// Validate input
	if settings == nil {
		enhancedErr := errors.Newf("settings parameter is nil").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "update_filter_chain").
			Build()

		if m := getFilterMetrics(); m != nil {
			m.RecordAudioProcessing("update_filters", "system", "error")
			m.RecordAudioProcessingError("update_filters", "system", "nil_settings")
		}
		return enhancedErr
	}

	// Lock the mutex to ensure thread-safety
	filterMutex.Lock()
	defer filterMutex.Unlock()

	// Create a new filter chain
	newChain := equalizer.NewFilterChain()
	if newChain == nil {
		enhancedErr := errors.Newf("failed to create new filter chain for update").
			Component("myaudio").
			Category(errors.CategorySystem).
			Context("operation", "update_filter_chain").
			Build()

		if m := getFilterMetrics(); m != nil {
			m.RecordAudioProcessing("update_filters", "system", "error")
			m.RecordAudioProcessingError("update_filters", "system", "chain_creation_failed")
		}
		return enhancedErr
	}

	filterCount := 0
	// If equalizer is enabled in settings, add filters
	if settings.Realtime.Audio.Equalizer.Enabled {
		// Iterate through each filter configuration
		for i, filterConfig := range settings.Realtime.Audio.Equalizer.Filters {
			// Create a new filter based on the configuration
			filter, err := createFilter(filterConfig, float64(conf.SampleRate))
			if err != nil {
				enhancedErr := errors.New(err).
					Component("myaudio").
					Category(errors.CategoryConfiguration).
					Context("operation", "update_filter_chain").
					Context("filter_index", i).
					Context("filter_type", filterConfig.Type).
					Build()

				if m := getFilterMetrics(); m != nil {
					m.RecordAudioProcessing("update_filters", "system", "error")
					m.RecordAudioProcessingError("update_filters", "system", "filter_creation_failed")
				}
				return enhancedErr
			}
			// If filter was successfully created, add it to the new chain
			if filter != nil {
				if err := newChain.AddFilter(filter); err != nil {
					enhancedErr := errors.New(err).
						Component("myaudio").
						Category(errors.CategorySystem).
						Context("operation", "update_filter_chain").
						Context("filter_index", i).
						Context("filter_type", filterConfig.Type).
						Build()

					if m := getFilterMetrics(); m != nil {
						m.RecordAudioProcessing("update_filters", "system", "error")
						m.RecordAudioProcessingError("update_filters", "system", "filter_add_failed")
					}
					return enhancedErr
				}
				filterCount++
			}
		}
	}

	// Replace the old filter chain with the new one
	filterChain = newChain

	// Record successful update
	if m := getFilterMetrics(); m != nil {
		duration := time.Since(start).Seconds()
		m.RecordAudioProcessing("update_filters", "system", "success")
		m.RecordAudioProcessingDuration("update_filters", "system", duration)
	}

	return nil
}

// createFilter creates a single filter based on the configuration
func createFilter(config conf.EqualizerFilter, sampleRate float64) (*equalizer.Filter, error) {
	// If passes is 0 or less, return nil without an error (filter is off)
	if config.Passes <= 0 {
		return nil, ErrFilterDisabled
	}

	// Create different types of filters based on the configuration
	switch config.Type {
	case "LowPass":
		return equalizer.NewLowPass(sampleRate, config.Frequency, config.Q, config.Passes)
	case "HighPass":
		return equalizer.NewHighPass(sampleRate, config.Frequency, config.Q, config.Passes)
	case "AllPass":
		return equalizer.NewAllPass(sampleRate, config.Frequency, config.Q, config.Passes)
	case "BandPass":
		return equalizer.NewBandPass(sampleRate, config.Frequency, config.Width, config.Passes)
	case "BandReject":
		return equalizer.NewBandReject(sampleRate, config.Frequency, config.Width, config.Passes)
	case "LowShelf":
		return equalizer.NewLowShelf(sampleRate, config.Frequency, config.Q, config.Gain, config.Passes)
	case "HighShelf":
		return equalizer.NewHighShelf(sampleRate, config.Frequency, config.Q, config.Gain, config.Passes)
	case "Peaking":
		return equalizer.NewPeaking(sampleRate, config.Frequency, config.Width, config.Gain, config.Passes)
	default:
		return nil, errors.Newf("unknown filter type: %s", config.Type).
			Component("myaudio").
			Category(errors.CategoryConfiguration).
			Context("operation", "create_filter").
			Context("filter_type", config.Type).
			Context("supported_types", "LowPass,HighPass,AllPass,BandPass,BandReject,LowShelf,HighShelf,Peaking").
			Build()
	}
}

// ApplyFilters applies the current filter chain to a byte slice of audio samples
func ApplyFilters(samples []byte) error {
	start := time.Now()

	// Validate input
	if len(samples) == 0 {
		enhancedErr := errors.Newf("empty samples provided for filter application").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "apply_filters").
			Context("sample_size", 0).
			Build()

		if m := getFilterMetrics(); m != nil {
			m.RecordAudioProcessing("apply_filters", "unknown", "error")
			m.RecordAudioProcessingError("apply_filters", "unknown", "empty_samples")
		}
		return enhancedErr
	}

	if len(samples)%2 != 0 {
		enhancedErr := errors.Newf("invalid sample length: %d bytes, must be even for 16-bit samples", len(samples)).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "apply_filters").
			Context("sample_size", len(samples)).
			Build()

		if m := getFilterMetrics(); m != nil {
			m.RecordAudioProcessing("apply_filters", "filter", "error")
			m.RecordAudioProcessingError("apply_filters", "filter", "invalid_sample_length")
			m.RecordAudioDataValidationError("filter", "alignment")
		}
		return enhancedErr
	}

	filterMutex.RLock()
	defer filterMutex.RUnlock()

	// If no filters, return early
	if filterChain == nil {
		enhancedErr := errors.Newf("filter chain is not initialized").
			Component("myaudio").
			Category(errors.CategorySystem).
			Context("operation", "apply_filters").
			Build()

		if m := getFilterMetrics(); m != nil {
			m.RecordAudioProcessing("apply_filters", "filter", "error")
			m.RecordAudioProcessingError("apply_filters", "filter", "uninitialized_chain")
		}
		return enhancedErr
	}

	if filterChain.Length() == 0 {
		// No filters to apply - record success but no processing
		if m := getFilterMetrics(); m != nil {
			duration := time.Since(start).Seconds()
			m.RecordAudioProcessing("apply_filters", "filter", "success")
			m.RecordAudioProcessingDuration("apply_filters", "filter", duration)
		}
		return nil
	}

	// Convert byte slice to float64 slice
	sampleCount := len(samples) / 2
	floatSamples := make([]float64, sampleCount)
	for i := 0; i < len(samples); i += 2 {
		floatSamples[i/2] = float64(int16(binary.LittleEndian.Uint16(samples[i:]))) / 32768.0 //nolint:gosec // G115: audio sample conversion within 16-bit range
	}

	// Apply filters to the float samples in batch
	filterChain.ApplyBatch(floatSamples)

	// Convert back to byte slice
	for i, sample := range floatSamples {
		// Clamp the sample to valid range
		if sample > 1.0 {
			sample = 1.0
		} else if sample < -1.0 {
			sample = -1.0
		}
		intSample := int16(sample * 32767.0)
		binary.LittleEndian.PutUint16(samples[i*2:], uint16(intSample)) //nolint:gosec // G115: audio sample conversion within 16-bit range
	}

	// Record successful filter application
	if m := getFilterMetrics(); m != nil {
		duration := time.Since(start).Seconds()
		m.RecordAudioProcessing("apply_filters", "filter", "success")
		m.RecordAudioProcessingDuration("apply_filters", "filter", duration)
		m.RecordAudioSampleCount("filter", sampleCount)
	}

	return nil
}
