// internal/health/error_buffer.go
package health

import (
	"maps"
	"sync"
	"time"
)

// DefaultErrorBufferSize is the default capacity for the health error ring buffer.
const DefaultErrorBufferSize = 500

// globalErrorBuffer holds the process-wide ErrorRingBuffer that is shared
// between the logger (writer) and the health checks (reader). Set once at
// startup via SetGlobalErrorBuffer.
var (
	globalErrorBuffer   *ErrorRingBuffer
	globalErrorBufferMu sync.Mutex
)

// SetGlobalErrorBuffer stores the shared error buffer. Call this once from
// main before starting the logger. Subsequent calls are no-ops, and nil
// arguments are ignored.
func SetGlobalErrorBuffer(buf *ErrorRingBuffer) {
	if buf == nil {
		return
	}
	globalErrorBufferMu.Lock()
	defer globalErrorBufferMu.Unlock()
	if globalErrorBuffer != nil {
		return
	}
	globalErrorBuffer = buf
}

// GlobalErrorBuffer returns the shared error buffer, or nil if not yet set.
func GlobalErrorBuffer() *ErrorRingBuffer {
	globalErrorBufferMu.Lock()
	defer globalErrorBufferMu.Unlock()
	return globalErrorBuffer
}

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
// If maxSize is less than or equal to zero, it is set to 1.
func NewErrorRingBuffer(maxSize int) *ErrorRingBuffer {
	if maxSize <= 0 {
		maxSize = 1
	}
	return &ErrorRingBuffer{
		entries: make([]LogEntry, maxSize),
		maxSize: maxSize,
	}
}

// Add inserts a log entry into the buffer. Oldest entries are overwritten.
// The Fields map is deep-copied to prevent mutation of the caller's map.
func (b *ErrorRingBuffer) Add(entry *LogEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := *entry
	if e.Fields != nil {
		e.Fields = maps.Clone(e.Fields)
	}
	b.entries[b.head] = e
	b.head = (b.head + 1) % b.maxSize
	if b.count < b.maxSize {
		b.count++
	}
}

// cloneEntry returns a copy of e with a deep-copied Fields map.
func cloneEntry(e *LogEntry) LogEntry {
	out := *e
	if e.Fields != nil {
		out.Fields = maps.Clone(e.Fields)
	}
	return out
}

// Entries returns all entries in chronological order (oldest first).
// Each returned entry has its own copy of the Fields map.
func (b *ErrorRingBuffer) Entries() []LogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]LogEntry, 0, b.count)
	if b.count < b.maxSize {
		for i := range b.count {
			result = append(result, cloneEntry(&b.entries[i]))
		}
	} else {
		for i := range b.entries[b.head:] {
			result = append(result, cloneEntry(&b.entries[b.head+i]))
		}
		for i := range b.head {
			result = append(result, cloneEntry(&b.entries[i]))
		}
	}
	return result
}

// Recent returns the most recent n entries in reverse chronological order (newest first).
// Each returned entry has its own copy of the Fields map.
// If n is less than or equal to zero, an empty slice is returned.
func (b *ErrorRingBuffer) Recent(n int) []LogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if n <= 0 {
		return []LogEntry{}
	}
	if n > b.count {
		n = b.count
	}
	result := make([]LogEntry, n)
	for i := range n {
		idx := (b.head - 1 - i + b.maxSize) % b.maxSize
		result[i] = cloneEntry(&b.entries[idx])
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
// All valid entries are scanned because insertion order may not match
// chronological order under concurrent writers.
func (b *ErrorRingBuffer) CountSince(since time.Time) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	count := 0
	for i := range b.count {
		if !b.entries[i].Timestamp.Before(since) {
			count++
		}
	}
	return count
}

// EntriesSince returns cloned entries with timestamps at or after the given time.
// Like CountSince, all valid entries are scanned regardless of insertion order.
func (b *ErrorRingBuffer) EntriesSince(since time.Time) []LogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var result []LogEntry
	for i := range b.count {
		if !b.entries[i].Timestamp.Before(since) {
			result = append(result, cloneEntry(&b.entries[i]))
		}
	}
	return result
}

// Clear removes all entries from the buffer, zeroing the backing slice to release references.
func (b *ErrorRingBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.entries {
		b.entries[i] = LogEntry{}
	}
	b.head = 0
	b.count = 0
}
