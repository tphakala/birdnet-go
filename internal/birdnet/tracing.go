// Package birdnet - tracing and telemetry helpers
package birdnet

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// TracingSpan represents a traced operation with minimal overhead
type TracingSpan struct {
	operation      string
	description    string
	startTime      time.Time
	tags           map[string]string      // Only allocated if needed
	data           map[string]interface{} // Only allocated if needed
	sentrySpan     *sentry.Span
	metricsEnabled bool
	model          string // For metrics labeling
}

// Global metrics instance (set by observability package)
var globalMetrics *metrics.BirdNETMetrics
var activeOperations int64

// SetMetrics sets the global metrics instance for tracing
func SetMetrics(m *metrics.BirdNETMetrics) {
	globalMetrics = m
}

// StartSpan starts a new tracing span with minimal overhead
func StartSpan(ctx context.Context, operation, description string) (*TracingSpan, context.Context) {
	span := &TracingSpan{
		operation:      operation,
		description:    description,
		startTime:      time.Now(),
		metricsEnabled: globalMetrics != nil,
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
		globalMetrics.SetActiveProcessing(float64(count))
	}

	return span, ctx
}

// SetTag sets a tag on the span (lazy allocation)
func (s *TracingSpan) SetTag(key, value string) {
	if s == nil {
		return
	}

	// Special handling for model tag
	if key == "model" {
		s.model = value
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
func (s *TracingSpan) SetData(key string, value interface{}) {
	if s == nil {
		return
	}

	// Only allocate data map if Sentry is enabled
	if s.sentrySpan != nil {
		if s.data == nil {
			s.data = make(map[string]interface{})
		}
		s.data[key] = value
		s.sentrySpan.SetData(key, value)
	}
}

// Finish completes the span and records timing
func (s *TracingSpan) Finish() {
	if s == nil {
		return
	}

	duration := time.Since(s.startTime)
	durationSeconds := duration.Seconds()

	// Record metrics if enabled
	if s.metricsEnabled {
		model := s.model
		if model == "" {
			model = "unknown"
		}

		// Record appropriate metric based on operation
		switch s.operation {
		case "birdnet.predict":
			globalMetrics.RecordPrediction(model, durationSeconds, nil)
		case "birdnet.process_chunk":
			globalMetrics.RecordChunkProcess(model, durationSeconds)
		case "birdnet.model_invoke":
			globalMetrics.RecordModelInvoke(model, durationSeconds)
		case "birdnet.range_filter":
			globalMetrics.RecordRangeFilter(model, durationSeconds)
		}

		// Update active operations count
		count := atomic.AddInt64(&activeOperations, -1)
		globalMetrics.SetActiveProcessing(float64(count))
	}

	// Record in Sentry if enabled
	if s.sentrySpan != nil {
		s.SetData("duration_ms", duration.Milliseconds())
		s.sentrySpan.Finish()
	}
}

// TraceAnalysis traces audio analysis operations
func TraceAnalysis(ctx context.Context, operation string, fn func() error) error {
	span, _ := StartSpan(ctx, fmt.Sprintf("birdnet.%s", operation), operation)
	defer span.Finish()

	err := fn()
	if err != nil {
		span.SetTag("error", "true")
		span.SetData("error_message", err.Error())
	}

	return err
}

// TracePrediction traces prediction operations with additional metrics
func TracePrediction(ctx context.Context, sampleSize int, fn func() (interface{}, error)) (interface{}, error) {
	span, _ := StartSpan(ctx, "birdnet.predict", "Audio prediction")
	defer span.Finish()

	span.SetData("sample_size", sampleSize)

	start := time.Now()
	result, err := fn()
	duration := time.Since(start)

	span.SetData("prediction_duration_ms", duration.Milliseconds())

	if err != nil {
		span.SetTag("error", "true")
		span.SetData("error_message", err.Error())
	} else {
		span.SetTag("error", "false")
		// Add result metrics if available
		if results, ok := result.([]interface{}); ok {
			span.SetData("result_count", len(results))
		}
	}

	return result, err
}

// RecordMetric records a performance metric
func RecordMetric(name string, value float64, tags map[string]string) {
	// Log if debug is enabled
	settings := conf.GetSettings()
	if settings != nil && settings.Debug {
		fmt.Printf("[METRIC] %s: %.2f tags=%v\n", name, value, tags)
	}

	// Note: Detailed metrics are now recorded via spans automatically
}
