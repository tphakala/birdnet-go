package myaudio

import (
	"fmt"
	"runtime"
	"testing"
)

// benchResultCB prevents compiler optimizations for capture buffer benchmarks
var benchResultCB any

// BenchmarkNewCaptureBuffer benchmarks the raw buffer creation
func BenchmarkNewCaptureBuffer(b *testing.B) {
	testCases := []struct {
		name           string
		duration       int
		sampleRate     int
		bytesPerSample int
	}{
		{"small_buffer_16khz", 10, 16000, 2},
		{"standard_buffer_48khz", 60, 48000, 2},
		{"large_buffer_96khz", 60, 96000, 2},
		{"24bit_buffer", 60, 48000, 3},
		{"32bit_buffer", 60, 48000, 4},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				cb := NewCaptureBuffer(tc.duration, tc.sampleRate, tc.bytesPerSample, "bench_source")
				benchResultCB = cb
			}
		})
	}
}

// BenchmarkCaptureBufferLifecycle benchmarks the complete lifecycle
func BenchmarkCaptureBufferLifecycle(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	i := 0
	for b.Loop() {
		source := fmt.Sprintf("bench_%d", i)

		// Allocate
		err := AllocateCaptureBuffer(60, 48000, 2, source)
		if err != nil {
			b.Fatalf("Allocation failed: %v", err)
		}

		// Use the buffer
		data := make([]byte, 4096)
		err = WriteToCaptureBuffer(source, data)
		if err != nil {
			b.Fatalf("Write failed: %v", err)
		}

		// Remove
		err = RemoveCaptureBuffer(source)
		if err != nil {
			b.Fatalf("Removal failed: %v", err)
		}
		i++
	}
}

// BenchmarkAllocateCaptureBuffer benchmarks the allocation function
func BenchmarkAllocateCaptureBuffer(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	i := 0
	for b.Loop() {
		source := fmt.Sprintf("bench_alloc_%d", i)

		err := AllocateCaptureBuffer(60, 48000, 2, source)
		if err != nil {
			b.Fatalf("Allocation failed: %v", err)
		}

		// Clean up to avoid memory exhaustion
		err = RemoveCaptureBuffer(source)
		if err != nil {
			b.Fatalf("Removal failed: %v", err)
		}
		i++
	}
}

// BenchmarkAllocateCaptureBufferIfNeeded benchmarks the conditional allocation
func BenchmarkAllocateCaptureBufferIfNeeded(b *testing.B) {
	source := "bench_if_needed"

	// Pre-allocate the buffer
	err := AllocateCaptureBuffer(60, 48000, 2, source)
	if err != nil {
		b.Fatalf("Initial allocation failed: %v", err)
	}
	defer func() {
		_ = RemoveCaptureBuffer(source)
	}()

	b.ReportAllocs()
	b.ResetTimer()

	// This should be very fast as it just checks existence
	for b.Loop() {
		err := AllocateCaptureBufferIfNeeded(60, 48000, 2, source)
		if err != nil {
			b.Fatalf("AllocateCaptureBufferIfNeeded failed: %v", err)
		}
	}
}

// BenchmarkCaptureBufferWrite benchmarks writing to capture buffer
func BenchmarkCaptureBufferWrite(b *testing.B) {
	source := "bench_write"

	// Pre-allocate the buffer
	err := AllocateCaptureBuffer(60, 48000, 2, source)
	if err != nil {
		b.Fatalf("Allocation failed: %v", err)
	}
	defer func() {
		_ = RemoveCaptureBuffer(source)
	}()

	// Create test data of various sizes
	testCases := []struct {
		name string
		size int
	}{
		{"1KB", 1024},
		{"4KB", 4096},
		{"16KB", 16384},
		{"64KB", 65536},
		{"256KB", 262144},
	}

	for _, tc := range testCases {
		data := make([]byte, tc.size)

		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				err := WriteToCaptureBuffer(source, data)
				if err != nil {
					b.Fatalf("Write failed: %v", err)
				}
			}
		})
	}
}

