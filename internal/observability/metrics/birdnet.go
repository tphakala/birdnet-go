// Package metrics provides custom Prometheus metrics for the BirdNET-Go application.
package metrics

import (
	"fmt"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// BirdNETMetrics contains all Prometheus metrics related to BirdNET operations.
type BirdNETMetrics struct {
	DetectionCounter *prometheus.CounterVec
	ProcessTimeGauge prometheus.Gauge

	// Performance metrics
	PredictionDuration   *prometheus.HistogramVec
	ChunkProcessDuration *prometheus.HistogramVec
	ModelInvokeDuration  *prometheus.HistogramVec
	RangeFilterDuration  *prometheus.HistogramVec

	// Operation counters
	PredictionTotal  *prometheus.CounterVec
	PredictionErrors *prometheus.CounterVec
	ModelLoadTotal   *prometheus.CounterVec
	ModelLoadErrors  *prometheus.CounterVec

	// Current state gauges
	ActiveProcessingGauge prometheus.Gauge
	ModelLoadedGauge      prometheus.Gauge

	registry *prometheus.Registry
}

// NewBirdNETMetrics creates a new instance of BirdNETMetrics.
// It requires a Prometheus registry to register the metrics.
// It returns an error if metric registration fails.
func NewBirdNETMetrics(registry *prometheus.Registry) (*BirdNETMetrics, error) {
	m := &BirdNETMetrics{registry: registry}
	if err := m.initMetrics(); err != nil {
		return nil, fmt.Errorf("failed to initialize BirdNET metrics: %w", err)
	}
	if err := registry.Register(m); err != nil {
		return nil, fmt.Errorf("failed to register BirdNET metrics: %w", err)
	}
	return m, nil
}

// initMetrics initializes all metrics for BirdNETMetrics.
func (m *BirdNETMetrics) initMetrics() error {
	// Original metrics
	m.DetectionCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "birdnet_detections",
			Help: "Total number of BirdNET detections partitioned by species name.",
		},
		[]string{"species"},
	)
	m.ProcessTimeGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "birdnet_processing_time_milliseconds",
			Help: "Most recent processing time for a BirdNET detection request in milliseconds.",
		},
	)

	// Performance histograms
	m.PredictionDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "birdnet_prediction_duration_seconds",
			Help:    "Time taken to perform a prediction",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 10), // 1ms to ~1s
		},
		[]string{"model"},
	)

	m.ChunkProcessDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "birdnet_chunk_process_duration_seconds",
			Help:    "Time taken to process an audio chunk",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 10),
		},
		[]string{"model"},
	)

	m.ModelInvokeDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "birdnet_model_invoke_duration_seconds",
			Help:    "Time taken for TensorFlow Lite model invocation",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 8), // 1ms to ~256ms
		},
		[]string{"model"},
	)

	m.RangeFilterDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "birdnet_range_filter_duration_seconds",
			Help:    "Time taken to apply range filter",
			Buckets: prometheus.ExponentialBuckets(0.0001, 2, 8), // 0.1ms to ~25.6ms
		},
		[]string{"model"},
	)

	// Operation counters
	m.PredictionTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "birdnet_predictions_total",
			Help: "Total number of prediction requests",
		},
		[]string{"model", "status"},
	)

	m.PredictionErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "birdnet_prediction_errors_total",
			Help: "Total number of prediction errors",
		},
		[]string{"model", "error_type"},
	)

	m.ModelLoadTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "birdnet_model_load_total",
			Help: "Total number of model load attempts",
		},
		[]string{"model", "status"},
	)

	m.ModelLoadErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "birdnet_model_load_errors_total",
			Help: "Total number of model load errors",
		},
		[]string{"model", "error_type"},
	)

	// State gauges
	m.ActiveProcessingGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "birdnet_active_processing",
			Help: "Number of currently active processing operations",
		},
	)

	m.ModelLoadedGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "birdnet_model_loaded",
			Help: "Whether the BirdNET model is currently loaded (1) or not (0)",
		},
	)

	return nil
}

// IncrementDetectionCounter increments the detection counter for a given species.
// It should be called each time BirdNET detects a species.
func (m *BirdNETMetrics) IncrementDetectionCounter(speciesName string) {
	m.DetectionCounter.WithLabelValues(speciesName).Inc()
}

// SetProcessTime sets the most recent processing time for a BirdNET detection request.
func (m *BirdNETMetrics) SetProcessTime(milliseconds float64) {
	m.ProcessTimeGauge.Set(milliseconds)
}

// RecordPrediction records metrics for a prediction operation
func (m *BirdNETMetrics) RecordPrediction(model string, durationSeconds float64, err error) {
	if err != nil {
		m.PredictionTotal.WithLabelValues(model, "error").Inc()
		m.PredictionErrors.WithLabelValues(model, categorizeError(err)).Inc()
	} else {
		m.PredictionTotal.WithLabelValues(model, "success").Inc()
		m.PredictionDuration.WithLabelValues(model).Observe(durationSeconds)
	}
}

// RecordChunkProcess records metrics for chunk processing
func (m *BirdNETMetrics) RecordChunkProcess(model string, durationSeconds float64) {
	m.ChunkProcessDuration.WithLabelValues(model).Observe(durationSeconds)
}

// RecordModelInvoke records metrics for model invocation
func (m *BirdNETMetrics) RecordModelInvoke(model string, durationSeconds float64) {
	m.ModelInvokeDuration.WithLabelValues(model).Observe(durationSeconds)
}

