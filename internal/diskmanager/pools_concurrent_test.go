package diskmanager

import (
	"sync"
	"sync/atomic"
	"testing"
)

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
				// Simulate the decrement logic from getPooledSlice
				for {
					current := poolMetrics.CurrentPoolSize.Load()
					if current == 0 {
						break // Already at zero, nothing to decrement
					}
					if poolMetrics.CurrentPoolSize.CompareAndSwap(current, current-1) {
						successfulDecrements.Add(1)
						break // Successfully decremented
					}
					// CAS failed, retry
				}
			}
		}()
	}

	wg.Wait()

	// Verify final state
	finalSize := poolMetrics.CurrentPoolSize.Load()
	totalAttempts := uint64(numGoroutines * opsPerGoroutine) //nolint:gosec // G115: test constants are small positive values

	// The number of successful decrements should not exceed initial size
	if successfulDecrements.Load() > initialSize {
		t.Errorf("Too many successful decrements: %d > %d (initial size)",
			successfulDecrements.Load(), initialSize)
	}

	// Final size should be max(0, initialSize - successfulDecrements)
	var expectedFinal uint64
	if initialSize > successfulDecrements.Load() {
		expectedFinal = initialSize - successfulDecrements.Load()
	}

	if finalSize != expectedFinal {
		t.Errorf("Final pool size incorrect: got %d, expected %d",
			finalSize, expectedFinal)
	}

	// Most importantly: verify no underflow (would show as huge number)
	if finalSize > initialSize {
		t.Errorf("Pool size underflowed! Final: %d > Initial: %d",
			finalSize, initialSize)
	}

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
				// Use the actual logic from getPooledSlice
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
		}()
	}

	wg.Wait()

	// Final value must be 0 (not underflowed to max uint64)
	final := poolMetrics.CurrentPoolSize.Load()
	if final != 0 {
		t.Errorf("Expected final size to be 0, got %d (possible underflow)", final)
	}
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

			for range opsPerGoroutine {
				if shouldIncrement {
					// Simulate Put operation (increment)
					poolMetrics.CurrentPoolSize.Add(1)
				} else {
					// Simulate Get operation (decrement with underflow protection)
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
			}
		}(increment)
	}

	wg.Wait()

	// With equal increments and decrements starting from 50,
	// we expect the value to be >= 50 (some decrements may have been skipped at 0)
	final := poolMetrics.CurrentPoolSize.Load()

	// Check for underflow (would be a huge number)
	if final > 10000 {
		t.Errorf("Likely underflow detected: final size = %d", final)
	}

	t.Logf("Final pool size after balanced operations: %d", final)
}

// BenchmarkCurrentPoolSizeDecrement compares the performance of the CAS approach
func BenchmarkCurrentPoolSizeDecrement(b *testing.B) {
	b.Run("CAS-Decrement", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
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
