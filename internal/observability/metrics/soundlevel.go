// Package metrics provides sound level metrics for observability
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// SoundLevelMetrics contains Prometheus metrics for sound level measurements
type SoundLevelMetrics struct {
	registry *prometheus.Registry

	// Sound level measurement metrics
	soundLevelGauge        *prometheus.GaugeVec
	soundLevelUpdatesTotal *prometheus.CounterVec
	soundLevelDuration     *prometheus.HistogramVec

	// Octave band specific metrics
	octaveBandLevelGauge *prometheus.GaugeVec
	octaveBandMinGauge   *prometheus.GaugeVec
	octaveBandMaxGauge   *prometheus.GaugeVec
	octaveBandMeanGauge  *prometheus.GaugeVec

	// Processing metrics
	soundLevelProcessingDuration *prometheus.HistogramVec
	soundLevelProcessingErrors   *prometheus.CounterVec
	soundLevelPublishingTotal    *prometheus.CounterVec
	soundLevelPublishingErrors   *prometheus.CounterVec
}

// NewSoundLevelMetrics creates and registers new sound level metrics
func NewSoundLevelMetrics(registry *prometheus.Registry) (*SoundLevelMetrics, error) {
	m := &SoundLevelMetrics{registry: registry}
	if err := m.initMetrics(); err != nil {
		return nil, err
	}
	if err := registry.Register(m); err != nil {
		return nil, err
	}
	return m, nil
}

// initMetrics initializes all Prometheus metrics
func (m *SoundLevelMetrics) initMetrics() error {
	// Sound level measurement metrics
	m.soundLevelGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "myaudio_sound_level_db",
			Help: "Current sound level in dB",
		},
		[]string{"source", "name", "measurement_type"}, // measurement_type: overall, weighted
	)

	m.soundLevelUpdatesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_sound_level_updates_total",
			Help: "Total number of sound level updates",
		},
		[]string{"source", "name"},
	)

	m.soundLevelDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "myaudio_sound_level_measurement_duration_seconds",
			Help:    "Duration of sound level measurement windows",
			Buckets: []float64{1, 5, 10, 30, 60}, // Common measurement windows
		},
		[]string{"source", "name"},
	)

	// Octave band specific metrics
	m.octaveBandLevelGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "myaudio_octave_band_level_db",
			Help: "Sound level for specific 1/3rd octave bands in dB",
		},
		[]string{"source", "name", "frequency_band", "metric_type"}, // metric_type: current
	)

	m.octaveBandMinGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "myaudio_octave_band_min_db",
			Help: "Minimum sound level for specific 1/3rd octave bands in dB",
		},
		[]string{"source", "name", "frequency_band"},
	)

	m.octaveBandMaxGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "myaudio_octave_band_max_db",
			Help: "Maximum sound level for specific 1/3rd octave bands in dB",
		},
		[]string{"source", "name", "frequency_band"},
	)

	m.octaveBandMeanGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "myaudio_octave_band_mean_db",
			Help: "Mean sound level for specific 1/3rd octave bands in dB",
		},
		[]string{"source", "name", "frequency_band"},
	)

	// Processing metrics
	m.soundLevelProcessingDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "myaudio_sound_level_processing_duration_seconds",
			Help:    "Time taken to process sound level data",
			Buckets: prometheus.ExponentialBuckets(BucketStart1ms, BucketFactor2, BucketCount12), // 1ms to ~4s
		},
		[]string{"source", "name", "operation"}, // operation: filter, aggregate, calculate
	)

	m.soundLevelProcessingErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_sound_level_processing_errors_total",
			Help: "Total number of sound level processing errors",
		},
		[]string{"source", "name", "error_type"},
	)

	m.soundLevelPublishingTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_sound_level_publishing_total",
			Help: "Total number of sound level publishing attempts",
		},
		[]string{"source", "name", "destination", "status"}, // destination: mqtt, sse; status: success, error
	)

	m.soundLevelPublishingErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_sound_level_publishing_errors_total",
			Help: "Total number of sound level publishing errors",
		},
		[]string{"source", "name", "destination", "error_type"},
	)

	return nil
}

