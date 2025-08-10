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
)

// setupTestLogger sets up a test logger and returns the buffer and cleanup function
func setupTestLogger(level slog.Level) (buf *bytes.Buffer, cleanup func()) {
	buf = &bytes.Buffer{}
	oldLogger := logger
	logger = slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{
		Level: level,
	}))
	return buf, func() { logger = oldLogger }
}

// parseLogEntry parses JSON log output into a map
func parseLogEntry(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var logEntry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("Failed to parse log JSON: %v", err)
	}
	return logEntry
}

// TestGetLogger tests the GetLogger function
func TestGetLogger(t *testing.T) {
	logger := GetLogger()
	
	if logger == nil {
		t.Fatal("GetLogger returned nil")
	}
	
	// Should return same instance
	logger2 := GetLogger()
	if logger != logger2 {
		t.Error("GetLogger should return the same instance")
	}
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
	if logEntry["job_id"] != "job-123" {
		t.Errorf("Expected job_id 'job-123', got %v", logEntry["job_id"])
	}
	if logEntry["action_type"] != "process" {
		t.Errorf("Expected action_type 'process', got %v", logEntry["action_type"])
	}
	if logEntry["retryable"] != true {
		t.Errorf("Expected retryable true, got %v", logEntry["retryable"])
	}
	if logEntry["trace_id"] != "trace-123" {
		t.Errorf("Expected trace_id 'trace-123', got %v", logEntry["trace_id"])
	}
	if logEntry["msg"] != "Job enqueued" {
		t.Errorf("Expected message 'Job enqueued', got %v", logEntry["msg"])
	}
}

// TestLogJobStarted tests job start logging
func TestLogJobStarted(t *testing.T) {
	buf, cleanup := setupTestLogger(slog.LevelInfo)
	t.Cleanup(cleanup)
	
	LogJobStarted(context.TODO(), "job-456", "analyze")
	
	logEntry := parseLogEntry(t, buf)
	
	// Assert JSON fields including action_type
	if logEntry["job_id"] != "job-456" {
		t.Errorf("Expected job_id 'job-456', got %v", logEntry["job_id"])
	}
	if logEntry["action_type"] != "analyze" {
		t.Errorf("Expected action_type 'analyze', got %v", logEntry["action_type"])
	}
	if logEntry["msg"] != "Job started" {
		t.Errorf("Expected message 'Job started', got %v", logEntry["msg"])
	}
}

// TestLogJobCompleted tests job completion logging
func TestLogJobCompleted(t *testing.T) {
	buf, cleanup := setupTestLogger(slog.LevelInfo)
	t.Cleanup(cleanup)
	
	duration := 150 * time.Millisecond
	LogJobCompleted(context.TODO(), "job-789", "upload", duration)
	
	logEntry := parseLogEntry(t, buf)
	
	// Assert JSON fields
	if logEntry["job_id"] != "job-789" {
		t.Errorf("Expected job_id 'job-789', got %v", logEntry["job_id"])
	}
	if logEntry["action_type"] != "upload" {
		t.Errorf("Expected action_type 'upload', got %v", logEntry["action_type"])
	}
	if logEntry["duration_ms"] != float64(150) {
		t.Errorf("Expected duration_ms 150, got %v", logEntry["duration_ms"])
	}
	if logEntry["msg"] != "Job completed" {
		t.Errorf("Expected message 'Job completed', got %v", logEntry["msg"])
	}
}

// TestLogJobFailed tests job failure logging
func TestLogJobFailed(t *testing.T) {
	buf, cleanup := setupTestLogger(slog.LevelInfo)
	t.Cleanup(cleanup)
	
	testErr := errors.New("connection timeout")
	LogJobFailed(context.TODO(), "job-999", "download", 3, 5, testErr)
	
	logEntry := parseLogEntry(t, buf)
	
	// Assert JSON fields
	if logEntry["job_id"] != "job-999" {
		t.Errorf("Expected job_id 'job-999', got %v", logEntry["job_id"])
	}
	if logEntry["action_type"] != "download" {
		t.Errorf("Expected action_type 'download', got %v", logEntry["action_type"])
	}
	if logEntry["attempt"] != float64(3) {
		t.Errorf("Expected attempt 3, got %v", logEntry["attempt"])
	}
	if logEntry["max_attempts"] != float64(5) {
		t.Errorf("Expected max_attempts 5, got %v", logEntry["max_attempts"])
	}
	if !containsError(logEntry, "connection timeout") {
		t.Errorf("Expected error containing 'connection timeout', got %v", logEntry["error"])
	}
	// Should be Warn for retryable failure
	if logEntry["level"] != "WARN" {
		t.Errorf("Expected level 'WARN' for retryable failure, got %v", logEntry["level"])
	}
	if logEntry["msg"] != "Job failed, will retry" {
		t.Errorf("Expected message 'Job failed, will retry', got %v", logEntry["msg"])
	}
	
	// Test when no more retries (final failure - should log at Error level)
	buf.Reset()
	LogJobFailed(context.TODO(), "job-1000", "process", 5, 5, testErr)
	
	logEntry2 := parseLogEntry(t, buf)
	
	// Should be Error for permanent failure
	if logEntry2["level"] != "ERROR" {
		t.Errorf("Expected level 'ERROR' for permanent failure, got %v", logEntry2["level"])
	}
	if logEntry2["msg"] != "Job failed permanently" {
		t.Errorf("Expected message 'Job failed permanently' for final failure, got %v", logEntry2["msg"])
	}
	if logEntry2["attempt"] != float64(5) {
		t.Errorf("Expected attempt 5 for permanent failure, got %v", logEntry2["attempt"])
	}
	if logEntry2["max_attempts"] != float64(5) {
		t.Errorf("Expected max_attempts 5 for permanent failure, got %v", logEntry2["max_attempts"])
	}
}

