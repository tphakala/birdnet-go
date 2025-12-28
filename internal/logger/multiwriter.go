package logger

import (
	"context"
	"errors"
	"log/slog"
)

// multiWriterHandler writes to multiple slog handlers
type multiWriterHandler struct {
	handlers []slog.Handler
}

// newMultiWriterHandler creates a handler that writes to multiple handlers
func newMultiWriterHandler(handlers ...slog.Handler) slog.Handler {
	if handlers == nil {
		handlers = make([]slog.Handler, 0)
	}
	return &multiWriterHandler{handlers: handlers}
}

// Enabled returns true if any handler is enabled for the level
func (h *multiWriterHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if h == nil {
		return false
	}
	for _, handler := range h.handlers {
		if handler != nil && handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

// Handle sends the record to all handlers
//
//nolint:gocritic // slog.Handler interface requires record by value, not pointer
func (h *multiWriterHandler) Handle(ctx context.Context, record slog.Record) error {
	if h == nil {
		return nil
	}
	var errs []error
	for _, handler := range h.handlers {
		if handler == nil {
			continue
		}
		if err := handler.Handle(ctx, record); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// WithAttrs returns a new handler with the attributes applied to all handlers
func (h *multiWriterHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if h == nil {
		return nil
	}
	newHandlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		if handler != nil {
			newHandlers[i] = handler.WithAttrs(attrs)
		}
	}
	return &multiWriterHandler{handlers: newHandlers}
}

// WithGroup returns a new handler with the group applied to all handlers
func (h *multiWriterHandler) WithGroup(name string) slog.Handler {
	if h == nil {
		return nil
	}
	newHandlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		if handler != nil {
			newHandlers[i] = handler.WithGroup(name)
		}
	}
	return &multiWriterHandler{handlers: newHandlers}
}
