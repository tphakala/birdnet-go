package health

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorBufferHandler_Enabled(t *testing.T) {
	t.Parallel()
	buf := NewErrorRingBuffer(10)
	h := NewErrorBufferHandler(buf, slog.LevelWarn)

	assert.False(t, h.Enabled(t.Context(), slog.LevelDebug))
	assert.False(t, h.Enabled(t.Context(), slog.LevelInfo))
	assert.True(t, h.Enabled(t.Context(), slog.LevelWarn))
	assert.True(t, h.Enabled(t.Context(), slog.LevelError))
}

func TestErrorBufferHandler_Handle(t *testing.T) {
	t.Parallel()
	buf := NewErrorRingBuffer(10)
	h := NewErrorBufferHandler(buf, slog.LevelWarn)

	now := time.Now()
	record := slog.NewRecord(now, slog.LevelError, "something broke", 0)
	record.AddAttrs(
		slog.String("module", "analysis"),
		slog.String("request_id", "abc-123"),
	)

	err := h.Handle(t.Context(), record)
	require.NoError(t, err)

	entries := buf.Entries()
	require.Len(t, entries, 1)

	entry := entries[0]
	assert.Equal(t, "error", entry.Level)
	assert.Equal(t, "something broke", entry.Message)
	assert.Equal(t, "analysis", entry.Component)
	assert.Equal(t, now, entry.Timestamp)
	assert.Equal(t, "abc-123", entry.Fields["request_id"])
}

func TestErrorBufferHandler_ComponentFromModuleKey(t *testing.T) {
	t.Parallel()
	buf := NewErrorRingBuffer(10)
	h := NewErrorBufferHandler(buf, slog.LevelWarn)

	record := slog.NewRecord(time.Now(), slog.LevelWarn, "slow query", 0)
	record.AddAttrs(slog.String("component", "database"))

	err := h.Handle(t.Context(), record)
	require.NoError(t, err)

	entries := buf.Entries()
	require.Len(t, entries, 1)
	assert.Equal(t, "database", entries[0].Component)
}

func TestErrorBufferHandler_SkipsBelowLevel(t *testing.T) {
	t.Parallel()
	buf := NewErrorRingBuffer(10)
	h := NewErrorBufferHandler(buf, slog.LevelWarn)

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "just info", 0)
	assert.False(t, h.Enabled(t.Context(), record.Level))
	assert.Equal(t, 0, buf.Count())
}

func TestErrorBufferHandler_WithAttrsAndGroup(t *testing.T) {
	t.Parallel()
	buf := NewErrorRingBuffer(10)
	h := NewErrorBufferHandler(buf, slog.LevelWarn)

	assert.Same(t, h, h.WithAttrs([]slog.Attr{slog.String("k", "v")}))
	assert.Same(t, h, h.WithGroup("group"))
}

func TestSlogLevelToString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		level    slog.Level
		expected string
	}{
		{slog.LevelDebug, "debug"},
		{slog.LevelInfo, "info"},
		{slog.LevelWarn, "warn"},
		{slog.LevelError, "error"},
		{slog.Level(12), "error"}, // above error still maps to error
		{slog.Level(-8), "debug"}, // trace level maps to debug
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, slogLevelToString(tt.level), "level=%v", tt.level)
	}
}
