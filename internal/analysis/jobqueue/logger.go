// Package jobqueue provides structured logging for the jobqueue package
package jobqueue

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"path/filepath"
	"time"
	
	"github.com/tphakala/birdnet-go/internal/logging"
)

// Service name constant to reduce duplication and improve maintainability
const serviceName = "analysis.jobqueue"

// Package-level logger for job queue operations
var (
	logger       *slog.Logger
	levelVar     = new(slog.LevelVar) // Dynamic level control
	closeLogger  func() error
)

func init() {
	var err error
	// Define log file path for job queue operations
	logFilePath := filepath.Join("logs", "analysis-jobqueue.log")
	initialLevel := slog.LevelInfo // Default to Info level
	levelVar.Set(initialLevel)
	
	// Initialize the jobqueue-specific file logger
	logger, closeLogger, err = logging.NewFileLogger(logFilePath, serviceName, levelVar)
	if err != nil {
		// Fallback: Log error to standard log and use console logging
		log.Printf("Failed to initialize jobqueue file logger at %s: %v. Using console logging.", logFilePath, err)
		// Set logger to a disabled handler to prevent nil panics, but respects level var
		fbHandler := slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: levelVar})
		logger = slog.New(fbHandler).With("service", serviceName)
		closeLogger = func() error { return nil } // No-op closer
	}
}

// GetLogger returns the jobqueue package logger
// Useful for external packages that need access to jobqueue logging
func GetLogger() *slog.Logger {
	if logger == nil {
		// Double-check initialization in case of race conditions
		logger = slog.Default().With("service", serviceName)
	}
	return logger
}

// CloseLogger closes the log file and releases resources
func CloseLogger() error {
	if closeLogger != nil {
		return closeLogger()
	}
	return nil
}

// LogJobEnqueued logs when a job is added to the queue
func LogJobEnqueued(ctx context.Context, jobID, actionType string, retryable bool) {
	args := []any{
		"job_id", jobID,
		"action_type", actionType,
		"retryable", retryable,
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		args = append(args, "trace_id", traceID)
	}
	logger.InfoContext(ctx, "Job enqueued", args...)
}

// LogJobStarted logs when a job begins execution
func LogJobStarted(ctx context.Context, jobID, actionType string) {
	args := []any{
		"job_id", jobID,
		"action_type", actionType,
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		args = append(args, "trace_id", traceID)
	}
	logger.InfoContext(ctx, "Job started", args...)
}

// LogJobCompleted logs when a job finishes successfully
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
func LogJobFailed(ctx context.Context, jobID, actionType string, attempt, maxRetries int, err error) {
	args := []any{
		"job_id", jobID,
		"action_type", actionType,
		"attempt", attempt,
		"max_retries", maxRetries,
		"error", err,
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		args = append(args, "trace_id", traceID)
	}

	// Use Error level for final failure, Warn for retryable failures
	if attempt >= maxRetries {
		logger.ErrorContext(ctx, "Job failed permanently", args...)
	} else {
		logger.WarnContext(ctx, "Job failed, will retry", args...)
	}
}

// LogQueueStats logs queue statistics
func LogQueueStats(ctx context.Context, pending, running, completed, failed int) {
	args := []any{
		"pending", pending,
		"running", running,
		"completed", completed,
		"failed", failed,
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		args = append(args, "trace_id", traceID)
	}
	logger.InfoContext(ctx, "Queue statistics", args...)
}

// LogJobDropped logs when a job is dropped due to queue being full
func LogJobDropped(ctx context.Context, jobID, actionDesc string) {
	args := []any{
		"job_id", jobID,
		"action_description", actionDesc,
		"reason", "queue_full",
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		args = append(args, "trace_id", traceID)
	}
	logger.WarnContext(ctx, "Job dropped", args...)
}

// LogQueueStopped logs when the job queue processing is stopped
func LogQueueStopped(ctx context.Context, reason string, details ...any) {
	args := []any{
		"reason", reason,
	}
	if len(details) > 0 && len(details)%2 == 0 {
		args = append(args, details...)
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		args = append(args, "trace_id", traceID)
	}
	logger.InfoContext(ctx, "Queue processing stopped", args...)
}

// LogJobRetrying logs when a job is being retried
func LogJobRetrying(ctx context.Context, jobID, actionDesc string, attempt, maxAttempts int) {
	args := []any{
		"job_id", jobID,
		"action_description", actionDesc,
		"attempt", attempt,
		"max_attempts", maxAttempts,
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		args = append(args, "trace_id", traceID)
	}
	logger.InfoContext(ctx, "Job retrying", args...)
}

// LogJobSuccess logs when a job completes successfully
func LogJobSuccess(ctx context.Context, jobID, actionDesc string, attempt int) {
	args := []any{
		"job_id", jobID,
		"action_description", actionDesc,
		"attempt", attempt,
		"first_attempt", attempt == 1,
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		args = append(args, "trace_id", traceID)
	}
	logger.InfoContext(ctx, "Job succeeded", args...)
}

// Context key types for safe context value retrieval
type contextKey string

const (
	contextKeyTraceID contextKey = "trace_id"
)

// extractTraceID attempts to extract a trace ID from the context
// This supports both string and fmt.Stringer types stored in context
func extractTraceID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	
	if traceID := ctx.Value(contextKeyTraceID); traceID != nil {
		switch v := traceID.(type) {
		case string:
			return v
		case fmt.Stringer:
			return v.String()
		}
	}
	return ""
}