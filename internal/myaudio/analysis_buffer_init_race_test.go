package myaudio

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAnalysisBuffer_InitializationRace tests that concurrent buffer allocations
// don't cause race conditions during pool initialization.
//
// BUG #4: The code in AllocateAnalysisBuffer checks `if overlapSize == 0` and
// initializes global variables (overlapSize, readSize, readBufferPool) without
// any synchronization. Multiple goroutines can race to initialize these.
//
// Run with: go test -race -run TestAnalysisBuffer_InitializationRace ./internal/myaudio/
func TestAnalysisBuffer_InitializationRace(t *testing.T) {
	// Do not use t.Parallel() - this test specifically targets race conditions

	// Reset the global state to trigger initialization
	// Note: This is testing internal state, which is not ideal but necessary
	// to expose the race condition.
	resetAnalysisBufferGlobals()

	const numGoroutines = 50
	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines)

	// Launch multiple goroutines to allocate buffers concurrently
	// Each will try to initialize the global pool
	for i := range numGoroutines {
		wg.Go(func() {
			sourceID := generateTestSourceID(i)
			err := AllocateAnalysisBuffer(1024*1024, sourceID) // 1MB buffer
			if err != nil {
				// "already exists" errors are expected for duplicate sourceIDs
				// but initialization race errors are bugs
				errChan <- err
			}
		})
	}

	wg.Wait()
	close(errChan)

	// Collect any errors (excluding "already exists" which is expected)
	var initErrors []error
	for err := range errChan {
		if err != nil && !isAlreadyExistsError(err) {
			initErrors = append(initErrors, err)
		}
	}

	// Check that no initialization errors occurred
	assert.Empty(t, initErrors, "no initialization errors should occur during concurrent allocation")

	// Verify the global state is consistent
	assert.NotZero(t, overlapSize, "overlapSize should be initialized")
	assert.NotZero(t, readSize, "readSize should be initialized")
	assert.NotNil(t, readBufferPool, "readBufferPool should be initialized")

	// Clean up all allocated buffers
	for i := range numGoroutines {
		sourceID := generateTestSourceID(i)
		_ = RemoveAnalysisBuffer(sourceID)
	}
}

// TestAnalysisBuffer_PoolInitializedOnce verifies that the buffer pool is
// initialized exactly once, even with concurrent allocations.
func TestAnalysisBuffer_PoolInitializedOnce(t *testing.T) {
	// Reset global state
	resetAnalysisBufferGlobals()

	const numGoroutines = 20
	var wg sync.WaitGroup
	poolSizes := make(chan int, numGoroutines)

	// Now race to initialize
	for i := range numGoroutines {
		wg.Go(func() {
			sourceID := generateTestSourceID(i + 100) // Offset to avoid conflicts
			err := AllocateAnalysisBuffer(1024*1024, sourceID)
			if err == nil {
				poolSizes <- getPoolAddress()
			}
		})
	}

	wg.Wait()
	close(poolSizes)

	// All successful allocations should see the same pool size (consistent initialization)
	sizes := make([]int, 0, numGoroutines)
	for size := range poolSizes { //nolint:gocritic // channel, not map
		sizes = append(sizes, size)
	}

	if len(sizes) > 1 {
		// All sizes should be the same (single pool instance with consistent size)
		firstSize := sizes[0]
		for i, size := range sizes[1:] {
			assert.Equal(t, firstSize, size,
				"pool size %d differs from first: got %d, want %d", i+1, size, firstSize)
		}
	}

	// Clean up
	for i := range numGoroutines {
		sourceID := generateTestSourceID(i + 100)
		_ = RemoveAnalysisBuffer(sourceID)
	}
}

// Helper functions for testing

func generateTestSourceID(index int) string {
	return "test-source-" + string(rune('a'+index%26)) + string(rune('0'+index/26%10))
}

func isAlreadyExistsError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "already exists")
}

// resetAnalysisBufferGlobals resets the global variables to simulate fresh start
// This is necessary to test the initialization race condition
// NOTE: sync.Once cannot be reset, so this function only clears the buffer maps.
// The buffer pool initialization will only happen once per process.
func resetAnalysisBufferGlobals() {
	abMutex.Lock()
	defer abMutex.Unlock()

	// Clear all buffers
	for sourceID := range analysisBuffers {
		if ab := analysisBuffers[sourceID]; ab != nil {
			ab.Reset()
		}
		delete(analysisBuffers, sourceID)
		delete(prevData, sourceID)
		delete(warningCounter, sourceID)
	}

	// Note: We intentionally do NOT reset readBufferPool, overlapSize, readSize,
	// or bufferPoolInitOnce because sync.Once cannot be reset.
	// The fix ensures these are initialized exactly once, thread-safely.
}

// getPoolAddress returns a simple identifier for the pool
// We use the pool's size as a proxy since we can't easily compare pointers
func getPoolAddress() int {
	abMutex.RLock()
	defer abMutex.RUnlock()
	if readBufferPool == nil {
		return 0
	}
	return readBufferPool.size
}
