package myaudio

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// PoolStatsProvider interface for accessing pool statistics
type PoolStatsProvider interface {
	GetHits() uint64
	GetMisses() uint64
	GetDiscarded() uint64
}

// BufferPoolStatsAdapter adapts BufferPoolStats to PoolStatsProvider interface
type BufferPoolStatsAdapter struct {
	BufferPoolStats
}

func (s BufferPoolStatsAdapter) GetHits() uint64      { return s.Hits }
func (s BufferPoolStatsAdapter) GetMisses() uint64    { return s.Misses }
func (s BufferPoolStatsAdapter) GetDiscarded() uint64 { return s.Discarded }

// Float32PoolStatsAdapter adapts Float32PoolStats to PoolStatsProvider interface
type Float32PoolStatsAdapter struct {
	Float32PoolStats
}

func (s Float32PoolStatsAdapter) GetHits() uint64      { return s.Hits }
func (s Float32PoolStatsAdapter) GetMisses() uint64    { return s.Misses }
func (s Float32PoolStatsAdapter) GetDiscarded() uint64 { return s.Discarded }

// runPoolConcurrencyGeneric runs pool concurrency tests with generic operations
func runPoolConcurrencyGeneric(t *testing.T, numWorkers, opsPerWorker int,
	getOp func() interface{}, putOp func(interface{}), validateBuffer func(interface{})) {
	t.Helper()

	var wg sync.WaitGroup

	for range numWorkers {
		wg.Go(func() {
			for range opsPerWorker {
				buf := getOp()
				validateBuffer(buf)
				putOp(buf)
			}
		})
	}

	wg.Wait()
}

// runPoolConcurrencyWithStats runs pool concurrency tests and verifies stats
func runPoolConcurrencyWithStats(t *testing.T, bufferSize, numWorkers, opsPerWorker int,
	getOp func() interface{}, putOp func(interface{}), validateBuffer func(interface{}),
	getStats func() PoolStatsProvider) {
	t.Helper()

	runPoolConcurrencyGeneric(t, numWorkers, opsPerWorker, getOp, putOp, validateBuffer)

	// Verify stats are consistent
	stats := getStats()
	totalOps := uint64(numWorkers * opsPerWorker)

	hits := stats.GetHits()
	misses := stats.GetMisses()
	discarded := stats.GetDiscarded()

	// Allow some variance due to sync.Pool's per-CPU sharding
	assert.InDelta(t, float64(totalOps), float64(hits+misses), float64(numWorkers*2))
	assert.Equal(t, uint64(0), discarded)
}

func TestNewBufferPool(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		size    int
		wantErr bool
	}{
		{
			name:    "valid_size",
			size:    1024,
			wantErr: false,
		},
		{
			name:    "zero_size",
			size:    0,
			wantErr: true,
		},
		{
			name:    "negative_size",
			size:    -1,
			wantErr: true,
		},
		{
			name:    "large_size",
			size:    1024 * 1024,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, err := NewBufferPool(tt.size)
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, pool)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, pool)
				assert.Equal(t, tt.size, pool.size)
			}
		})
	}
}

func TestBufferPoolGetPut(t *testing.T) {
	const bufferSize = 1024
	pool, err := NewBufferPool(bufferSize)
	require.NoError(t, err)

	// Test Get
	buf := pool.Get()
	assert.NotNil(t, buf)
	assert.Len(t, buf, bufferSize)

	// First get is always a miss since pool is empty
	stats := pool.GetStats()
	assert.Equal(t, uint64(0), stats.Hits)
	assert.GreaterOrEqual(t, stats.Misses, uint64(1))

	// Test Put and reuse
	pool.Put(buf)

	buf2 := pool.Get()
	assert.NotNil(t, buf2)
	assert.Len(t, buf2, bufferSize)

	// Verify stats changed (sync.Pool behavior is non-deterministic)
	stats = pool.GetStats()
	// We should have at least the initial miss
	assert.GreaterOrEqual(t, stats.Misses, uint64(1))
	// Total operations should have increased
	assert.Greater(t, stats.Hits+stats.Misses, uint64(1))
}

