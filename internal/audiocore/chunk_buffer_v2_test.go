package audiocore

import (
	"sync"
	"testing"
	"time"
)

// TestChunkBufferV2Overflow tests various overflow scenarios with the improved implementation
func TestChunkBufferV2Overflow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		chunkSize        int
		writes           []int // sizes of data to write
		expectedChunks   int
		expectedPending  int
	}{
		{
			name:            "exact_chunk_no_overflow",
			chunkSize:       100,
			writes:          []int{50, 50},
			expectedChunks:  1,
			expectedPending: 0,
		},
		{
			name:            "single_write_overflow",
			chunkSize:       100,
			writes:          []int{150},
			expectedChunks:  1,
			expectedPending: 50,
		},
		{
			name:            "multiple_writes_with_overflow",
			chunkSize:       100,
			writes:          []int{30, 40, 50}, // 120 total
			expectedChunks:  1,
			expectedPending: 20,
		},
		{
			name:            "overflow_carries_to_next_chunk",
			chunkSize:       100,
			writes:          []int{150, 60}, // First write creates 1 chunk + 50 overflow, then 50+60=110
			expectedChunks:  2,
			expectedPending: 10,
		},
		{
			name:            "multiple_chunks_from_single_write",
			chunkSize:       100,
			writes:          []int{250}, // Should produce 2 chunks with 50 pending
			expectedChunks:  2,
			expectedPending: 50,
		},
		{
			name:            "exact_multiple_chunks",
			chunkSize:       100,
			writes:          []int{300}, // Should produce 3 chunks with 0 pending
			expectedChunks:  3,
			expectedPending: 0,
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
					SampleRate: tt.chunkSize, // Hack: use chunk size as sample rate for easy calculation
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

			// Write data
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
			}

			// Collect all chunks
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

			// Verify pending size
			pendingSize := cb.GetPendingSize()
			if pendingSize != tt.expectedPending {
				t.Errorf("expected pending size %d, got %d", tt.expectedPending, pendingSize)
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

			// Add pending data to collected count
			collectedData += pendingSize

			// Verify no data loss
			if collectedData != totalData {
				t.Errorf("data loss: wrote %d bytes, collected %d bytes", totalData, collectedData)
			}
		})
	}
}

// TestChunkBufferV2Concurrency tests concurrent access
func TestChunkBufferV2Concurrency(t *testing.T) {
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

	var wg sync.WaitGroup
	chunks := make([][]byte, 0)
	var chunksMu sync.Mutex

	// Writer goroutines
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				data := &AudioData{
					Buffer: make([]byte, 50+id*10), // Variable sizes
					Format: config.Format,
				}
				cb.Add(data)
				time.Sleep(time.Millisecond * 5)
			}
		}(i)
	}

	// Reader goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
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
	}()

	// Wait for completion
	wg.Wait()

	// Verify we got some chunks and no panic
	if len(chunks) == 0 {
		t.Error("no chunks produced in concurrent test")
	}
	
	t.Logf("Produced %d chunks in concurrent test", len(chunks))
}