// TestLogQueueStats tests queue statistics logging
func TestLogQueueStats(t *testing.T) {
	buf, cleanup := setupTestLogger(slog.LevelInfo)
	t.Cleanup(cleanup)
	
	LogQueueStats(context.TODO(), 10, 3, 50, 2)
	
	logEntry := parseLogEntry(t, buf)
	
	// Assert JSON fields
	if logEntry["pending"] != float64(10) {
		t.Errorf("Expected pending 10, got %v", logEntry["pending"])
	}
	if logEntry["running"] != float64(3) {
		t.Errorf("Expected running 3, got %v", logEntry["running"])
	}
	if logEntry["completed"] != float64(50) {
		t.Errorf("Expected completed 50, got %v", logEntry["completed"])
	}
	if logEntry["failed"] != float64(2) {
		t.Errorf("Expected failed 2, got %v", logEntry["failed"])
	}
	if logEntry["msg"] != "Queue statistics" {
		t.Errorf("Expected message 'Queue statistics', got %v", logEntry["msg"])
	}
}

// TestLogJobDropped tests job dropped logging
func TestLogJobDropped(t *testing.T) {
	buf, cleanup := setupTestLogger(slog.LevelInfo)
	t.Cleanup(cleanup)
	
	LogJobDropped(context.TODO(), "job-dropped-1", "Upload to BirdWeather")
	
	logEntry := parseLogEntry(t, buf)
	
	// Assert JSON fields
	if logEntry["job_id"] != "job-dropped-1" {
		t.Errorf("Expected job_id 'job-dropped-1', got %v", logEntry["job_id"])
	}
	if logEntry["action_type"] != "Upload to BirdWeather" {
		t.Errorf("Expected action_type 'Upload to BirdWeather', got %v", logEntry["action_type"])
	}
	if logEntry["reason"] != "queue_full" {
		t.Errorf("Expected reason 'queue_full', got %v", logEntry["reason"])
	}
	if logEntry["level"] != "WARN" {
		t.Errorf("Expected level 'WARN' for dropped job, got %v", logEntry["level"])
	}
	if logEntry["msg"] != "Job dropped" {
		t.Errorf("Expected message 'Job dropped', got %v", logEntry["msg"])
	}
}

// TestLogQueueStopped tests queue stopped logging
func TestLogQueueStopped(t *testing.T) {
	buf, cleanup := setupTestLogger(slog.LevelInfo)
	t.Cleanup(cleanup)
	
	LogQueueStopped(context.TODO(), "manual shutdown", "pending_jobs", 5)
	
	logEntry := parseLogEntry(t, buf)
	
	// Assert JSON fields
	if logEntry["reason"] != "manual shutdown" {
		t.Errorf("Expected reason 'manual shutdown', got %v", logEntry["reason"])
	}
	if logEntry["pending_jobs"] != float64(5) {
		t.Errorf("Expected pending_jobs 5, got %v", logEntry["pending_jobs"])
	}
	if logEntry["level"] != "INFO" {
		t.Errorf("Expected level 'INFO' for queue stopped, got %v", logEntry["level"])
	}
	if logEntry["msg"] != "Queue processing stopped" {
		t.Errorf("Expected message 'Queue processing stopped', got %v", logEntry["msg"])
	}
}

