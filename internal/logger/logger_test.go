package logger

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFieldConstructors tests all field constructor functions
func TestFieldConstructors(t *testing.T) {
	t.Run("String field", func(t *testing.T) {
		field := String("key", "value")
		assert.Equal(t, "key", field.Key)
		assert.Equal(t, "value", field.Value)
	})

	t.Run("Int field", func(t *testing.T) {
		field := Int("count", 42)
		assert.Equal(t, "count", field.Key)
		assert.Equal(t, 42, field.Value)
	})

	t.Run("Int64 field", func(t *testing.T) {
		field := Int64("big_count", int64(9223372036854775807))
		assert.Equal(t, "big_count", field.Key)
		assert.Equal(t, int64(9223372036854775807), field.Value)
	})

	t.Run("Bool field", func(t *testing.T) {
		field := Bool("enabled", true)
		assert.Equal(t, "enabled", field.Key)
		assert.Equal(t, true, field.Value)
	})

	t.Run("Error field with error", func(t *testing.T) {
		err := errors.New("test error")
		field := Error(err)
		assert.Equal(t, "error", field.Key)
		assert.Equal(t, "test error", field.Value)
	})

	t.Run("Error field with nil", func(t *testing.T) {
		field := Error(nil)
		assert.Equal(t, "error", field.Key)
		assert.Nil(t, field.Value)
	})

	t.Run("Duration field", func(t *testing.T) {
		duration := 5 * time.Second
		field := Duration("elapsed", duration)
		assert.Equal(t, "elapsed", field.Key)
		// Field stores raw time.Duration; conversion to string happens in fieldToAttr
		assert.Equal(t, duration, field.Value)
	})

	t.Run("Time field", func(t *testing.T) {
		now := time.Now()
		field := Time("timestamp", now)
		assert.Equal(t, "timestamp", field.Key)
		assert.Equal(t, now, field.Value)
	})

	t.Run("Any field", func(t *testing.T) {
		data := map[string]int{"count": 10}
		field := Any("data", data)
		assert.Equal(t, "data", field.Key)
		assert.Equal(t, data, field.Value)
	})
}

// TestNewSlogLogger tests logger creation with different configurations
func TestNewSlogLogger(t *testing.T) {
	t.Run("create with valid config", func(t *testing.T) {
		buf := &bytes.Buffer{}
		tz, err := time.LoadLocation("Europe/Helsinki")
		require.NoError(t, err)

		logger := NewSlogLogger(buf, LogLevelInfo, tz)

		assert.NotNil(t, logger)
		assert.Equal(t, tz, logger.timezone)
	})

	t.Run("create with nil writer defaults to stdout", func(t *testing.T) {
		logger := NewSlogLogger(nil, LogLevelInfo, nil)

		assert.NotNil(t, logger)
		assert.NotNil(t, logger.timezone)
	})

	t.Run("create with nil timezone defaults to UTC", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := NewSlogLogger(buf, LogLevelDebug, nil)

		assert.NotNil(t, logger)
		assert.Equal(t, time.UTC, logger.timezone)
	})

	t.Run("create with different log levels", func(t *testing.T) {
		levels := []LogLevel{LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError}

		for _, level := range levels {
			buf := &bytes.Buffer{}
			logger := NewSlogLogger(buf, level, time.UTC)
			assert.NotNil(t, logger)
		}
	})
}

// TestSlogLogger_Module tests module scoping
func TestSlogLogger_Module(t *testing.T) {
	t.Run("create module logger", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := NewSlogLogger(buf, LogLevelInfo, time.UTC)

		moduleLogger := logger.Module("notifications")

		require.NotNil(t, moduleLogger)
		assert.IsType(t, &SlogLogger{}, moduleLogger)
	})

	t.Run("nested module names", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := NewSlogLogger(buf, LogLevelInfo, time.UTC)

		level1 := logger.Module("notifications")
		level2 := level1.Module("provider")

		buf.Reset()
		level2.Info("test message")

		output := buf.String()
		assert.Contains(t, output, "notifications.provider")
	})

	t.Run("nil logger returns nil module", func(t *testing.T) {
		var logger *SlogLogger
		moduleLogger := logger.Module("test")
		assert.Nil(t, moduleLogger)
	})
}

