package observability

import (
	"maps"
	"slices"
	"sync"
	"time"
)

// ringBuffer is a fixed-size circular buffer of MetricPoints.
// Zero allocations during steady-state writes.
type ringBuffer struct {
	data  []MetricPoint // fixed-size, allocated once
	head  int           // next write position
	count int           // number of valid entries (0..cap)
}

// newRingBuffer creates a ring buffer with the given capacity.
func newRingBuffer(capacity int) *ringBuffer {
	return &ringBuffer{
		data: make([]MetricPoint, capacity),
	}
}

// write stores a point, overwriting the oldest entry when full.
func (rb *ringBuffer) write(p MetricPoint) {
	rb.data[rb.head] = p
	rb.head = (rb.head + 1) % len(rb.data)
	if rb.count < len(rb.data) {
		rb.count++
	}
}

// read returns up to n points in chronological order as a new slice.
func (rb *ringBuffer) read(n int) []MetricPoint {
	if rb.count == 0 {
		return nil
	}
	if n <= 0 || n > rb.count {
		n = rb.count
	}

	result := make([]MetricPoint, n)
	// Start reading from (head - n) mod cap
	start := (rb.head - n + len(rb.data)) % len(rb.data)
	if start+n <= len(rb.data) {
		// Contiguous segment — single copy
		copy(result, rb.data[start:start+n])
	} else {
		// Wraps around — two copies
		part1 := len(rb.data) - start
		copy(result, rb.data[start:])
		copy(result[part1:], rb.data[:n-part1])
	}
	return result
}

// latest returns the most recent point. ok is false if the buffer is empty.
func (rb *ringBuffer) latest() (MetricPoint, bool) {
	if rb.count == 0 {
		return MetricPoint{}, false
	}
	idx := (rb.head - 1 + len(rb.data)) % len(rb.data)
	return rb.data[idx], true
}

// MemoryStore is an in-memory MetricsStore backed by per-metric circular buffers.
// It is safe for concurrent use.
type MemoryStore struct {
	mu        sync.RWMutex
	series    map[string]*ringBuffer
	maxPoints int

	subMu       sync.Mutex
	subscribers map[chan map[string]MetricPoint]struct{}

	topoMu          sync.Mutex
	topoSubscribers map[chan struct{}]struct{}
}

// NewMemoryStore creates a MemoryStore that keeps up to maxPoints per metric.
// A minimum of 1 point is enforced to avoid zero-capacity buffers.
func NewMemoryStore(maxPoints int) *MemoryStore {
	maxPoints = max(1, maxPoints)
	return &MemoryStore{
		series:          make(map[string]*ringBuffer),
		maxPoints:       maxPoints,
		subscribers:     make(map[chan map[string]MetricPoint]struct{}),
		topoSubscribers: make(map[chan struct{}]struct{}),
	}
}

// RecordBatch stores all metric values for a single collection tick.
// After recording, it broadcasts the latest snapshot to all active subscribers.
func (s *MemoryStore) RecordBatch(points map[string]float64) {
	now := time.Now()

	s.mu.Lock()
	for name, value := range points {
		rb, ok := s.series[name]
		if !ok {
			rb = newRingBuffer(s.maxPoints)
			s.series[name] = rb
		}
		rb.write(MetricPoint{Timestamp: now, Value: value})
	}

	// Only build the snapshot if there are active subscribers.
	s.subMu.Lock()
	hasSubscribers := len(s.subscribers) > 0
	s.subMu.Unlock()

	if hasSubscribers {
		// Build immutable snapshot inside the lock to guarantee consistency
		// even if multiple goroutines call RecordBatch concurrently.
		snapshot := make(map[string]MetricPoint, len(s.series))
		for name, rb := range s.series {
			if p, ok := rb.latest(); ok {
				snapshot[name] = p
			}
		}
		s.mu.Unlock()

		s.subMu.Lock()
		for ch := range s.subscribers {
			// Non-blocking send: drop if consumer is lagging.
			select {
			case ch <- snapshot:
			default:
			}
		}
		s.subMu.Unlock()
	} else {
		s.mu.Unlock()
	}
}

// Get returns up to the last n points for the named metric.
func (s *MemoryStore) Get(name string, n int) []MetricPoint {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rb, ok := s.series[name]
	if !ok {
		return nil
	}
	return rb.read(n)
}

// GetAll returns up to the last n points for every tracked metric.
func (s *MemoryStore) GetAll(n int) map[string][]MetricPoint {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string][]MetricPoint, len(s.series))
	for name, rb := range s.series {
		if points := rb.read(n); points != nil {
			result[name] = points
		}
	}
	return result
}

// GetLatest returns the most recent point for each tracked metric.
func (s *MemoryStore) GetLatest() map[string]MetricPoint {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]MetricPoint, len(s.series))
	for name, rb := range s.series {
		if p, ok := rb.latest(); ok {
			result[name] = p
		}
	}
	return result
}

// Names returns the sorted list of tracked metric names.
func (s *MemoryStore) Names() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := slices.Collect(maps.Keys(s.series))
	slices.Sort(names)
	return names
}

// Subscribe returns a channel that receives the latest metric snapshot
// after each RecordBatch, and a cancel function to unsubscribe.
// The received maps are shared and must be treated as read-only by consumers.
func (s *MemoryStore) Subscribe() (sub <-chan map[string]MetricPoint, cancel func()) {
	bidi := make(chan map[string]MetricPoint, 1)

	s.subMu.Lock()
	s.subscribers[bidi] = struct{}{}
	s.subMu.Unlock()

	cancel = func() {
		s.subMu.Lock()
		delete(s.subscribers, bidi)
		s.subMu.Unlock()
	}

	return bidi, cancel
}

// BroadcastTopologyChanged sends a non-blocking signal to every topology
// subscriber. Subscribers whose buffer is full keep their pending signal
// (the broadcaster never blocks), which is correct because the signal carries
// no payload and consumers re-fetch the full snapshot on receipt.
func (s *MemoryStore) BroadcastTopologyChanged() {
	s.topoMu.Lock()
	defer s.topoMu.Unlock()
	for ch := range s.topoSubscribers {
		// Non-blocking send: a coalesced signal is enough, drop if already pending.
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// SubscribeTopology returns a buffered (cap 1) signal channel that receives a
// value on each BroadcastTopologyChanged, and a cancel function to unsubscribe.
// The cancel function removes the subscriber but does NOT close the channel
// (avoids send-on-closed-channel panics if a broadcast races with cancel).
func (s *MemoryStore) SubscribeTopology() (sub <-chan struct{}, cancel func()) {
	bidi := make(chan struct{}, 1)

	s.topoMu.Lock()
	s.topoSubscribers[bidi] = struct{}{}
	s.topoMu.Unlock()

	cancel = func() {
		s.topoMu.Lock()
		delete(s.topoSubscribers, bidi)
		s.topoMu.Unlock()
	}

	return bidi, cancel
}
