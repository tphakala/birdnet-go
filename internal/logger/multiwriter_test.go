package logger

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultiWriterHandler_RespectsPerHandlerLevel(t *testing.T) {
	t.Parallel()

	t.Run("debug message goes to debug handler but not info handler", func(t *testing.T) {
		t.Parallel()

		var debugBuf, infoBuf bytes.Buffer
		debugHandler := slog.NewTextHandler(&debugBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
		infoHandler := slog.NewTextHandler(&infoBuf, &slog.HandlerOptions{Level: slog.LevelInfo})

		multi := newMultiWriterHandler(debugHandler, infoHandler)
		log := slog.New(multi)

		log.Debug("should only appear in debug output")
		log.Info("should appear in both outputs")

		assert.Contains(t, debugBuf.String(), "should only appear in debug output")
		assert.Contains(t, debugBuf.String(), "should appear in both outputs")

		assert.NotContains(t, infoBuf.String(), "should only appear in debug output")
		assert.Contains(t, infoBuf.String(), "should appear in both outputs")
	})

	t.Run("replicates issue 2938: console_also with mixed levels", func(t *testing.T) {
		t.Parallel()

		var fileBuf, consoleBuf bytes.Buffer
		fileHandler := slog.NewJSONHandler(&fileBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
		consoleHandler := newTextHandler(&consoleBuf, slog.LevelInfo, nil)

		multi := newMultiWriterHandler(fileHandler, consoleHandler)
		log := slog.New(multi)

		log.Debug("Processing detections from queue", slog.String("source", "rtsp_test"))

		assert.Contains(t, fileBuf.String(), "Processing detections from queue",
			"debug message should be written to file handler")
		assert.Empty(t, consoleBuf.String(),
			"debug message must not leak to info-level console handler")
	})

	t.Run("enabled returns true when any handler accepts the level", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer
		debugHandler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
		errorHandler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError})

		multi := newMultiWriterHandler(debugHandler, errorHandler)

		require.True(t, multi.Enabled(t.Context(), slog.LevelDebug))
		require.True(t, multi.Enabled(t.Context(), slog.LevelInfo))
		require.True(t, multi.Enabled(t.Context(), slog.LevelError))
	})

	t.Run("enabled returns false when no handler accepts the level", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer
		errorHandler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError})

		multi := newMultiWriterHandler(errorHandler)

		require.False(t, multi.Enabled(t.Context(), slog.LevelDebug))
		require.False(t, multi.Enabled(t.Context(), slog.LevelInfo))
		require.True(t, multi.Enabled(t.Context(), slog.LevelError))
	})

	t.Run("nil handler is safely skipped", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer
		handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})

		multi := newMultiWriterHandler(nil, handler, nil)
		log := slog.New(multi)

		log.Info("test message")
		assert.Contains(t, buf.String(), "test message")
	})

	t.Run("nil multiwriter handler is safe", func(t *testing.T) {
		t.Parallel()

		var h *multiWriterHandler
		require.False(t, h.Enabled(t.Context(), slog.LevelInfo))
		require.NoError(t, h.Handle(t.Context(), slog.Record{}))
	})
}