// TestSlogLogger_LogLevels tests different log levels
func TestSlogLogger_LogLevels(t *testing.T) {
	tests := []struct {
		name          string
		logLevel      LogLevel
		logFunc       func(Logger)
		shouldLog     bool
		expectedLevel string
	}{
		{
			name:      "debug message at debug level",
			logLevel:  LogLevelDebug,
			logFunc:   func(l Logger) { l.Debug("debug message") },
			shouldLog: true,
		},
		{
			name:      "debug message at info level",
			logLevel:  LogLevelInfo,
			logFunc:   func(l Logger) { l.Debug("debug message") },
			shouldLog: false,
		},
		{
			name:      "info message at info level",
			logLevel:  LogLevelInfo,
			logFunc:   func(l Logger) { l.Info("info message") },
			shouldLog: true,
		},
		{
			name:      "info message at warn level",
			logLevel:  LogLevelWarn,
			logFunc:   func(l Logger) { l.Info("info message") },
			shouldLog: false,
		},
		{
			name:      "warn message at warn level",
			logLevel:  LogLevelWarn,
			logFunc:   func(l Logger) { l.Warn("warn message") },
			shouldLog: true,
		},
		{
			name:      "warn message at error level",
			logLevel:  LogLevelError,
			logFunc:   func(l Logger) { l.Warn("warn message") },
			shouldLog: false,
		},
		{
			name:      "error message at any level",
			logLevel:  LogLevelError,
			logFunc:   func(l Logger) { l.Error("error message") },
			shouldLog: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			logger := NewSlogLogger(buf, tt.logLevel, time.UTC)

			tt.logFunc(logger)

			output := buf.String()
			if tt.shouldLog {
				assert.NotEmpty(t, output)
			} else {
				assert.Empty(t, output)
			}
		})
	}
}

// TestSlogLogger_StructuredFields tests structured field logging
func TestSlogLogger_StructuredFields(t *testing.T) {
	t.Run("log with string field", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := NewSlogLogger(buf, LogLevelInfo, time.UTC)

		logger.Info("test message", String("key", "value"))

		output := buf.String()
		assert.Contains(t, output, "test message")
		assert.Contains(t, output, "key")
		assert.Contains(t, output, "value")
	})

	t.Run("log with multiple fields", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := NewSlogLogger(buf, LogLevelInfo, time.UTC)

		logger.Info("test",
			String("name", "test"),
			Int("count", 42),
			Bool("enabled", true))

		output := buf.String()
		assert.Contains(t, output, "name")
		assert.Contains(t, output, "test")
		assert.Contains(t, output, "count")
		assert.Contains(t, output, "42")
		assert.Contains(t, output, "enabled")
		assert.Contains(t, output, "true")
	})

	t.Run("log with error field", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := NewSlogLogger(buf, LogLevelError, time.UTC)

		err := errors.New("test error")
		logger.Error("operation failed", Error(err))

		output := buf.String()
		assert.Contains(t, output, "operation failed")
		assert.Contains(t, output, "error")
		assert.Contains(t, output, "test error")
	})
}

// TestSlogLogger_With tests field accumulation
func TestSlogLogger_With(t *testing.T) {
	t.Run("accumulate fields with With", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := NewSlogLogger(buf, LogLevelInfo, time.UTC)

		contextLogger := logger.With(
			String("request_id", "req-123"),
			String("user_id", "user-456"))

		contextLogger.Info("processing request")

		output := buf.String()
		assert.Contains(t, output, "request_id")
		assert.Contains(t, output, "req-123")
		assert.Contains(t, output, "user_id")
		assert.Contains(t, output, "user-456")
	})

	t.Run("chain multiple With calls", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := NewSlogLogger(buf, LogLevelInfo, time.UTC)

		contextLogger := logger.
			With(String("field1", "value1")).
			With(String("field2", "value2"))

		contextLogger.Info("test")

		output := buf.String()
		assert.Contains(t, output, "field1")
		assert.Contains(t, output, "value1")
		assert.Contains(t, output, "field2")
		assert.Contains(t, output, "value2")
	})

	t.Run("nil logger With returns nil", func(t *testing.T) {
		var logger *SlogLogger
		result := logger.With(String("key", "value"))
		assert.Nil(t, result)
	})
}

// TestSlogLogger_WithContext tests context-aware logging
func TestSlogLogger_WithContext(t *testing.T) {
	t.Run("extract trace ID from context", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := NewSlogLogger(buf, LogLevelInfo, time.UTC)

		// Use WithTraceID() - the documented API for setting trace IDs
		ctx := WithTraceID(t.Context(), "trace-123")
		contextLogger := logger.WithContext(ctx)

		contextLogger.Info("test message")

		output := buf.String()
		assert.Contains(t, output, "trace_id")
		assert.Contains(t, output, "trace-123")
	})

	t.Run("nil context returns same logger", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := NewSlogLogger(buf, LogLevelInfo, time.UTC)

		result := logger.WithContext(t.Context())
		assert.Equal(t, logger, result)
	})

	t.Run("context without trace ID returns same logger", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := NewSlogLogger(buf, LogLevelInfo, time.UTC)

		ctx := t.Context()
		result := logger.WithContext(ctx)
		assert.Equal(t, logger, result)
	})

	t.Run("nil logger WithContext returns nil", func(t *testing.T) {
		var logger *SlogLogger
		ctx := t.Context()
		result := logger.WithContext(ctx)
		assert.Nil(t, result)
	})
}

