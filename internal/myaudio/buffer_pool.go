// Package myaudio provides audio processing functionality for BirdNET-Go.
package myaudio

import (
	"sync"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// BufferPool provides a thread-safe pool of byte slices to reduce allocations.
// It uses sync.Pool internally and handles buffer size validation.
//
// The pool automatically creates new buffers when needed and reuses returned
// buffers to minimize GC pressure. Buffers that don't match the expected size
// are discarded to maintain safety.
type BufferPool struct {
	pool      sync.Pool
	size      int
	gets      atomic.Uint64 // Total number of Get calls
	news      atomic.Uint64 // Number of new allocations from pool.New
	discarded atomic.Uint64 // Number of buffers discarded due to size mismatch
}

// NewBufferPool creates a new buffer pool with the specified buffer size.
// The size parameter determines the size of each buffer in bytes.
//
// Returns an error if size is <= 0.
func NewBufferPool(size int) (*BufferPool, error) {
	if size <= 0 {
		return nil, errors.Newf("invalid buffer size: %d, must be greater than 0", size).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "create_buffer_pool").
			Context("requested_size", size).
			Build()
	}

	bp := &BufferPool{
		size: size,
	}

	bp.pool.New = func() any {
		// New is called when pool is empty
		bp.news.Add(1)
		return make([]byte, size)
	}

	return bp, nil
}

// Get retrieves a buffer from the pool.
// If no buffer is available, a new one is created.
// The returned buffer is guaranteed to have the size specified in NewBufferPool.
func (bp *BufferPool) Get() []byte {
	bp.gets.Add(1)
	buf := bp.pool.Get().([]byte)

	// Verify buffer size for safety
	if len(buf) == bp.size {
		return buf
	}

	// Buffer size mismatch - discard and allocate new
	bp.discarded.Add(1)
	bp.news.Add(1)
	return make([]byte, bp.size)
}

// Put returns a buffer to the pool for reuse.
// Buffers that don't match the expected size are discarded.
// The buffer contents are not cleared - callers should handle this if needed.
func (bp *BufferPool) Put(buf []byte) {
	if buf == nil || len(buf) != bp.size {
		bp.discarded.Add(1)
		return
	}

	//nolint:staticcheck // SA6002: sync.Pool is designed to work with slices
	bp.pool.Put(buf)
}

// Stats returns the current pool statistics.
type BufferPoolStats struct {
	Hits      uint64 // Number of successful buffer reuses (Gets - News)
	Misses    uint64 // Number of new allocations
	Discarded uint64 // Number of buffers discarded
}

// GetStats returns the current pool statistics.
// This is useful for monitoring pool efficiency.
func (bp *BufferPool) GetStats() BufferPoolStats {
	gets := bp.gets.Load()
	news := bp.news.Load()

	// Calculate hits as gets minus news
	hits := uint64(0)
	if gets > news {
		hits = gets - news
	}

	return BufferPoolStats{
		Hits:      hits,
		Misses:    news,
		Discarded: bp.discarded.Load(),
	}
}

// RecordMetrics records buffer pool metrics if metrics are enabled.
// This should be called periodically to track pool efficiency.
func (bp *BufferPool) RecordMetrics(m *metrics.MyAudioMetrics, poolName string) {
	if m == nil {
		return
	}

	stats := bp.GetStats()

	// Calculate hit rate
	total := float64(stats.Hits + stats.Misses)
	hitRate := float64(0)
	if total > 0 {
		hitRate = float64(stats.Hits) / total
	}

	// TODO: Add metrics.RecordBufferPoolStats when available
	// For now, we can use existing metrics
	if hitRate > 0.9 {
		m.RecordBufferAllocation("pool", poolName, "hit")
	} else {
		m.RecordBufferAllocation("pool", poolName, "miss")
	}
}

// Clear empties the pool, allowing all buffers to be garbage collected.
// This is useful during shutdown or when reconfiguring the pool.
func (bp *BufferPool) Clear() {
	// sync.Pool doesn't provide a clear method, but we can hint to GC
	// by creating a new pool instance
	bp.pool = sync.Pool{
		New: func() any {
			bp.news.Add(1)
			return make([]byte, bp.size)
		},
	}
}
