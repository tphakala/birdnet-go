// Package metrics provides HTTP handler metrics for observability
package metrics

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

// HTTPMetrics contains Prometheus metrics for HTTP handler operations
type HTTPMetrics struct {
	registry *prometheus.Registry

	// HTTP request metrics
	httpRequestsTotal   *prometheus.CounterVec
	httpRequestDuration *prometheus.HistogramVec
	httpRequestErrors   *prometheus.CounterVec
	httpResponseSize    *prometheus.HistogramVec

	// Handler-specific metrics
	handlerOperationsTotal   *prometheus.CounterVec
	handlerOperationDuration *prometheus.HistogramVec
	handlerOperationErrors   *prometheus.CounterVec

	// Database operation metrics (for handlers)
	handlerDatabaseOpsTotal    *prometheus.CounterVec
	handlerDatabaseOpsDuration *prometheus.HistogramVec
	handlerDatabaseOpsErrors   *prometheus.CounterVec

	// Authentication metrics
	authOperationsTotal *prometheus.CounterVec
	authErrors          *prometheus.CounterVec

	// Template rendering metrics
	templateRenderDuration *prometheus.HistogramVec
	templateRenderErrors   *prometheus.CounterVec
}

// NewHTTPMetrics creates and registers new HTTP handler metrics
func NewHTTPMetrics(registry *prometheus.Registry) (*HTTPMetrics, error) {
	m := &HTTPMetrics{registry: registry}
	if err := m.initMetrics(); err != nil {
		return nil, err
	}
	if err := registry.Register(m); err != nil {
		return nil, err
	}
	return m, nil
}

// initMetrics initializes all Prometheus metrics
func (m *HTTPMetrics) initMetrics() error {
	// HTTP request metrics
	m.httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status_code"}, // method: GET, POST; path: /dashboard, /api/v1/detections; status_code: 200, 404, 500
	)

	m.httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Time taken for HTTP requests",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	m.httpRequestErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_request_errors_total",
			Help: "Total number of HTTP request errors",
		},
		[]string{"method", "path", "error_type"}, // error_type: validation, database, auth, template, system
	)

	m.httpResponseSize = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_response_size_bytes",
			Help:    "Size of HTTP responses in bytes",
			Buckets: prometheus.ExponentialBuckets(100, 10, 6), // 100B to ~100MB
		},
		[]string{"method", "path"},
	)

	// Handler-specific metrics
	m.handlerOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_handler_operations_total",
			Help: "Total number of handler operations",
		},
		[]string{"handler", "operation", "status"}, // handler: detections, dashboard, species; operation: get_data, render; status: success, error
	)

	m.handlerOperationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_handler_operation_duration_seconds",
			Help:    "Time taken for handler operations",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 12), // 1ms to ~4s
		},
		[]string{"handler", "operation"},
	)

	m.handlerOperationErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_handler_operation_errors_total",
			Help: "Total number of handler operation errors",
		},
		[]string{"handler", "operation", "error_type"},
	)

	// Database operation metrics (for handlers)
	m.handlerDatabaseOpsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_handler_database_operations_total",
			Help: "Total number of database operations from handlers",
		},
		[]string{"handler", "db_operation", "status"}, // db_operation: get_detections, get_species, save_detection
	)

	m.handlerDatabaseOpsDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_handler_database_operation_duration_seconds",
			Help:    "Time taken for database operations from handlers",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 15), // 1ms to ~32s
		},
		[]string{"handler", "db_operation"},
	)

	m.handlerDatabaseOpsErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_handler_database_operation_errors_total",
			Help: "Total number of database operation errors from handlers",
		},
		[]string{"handler", "db_operation", "error_type"},
	)

	// Authentication metrics
	m.authOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_auth_operations_total",
			Help: "Total number of authentication operations",
		},
		[]string{"auth_type", "operation", "status"}, // auth_type: basic, oauth2; operation: login, validate; status: success, error
	)

	m.authErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_auth_errors_total",
			Help: "Total number of authentication errors",
		},
		[]string{"auth_type", "error_type"}, // error_type: invalid_credentials, token_expired, access_denied
	)

	// Template rendering metrics
	m.templateRenderDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_template_render_duration_seconds",
			Help:    "Time taken for template rendering",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 10), // 1ms to ~1s
		},
		[]string{"template"},
	)

	m.templateRenderErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_template_render_errors_total",
			Help: "Total number of template rendering errors",
		},
		[]string{"template", "error_type"},
	)

	return nil
}

