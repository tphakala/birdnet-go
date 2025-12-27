// Package jobqueue provides structured logging for the jobqueue package
package jobqueue

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// Package-level logger for job queue operations
var log logger.Logger

// slogLogger is an slog.Logger used for test compatibility.
// Tests can swap this out with a custom logger to capture output.
// nolint:gochecknoglobals // Required for test compatibility
var slogLogger = slog.New(slog.NewJSONHandler(os.Stdout, nil))

func init() {
	log = logger.Global().Module("birdnet")
}

// GetLogger returns the slog logger for test compatibility
func GetLogger() *slog.Logger {
	return slogLogger
}

// LogJobEnqueued logs when a job is added to the queue
func LogJobEnqueued(ctx context.Context, jobID, actionType string, retryable bool) {
	fields := []logger.Field{
		logger.String("job_id", jobID),
		logger.String("action_type", actionType),
		logger.Bool("retryable", retryable),
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		fields = append(fields, logger.String("trace_id", traceID))
	}
	log.WithContext(ctx).Info("Job enqueued", fields...)
}

// LogJobStarted logs when a job begins execution
func LogJobStarted(ctx context.Context, jobID, actionType string) {
	fields := []logger.Field{
		logger.String("job_id", jobID),
		logger.String("action_type", actionType),
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		fields = append(fields, logger.String("trace_id", traceID))
	}
	log.WithContext(ctx).Info("Job started", fields...)
}

// LogJobCompleted logs when a job finishes successfully
func LogJobCompleted(ctx context.Context, jobID, actionType string, duration time.Duration) {
	fields := []logger.Field{
		logger.String("job_id", jobID),
		logger.String("action_type", actionType),
		logger.Int64("duration_ms", duration.Milliseconds()),
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		fields = append(fields, logger.String("trace_id", traceID))
	}
	log.WithContext(ctx).Info("Job completed", fields...)
}

// LogJobFailed logs when a job fails
func LogJobFailed(ctx context.Context, jobID, actionType string, attempt, maxAttempts int, err error) {
	fields := []logger.Field{
		logger.String("job_id", jobID),
		logger.String("action_type", actionType),
		logger.Int("attempt", attempt),
		logger.Int("max_attempts", maxAttempts),
		logger.Error(err),
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		fields = append(fields, logger.String("trace_id", traceID))
	}

	contextLogger := log.WithContext(ctx)
	// Use Error level for final failure, Warn for retryable failures
	if attempt >= maxAttempts {
		contextLogger.Error("Job failed permanently", fields...)
	} else {
		contextLogger.Warn("Job failed, will retry", fields...)
	}
}

// LogQueueStats logs queue statistics
func LogQueueStats(ctx context.Context, pending, running, completed, failed int) {
	fields := []logger.Field{
		logger.Int("pending", pending),
		logger.Int("running", running),
		logger.Int("completed", completed),
		logger.Int("failed", failed),
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		fields = append(fields, logger.String("trace_id", traceID))
	}
	log.WithContext(ctx).Info("Queue statistics", fields...)
}

// LogJobDropped logs when a job is dropped due to queue being full
func LogJobDropped(ctx context.Context, jobID, actionDesc string) {
	fields := []logger.Field{
		logger.String("job_id", jobID),
		logger.String("action_type", actionDesc),
		logger.String("reason", "queue_full"),
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		fields = append(fields, logger.String("trace_id", traceID))
	}
	log.WithContext(ctx).Warn("Job dropped", fields...)
}

// LogQueueStopped logs when the job queue processing is stopped
func LogQueueStopped(ctx context.Context, reason string, details ...any) {
	fields := []logger.Field{
		logger.String("reason", reason),
	}
	if len(details) > 0 {
		if len(details)%2 != 0 {
			// Append marker for odd length to prevent silent data loss
			details = append(details, "missing_value")
		}
		// Convert variadic key-value pairs to Fields
		for i := 0; i < len(details); i += 2 {
			key, ok := details[i].(string)
			if !ok {
				continue
			}
			fields = append(fields, logger.Any(key, details[i+1]))
		}
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		fields = append(fields, logger.String("trace_id", traceID))
	}
	log.WithContext(ctx).Info("Queue processing stopped", fields...)
}

// LogJobRetrying logs when a job is being retried (at execution start)
func LogJobRetrying(ctx context.Context, jobID, actionDesc string, attempt, maxAttempts int) {
	remainingAttempts := maxAttempts - attempt
	fields := []logger.Field{
		logger.String("job_id", jobID),
		logger.String("action_type", actionDesc),
		logger.Int("attempt", attempt),
		logger.Int("max_attempts", maxAttempts),
		logger.Int("remaining_attempts", remainingAttempts),
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		fields = append(fields, logger.String("trace_id", traceID))
	}
	log.WithContext(ctx).Info("Job retry execution starting", fields...)
}

// LogJobRetryScheduled logs when a job is scheduled for retry after failure
func LogJobRetryScheduled(ctx context.Context, jobID, actionDesc string, attempt, maxAttempts int, delay time.Duration, nextRetryAt time.Time, lastErr error) {
	remainingAttempts := maxAttempts - attempt
	fields := []logger.Field{
		logger.String("job_id", jobID),
		logger.String("action_type", actionDesc),
		logger.Int("attempt", attempt),
		logger.Int("max_attempts", maxAttempts),
		logger.Int("remaining_attempts", remainingAttempts),
		logger.Int64("retry_delay_ms", delay.Milliseconds()),
		logger.Time("next_retry_at", nextRetryAt),
		logger.Error(lastErr),
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		fields = append(fields, logger.String("trace_id", traceID))
	}
	log.WithContext(ctx).Warn("Job scheduled for retry after failure", fields...)
}

// LogJobSuccess logs when a job completes successfully
func LogJobSuccess(ctx context.Context, jobID, actionDesc string, attempt int) {
	fields := []logger.Field{
		logger.String("job_id", jobID),
		logger.String("action_type", actionDesc),
		logger.Int("attempt", attempt),
		logger.Bool("first_attempt", attempt == 1),
	}
	if traceID := extractTraceID(ctx); traceID != "" {
		fields = append(fields, logger.String("trace_id", traceID))
	}
	log.WithContext(ctx).Info("Job succeeded", fields...)
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