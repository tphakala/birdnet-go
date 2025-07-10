package myaudio

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
				assert.Error(t, err)
				assert.Nil(t, pool)
			} else {
				assert.NoError(t, err)
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

	// Verify reuse
	stats = pool.GetStats()
	assert.GreaterOrEqual(t, stats.Hits, uint64(1))
	assert.GreaterOrEqual(t, stats.Misses, uint64(1))
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
	assert.Equal(t, uint64(1), stats.Hits)
}

func TestBufferPoolConcurrency(t *testing.T) {
	const (
		bufferSize = 1024
		numWorkers = 10
		opsPerWorker = 1000
	)

	pool, err := NewBufferPool(bufferSize)
	require.NoError(t, err)

	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for i := range numWorkers {
		go func(workerID int) {
			defer wg.Done()
			
			for j := range opsPerWorker {
				buf := pool.Get()
				require.Len(t, buf, bufferSize)
				
				// Simulate some work with the buffer
				buf[0] = byte(workerID)
				buf[len(buf)-1] = byte(j)
				
				pool.Put(buf)
			}
		}(i)
	}

	wg.Wait()

	// Verify stats are consistent
	stats := pool.GetStats()
	totalOps := uint64(numWorkers * opsPerWorker)
	// Allow some variance due to sync.Pool's per-CPU sharding
	assert.InDelta(t, float64(totalOps), float64(stats.Hits+stats.Misses), float64(numWorkers*2))
	assert.Equal(t, uint64(0), stats.Discarded)
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
	assert.Greater(t, stats.Misses, uint64(0)) // At least some allocations
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
	assert.Greater(t, initialStats.Misses, uint64(0))

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
		wg        sync.WaitGroup
		totalOps  atomic.Uint64
		stopChan  = make(chan struct{})
	)

	wg.Add(numWorkers)

	// Start workers
	for i := range numWorkers {
		go func(workerID int) {
			defer wg.Done()
			
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
		}(i)
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
	
	assert.Greater(t, ops, uint64(0))
	// Allow some variance due to sync.Pool's per-CPU sharding
	assert.InDelta(t, float64(ops), float64(stats.Hits+stats.Misses), float64(numWorkers*2))
	assert.Greater(t, stats.Hits, uint64(0)) // Should have some reuse
}