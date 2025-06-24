// Package metrics provides custom Prometheus metrics for various components of the BirdNET-Go application.
package metrics

import (
	"fmt"
	"log"

	"github.com/prometheus/client_golang/prometheus"
)

// ImageProviderMetrics contains all Prometheus metrics related to the image provider operations.
type ImageProviderMetrics struct {
	CacheSize        prometheus.Gauge
	CacheHits        prometheus.Counter
	CacheMisses      prometheus.Counter
	ImageDownloads   prometheus.Counter
	DownloadErrors   prometheus.Counter
	DownloadDuration prometheus.Histogram
	registry         *prometheus.Registry
}

// NewImageProviderMetrics creates a new instance of ImageProviderMetrics.
// It requires a Prometheus registry to register the metrics.
// It returns an error if metric registration fails.
func NewImageProviderMetrics(registry *prometheus.Registry) (*ImageProviderMetrics, error) {
	m := &ImageProviderMetrics{registry: registry}
	if err := m.initMetrics(); err != nil {
		return nil, fmt.Errorf("failed to initialize ImageProvider metrics: %w", err)
	}
	if err := registry.Register(m); err != nil {
		return nil, fmt.Errorf("failed to register ImageProvider metrics: %w", err)
	}
	return m, nil
}

// initMetrics initializes all metrics for ImageProviderMetrics.
func (m *ImageProviderMetrics) initMetrics() error {
	m.CacheSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "image_provider_cache_size_bytes",
		Help: "Current size of the image cache in bytes.",
	})

	m.CacheHits = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "image_provider_cache_hits_total",
		Help: "Total number of cache hits.",
	})

	m.CacheMisses = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "image_provider_cache_misses_total",
		Help: "Total number of cache misses.",
	})

	m.ImageDownloads = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "image_provider_downloads_total",
		Help: "Total number of image downloads.",
	})

	m.DownloadErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "image_provider_download_errors_total",
		Help: "Total number of image download errors.",
	})

	m.DownloadDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "image_provider_download_duration_seconds",
		Help:    "Duration of image downloads in seconds.",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 10),
	})

	return nil
}

// SetCacheSize updates the current size of the image cache in bytes.
func (m *ImageProviderMetrics) SetCacheSize(sizeBytes float64) {
	m.CacheSize.Set(sizeBytes)
}

// IncrementCacheHits increases the cache hit counter by one.
func (m *ImageProviderMetrics) IncrementCacheHits() {
	m.CacheHits.Inc()
}

// IncrementCacheMisses increases the cache miss counter by one.
func (m *ImageProviderMetrics) IncrementCacheMisses() {
	m.CacheMisses.Inc()
}

// IncrementImageDownloads increases the image download counter by one.
func (m *ImageProviderMetrics) IncrementImageDownloads() {
	m.ImageDownloads.Inc()
}

// IncrementDownloadErrors increases the download error counter by one.
func (m *ImageProviderMetrics) IncrementDownloadErrors() {
	m.DownloadErrors.Inc()
}

// ObserveDownloadDuration records the duration of an image download operation.
// The duration should be provided in seconds.
func (m *ImageProviderMetrics) ObserveDownloadDuration(durationSeconds float64) {
	m.DownloadDuration.Observe(durationSeconds)
}

// Collect implements the prometheus.Collector interface.
func (m *ImageProviderMetrics) Collect(ch chan<- prometheus.Metric) {
	log.Println("ImageProviderMetrics Collect method called")
	ch <- m.CacheSize
	ch <- m.CacheHits
	ch <- m.CacheMisses
	ch <- m.ImageDownloads
	ch <- m.DownloadErrors
	ch <- m.DownloadDuration
}

// Describe implements the prometheus.Collector interface.
func (m *ImageProviderMetrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.CacheSize.Desc()
	ch <- m.CacheHits.Desc()
	ch <- m.CacheMisses.Desc()
	ch <- m.ImageDownloads.Desc()
	ch <- m.DownloadErrors.Desc()
	ch <- m.DownloadDuration.Desc()
}
