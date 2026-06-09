// Package classifier - tracing and telemetry helpers
package classifier

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/classifier/inferencestats"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// tagKeyModel is the tracing tag key used to identify the model in spans.
const tagKeyModel = "model"

// Tracing tag key and values that record the error outcome of a span.
// Finish() consults these via the errored flag set in SetTag to avoid recording
// a spurious success metric on error paths, so the producers that call SetTag and
// the consumer in Finish must agree on these exact strings.
const (
	tagKeyError   = "error"
	tagValueTrue  = "true"
	tagValueFalse = "false"
)

// opPredict is the span operation name for a model prediction. It is the only
// operation currently instrumented via StartSpan; the constant keeps the
// producers (BirdNET/Perch/Bat Predict) and the Finish consumer in sync.
const opPredict = "birdnet.predict"

// opPredictEmbeddings is the span operation name for a prediction that also
// extracts an embedding (PredictWithEmbeddings). It records the same prediction
// metric as opPredict in Finish, but is tracked under a distinct Sentry
// operation so embedding-extraction latency stays visible separately from plain
// predictions.
const opPredictEmbeddings = "birdnet.predict_embeddings"

// Span data keys recorded on prediction spans. Centralised so the telemetry
// schema is documented in one place and stays consistent across models.
const (
	dataKeyErrorType        = "error_type"
	dataKeySampleCount      = "sample_count"
	dataKeySampleSize       = "sample_size"
	dataKeyResultCount      = "result_count"
	dataKeyTotalDurationMs  = "total_duration_ms"
	dataKeyInvokeDurationMs = "invoke_duration_ms"
	dataKeyDurationMs       = "duration_ms"
)

// error_type values recorded on failed predictions. Shared by every
// ModelInstance.Predict implementation so the telemetry vocabulary stays
// consistent across models.
const (
	errTypeEmptySample         = "empty_sample"
	errTypeClassifierNil       = "classifier_nil"
	errTypeInvokeFailed        = "invoke_failed"
	errTypeLabelMismatch       = "label_mismatch"
	errTypeEmbeddingExtraction = "embedding_extraction"
	errTypeNilEmbeddings       = "nil_embeddings"
	errTypeBatClassification   = "bat_classification"
)

// defaultTopKResults is the number of top predictions every model returns.
const defaultTopKResults = 10

// TracingSpan represents a traced operation with minimal overhead.
// A TracingSpan must not be used from multiple goroutines concurrently.
type TracingSpan struct {
	operation      string
	description    string
	startTime      time.Time
	tags           map[string]string // Only allocated if needed
	data           map[string]any    // Only allocated if needed
	sentrySpan     *sentry.Span
	metricsEnabled bool
	model          string // For metrics labeling
	errored        bool   // True once an error tag is set; gates the success metric in Finish
	finished       bool   // True once Finish has run; makes Finish idempotent
}

// Global metrics instance (set by observability package)
var (
	globalMetrics    atomic.Pointer[metrics.BirdNETMetrics]
	metricsOnce      sync.Once
	activeOperations int64
)

// globalInferenceCounters tracks per-model inference timing via lock-free atomics.
var globalInferenceCounters = &inferencestats.CounterMap{}

// GetInferenceCounters returns the shared per-model inference counters for collector wiring.
func GetInferenceCounters() *inferencestats.CounterMap {
	return globalInferenceCounters
}

// SetMetrics sets the global metrics instance for tracing.
// This function is thread-safe and ensures metrics are only set once per process lifetime.
// Subsequent calls to this function will be ignored (idempotent behavior).
// This design prevents race conditions during initialization while ensuring
// metrics configuration remains consistent throughout the application lifecycle.
func SetMetrics(m *metrics.BirdNETMetrics) {
	metricsOnce.Do(func() {
		globalMetrics.Store(m)
	})
}

// getMetrics returns the current metrics instance in a thread-safe manner.
func getMetrics() *metrics.BirdNETMetrics {
	return globalMetrics.Load()
}

// StartSpan starts a new tracing span with minimal overhead
func StartSpan(ctx context.Context, operation, description string) (*TracingSpan, context.Context) {
	span := &TracingSpan{
		operation:      operation,
		description:    description,
		startTime:      time.Now(),
		metricsEnabled: getMetrics() != nil,
	}

	// Only create Sentry span if telemetry is enabled
	settings := conf.GetSettings()
	if settings != nil && settings.Sentry.Enabled {
		sentrySpan := sentry.StartSpan(ctx, operation)
		sentrySpan.Description = description
		span.sentrySpan = sentrySpan
		ctx = sentrySpan.Context()
	}

	// Track active operations for metrics
	if span.metricsEnabled {
		count := atomic.AddInt64(&activeOperations, 1)
		if m := getMetrics(); m != nil {
			m.SetActiveProcessing(float64(count))
		}
	}

	return span, ctx
}

// SetTag sets a tag on the span (lazy allocation)
func (s *TracingSpan) SetTag(key, value string) {
	if s == nil {
		return
	}

	// Special handling for model tag
	if key == tagKeyModel {
		s.model = value
	}

	// Track the error outcome so Finish does not record a spurious success metric
	// on error paths. Callers record the error outcome explicitly, so once an error
	// tag is set the flag stays set for the lifetime of the span.
	if key == tagKeyError && value == tagValueTrue {
		s.errored = true
	}

	// Only allocate tags map if Sentry is enabled
	if s.sentrySpan != nil {
		if s.tags == nil {
			s.tags = make(map[string]string)
		}
		s.tags[key] = value
		s.sentrySpan.SetTag(key, value)
	}
}