// RecordRangeFilter records metrics for range filter operations
func (m *BirdNETMetrics) RecordRangeFilter(model string, durationSeconds float64) {
	m.RangeFilterDuration.WithLabelValues(model).Observe(durationSeconds)
}

// RecordModelLoad records metrics for model loading operations
func (m *BirdNETMetrics) RecordModelLoad(model string, err error) {
	if err != nil {
		m.ModelLoadTotal.WithLabelValues(model, "error").Inc()
		m.ModelLoadErrors.WithLabelValues(model, categorizeError(err)).Inc()
		m.ModelLoadedGauge.Set(0)
	} else {
		m.ModelLoadTotal.WithLabelValues(model, "success").Inc()
		m.ModelLoadedGauge.Set(1)
	}
}

// SetActiveProcessing sets the number of active processing operations
func (m *BirdNETMetrics) SetActiveProcessing(count float64) {
	m.ActiveProcessingGauge.Set(count)
}

// categorizeError returns a category string for the error type using enhanced error categories
func categorizeError(err error) string {
	if err == nil {
		return "none"
	}

	// Check for enhanced errors with categories
	var enhancedErr *errors.EnhancedError
	if errors.As(err, &enhancedErr) {
		switch enhancedErr.GetCategory() {
		case string(errors.CategoryModelInit), string(errors.CategoryModelLoad):
			return "model_error"
		case string(errors.CategoryFileIO):
			return "file_error"
		case string(errors.CategoryAudio):
			return "audio_error"
		case string(errors.CategoryValidation):
			return "validation_error"
		case string(errors.CategorySystem):
			return "system_error"
		default:
			return enhancedErr.GetCategory()
		}
	}

	// Fallback to string matching for non-enhanced errors
	errStr := err.Error()
	switch {
	case strings.Contains(errStr, "tensor"):
		return "tensor_error"
	case strings.Contains(errStr, "invoke"):
		return "invoke_error"
	case strings.Contains(errStr, "file"):
		return "file_error"
	case strings.Contains(errStr, "model"):
		return "model_error"
	default:
		return "unknown"
	}
}

// Describe implements the prometheus.Collector interface.
func (m *BirdNETMetrics) Describe(ch chan<- *prometheus.Desc) {
	m.DetectionCounter.Describe(ch)
	ch <- m.ProcessTimeGauge.Desc()

	// Performance metrics
	m.PredictionDuration.Describe(ch)
	m.ChunkProcessDuration.Describe(ch)
	m.ModelInvokeDuration.Describe(ch)
	m.RangeFilterDuration.Describe(ch)

	// Operation counters
	m.PredictionTotal.Describe(ch)
	m.PredictionErrors.Describe(ch)
	m.ModelLoadTotal.Describe(ch)
	m.ModelLoadErrors.Describe(ch)

	// State gauges
	ch <- m.ActiveProcessingGauge.Desc()
	ch <- m.ModelLoadedGauge.Desc()
}

// Collect implements the prometheus.Collector interface.
func (m *BirdNETMetrics) Collect(ch chan<- prometheus.Metric) {
	m.DetectionCounter.Collect(ch)
	ch <- m.ProcessTimeGauge

	// Performance metrics
	m.PredictionDuration.Collect(ch)
	m.ChunkProcessDuration.Collect(ch)
	m.ModelInvokeDuration.Collect(ch)
	m.RangeFilterDuration.Collect(ch)

	// Operation counters
	m.PredictionTotal.Collect(ch)
	m.PredictionErrors.Collect(ch)
	m.ModelLoadTotal.Collect(ch)
	m.ModelLoadErrors.Collect(ch)

	// State gauges
	ch <- m.ActiveProcessingGauge
	ch <- m.ModelLoadedGauge
}

// RecordOperation implements the Recorder interface.
// It records operations related to BirdNET processing.
func (m *BirdNETMetrics) RecordOperation(operation, status string) {
	switch operation {
	case "prediction":
		m.PredictionTotal.WithLabelValues("birdnet", status).Inc()
	case "model_load":
		m.ModelLoadTotal.WithLabelValues("birdnet", status).Inc()
		if status == "success" {
			m.ModelLoadedGauge.Set(1)
		} else {
			m.ModelLoadedGauge.Set(0)
		}
	case "detection":
		// For detection, the status parameter is used as species name
		m.DetectionCounter.WithLabelValues(status).Inc()
	}
}

// RecordDuration implements the Recorder interface.
// It records duration metrics for various BirdNET operations.
func (m *BirdNETMetrics) RecordDuration(operation string, seconds float64) {
	switch operation {
	case "prediction":
		m.PredictionDuration.WithLabelValues("birdnet").Observe(seconds)
	case "chunk_process":
		m.ChunkProcessDuration.WithLabelValues("birdnet").Observe(seconds)
	case "model_invoke":
		m.ModelInvokeDuration.WithLabelValues("birdnet").Observe(seconds)
	case "range_filter":
		m.RangeFilterDuration.WithLabelValues("birdnet").Observe(seconds)
	case "process_time_ms":
		// Convert to milliseconds for backward compatibility
		m.ProcessTimeGauge.Set(seconds * 1000)
	}
}

// RecordError implements the Recorder interface.
// It records error metrics for BirdNET operations.
func (m *BirdNETMetrics) RecordError(operation, errorType string) {
	switch operation {
	case "prediction":
		m.PredictionErrors.WithLabelValues("birdnet", errorType).Inc()
	case "model_load":
		m.ModelLoadErrors.WithLabelValues("birdnet", errorType).Inc()
	}
}
