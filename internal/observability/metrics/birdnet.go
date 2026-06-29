// Package metrics provides custom Prometheus metrics for the BirdNET-Go application.
package metrics

import (
	"fmt"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	metricNameInferenceRTF         = "birdnet_inference_rtf"
	metricNameModelRSSBytes        = "birdnet_model_rss_bytes"
	metricNameAudioQueueDepth      = "birdnet_audio_queue_depth"
	metricNameAudioDroppedChunks   = "birdnet_audio_dropped_chunks"
	labelModel                     = "model"
	labelSource                    = "source"
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

	// Inference status gauges (AI Models page).
	InferenceRTF  *prometheus.GaugeVec
	ModelRSSBytes *prometheus.GaugeVec

	// Audio pipeline gauges (per source).
	AudioQueueDepth     *prometheus.GaugeVec
	AudioDroppedChunks  *prometheus.GaugeVec

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
			Buckets: prometheus.ExponentialBuckets(BucketStart1ms, BucketFactor2, BucketCount10), // 1ms to ~1s
		},
		[]string{"model"},
	)

	m.ChunkProcessDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "birdnet_chunk_process_duration_seconds",
			Help:    "Time taken to process an audio chunk",
			Buckets: prometheus.ExponentialBuckets(BucketStart1ms, BucketFactor2, BucketCount10),
		},
		[]string{"model"},
	)

	m.ModelInvokeDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "birdnet_model_invoke_duration_seconds",
			Help:    "Time taken for model invocation",
			Buckets: prometheus.ExponentialBuckets(BucketStart1ms, BucketFactor2, BucketCount8), // 1ms to ~256ms
		},
		[]string{"model"},
	)

	m.RangeFilterDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "birdnet_range_filter_duration_seconds",
			Help:    "Time taken to apply range filter",
			Buckets: prometheus.ExponentialBuckets(BucketStart100us, BucketFactor2, BucketCount8), // 0.1ms to ~25.6ms
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

	m.InferenceRTF = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricNameInferenceRTF,
			Help: "Real-time factor per model (inference time divided by audio clip duration). Lower is faster.",
		},
		[]string{labelModel},
	)
	m.ModelRSSBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricNameModelRSSBytes,
			Help: "Approximate host resident set size in bytes attributed to a loaded model at load time.",
		},
		[]string{labelModel},
	)

	m.AudioQueueDepth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricNameAudioQueueDepth,
			Help: "Current audio pipeline queue depth (max route inbox occupancy) per source.",
		},
		[]string{labelSource},
	)
	m.AudioDroppedChunks = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricNameAudioDroppedChunks,
			Help: "Running total of audio frames dropped across the source's routes (gauge snapshot, not a counter).",
		},
		[]string{labelSource},
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

// SetInferenceRTF sets the real-time factor gauge for a model. Nil-safe.
func (m *BirdNETMetrics) SetInferenceRTF(model string, rtf float64) {
	if m == nil || m.InferenceRTF == nil {
		return
	}
	m.InferenceRTF.WithLabelValues(model).Set(rtf)
}

// SetModelRSSBytes sets the approximate per-model host RSS gauge. Nil-safe.
func (m *BirdNETMetrics) SetModelRSSBytes(model string, bytes int64) {
	if m == nil || m.ModelRSSBytes == nil {
		return
	}
	m.ModelRSSBytes.WithLabelValues(model).Set(float64(bytes))
}

// SetAudioQueueDepth sets the instantaneous audio queue depth gauge for a source. Nil-safe.
func (m *BirdNETMetrics) SetAudioQueueDepth(source string, depth float64) {
	if m == nil || m.AudioQueueDepth == nil {
		return
	}
	m.AudioQueueDepth.WithLabelValues(source).Set(depth)
}

// SetAudioDroppedChunks sets the cumulative dropped-audio-chunks gauge for a source. Nil-safe.
func (m *BirdNETMetrics) SetAudioDroppedChunks(source string, total float64) {
	if m == nil || m.AudioDroppedChunks == nil {
		return
	}
	m.AudioDroppedChunks.WithLabelValues(source).Set(total)
}

