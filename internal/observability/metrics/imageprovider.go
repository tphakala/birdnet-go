// Package metrics provides custom Prometheus metrics for various components of the BirdNET-Go application.
package metrics

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

// ImageProviderMetrics contains all Prometheus metrics related to the image provider operations.
type ImageProviderMetrics struct {
	CacheHits      prometheus.Counter
	CacheMisses    prometheus.Counter
	ImageDownloads prometheus.Counter
	DownloadErrors *prometheus.CounterVec
	registry       *prometheus.Registry
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

	m.DownloadErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "image_provider_download_errors_total",
		Help: "Total number of image download errors.",
	}, []string{"error_category", "provider", "operation"})

	return nil
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

// IncrementDownloadErrorsWithCategory increases the download error counter with categorization.
func (m *ImageProviderMetrics) IncrementDownloadErrorsWithCategory(category, provider, operation string) {
	m.DownloadErrors.WithLabelValues(category, provider, operation).Inc()
}

// Collect implements the prometheus.Collector interface.
func (m *ImageProviderMetrics) Collect(ch chan<- prometheus.Metric) {
	m.CacheHits.Collect(ch)
	m.CacheMisses.Collect(ch)
	m.ImageDownloads.Collect(ch)
	m.DownloadErrors.Collect(ch)
}

// Describe implements the prometheus.Collector interface.
func (m *ImageProviderMetrics) Describe(ch chan<- *prometheus.Desc) {
	m.CacheHits.Describe(ch)
	m.CacheMisses.Describe(ch)
	m.ImageDownloads.Describe(ch)
	m.DownloadErrors.Describe(ch)
}
