package analysis

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetLogger tests the GetLogger function
func TestGetLogger(t *testing.T) {
	// Get the logger multiple times to test thread-safety
	logger1 := GetLogger()
	logger2 := GetLogger()

	// Both should return the same instance
	assert.Same(t, logger1, logger2, "GetLogger should return the same instance")

	// Logger should not be nil
	assert.NotNil(t, logger1, "GetLogger returned nil")
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
	t.Cleanup(func() { logger = oldLogger })

	// Use GetLogger and write a log
	l := GetLogger()
	l.Info("test message", "key", "value", "number", 42)

	// Parse JSON output
	var logEntry map[string]any
	err := json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err, "Failed to parse log JSON")

	// Check output contains expected fields
	assert.Equal(t, "test message", logEntry["msg"], "Expected message 'test message'")
	assert.Equal(t, "value", logEntry["key"], "Expected key 'value'")
	assert.InDelta(t, float64(42), logEntry["number"], 0, "Expected number 42")
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
	t.Cleanup(func() { logger = oldLogger })

	l := GetLogger()

	// Debug should not appear
	buf.Reset()
	l.Debug("debug message")
	assert.Zero(t, buf.Len(), "Debug message should not appear at Info level")

	// Info should appear
	buf.Reset()
	l.Info("info message")
	var logEntry map[string]any
	err := json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err, "Failed to parse Info log JSON")
	assert.Equal(t, "INFO", logEntry["level"], "Expected level 'INFO'")
	assert.Equal(t, "info message", logEntry["msg"], "Info message should appear at Info level")

	// Warn should appear
	buf.Reset()
	l.Warn("warn message")
	err = json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err, "Failed to parse Warn log JSON")
	assert.Equal(t, "WARN", logEntry["level"], "Expected level 'WARN'")
	assert.Equal(t, "warn message", logEntry["msg"], "Warn message should appear at Info level")

	// Error should appear
	buf.Reset()
	l.Error("error message")
	err = json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err, "Failed to parse Error log JSON")
	assert.Equal(t, "ERROR", logEntry["level"], "Expected level 'ERROR'")
	assert.Equal(t, "error message", logEntry["msg"], "Error message should appear at Info level")
}

// TestConcurrentLoggerAccess tests thread-safe access
func TestConcurrentLoggerAccess(t *testing.T) {
	// Run multiple goroutines accessing the logger
	// Use error channel to safely collect results (testing.T is not goroutine-safe)
	errCh := make(chan error, 10)

	for i := range 10 {
		go func(id int) {
			l := GetLogger()
			if l == nil {
				errCh <- fmt.Errorf("GetLogger returned nil in goroutine %d", id)
			} else {
				errCh <- nil
			}
		}(i)
	}

	// Collect results from all goroutines
	for range 10 {
		if err := <-errCh; err != nil {
			t.Error(err)
		}
	}
}
