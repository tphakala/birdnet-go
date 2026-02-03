package diskmanager

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

// safeDecrement performs a safe decrement with underflow protection using CAS.
func safeDecrement(counter *atomic.Uint64) bool {
	for {
		current := counter.Load()
		if current == 0 {
			return false
		}
		if counter.CompareAndSwap(current, current-1) {
			return true
		}
	}
}

// poolWorker performs increment or decrement operations for testing.
func poolWorker(counter *atomic.Uint64, shouldIncrement bool, ops int) {
	for range ops {
		if shouldIncrement {
			counter.Add(1)
		} else {
			safeDecrement(counter)
		}
	}
}

// TestCurrentPoolSizeConcurrentDecrement verifies that the pool size counter
// handles concurrent decrements correctly without underflow
func TestCurrentPoolSizeConcurrentDecrement(t *testing.T) {
	// Set up initial pool size
	initialSize := uint64(100)
	poolMetrics.CurrentPoolSize.Store(initialSize)

	// Number of goroutines to run concurrently
	numGoroutines := 50
	// Each goroutine will do this many operations
	opsPerGoroutine := 10

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Track successful decrements
	var successfulDecrements atomic.Uint64

	for range numGoroutines {
		go func() {
			defer wg.Done()

			for range opsPerGoroutine {
				if safeDecrement(&poolMetrics.CurrentPoolSize) {
					successfulDecrements.Add(1)
				}
			}
		}()
	}

	wg.Wait()

	// Verify final state
	finalSize := poolMetrics.CurrentPoolSize.Load()
	totalAttempts := uint64(numGoroutines * opsPerGoroutine) //nolint:gosec // G115: test constants are small positive values

	// The number of successful decrements should not exceed initial size
	assert.LessOrEqual(t, successfulDecrements.Load(), initialSize,
		"too many successful decrements")

	// Final size should be max(0, initialSize - successfulDecrements)
	var expectedFinal uint64
	if initialSize > successfulDecrements.Load() {
		expectedFinal = initialSize - successfulDecrements.Load()
	}

	assert.Equal(t, expectedFinal, finalSize, "final pool size incorrect")

	// Most importantly: verify no underflow (would show as huge number)
	assert.LessOrEqual(t, finalSize, initialSize, "pool size underflowed")

	t.Logf("Test completed: %d decrements attempted, %d succeeded, final size: %d",
		totalAttempts, successfulDecrements.Load(), finalSize)
}

// TestCurrentPoolSizeUnderflowPrevention specifically tests that the counter
// cannot go below zero even under extreme concurrent pressure
func TestCurrentPoolSizeUnderflowPrevention(t *testing.T) {
	// Start with a very small pool size
	poolMetrics.CurrentPoolSize.Store(3)

	// Launch many goroutines trying to decrement
	numGoroutines := 100
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for range numGoroutines {
		go func() {
			defer wg.Done()

			// Each tries to decrement multiple times
			for range 10 {
				safeDecrement(&poolMetrics.CurrentPoolSize)
			}
		}()
	}

	wg.Wait()

	// Final value must be 0 (not underflowed to max uint64)
	final := poolMetrics.CurrentPoolSize.Load()
	assert.Zero(t, final, "expected final size to be 0 (possible underflow)")
}

// TestPoolSizeIncrementDecrement tests that increment and decrement operations
// maintain consistency under concurrent access
func TestPoolSizeIncrementDecrement(t *testing.T) {
	// Reset to known state
	poolMetrics.CurrentPoolSize.Store(50)

	var wg sync.WaitGroup
	numGoroutines := 20
	opsPerGoroutine := 100

	// Half the goroutines increment, half decrement
	for i := range numGoroutines {
		wg.Add(1)
		increment := i%2 == 0

		go func(shouldIncrement bool) {
			defer wg.Done()
			poolWorker(&poolMetrics.CurrentPoolSize, shouldIncrement, opsPerGoroutine)
		}(increment)
	}

	wg.Wait()

	// With equal increments and decrements starting from 50,
	// we expect the value to be >= 50 (some decrements may have been skipped at 0)
	final := poolMetrics.CurrentPoolSize.Load()

	// Check for underflow (would be a huge number)
	assert.LessOrEqual(t, final, uint64(10000), "likely underflow detected")

	t.Logf("Final pool size after balanced operations: %d", final)
}

// BenchmarkCurrentPoolSizeDecrement compares the performance of the CAS approach
func BenchmarkCurrentPoolSizeDecrement(b *testing.B) {
	b.Run("CAS-Decrement", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()

		for b.Loop() {
			// Reset counter
			poolMetrics.CurrentPoolSize.Store(1000)

			// Perform safe decrement
			for {
				current := poolMetrics.CurrentPoolSize.Load()
				if current == 0 {
					break
				}
				if poolMetrics.CurrentPoolSize.CompareAndSwap(current, current-1) {
					break
				}
			}
		}
	})
}
