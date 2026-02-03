package myaudio

import (
	"testing"

	"github.com/smallnest/ringbuffer"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// BenchmarkReadFromAnalysisBuffer_WithPool benchmarks the new implementation
// with buffer pooling enabled.
func BenchmarkReadFromAnalysisBuffer_WithPool(b *testing.B) {
	// Setup
	const (
		bufferCapacity = 1024 * 1024 * 10 // 10MB for benchmark
		testStream     = "benchmark_stream_pool"
		dataSize       = 48000 * 2 // 1 second of 16-bit audio at 48kHz
	)

	// Set up read size
	overlapSize = SecondsToBytes(0.5) // 0.5 second overlap
	readSize = conf.BufferSize - overlapSize

	// Initialize buffer pool
	var err error
	readBufferPool, err = NewBufferPool(readSize)
	if err != nil {
		b.Fatalf("Failed to create buffer pool: %v", err)
	}

	// Initialize test buffer
	abMutex.Lock()
	if analysisBuffers == nil {
		analysisBuffers = make(map[string]*ringbuffer.RingBuffer)
	}
	if prevData == nil {
		prevData = make(map[string][]byte)
	}

	ab := ringbuffer.New(bufferCapacity)
	analysisBuffers[testStream] = ab
	prevData[testStream] = nil
	abMutex.Unlock()

	// Pre-fill buffer with test data
	testData := make([]byte, dataSize)
	for i := range testData {
		testData[i] = byte(i % 256)
	}

	// Fill buffer to have enough data
	// We need to write enough data so that after reading readSize bytes,
	// we still have enough for processing
	totalNeeded := readSize*2 + conf.BufferSize
	written := 0
	for written < totalNeeded {
		abMutex.Lock()
		n, _ := ab.Write(testData)
		abMutex.Unlock()
		written += n
		if n == 0 {
			b.Fatalf("Failed to write data to buffer, capacity may be too small")
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	// Run benchmark
	i := 0
	for b.Loop() {
		// Read until we get data (might need multiple reads due to sliding window)
		var data []byte
		var err error
		for range 10 {
			data, err = ReadFromAnalysisBuffer(testStream)
			if err != nil {
				b.Fatalf("ReadFromAnalysisBuffer failed: %v", err)
			}
			if data != nil && len(data) == conf.BufferSize {
				break
			}
			// Write more data if needed
			abMutex.Lock()
			_, err = ab.Write(testData)
			abMutex.Unlock()
			if err != nil {
				b.Fatalf("Failed to write test data: %v", err)
			}
		}

		if data == nil {
			b.Fatalf("ReadFromAnalysisBuffer returned nil data at iteration %d after multiple attempts", i)
		}
		if len(data) != conf.BufferSize {
			b.Fatalf("ReadFromAnalysisBuffer returned wrong size: got %d, want %d", len(data), conf.BufferSize)
		}
		i++
	}

	// Get pool stats
	if readBufferPool != nil {
		stats := readBufferPool.GetStats()
		b.Logf("Buffer pool stats - Hits: %d, Misses: %d, Hit Rate: %.2f%%",
			stats.Hits, stats.Misses,
			float64(stats.Hits)/float64(stats.Hits+stats.Misses)*100)
	}

	// Cleanup
	abMutex.Lock()
	delete(analysisBuffers, testStream)
	delete(prevData, testStream)
	abMutex.Unlock()
	readBufferPool = nil
}

// BenchmarkComparison runs both implementations for easy comparison
func BenchmarkComparison(b *testing.B) {
	b.Run("Original", func(b *testing.B) {
		// Ensure pool is nil for original test
		readBufferPool = nil
		BenchmarkReadFromAnalysisBuffer_Original(b)
	})

	b.Run("WithPool", func(b *testing.B) {
		BenchmarkReadFromAnalysisBuffer_WithPool(b)
	})
}
