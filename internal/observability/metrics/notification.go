// Package metrics provides custom Prometheus metrics for notification operations.
package metrics

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// NotificationMetrics contains all Prometheus metrics related to notification and push provider operations.
type NotificationMetrics struct {
	// Provider delivery metrics
	ProviderDeliveriesTotal   *prometheus.CounterVec // Total deliveries by provider, type, status
	ProviderDeliveryDuration  *prometheus.HistogramVec // Latency by provider and type
	ProviderDeliveryErrors    *prometheus.CounterVec // Errors by provider, type, error_category

	// Provider health metrics
	ProviderHealthStatus      *prometheus.GaugeVec // Current health status (1=healthy, 0=unhealthy) by provider
	ProviderCircuitBreakerState *prometheus.GaugeVec // Circuit breaker state (0=closed, 1=half-open, 2=open) by provider
	ProviderConsecutiveFailures *prometheus.GaugeVec // Consecutive failure count by provider
	ProviderLastSuccessTime   *prometheus.GaugeVec // Timestamp of last successful delivery by provider

	// Retry metrics
	ProviderRetryAttempts     *prometheus.CounterVec // Retry attempts by provider
	ProviderRetrySuccesses    *prometheus.CounterVec // Successful retries by provider

	// Filter metrics
	FilterMatchesTotal        *prometheus.CounterVec // Notifications matched by filter type
	FilterRejectionsTotal     *prometheus.CounterVec // Notifications rejected by filter reason

	// Dispatcher metrics
	NotificationDispatchTotal  prometheus.Counter // Total notifications dispatched
	NotificationDispatchActive prometheus.Gauge   // Currently active dispatch operations
	NotificationQueueDepth     prometheus.Gauge   // Notification queue depth (if buffered)

	// Provider-specific metadata
	ProviderTimeouts          *prometheus.CounterVec // Timeout occurrences by provider

	registry *prometheus.Registry
}

// NewNotificationMetrics creates a new instance of NotificationMetrics.
// It requires a Prometheus registry to register the metrics.
// It returns an error if metric registration fails.
func NewNotificationMetrics(registry *prometheus.Registry) (*NotificationMetrics, error) {
	m := &NotificationMetrics{registry: registry}
	if err := m.initMetrics(); err != nil {
		return nil, fmt.Errorf("failed to initialize notification metrics: %w", err)
	}
	if err := registry.Register(m); err != nil {
		return nil, fmt.Errorf("failed to register notification metrics: %w", err)
	}
	return m, nil
}

