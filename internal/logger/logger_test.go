package logger

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime"
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

// TestFieldToAttr_NonFiniteFloats verifies that non-finite float fields are
// converted to symbolic string attrs (which slog's JSON handler can encode)
// rather than raw float attrs (which it cannot), while finite floats keep the
// rounded numeric form.
func TestFieldToAttr_NonFiniteFloats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		field     Field
		wantKind  slog.Kind
		wantStr   string  // expected when wantKind is KindString
		wantFloat float64 // expected when wantKind is KindFloat64
	}{
		{"float64 -inf", Float64("k", math.Inf(-1)), slog.KindString, "-Inf", 0},
		{"float64 +inf", Float64("k", math.Inf(1)), slog.KindString, "+Inf", 0},
		{"float64 nan", Float64("k", math.NaN()), slog.KindString, "NaN", 0},
		{"float64 finite rounds", Float64("k", 1.23456), slog.KindFloat64, "", 1.235},
		// A finite magnitude whose rounding overflows to ±Inf must still log as a
		// numeric attr (the raw value), never a symbolic string.
		{"float64 max stays numeric", Float64("k", math.MaxFloat64), slog.KindFloat64, "", math.MaxFloat64},
		{"float64 -max stays numeric", Float64("k", -math.MaxFloat64), slog.KindFloat64, "", -math.MaxFloat64},
		{"float32 -inf", Float32("k", float32(math.Inf(-1))), slog.KindString, "-Inf", 0},
		{"float32 +inf", Float32("k", float32(math.Inf(1))), slog.KindString, "+Inf", 0},
		{"float32 nan", Float32("k", float32(math.NaN())), slog.KindString, "NaN", 0},
		{"float32 finite", Float32("k", 2.5), slog.KindFloat64, "", 2.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			attr := fieldToAttr(tt.field)
			require.Equal(t, tt.wantKind, attr.Value.Kind(), "attr kind for %s", tt.name)
			switch tt.wantKind {
			case slog.KindString:
				assert.Equal(t, tt.wantStr, attr.Value.String())
			case slog.KindFloat64:
				assert.InDelta(t, tt.wantFloat, attr.Value.Float64(), 1e-9)
			default:
				t.Fatalf("unexpected wantKind %v", tt.wantKind)
			}
		})
	}
}

// TestSlogLogger_NonFiniteFloatJSON is the end-to-end guard: a non-finite float
// field must produce a valid JSON log line with a readable value, never slog's
// "!ERROR:json: unsupported value" substitution.
func TestSlogLogger_NonFiniteFloatJSON(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	log := NewSlogLogger(buf, LogLevelInfo, time.UTC)
	log.Info("loudness",
		Float64("neg_inf", math.Inf(-1)),
		Float64("pos_inf", math.Inf(1)),
		Float64("nan", math.NaN()),
		Float64("finite", 1.5),
		Float64("huge", math.MaxFloat64),
	)

	out := buf.String()
	assert.NotContains(t, out, "!ERROR", "non-finite float corrupted the log line: %s", out)

	var m map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m), "log line must be valid JSON: %s", out)
	assert.Equal(t, "-Inf", m["neg_inf"])
	assert.Equal(t, "+Inf", m["pos_inf"])
	assert.Equal(t, "NaN", m["nan"])
	assert.InDelta(t, 1.5, m["finite"], 1e-9)
	// A large finite value whose rounding overflows stays an encodable JSON number.
	assert.InDelta(t, math.MaxFloat64, m["huge"], 1e292)
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
		timeStr, ok := logEntry["time"].(string)
		require.True(t, ok)
		parsedTime, err := time.Parse(time.RFC3339Nano, timeStr)
		require.NoError(t, err)

		_, offset := parsedTime.Zone()
		_, expectedOffset := parsedTime.In(tz).Zone()
		assert.Equal(t, expectedOffset, offset)
	})
}

