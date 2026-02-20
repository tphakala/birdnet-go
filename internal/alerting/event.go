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

// AlertEventBus is a simple pub/sub for alert events.
type AlertEventBus struct {
	handlers []AlertEventHandler
	mu       sync.RWMutex
}

// NewAlertEventBus creates a new alert event bus.
func NewAlertEventBus() *AlertEventBus {
	return &AlertEventBus{
		handlers: make([]AlertEventHandler, 0),
	}
}

// Subscribe registers a handler for alert events.
func (b *AlertEventBus) Subscribe(handler AlertEventHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers = append(b.handlers, handler)
}

// Publish sends an event to all registered handlers.
func (b *AlertEventBus) Publish(event *AlertEvent) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	b.mu.RLock()
	handlers := make([]AlertEventHandler, len(b.handlers))
	copy(handlers, b.handlers)
	b.mu.RUnlock()

	for _, handler := range handlers {
		handler(event)
	}
}