// initMetrics initializes all metrics for NotificationMetrics.
func (m *NotificationMetrics) initMetrics() error {
	// Provider delivery metrics
	m.ProviderDeliveriesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "notification_provider_deliveries_total",
			Help: "Total number of notification delivery attempts by provider, notification type, and status",
		},
		[]string{"provider", "notification_type", "status"}, // status: success, error, timeout
	)

	m.ProviderDeliveryDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "notification_provider_delivery_duration_seconds",
			Help:    "Time taken for notification delivery by provider and notification type",
			Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1.0, 2.0, 5.0, 10.0, 30.0}, // 10ms to 30s
		},
		[]string{"provider", "notification_type"},
	)

	m.ProviderDeliveryErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "notification_provider_delivery_errors_total",
			Help: "Total number of notification delivery errors by provider, type, and error category",
		},
		[]string{"provider", "notification_type", "error_category"}, // error_category: network, timeout, validation, provider_error
	)

	// Provider health metrics
	m.ProviderHealthStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "notification_provider_health_status",
			Help: "Current health status of notification provider (1=healthy, 0=unhealthy)",
		},
		[]string{"provider"},
	)

	m.ProviderCircuitBreakerState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "notification_provider_circuit_breaker_state",
			Help: "Circuit breaker state for notification provider (0=closed, 1=half-open, 2=open)",
		},
		[]string{"provider"},
	)

	m.ProviderConsecutiveFailures = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "notification_provider_consecutive_failures",
			Help: "Number of consecutive failures for notification provider",
		},
		[]string{"provider"},
	)

	m.ProviderLastSuccessTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "notification_provider_last_success_timestamp_seconds",
			Help: "Timestamp of last successful notification delivery by provider",
		},
		[]string{"provider"},
	)

	// Retry metrics
	m.ProviderRetryAttempts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "notification_provider_retry_attempts_total",
			Help: "Total number of retry attempts by provider",
		},
		[]string{"provider"},
	)

	m.ProviderRetrySuccesses = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "notification_provider_retry_successes_total",
			Help: "Total number of successful retries by provider",
		},
		[]string{"provider"},
	)

	// Filter metrics
	m.FilterMatchesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "notification_filter_matches_total",
			Help: "Total number of notifications matched by filter type",
		},
		[]string{"provider", "filter_type"}, // filter_type: type, priority, component, metadata
	)

	m.FilterRejectionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "notification_filter_rejections_total",
			Help: "Total number of notifications rejected by filter",
		},
		[]string{"provider", "rejection_reason"}, // rejection_reason: type_mismatch, priority_mismatch, etc.
	)

	// Dispatcher metrics
	m.NotificationDispatchTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "notification_dispatch_total",
			Help: "Total number of notifications dispatched to all providers",
		},
	)

	m.NotificationDispatchActive = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "notification_dispatch_active",
			Help: "Number of currently active notification dispatch operations",
		},
	)

	m.NotificationQueueDepth = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "notification_queue_depth",
			Help: "Current depth of notification queue",
		},
	)

	// Provider-specific metadata
	m.ProviderTimeouts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "notification_provider_timeouts_total",
			Help: "Total number of timeout occurrences by provider",
		},
		[]string{"provider"},
	)

	return nil
}

// RecordDelivery records a notification delivery attempt.
func (m *NotificationMetrics) RecordDelivery(provider, notificationType, status string, duration time.Duration) {
	m.ProviderDeliveriesTotal.WithLabelValues(provider, notificationType, status).Inc()
	m.ProviderDeliveryDuration.WithLabelValues(provider, notificationType).Observe(duration.Seconds())

	if status == "success" {
		m.ProviderLastSuccessTime.WithLabelValues(provider).SetToCurrentTime()
		m.ProviderConsecutiveFailures.WithLabelValues(provider).Set(0)
	}
}

// RecordDeliveryError records a notification delivery error.
func (m *NotificationMetrics) RecordDeliveryError(provider, notificationType, errorCategory string) {
	m.ProviderDeliveryErrors.WithLabelValues(provider, notificationType, errorCategory).Inc()
}

// RecordTimeout records a provider timeout.
func (m *NotificationMetrics) RecordTimeout(provider string) {
	m.ProviderTimeouts.WithLabelValues(provider).Inc()
}

// RecordRetryAttempt records a retry attempt.
func (m *NotificationMetrics) RecordRetryAttempt(provider string) {
	m.ProviderRetryAttempts.WithLabelValues(provider).Inc()
}

// RecordRetrySuccess records a successful retry.
func (m *NotificationMetrics) RecordRetrySuccess(provider string) {
	m.ProviderRetrySuccesses.WithLabelValues(provider).Inc()
}

// UpdateHealthStatus updates the health status of a provider.
func (m *NotificationMetrics) UpdateHealthStatus(provider string, healthy bool) {
	if healthy {
		m.ProviderHealthStatus.WithLabelValues(provider).Set(1)
	} else {
		m.ProviderHealthStatus.WithLabelValues(provider).Set(0)
	}
}

// UpdateCircuitBreakerState updates the circuit breaker state.
// state: 0=closed, 1=half-open, 2=open
func (m *NotificationMetrics) UpdateCircuitBreakerState(provider string, state int) {
	m.ProviderCircuitBreakerState.WithLabelValues(provider).Set(float64(state))
}

