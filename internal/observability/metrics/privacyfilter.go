// Package metrics provides privacy-filter metrics for observability.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Privacy-filter discard trigger label values.
const (
	// TriggerLabel marks a discard caused by the label-based human-class match
	// (a "Human" prediction in the classifier output).
	TriggerLabel = "label"
	// TriggerVAD marks a discard caused by the Silero VAD speech gate.
	TriggerVAD = "vad"
)

// PrivacyFilterMetrics contains Prometheus metrics for the privacy filter and
// its Silero VAD speech gate.
type PrivacyFilterMetrics struct {
	registry *prometheus.Registry

	// discardsTotal counts detections discarded by the privacy filter, attributed
	// to the trigger that flagged the source (label match vs VAD speech gate).
	discardsTotal *prometheus.CounterVec
	// vadSpeechHitsTotal counts chunks the VAD scored at or above the gate
	// threshold (human speech detected).
	vadSpeechHitsTotal prometheus.Counter
	// vadInferenceDuration observes the wall-clock time of one VAD inference call.
	vadInferenceDuration prometheus.Histogram
	// vadLastSpeechProbability is the most recent VAD speech probability [0,1].
	vadLastSpeechProbability prometheus.Gauge
}

// NewPrivacyFilterMetrics creates and registers new privacy-filter metrics.
func NewPrivacyFilterMetrics(registry *prometheus.Registry) (*PrivacyFilterMetrics, error) {
	m := &PrivacyFilterMetrics{registry: registry}
	if err := m.initMetrics(); err != nil {
		return nil, err
	}
	if err := registry.Register(m); err != nil {
		return nil, err
	}
	return m, nil
}

// initMetrics initializes all Prometheus metrics.
func (m *PrivacyFilterMetrics) initMetrics() error {
	m.discardsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "privacy_filter_discards_total",
			Help: "Total detections discarded by the privacy filter, by trigger",
		},
		[]string{"trigger"}, // trigger: label, vad
	)

	m.vadSpeechHitsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "vad_speech_hits_total",
			Help: "Total chunks the Silero VAD scored at or above the gate threshold",
		},
	)

	m.vadInferenceDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "vad_inference_duration_seconds",
			Help:    "Duration of one Silero VAD inference call in seconds",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		},
	)

	m.vadLastSpeechProbability = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "vad_last_speech_probability",
			Help: "Most recent Silero VAD speech probability in [0,1]",
		},
	)

	return nil
}

// Describe implements the Collector interface.
func (m *PrivacyFilterMetrics) Describe(ch chan<- *prometheus.Desc) {
	m.discardsTotal.Describe(ch)
	m.vadSpeechHitsTotal.Describe(ch)
	m.vadInferenceDuration.Describe(ch)
	m.vadLastSpeechProbability.Describe(ch)
}

// Collect implements the Collector interface.
func (m *PrivacyFilterMetrics) Collect(ch chan<- prometheus.Metric) {
	m.discardsTotal.Collect(ch)
	m.vadSpeechHitsTotal.Collect(ch)
	m.vadInferenceDuration.Collect(ch)
	m.vadLastSpeechProbability.Collect(ch)
}

// Recording methods. All are nil-safe so callers need only guard on the group
// being present (p.Metrics != nil), not on each metric.

// RecordDiscard increments the privacy-filter discard counter for a trigger
// (TriggerLabel or TriggerVAD).
func (m *PrivacyFilterMetrics) RecordDiscard(trigger string) {
	if m == nil {
		return
	}
	m.discardsTotal.WithLabelValues(trigger).Inc()
}

// RecordVADSpeechHit increments the VAD speech-hit counter.
func (m *PrivacyFilterMetrics) RecordVADSpeechHit() {
	if m == nil {
		return
	}
	m.vadSpeechHitsTotal.Inc()
}

// ObserveVADInference records the duration of one VAD inference call.
func (m *PrivacyFilterMetrics) ObserveVADInference(seconds float64) {
	if m == nil {
		return
	}
	m.vadInferenceDuration.Observe(seconds)
}

// SetVADLastSpeechProbability sets the last-speech-probability gauge.
func (m *PrivacyFilterMetrics) SetVADLastSpeechProbability(prob float64) {
	if m == nil {
		return
	}
	m.vadLastSpeechProbability.Set(prob)
}
