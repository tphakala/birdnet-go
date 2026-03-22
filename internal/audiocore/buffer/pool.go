// Package buffer provides thread-safe pool implementations for audio buffers.
// It is a subpackage of audiocore and supplies BytePool and Float32Pool for
// reusing byte slices and float32 slices respectively.
//
// Metrics recording is intentionally absent here; the buffer.Manager (a later
// task) is responsible for calling GetStats and forwarding values to the
// metrics layer.
package buffer

import (
	"sync"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// BytePool provides a thread-safe pool of byte slices to reduce allocations.
// It uses sync.Pool internally and validates buffer sizes on return to ensure
// correctness. Buffers that do not match the expected size are discarded.
type BytePool struct {
	pool      sync.Pool
	size      int
	gets      atomic.Uint64 // Total Get calls
	news      atomic.Uint64 // Allocations via pool.New
	discarded atomic.Uint64 // Buffers discarded due to size mismatch
}

// BytePoolStats holds usage statistics for a BytePool.
type BytePoolStats struct {
	Hits      uint64 // Successful buffer reuses (Gets - News)
	Misses    uint64 // New allocations (News)
	Discarded uint64 // Buffers discarded due to size mismatch
}

// NewBytePool creates a BytePool where every buffer has the given size in bytes.
// Returns an error if size is not positive.
func NewBytePool(size int) (*BytePool, error) {
	if size <= 0 {
		return nil, errors.Newf("invalid buffer size: %d, must be greater than 0", size).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "create_byte_pool").
			Context("requested_size", size).
			Build()
	}

	bp := &BytePool{
		size: size,
	}

	bp.pool.New = func() any {
		bp.news.Add(1)
		return make([]byte, size)
	}

	return bp, nil
}

// Get retrieves a byte slice from the pool. If no suitable buffer is available,
// a new one is allocated. The returned slice always has the length specified in
// NewBytePool.
func (bp *BytePool) Get() []byte {
	bp.gets.Add(1)
	buf := bp.pool.Get().([]byte) //nolint:forcetypeassert // pool.New always returns []byte

	if len(buf) == bp.size {
		return buf
	}

	// Size mismatch — discard and allocate a fresh buffer.
	bp.discarded.Add(1)
	bp.news.Add(1)

	return make([]byte, bp.size)
}

// Put returns a byte slice to the pool for future reuse.
// Nil slices and slices with incorrect lengths are silently discarded.
// The buffer contents are not cleared; callers must zero the slice themselves
// if sensitive data must not be retained.
func (bp *BytePool) Put(buf []byte) {
	if buf == nil || len(buf) != bp.size {
		bp.discarded.Add(1)
		return
	}

	//nolint:staticcheck // SA6002: accepted trade-off — allocation savings outweigh interface boxing overhead
	bp.pool.Put(buf)
}

// GetStats returns a snapshot of the pool's usage statistics.
func (bp *BytePool) GetStats() BytePoolStats {
	gets := bp.gets.Load()
	news := bp.news.Load()

	hits := uint64(0)
	if gets > news {
		hits = gets - news
	}

	return BytePoolStats{
		Hits:      hits,
		Misses:    news,
		Discarded: bp.discarded.Load(),
	}
}

// Clear replaces the internal sync.Pool with a fresh one, allowing all pooled
// buffers to be garbage-collected. This is useful during shutdown or when
// reconfiguring the pool size.
//
// SAFETY: Clear must only be called when no other goroutines are calling
// Get or Put on this pool. Concurrent access during Clear is a data race.
func (bp *BytePool) Clear() {
	bp.pool = sync.Pool{
		New: func() any {
			bp.news.Add(1)
			return make([]byte, bp.size)
		},
	}
}

// Float32Pool provides a thread-safe pool of float32 slices to reduce
// allocations during audio conversion operations. Slices with incorrect lengths
// are discarded to maintain pool integrity.
type Float32Pool struct {
	pool      sync.Pool
	size      int
	gets      atomic.Uint64
	news      atomic.Uint64
	discarded atomic.Uint64
}

// Float32PoolStats holds usage statistics for a Float32Pool.
type Float32PoolStats struct {
	Hits      uint64 // Successful reuses (Gets - News)
	Misses    uint64 // New allocations (News)
	Discarded uint64 // Slices discarded due to size mismatch
}

// NewFloat32Pool creates a Float32Pool where every slice has the given length.
// Returns an error if size is not positive.
func NewFloat32Pool(size int) (*Float32Pool, error) {
	if size <= 0 {
		return nil, errors.Newf("invalid float32 pool size: %d, must be greater than 0", size).
			Component("audiocore").
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

// Get retrieves a float32 slice from the pool. If the pool is empty, a new
// slice is allocated. The returned slice always has the length specified in
// NewFloat32Pool.
func (fp *Float32Pool) Get() []float32 {
	fp.gets.Add(1)
	buf := fp.pool.Get().([]float32) //nolint:forcetypeassert // pool.New always returns []float32

	if len(buf) == fp.size {
		return buf
	}

	// Size mismatch — discard and allocate a fresh slice.
	fp.discarded.Add(1)
	fp.news.Add(1)

	return make([]float32, fp.size)
}

// Put returns a float32 slice to the pool for future reuse.
// Nil slices and slices with incorrect lengths are silently discarded.
func (fp *Float32Pool) Put(buf []float32) {
	if buf == nil || len(buf) != fp.size {
		fp.discarded.Add(1)
		return
	}

	//nolint:staticcheck // SA6002: accepted trade-off — allocation savings outweigh interface boxing overhead
	fp.pool.Put(buf)
}

// GetStats returns a snapshot of the pool's usage statistics.
func (fp *Float32Pool) GetStats() Float32PoolStats {
	gets := fp.gets.Load()
	news := fp.news.Load()

	hits := uint64(0)
	if gets > news {
		hits = gets - news
	}

	return Float32PoolStats{
		Hits:      hits,
		Misses:    news,
		Discarded: fp.discarded.Load(),
	}
}

// Clear replaces the internal sync.Pool with a fresh one, allowing all pooled
// slices to be garbage-collected.
//
// SAFETY: Clear must only be called when no other goroutines are calling
// Get or Put on this pool. Concurrent access during Clear is a data race.
func (fp *Float32Pool) Clear() {
	fp.pool = sync.Pool{
		New: func() any {
			fp.news.Add(1)
			return make([]float32, fp.size)
		},
	}
}
