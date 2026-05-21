package events

import (
	"context"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// AppEventStore is the minimal interface the emitter needs for persistence.
// Defined here to avoid importing the full datastore package and risking cycles.
type AppEventStore interface {
	SaveAppEvent(ctx context.Context, category, eventType, message string, metadata map[string]any) error
}

// Emitter persists application events to the datastore.
// Nil-safe: calling Emit on a nil *Emitter or one with a nil store is a no-op.
type Emitter struct {
	store AppEventStore
}

// NewEmitter creates an Emitter backed by the given store.
func NewEmitter(store AppEventStore) *Emitter {
	return &Emitter{store: store}
}

// Emit records an application event. Errors are logged but never propagated.
func (e *Emitter) Emit(ctx context.Context, category, eventType, message string, metadata map[string]any) {
	if e == nil || e.store == nil {
		return
	}
	if err := e.store.SaveAppEvent(ctx, category, eventType, message, metadata); err != nil {
		logger.Global().Module("events").Warn("failed to save app event",
			logger.String("category", category),
			logger.String("event_type", eventType),
			logger.Error(err))
	}
}

var defaultEmitter atomic.Pointer[Emitter]

// SetDefault sets the global emitter (called once at startup).
func SetDefault(e *Emitter) { defaultEmitter.Store(e) }

// Default returns the global emitter, or nil if not yet set.
func Default() *Emitter { return defaultEmitter.Load() }

// Emit is a package-level convenience that delegates to the global emitter.
// No-op if SetDefault has not been called.
func Emit(ctx context.Context, category, eventType, message string, metadata map[string]any) {
	if e := Default(); e != nil {
		e.Emit(ctx, category, eventType, message, metadata)
	}
}
