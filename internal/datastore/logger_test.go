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

// TestGormLogger_Trace_ContextCanceled verifies that a context.Canceled query
// error is logged at debug level ("Query canceled") and never as an error
// ("Database query failed"), so normal client disconnects and graceful
// shutdowns do not produce misleading datastore error logs. The user-facing
// false-positive notification these errors used to raise is suppressed
// separately in the notification worker; this test only covers the log path.
func TestGormLogger_Trace_ContextCanceled(t *testing.T) {
	// Intercept logs written by the datastore module logger.
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	cfg := &logger.LoggingConfig{
		DefaultLevel: "debug",
		Console:      &logger.ConsoleOutput{Enabled: false},
		FileOutput:   &logger.FileOutput{Enabled: false},
	}
	cl, err := logger.NewCentralLogger(cfg, handler)
	require.NoError(t, err, "failed to create test logger")

	// Restore the global logger after the test.
	oldGlobal := logger.Global()
	logger.SetGlobal(cl)
	t.Cleanup(func() { logger.SetGlobal(oldGlobal) })

	gLogger := NewGormLogger(200*time.Millisecond, gormlogger.Info, nil)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // Context is now canceled.

	fc := func() (sql string, rowsAffected int64) {
		return "SELECT * FROM test", 0
	}

	gLogger.Trace(ctx, time.Now(), fc, context.Canceled)

	output := buf.String()
	assert.NotContains(t, output, "level=ERROR", "context.Canceled must not be logged at error level")
	assert.NotContains(t, output, "Database query failed", "context.Canceled must not be reported as a query failure")
	assert.Contains(t, output, "Query canceled", "context.Canceled should be logged as a debug message")
}
