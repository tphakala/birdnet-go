// Package metrics provides sound level metrics for observability
package metrics

import (
	"sync"

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

	// lastNames holds the name each source last published under, guarded by
	// namesMu, so a rename can drop the series of the old name.
	namesMu   sync.RWMutex
	lastNames map[string]string
}

// NewSoundLevelMetrics creates and registers new sound level metrics
func NewSoundLevelMetrics(registry *prometheus.Registry) (*SoundLevelMetrics, error) {
	m := &SoundLevelMetrics{registry: registry, lastNames: make(map[string]string)}
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
		[]string{labelSource, "name", "measurement_type"}, // measurement_type: overall, weighted
	)

	m.soundLevelUpdatesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_sound_level_updates_total",
			Help: "Total number of sound level updates",
		},
		[]string{labelSource, "name"},
	)

	m.soundLevelDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "myaudio_sound_level_measurement_duration_seconds",
			Help:    "Duration of sound level measurement windows",
			Buckets: []float64{1, 5, 10, 30, 60}, // Common measurement windows
		},
		[]string{labelSource, "name"},
	)

	// Octave band specific metrics
	m.octaveBandLevelGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "myaudio_octave_band_level_db",
			Help: "Sound level for specific 1/3rd octave bands in dB",
		},
		[]string{labelSource, "name", "frequency_band", "metric_type"}, // metric_type: current
	)

	m.octaveBandMinGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "myaudio_octave_band_min_db",
			Help: "Minimum sound level for specific 1/3rd octave bands in dB",
		},
		[]string{labelSource, "name", "frequency_band"},
	)

	m.octaveBandMaxGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "myaudio_octave_band_max_db",
			Help: "Maximum sound level for specific 1/3rd octave bands in dB",
		},
		[]string{labelSource, "name", "frequency_band"},
	)

	m.octaveBandMeanGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "myaudio_octave_band_mean_db",
			Help: "Mean sound level for specific 1/3rd octave bands in dB",
		},
		[]string{labelSource, "name", "frequency_band"},
	)

	// Processing metrics
	m.soundLevelProcessingDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "myaudio_sound_level_processing_duration_seconds",
			Help:    "Time taken to process sound level data",
			Buckets: prometheus.ExponentialBuckets(BucketStart1ms, BucketFactor2, BucketCount12), // 1ms to ~4s
		},
		[]string{labelSource, "name", "operation"}, // operation: filter, aggregate, calculate
	)

	m.soundLevelProcessingErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_sound_level_processing_errors_total",
			Help: "Total number of sound level processing errors",
		},
		[]string{labelSource, "name", "error_type"},
	)

	m.soundLevelPublishingTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_sound_level_publishing_total",
			Help: "Total number of sound level publishing attempts",
		},
		[]string{labelSource, "name", "destination", "status"}, // destination: mqtt, sse; status: success, error
	)

	m.soundLevelPublishingErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_sound_level_publishing_errors_total",
			Help: "Total number of sound level publishing errors",
		},
		[]string{labelSource, "name", "destination", "error_type"},
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

// TrackSourceName records the name a source publishes under. When the name
// differs from the one last recorded for that source, every series labelled with
// that source and the old name is deleted from all sound level vectors, so a
// rename leaves no frozen series behind and the new name starts from zero. An
// unchanged name only takes the read lock and compares strings. It is safe for
// concurrent use. Call it once per interval before any recorder sees the new
// name, so every recorder can tell a stale name from the current one.
func (m *SoundLevelMetrics) TrackSourceName(source, name string) {
	m.namesMu.RLock()
	prev, seen := m.lastNames[source]
	m.namesMu.RUnlock()
	if seen && prev == name {
		return
	}
	m.namesMu.Lock()
	defer m.namesMu.Unlock()
	prev, seen = m.lastNames[source]
	if seen && prev == name {
		return
	}
	m.lastNames[source] = name
	if seen {
		m.deleteSourceName(source, prev)
	}
}

// deleteSourceName removes every series with the given source and name labels.
func (m *SoundLevelMetrics) deleteSourceName(source, name string) {
	labels := prometheus.Labels{labelSource: source, "name": name}
	m.soundLevelGauge.DeletePartialMatch(labels)
	m.soundLevelUpdatesTotal.DeletePartialMatch(labels)
	m.soundLevelDuration.DeletePartialMatch(labels)
	m.octaveBandLevelGauge.DeletePartialMatch(labels)
	m.octaveBandMinGauge.DeletePartialMatch(labels)
	m.octaveBandMaxGauge.DeletePartialMatch(labels)
	m.octaveBandMeanGauge.DeletePartialMatch(labels)
	m.soundLevelProcessingDuration.DeletePartialMatch(labels)
	m.soundLevelProcessingErrors.DeletePartialMatch(labels)
	m.soundLevelPublishingTotal.DeletePartialMatch(labels)
	m.soundLevelPublishingErrors.DeletePartialMatch(labels)
}

