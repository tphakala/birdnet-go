// internal/health/error_buffer.go
package health

import (
	"sync"
	"time"
)

// LogEntry represents a captured log entry from the application logger.
type LogEntry struct {
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Component string         `json:"component,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
	Fields    map[string]any `json:"fields,omitempty"`
}

// ErrorRingBuffer is a thread-safe fixed-size ring buffer for log entries.
type ErrorRingBuffer struct {
	mu      sync.RWMutex
	entries []LogEntry
	head    int
	count   int
	maxSize int
}

// NewErrorRingBuffer creates a buffer that keeps the most recent maxSize entries.
func NewErrorRingBuffer(maxSize int) *ErrorRingBuffer {
	return &ErrorRingBuffer{
		entries: make([]LogEntry, maxSize),
		maxSize: maxSize,
	}
}

// Add inserts a log entry into the buffer. Oldest entries are overwritten.
func (b *ErrorRingBuffer) Add(entry *LogEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries[b.head] = *entry
	b.head = (b.head + 1) % b.maxSize
	if b.count < b.maxSize {
		b.count++
	}
}

// Entries returns all entries in chronological order (oldest first).
func (b *ErrorRingBuffer) Entries() []LogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]LogEntry, 0, b.count)
	if b.count < b.maxSize {
		result = append(result, b.entries[:b.count]...)
	} else {
		result = append(result, b.entries[b.head:]...)
		result = append(result, b.entries[:b.head]...)
	}
	return result
}

// Recent returns the most recent n entries in reverse chronological order (newest first).
func (b *ErrorRingBuffer) Recent(n int) []LogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if n > b.count {
		n = b.count
	}
	result := make([]LogEntry, n)
	for i := range n {
		idx := (b.head - 1 - i + b.maxSize) % b.maxSize
		result[i] = b.entries[idx]
	}
	return result
}

// Count returns the number of entries currently in the buffer.
func (b *ErrorRingBuffer) Count() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.count
}

// CountSince returns the number of entries at or after the given time.
func (b *ErrorRingBuffer) CountSince(since time.Time) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	count := 0
	for i := range b.count {
		idx := (b.head - 1 - i + b.maxSize) % b.maxSize
		if b.entries[idx].Timestamp.Before(since) {
			break
		}
		count++
	}
	return count
}

// Clear removes all entries from the buffer.
func (b *ErrorRingBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.head = 0
	b.count = 0
}
