package myaudio

import (
	"fmt"
	"testing"

	"github.com/smallnest/ringbuffer"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// BenchmarkReadFromAnalysisBuffer_Original benchmarks the current implementation
// without buffer pooling to establish a baseline.
func BenchmarkReadFromAnalysisBuffer_Original(b *testing.B) {
	// Setup
	const (
		bufferCapacity = 1024 * 1024 * 10 // 10MB for benchmark
		testStream     = "benchmark_stream"
		dataSize       = 48000 * 2 // 1 second of 16-bit audio at 48kHz
	)

	// Set up read size based on constants
	// conf.BufferSize is already defined as a constant for 3 seconds of audio
	overlapSize = SecondsToBytes(0.5) // 0.5 second overlap
	readSize = conf.BufferSize - overlapSize

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

	// Cleanup
	abMutex.Lock()
	delete(analysisBuffers, testStream)
	delete(prevData, testStream)
	abMutex.Unlock()
}

// BenchmarkReadFromAnalysisBuffer_Concurrent benchmarks concurrent access
// to measure contention and performance under load.
func BenchmarkReadFromAnalysisBuffer_Concurrent(b *testing.B) {
	// Setup similar to above
	const (
		bufferCapacity = 1024 * 1024 * 10 // 10MB for concurrent test
		numStreams     = 4
		dataSize       = 48000 * 2 // 1 second of 16-bit audio at 48kHz
	)

	// Set up read size based on constants
	overlapSize = SecondsToBytes(0.5)
	readSize = conf.BufferSize - overlapSize

	// Initialize test buffers for multiple streams
	streams := make([]string, numStreams)
	for i := range numStreams {
		streams[i] = fmt.Sprintf("stream_%d", i)
	}

	abMutex.Lock()
	if analysisBuffers == nil {
		analysisBuffers = make(map[string]*ringbuffer.RingBuffer)
	}
	if prevData == nil {
		prevData = make(map[string][]byte)
	}

	for _, stream := range streams {
		ab := ringbuffer.New(bufferCapacity)
		analysisBuffers[stream] = ab
		prevData[stream] = nil

		// Pre-fill buffer
		testData := make([]byte, dataSize)
		for i := range testData {
			testData[i] = byte(i % 256)
		}
		for range 3 {
			_, err := ab.Write(testData)
			if err != nil {
				b.Fatalf("Failed to write test data for stream %s: %v", stream, err)
			}
		}
	}
	abMutex.Unlock()

	b.ResetTimer()
	b.ReportAllocs()

	// Run concurrent benchmark
	b.RunParallel(func(pb *testing.PB) {
		streamIdx := 0
		for pb.Next() {
			stream := streams[streamIdx%numStreams]
			streamIdx++

			data, err := ReadFromAnalysisBuffer(stream)
			if err != nil {
				b.Fatalf("ReadFromAnalysisBuffer failed: %v", err)
			}
			if data == nil || len(data) != conf.BufferSize {
				b.Fatalf("ReadFromAnalysisBuffer returned invalid data")
			}
		}
	})

	// Cleanup
	abMutex.Lock()
	for _, stream := range streams {
		delete(analysisBuffers, stream)
		delete(prevData, stream)
	}
	abMutex.Unlock()
}

// BenchmarkReadFromAnalysisBuffer_MemoryPressure benchmarks under memory pressure
// to simulate real-world conditions with GC activity.
func BenchmarkReadFromAnalysisBuffer_MemoryPressure(b *testing.B) {
	const (
		bufferCapacity = 1024 * 1024 * 10 // 10MB
		testStream     = "pressure_test"
		dataSize       = 48000 * 2 // 1 second of 16-bit audio at 48kHz
	)

	// Set up read size
	overlapSize = SecondsToBytes(0.5)
	readSize = conf.BufferSize - overlapSize

	// Initialize buffer
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

	// Pre-fill buffer
	testData := make([]byte, dataSize)
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	for range 5 {
		abMutex.Lock()
		_, err := ab.Write(testData)
		abMutex.Unlock()
		if err != nil {
			b.Fatalf("Failed to write test data: %v", err)
		}
	}

	// Create memory pressure with allocations
	pressure := make([][]byte, 0, 100)

	b.ResetTimer()
	b.ReportAllocs()

	// Run benchmark with memory pressure
	i := 0
	for b.Loop() {
		data, err := ReadFromAnalysisBuffer(testStream)
		if err != nil {
			b.Fatalf("ReadFromAnalysisBuffer failed: %v", err)
		}
		if data == nil {
			b.Fatalf("ReadFromAnalysisBuffer returned nil data")
		}

		// Add memory pressure every 10 iterations
		if i%10 == 0 {
			pressure = append(pressure, make([]byte, 1024*10)) // 10KB allocations
			if len(pressure) > 100 {
				pressure = pressure[50:] // Keep last 50 to maintain some pressure
			}
		}
		i++
	}

	// Cleanup
	abMutex.Lock()
	delete(analysisBuffers, testStream)
	delete(prevData, testStream)
	abMutex.Unlock()
}
