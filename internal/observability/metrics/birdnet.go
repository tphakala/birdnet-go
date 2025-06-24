// Package metrics provides custom Prometheus metrics for the BirdNET-Go application.
package metrics

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

// BirdNETMetrics contains all Prometheus metrics related to BirdNET operations.
type BirdNETMetrics struct {
	DetectionCounter *prometheus.CounterVec
	ProcessTimeGauge prometheus.Gauge
	registry         *prometheus.Registry
}

// NewBirdNETMetrics creates a new instance of BirdNETMetrics.
// It requires a Prometheus registry to register the metrics.
// It returns an error if metric registration fails.
func NewBirdNETMetrics(registry *prometheus.Registry) (*BirdNETMetrics, error) {
	m := &BirdNETMetrics{registry: registry}
	if err := m.initMetrics(); err != nil {
		return nil, fmt.Errorf("failed to initialize BirdNET metrics: %w", err)
	}
	if err := registry.Register(m); err != nil {
		return nil, fmt.Errorf("failed to register BirdNET metrics: %w", err)
	}
	return m, nil
}

// initMetrics initializes all metrics for BirdNETMetrics.
func (m *BirdNETMetrics) initMetrics() error {
	var err error
	m.DetectionCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "birdnet_detections",
			Help: "Total number of BirdNET detections partitioned by species name.",
		},
		[]string{"species"},
	)
	m.ProcessTimeGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "birdnet_processing_time_milliseconds",
			Help: "Most recent processing time for a BirdNET detection request in milliseconds.",
		},
	)
	return err
}

// IncrementDetectionCounter increments the detection counter for a given species.
// It should be called each time BirdNET detects a species.
func (m *BirdNETMetrics) IncrementDetectionCounter(speciesName string) {
	m.DetectionCounter.WithLabelValues(speciesName).Inc()
}

// SetProcessTime sets the most recent processing time for a BirdNET detection request.
func (m *BirdNETMetrics) SetProcessTime(milliseconds float64) {
	m.ProcessTimeGauge.Set(milliseconds)
}

// Describe implements the prometheus.Collector interface.
func (m *BirdNETMetrics) Describe(ch chan<- *prometheus.Desc) {
	m.DetectionCounter.Describe(ch)
	ch <- m.ProcessTimeGauge.Desc()
}

// Collect implements the prometheus.Collector interface.
func (m *BirdNETMetrics) Collect(ch chan<- prometheus.Metric) {
	m.DetectionCounter.Collect(ch)
	ch <- m.ProcessTimeGauge
}
