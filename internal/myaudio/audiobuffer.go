package myaudio

import (
	"errors"
	"sync"
	"time"
)

// AudioBuffer represents a circular buffer for storing PCM audio data, with timestamp tracking.
type AudioBuffer struct {
	data           []byte
	writeIndex     int
	sampleRate     int
	bytesPerSample int
	bufferSize     int
	bufferDuration time.Duration
	startTime      time.Time
	lock           sync.Mutex
}

// Initializes a new AudioBuffer with timestamp tracking
func NewAudioBuffer(durationSeconds int, sampleRate, bytesPerSample int) *AudioBuffer {
	bufferSize := durationSeconds * sampleRate * bytesPerSample
	return &AudioBuffer{
		data:           make([]byte, bufferSize),
		sampleRate:     sampleRate,
		bytesPerSample: bytesPerSample,
		bufferSize:     bufferSize,
		bufferDuration: time.Second * time.Duration(durationSeconds),
		startTime:      time.Now(),
	}
}

// Write adds PCM audio data to the buffer, updating the startTime if necessary for precise timekeeping.
func (ab *AudioBuffer) Write(data []byte) {
	// Copy the data to the buffer
	bytesWritten := copy(ab.data[ab.writeIndex:], data)
	ab.writeIndex = (ab.writeIndex + bytesWritten) % ab.bufferSize

	// Update startTime only if we've overwritten the old start
	if bytesWritten > ab.bufferSize-ab.writeIndex {
		ab.startTime = time.Now().Add(-ab.bufferDuration)
	}
}

// ReadSegment extracts a segment of audio data based on precise start and end times, handling wraparounds.
func (ab *AudioBuffer) ReadSegment(requestedStartTime, requestedEndTime time.Time) ([]byte, error) {
	ab.lock.Lock()
	defer ab.lock.Unlock()

	// Calculate time since the buffer's startTime for both requested start and end times
	startOffset := requestedStartTime.Sub(ab.startTime)
	endOffset := requestedEndTime.Sub(ab.startTime)

	// Convert time offsets to buffer indices
	startIndex := int(startOffset.Seconds()) * ab.sampleRate * ab.bytesPerSample
	endIndex := int(endOffset.Seconds()) * ab.sampleRate * ab.bytesPerSample

	// Normalize indices based on buffer size
	startIndex = startIndex % ab.bufferSize
	endIndex = endIndex % ab.bufferSize

	// Check if requested times are within the buffer's timeframe
	if startOffset < 0 || endOffset < 0 || endOffset <= startOffset {
		return nil, errors.New("requested times are outside the buffer's current timeframe")
	}

	// Determine if the read segment wraps around the buffer's end
	if startIndex < endIndex {
		// Simple case: The segment does not wrap around
		segmentSize := endIndex - startIndex
		segment := make([]byte, segmentSize)
		copy(segment, ab.data[startIndex:endIndex])
		return segment, nil
	} else {
		// Wraparound case: The segment spans the end and restarts at the beginning of the buffer
		segmentSize := (ab.bufferSize - startIndex) + endIndex
		segment := make([]byte, segmentSize)

		// Copy from startIndex to the end of the buffer
		firstPartSize := ab.bufferSize - startIndex
		copy(segment[:firstPartSize], ab.data[startIndex:])

		// Copy from the beginning of the buffer to endIndex
		copy(segment[firstPartSize:], ab.data[:endIndex])
		return segment, nil
	}
}
