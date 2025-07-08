package myaudio

import (
	"sync"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Float32Pool provides a thread-safe pool of float32 slices to reduce allocations
// during audio conversion operations. It uses sync.Pool internally and handles
// buffer size validation to ensure correctness.
//
// The pool automatically falls back to allocation if the pool is empty, ensuring
// that operations never fail due to pool exhaustion.
type Float32Pool struct {
	pool      sync.Pool
	size      int
	gets      atomic.Uint64
	news      atomic.Uint64
	discarded atomic.Uint64
}

// Float32PoolStats contains statistics about pool usage
type Float32PoolStats struct {
	Hits      uint64 // Number of successful buffer reuses (Gets - News)
	Misses    uint64 // Number of new allocations (News)
	Discarded uint64 // Number of buffers discarded due to size mismatch
}

// NewFloat32Pool creates a new pool for float32 slices of the specified size.
// Returns an error if the size is invalid (zero or negative).
func NewFloat32Pool(size int) (*Float32Pool, error) {
	if size <= 0 {
		return nil, errors.Newf("invalid float32 pool size: %d", size).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "create_float32_pool").
			Context("requested_size", size).
			Build()
	}

	fp := &Float32Pool{
		size: size,
	}

	fp.pool = sync.Pool{
		New: func() any {
			fp.news.Add(1)
			return make([]float32, size)
		},
	}

	return fp, nil
}

// Get retrieves a float32 slice from the pool.
// If the pool is empty, a new slice is allocated.
func (fp *Float32Pool) Get() []float32 {
	fp.gets.Add(1)
	return fp.pool.Get().([]float32)
}

// Put returns a float32 slice to the pool.
// Buffers with incorrect sizes are discarded to maintain pool integrity.
// Nil buffers are safely ignored.
func (fp *Float32Pool) Put(buf []float32) {
	if buf == nil {
		fp.discarded.Add(1)
		return
	}
	if len(buf) != fp.size {
		fp.discarded.Add(1)
		return
	}
	fp.pool.Put(buf)
}

// GetStats returns current pool statistics
func (fp *Float32Pool) GetStats() Float32PoolStats {
	gets := fp.gets.Load()
	news := fp.news.Load()
	
	return Float32PoolStats{
		Hits:      gets - news,
		Misses:    news,
		Discarded: fp.discarded.Load(),
	}
}

// Clear removes all buffers from the pool, forcing new allocations
// on subsequent Get calls. This can be useful for testing or when
// the pool needs to be reset.
func (fp *Float32Pool) Clear() {
	// sync.Pool doesn't provide a direct clear method,
	// but we can achieve this by creating a new pool
	fp.pool = sync.Pool{
		New: func() any {
			fp.news.Add(1)
			return make([]float32, fp.size)
		},
	}
}