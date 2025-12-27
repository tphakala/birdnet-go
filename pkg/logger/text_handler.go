package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"
)

// textHandler formats logs in human-readable text format for console output.
// This handler is optimized for developer experience during local development
// and server startup, providing clear, scannable log messages without JSON clutter.
//
// Format: [YYYY-MM-DD HH:MM:SS] LEVEL  [module] message key=value key2=value2
//
// Example output:
//
//	[2025-11-17 09:59:51] INFO  [main] Initializing application database=/path/to/db
//	[2025-11-17 09:59:52] ERROR [api] Request failed status=500 error="connection timeout"
type textHandler struct {
	writer   io.Writer
	level    slog.Level
	timezone *time.Location
	attrs    []slog.Attr
}

// newTextHandler creates a new text handler for human-readable console output
func newTextHandler(w io.Writer, level slog.Level, tz *time.Location) *textHandler {
	if tz == nil {
		tz = time.UTC
	}
	return &textHandler{
		writer:   w,
		level:    level,
		timezone: tz,
		attrs:    make([]slog.Attr, 0),
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
	// Format timestamp in Finnish locale (DD.MM.YYYY HH:MM:SS) for consistency
	timestamp := record.Time.In(h.timezone).Format("02.01.2006 15:04:05")
	level := record.Level.String()

	// Extract module and collect other attributes
	module := ""
	extraAttrs := make([]slog.Attr, 0, record.NumAttrs())
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "module" {
			module = attr.Value.String()
		} else {
			extraAttrs = append(extraAttrs, attr)
		}
		return true
	})

	// Build message using strings.Builder with direct writes (faster than fmt.Sprintf)
	var sb strings.Builder

	// [timestamp] LEVEL
	sb.WriteByte('[')
	sb.WriteString(timestamp)
	sb.WriteString("] ")
	sb.WriteString(level)
	// Pad level to 5 chars for alignment
	for i := len(level); i < 5; i++ {
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
	_, err := h.writer.Write([]byte(sb.String()))
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
		// Format time in Finnish locale
		sb.WriteString(v.Format("02.01.2006 15:04:05"))
	case time.Duration:
		sb.WriteString(v.String())
	default:
		fmt.Fprintf(sb, "%v", v)
	}
}

// WithAttrs returns a new handler with accumulated attributes
func (h *textHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &textHandler{
		writer:   h.writer,
		level:    h.level,
		timezone: h.timezone,
		attrs:    slices.Concat(h.attrs, attrs),
	}
}

// WithGroup returns a new handler with a group name prefix
// Groups are not implemented for text output - returns same handler
func (h *textHandler) WithGroup(_ string) slog.Handler {
	return h
}
