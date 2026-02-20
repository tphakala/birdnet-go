package alerting

import (
	"sync"
	"time"
)

// AlertEvent represents an event that can trigger alert rules.
type AlertEvent struct {
	ObjectType string
	EventName  string         // For event triggers (e.g., "stream.disconnected")
	MetricName string         // For metric triggers (e.g., "system.cpu_usage")
	Properties map[string]any // Event-specific properties for condition evaluation
	Timestamp  time.Time
}

// AlertEventHandler processes alert events.
type AlertEventHandler func(event *AlertEvent)

// Package-level singleton for the alert event bus.
var (
	globalAlertBus *AlertEventBus
	alertBusMu     sync.RWMutex
)

// SetGlobalBus sets the package-level alert event bus singleton.
// Called during initialization.
func SetGlobalBus(bus *AlertEventBus) {
	alertBusMu.Lock()
	defer alertBusMu.Unlock()
	globalAlertBus = bus
}

// GetGlobalBus returns the package-level alert event bus, or nil if not initialized.
func GetGlobalBus() *AlertEventBus {
	alertBusMu.RLock()
	defer alertBusMu.RUnlock()
	return globalAlertBus
}

// TryPublish publishes an event to the global alert bus if initialized.
// Returns false if the bus is not yet available.
func TryPublish(event *AlertEvent) bool {
	bus := GetGlobalBus()
	if bus == nil {
		return false
	}
	bus.Publish(event)
	return true
}

const (
	// eventBusBufferSize is the capacity of the async event channel.
	// Events are dropped if the buffer is full to avoid blocking callers.
	eventBusBufferSize = 1000
)

// AlertEventBus is an async pub/sub for alert events. Publish is non-blocking:
// events are sent to a buffered channel and processed by a worker goroutine,
// so callers (stream managers, monitors) are never blocked by DB writes or
// notification dispatch.
type AlertEventBus struct {
	handlers []AlertEventHandler
	mu       sync.RWMutex
	eventCh  chan *AlertEvent
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewAlertEventBus creates a new alert event bus and starts its worker.
func NewAlertEventBus() *AlertEventBus {
	b := &AlertEventBus{
		handlers: make([]AlertEventHandler, 0),
		eventCh:  make(chan *AlertEvent, eventBusBufferSize),
		stopCh:   make(chan struct{}),
	}
	go b.processLoop()
	return b
}

// Subscribe registers a handler for alert events.
func (b *AlertEventBus) Subscribe(handler AlertEventHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers = append(b.handlers, handler)
}

// Publish enqueues an event for async processing. Non-blocking: if the buffer
// is full the event is dropped to protect callers on hot paths.
// Events are silently dropped after Stop() has been called.
func (b *AlertEventBus) Publish(event *AlertEvent) {
	select {
	case <-b.stopCh:
		return // Bus is stopped, discard event
	default:
	}

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	select {
	case b.eventCh <- event:
	default:
		// Buffer full — drop event to avoid blocking callers
	}
}

// Stop shuts down the worker goroutine. Safe to call multiple times.
func (b *AlertEventBus) Stop() {
	b.stopOnce.Do(func() {
		close(b.stopCh)
	})
}

// processLoop drains the event channel and dispatches to handlers.
func (b *AlertEventBus) processLoop() {
	for {
		select {
		case event := <-b.eventCh:
			b.dispatch(event)
		case <-b.stopCh:
			// Drain remaining events before exiting
			for {
				select {
				case event := <-b.eventCh:
					b.dispatch(event)
				default:
					return
				}
			}
		}
	}
}

func (b *AlertEventBus) dispatch(event *AlertEvent) {
	b.mu.RLock()
	handlers := make([]AlertEventHandler, len(b.handlers))
	copy(handlers, b.handlers)
	b.mu.RUnlock()

	for _, handler := range handlers {
		b.safeCall(handler, event)
	}
}

// safeCall invokes a handler with panic recovery so a panicking handler
// cannot kill the event bus goroutine.
func (b *AlertEventBus) safeCall(handler AlertEventHandler, event *AlertEvent) {
	defer func() {
		// Swallow panics to keep the bus alive. There is no logger
		// available at this level; the handler should do its own logging.
		recover() //nolint:errcheck // intentionally swallowed to keep bus alive
	}()
	handler(event)
}