// BenchmarkConcurrentCaptureBufferAccess benchmarks concurrent buffer operations
func BenchmarkConcurrentCaptureBufferAccess(b *testing.B) {
	numSources := 10
	sources := make([]string, numSources)

	// Pre-allocate buffers
	for i := range numSources {
		sources[i] = fmt.Sprintf("concurrent_%d", i)
		err := AllocateCaptureBuffer(60, 48000, 2, sources[i])
		if err != nil {
			b.Fatalf("Allocation failed: %v", err)
		}
	}

	// Clean up after benchmark
	defer func() {
		for _, source := range sources {
			_ = RemoveCaptureBuffer(source)
		}
	}()

	data := make([]byte, 4096)
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			source := sources[i%numSources]
			err := WriteToCaptureBuffer(source, data)
			if err != nil {
				b.Fatalf("Write failed: %v", err)
			}
			i++
		}
	})
}

// BenchmarkHasCaptureBuffer benchmarks buffer existence check
func BenchmarkHasCaptureBuffer(b *testing.B) {
	// Create buffers with varying numbers
	numBuffers := []int{10, 100, 1000}

	for _, n := range numBuffers {
		b.Run(fmt.Sprintf("%d_buffers", n), func(b *testing.B) {
			// Setup buffers
			sources := make([]string, n)
			for i := range n {
				sources[i] = fmt.Sprintf("has_check_%d", i)
				err := AllocateCaptureBuffer(60, 48000, 2, sources[i])
				if err != nil {
					b.Fatalf("Allocation failed: %v", err)
				}
			}

			// Clean up after benchmark
			defer func() {
				for _, source := range sources {
					_ = RemoveCaptureBuffer(source)
				}
			}()

			testSource := sources[n/2] // Check middle element
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				exists := HasCaptureBuffer(testSource)
				benchResultCB = exists
			}
		})
	}
}

// BenchmarkMemoryUsage estimates memory usage for different buffer configurations
func BenchmarkMemoryUsage(b *testing.B) {
	configurations := []struct {
		name           string
		numSources     int
		duration       int
		sampleRate     int
		bytesPerSample int
	}{
		{"10_cameras_SD", 10, 60, 16000, 2},
		{"10_cameras_HD", 10, 60, 48000, 2},
		{"50_cameras_SD", 50, 60, 16000, 2},
		{"50_cameras_HD", 50, 60, 48000, 2},
		{"100_cameras_SD", 100, 60, 16000, 2},
	}

	for _, cfg := range configurations {
		b.Run(cfg.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				sources := make([]string, 0, cfg.numSources)

				// Force GC to get clean baseline
				runtime.GC()
				runtime.GC() // Run twice to ensure finalizers run

				// Measure memory before allocation
				var memStatsBefore runtime.MemStats
				runtime.ReadMemStats(&memStatsBefore)

				// Allocate buffers
				for j := range cfg.numSources {
					source := fmt.Sprintf("mem_test_%d_%d", i, j)
					sources = append(sources, source)
					err := AllocateCaptureBuffer(cfg.duration, cfg.sampleRate, cfg.bytesPerSample, source)
					if err != nil {
						b.Fatalf("Allocation failed: %v", err)
					}
				}

				// Force GC to account for all allocations
				runtime.GC()

				// Measure memory after allocation
				var memStatsAfter runtime.MemStats
				runtime.ReadMemStats(&memStatsAfter)

				// Calculate memory usage
				memUsed := memStatsAfter.Alloc - memStatsBefore.Alloc
				expectedSize := uint64(cfg.numSources * cfg.duration * cfg.sampleRate * cfg.bytesPerSample) //nolint:gosec // G115: test config values are small positive values

				// Only log on first iteration to avoid spam
				if i == 0 {
					b.Logf("Configuration: %s", cfg.name)
					b.Logf("Expected total buffer size: %.2f MB", float64(expectedSize)/1024/1024)
					b.Logf("Actual memory allocated: %.2f MB", float64(memUsed)/1024/1024)
					b.Logf("Memory overhead: %.2f%%", float64(memUsed-expectedSize)/float64(expectedSize)*100)
				}

				// Clean up
				for _, source := range sources {
					_ = RemoveCaptureBuffer(source)
				}
			}
		})
	}
}