func TestBufferPoolSizeValidation(t *testing.T) {
	t.Parallel()
	const bufferSize = 1024
	pool, err := NewBufferPool(bufferSize)
	require.NoError(t, err)

	// Test putting nil buffer
	pool.Put(nil)
	stats := pool.GetStats()
	assert.Equal(t, uint64(1), stats.Discarded)

	// Test putting wrong size buffer
	wrongSizeBuf := make([]byte, bufferSize+1)
	pool.Put(wrongSizeBuf)
	stats = pool.GetStats()
	assert.Equal(t, uint64(2), stats.Discarded)

	// Test putting correct size buffer
	correctBuf := make([]byte, bufferSize)
	pool.Put(correctBuf)

	// Verify it gets reused
	reusedBuf := pool.Get()
	assert.NotNil(t, reusedBuf)
	stats = pool.GetStats()
	assert.GreaterOrEqual(t, stats.Hits, uint64(1))
}

func TestBufferPoolConcurrency(t *testing.T) {
	const (
		bufferSize   = 1024
		numWorkers   = 10
		opsPerWorker = 1000
	)

	pool, err := NewBufferPool(bufferSize)
	require.NoError(t, err)

	runPoolConcurrencyWithStats(t, bufferSize, numWorkers, opsPerWorker,
		func() interface{} { return pool.Get() },
		func(buf interface{}) { pool.Put(buf.([]byte)) },
		func(buf interface{}) {
			buffer := buf.([]byte)
			assert.Len(t, buffer, bufferSize)
			// Simulate some work with the buffer
			buffer[0] = byte(0)
			buffer[len(buffer)-1] = byte(1)
		},
		func() PoolStatsProvider { return BufferPoolStatsAdapter{pool.GetStats()} })
}

func TestBufferPoolMemoryReuse(t *testing.T) {
	const bufferSize = 1024
	pool, err := NewBufferPool(bufferSize)
	require.NoError(t, err)

	// Get and return multiple buffers to increase chance of reuse
	for i := 0; i < 10; i++ {
		buf := pool.Get()
		pool.Put(buf)
	}

	// Get more buffers - some should be reused from pool
	for i := 0; i < 10; i++ {
		buf := pool.Get()
		assert.Len(t, buf, bufferSize)
		pool.Put(buf)
	}

	// Verify stats show both hits and misses
	stats := pool.GetStats()
	// sync.Pool behavior is non-deterministic, so we just verify basic functionality
	assert.Positive(t, stats.Misses) // At least some allocations
	// Don't assert on hits as sync.Pool may release buffers under memory pressure
}

func TestBufferPoolClear(t *testing.T) {
	t.Parallel()
	const bufferSize = 1024
	pool, err := NewBufferPool(bufferSize)
	require.NoError(t, err)

	// Add some buffers to the pool
	for range 5 {
		buf := pool.Get()
		pool.Put(buf)
	}

	initialStats := pool.GetStats()
	assert.Positive(t, initialStats.Misses)

	// Clear the pool
	pool.Clear()

	// Get a new buffer - should be a new allocation
	buf := pool.Get()
	assert.Len(t, buf, bufferSize)

	// Stats should show a new miss after clear
	newStats := pool.GetStats()
	assert.Greater(t, newStats.Misses, initialStats.Misses)
}

// TestBufferPoolStress performs a stress test with many goroutines
func TestBufferPoolStress(t *testing.T) {
	t.Attr("kind", "stress")
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	const (
		bufferSize = 4096
		numWorkers = 50
		duration   = 1 // second
	)

	pool, err := NewBufferPool(bufferSize)
	require.NoError(t, err)

	var (
		wg       sync.WaitGroup
		totalOps atomic.Uint64
		stopChan = make(chan struct{})
	)

	// Start workers
	for range numWorkers {
		wg.Go(func() {
			for {
				select {
				case <-stopChan:
					return
				default:
					buf := pool.Get()
					// Simulate work
					for j := range len(buf) {
						buf[j] = byte(j % 256)
					}
					pool.Put(buf)
					totalOps.Add(1)
				}
			}
		})
	}

	// Run for specified duration
	// Use a separate goroutine to control test duration
	go func() {
		time.Sleep(time.Duration(duration) * time.Second)
		close(stopChan)
	}()
	wg.Wait()

	// Verify results
	ops := totalOps.Load()
	stats := pool.GetStats()

	t.Logf("Total operations: %d", ops)
	t.Logf("Hit rate: %.2f%%", float64(stats.Hits)/float64(stats.Hits+stats.Misses)*100)
	t.Logf("Stats: %+v", stats)

	assert.Positive(t, ops)
	// Allow some variance due to sync.Pool's per-CPU sharding
	assert.InDelta(t, float64(ops), float64(stats.Hits+stats.Misses), float64(numWorkers*2))
	assert.Positive(t, stats.Hits) // Should have some reuse
}
