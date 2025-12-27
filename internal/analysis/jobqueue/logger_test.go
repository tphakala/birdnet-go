package jobqueue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test constants for log level assertions.
const (
	logLevelWarn  = "WARN"
	logLevelError = "ERROR"
)

// setupTestLogger sets up a test logger and returns the buffer and cleanup function
func setupTestLogger(level slog.Level) (buf *bytes.Buffer, cleanup func()) {
	buf = &bytes.Buffer{}
	oldLogger := slogLogger
	slogLogger = slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{
		Level: level,
	}))
	return buf, func() { slogLogger = oldLogger }
}

// parseLogEntry parses JSON log output into a map
func parseLogEntry(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var logEntry map[string]any
	err := json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err, "Failed to parse log JSON")
	return logEntry
}

// TestGetLogger tests the GetLogger function
func TestGetLogger(t *testing.T) {
	logger := GetLogger()

	require.NotNil(t, logger, "GetLogger returned nil")

	// Should return same instance
	logger2 := GetLogger()
	assert.Same(t, logger, logger2, "GetLogger should return the same instance")
}

// TestLogJobEnqueued tests job enqueue logging
func TestLogJobEnqueued(t *testing.T) {
	buf, cleanup := setupTestLogger(slog.LevelInfo)
	t.Cleanup(cleanup)

	// Create context with trace ID for verification
	ctx := WithTraceID(context.Background(), "trace-123")
	LogJobEnqueued(ctx, "job-123", "process", true)

	logEntry := parseLogEntry(t, buf)

	// Assert JSON fields
	assert.Equal(t, "job-123", logEntry["job_id"], "Expected job_id 'job-123'")
	assert.Equal(t, "process", logEntry["action_type"], "Expected action_type 'process'")
	assert.Equal(t, true, logEntry["retryable"], "Expected retryable true")
	assert.Equal(t, "trace-123", logEntry["trace_id"], "Expected trace_id 'trace-123'")
	assert.Equal(t, "Job enqueued", logEntry["msg"], "Expected message 'Job enqueued'")
}

// TestLogJobStarted tests job start logging
func TestLogJobStarted(t *testing.T) {
	buf, cleanup := setupTestLogger(slog.LevelInfo)
	t.Cleanup(cleanup)

	LogJobStarted(context.TODO(), "job-456", "analyze")

	logEntry := parseLogEntry(t, buf)

	// Assert JSON fields including action_type
	assert.Equal(t, "job-456", logEntry["job_id"], "Expected job_id 'job-456'")
	assert.Equal(t, "analyze", logEntry["action_type"], "Expected action_type 'analyze'")
	assert.Equal(t, "Job started", logEntry["msg"], "Expected message 'Job started'")
}

// TestLogJobCompleted tests job completion logging
func TestLogJobCompleted(t *testing.T) {
	buf, cleanup := setupTestLogger(slog.LevelInfo)
	t.Cleanup(cleanup)

	duration := 150 * time.Millisecond
	LogJobCompleted(context.TODO(), "job-789", "upload", duration)

	logEntry := parseLogEntry(t, buf)

	// Assert JSON fields
	assert.Equal(t, "job-789", logEntry["job_id"], "Expected job_id 'job-789'")
	assert.Equal(t, "upload", logEntry["action_type"], "Expected action_type 'upload'")
	assert.InDelta(t, float64(150), logEntry["duration_ms"], 0, "Expected duration_ms 150")
	assert.Equal(t, "Job completed", logEntry["msg"], "Expected message 'Job completed'")
}