// TestLogJobRetrying tests job retry logging
func TestLogJobRetrying(t *testing.T) {
	buf, cleanup := setupTestLogger(slog.LevelInfo)
	t.Cleanup(cleanup)
	
	LogJobRetrying(context.TODO(), "job-retry-1", "Send MQTT message", 2, 5)
	
	logEntry := parseLogEntry(t, buf)
	
	// Assert JSON fields
	if logEntry["job_id"] != "job-retry-1" {
		t.Errorf("Expected job_id 'job-retry-1', got %v", logEntry["job_id"])
	}
	if logEntry["action_type"] != "Send MQTT message" {
		t.Errorf("Expected action_type 'Send MQTT message', got %v", logEntry["action_type"])
	}
	if logEntry["attempt"] != float64(2) {
		t.Errorf("Expected attempt 2, got %v", logEntry["attempt"])
	}
	if logEntry["max_attempts"] != float64(5) {
		t.Errorf("Expected max_attempts 5, got %v", logEntry["max_attempts"])
	}
	if logEntry["remaining_attempts"] != float64(3) { // 5 - 2 = 3
		t.Errorf("Expected remaining_attempts 3, got %v", logEntry["remaining_attempts"])
	}
	if logEntry["msg"] != "Job retry execution starting" {
		t.Errorf("Expected message 'Job retry execution starting', got %v", logEntry["msg"])
	}
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
	if logEntry["job_id"] != "job-retry-sched-1" {
		t.Errorf("Expected job_id 'job-retry-sched-1', got %v", logEntry["job_id"])
	}
	if logEntry["action_type"] != "HTTP POST request" {
		t.Errorf("Expected action_type 'HTTP POST request', got %v", logEntry["action_type"])
	}
	if logEntry["attempt"] != float64(2) {
		t.Errorf("Expected attempt 2, got %v", logEntry["attempt"])
	}
	if logEntry["max_attempts"] != float64(5) {
		t.Errorf("Expected max_attempts 5, got %v", logEntry["max_attempts"])
	}
	if logEntry["remaining_attempts"] != float64(3) { // 5 - 2 = 3
		t.Errorf("Expected remaining_attempts 3, got %v", logEntry["remaining_attempts"])
	}
	if logEntry["retry_delay_ms"] != float64(30000) { // 30 seconds = 30000ms
		t.Errorf("Expected retry_delay_ms 30000, got %v", logEntry["retry_delay_ms"])
	}
	if logEntry["error"] != "connection timeout" {
		t.Errorf("Expected error 'connection timeout', got %v", logEntry["error"])
	}
	if logEntry["level"] != "WARN" {
		t.Errorf("Expected level 'WARN' for retry scheduling, got %v", logEntry["level"])
	}
	if logEntry["msg"] != "Job scheduled for retry after failure" {
		t.Errorf("Expected message 'Job scheduled for retry after failure', got %v", logEntry["msg"])
	}
	// Note: next_retry_at is a formatted timestamp, we just check it exists
	if _, exists := logEntry["next_retry_at"]; !exists {
		t.Errorf("Expected next_retry_at field to exist")
	}
}

// TestLogJobSuccess tests job success logging
func TestLogJobSuccess(t *testing.T) {
	buf, cleanup := setupTestLogger(slog.LevelInfo)
	t.Cleanup(cleanup)
	
	// Test first attempt success
	LogJobSuccess(context.TODO(), "job-success-1", "Save to database", 1)
	
	logEntry := parseLogEntry(t, buf)
	
	// Assert JSON fields
	if logEntry["job_id"] != "job-success-1" {
		t.Errorf("Expected job_id 'job-success-1', got %v", logEntry["job_id"])
	}
	if logEntry["action_type"] != "Save to database" {
		t.Errorf("Expected action_type 'Save to database', got %v", logEntry["action_type"])
	}
	if logEntry["attempt"] != float64(1) {
		t.Errorf("Expected attempt 1, got %v", logEntry["attempt"])
	}
	if logEntry["first_attempt"] != true {
		t.Errorf("Expected first_attempt true, got %v", logEntry["first_attempt"])
	}
	if logEntry["msg"] != "Job succeeded" {
		t.Errorf("Expected message 'Job succeeded', got %v", logEntry["msg"])
	}
	
	// Test retry success
	buf.Reset()
	LogJobSuccess(context.TODO(), "job-success-2", "Upload after retry", 3)
	
	logEntry2 := parseLogEntry(t, buf)
	
	if logEntry2["attempt"] != float64(3) {
		t.Errorf("Expected attempt 3, got %v", logEntry2["attempt"])
	}
	if logEntry2["first_attempt"] != false {
		t.Errorf("Expected first_attempt false for retry, got %v", logEntry2["first_attempt"])
	}
}

// TestExtractTraceID tests trace ID extraction from context
func TestExtractTraceID(t *testing.T) {
	// Test with empty context
	if traceID := extractTraceID(context.Background()); traceID != "" {
		t.Errorf("Expected empty trace ID for empty context, got %q", traceID)
	}
	
	// Test with string trace ID
	ctx := WithTraceID(context.Background(), "trace-123")
	if traceID := extractTraceID(ctx); traceID != "trace-123" {
		t.Errorf("Expected trace ID 'trace-123', got %q", traceID)
	}
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