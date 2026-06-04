package observability

import (
	"sync"
	"time"
)

// DefaultEventBufferCapacity is the default number of recent events to retain per buffer.
const DefaultEventBufferCapacity = 100

// HealthEvent records a single occurrence of a health counter change.
type HealthEvent struct {
	Time   time.Time `json:"time"`
	Source string    `json:"source"`
	Delta  int64     `json:"delta"`
	Metric string    `json:"metric"`
}

// HealthEventBuffer is a thread-safe ring buffer that stores recent health events.
type HealthEventBuffer struct {
	mu       sync.RWMutex
	events   []HealthEvent
	head     int
	size     int
	capacity int
}

// NewHealthEventBuffer creates a buffer with the given capacity.
func NewHealthEventBuffer(capacity int) *HealthEventBuffer {
	if capacity <= 0 {
		capacity = DefaultEventBufferCapacity
	}
	return &HealthEventBuffer{
		events:   make([]HealthEvent, capacity),
		head:     -1,
		capacity: capacity,
	}
}

// Add appends an event to the ring buffer, evicting the oldest if full.
func (b *HealthEventBuffer) Add(event HealthEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.head = (b.head + 1) % b.capacity
	b.events[b.head] = event
	if b.size < b.capacity {
		b.size++
	}
}

// Recent returns the last n events matching the given metric, sorted most recent first.
// If metric is empty, all events are returned.
func (b *HealthEventBuffer) Recent(metric string, n int) []HealthEvent {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.size == 0 || n <= 0 {
		return nil
	}

	result := make([]HealthEvent, 0, min(n, b.size))
	for i := range b.size {
		if len(result) >= n {
			break
		}
		idx := (b.head - i + b.capacity) % b.capacity
		e := b.events[idx]
		if metric == "" || e.Metric == metric {
			result = append(result, e)
		}
	}
	return result
}

// RecentAll returns the last n events regardless of metric type, most recent first.
func (b *HealthEventBuffer) RecentAll(n int) []HealthEvent {
	return b.Recent("", n)
}
