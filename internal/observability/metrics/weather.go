// Package metrics provides weather service metrics for observability
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// WeatherMetrics contains Prometheus metrics for weather service operations
type WeatherMetrics struct {
	registry *prometheus.Registry

	// Weather data fetch metrics
	weatherFetchsTotal      *prometheus.CounterVec
	weatherFetchErrorsTotal *prometheus.CounterVec
	weatherFetchDuration    *prometheus.HistogramVec

	// Weather data validation metrics
	weatherValidationErrorsTotal *prometheus.CounterVec

	// Weather provider metrics
	weatherProviderRequestsTotal *prometheus.CounterVec
	weatherProviderErrorsTotal   *prometheus.CounterVec

	// Database operations metrics
	weatherDbOperationsTotal *prometheus.CounterVec
	weatherDbErrorsTotal     *prometheus.CounterVec
	weatherDbDuration        *prometheus.HistogramVec

	// Weather data quality metrics
	weatherDataPointsTotal  *prometheus.CounterVec
	weatherTemperatureGauge prometheus.Gauge
	weatherHumidityGauge    prometheus.Gauge
	weatherPressureGauge    prometheus.Gauge
	weatherWindSpeedGauge   prometheus.Gauge
	weatherVisibilityGauge  prometheus.Gauge
}

// NewWeatherMetrics creates and registers new weather metrics
func NewWeatherMetrics(registry *prometheus.Registry) (*WeatherMetrics, error) {
	m := &WeatherMetrics{registry: registry}
	if err := m.initMetrics(); err != nil {
		return nil, err
	}
	if err := registry.Register(m); err != nil {
		return nil, err
	}
	return m, nil
}

// initMetrics initializes all Prometheus metrics
func (m *WeatherMetrics) initMetrics() error {
	// Weather data fetch metrics
	m.weatherFetchsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "weather_fetches_total",
			Help: "Total number of weather data fetch operations",
		},
		[]string{"provider", "status"}, // status: success, error
	)

	m.weatherFetchErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "weather_fetch_errors_total",
			Help: "Total number of weather fetch errors",
		},
		[]string{"provider", "error_type"},
	)

	m.weatherFetchDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "weather_fetch_duration_seconds",
			Help: "Time taken to fetch weather data",
			// Buckets cover typical weather API response times: 100ms to ~100s
			// Exponential buckets: 0.1, 0.2, 0.4, 0.8, 1.6, 3.2, 6.4, 12.8, 25.6, 51.2s
			// This range captures fast local responses (100ms) to slow/timeout scenarios (50s+)
			Buckets: prometheus.ExponentialBuckets(BucketStart100ms, BucketFactor2, BucketCount10),
		},
		[]string{"provider"},
	)

	// Weather data validation metrics
	m.weatherValidationErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "weather_validation_errors_total",
			Help: "Total number of weather data validation errors",
		},
		[]string{"validation_type"},
	)

	// Weather provider metrics
	m.weatherProviderRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "weather_provider_requests_total",
			Help: "Total number of requests to weather providers",
		},
		[]string{"provider", "method", "status_code"},
	)

	m.weatherProviderErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "weather_provider_errors_total",
			Help: "Total number of weather provider errors",
		},
		[]string{"provider", "error_type"},
	)

	// Database operations metrics
	m.weatherDbOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "weather_db_operations_total",
			Help: "Total number of weather database operations",
		},
		[]string{"operation", "status"}, // operation: save_daily_events, save_hourly_weather
	)

	m.weatherDbErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "weather_db_errors_total",
			Help: "Total number of weather database errors",
		},
		[]string{"operation", "error_type"},
	)

	m.weatherDbDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "weather_db_duration_seconds",
			Help: "Time taken for weather database operations",
			// Buckets cover typical database operation times: 1ms to ~1s
			// Exponential buckets: 1ms, 2ms, 4ms, 8ms, 16ms, 32ms, 64ms, 128ms, 256ms, 512ms
			// This range captures fast queries (1ms) to slow database operations (500ms+)
			Buckets: prometheus.ExponentialBuckets(BucketStart1ms, BucketFactor2, BucketCount10),
		},
		[]string{"operation"},
	)

	// Weather data quality metrics
	m.weatherDataPointsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "weather_data_points_total",
			Help: "Total number of weather data points processed",
		},
		[]string{"provider", "data_type"}, // data_type: temperature, humidity, pressure, etc.
	)

	m.weatherTemperatureGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "weather_temperature_celsius",
		Help: "Current weather temperature in Celsius",
	})

	m.weatherHumidityGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "weather_humidity_percentage",
		Help: "Current weather humidity percentage",
	})

	m.weatherPressureGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "weather_pressure_hpa",
		Help: "Current weather pressure in hPa",
	})

	m.weatherWindSpeedGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "weather_wind_speed_mps",
		Help: "Current weather wind speed in meters per second",
	})

	m.weatherVisibilityGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "weather_visibility_meters",
		Help: "Current weather visibility in meters",
	})

	return nil
}

