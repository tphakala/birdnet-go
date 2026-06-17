// Package metrics provides custom Prometheus metrics for various components of the BirdNET-Go application.
package metrics

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

// GuideProviderMetrics contains all Prometheus metrics related to the species
// guide provider cache and its upstream providers (Wikipedia, eBird).
type GuideProviderMetrics struct {
	// CacheHits counts guide cache hits, labeled by tier (memory|db) and the
	// quality of the cached entry (full|intro_only|stub|negative).
	CacheHits *prometheus.CounterVec
	// CacheMisses counts guide cache misses, labeled by tier (memory|db).
	CacheMisses *prometheus.CounterVec
	// Fetches counts upstream provider fetches, labeled by provider and outcome
	// (success|not_found|error|transient_error).
	Fetches *prometheus.CounterVec
	// FetchDuration observes provider fetch latency, labeled by provider and outcome.
	FetchDuration *prometheus.HistogramVec
	// DBErrors counts datastore errors, labeled by error_type and operation.
	DBErrors *prometheus.CounterVec
	// NegativeEntries counts negative (not-found) cache entries created.
	NegativeEntries prometheus.Counter
	// CachePopulationRatio is the fraction of warmed/target species present in the cache.
	CachePopulationRatio prometheus.Gauge

	registry *prometheus.Registry
}

// NewGuideProviderMetrics creates a new instance of GuideProviderMetrics.
// It requires a Prometheus registry to register the metrics.
// It returns an error if metric registration fails.
func NewGuideProviderMetrics(registry *prometheus.Registry) (*GuideProviderMetrics, error) {
	m := &GuideProviderMetrics{registry: registry}
	if err := m.initMetrics(); err != nil {
		return nil, fmt.Errorf("failed to initialize GuideProvider metrics: %w", err)
	}
	if err := registry.Register(m); err != nil {
		return nil, fmt.Errorf("failed to register GuideProvider metrics: %w", err)
	}
	return m, nil
}

// initMetrics initializes all metrics for GuideProviderMetrics.
func (m *GuideProviderMetrics) initMetrics() error {
	m.CacheHits = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "guide_provider_cache_hits_total",
		Help: "Total number of species guide cache hits.",
	}, []string{"tier", "quality"}) // tier: memory|db, quality: full|intro_only|stub|negative

	m.CacheMisses = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "guide_provider_cache_misses_total",
		Help: "Total number of species guide cache misses.",
	}, []string{"tier"}) // tier: memory|db

	m.Fetches = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "guide_provider_fetches_total",
		Help: "Total number of upstream provider fetches.",
	}, []string{"provider", "outcome"}) // outcome: success|not_found|error|transient_error

	m.FetchDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "guide_provider_fetch_duration_seconds",
		Help:    "Latency of upstream provider fetches in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"provider", "outcome"})

	m.DBErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "guide_provider_db_errors_total",
		Help: "Total number of species guide datastore errors.",
	}, []string{"error_type", "operation"})

	m.NegativeEntries = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "guide_provider_negative_entries_total",
		Help: "Total number of negative (not-found) cache entries created.",
	})

	m.CachePopulationRatio = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "guide_provider_cache_population_ratio",
		Help: "Fraction of target species currently present in the guide cache (0-1).",
	})

	return nil
}

// RecordCacheHit records a cache hit at the given tier with the entry quality.
func (m *GuideProviderMetrics) RecordCacheHit(tier, quality string) {
	m.CacheHits.WithLabelValues(tier, quality).Inc()
}

// RecordCacheMiss records a cache miss at the given tier.
func (m *GuideProviderMetrics) RecordCacheMiss(tier string) {
	m.CacheMisses.WithLabelValues(tier).Inc()
}

// RecordFetch records an upstream provider fetch and its latency.
func (m *GuideProviderMetrics) RecordFetch(provider, outcome string, seconds float64) {
	m.Fetches.WithLabelValues(provider, outcome).Inc()
	m.FetchDuration.WithLabelValues(provider, outcome).Observe(seconds)
}

// RecordDBError records a datastore error categorized by type and operation.
func (m *GuideProviderMetrics) RecordDBError(errorType, operation string) {
	m.DBErrors.WithLabelValues(errorType, operation).Inc()
}

// RecordNegativeEntry records creation of a negative (not-found) cache entry.
func (m *GuideProviderMetrics) RecordNegativeEntry() {
	m.NegativeEntries.Inc()
}

// UpdateCachePopulationRatio sets the cache population ratio gauge (0-1).
func (m *GuideProviderMetrics) UpdateCachePopulationRatio(ratio float64) {
	m.CachePopulationRatio.Set(ratio)
}

// Collect implements the prometheus.Collector interface.
func (m *GuideProviderMetrics) Collect(ch chan<- prometheus.Metric) {
	m.CacheHits.Collect(ch)
	m.CacheMisses.Collect(ch)
	m.Fetches.Collect(ch)
	m.FetchDuration.Collect(ch)
	m.DBErrors.Collect(ch)
	m.NegativeEntries.Collect(ch)
	m.CachePopulationRatio.Collect(ch)
}

// Describe implements the prometheus.Collector interface.
func (m *GuideProviderMetrics) Describe(ch chan<- *prometheus.Desc) {
	m.CacheHits.Describe(ch)
	m.CacheMisses.Describe(ch)
	m.Fetches.Describe(ch)
	m.FetchDuration.Describe(ch)
	m.DBErrors.Describe(ch)
	m.NegativeEntries.Describe(ch)
	m.CachePopulationRatio.Describe(ch)
}