// Describe implements the Collector interface
func (m *HTTPMetrics) Describe(ch chan<- *prometheus.Desc) {
	m.httpRequestsTotal.Describe(ch)
	m.httpRequestDuration.Describe(ch)
	m.httpRequestErrors.Describe(ch)
	m.httpResponseSize.Describe(ch)
	m.handlerOperationsTotal.Describe(ch)
	m.handlerOperationDuration.Describe(ch)
	m.handlerOperationErrors.Describe(ch)
	m.handlerDatabaseOpsTotal.Describe(ch)
	m.handlerDatabaseOpsDuration.Describe(ch)
	m.handlerDatabaseOpsErrors.Describe(ch)
	m.authOperationsTotal.Describe(ch)
	m.authErrors.Describe(ch)
	m.templateRenderDuration.Describe(ch)
	m.templateRenderErrors.Describe(ch)
}

// Collect implements the Collector interface
func (m *HTTPMetrics) Collect(ch chan<- prometheus.Metric) {
	m.httpRequestsTotal.Collect(ch)
	m.httpRequestDuration.Collect(ch)
	m.httpRequestErrors.Collect(ch)
	m.httpResponseSize.Collect(ch)
	m.handlerOperationsTotal.Collect(ch)
	m.handlerOperationDuration.Collect(ch)
	m.handlerOperationErrors.Collect(ch)
	m.handlerDatabaseOpsTotal.Collect(ch)
	m.handlerDatabaseOpsDuration.Collect(ch)
	m.handlerDatabaseOpsErrors.Collect(ch)
	m.authOperationsTotal.Collect(ch)
	m.authErrors.Collect(ch)
	m.templateRenderDuration.Collect(ch)
	m.templateRenderErrors.Collect(ch)
}

// HTTP request recording methods

// RecordHTTPRequest records an HTTP request
func (m *HTTPMetrics) RecordHTTPRequest(method, path string, statusCode int, duration float64) {
	m.httpRequestsTotal.WithLabelValues(method, path, fmt.Sprintf("%d", statusCode)).Inc()
	m.httpRequestDuration.WithLabelValues(method, path).Observe(duration)
}

// RecordHTTPRequestError records an HTTP request error
func (m *HTTPMetrics) RecordHTTPRequestError(method, path, errorType string) {
	m.httpRequestErrors.WithLabelValues(method, path, errorType).Inc()
}

// RecordHTTPResponseSize records the size of an HTTP response
func (m *HTTPMetrics) RecordHTTPResponseSize(method, path string, sizeBytes int64) {
	m.httpResponseSize.WithLabelValues(method, path).Observe(float64(sizeBytes))
}

// Handler operation recording methods

// RecordHandlerOperation records a handler operation
func (m *HTTPMetrics) RecordHandlerOperation(handler, operation, status string) {
	m.handlerOperationsTotal.WithLabelValues(handler, operation, status).Inc()
}

// RecordHandlerOperationDuration records the duration of a handler operation
func (m *HTTPMetrics) RecordHandlerOperationDuration(handler, operation string, duration float64) {
	m.handlerOperationDuration.WithLabelValues(handler, operation).Observe(duration)
}

// RecordHandlerOperationError records a handler operation error
func (m *HTTPMetrics) RecordHandlerOperationError(handler, operation, errorType string) {
	m.handlerOperationErrors.WithLabelValues(handler, operation, errorType).Inc()
}

// Database operation recording methods

// RecordHandlerDatabaseOperation records a database operation from a handler
func (m *HTTPMetrics) RecordHandlerDatabaseOperation(handler, dbOperation, status string) {
	m.handlerDatabaseOpsTotal.WithLabelValues(handler, dbOperation, status).Inc()
}

// RecordHandlerDatabaseOperationDuration records the duration of a database operation from a handler
func (m *HTTPMetrics) RecordHandlerDatabaseOperationDuration(handler, dbOperation string, duration float64) {
	m.handlerDatabaseOpsDuration.WithLabelValues(handler, dbOperation).Observe(duration)
}

// RecordHandlerDatabaseOperationError records a database operation error from a handler
func (m *HTTPMetrics) RecordHandlerDatabaseOperationError(handler, dbOperation, errorType string) {
	m.handlerDatabaseOpsErrors.WithLabelValues(handler, dbOperation, errorType).Inc()
}

// Authentication recording methods

// RecordAuthOperation records an authentication operation
func (m *HTTPMetrics) RecordAuthOperation(authType, operation, status string) {
	m.authOperationsTotal.WithLabelValues(authType, operation, status).Inc()
}

// RecordAuthError records an authentication error
func (m *HTTPMetrics) RecordAuthError(authType, errorType string) {
	m.authErrors.WithLabelValues(authType, errorType).Inc()
}

// Template rendering recording methods

// RecordTemplateRender records template rendering duration
func (m *HTTPMetrics) RecordTemplateRender(template string, duration float64) {
	m.templateRenderDuration.WithLabelValues(template).Observe(duration)
}

// RecordTemplateRenderError records a template rendering error
func (m *HTTPMetrics) RecordTemplateRenderError(template, errorType string) {
	m.templateRenderErrors.WithLabelValues(template, errorType).Inc()
}