// Describe implements the Collector interface
func (m *WeatherMetrics) Describe(ch chan<- *prometheus.Desc) {
	m.weatherFetchsTotal.Describe(ch)
	m.weatherFetchErrorsTotal.Describe(ch)
	m.weatherFetchDuration.Describe(ch)
	m.weatherValidationErrorsTotal.Describe(ch)
	m.weatherProviderRequestsTotal.Describe(ch)
	m.weatherProviderErrorsTotal.Describe(ch)
	m.weatherDbOperationsTotal.Describe(ch)
	m.weatherDbErrorsTotal.Describe(ch)
	m.weatherDbDuration.Describe(ch)
	m.weatherDataPointsTotal.Describe(ch)
	m.weatherTemperatureGauge.Describe(ch)
	m.weatherHumidityGauge.Describe(ch)
	m.weatherPressureGauge.Describe(ch)
	m.weatherWindSpeedGauge.Describe(ch)
	m.weatherVisibilityGauge.Describe(ch)
}

// Collect implements the Collector interface
func (m *WeatherMetrics) Collect(ch chan<- prometheus.Metric) {
	m.weatherFetchsTotal.Collect(ch)
	m.weatherFetchErrorsTotal.Collect(ch)
	m.weatherFetchDuration.Collect(ch)
	m.weatherValidationErrorsTotal.Collect(ch)
	m.weatherProviderRequestsTotal.Collect(ch)
	m.weatherProviderErrorsTotal.Collect(ch)
	m.weatherDbOperationsTotal.Collect(ch)
	m.weatherDbErrorsTotal.Collect(ch)
	m.weatherDbDuration.Collect(ch)
	m.weatherDataPointsTotal.Collect(ch)
	m.weatherTemperatureGauge.Collect(ch)
	m.weatherHumidityGauge.Collect(ch)
	m.weatherPressureGauge.Collect(ch)
	m.weatherWindSpeedGauge.Collect(ch)
	m.weatherVisibilityGauge.Collect(ch)
}

// RecordWeatherFetch records a weather fetch operation
func (m *WeatherMetrics) RecordWeatherFetch(provider, status string) {
	m.weatherFetchsTotal.WithLabelValues(provider, status).Inc()
}

// RecordWeatherFetchError records a weather fetch error
func (m *WeatherMetrics) RecordWeatherFetchError(provider, errorType string) {
	m.weatherFetchErrorsTotal.WithLabelValues(provider, errorType).Inc()
}

// RecordWeatherFetchDuration records the duration of a weather fetch operation
func (m *WeatherMetrics) RecordWeatherFetchDuration(provider string, duration float64) {
	m.weatherFetchDuration.WithLabelValues(provider).Observe(duration)
}

// RecordWeatherValidationError records a weather validation error
func (m *WeatherMetrics) RecordWeatherValidationError(validationType string) {
	m.weatherValidationErrorsTotal.WithLabelValues(validationType).Inc()
}

// RecordWeatherProviderRequest records a weather provider request
func (m *WeatherMetrics) RecordWeatherProviderRequest(provider, method, statusCode string) {
	m.weatherProviderRequestsTotal.WithLabelValues(provider, method, statusCode).Inc()
}

// RecordWeatherProviderError records a weather provider error
func (m *WeatherMetrics) RecordWeatherProviderError(provider, errorType string) {
	m.weatherProviderErrorsTotal.WithLabelValues(provider, errorType).Inc()
}

// RecordWeatherDbOperation records a weather database operation
func (m *WeatherMetrics) RecordWeatherDbOperation(operation, status string) {
	m.weatherDbOperationsTotal.WithLabelValues(operation, status).Inc()
}

// RecordWeatherDbError records a weather database error
func (m *WeatherMetrics) RecordWeatherDbError(operation, errorType string) {
	m.weatherDbErrorsTotal.WithLabelValues(operation, errorType).Inc()
}

// RecordWeatherDbDuration records the duration of a weather database operation
func (m *WeatherMetrics) RecordWeatherDbDuration(operation string, duration float64) {
	m.weatherDbDuration.WithLabelValues(operation).Observe(duration)
}

// RecordWeatherDataPoint records a weather data point
func (m *WeatherMetrics) RecordWeatherDataPoint(provider, dataType string) {
	m.weatherDataPointsTotal.WithLabelValues(provider, dataType).Inc()
}

// UpdateWeatherGauges updates the current weather gauge values
func (m *WeatherMetrics) UpdateWeatherGauges(temperature, humidity, pressure, windSpeed, visibility float64) {
	m.weatherTemperatureGauge.Set(temperature)
	m.weatherHumidityGauge.Set(humidity)
	m.weatherPressureGauge.Set(pressure)
	m.weatherWindSpeedGauge.Set(windSpeed)
	m.weatherVisibilityGauge.Set(visibility)
}
