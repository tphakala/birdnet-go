package jobqueue

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// TestGetLog tests the getLog function returns a valid logger
func TestGetLog(t *testing.T) {
	log := getLog()
	require.NotNil(t, log, "getLog returned nil")
}

// TestLogJobEnqueued tests job enqueue logging doesn't panic
func TestLogJobEnqueued(t *testing.T) {
	ctx := WithTraceID(t.Context(), "trace-123")

	// Should not panic
	assert.NotPanics(t, func() {
		LogJobEnqueued(ctx, "job-123", "process", true)
	})

	// Test without trace ID
	assert.NotPanics(t, func() {
		LogJobEnqueued(t.Context(), "job-124", "analyze", false)
	})
}

// TestLogJobStarted tests job start logging doesn't panic
func TestLogJobStarted(t *testing.T) {
	assert.NotPanics(t, func() {
		LogJobStarted(t.Context(), "job-456", "analyze")
	})
}

// TestLogJobCompleted tests job completion logging doesn't panic
func TestLogJobCompleted(t *testing.T) {
	duration := 150 * time.Millisecond

	assert.NotPanics(t, func() {
		LogJobCompleted(t.Context(), "job-789", "upload", duration)
	})
}

// TestLogJobFailed pins the level and message, not just the absence of a panic.
// The whole point of the change here is that the line is unconditionally an
// Error reading "Job failed permanently"; a NotPanics-only assertion would stay
// green if it regressed to Warn or to the old retryable wording.
//
// Only the exhausted-attempts shape is exercised, because that is the only one
// handleJobFailure ever produces; see the doc comment on LogJobFailed.
func TestLogJobFailed(t *testing.T) {
	// Not parallel: swaps the process-global logger, same shape as
	// TestLogJobRetryScheduled_Level below.
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	cl, err := logger.NewCentralLogger(&logger.LoggingConfig{
		DefaultLevel: "debug",
		Console:      &logger.ConsoleOutput{Enabled: false},
		FileOutput:   &logger.FileOutput{Enabled: false},
	}, handler)
	require.NoError(t, err)
	oldGlobal := logger.Global()
	logger.SetGlobal(cl)
	t.Cleanup(func() { logger.SetGlobal(oldGlobal) })

	testErr := errors.New("connection timeout")
	assert.NotPanics(t, func() {
		LogJobFailed(t.Context(), "job-1000", "process", 5, 5, testErr)
	})

	out := buf.String()
	assert.Contains(t, out, "Job failed permanently")
	assert.Contains(t, out, "level=ERROR",
		"a permanently failed job must be an Error, not a Warn")
	assert.NotContains(t, out, "will retry",
		"the retryable wording is unreachable and must not come back")
}

// TestLogQueueStats tests queue statistics logging doesn't panic
func TestLogQueueStats(t *testing.T) {
	assert.NotPanics(t, func() {
		LogQueueStats(t.Context(), 10, 3, 50, 2)
	})
}

// TestLogJobDropped tests job dropped logging doesn't panic
func TestLogJobDropped(t *testing.T) {
	assert.NotPanics(t, func() {
		LogJobDropped(t.Context(), "job-dropped-1", "Upload to BirdWeather")
	})
}

// TestLogQueueStopped tests queue stopped logging doesn't panic
func TestLogQueueStopped(t *testing.T) {
	// Test with key-value pairs
	assert.NotPanics(t, func() {
		LogQueueStopped(t.Context(), "manual shutdown", "pending_jobs", 5)
	})

	// Test with odd number of details (edge case)
	assert.NotPanics(t, func() {
		LogQueueStopped(t.Context(), "error shutdown", "odd_key")
	})

	// Test with no details
	assert.NotPanics(t, func() {
		LogQueueStopped(t.Context(), "clean shutdown")
	})
}

// TestLogJobRetrying tests job retry logging doesn't panic
func TestLogJobRetrying(t *testing.T) {
	assert.NotPanics(t, func() {
		LogJobRetrying(t.Context(), "job-retry-1", "Send MQTT message", 2, 5)
	})
}

