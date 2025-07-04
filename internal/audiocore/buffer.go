package audiocore

import (
	"sync"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// bufferImpl is the concrete implementation of AudioBuffer
type bufferImpl struct {
	data     []byte
	length   int
	refCount int32
	pool     *bufferPoolImpl
	mu       sync.Mutex
}

// Data returns the underlying byte slice
func (b *bufferImpl) Data() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data[:b.length]
}

// Len returns the current length of valid data
func (b *bufferImpl) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.length
}

// Cap returns the capacity of the buffer
func (b *bufferImpl) Cap() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return cap(b.data)
}

// Reset clears the buffer
func (b *bufferImpl) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.length = 0
}

// Resize changes the buffer size
func (b *bufferImpl) Resize(newSize int) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if newSize < 0 {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("operation", "buffer_resize").
			Context("new_size", newSize).
			Build()
	}

	if newSize <= cap(b.data) {
		b.length = newSize
		return nil
	}

	// Need to allocate a new buffer
	newData := make([]byte, newSize)
	copy(newData, b.data[:b.length])
	b.data = newData
	b.length = newSize

	return nil
}

// Slice returns a slice of the buffer
func (b *bufferImpl) Slice(start, end int) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if start < 0 || end > b.length || start > end {
		return nil, errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("operation", "buffer_slice").
			Context("start", start).
			Context("end", end).
			Context("length", b.length).
			Context("error", "invalid slice bounds").
			Build()
	}

	return b.data[start:end], nil
}

// Acquire increments the reference count
func (b *bufferImpl) Acquire() {
	atomic.AddInt32(&b.refCount, 1)
}

// Release decrements the reference count and returns to pool if zero
func (b *bufferImpl) Release() {
	newCount := atomic.AddInt32(&b.refCount, -1)
	if newCount == 0 && b.pool != nil {
		b.pool.Put(b)
	}
}

// bufferPoolImpl manages reusable audio buffers
type bufferPoolImpl struct {
	smallPool  sync.Pool  // For buffers up to smallSize
	mediumPool sync.Pool  // For buffers up to mediumSize
	largePool  sync.Pool  // For buffers up to largeSize
	config     BufferPoolConfig
	stats      BufferPoolStats
	statsMu    sync.RWMutex
}

// NewBufferPool creates a new buffer pool
func NewBufferPool(config BufferPoolConfig) BufferPool {
	pool := &bufferPoolImpl{
		config: config,
	}

	// Initialize sync.Pools with constructors
	pool.smallPool.New = func() any {
		return &bufferImpl{
			data: make([]byte, config.SmallBufferSize),
			pool: pool,
		}
	}

	pool.mediumPool.New = func() any {
		return &bufferImpl{
			data: make([]byte, config.MediumBufferSize),
			pool: pool,
		}
	}

	pool.largePool.New = func() any {
		return &bufferImpl{
			data: make([]byte, config.LargeBufferSize),
			pool: pool,
		}
	}

	return pool
}

// Get retrieves a buffer of at least the specified size
func (p *bufferPoolImpl) Get(size int) AudioBuffer {
	p.updateStats(func() {
		p.stats.TotalBuffers++
		p.stats.ActiveBuffers++
	})

	var buf *bufferImpl

	// Select appropriate pool based on size
	switch {
	case size <= p.config.SmallBufferSize:
		buf = p.smallPool.Get().(*bufferImpl)
	case size <= p.config.MediumBufferSize:
		buf = p.mediumPool.Get().(*bufferImpl)
	case size <= p.config.LargeBufferSize:
		buf = p.largePool.Get().(*bufferImpl)
	default:
		// Create a custom-sized buffer for very large requests
		buf = &bufferImpl{
			data: make([]byte, size),
			pool: p,
		}
	}

	// Initialize buffer state
	buf.length = size
	buf.refCount = 1

	return buf
}

// Put returns a buffer to the pool
func (p *bufferPoolImpl) Put(buffer AudioBuffer) {
	buf, ok := buffer.(*bufferImpl)
	if !ok {
		return
	}

	p.updateStats(func() {
		p.stats.ActiveBuffers--
	})

	// Reset buffer state
	buf.Reset()
	buf.refCount = 0

	// Return to appropriate pool
	capacity := cap(buf.data)
	switch {
	case capacity <= p.config.SmallBufferSize:
		p.smallPool.Put(buf)
	case capacity <= p.config.MediumBufferSize:
		p.mediumPool.Put(buf)
	case capacity <= p.config.LargeBufferSize:
		p.largePool.Put(buf)
	default:
		// Don't pool very large buffers
	}
}

// Stats returns statistics about the pool
func (p *bufferPoolImpl) Stats() BufferPoolStats {
	p.statsMu.RLock()
	defer p.statsMu.RUnlock()
	return p.stats
}

// updateStats safely updates pool statistics
func (p *bufferPoolImpl) updateStats(fn func()) {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	fn()
}