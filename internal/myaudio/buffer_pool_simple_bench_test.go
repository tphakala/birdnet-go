package myaudio

import (
	"testing"
)

// BenchmarkBufferAllocation_NoPool benchmarks direct buffer allocation
func BenchmarkBufferAllocation_NoPool(b *testing.B) {
	const bufferSize = 240768 // readSize from our calculations

	b.ReportAllocs()

	for b.Loop() {
		buf := make([]byte, bufferSize)
		// Simulate minimal work to prevent compiler optimization
		buf[0] = 1
		buf[len(buf)-1] = 2
	}
}

// BenchmarkBufferAllocation_WithPool benchmarks buffer pool allocation
func BenchmarkBufferAllocation_WithPool(b *testing.B) {
	const bufferSize = 240768 // readSize from our calculations

	pool, err := NewBufferPool(bufferSize)
	if err != nil {
		b.Fatalf("Failed to create pool: %v", err)
	}

	b.ReportAllocs()

	for b.Loop() {
		buf := pool.Get()
		// Simulate minimal work
		buf[0] = 1
		buf[len(buf)-1] = 2
		pool.Put(buf)
	}

	// Report pool stats
	stats := pool.GetStats()
	b.Logf("Pool stats - Hits: %d, Misses: %d, Hit Rate: %.2f%%",
		stats.Hits, stats.Misses,
		float64(stats.Hits)/float64(stats.Hits+stats.Misses)*100)
}

// BenchmarkBufferAllocation_WithPoolConcurrent benchmarks concurrent pool usage
func BenchmarkBufferAllocation_WithPoolConcurrent(b *testing.B) {
	const bufferSize = 240768

	pool, err := NewBufferPool(bufferSize)
	if err != nil {
		b.Fatalf("Failed to create pool: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buf := pool.Get()
			// Simulate work
			for i := range 100 {
				buf[i] = byte(i)
			}
			pool.Put(buf)
		}
	})

	// Report pool stats
	stats := pool.GetStats()
	b.Logf("Pool stats - Hits: %d, Misses: %d, Hit Rate: %.2f%%",
		stats.Hits, stats.Misses,
		float64(stats.Hits)/float64(stats.Hits+stats.Misses)*100)
}
