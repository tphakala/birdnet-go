package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// textAttrPool provides reusable slices for slog.Attr to reduce allocations in Handle.
var textAttrPool = sync.Pool{
	New: func() any {
		s := make([]slog.Attr, 0, defaultAttrCapacity)
		return &s
	},
}

// textHandler formats logs in human-readable text format for console output.
// This handler is optimized for developer experience during local development
// and server startup, providing clear, scannable log messages without JSON clutter.
//
// Timestamps are intentionally omitted from console output following the
// Twelve-Factor App methodology. When running as a systemd service or Docker
// container, the execution environment (journald, Docker log driver) adds
// timestamps automatically. This avoids redundant timestamps like:
//
//	Dec 28 13:43:08 birdnet-go[1234]: [28.12.2025 13:43:08] INFO ...
//
// Format: LEVEL  [module] message key=value key2=value2
//
// Example output:
//
//	INFO  [main] Initializing application database=/path/to/db
//	ERROR [api] Request failed status=500 error="connection timeout"
//
// In journald, this appears as:
//
//	Dec 28 13:43:08 birdnet-go[1234]: INFO  [main] Initializing application
type textHandler struct {
	writer io.Writer
	level  slog.Level
	attrs  []slog.Attr
}

// newTextHandler creates a new text handler for human-readable console output.
// The timezone parameter is accepted for API compatibility but ignored since
// console output no longer includes timestamps.
func newTextHandler(w io.Writer, level slog.Level, _ *time.Location) *textHandler {
	return &textHandler{
		writer: w,
		level:  level,
		attrs:  nil, // nil is equivalent to empty slice but avoids allocation
	}
}

// Enabled returns true if the handler should process records at this level
func (h *textHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

// Handle processes and formats a log record for text output
//
//nolint:gocritic // slog.Handler interface requires record by value, not pointer
func (h *textHandler) Handle(_ context.Context, record slog.Record) error {
	level := record.Level.String()

	// Get attribute slice from pool (reduces allocations in hot path)
	attrsPtr := textAttrPool.Get().(*[]slog.Attr)
	extraAttrs := *attrsPtr

	// Extract module and collect other attributes
	module := ""
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == moduleKey {
			module = attr.Value.String()
		} else {
			extraAttrs = append(extraAttrs, attr)
		}
		return true
	})

	// Build message using strings.Builder with direct writes (faster than fmt.Sprintf)
	var sb strings.Builder

	// LEVEL (no timestamp - journald/Docker adds it)
	sb.WriteString(level)
	// Pad level for alignment (longest level is "ERROR" = 5 chars)
	for i := len(level); i < maxLevelWidth; i++ {
		sb.WriteByte(' ')
	}

	// [module] if set
	if module != "" {
		sb.WriteString(" [")
		sb.WriteString(module)
		sb.WriteByte(']')
	}

	// message
	sb.WriteByte(' ')
	sb.WriteString(record.Message)

	// Add attributes as key=value pairs
	for _, attr := range h.attrs {
		writeAttr(&sb, attr)
	}
	for _, attr := range extraAttrs {
		writeAttr(&sb, attr)
	}

	sb.WriteByte('\n')

	// Return slice to pool
	*attrsPtr = extraAttrs[:0]
	textAttrPool.Put(attrsPtr)

	// Use io.WriteString to avoid allocation if writer implements io.StringWriter
	_, err := io.WriteString(h.writer, sb.String())
	return err
}

// writeAttr writes a single attribute to the builder
func writeAttr(sb *strings.Builder, attr slog.Attr) {
	sb.WriteByte(' ')
	sb.WriteString(attr.Key)
	sb.WriteByte('=')
	writeAttrValue(sb, attr)
}

// writeAttrValue writes an attribute value to the builder
func writeAttrValue(sb *strings.Builder, attr slog.Attr) {
	value := attr.Value.Any()
	switch v := value.(type) {
	case string:
		// Quote strings if they contain spaces
		if strings.ContainsAny(v, " \t\n\r") {
			sb.WriteString(strconv.Quote(v))
		} else {
			sb.WriteString(v)
		}
	case int:
		sb.WriteString(strconv.Itoa(v))
	case int64:
		sb.WriteString(strconv.FormatInt(v, 10))
	case bool:
		sb.WriteString(strconv.FormatBool(v))
	case time.Time:
		// Format time in ISO 8601 for consistency
		sb.WriteString(v.Format(time.RFC3339))
	case time.Duration:
		sb.WriteString(v.String())
	default:
		fmt.Fprintf(sb, "%v", v)
	}
}

// WithAttrs returns a new handler with accumulated attributes
func (h *textHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &textHandler{
		writer: h.writer,
		level:  h.level,
		attrs:  slices.Concat(h.attrs, attrs),
	}
}

// WithGroup returns a new handler with a group name prefix
// Groups are not implemented for text output - returns same handler
func (h *textHandler) WithGroup(_ string) slog.Handler {
	return h
}
