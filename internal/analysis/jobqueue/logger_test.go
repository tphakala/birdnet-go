package jobqueue

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
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
	defer func() { logger = oldLogger }()
	
	LogJobEnqueued("job-123", "process", 5)
	
	output := buf.String()
	if !strings.Contains(output, `"job_id":"job-123"`) {
		t.Errorf("Missing job_id in log: %s", output)
	}
	if !strings.Contains(output, `"action_type":"process"`) {
		t.Errorf("Missing action_type in log: %s", output)
	}
	if !strings.Contains(output, `"priority":5`) {
		t.Errorf("Missing priority in log: %s", output)
	}
	if !strings.Contains(output, "Job enqueued") {
		t.Errorf("Missing message in log: %s", output)
	}
}

// TestLogJobStarted tests job start logging
func TestLogJobStarted(t *testing.T) {
	var buf bytes.Buffer
	
	oldLogger := logger
	logger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	defer func() { logger = oldLogger }()
	
	LogJobStarted("job-456", "analyze", 2)
	
	output := buf.String()
	if !strings.Contains(output, `"job_id":"job-456"`) {
		t.Errorf("Missing job_id in log: %s", output)
	}
	if !strings.Contains(output, `"attempt":2`) {
		t.Errorf("Missing attempt in log: %s", output)
	}
	if !strings.Contains(output, "Job started") {
		t.Errorf("Missing message in log: %s", output)
	}
}

// TestLogJobCompleted tests job completion logging
func TestLogJobCompleted(t *testing.T) {
	var buf bytes.Buffer
	
	oldLogger := logger
	logger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	defer func() { logger = oldLogger }()
	
	duration := 150 * time.Millisecond
	LogJobCompleted("job-789", "upload", duration)
	
	output := buf.String()
	if !strings.Contains(output, `"job_id":"job-789"`) {
		t.Errorf("Missing job_id in log: %s", output)
	}
	if !strings.Contains(output, `"duration_ms":150`) {
		t.Errorf("Missing or incorrect duration_ms in log: %s", output)
	}
	if !strings.Contains(output, "Job completed") {
		t.Errorf("Missing message in log: %s", output)
	}
}

// TestLogJobFailed tests job failure logging
func TestLogJobFailed(t *testing.T) {
	var buf bytes.Buffer
	
	oldLogger := logger
	logger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	defer func() { logger = oldLogger }()
	
	testErr := errors.New("connection timeout")
	LogJobFailed("job-999", "download", 3, 5, testErr)
	
	output := buf.String()
	if !strings.Contains(output, `"job_id":"job-999"`) {
		t.Errorf("Missing job_id in log: %s", output)
	}
	if !strings.Contains(output, `"attempt":3`) {
		t.Errorf("Missing attempt in log: %s", output)
	}
	if !strings.Contains(output, `"max_retries":5`) {
		t.Errorf("Missing max_retries in log: %s", output)
	}
	if !strings.Contains(output, `"will_retry":true`) {
		t.Errorf("Missing or incorrect will_retry in log: %s", output)
	}
	if !strings.Contains(output, "connection timeout") {
		t.Errorf("Missing error message in log: %s", output)
	}
	if !strings.Contains(output, "Job failed") {
		t.Errorf("Missing message in log: %s", output)
	}
	
	// Test when no more retries
	buf.Reset()
	LogJobFailed("job-1000", "process", 5, 5, testErr)
	output = buf.String()
	if !strings.Contains(output, `"will_retry":false`) {
		t.Errorf("will_retry should be false when attempt equals max_retries: %s", output)
	}
}

// TestLogQueueStats tests queue statistics logging
func TestLogQueueStats(t *testing.T) {
	var buf bytes.Buffer
	
	oldLogger := logger
	logger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	defer func() { logger = oldLogger }()
	
	LogQueueStats(10, 3, 50, 2)
	
	output := buf.String()
	if !strings.Contains(output, `"pending":10`) {
		t.Errorf("Missing pending in log: %s", output)
	}
	if !strings.Contains(output, `"running":3`) {
		t.Errorf("Missing running in log: %s", output)
	}
	if !strings.Contains(output, `"completed":50`) {
		t.Errorf("Missing completed in log: %s", output)
	}
	if !strings.Contains(output, `"failed":2`) {
		t.Errorf("Missing failed in log: %s", output)
	}
	if !strings.Contains(output, `"total":65`) {
		t.Errorf("Missing or incorrect total in log: %s", output)
	}
	if !strings.Contains(output, "Queue statistics") {
		t.Errorf("Missing message in log: %s", output)
	}
}