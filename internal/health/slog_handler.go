// internal/health/slog_handler.go
package health

import (
	"context"
	"log/slog"
)

// ErrorBufferHandler implements slog.Handler and feeds warn/error/fatal log
// records into an ErrorRingBuffer for use by the diagnostics health checks.
type ErrorBufferHandler struct {
	buffer *ErrorRingBuffer
	level  slog.Level
}

// NewErrorBufferHandler creates a handler that captures log records at or above
// minLevel and writes them into buffer.
func NewErrorBufferHandler(buffer *ErrorRingBuffer, minLevel slog.Level) *ErrorBufferHandler {
	return &ErrorBufferHandler{buffer: buffer, level: minLevel}
}

// Enabled reports whether the handler accepts records at the given level.
func (h *ErrorBufferHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

// Handle extracts key fields from the record and adds a LogEntry to the buffer.
//
//nolint:gocritic // slog.Handler interface requires record by value, not pointer
func (h *ErrorBufferHandler) Handle(_ context.Context, record slog.Record) error {
	entry := LogEntry{
		Level:     slogLevelToString(record.Level),
		Message:   record.Message,
		Timestamp: record.Time,
	}

	record.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case "component", "module":
			entry.Component = a.Value.String()
		default:
			if entry.Fields == nil {
				entry.Fields = make(map[string]any, 4)
			}
			entry.Fields[a.Key] = a.Value.Any()
		}
		return true
	})

	h.buffer.Add(&entry)
	return nil
}

// WithAttrs returns the handler unchanged; per-logger attributes are not
// tracked because the handler captures record-level attrs directly.
func (h *ErrorBufferHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }

// WithGroup returns the handler unchanged; grouping is not relevant for the
// error buffer use case.
func (h *ErrorBufferHandler) WithGroup(_ string) slog.Handler { return h }

// slogLevelToString converts an slog.Level to the string format expected by
// the health check log analysis (matching the LogEntry.Level field).
func slogLevelToString(level slog.Level) string {
	switch {
	case level >= slog.LevelError:
		return "error"
	case level >= slog.LevelWarn:
		return "warn"
	case level >= slog.LevelInfo:
		return "info"
	default:
		return "debug"
	}
}
