package myaudio

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

// BenchmarkRepeatedAllocationPrevention shows the benefit of preventing repeated allocations
func BenchmarkRepeatedAllocationPrevention(b *testing.B) {

	b.Run("without_fix_allows_double_alloc", func(b *testing.B) {
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			source := fmt.Sprintf("test_double_%d", i)

			// First allocation - succeeds
			_ = AllocateCaptureBuffer(60, 48000, 2, source)

			// Without the fix, this could succeed in a race condition
			// (simulating what would happen without proper locking)
			// In our case it will fail, but in production with races it could succeed

			// Clean up
			_ = RemoveCaptureBuffer(source)
		}
	})

	b.Run("with_fix_prevents_double_alloc", func(b *testing.B) {
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			source := fmt.Sprintf("test_safe_%d", i)

			// First allocation - succeeds
			_ = AllocateCaptureBufferIfNeeded(60, 48000, 2, source)

			// Second attempt - safely returns without allocation
			_ = AllocateCaptureBufferIfNeeded(60, 48000, 2, source)

			// Third attempt - still safe
			_ = AllocateCaptureBufferIfNeeded(60, 48000, 2, source)

			// Clean up
			_ = RemoveCaptureBuffer(source)
		}
	})
}

// BenchmarkProductionScenario simulates a real-world scenario
func BenchmarkProductionScenario(b *testing.B) {
	// Simulate 10 RTSP cameras
	sources := make([]string, 10)
	for i := range 10 {
		sources[i] = fmt.Sprintf("rtsp://camera%d.local/stream", i)
	}

	b.Run("startup_allocation", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			// Simulate startup - allocate all buffers
			for _, source := range sources {
				_ = AllocateCaptureBufferIfNeeded(60, 48000, 2, source)
			}

			// Simulate multiple initialization attempts (common in production)
			for _, source := range sources {
				_ = AllocateCaptureBufferIfNeeded(60, 48000, 2, source)
			}

			// Clean up for next iteration
			for _, source := range sources {
				_ = RemoveCaptureBuffer(source)
			}
		}
	})
}
