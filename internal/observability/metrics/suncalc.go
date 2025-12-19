// Package metrics provides suncalc service metrics for observability
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// SunCalcMetrics contains Prometheus metrics for suncalc service operations
type SunCalcMetrics struct {
	registry *prometheus.Registry

	// Sun calculation metrics
	sunCalcOperationsTotal  *prometheus.CounterVec
	sunCalcErrorsTotal      *prometheus.CounterVec
	sunCalcDurationSeconds  *prometheus.HistogramVec
	sunCalcCacheHitsTotal   *prometheus.CounterVec
	sunCalcCacheMissesTotal *prometheus.CounterVec

	// Astronomical calculation metrics
	astralCalculationsTotal *prometheus.CounterVec
	astralErrorsTotal       *prometheus.CounterVec

	// Time conversion metrics
	timeConversionTotal  *prometheus.CounterVec
	timeConversionErrors *prometheus.CounterVec

	// Cache metrics
	sunCalcCacheSize     prometheus.Gauge
	sunCalcCacheEviction *prometheus.CounterVec

	// Sun event timing metrics
	sunriseTimeGauge prometheus.Gauge
	sunsetTimeGauge  prometheus.Gauge
	civilDawnGauge   prometheus.Gauge
	civilDuskGauge   prometheus.Gauge
	dayLengthGauge   prometheus.Gauge
}

// NewSunCalcMetrics creates and registers new suncalc metrics
func NewSunCalcMetrics(registry *prometheus.Registry) (*SunCalcMetrics, error) {
	m := &SunCalcMetrics{registry: registry}
	if err := m.initMetrics(); err != nil {
		return nil, err
	}
	if err := registry.Register(m); err != nil {
		return nil, err
	}
	return m, nil
}

// initMetrics initializes all Prometheus metrics
func (m *SunCalcMetrics) initMetrics() error {
	// Sun calculation metrics
	m.sunCalcOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "suncalc_operations_total",
			Help: "Total number of sun calculation operations",
		},
		[]string{"operation", "status"}, // operation: get_sun_events, get_sunrise, get_sunset; status: success, error
	)

	m.sunCalcErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "suncalc_errors_total",
			Help: "Total number of sun calculation errors",
		},
		[]string{"operation", "error_type"},
	)

	m.sunCalcDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "suncalc_duration_seconds",
			Help:    "Time taken for sun calculation operations",
			Buckets: prometheus.ExponentialBuckets(BucketStart1ms, BucketFactor2, BucketCount10), // 1ms to ~1s
		},
		[]string{"operation"},
	)

	m.sunCalcCacheHitsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "suncalc_cache_hits_total",
			Help: "Total number of sun calculation cache hits",
		},
		[]string{"operation"},
	)

	m.sunCalcCacheMissesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "suncalc_cache_misses_total",
			Help: "Total number of sun calculation cache misses",
		},
		[]string{"operation"},
	)

	// Astronomical calculation metrics
	m.astralCalculationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "suncalc_astral_calculations_total",
			Help: "Total number of astral library calculations",
		},
		[]string{"calculation_type", "status"}, // calculation_type: sunrise, sunset, dawn, dusk
	)

	m.astralErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "suncalc_astral_errors_total",
			Help: "Total number of astral library calculation errors",
		},
		[]string{"calculation_type", "error_type"},
	)

	// Time conversion metrics
	m.timeConversionTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "suncalc_time_conversion_total",
			Help: "Total number of time conversion operations",
		},
		[]string{"conversion_type", "status"}, // conversion_type: utc_to_local
	)

	m.timeConversionErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "suncalc_time_conversion_errors_total",
			Help: "Total number of time conversion errors",
		},
		[]string{"conversion_type", "error_type"},
	)

	// Cache metrics
	m.sunCalcCacheSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "suncalc_cache_size",
		Help: "Current number of entries in the suncalc cache",
	})

	m.sunCalcCacheEviction = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "suncalc_cache_evictions_total",
			Help: "Total number of cache evictions",
		},
		[]string{"reason"}, // reason: expired, size_limit
	)

	// Sun event timing metrics (in Unix timestamp format)
	m.sunriseTimeGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "suncalc_sunrise_time_seconds",
		Help: "Current day sunrise time as Unix timestamp",
	})

	m.sunsetTimeGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "suncalc_sunset_time_seconds",
		Help: "Current day sunset time as Unix timestamp",
	})

	m.civilDawnGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "suncalc_civil_dawn_time_seconds",
		Help: "Current day civil dawn time as Unix timestamp",
	})

	m.civilDuskGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "suncalc_civil_dusk_time_seconds",
		Help: "Current day civil dusk time as Unix timestamp",
	})

	m.dayLengthGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "suncalc_day_length_seconds",
		Help: "Current day length in seconds (sunset - sunrise)",
	})

	return nil
}

