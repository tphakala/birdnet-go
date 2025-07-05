package capture

import (
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// CircularBufferConfig holds configuration for circular buffer
type CircularBufferConfig struct {
	Duration   time.Duration
	SampleRate int
	Channels   int
	BitDepth   int
	BufferPool audiocore.BufferPool
}

// CircularBuffer implements a circular buffer for audio data
type CircularBuffer struct {
	config       CircularBufferConfig
	buffer       []byte
	capacity     int
	writePos     int64
	mu           sync.RWMutex
	bufferPool   audiocore.BufferPool
	
	// Timing information
	startTime    time.Time
	samplesPerMs int
	bytesPerMs   int
	
	// Statistics
	totalWritten int64
}

// NewCircularBuffer creates a new circular buffer
func NewCircularBuffer(config CircularBufferConfig) *CircularBuffer {
	// Calculate buffer size
	bytesPerSample := config.BitDepth / 8
	samplesPerSecond := config.SampleRate * config.Channels
	totalSamples := int(config.Duration.Seconds() * float64(samplesPerSecond))
	capacity := totalSamples * bytesPerSample
	
	// Add 10% extra capacity for safety
	capacity = int(float64(capacity) * 1.1)
	
	return &CircularBuffer{
		config:       config,
		buffer:       make([]byte, capacity),
		capacity:     capacity,
		bufferPool:   config.BufferPool,
		startTime:    time.Now(),
		samplesPerMs: samplesPerSecond / 1000,
		bytesPerMs:   (samplesPerSecond * bytesPerSample) / 1000,
	}
}

// Write writes audio data to the circular buffer
func (b *CircularBuffer) Write(data *audiocore.AudioData) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	// Validate format matches
	if data.Format.SampleRate != b.config.SampleRate ||
		data.Format.Channels != b.config.Channels ||
		data.Format.BitDepth != b.config.BitDepth {
		return errors.New(nil).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "audio format mismatch").
			Build()
	}
	
	dataLen := len(data.Buffer)
	if dataLen == 0 {
		return nil
	}
	
	// Calculate write position in circular buffer
	writeIndex := int(b.writePos % int64(b.capacity))
	
	// Handle wrap around
	if writeIndex+dataLen <= b.capacity {
		// Simple case: no wrap
		copy(b.buffer[writeIndex:], data.Buffer)
	} else {
		// Wrap around case
		firstPart := b.capacity - writeIndex
		copy(b.buffer[writeIndex:], data.Buffer[:firstPart])
		copy(b.buffer[0:], data.Buffer[firstPart:])
	}
	
	b.writePos += int64(dataLen)
	b.totalWritten += int64(dataLen)
	
	return nil
}

// Extract extracts audio data from the buffer for a specific time range
func (b *CircularBuffer) Extract(startTime time.Time, duration time.Duration) ([]byte, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	
	// Calculate byte positions based on time
	timeSinceStart := startTime.Sub(b.startTime)
	if timeSinceStart < 0 {
		// Request is before buffer start
		timeSinceStart = 0
	}
	
	startByte := timeSinceStart.Milliseconds() * int64(b.bytesPerMs)
	endByte := startByte + duration.Milliseconds()*int64(b.bytesPerMs)
	
	// Check if we have enough data
	if startByte >= b.writePos {
		return nil, errors.New(nil).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "requested time not in buffer").
			Build()
	}
	
	// Adjust end if beyond written data
	if endByte > b.writePos {
		endByte = b.writePos
	}
	
	// Check if data is still in buffer (not overwritten)
	if b.writePos-startByte > int64(b.capacity) {
		return nil, errors.New(nil).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "requested data has been overwritten").
			Build()
	}
	
	// Extract data
	dataLen := int(endByte - startByte)
	result := make([]byte, dataLen)
	
	startIndex := int(startByte % int64(b.capacity))
	if startIndex+dataLen <= b.capacity {
		// Simple case: no wrap
		copy(result, b.buffer[startIndex:startIndex+dataLen])
	} else {
		// Wrap around case
		firstPart := b.capacity - startIndex
		copy(result[:firstPart], b.buffer[startIndex:])
		copy(result[firstPart:], b.buffer[:dataLen-firstPart])
	}
	
	return result, nil
}

// Size returns the current size of valid data in the buffer
func (b *CircularBuffer) Size() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	
	if b.writePos < int64(b.capacity) {
		return b.writePos
	}
	return int64(b.capacity)
}

// TotalWritten returns the total bytes written to the buffer
func (b *CircularBuffer) TotalWritten() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.totalWritten
}

// Clear clears the buffer
func (b *CircularBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	b.writePos = 0
	b.totalWritten = 0
	b.startTime = time.Now()
	// Don't need to zero the buffer, just reset positions
}

// Close releases any resources
func (b *CircularBuffer) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	// Clear the buffer reference
	// Note: Buffer was allocated with make(), not from a pool
	b.buffer = nil
}