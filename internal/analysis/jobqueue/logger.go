// Package jobqueue provides structured logging for the jobqueue package
package jobqueue

import (
	"context"
	"log/slog"
	"time"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// Service name constant to reduce duplication and improve maintainability
const serviceName = "analysis.jobqueue"

// Package-level logger for job queue operations
var logger *slog.Logger

func init() {
	// Create service-specific logger for analysis job queue
	// This provides dedicated logging for job queue operations
	logger = logging.ForService(serviceName)
	
	// Defensive initialization for early startup scenarios
	// This ensures we always have a working logger even if
	// the logging system isn't fully initialized yet
	if logger == nil {
		logger = slog.Default().With("service", serviceName)
	}
}

// GetLogger returns the jobqueue package logger
// Useful for external packages that need access to jobqueue logging
func GetLogger() *slog.Logger {
	if logger == nil {
		// Double-check initialization in case of race conditions
		logger = logging.ForService(serviceName)
		if logger == nil {
			logger = slog.Default().With("service", serviceName)
		}
	}
	return logger
}

// LogJobEnqueued logs when a job is successfully enqueued
func LogJobEnqueued(ctx context.Context, jobID, actionType string, priority int) {
	args := []any{
		"job_id", jobID,
		"action_type", actionType,
		"priority", priority,
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		args = append(args, "trace_id", traceID)
	}
	logger.DebugContext(ctx, "Job enqueued", args...)
}

// LogJobStarted logs when a job starts processing
func LogJobStarted(ctx context.Context, jobID, actionType string, attempt int) {
	args := []any{
		"job_id", jobID,
		"action_type", actionType,
		"attempt", attempt,
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		args = append(args, "trace_id", traceID)
	}
	logger.DebugContext(ctx, "Job started", args...)
}

// LogJobCompleted logs when a job completes successfully
func LogJobCompleted(ctx context.Context, jobID, actionType string, duration time.Duration) {
	args := []any{
		"job_id", jobID,
		"action_type", actionType,
		"duration_ms", duration.Milliseconds(),
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		args = append(args, "trace_id", traceID)
	}
	logger.InfoContext(ctx, "Job completed", args...)
}

// LogJobFailed logs when a job fails
// Uses Warn level for retryable failures, Error level for final failures
func LogJobFailed(ctx context.Context, jobID, actionType string, attempt, maxRetries int, err error) {
	args := []any{
		"job_id", jobID,
		"action_type", actionType,
		"attempt", attempt,
		"max_retries", maxRetries,
		"error", err,
		"will_retry", attempt < maxRetries,
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		args = append(args, "trace_id", traceID)
	}
	
	// Use Error level for final failure, Warn for retryable failures
	if attempt >= maxRetries {
		logger.ErrorContext(ctx, "Job failed permanently", args...)
	} else {
		logger.WarnContext(ctx, "Job failed", args...)
	}
}

// LogQueueStats logs periodic queue statistics
func LogQueueStats(ctx context.Context, pending, running, completed, failed int) {
	args := []any{
		"pending", pending,
		"running", running,
		"completed", completed,
		"failed", failed,
		"total", pending + running + completed + failed,
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		args = append(args, "trace_id", traceID)
	}
	logger.InfoContext(ctx, "Queue statistics", args...)
}

// Context key types for safe context value retrieval
type contextKey string

const (
	// traceIDKey is the typed context key for trace IDs
	traceIDKey contextKey = "jobqueue.trace_id"
)

// WithTraceID adds a trace ID to the context using the typed key
// External systems should use this function to normalize trace IDs at ingress points
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

// extractTraceID attempts to extract a trace ID from the context using typed keys
// External systems should normalize their trace IDs to this typed key at ingress points
func extractTraceID(ctx context.Context) string {
	if traceID := ctx.Value(traceIDKey); traceID != nil {
		if id, ok := traceID.(string); ok {
			return id
		}
	}
	return ""
}