// TestLogJobFailed tests job failure logging
func TestLogJobFailed(t *testing.T) {
	buf, cleanup := setupTestLogger(slog.LevelInfo)
	t.Cleanup(cleanup)

	testErr := errors.New("connection timeout")
	LogJobFailed(context.TODO(), "job-999", "download", 3, 5, testErr)

	logEntry := parseLogEntry(t, buf)

	// Assert JSON fields
	assert.Equal(t, "job-999", logEntry["job_id"], "Expected job_id 'job-999'")
	assert.Equal(t, "download", logEntry["action_type"], "Expected action_type 'download'")
	assert.InDelta(t, float64(3), logEntry["attempt"], 0, "Expected attempt 3")
	assert.InDelta(t, float64(5), logEntry["max_attempts"], 0, "Expected max_attempts 5")
	assert.True(t, containsError(logEntry, "connection timeout"), "Expected error containing 'connection timeout'")
	// Should be Warn for retryable failure
	assert.Equal(t, logLevelWarn, logEntry["level"], "Expected level '%s' for retryable failure", logLevelWarn)
	assert.Equal(t, "Job failed, will retry", logEntry["msg"], "Expected message 'Job failed, will retry'")

	// Test when no more retries (final failure - should log at Error level)
	buf.Reset()
	LogJobFailed(context.TODO(), "job-1000", "process", 5, 5, testErr)

	logEntry2 := parseLogEntry(t, buf)

	// Should be Error for permanent failure
	assert.Equal(t, logLevelError, logEntry2["level"], "Expected level '%s' for permanent failure", logLevelError)
	assert.Equal(t, "Job failed permanently", logEntry2["msg"], "Expected message 'Job failed permanently' for final failure")
	assert.InDelta(t, float64(5), logEntry2["attempt"], 0, "Expected attempt 5 for permanent failure")
	assert.InDelta(t, float64(5), logEntry2["max_attempts"], 0, "Expected max_attempts 5 for permanent failure")
}

// TestLogQueueStats tests queue statistics logging
func TestLogQueueStats(t *testing.T) {
	buf, cleanup := setupTestLogger(slog.LevelInfo)
	t.Cleanup(cleanup)

	LogQueueStats(context.TODO(), 10, 3, 50, 2)

	logEntry := parseLogEntry(t, buf)

	// Assert JSON fields
	assert.InDelta(t, float64(10), logEntry["pending"], 0, "Expected pending 10")
	assert.InDelta(t, float64(3), logEntry["running"], 0, "Expected running 3")
	assert.InDelta(t, float64(50), logEntry["completed"], 0, "Expected completed 50")
	assert.InDelta(t, float64(2), logEntry["failed"], 0, "Expected failed 2")
	assert.Equal(t, "Queue statistics", logEntry["msg"], "Expected message 'Queue statistics'")
}

// TestLogJobDropped tests job dropped logging
func TestLogJobDropped(t *testing.T) {
	buf, cleanup := setupTestLogger(slog.LevelInfo)
	t.Cleanup(cleanup)

	LogJobDropped(context.TODO(), "job-dropped-1", "Upload to BirdWeather")

	logEntry := parseLogEntry(t, buf)

	// Assert JSON fields
	assert.Equal(t, "job-dropped-1", logEntry["job_id"], "Expected job_id 'job-dropped-1'")
	assert.Equal(t, "Upload to BirdWeather", logEntry["action_type"], "Expected action_type 'Upload to BirdWeather'")
	assert.Equal(t, "queue_full", logEntry["reason"], "Expected reason 'queue_full'")
	assert.Equal(t, logLevelWarn, logEntry["level"], "Expected level '%s' for dropped job", logLevelWarn)
	assert.Equal(t, "Job dropped", logEntry["msg"], "Expected message 'Job dropped'")
}

// TestLogQueueStopped tests queue stopped logging
func TestLogQueueStopped(t *testing.T) {
	buf, cleanup := setupTestLogger(slog.LevelInfo)
	t.Cleanup(cleanup)

	LogQueueStopped(context.TODO(), "manual shutdown", "pending_jobs", 5)

	logEntry := parseLogEntry(t, buf)

	// Assert JSON fields
	assert.Equal(t, "manual shutdown", logEntry["reason"], "Expected reason 'manual shutdown'")
	assert.InDelta(t, float64(5), logEntry["pending_jobs"], 0, "Expected pending_jobs 5")
	assert.Equal(t, "INFO", logEntry["level"], "Expected level 'INFO' for queue stopped")
	assert.Equal(t, "Queue processing stopped", logEntry["msg"], "Expected message 'Queue processing stopped'")
}

// TestLogJobRetrying tests job retry logging
func TestLogJobRetrying(t *testing.T) {
	buf, cleanup := setupTestLogger(slog.LevelInfo)
	t.Cleanup(cleanup)

	LogJobRetrying(context.TODO(), "job-retry-1", "Send MQTT message", 2, 5)

	logEntry := parseLogEntry(t, buf)

	// Assert JSON fields
	assert.Equal(t, "job-retry-1", logEntry["job_id"], "Expected job_id 'job-retry-1'")
	assert.Equal(t, "Send MQTT message", logEntry["action_type"], "Expected action_type 'Send MQTT message'")
	assert.InDelta(t, float64(2), logEntry["attempt"], 0, "Expected attempt 2")
	assert.InDelta(t, float64(5), logEntry["max_attempts"], 0, "Expected max_attempts 5")
	assert.InDelta(t, float64(3), logEntry["remaining_attempts"], 0, "Expected remaining_attempts 3") // 5 - 2 = 3
	assert.Equal(t, "Job retry execution starting", logEntry["msg"], "Expected message 'Job retry execution starting'")
}

