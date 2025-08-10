package analysis

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestGetLogger tests the GetLogger function
func TestGetLogger(t *testing.T) {
	// Get the logger multiple times to test thread-safety
	logger1 := GetLogger()
	logger2 := GetLogger()
	
	// Both should return the same instance
	if logger1 != logger2 {
		t.Error("GetLogger should return the same instance")
	}
	
	// Logger should not be nil
	if logger1 == nil {
		t.Error("GetLogger returned nil")
	}
}

// TestLoggerOutput tests that the logger produces expected output
func TestLoggerOutput(t *testing.T) {
	// Create a buffer to capture output
	var buf bytes.Buffer
	
	// Create a test logger with JSON handler
	testLogger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	
	// Temporarily replace the package logger
	oldLogger := logger
	logger = testLogger
	defer func() { logger = oldLogger }()
	
	// Use GetLogger and write a log
	l := GetLogger()
	l.Info("test message", "key", "value", "number", 42)
	
	// Check output contains expected fields
	output := buf.String()
	if !strings.Contains(output, `"msg":"test message"`) {
		t.Errorf("Log output missing message: %s", output)
	}
	if !strings.Contains(output, `"key":"value"`) {
		t.Errorf("Log output missing key field: %s", output)
	}
	if !strings.Contains(output, `"number":42`) {
		t.Errorf("Log output missing number field: %s", output)
	}
}

// TestLoggerLevels tests that log levels work correctly
func TestLoggerLevels(t *testing.T) {
	var buf bytes.Buffer
	
	// Create logger with Info level
	testLogger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	
	oldLogger := logger
	logger = testLogger
	defer func() { logger = oldLogger }()
	
	l := GetLogger()
	
	// Debug should not appear
	buf.Reset()
	l.Debug("debug message")
	if buf.Len() > 0 {
		t.Error("Debug message should not appear at Info level")
	}
	
	// Info should appear
	buf.Reset()
	l.Info("info message")
	if !strings.Contains(buf.String(), "info message") {
		t.Error("Info message should appear at Info level")
	}
	
	// Warn should appear
	buf.Reset()
	l.Warn("warn message")
	if !strings.Contains(buf.String(), "warn message") {
		t.Error("Warn message should appear at Info level")
	}
	
	// Error should appear
	buf.Reset()
	l.Error("error message")
	if !strings.Contains(buf.String(), "error message") {
		t.Error("Error message should appear at Info level")
	}
}

// TestConcurrentLoggerAccess tests thread-safe access
func TestConcurrentLoggerAccess(t *testing.T) {
	// Run multiple goroutines accessing the logger
	done := make(chan bool, 10)
	
	for i := 0; i < 10; i++ {
		go func(id int) {
			l := GetLogger()
			if l == nil {
				t.Error("GetLogger returned nil in goroutine")
			}
			done <- true
		}(i)
	}
	
	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}