// Describe implements the Collector interface
func (m *SoundLevelMetrics) Describe(ch chan<- *prometheus.Desc) {
	m.soundLevelGauge.Describe(ch)
	m.soundLevelUpdatesTotal.Describe(ch)
	m.soundLevelDuration.Describe(ch)
	m.octaveBandLevelGauge.Describe(ch)
	m.octaveBandMinGauge.Describe(ch)
	m.octaveBandMaxGauge.Describe(ch)
	m.octaveBandMeanGauge.Describe(ch)
	m.soundLevelProcessingDuration.Describe(ch)
	m.soundLevelProcessingErrors.Describe(ch)
	m.soundLevelPublishingTotal.Describe(ch)
	m.soundLevelPublishingErrors.Describe(ch)
}

// Collect implements the Collector interface
func (m *SoundLevelMetrics) Collect(ch chan<- prometheus.Metric) {
	m.soundLevelGauge.Collect(ch)
	m.soundLevelUpdatesTotal.Collect(ch)
	m.soundLevelDuration.Collect(ch)
	m.octaveBandLevelGauge.Collect(ch)
	m.octaveBandMinGauge.Collect(ch)
	m.octaveBandMaxGauge.Collect(ch)
	m.octaveBandMeanGauge.Collect(ch)
	m.soundLevelProcessingDuration.Collect(ch)
	m.soundLevelProcessingErrors.Collect(ch)
	m.soundLevelPublishingTotal.Collect(ch)
	m.soundLevelPublishingErrors.Collect(ch)
}

// Recording methods

// UpdateSoundLevel updates the current sound level gauge
func (m *SoundLevelMetrics) UpdateSoundLevel(source, name, measurementType string, levelDB float64) {
	m.soundLevelGauge.WithLabelValues(source, name, measurementType).Set(levelDB)
	m.soundLevelUpdatesTotal.WithLabelValues(source, name).Inc()
}

// RecordSoundLevelDuration records the duration of a sound level measurement window
func (m *SoundLevelMetrics) RecordSoundLevelDuration(source, name string, durationSeconds float64) {
	m.soundLevelDuration.WithLabelValues(source, name).Observe(durationSeconds)
}

// UpdateOctaveBandLevel updates the sound level for a specific octave band
func (m *SoundLevelMetrics) UpdateOctaveBandLevel(source, name, frequencyBand string, minDB, maxDB, meanDB float64) {
	m.octaveBandMinGauge.WithLabelValues(source, name, frequencyBand).Set(minDB)
	m.octaveBandMaxGauge.WithLabelValues(source, name, frequencyBand).Set(maxDB)
	m.octaveBandMeanGauge.WithLabelValues(source, name, frequencyBand).Set(meanDB)
	m.octaveBandLevelGauge.WithLabelValues(source, name, frequencyBand, "current").Set(meanDB)
}

// RecordSoundLevelProcessingDuration records the duration of sound level processing
func (m *SoundLevelMetrics) RecordSoundLevelProcessingDuration(source, name, operation string, duration float64) {
	m.soundLevelProcessingDuration.WithLabelValues(source, name, operation).Observe(duration)
}

// RecordSoundLevelProcessingError records a sound level processing error
func (m *SoundLevelMetrics) RecordSoundLevelProcessingError(source, name, errorType string) {
	m.soundLevelProcessingErrors.WithLabelValues(source, name, errorType).Inc()
}

// RecordSoundLevelPublishing records a sound level publishing attempt
func (m *SoundLevelMetrics) RecordSoundLevelPublishing(source, name, destination, status string) {
	m.soundLevelPublishingTotal.WithLabelValues(source, name, destination, status).Inc()
}

// RecordSoundLevelPublishingError records a sound level publishing error
func (m *SoundLevelMetrics) RecordSoundLevelPublishingError(source, name, destination, errorType string) {
	m.soundLevelPublishingErrors.WithLabelValues(source, name, destination, errorType).Inc()
}