// TestLogJobRetryScheduled tests job retry scheduling logging
func TestLogJobRetryScheduled(t *testing.T) {
	buf, cleanup := setupTestLogger(slog.LevelWarn)
	t.Cleanup(cleanup)

	// Create test data
	nextRetryAt := time.Now().Add(30 * time.Second)
	delay := 30 * time.Second
	testErr := fmt.Errorf("connection timeout")

	LogJobRetryScheduled(context.TODO(), "job-retry-sched-1", "HTTP POST request", 2, 5, delay, nextRetryAt, testErr)

	logEntry := parseLogEntry(t, buf)

	// Assert JSON fields
	assert.Equal(t, "job-retry-sched-1", logEntry["job_id"], "Expected job_id 'job-retry-sched-1'")
	assert.Equal(t, "HTTP POST request", logEntry["action_type"], "Expected action_type 'HTTP POST request'")
	assert.InDelta(t, float64(2), logEntry["attempt"], 0, "Expected attempt 2")
	assert.InDelta(t, float64(5), logEntry["max_attempts"], 0, "Expected max_attempts 5")
	assert.InDelta(t, float64(3), logEntry["remaining_attempts"], 0, "Expected remaining_attempts 3") // 5 - 2 = 3
	assert.InDelta(t, float64(30000), logEntry["retry_delay_ms"], 0, "Expected retry_delay_ms 30000") // 30 seconds = 30000ms
	assert.Equal(t, "connection timeout", logEntry["error"], "Expected error 'connection timeout'")
	assert.Equal(t, logLevelWarn, logEntry["level"], "Expected level '%s' for retry scheduling", logLevelWarn)
	assert.Equal(t, "Job scheduled for retry after failure", logEntry["msg"], "Expected message 'Job scheduled for retry after failure'")
	// Note: next_retry_at is a formatted timestamp, we just check it exists
	assert.Contains(t, logEntry, "next_retry_at", "Expected next_retry_at field to exist")
}

// TestLogJobSuccess tests job success logging
func TestLogJobSuccess(t *testing.T) {
	buf, cleanup := setupTestLogger(slog.LevelInfo)
	t.Cleanup(cleanup)

	// Test first attempt success
	LogJobSuccess(context.TODO(), "job-success-1", "Save to database", 1)

	logEntry := parseLogEntry(t, buf)

	// Assert JSON fields
	assert.Equal(t, "job-success-1", logEntry["job_id"], "Expected job_id 'job-success-1'")
	assert.Equal(t, "Save to database", logEntry["action_type"], "Expected action_type 'Save to database'")
	assert.InDelta(t, float64(1), logEntry["attempt"], 0, "Expected attempt 1")
	assert.Equal(t, true, logEntry["first_attempt"], "Expected first_attempt true")
	assert.Equal(t, "Job succeeded", logEntry["msg"], "Expected message 'Job succeeded'")

	// Test retry success
	buf.Reset()
	LogJobSuccess(context.TODO(), "job-success-2", "Upload after retry", 3)

	logEntry2 := parseLogEntry(t, buf)

	assert.InDelta(t, float64(3), logEntry2["attempt"], 0, "Expected attempt 3")
	assert.Equal(t, false, logEntry2["first_attempt"], "Expected first_attempt false for retry")
}

// TestExtractTraceID tests trace ID extraction from context
func TestExtractTraceID(t *testing.T) {
	// Test with empty context
	traceID := extractTraceID(context.Background())
	assert.Empty(t, traceID, "Expected empty trace ID for empty context")

	// Test with string trace ID
	ctx := WithTraceID(context.Background(), "trace-123")
	traceID = extractTraceID(ctx)
	assert.Equal(t, "trace-123", traceID, "Expected trace ID 'trace-123'")
}

// Helper function to check if error field contains expected text
func containsError(logEntry map[string]any, expectedText string) bool {
	if errorVal, ok := logEntry["error"]; ok {
		if errorStr, ok := errorVal.(string); ok {
			// Check if the expectedText is a substring of the error string
			return errorStr != "" && strings.Contains(errorStr, expectedText)
		}
	}
	return false
}
