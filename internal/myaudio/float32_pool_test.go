package myaudio

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFloat32Pool(t *testing.T) {
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
			size:    144000, // Typical audio buffer size
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, err := NewFloat32Pool(tt.size)
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

func TestFloat32PoolGetPut(t *testing.T) {
	const bufferSize = 1024
	pool, err := NewFloat32Pool(bufferSize)
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

func TestFloat32PoolSizeValidation(t *testing.T) {
	t.Parallel()
	const bufferSize = 1024
	pool, err := NewFloat32Pool(bufferSize)
	require.NoError(t, err)

	// Test putting nil buffer
	pool.Put(nil)
	stats := pool.GetStats()
	assert.Equal(t, uint64(1), stats.Discarded)

	// Test putting wrong size buffer
	wrongSizeBuf := make([]float32, bufferSize+1)
	pool.Put(wrongSizeBuf)
	stats = pool.GetStats()
	assert.Equal(t, uint64(2), stats.Discarded)

	// Test putting correct size buffer
	correctBuf := make([]float32, bufferSize)
	pool.Put(correctBuf)
	
	// Get another buffer
	reusedBuf := pool.Get()
	assert.NotNil(t, reusedBuf)
	assert.Len(t, reusedBuf, bufferSize)
	
	// Verify stats show pool activity (sync.Pool behavior is non-deterministic)
	finalStats := pool.GetStats()
	assert.Greater(t, finalStats.Hits+finalStats.Misses, stats.Hits+stats.Misses)
}

func TestFloat32PoolConcurrency(t *testing.T) {
	const (
		bufferSize   = 144000 // Typical audio buffer size
		numWorkers   = 10
		opsPerWorker = 1000
	)

	pool, err := NewFloat32Pool(bufferSize)
	require.NoError(t, err)

	testPoolConcurrencyWithStats(t, bufferSize, numWorkers, opsPerWorker,
		func() interface{} { return pool.Get() },
		func(buf interface{}) { pool.Put(buf.([]float32)) },
		func(buf interface{}) {
			buffer := buf.([]float32)
			assert.Len(t, buffer, bufferSize)
			// Simulate some work with the buffer
			buffer[0] = float32(0)
			buffer[len(buffer)-1] = float32(1)
		},
		func() interface{} { return pool.GetStats() })
}

func TestFloat32PoolMemoryReuse(t *testing.T) {
	const bufferSize = 1024
	pool, err := NewFloat32Pool(bufferSize)
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

func TestFloat32PoolClear(t *testing.T) {
	t.Parallel()
	const bufferSize = 1024
	pool, err := NewFloat32Pool(bufferSize)
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

// TestFloat32PoolStress performs a stress test with many goroutines
func TestFloat32PoolStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	const (
		bufferSize = 144000 // Typical audio buffer size
		numWorkers = 50
		duration   = 1 // second
	)

	pool, err := NewFloat32Pool(bufferSize)
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
						buf[j] = float32(j) / float32(workerID+1)
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
	
	assert.Positive(t, ops)
	// Allow some variance due to sync.Pool's per-CPU sharding
	assert.InDelta(t, float64(ops), float64(stats.Hits+stats.Misses), float64(numWorkers*2))
	assert.Positive(t, stats.Hits) // Should have some reuse
}