package myaudio

import (
	"fmt"
	"sync"
	"testing"
	"time"
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
		
		for i := 0; i < b.N; i++ {
			// Clean up from previous iteration
			for j := 0; j < numSources; j++ {
				source := fmt.Sprintf("old_source_%d", j)
				_ = RemoveCaptureBuffer(source)
			}
			
			// Create sources
			sources := make([]string, numSources)
			for j := 0; j < numSources; j++ {
				sources[j] = fmt.Sprintf("old_source_%d", j)
			}
			
			// Simulate old behavior
			_ = simulateOldBehavior(sources)
		}
	})
	
	b.Run("new_pattern_safe", func(b *testing.B) {
		b.ReportAllocs()
		
		for i := 0; i < b.N; i++ {
			// Clean up from previous iteration
			for j := 0; j < numSources; j++ {
				source := fmt.Sprintf("new_source_%d", j)
				_ = RemoveCaptureBuffer(source)
			}
			
			// Create sources
			sources := make([]string, numSources)
			for j := 0; j < numSources; j++ {
				sources[j] = fmt.Sprintf("new_source_%d", j)
			}
			
			// Simulate new behavior
			_ = simulateNewBehavior(sources)
		}
	})
}

// BenchmarkConcurrentAllocationPatterns tests concurrent allocation scenarios
func BenchmarkConcurrentAllocationPatterns(b *testing.B) {
	b.Run("old_pattern_concurrent", func(b *testing.B) {
		b.ReportAllocs()
		
		for i := 0; i < b.N; i++ {
			source := fmt.Sprintf("concurrent_old_%d", i)
			var wg sync.WaitGroup
			
			// Simulate 3 goroutines trying to allocate the same buffer
			for j := 0; j < 3; j++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if !HasCaptureBuffer(source) {
						_ = AllocateCaptureBuffer(60, 48000, 2, source)
					}
				}()
			}
			
			wg.Wait()
			_ = RemoveCaptureBuffer(source)
		}
	})
	
	b.Run("new_pattern_concurrent", func(b *testing.B) {
		b.ReportAllocs()
		
		for i := 0; i < b.N; i++ {
			source := fmt.Sprintf("concurrent_new_%d", i)
			var wg sync.WaitGroup
			
			// Simulate 3 goroutines trying to allocate the same buffer
			for j := 0; j < 3; j++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_ = AllocateCaptureBufferIfNeeded(60, 48000, 2, source)
				}()
			}
			
			wg.Wait()
			_ = RemoveCaptureBuffer(source)
		}
	})
}

// BenchmarkMemoryImpact measures the memory impact of repeated allocations
func BenchmarkMemoryImpact(b *testing.B) {
	// Expected buffer size: 60s * 48000Hz * 2 bytes = 5,760,000 bytes (~5.5MB)
	const expectedBufferSize = 5760000
	
	// Verify our calculation is correct
	actualSize := 60 * 48000 * 2
	if actualSize != expectedBufferSize {
		b.Errorf("Buffer size calculation mismatch: expected %d, got %d", expectedBufferSize, actualSize)
	}
	
	b.Run("single_allocation_memory", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		
		i := 0
		for b.Loop() {
			source := fmt.Sprintf("mem_single_%d", i)
			
			// Single allocation
			err := AllocateCaptureBuffer(60, 48000, 2, source)
			if err != nil {
				b.Fatalf("Allocation failed: %v", err)
			}
			
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
			if err != nil {
				b.Fatalf("First allocation failed: %v", err)
			}
			
			// Second allocation is prevented (no additional memory)
			err = AllocateCaptureBufferIfNeeded(60, 48000, 2, source)
			if err != nil {
				b.Fatalf("Second allocation failed: %v", err)
			}
			
			// Clean up
			_ = RemoveCaptureBuffer(source)
			i++
		}
	})
}