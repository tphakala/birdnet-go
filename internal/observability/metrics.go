// Package observability provides metrics and monitoring capabilities for the BirdNET-Go application.
package observability

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/diskmanager"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// Metrics holds all the metric collectors for the application.
type Metrics struct {
	registry      *prometheus.Registry
	MQTT          *metrics.MQTTMetrics
	BirdNET       *metrics.BirdNETMetrics
	ImageProvider *metrics.ImageProviderMetrics
	DiskManager   *metrics.DiskManagerMetrics
	Weather       *metrics.WeatherMetrics
	SunCalc       *metrics.SunCalcMetrics
	Datastore     *metrics.DatastoreMetrics
	MyAudio       *metrics.MyAudioMetrics
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

	diskManagerMetrics, err := metrics.NewDiskManagerMetrics(registry)
	if err != nil {
		return nil, fmt.Errorf("failed to create DiskManager metrics: %w", err)
	}

	weatherMetrics, err := metrics.NewWeatherMetrics(registry)
	if err != nil {
		return nil, fmt.Errorf("failed to create Weather metrics: %w", err)
	}

	sunCalcMetrics, err := metrics.NewSunCalcMetrics(registry)
	if err != nil {
		return nil, fmt.Errorf("failed to create SunCalc metrics: %w", err)
	}

	datastoreMetrics, err := metrics.NewDatastoreMetrics(registry)
	if err != nil {
		return nil, fmt.Errorf("failed to create Datastore metrics: %w", err)
	}

	myAudioMetrics, err := metrics.NewMyAudioMetrics(registry)
	if err != nil {
		return nil, fmt.Errorf("failed to create MyAudio metrics: %w", err)
	}

	m := &Metrics{
		registry:      registry,
		MQTT:          mqttMetrics,
		BirdNET:       birdnetMetrics,
		ImageProvider: imageProviderMetrics,
		DiskManager:   diskManagerMetrics,
		Weather:       weatherMetrics,
		SunCalc:       sunCalcMetrics,
		Datastore:     datastoreMetrics,
		MyAudio:       myAudioMetrics,
	}

	// Initialize tracing with metrics
	initializeTracing(birdnetMetrics)

	// Initialize diskmanager with metrics
	diskmanager.SetMetrics(diskManagerMetrics)

	// Initialize myaudio with metrics
	initializeMyAudioMetrics(myAudioMetrics)

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

// initializeTracing sets up the birdnet tracing system with metrics
func initializeTracing(birdnetMetrics *metrics.BirdNETMetrics) {
	birdnet.SetMetrics(birdnetMetrics)
}

// initializeMyAudioMetrics sets up the myaudio buffer system with metrics
func initializeMyAudioMetrics(myAudioMetrics *metrics.MyAudioMetrics) {
	myaudio.SetAnalysisMetrics(myAudioMetrics)
	myaudio.SetCaptureMetrics(myAudioMetrics)
	myaudio.SetFileMetrics(myAudioMetrics)
	myaudio.SetProcessMetrics(myAudioMetrics)
	myaudio.SetFilterMetrics(myAudioMetrics)
}