// SetData sets arbitrary data on the span (lazy allocation)
func (s *TracingSpan) SetData(key string, value any) {
	if s == nil {
		return
	}

	// Only allocate data map if Sentry is enabled
	if s.sentrySpan != nil {
		if s.data == nil {
			s.data = make(map[string]any)
		}
		s.data[key] = value
		s.sentrySpan.SetData(key, value)
	}
}

// Finish completes the span and records timing
func (s *TracingSpan) Finish() {
	if s == nil || s.finished {
		return
	}
	// Make Finish idempotent: a span represents one operation, so a second call
	// (e.g. a manual Finish plus a deferred one) must not decrement
	// activeOperations again or record the prediction twice.
	s.finished = true

	duration := time.Since(s.startTime)
	durationSeconds := duration.Seconds()

	// Record metrics if enabled
	if s.metricsEnabled {
		// Balance the activeOperations increment from StartSpan. Decrement under
		// the same metricsEnabled guard as the increment so the counter stays
		// balanced even if the metrics instance is reset between StartSpan and
		// Finish.
		count := atomic.AddInt64(&activeOperations, -1)

		if m := getMetrics(); m != nil {
			model := s.model
			if model == "" {
				model = "unknown"
			}

			// Only predict spans are recorded here. Skip the success record on
			// error spans: callers record the error outcome explicitly, so
			// recording here would either double-count (when the caller already
			// recorded an error) or log a spurious success (on early-guard error
			// paths). Plain predictions and embedding predictions record the same
			// prediction metric; they differ only in the Sentry operation name.
			if (s.operation == opPredict || s.operation == opPredictEmbeddings) && !s.errored {
				m.RecordPrediction(model, durationSeconds, nil)
			}

			m.SetActiveProcessing(float64(count))
		}
	}

	// Record in Sentry if enabled
	if s.sentrySpan != nil {
		s.SetData(dataKeyDurationMs, duration.Milliseconds())
		s.sentrySpan.Finish()
	}
}

// startPredictSpan opens a birdnet.predict span and records the standard
// prediction preamble (model tag and sample dimensions). It is the shared entry
// point for every ModelInstance.Predict implementation so the operation name,
// description, and data keys stay consistent across models.
func startPredictSpan(ctx context.Context, model string, samples [][]float32) (*TracingSpan, context.Context) {
	return startPredictSpanOp(ctx, opPredict, "Species prediction", model, samples)
}

// startPredictEmbeddingsSpan opens a birdnet.predict_embeddings span with the
// same prediction preamble as startPredictSpan. PredictWithEmbeddings
// implementations use it so embedding predictions share the success metric and
// data schema with plain predictions while keeping a distinct Sentry operation.
func startPredictEmbeddingsSpan(ctx context.Context, model string, samples [][]float32) (*TracingSpan, context.Context) {
	return startPredictSpanOp(ctx, opPredictEmbeddings, "Species prediction with embeddings", model, samples)
}

// startPredictSpanOp opens a prediction span for the given operation and records
// the standard preamble (model tag and sample dimensions). It backs both
// startPredictSpan and startPredictEmbeddingsSpan so the preamble stays
// consistent regardless of operation name.
func startPredictSpanOp(ctx context.Context, operation, description, model string, samples [][]float32) (*TracingSpan, context.Context) {
	span, ctx := StartSpan(ctx, operation, description)
	span.SetTag(tagKeyModel, model)
	span.SetData(dataKeySampleCount, len(samples))
	if len(samples) > 0 {
		span.SetData(dataKeySampleSize, len(samples[0]))
	}
	return span, ctx
}

// markErrored tags the span as failed with the given error_type. SetTag latches
// the errored flag so Finish skips the success metric. Pre-inference guard
// rejections (empty sample, uninitialised classifier) use this directly because
// they are not counted as predictions; failures after inference begins use
// recordPredictionFailure, which also records the error outcome.
func (s *TracingSpan) markErrored(errorType string) {
	s.SetTag(tagKeyError, tagValueTrue)
	s.SetData(dataKeyErrorType, errorType)
}

// recordPredictionFailure marks the span errored and records the failed
// prediction exactly once for the given model. Finish then skips its success
// record because the span is errored.
func recordPredictionFailure(span *TracingSpan, model, errorType string, start time.Time, err error) {
	span.markErrored(errorType)
	if m := getMetrics(); m != nil {
		m.RecordPrediction(model, time.Since(start).Seconds(), err)
	}
}

// recordPredictionSuccess records span data for a successful prediction and tags
// it not-errored. The single success metric is recorded by Finish.
func recordPredictionSuccess(span *TracingSpan, resultCount int, start time.Time) {
	span.SetData(dataKeyResultCount, resultCount)
	span.SetData(dataKeyTotalDurationMs, time.Since(start).Milliseconds())
	span.SetTag(tagKeyError, tagValueFalse)
}

// RecordMetric records a performance metric
func RecordMetric(name string, value float64, tags map[string]string) {
	// Log if debug is enabled
	settings := conf.GetSettings()
	if settings != nil && settings.Debug {
		GetLogger().Debug("Metric recorded",
			logger.String("metric_name", name),
			logger.Float64("value", value))
	}

	// Note: Detailed metrics are now recorded via spans automatically
}
