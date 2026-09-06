package metrics

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

// AudioStreamMetrics contains Prometheus metrics for network audio stream ingest
// (both the FFmpeg and native go-audio-stream producers). It is the concrete
// implementation of the audiocore.StreamMetrics interface; because that interface
// uses only primitive argument types, this package does not import audiocore and
// no import cycle is introduced. The interface contract is asserted in the tests.
//
// All series are keyed by the stable internal source ID (not the raw URL) to
// avoid leaking stream credentials into metric labels and to stay stable across
// URL changes.
type AudioStreamMetrics struct {
	StreamErrors  *prometheus.CounterVec
	StreamHealthy *prometheus.GaugeVec
	DataRate      *prometheus.GaugeVec
	WireRate      *prometheus.GaugeVec
	StreamEngine  *prometheus.GaugeVec
	registry      *prometheus.Registry
}

// NewAudioStreamMetrics creates a new AudioStreamMetrics and registers it with
// the provided Prometheus registry. It returns an error if registration fails.
func NewAudioStreamMetrics(registry *prometheus.Registry) (*AudioStreamMetrics, error) {
	m := &AudioStreamMetrics{registry: registry}
	if err := m.initMetrics(); err != nil {
		return nil, fmt.Errorf("failed to initialize audio stream metrics: %w", err)
	}
	if err := registry.Register(m); err != nil {
		return nil, fmt.Errorf("failed to register audio stream metrics: %w", err)
	}
	return m, nil
}

// initMetrics initializes all metrics for AudioStreamMetrics.
func (m *AudioStreamMetrics) initMetrics() error {
	m.StreamErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "audio_stream_errors_total",
		Help: "Total number of network audio stream ingest errors per source",
	}, []string{"source_id"})

	m.StreamHealthy = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "audio_stream_healthy",
		Help: "Network audio stream health per source (1 when connected, 0 otherwise)",
	}, []string{"source_id"})

	m.DataRate = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "audio_stream_data_rate_bytes_per_second",
		Help: "Decoded PCM data rate in bytes per second per source",
	}, []string{"source_id"})

	m.WireRate = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "audio_stream_wire_rate_bytes_per_second",
		Help: "Wire (pre-decode) data rate in bytes per second per source; native ingest only",
	}, []string{"source_id"})

	m.StreamEngine = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "audio_stream_engine",
		Help: "Active ingest engine per source, exported as a constant 1 labeled by engine (native or ffmpeg)",
	}, []string{"source_id", "engine"})

	return nil
}

// IncStreamErrors increments the error count for a stream.
func (m *AudioStreamMetrics) IncStreamErrors(sourceID string) {
	m.StreamErrors.WithLabelValues(sourceID).Inc()
}

// SetStreamHealth updates the health status of a stream (1 healthy, 0 unhealthy).
func (m *AudioStreamMetrics) SetStreamHealth(sourceID string, healthy bool) {
	m.StreamHealthy.WithLabelValues(sourceID).Set(boolToFloat(healthy))
}

// RecordDataRate records the current decoded-PCM data rate (bytes per second).
func (m *AudioStreamMetrics) RecordDataRate(sourceID string, bytesPerSec float64) {
	m.DataRate.WithLabelValues(sourceID).Set(bytesPerSec)
}

// RecordWireRate records the current wire data rate (bytes per second), distinct
// from the decoded-PCM RecordDataRate. Native ingest only.
func (m *AudioStreamMetrics) RecordWireRate(sourceID string, bytesPerSec float64) {
	m.WireRate.WithLabelValues(sourceID).Set(bytesPerSec)
}

// SetStreamEngine records which ingest producer ("native"/"ffmpeg") serves a
// stream. The engine per source is fixed for the life of a run, so this yields a
// single series per source.
func (m *AudioStreamMetrics) SetStreamEngine(sourceID, engine string) {
	m.StreamEngine.WithLabelValues(sourceID, engine).Set(1)
}

// DeleteStream removes every series for sourceID across all vecs, so a stopped or
// removed stream stops being exported. It is safe to call for a source that has
// no series yet (the deletes are no-ops).
func (m *AudioStreamMetrics) DeleteStream(sourceID string) {
	m.StreamErrors.DeleteLabelValues(sourceID)
	m.StreamHealthy.DeleteLabelValues(sourceID)
	m.DataRate.DeleteLabelValues(sourceID)
	m.WireRate.DeleteLabelValues(sourceID)
	// StreamEngine carries a second (engine) label, so delete every series whose
	// source_id matches regardless of the engine value.
	m.StreamEngine.DeletePartialMatch(prometheus.Labels{"source_id": sourceID})
}

// Collect implements the prometheus.Collector interface.
func (m *AudioStreamMetrics) Collect(ch chan<- prometheus.Metric) {
	m.StreamErrors.Collect(ch)
	m.StreamHealthy.Collect(ch)
	m.DataRate.Collect(ch)
	m.WireRate.Collect(ch)
	m.StreamEngine.Collect(ch)
}

// Describe implements the prometheus.Collector interface.
func (m *AudioStreamMetrics) Describe(ch chan<- *prometheus.Desc) {
	m.StreamErrors.Describe(ch)
	m.StreamHealthy.Describe(ch)
	m.DataRate.Describe(ch)
	m.WireRate.Describe(ch)
	m.StreamEngine.Describe(ch)
}

// boolToFloat maps a health boolean onto the gauge value space (1 or 0).
func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
