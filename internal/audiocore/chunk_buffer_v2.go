package audiocore

import (
	"sync"
	"time"
)

// ChunkBufferConfig holds configuration for chunk buffer
type ChunkBufferConfig struct {
	ChunkDuration time.Duration
	Format        AudioFormat
	BufferPool    BufferPool
}

// ChunkBufferV2 accumulates audio data into fixed-duration chunks with improved overflow handling
// Handles: single writes producing multiple chunks, partial data accumulation
// Thread-safe: all methods use mutex protection
type ChunkBufferV2 struct {
	chunkDuration   time.Duration
	format          AudioFormat
	bufferPool      BufferPool
	targetSize      int // bytes per chunk based on format and duration
	
	// Buffer management
	pendingData     []byte      // All pending data that hasn't been chunked yet
	completedChunks []AudioData // Ready chunks
	firstTimestamp  time.Time   // Timestamp of first data in pending buffer
	mu              sync.Mutex
}

// NewChunkBufferV2 creates a new improved chunk buffer
func NewChunkBufferV2(config ChunkBufferConfig) *ChunkBufferV2 {
	// Calculate target buffer size using int64 to prevent overflow
	bytesPerSecond := int64(config.Format.SampleRate) * int64(config.Format.Channels) * int64(config.Format.BitDepth/8)
	targetSize := int(float64(bytesPerSecond) * config.ChunkDuration.Seconds())

	return &ChunkBufferV2{
		chunkDuration:   config.ChunkDuration,
		format:          config.Format,
		bufferPool:      config.BufferPool,
		targetSize:      targetSize,
		pendingData:     make([]byte, 0, targetSize*2), // Pre-allocate some capacity
		completedChunks: make([]AudioData, 0),
	}
}

// Add adds audio data to the buffer
// Creates multiple chunks if data exceeds targetSize (handles large writes correctly)
func (c *ChunkBufferV2) Add(data *AudioData) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Track timestamp of first data if buffer was empty
	if len(c.pendingData) == 0 && len(data.Buffer) > 0 {
		c.firstTimestamp = data.Timestamp
	}

	// Append new data to pending
	c.pendingData = append(c.pendingData, data.Buffer...)

	// Extract all complete chunks from pending data
	chunkIndex := 0
	for len(c.pendingData) >= c.targetSize {
		// Get buffer from pool if available
		var chunkBuffer AudioBuffer
		var chunkData []byte
		
		if c.bufferPool != nil {
			// Use buffer pool for memory efficiency
			chunkBuffer = c.bufferPool.Get(c.targetSize)
			if chunkBuffer != nil {
				chunkBuffer.Reset()
				// Resize to exact size needed
				if err := chunkBuffer.Resize(c.targetSize); err == nil {
					chunkData = chunkBuffer.Data()
				} else {
					// If resize fails, release buffer and fall back to allocation
					chunkBuffer.Release()
					chunkBuffer = nil
					chunkData = make([]byte, c.targetSize)
				}
			} else {
				// Pool returned nil, fall back to allocation
				chunkData = make([]byte, c.targetSize)
			}
		} else {
			// No buffer pool available, use regular allocation
			chunkData = make([]byte, c.targetSize)
		}
		
		// Copy data to chunk buffer
		copy(chunkData, c.pendingData[:c.targetSize])
		
		// Calculate timestamp for this chunk
		chunkTimestamp := c.firstTimestamp.Add(time.Duration(chunkIndex) * c.chunkDuration)
		
		// Create chunk with buffer handle for proper lifecycle management
		chunk := AudioData{
			Buffer:       chunkData,
			Format:       c.format,
			Timestamp:    chunkTimestamp,
			Duration:     c.chunkDuration,
			SourceID:     data.SourceID,
			BufferHandle: chunkBuffer, // Store handle so consumer can release when done
		}
		
		c.completedChunks = append(c.completedChunks, chunk)
		chunkIndex++
		
		// Remove chunk from pending - use copy to avoid potential slice aliasing issues
		remaining := len(c.pendingData) - c.targetSize
		if remaining > 0 {
			copy(c.pendingData[:remaining], c.pendingData[c.targetSize:])
		}
		c.pendingData = c.pendingData[:remaining]
	}
	
	// Update first timestamp if all chunks were extracted
	if len(c.pendingData) > 0 {
		// Adjust timestamp for remaining data
		c.firstTimestamp = c.firstTimestamp.Add(time.Duration(chunkIndex) * c.chunkDuration)
	}
}

// HasCompleteChunk returns true if a complete chunk is ready
func (c *ChunkBufferV2) HasCompleteChunk() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.completedChunks) > 0
}

// GetChunk returns a complete chunk
func (c *ChunkBufferV2) GetChunk() *AudioData {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.completedChunks) == 0 {
		return nil
	}

	// Return first chunk - make a copy to avoid race conditions
	chunk := c.completedChunks[0]
	
	// Remove first chunk safely
	remaining := len(c.completedChunks) - 1
	if remaining > 0 {
		copy(c.completedChunks[:remaining], c.completedChunks[1:])
	}
	c.completedChunks = c.completedChunks[:remaining]
	
	return &chunk
}

// GetPendingSize returns the size of pending data that hasn't formed a complete chunk yet
func (c *ChunkBufferV2) GetPendingSize() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pendingData)
}

// Reset clears all buffers
func (c *ChunkBufferV2) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	c.pendingData = c.pendingData[:0]
	c.completedChunks = c.completedChunks[:0]
	c.firstTimestamp = time.Time{}
}