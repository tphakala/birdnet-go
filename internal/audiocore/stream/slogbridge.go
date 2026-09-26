package stream

import (
	"context"
	"log/slog"
	"slices"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// debugSlog returns an *slog.Logger that bridges go-audio-stream's library
// logging onto the audiocore logger, or nil when debug is disabled so the library
// stays quiet. It is the replacement for the Options.Debug field dropped in the
// Phase 2 merge: without it the native producer ignores StreamSpec.Debug and the
// library's reconnect and warning records never surface.
func debugSlog(debug bool, log logger.Logger) *slog.Logger {
	if !debug || log == nil {
		return nil
	}
	return slog.New(&slogBridge{log: log})
}

// slogBridge is an slog.Handler that forwards records onto a logger.Logger, so
// the go-audio-stream library's diagnostics flow through the same structured
// logger the rest of the stream package uses. It is always enabled: gating
// happens at construction (debugSlog returns nil when debug is off), so the
// library never receives a handler unless debug logging was requested.
type slogBridge struct {
	log    logger.Logger
	attrs  []logger.Field
	prefix string
}

// Enabled reports handler availability. The bridge is only constructed when debug
// logging is on, so it accepts every level and lets the audiocore logger apply
// its own level policy.
func (b *slogBridge) Enabled(_ context.Context, _ slog.Level) bool { return true }

// Handle forwards one record, mapping the slog level onto the logger's leveled
// methods and carrying both the accumulated and per-record attributes as fields.
func (b *slogBridge) Handle(_ context.Context, r slog.Record) error { //nolint:gocritic // hugeParam: slog.Handler.Handle signature is fixed by the interface
	fields := make([]logger.Field, 0, len(b.attrs)+r.NumAttrs())
	fields = append(fields, b.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		fields = append(fields, b.field(a))
		return true
	})
	switch {
	case r.Level >= slog.LevelError:
		b.log.Error(r.Message, fields...)
	case r.Level >= slog.LevelWarn:
		b.log.Warn(r.Message, fields...)
	case r.Level >= slog.LevelInfo:
		b.log.Info(r.Message, fields...)
	default:
		b.log.Debug(r.Message, fields...)
	}
	return nil
}

// WithAttrs returns a handler that prepends attrs to every subsequent record.
func (b *slogBridge) WithAttrs(attrs []slog.Attr) slog.Handler {
	nb := &slogBridge{log: b.log, prefix: b.prefix, attrs: slices.Clone(b.attrs)}
	for i := range attrs {
		nb.attrs = append(nb.attrs, nb.field(attrs[i]))
	}
	return nb
}

// WithGroup returns a handler that dot-prefixes subsequent attribute keys with
// the group name.
func (b *slogBridge) WithGroup(name string) slog.Handler {
	if name == "" {
		return b
	}
	return &slogBridge{log: b.log, prefix: b.prefix + name + ".", attrs: slices.Clone(b.attrs)}
}

// field converts one slog attribute into a logger.Field, applying the current
// group prefix to the key.
func (b *slogBridge) field(a slog.Attr) logger.Field {
	return logger.Any(b.prefix+a.Key, a.Value.Any())
}
