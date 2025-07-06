package audiocore

import (
	"sync"
	"testing"
	"time"
)

// TestChunkBufferV2Basic tests basic chunk buffer functionality
func TestChunkBufferV2Basic(t *testing.T) {
	t.Parallel()

	config := ChunkBufferConfig{
		ChunkDuration: time.Second,
		Format: AudioFormat{
			SampleRate: 100, // Using 100 for easy calculation
			Channels:   1,
			BitDepth:   8,
		},
		BufferPool: &mockBufferPool{buffers: make(map[int][]*mockBuffer)},
	}

	cb := NewChunkBufferV2(config)
	if cb == nil {
		t.Fatal("failed to create chunk buffer")
	}

	// Test 1: No data, no chunks
	if cb.HasCompleteChunk() {
		t.Error("expected no chunks with empty buffer")
	}

	// Test 2: Add partial data
	data := &AudioData{
		Buffer: make([]byte, 50),
		Format: config.Format,
	}
	cb.Add(data)

	if cb.HasCompleteChunk() {
		t.Error("expected no complete chunk with partial data")
	}
	if cb.GetPendingSize() != 50 {
		t.Errorf("expected 50 pending bytes, got %d", cb.GetPendingSize())
	}

	// Test 3: Complete the chunk
	cb.Add(data) // Now we have 100 bytes total

	if !cb.HasCompleteChunk() {
		t.Error("expected complete chunk after adding enough data")
	}

	chunk := cb.GetChunk()
	if chunk == nil {
		t.Fatal("expected chunk, got nil")
	}
	if len(chunk.Buffer) != 100 {
		t.Errorf("expected chunk size 100, got %d", len(chunk.Buffer))
	}

	// Test 4: Multiple chunks from single write
	largeData := &AudioData{
		Buffer: make([]byte, 250), // 2.5 chunks
		Format: config.Format,
	}
	cb.Add(largeData)

	chunkCount := 0
	for cb.HasCompleteChunk() {
		chunk := cb.GetChunk()
		if chunk == nil {
			t.Error("unexpected nil chunk")
			break
		}
		chunkCount++
	}

	if chunkCount != 2 {
		t.Errorf("expected 2 chunks from 250 bytes, got %d", chunkCount)
	}
	if cb.GetPendingSize() != 50 {
		t.Errorf("expected 50 bytes pending, got %d", cb.GetPendingSize())
	}
}

// TestChunkBufferV2ConcurrentAccess tests thread safety
func TestChunkBufferV2ConcurrentAccess(t *testing.T) {
	t.Parallel()

	config := ChunkBufferConfig{
		ChunkDuration: 10 * time.Millisecond,
		Format: AudioFormat{
			SampleRate: 1000,
			Channels:   1,
			BitDepth:   8,
		},
		BufferPool: &mockBufferPool{buffers: make(map[int][]*mockBuffer)},
	}

	cb := NewChunkBufferV2(config)
	
	var wg sync.WaitGroup
	chunks := make([][]byte, 0)
	var chunksMu sync.Mutex

	// Writer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			data := &AudioData{
				Buffer: make([]byte, 5), // Small writes
				Format: config.Format,
			}
			cb.Add(data)
		}
	}()

	// Reader goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ { // Expect ~50 chunks from 500 bytes
			if cb.HasCompleteChunk() {
				chunk := cb.GetChunk()
				if chunk != nil {
					chunksMu.Lock()
					chunks = append(chunks, chunk.Buffer)
					chunksMu.Unlock()
				}
			}
			time.Sleep(time.Millisecond)
		}
	}()

	wg.Wait()

	// Verify we got chunks and no race conditions occurred
	if len(chunks) == 0 {
		t.Error("expected some chunks from concurrent access")
	}
}

// TestChunkBufferV2Reset tests buffer reset functionality
func TestChunkBufferV2Reset(t *testing.T) {
	t.Parallel()

	config := ChunkBufferConfig{
		ChunkDuration: time.Second,
		Format: AudioFormat{
			SampleRate: 100,
			Channels:   1,
			BitDepth:   8,
		},
		BufferPool: &mockBufferPool{buffers: make(map[int][]*mockBuffer)},
	}

	cb := NewChunkBufferV2(config)

	// Add some data
	data := &AudioData{
		Buffer: make([]byte, 150), // 1.5 chunks
		Format: config.Format,
	}
	cb.Add(data)

	// Verify state before reset
	if !cb.HasCompleteChunk() {
		t.Error("expected complete chunk before reset")
	}
	if cb.GetPendingSize() != 50 {
		t.Errorf("expected 50 pending bytes before reset, got %d", cb.GetPendingSize())
	}

	// Reset
	cb.Reset()

	// Verify state after reset
	if cb.HasCompleteChunk() {
		t.Error("expected no chunks after reset")
	}
	if cb.GetPendingSize() != 0 {
		t.Errorf("expected 0 pending bytes after reset, got %d", cb.GetPendingSize())
	}
}

// TestChunkBufferV2EdgeCases tests edge cases
func TestChunkBufferV2EdgeCases(t *testing.T) {
	t.Parallel()

	config := ChunkBufferConfig{
		ChunkDuration: time.Second,
		Format: AudioFormat{
			SampleRate: 100,
			Channels:   1,
			BitDepth:   8,
		},
		BufferPool: &mockBufferPool{buffers: make(map[int][]*mockBuffer)},
	}

	cb := NewChunkBufferV2(config)

	// Test 1: Empty data
	emptyData := &AudioData{
		Buffer: []byte{},
		Format: config.Format,
	}
	cb.Add(emptyData) // Should not panic

	if cb.HasCompleteChunk() {
		t.Error("expected no chunk from empty data")
	}

	// Test 2: Exact chunk size
	exactData := &AudioData{
		Buffer: make([]byte, 100),
		Format: config.Format,
	}
	cb.Add(exactData)

	if !cb.HasCompleteChunk() {
		t.Error("expected chunk from exact size data")
	}
	chunk := cb.GetChunk()
	if chunk == nil || len(chunk.Buffer) != 100 {
		t.Error("unexpected chunk from exact size data")
	}
	if cb.GetPendingSize() != 0 {
		t.Errorf("expected no pending data, got %d", cb.GetPendingSize())
	}

	// Test 3: Get chunk when empty
	if cb.HasCompleteChunk() {
		t.Error("expected no chunks")
	}
	nilChunk := cb.GetChunk()
	if nilChunk != nil {
		t.Error("expected nil chunk when buffer is empty")
	}
}