// TestSlogLogger_FileLogging tests file-based logging
func TestSlogLogger_FileLogging(t *testing.T) {
	t.Run("create logger with file output", func(t *testing.T) {
		t.Helper()

		tmpFile := filepath.Join(t.TempDir(), "test.log")
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

		tmpFile := filepath.Join(t.TempDir(), "test.log")

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

		if runtime.GOOS == "windows" {
			t.Skip("SIGHUP-based log rotation is a Unix-only pattern; Windows does not allow renaming open files")
		}

		tmpFile := filepath.Join(t.TempDir(), "test.log")

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

		tmpFile := filepath.Join(t.TempDir(), "test.log")

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

// type Decibels float64 for testing named float type
type Decibels float64

type mockJSONMarshaler struct{}

func (mockJSONMarshaler) MarshalJSON() ([]byte, error) { return []byte(`"mock"`), nil }

func TestSanitizeAny(t *testing.T) {
	t.Parallel()

	//nolint:testifylint // Intentionally strict typing for our float return tests
	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, sanitizeAny(nil))
	})

	t.Run("basic types", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, 42, sanitizeAny(42))
		assert.Equal(t, "test", sanitizeAny("test"))
		assert.Equal(t, true, sanitizeAny(true))
	})

	t.Run("finite floats", func(t *testing.T) {
		t.Parallel()
		if v := sanitizeAny(1.23); v != 1.23 {
			t.Errorf("expected 1.23, got %v", v)
		}
		if v := sanitizeAny(float32(1.23)); v != float32(1.23) {
			t.Errorf("expected float32 1.23, got %v", v)
		}
		if v := sanitizeAny(Decibels(1.23)); v != Decibels(1.23) {
			t.Errorf("expected Decibels 1.23, got %v", v)
		}
	})

	t.Run("non-finite floats", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "+Inf", sanitizeAny(math.Inf(1)))
		assert.Equal(t, "-Inf", sanitizeAny(math.Inf(-1)))
		assert.Equal(t, "NaN", sanitizeAny(math.NaN()))
		assert.Equal(t, "NaN", sanitizeAny(float32(math.NaN())))
		assert.Equal(t, "+Inf", sanitizeAny(float32(math.Inf(1))))
	})

	t.Run("named non-finite floats", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "+Inf", sanitizeAny(Decibels(math.Inf(1))))
		assert.Equal(t, "-Inf", sanitizeAny(Decibels(math.Inf(-1))))
	})

	t.Run("nested slice", func(t *testing.T) {
		t.Parallel()
		input := []any{1.23, math.Inf(1), "test"}
		got := sanitizeAny(input)
		expected := []any{1.23, "+Inf", "test"}
		assert.Equal(t, expected, got)
	})

	t.Run("nested map", func(t *testing.T) {
		t.Parallel()
		input := map[string]any{"a": math.Inf(-1), "b": 1.23}
		got := sanitizeAny(input)
		expected := map[string]any{"a": "-Inf", "b": 1.23}
		assert.Equal(t, expected, got)
	})

	t.Run("nested struct with tags and uncomparable types", func(t *testing.T) {
		t.Parallel()
		type Nested struct {
			Val     float64 `json:"val"`
			Ok      bool    `json:"ok"`
			Ignored int     `json:"-"`
			Slice   []int   `json:"slice"` // Uncomparable
		}
		input := Nested{Val: math.NaN(), Ok: true, Ignored: 10, Slice: []int{1, 2}}
		got := sanitizeAny(input)
		expected := map[string]any{"val": "NaN", "ok": true, "slice": []int{1, 2}}
		assert.Equal(t, expected, got)
	})

	t.Run("cyclic data structure", func(t *testing.T) {
		t.Parallel()
		// map pointing to itself
		m := make(map[string]any)
		m["self"] = m
		m["val"] = math.NaN()

		got := sanitizeAny(m)
		// The cycle is broken with a "[Circular]" placeholder (not the original
		// self-referential map), and the sibling non-finite float is sanitized.
		resMap, ok := got.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "NaN", resMap["val"])
		assert.Equal(t, "[Circular]", resMap["self"])
		// The whole result must be JSON-encodable (no residual cycle or NaN).
		_, err := json.Marshal(resMap)
		require.NoError(t, err)
	})

	t.Run("delegates to json.Marshaler", func(t *testing.T) {
		t.Parallel()
		input := []any{math.NaN(), mockJSONMarshaler{}}
		got := sanitizeAny(input)
		expected := []any{"NaN", mockJSONMarshaler{}}
		assert.Equal(t, expected, got)
	})

	t.Run("clean struct is returned unchanged (not flattened)", func(t *testing.T) {
		t.Parallel()
		type Clean struct {
			A int    `json:"a"`
			B string `json:"b"`
		}
		in := Clean{A: 1, B: "x"}
		// No non-finite float: the value must pass through untouched so slog
		// serializes it with real encoding/json semantics.
		assert.Equal(t, in, sanitizeAny(in))
	})

	t.Run("clean composites pass through by identity", func(t *testing.T) {
		t.Parallel()
		s := []int{1, 2, 3}
		assert.Equal(t, s, sanitizeAny(s))
		m := map[string]int{"a": 1}
		assert.Equal(t, m, sanitizeAny(m))
	})

	t.Run("array with non-finite float", func(t *testing.T) {
		t.Parallel()
		got := sanitizeAny([3]any{1.23, math.Inf(1), "x"})
		assert.Equal(t, []any{1.23, "+Inf", "x"}, got)
	})

	t.Run("map with non-string keys", func(t *testing.T) {
		t.Parallel()
		got := sanitizeAny(map[int]float64{1: math.NaN(), 2: 3.0})
		resMap, ok := got.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "NaN", resMap["1"])
		assert.InEpsilon(t, 3.0, resMap["2"], 1e-9)
	})

	t.Run("pointer to struct with non-finite float", func(t *testing.T) {
		t.Parallel()
		type P struct {
			V float64 `json:"v"`
		}
		got := sanitizeAny(&P{V: math.NaN()})
		requireJSONSafe(t, got)
	})
}

