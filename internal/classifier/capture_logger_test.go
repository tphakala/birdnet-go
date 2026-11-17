package classifier

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// newCaptureLogger swaps the process-global logger for one that writes to the
// returned buffer at debug level, restoring the previous logger on cleanup. It is
// used to assert which lines the classifier logs. Callers must NOT run in
// parallel: it mutates global state.
func newCaptureLogger(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	capture := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	cl, err := logger.NewCentralLogger(
		&logger.LoggingConfig{
			Console:       &logger.ConsoleOutput{Enabled: false},
			FileOutput:    &logger.FileOutput{Enabled: false},
			ModuleOutputs: logger.DisabledModuleOutputs(),
			DefaultLevel:  "debug",
		},
		capture,
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, cl.Close()) })
	prev := logger.Global()
	logger.SetGlobal(cl)
	t.Cleanup(func() { logger.SetGlobal(prev) })
	return &buf
}