// IncrementConsecutiveFailures increments the consecutive failure count.
func (m *NotificationMetrics) IncrementConsecutiveFailures(provider string) {
	m.ProviderConsecutiveFailures.WithLabelValues(provider).Inc()
}

// RecordFilterMatch records a filter match.
func (m *NotificationMetrics) RecordFilterMatch(provider, filterType string) {
	m.FilterMatchesTotal.WithLabelValues(provider, filterType).Inc()
}

// RecordFilterRejection records a filter rejection.
func (m *NotificationMetrics) RecordFilterRejection(provider, reason string) {
	m.FilterRejectionsTotal.WithLabelValues(provider, reason).Inc()
}

// IncrementDispatchTotal increments the total dispatch counter.
func (m *NotificationMetrics) IncrementDispatchTotal() {
	m.NotificationDispatchTotal.Inc()
}

// SetDispatchActive sets the active dispatch gauge.
func (m *NotificationMetrics) SetDispatchActive(count int) {
	m.NotificationDispatchActive.Set(float64(count))
}

// SetQueueDepth sets the notification queue depth.
func (m *NotificationMetrics) SetQueueDepth(depth int) {
	m.NotificationQueueDepth.Set(float64(depth))
}

// Collect implements the prometheus.Collector interface.
func (m *NotificationMetrics) Collect(ch chan<- prometheus.Metric) {
	m.ProviderDeliveriesTotal.Collect(ch)
	m.ProviderDeliveryDuration.Collect(ch)
	m.ProviderDeliveryErrors.Collect(ch)
	m.ProviderHealthStatus.Collect(ch)
	m.ProviderCircuitBreakerState.Collect(ch)
	m.ProviderConsecutiveFailures.Collect(ch)
	m.ProviderLastSuccessTime.Collect(ch)
	m.ProviderRetryAttempts.Collect(ch)
	m.ProviderRetrySuccesses.Collect(ch)
	m.FilterMatchesTotal.Collect(ch)
	m.FilterRejectionsTotal.Collect(ch)
	m.NotificationDispatchTotal.Collect(ch)
	m.NotificationDispatchActive.Collect(ch)
	m.NotificationQueueDepth.Collect(ch)
	m.ProviderTimeouts.Collect(ch)
}

// Describe implements the prometheus.Collector interface.
func (m *NotificationMetrics) Describe(ch chan<- *prometheus.Desc) {
	m.ProviderDeliveriesTotal.Describe(ch)
	m.ProviderDeliveryDuration.Describe(ch)
	m.ProviderDeliveryErrors.Describe(ch)
	m.ProviderHealthStatus.Describe(ch)
	m.ProviderCircuitBreakerState.Describe(ch)
	m.ProviderConsecutiveFailures.Describe(ch)
	m.ProviderLastSuccessTime.Describe(ch)
	m.ProviderRetryAttempts.Describe(ch)
	m.ProviderRetrySuccesses.Describe(ch)
	m.FilterMatchesTotal.Describe(ch)
	m.FilterRejectionsTotal.Describe(ch)
	m.NotificationDispatchTotal.Describe(ch)
	m.NotificationDispatchActive.Describe(ch)
	m.NotificationQueueDepth.Describe(ch)
	m.ProviderTimeouts.Describe(ch)
}

// StartDeliveryTimer creates a timer for measuring delivery duration.
func (m *NotificationMetrics) StartDeliveryTimer() *DeliveryTimer {
	return &DeliveryTimer{
		startTime: time.Now(),
		metrics:   m,
	}
}

// DeliveryTimer is a helper struct for measuring delivery duration.
type DeliveryTimer struct {
	startTime time.Time
	metrics   *NotificationMetrics
}

// ObserveDuration stops the timer and records the duration with delivery status.
func (dt *DeliveryTimer) ObserveDuration(provider, notificationType, status string) {
	duration := time.Since(dt.startTime)
	dt.metrics.RecordDelivery(provider, notificationType, status, duration)
}
