// Package buffer provides pool implementations for audio buffer management.
package buffer_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
)

// TestBytePool_GetPut verifies that Get returns a buffer of the correct size and
// that Put followed by Get reuses the same underlying buffer.
func TestBytePool_GetPut(t *testing.T) {
	t.Parallel()

	const size = 1024
	pool, err := buffer.NewBytePool(size)
	require.NoError(t, err)

	// Get returns a buffer of the correct size.
	buf := pool.Get()
	assert.Len(t, buf, size)

	// Mark the buffer with a sentinel value, return it, and get it again.
	buf[0] = 0xAB
	pool.Put(buf)

	buf2 := pool.Get()
	assert.Len(t, buf2, size)
	// sync.Pool may or may not reuse the buffer, but size must still be correct.
}

// TestBytePool_Stats verifies the Hits, Misses, and Discarded counters.
func TestBytePool_Stats(t *testing.T) {
	t.Parallel()

	const size = 512
	pool, err := buffer.NewBytePool(size)
	require.NoError(t, err)

	// First Get — pool is empty, so this is a miss (new allocation).
	buf1 := pool.Get()
	pool.Put(buf1)

	// Second Get — pool now has a buffer, so this is a hit.
	buf2 := pool.Get()
	pool.Put(buf2)

	stats := pool.GetStats()
	// At least one miss (the initial allocation) must have been recorded.
	assert.GreaterOrEqual(t, stats.Misses, uint64(1), "expected at least one miss")
	// Total gets == hits + misses.
	assert.Equal(t, uint64(2), stats.Hits+stats.Misses, "hits + misses must equal total gets")
}

// TestBytePool_SizeMismatch verifies that returning a wrong-sized buffer
// increments the Discarded counter instead of polluting the pool.
func TestBytePool_SizeMismatch(t *testing.T) {
	t.Parallel()

	const size = 256
	pool, err := buffer.NewBytePool(size)
	require.NoError(t, err)

	// Put a buffer of a different size — must be discarded.
	wrongSize := make([]byte, size*2)
	pool.Put(wrongSize)

	stats := pool.GetStats()
	assert.Equal(t, uint64(1), stats.Discarded, "wrong-sized buffer must be discarded")
}

// TestBytePool_InvalidSize verifies that NewBytePool returns an error for invalid sizes.
func TestBytePool_InvalidSize(t *testing.T) {
	t.Parallel()

	_, err := buffer.NewBytePool(0)
	require.Error(t, err)

	_, err = buffer.NewBytePool(-1)
	require.Error(t, err)
}

// TestFloat32Pool_GetPut verifies that Get returns a slice of the correct length
// and that Put followed by Get works correctly.
func TestFloat32Pool_GetPut(t *testing.T) {
	t.Parallel()

	const size = 512
	pool, err := buffer.NewFloat32Pool(size)
	require.NoError(t, err)

	buf := pool.Get()
	assert.Len(t, buf, size)

	pool.Put(buf)

	buf2 := pool.Get()
	assert.Len(t, buf2, size)
}

// TestFloat32Pool_Stats verifies that pool stats are tracked for float32 slices.
func TestFloat32Pool_Stats(t *testing.T) {
	t.Parallel()

	const size = 256
	pool, err := buffer.NewFloat32Pool(size)
	require.NoError(t, err)

	buf1 := pool.Get()
	pool.Put(buf1)

	buf2 := pool.Get()
	pool.Put(buf2)

	stats := pool.GetStats()
	assert.GreaterOrEqual(t, stats.Misses, uint64(1), "expected at least one miss")
	assert.Equal(t, uint64(2), stats.Hits+stats.Misses, "hits + misses must equal total gets")
}

// TestFloat32Pool_SizeMismatch verifies that returning a wrong-sized float32 slice
// increments the Discarded counter.
func TestFloat32Pool_SizeMismatch(t *testing.T) {
	t.Parallel()

	const size = 128
	pool, err := buffer.NewFloat32Pool(size)
	require.NoError(t, err)

	wrongSize := make([]float32, size+1)
	pool.Put(wrongSize)

	stats := pool.GetStats()
	assert.Equal(t, uint64(1), stats.Discarded, "wrong-sized slice must be discarded")
}

// TestFloat32Pool_NilPut verifies that putting nil into the pool increments Discarded.
func TestFloat32Pool_NilPut(t *testing.T) {
	t.Parallel()

	pool, err := buffer.NewFloat32Pool(64)
	require.NoError(t, err)

	pool.Put(nil)

	stats := pool.GetStats()
	assert.Equal(t, uint64(1), stats.Discarded)
}

// TestFloat32Pool_Clear verifies that Clear resets the pool so subsequent Gets
// allocate new buffers (stats counters continue from where they left off).
func TestFloat32Pool_Clear(t *testing.T) {
	t.Parallel()

	const size = 64
	pool, err := buffer.NewFloat32Pool(size)
	require.NoError(t, err)

	// Prime the pool with one Get+Put cycle.
	buf := pool.Get()
	pool.Put(buf)

	// Clear should drain the pool without panicking.
	pool.Clear()

	// After clear, next Get must still return a valid buffer.
	buf2 := pool.Get()
	assert.Len(t, buf2, size)
}

// TestBytePool_Clear verifies that Clear resets the byte pool.
func TestBytePool_Clear(t *testing.T) {
	t.Parallel()

	const size = 128
	pool, err := buffer.NewBytePool(size)
	require.NoError(t, err)

	buf := pool.Get()
	pool.Put(buf)

	pool.Clear()

	buf2 := pool.Get()
	assert.Len(t, buf2, size)
}

// TestBytePool_ClearConcurrent verifies that concurrent Get, Put, and Clear
// calls do not race. This test exercises the mutex protection added to Clear.
// The -race detector will flag any data race if the mutex is missing.
func TestBytePool_ClearConcurrent(t *testing.T) {
	t.Parallel()

	const (
		size       = 256
		goroutines = 10
		iterations = 100
	)

	pool, err := buffer.NewBytePool(size)
	require.NoError(t, err)

	// Collect buffer sizes from goroutines to verify in the main goroutine.
	results := make(chan int, goroutines*iterations)

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range iterations {
				buf := pool.Get()
				results <- len(buf)
				pool.Put(buf)
			}
		})
	}

	// Concurrently clear the pool while Get/Put are running.
	wg.Go(func() {
		for range iterations {
			pool.Clear()
		}
	})

	wg.Wait()
	close(results)

	for bufLen := range results {
		assert.Equal(t, size, bufLen, "all buffers must have correct size")
	}
}

// TestFloat32Pool_ClearConcurrent verifies that concurrent Get, Put, and Clear
// calls on Float32Pool do not race.
func TestFloat32Pool_ClearConcurrent(t *testing.T) {
	t.Parallel()

	const (
		size       = 128
		goroutines = 10
		iterations = 100
	)

	pool, err := buffer.NewFloat32Pool(size)
	require.NoError(t, err)

	// Collect slice lengths from goroutines to verify in the main goroutine.
	results := make(chan int, goroutines*iterations)

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range iterations {
				buf := pool.Get()
				results <- len(buf)
				pool.Put(buf)
			}
		})
	}

	// Concurrently clear the pool while Get/Put are running.
	wg.Go(func() {
		for range iterations {
			pool.Clear()
		}
	})

	wg.Wait()
	close(results)

	for bufLen := range results {
		assert.Equal(t, size, bufLen, "all slices must have correct size")
	}
}
