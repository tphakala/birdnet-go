package capture

import (
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// CircularBuffer implements a time-based circular audio buffer
type CircularBuffer struct {
	data           []byte
	writeIndex     int
	capacity       int
	format         audiocore.AudioFormat
	duration       time.Duration
	startTime      time.Time
	initialized    bool
	bytesPerSecond int
	mu             sync.RWMutex
	bufferPool     audiocore.BufferPool
}

// NewCircularBuffer creates a new circular buffer
func NewCircularBuffer(duration time.Duration, format audiocore.AudioFormat, bufferPool audiocore.BufferPool) (*CircularBuffer, error) {
	// Validate inputs
	if duration <= 0 {
		return nil, errors.Newf("invalid buffer duration: %v", duration).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Build()
	}

	// Calculate buffer size
	bytesPerSample := format.BitDepth / 8
	bytesPerSecond := format.SampleRate * format.Channels * bytesPerSample
	totalBytes := int(duration.Seconds()) * bytesPerSecond

	// Align to 2048 bytes for better memory alignment
	alignedSize := ((totalBytes + 2047) / 2048) * 2048

	// Allocate buffer
	buffer := make([]byte, alignedSize)

	return &CircularBuffer{
		data:           buffer,
		capacity:       alignedSize,
		format:         format,
		duration:       duration,
		bytesPerSecond: bytesPerSecond,
		bufferPool:     bufferPool,
	}, nil
}

// Write adds audio data to the buffer
func (cb *CircularBuffer) Write(data []byte) error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if len(data) == 0 {
		return nil
	}

	// Initialize start time on first write
	if !cb.initialized {
		cb.startTime = time.Now()
		cb.initialized = true
	}

	// Track if we wrapped around
	prevWriteIndex := cb.writeIndex

	// Write data to buffer
	bytesWritten := 0
	for bytesWritten < len(data) {
		// Calculate how much we can write before wrapping
		writeSize := min(len(data)-bytesWritten, cb.capacity-cb.writeIndex)

		// Copy data
		copy(cb.data[cb.writeIndex:cb.writeIndex+writeSize], data[bytesWritten:bytesWritten+writeSize])

		// Update indices
		bytesWritten += writeSize
		cb.writeIndex = (cb.writeIndex + writeSize) % cb.capacity
	}

	// If we wrapped around, adjust start time
	if cb.writeIndex < prevWriteIndex {
		// Buffer has wrapped, adjust start time to maintain time window
		cb.startTime = time.Now().Add(-cb.duration)
	}

	return nil
}

// ReadSegment reads audio data for a specific time range
func (cb *CircularBuffer) ReadSegment(startTime, endTime time.Time) ([]byte, error) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	if !cb.initialized {
		return nil, errors.Newf("buffer not initialized").
			Component("audiocore").
			Category(errors.CategoryState).
			Build()
	}

	// Calculate offsets
	startOffset := startTime.Sub(cb.startTime)
	endOffset := endTime.Sub(cb.startTime)

	// Validate time range
	if startOffset < 0 || endOffset > cb.duration {
		return nil, errors.Newf("requested time range outside buffer: start=%v, end=%v", startTime, endTime).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("buffer_start", cb.startTime.Format(time.RFC3339)).
			Context("buffer_duration", cb.duration.String()).
			Build()
	}

	if endOffset <= startOffset {
		return nil, errors.Newf("invalid time range: end before start").
			Component("audiocore").
			Category(errors.CategoryValidation).
			Build()
	}

	// Calculate byte positions
	startByte := int(startOffset.Seconds() * float64(cb.bytesPerSecond))
	endByte := int(endOffset.Seconds() * float64(cb.bytesPerSecond))

	// Align to sample boundaries
	sampleSize := cb.format.Channels * (cb.format.BitDepth / 8)
	startByte = (startByte / sampleSize) * sampleSize
	endByte = (endByte / sampleSize) * sampleSize

	// Calculate positions in circular buffer
	startPos := startByte % cb.capacity
	endPos := endByte % cb.capacity
	segmentSize := endByte - startByte

	// Allocate result buffer
	result := make([]byte, segmentSize)

	// Read data handling wraparound
	if startPos < endPos {
		// No wraparound
		copy(result, cb.data[startPos:endPos])
	} else {
		// Handle wraparound
		firstPartSize := cb.capacity - startPos
		copy(result[:firstPartSize], cb.data[startPos:])
		copy(result[firstPartSize:], cb.data[:endPos])
	}

	return result, nil
}

// GetFormat returns the audio format of the buffer
func (cb *CircularBuffer) GetFormat() audiocore.AudioFormat {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.format
}

// GetDuration returns the total buffer duration
func (cb *CircularBuffer) GetDuration() time.Duration {
	return cb.duration
}

// Reset clears the buffer
func (cb *CircularBuffer) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.writeIndex = 0
	cb.initialized = false
	// Clear buffer data for security
	for i := range cb.data {
		cb.data[i] = 0
	}
}

// Close releases any resources
func (cb *CircularBuffer) Close() error {
	cb.Reset()
	return nil
}
