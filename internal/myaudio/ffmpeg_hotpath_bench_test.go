package myaudio

import (
	"sync"
	"sync/atomic"
	"testing"
)

// BenchmarkAtomicBoolCheck benchmarks the new atomic.Bool approach
func BenchmarkAtomicBoolCheck(b *testing.B) {
	audioChan := make(chan UnifiedAudioData, 1000)
	defer close(audioChan)
	
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)
	stream.running.Store(true)
	
	// Create test data
	testData := make([]byte, 4096) // Typical audio buffer size
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		_ = stream.handleAudioData(testData)
	}
	
	stream.Stop()
}

// BenchmarkMutexBoolCheck benchmarks the old mutex+bool approach for comparison
func BenchmarkMutexBoolCheck(b *testing.B) {
	// Simulate the old approach with mutex
	type oldStream struct {
		mu      sync.RWMutex
		stopped bool
		audioChan chan UnifiedAudioData
	}
	
	s := &oldStream{
		audioChan: make(chan UnifiedAudioData, 1000),
	}
	defer close(s.audioChan)
	
	// Create test data
	testData := make([]byte, 4096)
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	
	handleAudioDataOld := func(data []byte) error {
		// Old approach: mutex lock for checking stopped state
		s.mu.RLock()
		stopped := s.stopped
		s.mu.RUnlock()
		
		if stopped {
			return nil
		}
		
		// Simulate processing
		unifiedData := UnifiedAudioData{
			AudioLevel: AudioLevelData{
				Level:    50,
				Clipping: false,
				Source:   "test",
				Name:     "Test Stream",
			},
		}
		
		select {
		case s.audioChan <- unifiedData:
		default:
			// Channel full, drop data
		}
		
		return nil
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		_ = handleAudioDataOld(testData)
	}
}

// BenchmarkConcurrentAtomicBool benchmarks atomic.Bool under concurrent load
func BenchmarkConcurrentAtomicBool(b *testing.B) {
	audioChan := make(chan UnifiedAudioData, 1000)
	defer close(audioChan)
	
	stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)
	stream.running.Store(true)
	
	// Create test data
	testData := make([]byte, 4096)
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = stream.handleAudioData(testData)
		}
	})
	
	stream.Stop()
}

// BenchmarkConcurrentMutex benchmarks mutex approach under concurrent load
func BenchmarkConcurrentMutex(b *testing.B) {
	// Simulate the old approach with mutex
	type oldStream struct {
		mu      sync.RWMutex
		stopped bool
		audioChan chan UnifiedAudioData
	}
	
	s := &oldStream{
		audioChan: make(chan UnifiedAudioData, 1000),
	}
	defer close(s.audioChan)
	
	// Create test data
	testData := make([]byte, 4096)
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	
	handleAudioDataOld := func(data []byte) error {
		s.mu.RLock()
		stopped := s.stopped
		s.mu.RUnlock()
		
		if stopped {
			return nil
		}
		
		unifiedData := UnifiedAudioData{
			AudioLevel: AudioLevelData{
				Level:    50,
				Clipping: false,
				Source:   "test",
				Name:     "Test Stream",
			},
		}
		
		select {
		case s.audioChan <- unifiedData:
		default:
		}
		
		return nil
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = handleAudioDataOld(testData)
		}
	})
}

// BenchmarkStopOperation benchmarks the Stop operation with atomic.Bool
func BenchmarkStopOperation(b *testing.B) {
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		audioChan := make(chan UnifiedAudioData, 100)
		stream := NewFFmpegStream("rtsp://test.example.com/stream", "tcp", audioChan)
		stream.running.Store(true)
		
		// Benchmark the stop operation
		stream.Stop()
		
		close(audioChan)
	}
}

// BenchmarkCompareAndSwap benchmarks the atomic CompareAndSwap operation
func BenchmarkCompareAndSwap(b *testing.B) {
	var running atomic.Bool
	running.Store(true)
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		// Try to swap true to false
		if running.CompareAndSwap(true, false) {
			// Reset for next iteration
			running.Store(true)
		}
	}
}

// BenchmarkChannelSendWithCheck benchmarks sending to channel with atomic check
func BenchmarkChannelSendWithCheck(b *testing.B) {
	audioChan := make(chan UnifiedAudioData, 1000)
	defer close(audioChan)
	
	var running atomic.Bool
	running.Store(true)
	
	data := UnifiedAudioData{
		AudioLevel: AudioLevelData{
			Level:    50,
			Clipping: false,
			Source:   "test",
			Name:     "Test Stream",
		},
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		// Fast path check
		if !running.Load() {
			continue
		}
		
		// Non-blocking send
		select {
		case audioChan <- data:
		default:
		}
	}
}

// BenchmarkChannelSendWithMutex benchmarks sending to channel with mutex check
func BenchmarkChannelSendWithMutex(b *testing.B) {
	audioChan := make(chan UnifiedAudioData, 1000)
	defer close(audioChan)
	
	var mu sync.RWMutex
	stopped := false
	
	data := UnifiedAudioData{
		AudioLevel: AudioLevelData{
			Level:    50,
			Clipping: false,
			Source:   "test",
			Name:     "Test Stream",
		},
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		// Mutex check
		mu.RLock()
		isStopped := stopped
		mu.RUnlock()
		
		if isStopped {
			continue
		}
		
		// Non-blocking send
		select {
		case audioChan <- data:
		default:
		}
	}
}