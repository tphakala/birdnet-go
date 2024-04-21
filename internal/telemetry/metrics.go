// metrics.go: Prometheus metrics setup and manipulation for telemetry
package telemetry

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	DetectionCounter *prometheus.CounterVec
	// Additional metrics can be added here
}

const metricsPath = "/metrics"

// NewMetrics initializes and registers all Prometheus metrics used in the telemetry system.
func NewMetrics() (*Metrics, error) {
	metrics := &Metrics{}

	// Setup DetectionCounter
	counterOpts := prometheus.CounterOpts{
		Name: "birdnet_detections",
		Help: "Counts of BirdNET detections partitioned by common name.",
	}
	labels := []string{"name"}
	metrics.DetectionCounter = prometheus.NewCounterVec(counterOpts, labels)

	if err := prometheus.Register(metrics.DetectionCounter); err != nil {
		return nil, err
	}

	// Additional metrics can be initialized here

	return metrics, nil
}

// RegisterMetricsHandlers adds metrics routes to the provided mux
func RegisterMetricsHandlers(mux *http.ServeMux) {
	mux.Handle(metricsPath, promhttp.Handler())
}

// IncrementDetectionCounter increments the detection counter for a given species
func (m *Metrics) IncrementDetectionCounter(speciesName string) {
	m.DetectionCounter.WithLabelValues(speciesName).Inc()
}
