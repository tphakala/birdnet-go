package audiocore

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestChunkBufferOverflow tests various overflow scenarios
func TestChunkBufferOverflow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		chunkSize      int
		writes         []int // sizes of data to write
		expectedChunks int
		expectedOverflow int
	}{
		{
			name:           "exact_chunk_no_overflow",
			chunkSize:      100,
			writes:         []int{50, 50},
			expectedChunks: 1,
			expectedOverflow: 0,
		},
		{
			name:           "single_write_overflow",
			chunkSize:      100,
			writes:         []int{150},
			expectedChunks: 1,
			expectedOverflow: 50,
		},
		{
			name:           "multiple_writes_with_overflow",
			chunkSize:      100,
			writes:         []int{30, 40, 50}, // 120 total
			expectedChunks: 1,
			expectedOverflow: 20,
		},
		{
			name:           "overflow_carries_to_next_chunk",
			chunkSize:      100,
			writes:         []int{150, 60}, // First write overflows 50, next chunk gets 50+60=110
			expectedChunks: 2,
			expectedOverflow: 10,
		},
		{
			name:           "multiple_chunks_with_overflow",
			chunkSize:      100,
			writes:         []int{250}, // Should produce 2 chunks with 50 overflow
			expectedChunks: 2,
			expectedOverflow: 50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock buffer pool
			pool := &mockBufferPool{
				buffers: make(map[int][]*mockBuffer),
			}

			// Create chunk buffer
			config := ChunkBufferConfig{
				ChunkDuration: time.Second,
				Format: AudioFormat{
					// TEST HACK: Using chunk size as sample rate for test simplification
					// This makes the math work out so that 1 second of audio = chunkSize bytes
					// Formula: bytes = sampleRate * channels * (bitDepth/8) * duration
					// With channels=1, bitDepth=8, duration=1s: bytes = sampleRate
					// This is NOT a real sample rate - just simplifies test calculations
					SampleRate: tt.chunkSize,
					Channels:   1,
					BitDepth:   8,
				},
				BufferPool: pool,
			}
			cb := NewChunkBufferV2(config)

			// Track chunks produced
			var chunks [][]byte
			var chunksMu sync.Mutex
			
			// Track total bytes written for continuous pattern
			totalWritten := 0

			// Write data and collect chunks
			for _, writeSize := range tt.writes {
				data := &AudioData{
					Buffer: make([]byte, writeSize),
					Format: config.Format,
				}
				// Fill with continuous test pattern
				for i := range data.Buffer {
					data.Buffer[i] = byte((totalWritten + i) % 256)
				}
				totalWritten += writeSize

				cb.Add(data)

				// Collect any complete chunks
				for cb.HasCompleteChunk() {
					chunk := cb.GetChunk()
					if chunk != nil {
						chunksMu.Lock()
						chunkCopy := make([]byte, len(chunk.Buffer))
						copy(chunkCopy, chunk.Buffer)
						chunks = append(chunks, chunkCopy)
						chunksMu.Unlock()
					}
				}
			}

			// Verify number of chunks
			if len(chunks) != tt.expectedChunks {
				t.Errorf("expected %d chunks, got %d", tt.expectedChunks, len(chunks))
			}

			// Verify chunk sizes
			for i, chunk := range chunks {
				if len(chunk) != tt.chunkSize {
					t.Errorf("chunk %d: expected size %d, got %d", i, tt.chunkSize, len(chunk))
				}
			}

			// Verify overflow buffer size
			// ChunkBufferV2 uses GetPendingSize() to check remaining data
			pendingSize := cb.GetPendingSize()

			if pendingSize != tt.expectedOverflow {
				t.Errorf("expected overflow size %d, got %d", tt.expectedOverflow, pendingSize)
			}

			// Verify data integrity by checking patterns
			totalData := 0
			for _, writeSize := range tt.writes {
				totalData += writeSize
			}

			collectedData := 0
			for _, chunk := range chunks {
				for i, b := range chunk {
					expected := byte((collectedData + i) % 256)
					if b != expected {
						t.Errorf("data corruption at position %d: expected %d, got %d", 
							collectedData+i, expected, b)
					}
				}
				collectedData += len(chunk)
			}

			// Add overflow data to collected count
			collectedData += pendingSize

			// Verify no data loss
			if collectedData != totalData {
				t.Errorf("data loss: wrote %d bytes, collected %d bytes", totalData, collectedData)
			}
		})
	}
}

