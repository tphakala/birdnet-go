package myaudio

import (
	"testing"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// Global variable to prevent compiler optimizations
var benchResult []float32

// BenchmarkConvert16BitToFloat32_Original benchmarks the original implementation
// without float32 pool to establish a baseline.
func BenchmarkConvert16BitToFloat32_Original(b *testing.B) {
	// Create test data - 3 seconds of 16-bit audio at 48kHz
	testData := make([]byte, conf.BufferSize)
	
	// Fill with realistic audio pattern
	for i := 0; i < len(testData); i += 2 {
		// Simulate audio wave
		value := int16(i % 32768) //nolint:gosec // G115: i%32768 is always in int16 range
		testData[i] = byte(value & 0xFF)
		testData[i+1] = byte(value >> 8)
	}
	
	// Temporarily disable pool for baseline test
	originalPool := float32Pool
	float32Pool = nil
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for b.Loop() {
		result := convert16BitToFloat32(testData)
		benchResult = result // Prevent compiler optimization
	}
	
	// Restore pool
	float32Pool = originalPool
}

// BenchmarkConvert16BitToFloat32_WithPool benchmarks the implementation
// with float32 pool enabled.
func BenchmarkConvert16BitToFloat32_WithPool(b *testing.B) {
	// Initialize pool
	if float32Pool == nil {
		if err := InitFloat32Pool(); err != nil {
			b.Fatalf("Failed to initialize float32 pool: %v", err)
		}
	}
	
	// Create test data - 3 seconds of 16-bit audio at 48kHz
	testData := make([]byte, conf.BufferSize)
	
	// Fill with realistic audio pattern
	for i := 0; i < len(testData); i += 2 {
		// Simulate audio wave
		value := int16(i % 32768) //nolint:gosec // G115: i%32768 is always in int16 range
		testData[i] = byte(value & 0xFF)
		testData[i+1] = byte(value >> 8)
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for b.Loop() {
		result := convert16BitToFloat32(testData)
		// Return to pool to simulate real usage
		ReturnFloat32Buffer(result)
		benchResult = result // Prevent compiler optimization
	}
	
	// Log pool statistics
	if float32Pool != nil {
		stats := float32Pool.GetStats()
		b.Logf("Pool stats - Hits: %d, Misses: %d, Hit Rate: %.2f%%",
			stats.Hits, stats.Misses,
			float64(stats.Hits)/float64(stats.Hits+stats.Misses)*100)
	}
}

// BenchmarkConvert16BitToFloat32_Various_Sizes benchmarks conversion
// with different buffer sizes to understand performance characteristics.
func BenchmarkConvert16BitToFloat32_Various_Sizes(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"1KB", 512},          // 512 samples = 1KB
		{"10KB", 5120},        // 5120 samples = 10KB
		{"100KB", 51200},      // 51200 samples = 100KB
		{"Standard", 144000},  // Standard buffer size (288KB)
		{"1MB", 524288},       // 524288 samples = 1MB
	}
	
	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			testData := make([]byte, sz.size*2) // 2 bytes per sample
			
			// Fill with test pattern
			for i := 0; i < len(testData); i += 2 {
				value := int16(i % 32768) //nolint:gosec // G115: i%32768 is always in int16 range
				testData[i] = byte(value & 0xFF)
				testData[i+1] = byte(value >> 8)
			}
			
			b.ResetTimer()
			b.ReportAllocs()
			
			for b.Loop() {
				result := convert16BitToFloat32(testData)
				benchResult = result
			}
		})
	}
}

// BenchmarkConvert16BitToFloat32_Concurrent benchmarks concurrent conversion
// to measure performance under load.
func BenchmarkConvert16BitToFloat32_Concurrent(b *testing.B) {
	// Initialize pool
	if float32Pool == nil {
		if err := InitFloat32Pool(); err != nil {
			b.Fatalf("Failed to initialize float32 pool: %v", err)
		}
	}
	
	// Create test data
	testData := make([]byte, conf.BufferSize)
	for i := 0; i < len(testData); i += 2 {
		value := int16(i % 32768) //nolint:gosec // G115: i%32768 is always in int16 range
		testData[i] = byte(value & 0xFF)
		testData[i+1] = byte(value >> 8)
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			result := convert16BitToFloat32(testData)
			ReturnFloat32Buffer(result)
			benchResult = result
		}
	})
	
	// Log pool statistics
	if float32Pool != nil {
		stats := float32Pool.GetStats()
		b.Logf("Pool stats - Hits: %d, Misses: %d, Hit Rate: %.2f%%",
			stats.Hits, stats.Misses,
			float64(stats.Hits)/float64(stats.Hits+stats.Misses)*100)
	}
}

// BenchmarkProcessData_Integration benchmarks the full audio processing pipeline
// to measure the overall impact of float32 pooling.
func BenchmarkProcessData_Integration(b *testing.B) {
	// This would require setting up a full BirdNET instance
	// Skipping for now as it's more of an integration test
	b.Skip("Integration benchmark requires full BirdNET setup")
}

// benchmarkOriginalConversion runs the baseline conversion benchmark
func benchmarkOriginalConversion(b *testing.B) {
	// Create test data - 3 seconds of 16-bit audio at 48kHz
	testData := make([]byte, conf.BufferSize)
	
	// Fill with realistic audio pattern
	for i := 0; i < len(testData); i += 2 {
		// Simulate audio wave
		value := int16(i % 32768) //nolint:gosec // G115: i%32768 is always in int16 range
		testData[i] = byte(value & 0xFF)
		testData[i+1] = byte(value >> 8)
	}
	
	// Temporarily disable pool for baseline test
	originalPool := float32Pool
	float32Pool = nil
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for b.Loop() {
		result := convert16BitToFloat32(testData)
		benchResult = result // Prevent compiler optimization
	}
	
	// Restore pool
	float32Pool = originalPool
}

// benchmarkPooledConversion runs the pooled conversion benchmark
func benchmarkPooledConversion(b *testing.B) {
	// Initialize pool
	if float32Pool == nil {
		if err := InitFloat32Pool(); err != nil {
			b.Fatalf("Failed to initialize float32 pool: %v", err)
		}
	}
	
	// Create test data - 3 seconds of 16-bit audio at 48kHz
	testData := make([]byte, conf.BufferSize)
	
	// Fill with realistic audio pattern
	for i := 0; i < len(testData); i += 2 {
		// Simulate audio wave
		value := int16(i % 32768) //nolint:gosec // G115: i%32768 is always in int16 range
		testData[i] = byte(value & 0xFF)
		testData[i+1] = byte(value >> 8)
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for b.Loop() {
		result := convert16BitToFloat32(testData)
		// Return to pool to simulate real usage
		ReturnFloat32Buffer(result)
		benchResult = result // Prevent compiler optimization
	}
	
	// Log pool statistics
	if float32Pool != nil {
		stats := float32Pool.GetStats()
		b.Logf("Pool stats - Hits: %d, Misses: %d, Hit Rate: %.2f%%",
			stats.Hits, stats.Misses,
			float64(stats.Hits)/float64(stats.Hits+stats.Misses)*100)
	}
}

// BenchmarkAudioConversionComparison runs both versions side by side
func BenchmarkAudioConversionComparison(b *testing.B) {
	b.Run("Original", benchmarkOriginalConversion)
	b.Run("WithPool", benchmarkPooledConversion)
}