// Package telemetry provides metrics and monitoring capabilities for the BirdNET-Go application.
package telemetry

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/tphakala/birdnet-go/internal/telemetry/metrics"
)

// Metrics holds all the metric collectors for the application.
type Metrics struct {
	registry      *prometheus.Registry
	MQTT          *metrics.MQTTMetrics
	BirdNET       *metrics.BirdNETMetrics
	ImageProvider *metrics.ImageProviderMetrics
}

// NewMetrics creates a new instance of Metrics, initializing all metric collectors.
// It returns an error if any metric collector fails to initialize.
func NewMetrics() (*Metrics, error) {
	registry := prometheus.NewRegistry()

	mqttMetrics, err := metrics.NewMQTTMetrics(registry)
	if err != nil {
		return nil, fmt.Errorf("failed to create MQTT metrics: %w", err)
	}

	birdnetMetrics, err := metrics.NewBirdNETMetrics(registry)
	if err != nil {
		return nil, fmt.Errorf("failed to create BirdNET metrics: %w", err)
	}

	imageProviderMetrics, err := metrics.NewImageProviderMetrics(registry)
	if err != nil {
		return nil, fmt.Errorf("failed to create ImageProvider metrics: %w", err)
	}

	m := &Metrics{
		registry:      registry,
		MQTT:          mqttMetrics,
		BirdNET:       birdnetMetrics,
		ImageProvider: imageProviderMetrics,
	}

	return m, nil
}

// RegisterHandlers registers the metrics endpoint with the provided http.ServeMux.
func (m *Metrics) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/metrics", m.metricsHandler)
}

// metricsHandler is the HTTP handler for the /metrics endpoint.
func (m *Metrics) metricsHandler(w http.ResponseWriter, r *http.Request) {
	h := promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		ErrorLog:      log.New(os.Stderr, "metrics handler: ", log.LstdFlags),
		ErrorHandling: promhttp.HTTPErrorOnError,
	})
	h.ServeHTTP(w, r)
}