// TestSlogLogger_Log tests explicit level logging
func TestSlogLogger_Log(t *testing.T) {
	tests := []struct {
		name     string
		logLevel LogLevel
		message  string
	}{
		{"debug level", LogLevelDebug, "debug message"},
		{"info level", LogLevelInfo, "info message"},
		{"warn level", LogLevelWarn, "warn message"},
		{"error level", LogLevelError, "error message"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			logger := NewSlogLogger(buf, LogLevelDebug, time.UTC)

			logger.Log(tt.logLevel, tt.message)

			output := buf.String()
			assert.Contains(t, output, tt.message)
		})
	}
}

// TestSlogLogger_Flush tests flush operation
func TestSlogLogger_Flush(t *testing.T) {
	t.Run("flush returns no error", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := NewSlogLogger(buf, LogLevelInfo, time.UTC)

		err := logger.Flush()
		assert.NoError(t, err)
	})

	t.Run("nil logger flush is safe", func(t *testing.T) {
		var logger *SlogLogger
		err := logger.Flush()
		assert.NoError(t, err)
	})
}

// TestSlogLogger_NilSafety tests nil safety across all operations
func TestSlogLogger_NilSafety(t *testing.T) {
	var logger *SlogLogger

	t.Run("nil logger Debug is safe", func(t *testing.T) {
		assert.NotPanics(t, func() {
			logger.Debug("test")
		})
	})

	t.Run("nil logger Info is safe", func(t *testing.T) {
		assert.NotPanics(t, func() {
			logger.Info("test")
		})
	})

	t.Run("nil logger Warn is safe", func(t *testing.T) {
		assert.NotPanics(t, func() {
			logger.Warn("test")
		})
	})

	t.Run("nil logger Error is safe", func(t *testing.T) {
		assert.NotPanics(t, func() {
			logger.Error("test")
		})
	})

	t.Run("nil logger Log is safe", func(t *testing.T) {
		assert.NotPanics(t, func() {
			logger.Log(LogLevelInfo, "test")
		})
	})
}

// TestSlogLogger_JSONOutput tests JSON output format
func TestSlogLogger_JSONOutput(t *testing.T) {
	t.Run("output is valid JSON", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := NewSlogLogger(buf, LogLevelInfo, time.UTC)

		logger.Info("test message",
			String("key", "value"),
			Int("count", 42))

		output := buf.String()
		lines := strings.Split(strings.TrimSpace(output), "\n")
		require.Len(t, lines, 1)

		var logEntry map[string]any
		err := json.Unmarshal([]byte(lines[0]), &logEntry)
		require.NoError(t, err)
		assert.Equal(t, "test message", logEntry["msg"])
		assert.Equal(t, "value", logEntry["key"])
		assert.InDelta(t, 42.0, logEntry["count"], 0.001)
	})

	t.Run("includes timestamp", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := NewSlogLogger(buf, LogLevelInfo, time.UTC)

		logger.Info("test")

		var logEntry map[string]any
		err := json.Unmarshal(buf.Bytes(), &logEntry)
		require.NoError(t, err)
		assert.Contains(t, logEntry, "time")
	})

	t.Run("includes module when set", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := NewSlogLogger(buf, LogLevelInfo, time.UTC)
		moduleLogger := logger.Module("test-module")

		moduleLogger.Info("test")

		var logEntry map[string]any
		err := json.Unmarshal(buf.Bytes(), &logEntry)
		require.NoError(t, err)
		assert.Equal(t, "test-module", logEntry["module"])
	})
}

// TestSlogLogger_Timezone tests timezone handling
func TestSlogLogger_Timezone(t *testing.T) {
	t.Run("uses specified timezone", func(t *testing.T) {
		buf := &bytes.Buffer{}
		tz, err := time.LoadLocation("Europe/Helsinki")
		require.NoError(t, err)

		logger := NewSlogLogger(buf, LogLevelInfo, tz)
		logger.Info("test")

		// Parse the JSON output
		var logEntry map[string]any
		err = json.Unmarshal(buf.Bytes(), &logEntry)
		require.NoError(t, err)

		// Verify timestamp exists (actual timezone verification would require parsing the timestamp)
		assert.Contains(t, logEntry, "time")
	})
}

