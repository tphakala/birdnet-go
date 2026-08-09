package datastore

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/logger"
	gormlogger "gorm.io/gorm/logger"
)

// TestGormLogger_Trace_ContextCancellation verifies that context cancellation
// and deadline-exceeded query errors are logged at debug level ("Query canceled
// or timed out") and never as an error ("Database query failed"), so normal
// client disconnects, graceful shutdowns, and request timeouts do not produce
// misleading datastore error logs. The user-facing false-positive notification
// these errors used to raise is suppressed separately in the notification
// worker; this test only covers the log path.
func TestGormLogger_Trace_ContextCancellation(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "context_canceled", err: context.Canceled},
		{name: "deadline_exceeded", err: context.DeadlineExceeded},
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
			require.NoError(t, err, "failed to create test logger")

			// Restore the global logger after the subtest.
			oldGlobal := logger.Global()
			logger.SetGlobal(cl)
			t.Cleanup(func() { logger.SetGlobal(oldGlobal) })

			gLogger := NewGormLogger(200*time.Millisecond, gormlogger.Info, nil)

			// Trace selects its branch from the err argument, not the context.
			fc := func() (sql string, rowsAffected int64) {
				return "SELECT * FROM test", 0
			}

			gLogger.Trace(t.Context(), time.Now(), fc, tt.err)

			output := buf.String()
			assert.NotContains(t, output, "level=ERROR", "%s must not be logged at error level", tt.name)
			assert.NotContains(t, output, "Database query failed", "%s must not be reported as a query failure", tt.name)
			assert.Contains(t, output, "level=DEBUG", "%s must be logged at debug level", tt.name)
			assert.Contains(t, output, "Query canceled or timed out", "%s should be logged as a debug message", tt.name)
		})
	}
}
