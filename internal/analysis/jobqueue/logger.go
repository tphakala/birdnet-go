// Package jobqueue provides structured logging for the jobqueue package
package jobqueue

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
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
	once         sync.Once            // Thread-safe initialization
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
		// Set logger to console handler for actual console output
		fbHandler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: levelVar})
		logger = slog.New(fbHandler).With("service", serviceName, "component", "jobqueue")
		closeLogger = func() error { return nil } // No-op closer
	} else {
		// Add component field to the successfully initialized file logger
		logger = logger.With("component", "jobqueue")
	}
}

// GetLogger returns the jobqueue package logger
// Useful for external packages that need access to jobqueue logging
func GetLogger() *slog.Logger {
	once.Do(func() {
		if logger == nil {
			// Initialize logger with default if not already done
			logger = slog.Default().With("service", serviceName, "component", "jobqueue")
		}
	})
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
func LogJobFailed(ctx context.Context, jobID, actionType string, attempt, maxAttempts int, err error) {
	args := []any{
		"job_id", jobID,
		"action_type", actionType,
		"attempt", attempt,
		"max_attempts", maxAttempts,
		"error", err,
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		args = append(args, "trace_id", traceID)
	}

	// Use Error level for final failure, Warn for retryable failures
	if attempt >= maxAttempts {
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
		"action_type", actionDesc,
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
	if len(details) > 0 {
		if len(details)%2 != 0 {
			// Append marker for odd length to prevent silent data loss
			details = append(details, "missing_value")
		}
		args = append(args, details...)
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		args = append(args, "trace_id", traceID)
	}
	logger.InfoContext(ctx, "Queue processing stopped", args...)
}

// LogJobRetrying logs when a job is being retried (at execution start)
func LogJobRetrying(ctx context.Context, jobID, actionDesc string, attempt, maxAttempts int) {
	remainingAttempts := maxAttempts - attempt
	args := []any{
		"job_id", jobID,
		"action_type", actionDesc,
		"attempt", attempt,
		"max_attempts", maxAttempts,
		"remaining_attempts", remainingAttempts,
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		args = append(args, "trace_id", traceID)
	}
	logger.InfoContext(ctx, "Job retry execution starting", args...)
}

// LogJobRetryScheduled logs when a job is scheduled for retry after failure
func LogJobRetryScheduled(ctx context.Context, jobID, actionDesc string, attempt, maxAttempts int, delay time.Duration, nextRetryAt time.Time, lastErr error) {
	remainingAttempts := maxAttempts - attempt
	args := []any{
		"job_id", jobID,
		"action_type", actionDesc,
		"attempt", attempt,
		"max_attempts", maxAttempts,
		"remaining_attempts", remainingAttempts,
		"retry_delay_ms", delay.Milliseconds(),
		"next_retry_at", nextRetryAt,
		"error", lastErr,
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		args = append(args, "trace_id", traceID)
	}
	logger.WarnContext(ctx, "Job scheduled for retry after failure", args...)
}

// LogJobSuccess logs when a job completes successfully
func LogJobSuccess(ctx context.Context, jobID, actionDesc string, attempt int) {
	args := []any{
		"job_id", jobID,
		"action_type", actionDesc,
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
	// contextKeyTraceID is used to store trace IDs in context for distributed tracing
	contextKeyTraceID contextKey = "trace_id"
)

// WithTraceID adds a trace ID to the context using the standardized key.
// This helper ensures consistent trace ID storage and retrieval across the jobqueue package.
//
// Example usage:
//   ctx = WithTraceID(ctx, "trace-12345")
//   LogJobStarted(ctx, jobID, actionType)
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, contextKeyTraceID, traceID)
}

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