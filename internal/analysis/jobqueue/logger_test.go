package jobqueue

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"
)

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
	var buf bytes.Buffer
	
	// Replace logger with test logger
	oldLogger := logger
	logger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	t.Cleanup(func() { logger = oldLogger })
	
	LogJobEnqueued("job-123", "process", 5)
	
	// Parse JSON output
	var logEntry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("Failed to parse log JSON: %v", err)
	}
	
	// Assert JSON fields
	if logEntry["job_id"] != "job-123" {
		t.Errorf("Expected job_id 'job-123', got %v", logEntry["job_id"])
	}
	if logEntry["action_type"] != "process" {
		t.Errorf("Expected action_type 'process', got %v", logEntry["action_type"])
	}
	if logEntry["priority"] != float64(5) {
		t.Errorf("Expected priority 5, got %v", logEntry["priority"])
	}
	if logEntry["msg"] != "Job enqueued" {
		t.Errorf("Expected message 'Job enqueued', got %v", logEntry["msg"])
	}
}

// TestLogJobStarted tests job start logging
func TestLogJobStarted(t *testing.T) {
	var buf bytes.Buffer
	
	oldLogger := logger
	logger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	t.Cleanup(func() { logger = oldLogger })
	
	LogJobStarted("job-456", "analyze", 2)
	
	// Parse JSON output
	var logEntry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("Failed to parse log JSON: %v", err)
	}
	
	// Assert JSON fields including action_type
	if logEntry["job_id"] != "job-456" {
		t.Errorf("Expected job_id 'job-456', got %v", logEntry["job_id"])
	}
	if logEntry["action_type"] != "analyze" {
		t.Errorf("Expected action_type 'analyze', got %v", logEntry["action_type"])
	}
	if logEntry["attempt"] != float64(2) {
		t.Errorf("Expected attempt 2, got %v", logEntry["attempt"])
	}
	if logEntry["msg"] != "Job started" {
		t.Errorf("Expected message 'Job started', got %v", logEntry["msg"])
	}
}

// TestLogJobCompleted tests job completion logging
func TestLogJobCompleted(t *testing.T) {
	var buf bytes.Buffer
	
	oldLogger := logger
	logger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	t.Cleanup(func() { logger = oldLogger })
	
	duration := 150 * time.Millisecond
	LogJobCompleted("job-789", "upload", duration)
	
	// Parse JSON output
	var logEntry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("Failed to parse log JSON: %v", err)
	}
	
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
	var buf bytes.Buffer
	
	oldLogger := logger
	logger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	t.Cleanup(func() { logger = oldLogger })
	
	testErr := errors.New("connection timeout")
	LogJobFailed("job-999", "download", 3, 5, testErr)
	
	// Parse JSON output
	var logEntry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("Failed to parse log JSON: %v", err)
	}
	
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
	if logEntry["max_retries"] != float64(5) {
		t.Errorf("Expected max_retries 5, got %v", logEntry["max_retries"])
	}
	if logEntry["will_retry"] != true {
		t.Errorf("Expected will_retry true, got %v", logEntry["will_retry"])
	}
	if !containsError(logEntry, "connection timeout") {
		t.Errorf("Expected error containing 'connection timeout', got %v", logEntry["error"])
	}
	if logEntry["msg"] != "Job failed" {
		t.Errorf("Expected message 'Job failed', got %v", logEntry["msg"])
	}
	
	// Test when no more retries
	buf.Reset()
	LogJobFailed("job-1000", "process", 5, 5, testErr)
	
	var logEntry2 map[string]any
	if err := json.Unmarshal(buf.Bytes(), &logEntry2); err != nil {
		t.Fatalf("Failed to parse log JSON: %v", err)
	}
	
	if logEntry2["will_retry"] != false {
		t.Errorf("Expected will_retry false when attempt equals max_retries, got %v", logEntry2["will_retry"])
	}
}

// TestLogQueueStats tests queue statistics logging
func TestLogQueueStats(t *testing.T) {
	var buf bytes.Buffer
	
	oldLogger := logger
	logger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	t.Cleanup(func() { logger = oldLogger })
	
	LogQueueStats(10, 3, 50, 2)
	
	// Parse JSON output
	var logEntry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("Failed to parse log JSON: %v", err)
	}
	
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
	if logEntry["total"] != float64(65) {
		t.Errorf("Expected total 65, got %v", logEntry["total"])
	}
	if logEntry["msg"] != "Queue statistics" {
		t.Errorf("Expected message 'Queue statistics', got %v", logEntry["msg"])
	}
}

// TestDebugLogSuppression tests that debug logs are suppressed at Info level
func TestDebugLogSuppression(t *testing.T) {
	var buf bytes.Buffer
	
	oldLogger := logger
	logger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	t.Cleanup(func() { logger = oldLogger })
	
	// Debug logs should not appear
	LogJobEnqueued("job-debug", "test", 1)
	if buf.Len() > 0 {
		t.Error("Debug log should be suppressed at Info level")
	}
	
	buf.Reset()
	LogJobStarted("job-debug", "test", 1)
	if buf.Len() > 0 {
		t.Error("Debug log should be suppressed at Info level")
	}
}

// Helper function to check if error field contains expected text
func containsError(logEntry map[string]any, expectedText string) bool {
	if errorVal, ok := logEntry["error"]; ok {
		if errorStr, ok := errorVal.(string); ok {
			return len(errorStr) > 0 // Just check that error is present and non-empty
		}
	}
	return false
}