// --- Helpers and types for sanitizeAny corruption/fuzz tests ---

// requireJSONSafe asserts a value can be JSON-encoded without a non-finite-float
// error (the corruption sanitizeAny exists to prevent). Unsupported *types*
// (chan/func) are out of scope and tolerated.
func requireJSONSafe(t *testing.T, v any) {
	t.Helper()
	_, err := json.Marshal(v)
	_, isValueErr := errors.AsType[*json.UnsupportedValueError](err)
	require.Falsef(t, isValueErr, "sanitized value must not carry a non-finite float: %v", err)
}

// logDataField renders v through the real slog JSON handler exactly like
// fieldToAttr's default case does, and returns the emitted line.
func logDataField(t *testing.T, v any) string {
	t.Helper()
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("m", slog.Any("data", sanitizeAny(v)))
	return buf.String()
}

func assertNoCorruption(t *testing.T, line string) {
	t.Helper()
	assert.NotContains(t, line, "!ERROR", "log line must not contain a marshal-error placeholder")
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(line), &m), "log line must be valid JSON")
}

// stringerWithFloat implements fmt.Stringer and carries a float field. Under the
// JSON handler slog ignores String() and marshals the fields, so a non-finite
// float here must still be sanitized.
type stringerWithFloat struct {
	Gain float64
}

func (stringerWithFloat) String() string { return "src" }

// floatFieldError implements error and carries a float field.
type floatFieldError struct {
	Gain float64
}

func (floatFieldError) Error() string { return "boom" }

// innerFloat is an unexported type whose exported field is promoted into
// outerEmbed, mirroring encoding/json's promotion of unexported embedded structs.
type innerFloat struct {
	Y float64
}

