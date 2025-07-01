package telemetry

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
)

// MockTransport implements sentry.Transport for testing
type MockTransport struct {
	mu       sync.RWMutex
	events   []*sentry.Event
	disabled bool
	delay    time.Duration
}

// NewMockTransport creates a new mock transport
func NewMockTransport() *MockTransport {
	return &MockTransport{
		events: make([]*sentry.Event, 0),
	}
}

// Configure implements sentry.Transport.
// This is a no-op for the mock transport as configuration is handled during creation.
//
//nolint:gocritic // hugeParam: interface requirement, cannot change signature
func (t *MockTransport) Configure(_ sentry.ClientOptions) {}

// SendEvent implements sentry.Transport
func (t *MockTransport) SendEvent(event *sentry.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.disabled {
		return
	}

	// Simulate network delay if configured
	if t.delay > 0 {
		time.Sleep(t.delay)
	}

	// Store the event
	t.events = append(t.events, event)
}

// Flush implements sentry.Transport
func (t *MockTransport) Flush(timeout time.Duration) bool {
	// Simulate flush delay
	if t.delay > 0 && t.delay < timeout {
		time.Sleep(t.delay)
	}
	return true
}

// FlushWithContext implements sentry.Transport
func (t *MockTransport) FlushWithContext(ctx context.Context) bool {
	// Check if context is already cancelled
	select {
	case <-ctx.Done():
		return false
	default:
	}
	
	// If delay is set, wait for it or context cancellation
	if t.delay > 0 {
		select {
		case <-time.After(t.delay):
			return true
		case <-ctx.Done():
			return false
		}
	}
	
	return true
}

// GetEvents returns captured events
func (t *MockTransport) GetEvents() []*sentry.Event {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	// Return a copy to avoid race conditions
	events := make([]*sentry.Event, len(t.events))
	copy(events, t.events)
	return events
}

// GetEventCount returns the number of captured events
func (t *MockTransport) GetEventCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.events)
}

// Clear removes all captured events
func (t *MockTransport) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = t.events[:0]
}

// SetDisabled controls whether events are captured
func (t *MockTransport) SetDisabled(disabled bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.disabled = disabled
}

// SetDelay sets the simulated network delay
func (t *MockTransport) SetDelay(delay time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.delay = delay
}

// GetLastEvent returns the most recent event or nil
func (t *MockTransport) GetLastEvent() *sentry.Event {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	if len(t.events) == 0 {
		return nil
	}
	return t.events[len(t.events)-1]
}

// FindEventByMessage searches for an event by message
func (t *MockTransport) FindEventByMessage(message string) *sentry.Event {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	for _, event := range t.events {
		if event.Message == message {
			return event
		}
	}
	return nil
}

// GetEventMessages returns all event messages for easy assertions
func (t *MockTransport) GetEventMessages() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	messages := make([]string, 0, len(t.events))
	for _, event := range t.events {
		messages = append(messages, event.Message)
	}
	return messages
}

// EventSummary provides a simplified view of an event for testing
type EventSummary struct {
	Message     string                 `json:"message"`
	Level       string                 `json:"level"`
	Tags        map[string]string      `json:"tags"`
	Extra       map[string]any `json:"extra"`
	Fingerprint []string               `json:"fingerprint"`
	Timestamp   time.Time              `json:"timestamp"`
}

// GetEventSummaries returns simplified summaries of all events
func (t *MockTransport) GetEventSummaries() []EventSummary {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	summaries := make([]EventSummary, 0, len(t.events))
	for _, event := range t.events {
		summary := EventSummary{
			Message:     event.Message,
			Level:       string(event.Level),
			Tags:        event.Tags,
			Extra:       event.Extra,
			Fingerprint: event.Fingerprint,
			Timestamp:   event.Timestamp,
		}
		summaries = append(summaries, summary)
	}
	return summaries
}

// ToJSON returns events as JSON for debugging
func (t *MockTransport) ToJSON() (string, error) {
	summaries := t.GetEventSummaries()
	data, err := json.MarshalIndent(summaries, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WaitForEventCount waits for a specific number of events or timeout
func (t *MockTransport) WaitForEventCount(count int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if t.GetEventCount() >= count {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// Close implements sentry.Transport
func (t *MockTransport) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	// Clear all events on close
	t.events = nil
	t.disabled = true
}