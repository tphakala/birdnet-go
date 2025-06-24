// Package birdnet - tracing and telemetry helpers
package birdnet

import (
	"context"
	"fmt"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TracingSpan represents a traced operation
type TracingSpan struct {
	operation   string
	description string
	startTime   time.Time
	tags        map[string]string
	data        map[string]interface{}
	sentrySpan  *sentry.Span
}

// StartSpan starts a new tracing span
func StartSpan(ctx context.Context, operation string, description string) (*TracingSpan, context.Context) {
	span := &TracingSpan{
		operation:   operation,
		description: description,
		startTime:   time.Now(),
		tags:        make(map[string]string),
		data:        make(map[string]interface{}),
	}

	// Only create Sentry span if telemetry is enabled
	settings := conf.GetSettings()
	if settings != nil && settings.Sentry.Enabled {
		sentrySpan := sentry.StartSpan(ctx, operation)
		sentrySpan.Description = description
		span.sentrySpan = sentrySpan
		ctx = sentrySpan.Context()
	}

	return span, ctx
}

// SetTag sets a tag on the span
func (s *TracingSpan) SetTag(key, value string) {
	if s == nil {
		return
	}
	
	s.tags[key] = value
	if s.sentrySpan != nil {
		s.sentrySpan.SetTag(key, value)
	}
}

// SetData sets arbitrary data on the span
func (s *TracingSpan) SetData(key string, value interface{}) {
	if s == nil {
		return
	}
	
	s.data[key] = value
	if s.sentrySpan != nil {
		s.sentrySpan.SetData(key, value)
	}
}

// Finish completes the span and records timing
func (s *TracingSpan) Finish() {
	if s == nil {
		return
	}
	
	duration := time.Since(s.startTime)
	s.SetData("duration_ms", duration.Milliseconds())
	
	if s.sentrySpan != nil {
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

// RecordMetric records a performance metric (for future metrics collection)
func RecordMetric(name string, value float64, tags map[string]string) {
	// For now, just log if debug is enabled
	settings := conf.GetSettings()
	if settings != nil && settings.Debug {
		fmt.Printf("[METRIC] %s: %.2f tags=%v\n", name, value, tags)
	}
	
	// Future: Send to metrics collection system
}

// RecordDuration records the duration of an operation
func RecordDuration(operation string, duration time.Duration) {
	RecordMetric(fmt.Sprintf("birdnet.%s.duration", operation), 
		float64(duration.Milliseconds()), 
		map[string]string{"unit": "ms"})
}