// acquireCurrent takes the names read lock and reports whether name is the one
// source currently publishes under (a source never passed to TrackSourceName
// counts as current). On true the caller writes its series and then calls
// m.namesMu.RUnlock, so TrackSourceName cannot delete between the check and the
// write. On false the lock is already released and the write must be skipped: a
// recorder finishing an old interval, for example a publisher still draining its
// channel after a rename, must not recreate a series TrackSourceName deleted.
// The lock is a single read lock and no other lock is taken while it is held.
func (m *SoundLevelMetrics) acquireCurrent(source, name string) bool {
	m.namesMu.RLock()
	if current, seen := m.lastNames[source]; seen && current != name {
		m.namesMu.RUnlock()
		return false
	}
	return true
}

// UpdateSoundLevel updates the current sound level gauge. A write under a name
// the source no longer has is dropped (see acquireCurrent).
func (m *SoundLevelMetrics) UpdateSoundLevel(source, name, measurementType string, levelDB float64) {
	if !m.acquireCurrent(source, name) {
		return
	}
	defer m.namesMu.RUnlock()
	m.soundLevelGauge.WithLabelValues(source, name, measurementType).Set(levelDB)
	m.soundLevelUpdatesTotal.WithLabelValues(source, name).Inc()
}

// RecordSoundLevelDuration records the duration of a sound level measurement
// window. A write under a stale name is dropped (see acquireCurrent).
func (m *SoundLevelMetrics) RecordSoundLevelDuration(source, name string, durationSeconds float64) {
	if !m.acquireCurrent(source, name) {
		return
	}
	defer m.namesMu.RUnlock()
	m.soundLevelDuration.WithLabelValues(source, name).Observe(durationSeconds)
}

// UpdateOctaveBandLevel updates the sound level for a specific octave band. A
// write under a stale name is dropped (see acquireCurrent).
func (m *SoundLevelMetrics) UpdateOctaveBandLevel(source, name, frequencyBand string, minDB, maxDB, meanDB float64) {
	if !m.acquireCurrent(source, name) {
		return
	}
	defer m.namesMu.RUnlock()
	m.octaveBandMinGauge.WithLabelValues(source, name, frequencyBand).Set(minDB)
	m.octaveBandMaxGauge.WithLabelValues(source, name, frequencyBand).Set(maxDB)
	m.octaveBandMeanGauge.WithLabelValues(source, name, frequencyBand).Set(meanDB)
	m.octaveBandLevelGauge.WithLabelValues(source, name, frequencyBand, "current").Set(meanDB)
}

// RecordSoundLevelProcessingDuration records the duration of sound level
// processing. A write under a stale name is dropped (see acquireCurrent).
func (m *SoundLevelMetrics) RecordSoundLevelProcessingDuration(source, name, operation string, duration float64) {
	if !m.acquireCurrent(source, name) {
		return
	}
	defer m.namesMu.RUnlock()
	m.soundLevelProcessingDuration.WithLabelValues(source, name, operation).Observe(duration)
}

// RecordSoundLevelProcessingError records a sound level processing error. A
// write under a stale name is dropped (see acquireCurrent).
func (m *SoundLevelMetrics) RecordSoundLevelProcessingError(source, name, errorType string) {
	if !m.acquireCurrent(source, name) {
		return
	}
	defer m.namesMu.RUnlock()
	m.soundLevelProcessingErrors.WithLabelValues(source, name, errorType).Inc()
}

// RecordSoundLevelPublishing records a sound level publishing attempt. A write
// under a stale name is dropped (see acquireCurrent).
func (m *SoundLevelMetrics) RecordSoundLevelPublishing(source, name, destination, status string) {
	if !m.acquireCurrent(source, name) {
		return
	}
	defer m.namesMu.RUnlock()
	m.soundLevelPublishingTotal.WithLabelValues(source, name, destination, status).Inc()
}

// RecordSoundLevelPublishingError records a sound level publishing error. A
// write under a stale name is dropped (see acquireCurrent).
func (m *SoundLevelMetrics) RecordSoundLevelPublishingError(source, name, destination, errorType string) {
	if !m.acquireCurrent(source, name) {
		return
	}
	defer m.namesMu.RUnlock()
	m.soundLevelPublishingErrors.WithLabelValues(source, name, destination, errorType).Inc()
}

// SoundLevelPublishingErrorsVec exposes the publishing-errors counter vector so
// tests can assert the exact value of a specific labelled series (e.g. via
// testutil.ToFloat64), rather than only counting how many series exist.
func (m *SoundLevelMetrics) SoundLevelPublishingErrorsVec() *prometheus.CounterVec {
	return m.soundLevelPublishingErrors
}
