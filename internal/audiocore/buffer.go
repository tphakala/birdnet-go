package audiocore

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
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
		return nil, errors.Newf("invalid slice bounds [%d:%d] for buffer of length %d", start, end, b.length).
			Component(ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("operation", "buffer_slice").
			Context("start", start).
			Context("end", end).
			Context("length", b.length).
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
	smallPool  sync.Pool // For buffers up to smallSize
	mediumPool sync.Pool // For buffers up to mediumSize
	largePool  sync.Pool // For buffers up to largeSize
	config     BufferPoolConfig
	stats      BufferPoolStats
	statsMu    sync.RWMutex
	logger     *slog.Logger
}

// NewBufferPool creates a new buffer pool
func NewBufferPool(config BufferPoolConfig) BufferPool {
	logger := logging.ForService("audiocore")
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("component", "buffer_pool")

	pool := &bufferPoolImpl{
		config: config,
		logger: logger,
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

	logger.Info("buffer pool created",
		"small_size", config.SmallBufferSize,
		"medium_size", config.MediumBufferSize,
		"large_size", config.LargeBufferSize,
		"max_per_size", config.MaxBuffersPerSize)

	return pool
}

// Get retrieves a buffer of at least the specified size
func (p *bufferPoolImpl) Get(size int) AudioBuffer {
	p.updateStats(func() {
		p.stats.TotalBuffers++
		p.stats.ActiveBuffers++
	})

	var buf *bufferImpl
	var poolTier string

	// Select appropriate pool based on size
	switch {
	case size <= p.config.SmallBufferSize:
		buf = p.smallPool.Get().(*bufferImpl)
		poolTier = "small"
	case size <= p.config.MediumBufferSize:
		buf = p.mediumPool.Get().(*bufferImpl)
		poolTier = "medium"
	case size <= p.config.LargeBufferSize:
		buf = p.largePool.Get().(*bufferImpl)
		poolTier = "large"
	default:
		// Create a custom-sized buffer for very large requests
		buf = &bufferImpl{
			data: make([]byte, size),
			pool: p,
		}
		poolTier = "custom"
		p.logger.Debug("allocated custom-sized buffer",
			"size", size)
	}

	// Initialize buffer state
	buf.length = size
	buf.refCount = 1

	if p.logger.Enabled(context.TODO(), slog.LevelDebug) {
		p.logger.Debug("buffer allocated",
			"tier", poolTier,
			"requested_size", size,
			"actual_capacity", cap(buf.data))
	}

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
	var poolTier string
	switch {
	case capacity <= p.config.SmallBufferSize:
		p.smallPool.Put(buf)
		poolTier = "small"
	case capacity <= p.config.MediumBufferSize:
		p.mediumPool.Put(buf)
		poolTier = "medium"
	case capacity <= p.config.LargeBufferSize:
		p.largePool.Put(buf)
		poolTier = "large"
	default:
		// Don't pool very large buffers
		poolTier = "custom_discarded"
		p.logger.Debug("discarding custom-sized buffer",
			"capacity", capacity)
	}

	if p.logger.Enabled(context.TODO(), slog.LevelDebug) && poolTier != "custom_discarded" {
		p.logger.Debug("buffer returned to pool",
			"tier", poolTier,
			"capacity", capacity)
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
