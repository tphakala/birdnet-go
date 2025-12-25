package myaudio

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// simulateOldBehavior simulates the old pattern where buffers could be allocated multiple times
func simulateOldBehavior(sources []string) error {
	// Simulate multiple initialization paths trying to allocate
	for _, source := range sources {
		// Path 1: InitCaptureBuffers
		if !HasCaptureBuffer(source) {
			if err := AllocateCaptureBuffer(60, 48000, 2, source); err != nil {
				// Ignore "already exists" errors in simulation
				continue
			}
		}

		// Simulate some time passing
		time.Sleep(time.Microsecond)

		// Path 2: CaptureAudio initialization
		if !HasCaptureBuffer(source) {
			if err := AllocateCaptureBuffer(60, 48000, 2, source); err != nil {
				continue
			}
		}

		// Path 3: ReconfigureRTSPStreams
		if !HasCaptureBuffer(source) {
			if err := AllocateCaptureBuffer(60, 48000, 2, source); err != nil {
				continue
			}
		}
	}
	return nil
}

// simulateNewBehavior simulates the new pattern with AllocateCaptureBufferIfNeeded
func simulateNewBehavior(sources []string) error {
	// Simulate multiple initialization paths using safe allocation
	for _, source := range sources {
		// Path 1: InitCaptureBuffers
		if err := AllocateCaptureBufferIfNeeded(60, 48000, 2, source); err != nil {
			return err
		}

		// Simulate some time passing
		time.Sleep(time.Microsecond)

		// Path 2: CaptureAudio initialization
		if err := AllocateCaptureBufferIfNeeded(60, 48000, 2, source); err != nil {
			return err
		}

		// Path 3: ReconfigureRTSPStreams
		if err := AllocateCaptureBufferIfNeeded(60, 48000, 2, source); err != nil {
			return err
		}
	}
	return nil
}

// BenchmarkCaptureBufferAllocationComparison compares old vs new allocation patterns
func BenchmarkCaptureBufferAllocationComparison(b *testing.B) {
	numSources := 10

	b.Run("old_pattern_race_prone", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		i := 0
		for b.Loop() {
			// Clean up from previous iteration
			for j := range numSources {
				source := fmt.Sprintf("old_source_%d_%d", i, j)
				_ = RemoveCaptureBuffer(source)
			}

			// Create sources
			sources := make([]string, numSources)
			for j := range numSources {
				sources[j] = fmt.Sprintf("old_source_%d_%d", i, j)
			}

			// Simulate old behavior
			_ = simulateOldBehavior(sources)
			i++
		}
	})

	b.Run("new_pattern_safe", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		i := 0
		for b.Loop() {
			// Clean up from previous iteration
			for j := range numSources {
				source := fmt.Sprintf("new_source_%d_%d", i, j)
				_ = RemoveCaptureBuffer(source)
			}

			// Create sources
			sources := make([]string, numSources)
			for j := range numSources {
				sources[j] = fmt.Sprintf("new_source_%d_%d", i, j)
			}

			// Simulate new behavior
			_ = simulateNewBehavior(sources)
			i++
		}
	})
}

// BenchmarkConcurrentAllocationPatterns tests concurrent allocation scenarios
func BenchmarkConcurrentAllocationPatterns(b *testing.B) {
	b.Run("old_pattern_concurrent", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		i := 0
		for b.Loop() {
			source := fmt.Sprintf("concurrent_old_%d", i)
			var wg sync.WaitGroup

			// Simulate 3 goroutines trying to allocate the same buffer
			for range 3 {
				wg.Go(func() {
					if !HasCaptureBuffer(source) {
						_ = AllocateCaptureBuffer(60, 48000, 2, source)
					}
				})
			}

			wg.Wait()
			_ = RemoveCaptureBuffer(source)
			i++
		}
	})

	b.Run("new_pattern_concurrent", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		i := 0
		for b.Loop() {
			source := fmt.Sprintf("concurrent_new_%d", i)
			var wg sync.WaitGroup

			// Simulate 3 goroutines trying to allocate the same buffer
			for range 3 {
				wg.Go(func() {
					_ = AllocateCaptureBufferIfNeeded(60, 48000, 2, source)
				})
			}

			wg.Wait()
			_ = RemoveCaptureBuffer(source)
			i++
		}
	})
}

// BenchmarkMemoryImpact measures the memory impact of repeated allocations
func BenchmarkMemoryImpact(b *testing.B) {
	b.Run("single_allocation_memory", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		i := 0
		for b.Loop() {
			source := fmt.Sprintf("mem_single_%d", i)

			// Single allocation
			err := AllocateCaptureBuffer(60, 48000, 2, source)
			require.NoError(b, err, "Allocation failed")

			// Clean up
			_ = RemoveCaptureBuffer(source)
			i++
		}
	})

	b.Run("prevented_double_allocation", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		i := 0
		for b.Loop() {
			source := fmt.Sprintf("mem_prevented_%d", i)

			// First allocation succeeds
			err := AllocateCaptureBufferIfNeeded(60, 48000, 2, source)
			require.NoError(b, err, "First allocation failed")

			// Second allocation is prevented (no additional memory)
			err = AllocateCaptureBufferIfNeeded(60, 48000, 2, source)
			require.NoError(b, err, "Second allocation failed")

			// Clean up
			_ = RemoveCaptureBuffer(source)
			i++
		}
	})
}