// TestLogJobRetryScheduled tests job retry scheduling logging doesn't panic
func TestLogJobRetryScheduled(t *testing.T) {
	nextRetryAt := time.Now().Add(30 * time.Second)
	delay := 30 * time.Second
	testErr := errors.New("connection timeout")

	assert.NotPanics(t, func() {
		LogJobRetryScheduled(t.Context(), "job-retry-sched-1", "HTTP POST request", 2, 5, delay, nextRetryAt, testErr)
	})

	// A deferred action wraps ErrJobDeferred and must take the Debug branch instead of
	// the Warn branch; exercise it so the classification code path is covered.
	deferredErr := fmt.Errorf("audio export deferred: %w", ErrJobDeferred)
	require.ErrorIs(t, deferredErr, ErrJobDeferred, "wrapped deferral must satisfy errors.Is")
	assert.NotPanics(t, func() {
		LogJobRetryScheduled(t.Context(), "job-retry-sched-2", "Save audio clip to file", 2, 10, delay, nextRetryAt, deferredErr)
	})
}

// TestLogJobRetryScheduled_Level asserts the classification OUTCOME (not just that the
// branch runs): an intentional deferral (wrapping ErrJobDeferred) logs at Debug, a
// genuine failure at Warn. A condition inversion in LogJobRetryScheduled would pass the
// NotPanics test above but is caught here by capturing the emitted level.
func TestLogJobRetryScheduled_Level(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantLevel string
		wantMsg   string
	}{
		{
			name:      "deferral logs at debug",
			err:       fmt.Errorf("audio export deferred: %w", ErrJobDeferred),
			wantLevel: "level=DEBUG",
			wantMsg:   "Job deferred, retry scheduled",
		},
		{
			name:      "genuine failure logs at warn",
			err:       errors.New("connection refused"),
			wantLevel: "level=WARN",
			wantMsg:   "Job scheduled for retry after failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Not parallel: swaps the process-global logger for the duration.
			var buf bytes.Buffer
			handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
			cfg := &logger.LoggingConfig{
				DefaultLevel: "debug",
				Console:      &logger.ConsoleOutput{Enabled: false},
				FileOutput:   &logger.FileOutput{Enabled: false},
			}
			cl, err := logger.NewCentralLogger(cfg, handler)
			require.NoError(t, err)

			oldGlobal := logger.Global()
			logger.SetGlobal(cl)
			t.Cleanup(func() { logger.SetGlobal(oldGlobal) })

			LogJobRetryScheduled(t.Context(), "job-lvl", "Save audio clip to file", 2, 10,
				3*time.Second, time.Now().Add(3*time.Second), tt.err)

			output := buf.String()
			assert.Contains(t, output, tt.wantLevel, "wrong log level for %s", tt.name)
			assert.Contains(t, output, tt.wantMsg, "wrong log message for %s", tt.name)
		})
	}
}

// TestLogJobSuccess tests job success logging doesn't panic
func TestLogJobSuccess(t *testing.T) {
	// Test first attempt success
	assert.NotPanics(t, func() {
		LogJobSuccess(t.Context(), "job-success-1", "Save to database", 1)
	})

	// Test retry success
	assert.NotPanics(t, func() {
		LogJobSuccess(t.Context(), "job-success-2", "Upload after retry", 3)
	})
}

// TestWithTraceID tests trace ID context functions
func TestWithTraceID(t *testing.T) {
	ctx := t.Context()

	// Add trace ID
	ctx = WithTraceID(ctx, "trace-123")

	// Extract and verify
	traceID := extractTraceID(ctx)
	assert.Equal(t, "trace-123", traceID, "Expected trace ID 'trace-123'")
}

// TestExtractTraceID tests trace ID extraction from context
func TestExtractTraceID(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		expected string
	}{
		{
			name:     "nil context",
			ctx:      nil,
			expected: "",
		},
		{
			name:     "empty context",
			ctx:      t.Context(),
			expected: "",
		},
		{
			name:     "context with trace ID",
			ctx:      WithTraceID(t.Context(), "trace-456"),
			expected: "trace-456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractTraceID(tt.ctx)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// stringerID is a test type that implements fmt.Stringer
type stringerID struct{ id string }

func (s stringerID) String() string { return s.id }

// TestExtractTraceIDWithStringer tests trace ID extraction with fmt.Stringer type
func TestExtractTraceIDWithStringer(t *testing.T) {
	ctx := context.WithValue(t.Context(), contextKeyTraceID, stringerID{"stringer-trace"})
	result := extractTraceID(ctx)
	assert.Equal(t, "stringer-trace", result)
}