// DeleteInferenceMetrics removes a model's inference gauge label values, e.g.
// after the model is unloaded, so Prometheus stops reporting stale series. Nil-safe.
func (m *BirdNETMetrics) DeleteInferenceMetrics(model string) {
	if m == nil {
		return
	}
	if m.InferenceRTF != nil {
		m.InferenceRTF.DeleteLabelValues(model)
	}
	if m.ModelRSSBytes != nil {
		m.ModelRSSBytes.DeleteLabelValues(model)
	}
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
	m.ProcessTimeGauge.Describe(ch)

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
	m.ActiveProcessingGauge.Describe(ch)
	m.ModelLoadedGauge.Describe(ch)

	// Inference status gauges
	m.InferenceRTF.Describe(ch)
	m.ModelRSSBytes.Describe(ch)

	// Audio pipeline gauges
	m.AudioQueueDepth.Describe(ch)
	m.AudioDroppedChunks.Describe(ch)
}

// Collect implements the prometheus.Collector interface.
func (m *BirdNETMetrics) Collect(ch chan<- prometheus.Metric) {
	m.DetectionCounter.Collect(ch)
	m.ProcessTimeGauge.Collect(ch)

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
	m.ActiveProcessingGauge.Collect(ch)
	m.ModelLoadedGauge.Collect(ch)

	// Inference status gauges
	m.InferenceRTF.Collect(ch)
	m.ModelRSSBytes.Collect(ch)

	// Audio pipeline gauges
	m.AudioQueueDepth.Collect(ch)
	m.AudioDroppedChunks.Collect(ch)
}

// RecordOperation implements the Recorder interface.
// It records operations related to BirdNET processing.
// Supported operations: "prediction", "model_load", "detection"
// Status values: "success", "error", or species name for "detection"
func (m *BirdNETMetrics) RecordOperation(operation, status string) {
	switch operation {
	case OpPrediction:
		m.PredictionTotal.WithLabelValues(LabelBirdnet, status).Inc()
	case OpModelLoad:
		m.ModelLoadTotal.WithLabelValues(LabelBirdnet, status).Inc()
		if status == "success" {
			m.ModelLoadedGauge.Set(1)
		} else {
			m.ModelLoadedGauge.Set(0)
		}
	case OpDetection:
		// IMPORTANT: For the "detection" operation, the status parameter represents
		// the detected species name (e.g., "Turdus migratorius" for American Robin),
		// not a success/error status. This is a special case where we reuse the
		// status parameter for semantic convenience in the Recorder interface.
		m.DetectionCounter.WithLabelValues(status).Inc()
	}
}

// RecordDuration implements the Recorder interface.
// It records duration metrics for various BirdNET operations.
// Supported operations: "prediction", "chunk_process", "model_invoke", "range_filter", "process_time_ms"
func (m *BirdNETMetrics) RecordDuration(operation string, seconds float64) {
	switch operation {
	case OpPrediction:
		m.PredictionDuration.WithLabelValues(LabelBirdnet).Observe(seconds)
	case OpChunkProcess:
		m.ChunkProcessDuration.WithLabelValues(LabelBirdnet).Observe(seconds)
	case OpModelInvoke:
		m.ModelInvokeDuration.WithLabelValues(LabelBirdnet).Observe(seconds)
	case OpRangeFilter:
		m.RangeFilterDuration.WithLabelValues(LabelBirdnet).Observe(seconds)
	case OpProcessTimeMs:
		// Convert to milliseconds for backward compatibility
		m.ProcessTimeGauge.Set(seconds * MillisecondsPerSecond)
	}
}

// RecordError implements the Recorder interface.
// It records error metrics for BirdNET operations.
// Supported operations: "prediction", "model_load"
// Error types: "validation", "model_error", "tensor_error", "invoke_error", etc.
func (m *BirdNETMetrics) RecordError(operation, errorType string) {
	switch operation {
	case OpPrediction:
		m.PredictionErrors.WithLabelValues(LabelBirdnet, errorType).Inc()
	case OpModelLoad:
		m.ModelLoadErrors.WithLabelValues(LabelBirdnet, errorType).Inc()
	}
}