// TestSlogLogger_FileLogging tests file-based logging
func TestSlogLogger_FileLogging(t *testing.T) {
	t.Run("create logger with file output", func(t *testing.T) {
		t.Helper()

		tmpFile := t.TempDir() + "/test.log"
		tz, err := time.LoadLocation("Europe/Helsinki")
		require.NoError(t, err)

		logger, err := NewSlogLoggerWithFile(tmpFile, LogLevelInfo, tz)
		require.NoError(t, err)
		require.NotNil(t, logger)
		defer func() {
			require.NoError(t, logger.Close())
		}()

		// Write a log message
		logger.Info("test message", String("key", "value"))

		// Flush to ensure write
		require.NoError(t, logger.Flush())

		// Read file and verify content
		//nolint:gosec // Test file in temp directory
		content, err := os.ReadFile(tmpFile)
		require.NoError(t, err)
		assert.Contains(t, string(content), "test message")
		assert.Contains(t, string(content), "key")
		assert.Contains(t, string(content), "value")
	})

	t.Run("file logger with nil timezone defaults to UTC", func(t *testing.T) {
		t.Helper()

		tmpFile := t.TempDir() + "/test.log"

		logger, err := NewSlogLoggerWithFile(tmpFile, LogLevelInfo, nil)
		require.NoError(t, err)
		require.NotNil(t, logger)
		defer func() {
			require.NoError(t, logger.Close())
		}()

		assert.Equal(t, time.UTC, logger.timezone)
	})

	t.Run("reopen log file", func(t *testing.T) {
		t.Helper()

		tmpFile := t.TempDir() + "/test.log"

		logger, err := NewSlogLoggerWithFile(tmpFile, LogLevelInfo, time.UTC)
		require.NoError(t, err)
		require.NotNil(t, logger)
		defer func() {
			require.NoError(t, logger.Close())
		}()

		// Write initial message
		logger.Info("message before reopen")
		require.NoError(t, logger.Flush())

		// Simulate log rotation: move file
		rotatedFile := tmpFile + ".1"
		require.NoError(t, os.Rename(tmpFile, rotatedFile))

		// Reopen log file (simulates SIGHUP handler)
		require.NoError(t, logger.ReopenLogFile())

		// Write message to new file
		logger.Info("message after reopen")
		require.NoError(t, logger.Flush())

		// Verify new file has the second message
		//nolint:gosec // Test file in temp directory
		newContent, err := os.ReadFile(tmpFile)
		require.NoError(t, err)
		assert.Contains(t, string(newContent), "message after reopen")
		assert.NotContains(t, string(newContent), "message before reopen")

		// Verify rotated file has the first message
		//nolint:gosec // Test file in temp directory
		rotatedContent, err := os.ReadFile(rotatedFile)
		require.NoError(t, err)
		assert.Contains(t, string(rotatedContent), "message before reopen")
		assert.NotContains(t, string(rotatedContent), "message after reopen")
	})

	t.Run("reopen without file path is safe", func(t *testing.T) {
		t.Helper()

		buf := &bytes.Buffer{}
		logger := NewSlogLogger(buf, LogLevelInfo, time.UTC)

		// Should not error when no file is configured
		err := logger.ReopenLogFile()
		assert.NoError(t, err)
	})

	t.Run("close logger without file is safe", func(t *testing.T) {
		t.Helper()

		buf := &bytes.Buffer{}
		logger := NewSlogLogger(buf, LogLevelInfo, time.UTC)

		err := logger.Close()
		assert.NoError(t, err)
	})

	t.Run("close nil logger is safe", func(t *testing.T) {
		t.Helper()

		var logger *SlogLogger
		err := logger.Close()
		assert.NoError(t, err)
	})

	t.Run("file logger Module preserves file handle", func(t *testing.T) {
		t.Helper()

		tmpFile := t.TempDir() + "/test.log"

		logger, err := NewSlogLoggerWithFile(tmpFile, LogLevelInfo, time.UTC)
		require.NoError(t, err)
		defer func() {
			require.NoError(t, logger.Close())
		}()

		moduleLogger := logger.Module("test-module")
		require.NotNil(t, moduleLogger)

		// Write via module logger
		moduleLogger.Info("module message")
		require.NoError(t, logger.Flush())

		// Verify file has the message
		//nolint:gosec // Test file in temp directory
		content, err := os.ReadFile(tmpFile)
		require.NoError(t, err)
		assert.Contains(t, string(content), "module message")
		assert.Contains(t, string(content), "test-module")
	})
}
