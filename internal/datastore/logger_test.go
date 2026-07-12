package datastore

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
	gormlogger "gorm.io/gorm/logger"
)

func TestGormLogger_Trace_ContextCanceled(t *testing.T) {
	// Intercept logs
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	cfg := &logger.LoggingConfig{
		DefaultLevel: "debug",
		Console:      &logger.ConsoleOutput{Enabled: false},
		FileOutput:   &logger.FileOutput{Enabled: false},
	}
	cl, err := logger.NewCentralLogger(cfg, handler)
	if err != nil {
		t.Fatalf("failed to create test logger: %v", err)
	}

	// Restore global logger after test
	oldGlobal := logger.Global()
	logger.SetGlobal(cl)
	defer logger.SetGlobal(oldGlobal)

	gLogger := NewGormLogger(200*time.Millisecond, gormlogger.Info, nil)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // Context is now canceled

	// Create a dummy func
	fc := func() (string, int64) {
		return "SELECT * FROM test", 0
	}

	gLogger.Trace(ctx, time.Now(), fc, context.Canceled)

	output := buf.String()
	if strings.Contains(output, "level=ERROR") || strings.Contains(output, "Database query failed") {
		t.Errorf("Expected no error log for context.Canceled, got: %s", output)
	}
	if !strings.Contains(output, "Query canceled") {
		t.Errorf("Expected debug log for canceled query, got: %s", output)
	}
}
