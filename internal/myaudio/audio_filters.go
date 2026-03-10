package myaudio

import (
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/myaudio/equalizer"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// Global variables for filter metrics
var (
	filterMetrics      *metrics.MyAudioMetrics // Global metrics instance for filter operations
	filterMetricsMutex sync.RWMutex            // Mutex for thread-safe access to filterMetrics
	filterMetricsOnce  sync.Once               // Ensures metrics are only set once
)

// maxFloat64PoolSize is the maximum number of float64 samples pooled.
// 144000 = 48kHz * 3 seconds, which is the typical analysis buffer size.
const maxFloat64PoolSize = 144000

// float64Pool provides reusable float64 slices for audio filter processing.
// Uses *[]float64 pointers with reslicing to handle variable-length FFmpeg reads,
// following the s16BufferPool pattern from capture.go.
var float64Pool = sync.Pool{
	New: func() any {
		buf := make([]float64, maxFloat64PoolSize)
		return &buf
	},
}

// Sentinel errors for myaudio operations
var (
	ErrFilterDisabled     = errors.Newf("audio filter is disabled").Component("myaudio").Category(errors.CategoryNotFound).Build()
	ErrNoAudioData        = errors.Newf("no audio data to process").Component("myaudio").Category(errors.CategoryNotFound).Build()
	ErrIntervalIncomplete = errors.Newf("audio interval window not yet complete").Component("myaudio").Category(errors.CategoryNotFound).Build()
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

// NewFilterChainFromSettings creates a new filter chain configured from settings.
// Each audio source should have its own chain to avoid biquad state corruption.
func NewFilterChainFromSettings(settings *conf.Settings) (*equalizer.FilterChain, error) {
	if settings == nil {
		return nil, errors.Newf("settings parameter is nil").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "new_filter_chain").
			Build()
	}

	chain := equalizer.NewFilterChain()
	if chain == nil {
		return nil, errors.Newf("failed to create new filter chain").
			Component("myaudio").
			Category(errors.CategorySystem).
			Context("operation", "new_filter_chain").
			Build()
	}

	if !settings.Realtime.Audio.Equalizer.Enabled {
		return chain, nil
	}

	for i, filterConfig := range settings.Realtime.Audio.Equalizer.Filters {
		filter, err := createFilter(filterConfig, float64(conf.SampleRate))
		if err != nil {
			if errors.Is(err, ErrFilterDisabled) {
				continue
			}
			return nil, errors.New(err).
				Component("myaudio").
				Category(errors.CategoryConfiguration).
				Context("operation", "new_filter_chain").
				Context("filter_index", i).
				Context("filter_type", filterConfig.Type).
				Context("filter_frequency", filterConfig.Frequency).
				Build()
		}
		if filter != nil {
			if err := chain.AddFilter(filter); err != nil {
				return nil, errors.New(err).
					Component("myaudio").
					Category(errors.CategorySystem).
					Context("operation", "new_filter_chain").
					Context("filter_index", i).
					Context("filter_type", filterConfig.Type).
					Build()
			}
		}
	}

	return chain, nil
}

// createFilter creates a single filter based on the configuration
func createFilter(config conf.EqualizerFilter, sampleRate float64) (*equalizer.Filter, error) {
	if config.Passes <= 0 {
		return nil, ErrFilterDisabled
	}

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

// ApplySourceFilters applies the given source's filter chain to audio samples.
// Each source has its own filter chain with independent biquad state, so
// concurrent calls for different sources are safe without synchronization.
func ApplySourceFilters(sourceID string, samples []byte) error {
	start := time.Now()

	// Validate input
	if len(samples) == 0 {
		if m := getFilterMetrics(); m != nil {
			m.RecordAudioProcessing("apply_filters", "unknown", "error")
			m.RecordAudioProcessingError("apply_filters", "unknown", "empty_samples")
		}
		return errors.Newf("empty samples provided for filter application").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "apply_filters").
			Context("source_id", sourceID).
			Context("sample_size", 0).
			Build()
	}

	if len(samples)%2 != 0 {
		if m := getFilterMetrics(); m != nil {
			m.RecordAudioProcessing("apply_filters", "filter", "error")
			m.RecordAudioProcessingError("apply_filters", "filter", "invalid_sample_length")
			m.RecordAudioDataValidationError("filter", "alignment")
		}
		return errors.Newf("invalid sample length: %d bytes, must be even for 16-bit samples", len(samples)).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "apply_filters").
			Context("source_id", sourceID).
			Context("sample_size", len(samples)).
			Build()
	}

	// Look up the source's filter chain
	registry := GetRegistry()
	source, ok := registry.GetSourceByID(sourceID)
	if !ok || source == nil {
		// Source not found — no-op (source may have been removed)
		if m := getFilterMetrics(); m != nil {
			duration := time.Since(start).Seconds()
			m.RecordAudioProcessing("apply_filters", "filter", "skipped")
			m.RecordAudioProcessingDuration("apply_filters", "filter", duration)
		}
		return nil
	}

	chain := source.GetFilterChain() // atomic load — lock-free
	if chain == nil || chain.Length() == 0 {
		// No filters configured — record as skipped
		if m := getFilterMetrics(); m != nil {
			duration := time.Since(start).Seconds()
			m.RecordAudioProcessing("apply_filters", "filter", "skipped")
			m.RecordAudioProcessingDuration("apply_filters", "filter", duration)
		}
		return nil
	}

	// Convert bytes to float64 using pooled buffer
	sampleCount := len(samples) / 2
	var floatSamples []float64
	var bufPtr *[]float64
	pooled := false

	if sampleCount <= maxFloat64PoolSize {
		bufPtr = float64Pool.Get().(*[]float64) //nolint:forcetypeassert // pool always returns *[]float64
		floatSamples = (*bufPtr)[:sampleCount]
		pooled = true
	} else {
		floatSamples = make([]float64, sampleCount)
	}

	BytesToFloat64PCM16Into(floatSamples, samples)

	// Apply filters (each source's chain has independent biquad state)
	chain.ApplyBatch(floatSamples)

	// Convert back to bytes with SIMD-accelerated clamping
	if err := Float64ToBytesPCM16(floatSamples, samples); err != nil {
		if pooled {
			*bufPtr = (*bufPtr)[:cap(*bufPtr)]
			float64Pool.Put(bufPtr) //nolint:staticcheck // SA6002: sync.Pool works with slices
		}
		if m := getFilterMetrics(); m != nil {
			m.RecordAudioProcessing("apply_filters", "filter", "error")
			m.RecordAudioProcessingError("apply_filters", "filter", "pcm16_conversion_failed")
		}
		return errors.New(err).
			Component("myaudio").
			Category(errors.CategorySystem).
			Context("operation", "apply_filters").
			Context("source_id", sourceID).
			Context("sample_count", sampleCount).
			Build()
	}

	// Return buffer to pool
	if pooled {
		*bufPtr = (*bufPtr)[:cap(*bufPtr)]
		float64Pool.Put(bufPtr) //nolint:staticcheck // SA6002: sync.Pool works with slices
	}

	// Record success
	if m := getFilterMetrics(); m != nil {
		duration := time.Since(start).Seconds()
		m.RecordAudioProcessing("apply_filters", "filter", "success")
		m.RecordAudioProcessingDuration("apply_filters", "filter", duration)
		m.RecordAudioSampleCount("filter", sampleCount)
	}

	return nil
}