type outerEmbed struct {
	innerFloat
	Name string
}

// fuzzStruct is a small mixed struct used by FuzzSanitizeAny.
type fuzzStruct struct {
	V float64
	S string
}

// TestSanitizeAny_NonFiniteBehindInterfaces covers the classes the reflective
// walk cannot reach on its own (values behind fmt.Stringer/error, or promoted
// from an unexported embedded struct): the safety net must still keep the log
// line uncorrupted.
func TestSanitizeAny_NonFiniteBehindInterfaces(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		val  any
	}{
		{"top-level Stringer with NaN", stringerWithFloat{Gain: math.NaN()}},
		{"nested Stringer with NaN", map[string]any{"src": stringerWithFloat{Gain: math.NaN()}, "trigger": math.Inf(1)}},
		{"top-level error with Inf", floatFieldError{Gain: math.Inf(1)}},
		{"nested error with NaN", map[string]any{"err": floatFieldError{Gain: math.NaN()}, "trigger": math.Inf(1)}},
		{"unexported embedded struct with NaN", outerEmbed{innerFloat: innerFloat{Y: math.NaN()}, Name: "x"}},
		{"pointer to Stringer with NaN", &stringerWithFloat{Gain: math.NaN()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			requireJSONSafe(t, sanitizeAny(tt.val))
			assertNoCorruption(t, logDataField(t, tt.val))
		})
	}
}

// TestSanitizeAny_MatchesEncodingJSONForCleanValues pins the "no schema drift"
// property: a value with no non-finite float must marshal identically to what
// encoding/json produces for the original.
func TestSanitizeAny_MatchesEncodingJSONForCleanValues(t *testing.T) {
	t.Parallel()
	type Inner struct {
		A int    `json:"a"`
		B string `json:"b,omitempty"`
	}
	type Outer struct {
		Inner
		Name  string  `json:"name"`
		Ptr   *int    `json:"ptr,omitempty"`
		Score float64 `json:"score"`
	}
	cases := []any{
		Outer{Inner: Inner{A: 1}, Name: "x", Score: 2.5},
		[]any{1, "two", 3.5, true},
		map[string]any{"a": 1, "b": []int{1, 2}},
		Inner{A: 7, B: "z"},
	}
	for i, c := range cases {
		want, err := json.Marshal(c)
		require.NoError(t, err)
		got, err := json.Marshal(sanitizeAny(c))
		require.NoErrorf(t, err, "case %d", i)
		assert.JSONEqf(t, string(want), string(got), "case %d", i)
	}
}

// FuzzSanitizeAny asserts the two load-bearing invariants over generated input:
// sanitizeAny never panics, and its output never fails to JSON-encode because of
// a non-finite float.
func FuzzSanitizeAny(f *testing.F) {
	f.Add([]byte("hello"), 3, 1.5)
	f.Add([]byte{}, 0, math.NaN())
	f.Add([]byte("x"), -1, math.Inf(1))
	f.Fuzz(func(t *testing.T, b []byte, n int, x float64) {
		inputs := []any{
			x,
			float32(x),
			Decibels(x),
			[]any{x, string(b), n},
			map[string]any{"x": x, "s": string(b)},
			map[int]float64{n: x},
			fuzzStruct{V: x, S: string(b)},
			stringerWithFloat{Gain: x},
			floatFieldError{Gain: x},
			outerEmbed{innerFloat: innerFloat{Y: x}, Name: string(b)},
			&fuzzStruct{V: x, S: string(b)},
			[]float64{x, float64(n)},
		}
		for i, in := range inputs {
			out := sanitizeAny(in) // must not panic
			_, err := json.Marshal(out)
			if _, ok := errors.AsType[*json.UnsupportedValueError](err); ok {
				t.Fatalf("input %d (%T) still fails to encode on a non-finite float: %v", i, in, err)
			}
		}
	})
}