// TestChunkBufferConcurrency tests concurrent access to ChunkBuffer
func TestChunkBufferConcurrency(t *testing.T) {
	t.Parallel()

	pool := &mockBufferPool{
		buffers: make(map[int][]*mockBuffer),
	}

	config := ChunkBufferConfig{
		ChunkDuration: time.Millisecond * 100,
		Format: AudioFormat{
			SampleRate: 1000,
			Channels:   1,
			BitDepth:   8,
		},
		BufferPool: pool,
	}
	cb := NewChunkBufferV2(config)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	chunks := make([][]byte, 0)
	var chunksMu sync.Mutex

	// Writer goroutines
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					data := &AudioData{
						Buffer: make([]byte, 10+id*10), // Variable sizes
						Format: config.Format,
					}
					cb.Add(data)
					time.Sleep(time.Millisecond * 10)
				}
			}
		}(i)
	}

	// Reader goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				if cb.HasCompleteChunk() {
					chunk := cb.GetChunk()
					if chunk != nil {
						chunksMu.Lock()
						chunks = append(chunks, chunk.Buffer)
						chunksMu.Unlock()
					}
				}
				time.Sleep(time.Millisecond * 5)
			}
		}
	}()

	// Let it run for a bit
	time.Sleep(time.Second)
	cancel()
	wg.Wait()

	// Verify we got some chunks and no panic
	if len(chunks) == 0 {
		t.Error("no chunks produced in concurrent test")
	}
}

// TestChunkBufferReset tests buffer reset after getting chunk
func TestChunkBufferReset(t *testing.T) {
	t.Parallel()

	pool := &mockBufferPool{
		buffers: make(map[int][]*mockBuffer),
	}

	config := ChunkBufferConfig{
		ChunkDuration: time.Second,
		Format: AudioFormat{
			SampleRate: 100,
			Channels:   1,
			BitDepth:   8,
		},
		BufferPool: pool,
	}
	cb := NewChunkBufferV2(config)

	// First chunk with overflow
	data1 := &AudioData{
		Buffer: make([]byte, 150), // 50 bytes overflow
		Format: config.Format,
	}
	cb.Add(data1)

	// Get first chunk
	chunk1 := cb.GetChunk()
	if chunk1 == nil {
		t.Fatal("expected chunk, got nil")
	}

	// Verify overflow is preserved
	pendingSize := cb.GetPendingSize()
	if pendingSize != 50 {
		t.Errorf("expected 50 bytes overflow, got %d", pendingSize)
	}

	// Add more data
	data2 := &AudioData{
		Buffer: make([]byte, 60),
		Format: config.Format,
	}
	cb.Add(data2)

	// Should have another complete chunk (50 overflow + 60 new = 110 total)
	if !cb.HasCompleteChunk() {
		t.Error("expected complete chunk after adding to overflow")
	}

	chunk2 := cb.GetChunk()
	if chunk2 == nil {
		t.Fatal("expected second chunk, got nil")
	}

	// Verify second overflow
	pendingSize2 := cb.GetPendingSize()
	if pendingSize2 != 10 {
		t.Errorf("expected 10 bytes overflow, got %d", pendingSize2)
	}
}

// mockBufferPool for testing
type mockBufferPool struct {
	mu          sync.Mutex
	buffers     map[int][]*mockBuffer
	maxPoolSize int // Maximum number of buffers allowed in pool (0 = unlimited)
	totalCount  int // Total number of buffers created
}

func (p *mockBufferPool) Get(size int) AudioBuffer {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check if we have buffers in the pool
	if buffers, ok := p.buffers[size]; ok && len(buffers) > 0 {
		buf := buffers[len(buffers)-1]
		p.buffers[size] = buffers[:len(buffers)-1]
		return buf
	}

	// Check if we've reached the pool limit
	if p.maxPoolSize > 0 && p.totalCount >= p.maxPoolSize {
		// Pool exhausted - return nil to simulate resource exhaustion
		return nil
	}

	// Create new buffer
	p.totalCount++
	return &mockBuffer{
		data: make([]byte, size),
		size: size,
	}
}

func (p *mockBufferPool) Put(buffer AudioBuffer) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if mb, ok := buffer.(*mockBuffer); ok {
		p.buffers[mb.size] = append(p.buffers[mb.size], mb)
	}
}

func (p *mockBufferPool) ReportMetrics() {
	// Mock implementation - no metrics needed for tests
}

func (p *mockBufferPool) Stats() BufferPoolStats {
	return BufferPoolStats{}
}

func (p *mockBufferPool) TierStats(tier string) (BufferPoolStats, bool) {
	return BufferPoolStats{}, false
}

type mockBuffer struct {
	data []byte
	size int
}

func (b *mockBuffer) Data() []byte                      { return b.data }
func (b *mockBuffer) Len() int                          { return len(b.data) }
func (b *mockBuffer) Cap() int                          { return cap(b.data) }
func (b *mockBuffer) Reset()                            { b.data = b.data[:0] }
func (b *mockBuffer) Resize(newSize int) error          { b.data = make([]byte, newSize); return nil }
func (b *mockBuffer) Slice(s, e int) ([]byte, error)    { return b.data[s:e], nil }
func (b *mockBuffer) Acquire()                          {}
func (b *mockBuffer) Release()                          {}