// Describe implements the Collector interface
func (m *SunCalcMetrics) Describe(ch chan<- *prometheus.Desc) {
	m.sunCalcOperationsTotal.Describe(ch)
	m.sunCalcErrorsTotal.Describe(ch)
	m.sunCalcDurationSeconds.Describe(ch)
	m.sunCalcCacheHitsTotal.Describe(ch)
	m.sunCalcCacheMissesTotal.Describe(ch)
	m.astralCalculationsTotal.Describe(ch)
	m.astralErrorsTotal.Describe(ch)
	m.timeConversionTotal.Describe(ch)
	m.timeConversionErrors.Describe(ch)
	m.sunCalcCacheSize.Describe(ch)
	m.sunCalcCacheEviction.Describe(ch)
	m.sunriseTimeGauge.Describe(ch)
	m.sunsetTimeGauge.Describe(ch)
	m.civilDawnGauge.Describe(ch)
	m.civilDuskGauge.Describe(ch)
	m.dayLengthGauge.Describe(ch)
}

// Collect implements the Collector interface
func (m *SunCalcMetrics) Collect(ch chan<- prometheus.Metric) {
	m.sunCalcOperationsTotal.Collect(ch)
	m.sunCalcErrorsTotal.Collect(ch)
	m.sunCalcDurationSeconds.Collect(ch)
	m.sunCalcCacheHitsTotal.Collect(ch)
	m.sunCalcCacheMissesTotal.Collect(ch)
	m.astralCalculationsTotal.Collect(ch)
	m.astralErrorsTotal.Collect(ch)
	m.timeConversionTotal.Collect(ch)
	m.timeConversionErrors.Collect(ch)
	m.sunCalcCacheSize.Collect(ch)
	m.sunCalcCacheEviction.Collect(ch)
	m.sunriseTimeGauge.Collect(ch)
	m.sunsetTimeGauge.Collect(ch)
	m.civilDawnGauge.Collect(ch)
	m.civilDuskGauge.Collect(ch)
	m.dayLengthGauge.Collect(ch)
}

// RecordSunCalcOperation records a sun calculation operation
func (m *SunCalcMetrics) RecordSunCalcOperation(operation, status string) {
	m.sunCalcOperationsTotal.WithLabelValues(operation, status).Inc()
}

// RecordSunCalcError records a sun calculation error
func (m *SunCalcMetrics) RecordSunCalcError(operation, errorType string) {
	m.sunCalcErrorsTotal.WithLabelValues(operation, errorType).Inc()
}

// RecordSunCalcDuration records the duration of a sun calculation operation
func (m *SunCalcMetrics) RecordSunCalcDuration(operation string, duration float64) {
	m.sunCalcDurationSeconds.WithLabelValues(operation).Observe(duration)
}

// RecordSunCalcCacheHit records a cache hit
func (m *SunCalcMetrics) RecordSunCalcCacheHit(operation string) {
	m.sunCalcCacheHitsTotal.WithLabelValues(operation).Inc()
}

// RecordSunCalcCacheMiss records a cache miss
func (m *SunCalcMetrics) RecordSunCalcCacheMiss(operation string) {
	m.sunCalcCacheMissesTotal.WithLabelValues(operation).Inc()
}

// RecordAstralCalculation records an astral library calculation
func (m *SunCalcMetrics) RecordAstralCalculation(calculationType, status string) {
	m.astralCalculationsTotal.WithLabelValues(calculationType, status).Inc()
}

// RecordAstralError records an astral library calculation error
func (m *SunCalcMetrics) RecordAstralError(calculationType, errorType string) {
	m.astralErrorsTotal.WithLabelValues(calculationType, errorType).Inc()
}

// RecordTimeConversion records a time conversion operation
func (m *SunCalcMetrics) RecordTimeConversion(conversionType, status string) {
	m.timeConversionTotal.WithLabelValues(conversionType, status).Inc()
}

// RecordTimeConversionError records a time conversion error
func (m *SunCalcMetrics) RecordTimeConversionError(conversionType, errorType string) {
	m.timeConversionErrors.WithLabelValues(conversionType, errorType).Inc()
}

// UpdateCacheSize updates the cache size gauge
func (m *SunCalcMetrics) UpdateCacheSize(size float64) {
	m.sunCalcCacheSize.Set(size)
}

// RecordCacheEviction records a cache eviction
func (m *SunCalcMetrics) RecordCacheEviction(reason string) {
	m.sunCalcCacheEviction.WithLabelValues(reason).Inc()
}

// UpdateSunTimes updates the sun event time gauges
func (m *SunCalcMetrics) UpdateSunTimes(sunrise, sunset, civilDawn, civilDusk float64) {
	m.sunriseTimeGauge.Set(sunrise)
	m.sunsetTimeGauge.Set(sunset)
	m.civilDawnGauge.Set(civilDawn)
	m.civilDuskGauge.Set(civilDusk)

	// Calculate and set day length
	if sunset > sunrise {
		dayLength := sunset - sunrise
		m.dayLengthGauge.Set(dayLength)
	}
}
