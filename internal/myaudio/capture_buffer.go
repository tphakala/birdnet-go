// this file defines ring buffer which is used for capturing audio clips
package myaudio

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// CaptureBuffer represents a circular buffer for storing PCM audio data, with timestamp tracking.
type CaptureBuffer struct {
	data           []byte
	writeIndex     int
	sampleRate     int
	bytesPerSample int
	bufferSize     int
	bufferDuration time.Duration
	startTime      time.Time
	initialized    bool
	lock           sync.Mutex
}

// map to store audio buffers for each audio source
var (
	captureBuffers map[string]*CaptureBuffer
	cbMutex        sync.RWMutex // Mutex to protect access to the captureBuffers map
)

// init initializes the audioBuffers map
func init() {
	captureBuffers = make(map[string]*CaptureBuffer)
}

// AllocateCaptureBuffer initializes an audio buffer for a single source.
// It returns an error if initialization fails or if the input is invalid.
func AllocateCaptureBuffer(durationSeconds, sampleRate, bytesPerSample int, source string) error {
	// Validate inputs
	if durationSeconds <= 0 {
		return fmt.Errorf("invalid duration: %d seconds, must be greater than 0", durationSeconds)
	}
	if sampleRate <= 0 {
		return fmt.Errorf("invalid sample rate: %d Hz, must be greater than 0", sampleRate)
	}
	if bytesPerSample <= 0 {
		return fmt.Errorf("invalid bytes per sample: %d, must be greater than 0", bytesPerSample)
	}
	if source == "" {
		return fmt.Errorf("empty source name provided")
	}

	// Calculate buffer size and check memory requirements
	bufferSize := durationSeconds * sampleRate * bytesPerSample
	alignedBufferSize := ((bufferSize + 2047) / 2048) * 2048 // Round up to the nearest multiple of 2048

	// Only prevent extremely large allocations (e.g. over 1GB)
	if alignedBufferSize > 1<<30 { // 1GB
		return fmt.Errorf("requested buffer size too large: %d bytes (>1GB)", alignedBufferSize)
	}

	// Create new buffer
	cb := NewCaptureBuffer(durationSeconds, sampleRate, bytesPerSample)
	if cb == nil {
		return fmt.Errorf("failed to create capture buffer for source: %s", source)
	}

	// Update global map safely
	cbMutex.Lock()
	defer cbMutex.Unlock()

	// Check if buffer already exists
	if _, exists := captureBuffers[source]; exists {
		return fmt.Errorf("capture buffer already exists for source: %s", source)
	}

	captureBuffers[source] = cb
	return nil
}

// RemoveCaptureBuffer safely removes and cleans up an audio buffer for a single source.
func RemoveCaptureBuffer(source string) error {
	cbMutex.Lock()
	defer cbMutex.Unlock()

	if _, exists := captureBuffers[source]; !exists {
		return fmt.Errorf("no capture buffer found for source: %s", source)
	}

	delete(captureBuffers, source)
	return nil
}

// HasCaptureBuffer checks if a capture buffer exists for the given source ID
func HasCaptureBuffer(sourceID string) bool {
	cbMutex.RLock()
	defer cbMutex.RUnlock()
	_, exists := captureBuffers[sourceID]
	return exists
}

// InitCaptureBuffers initializes the capture buffers for each capture source.
// It returns an error if initialization fails for any source.
func InitCaptureBuffers(durationSeconds, sampleRate, bytesPerSample int, sources []string) error {
	if len(sources) == 0 {
		return fmt.Errorf("no capture sources provided")
	}

	// Try to initialize each buffer
	var initErrors []string
	for _, source := range sources {
		if err := AllocateCaptureBuffer(durationSeconds, sampleRate, bytesPerSample, source); err != nil {
			initErrors = append(initErrors, fmt.Sprintf("source %s: %v", source, err))
		}
	}

	// If there were any errors, return them all
	if len(initErrors) > 0 {
		return fmt.Errorf("failed to initialize some capture buffers: %s", strings.Join(initErrors, "; "))
	}

	return nil
}

// WriteToCaptureBuffer adds PCM audio data to the buffer for a given source.
func WriteToCaptureBuffer(source string, data []byte) error {
	cbMutex.RLock()
	cb, exists := captureBuffers[source]
	cbMutex.RUnlock()

	if !exists {
		return fmt.Errorf("no capture buffer found for source: %s", source)
	}

	cb.Write(data)
	return nil
}

// ReadSegmentFromCaptureBuffer extracts a segment of audio data from the buffer for a given source.
func ReadSegmentFromCaptureBuffer(source string, requestedStartTime time.Time, duration int) ([]byte, error) {
	cbMutex.RLock()
	cb, exists := captureBuffers[source]
	cbMutex.RUnlock()

	if !exists {
		return nil, fmt.Errorf("no capture buffer found for source: %s", source)
	}

	return cb.ReadSegment(requestedStartTime, duration)
}

// NewCaptureBuffer initializes a new CaptureBuffer with timestamp tracking
func NewCaptureBuffer(durationSeconds, sampleRate, bytesPerSample int) *CaptureBuffer {
	bufferSize := durationSeconds * sampleRate * bytesPerSample
	alignedBufferSize := ((bufferSize + 2047) / 2048) * 2048 // Round up to the nearest multiple of 2048
	cb := &CaptureBuffer{
		data:           make([]byte, alignedBufferSize),
		sampleRate:     sampleRate,
		bytesPerSample: bytesPerSample,
		bufferSize:     alignedBufferSize,
		bufferDuration: time.Second * time.Duration(durationSeconds),
		initialized:    false,
	}

	return cb
}

// Write adds PCM audio data to the buffer, ensuring thread safety and accurate timekeeping.
func (cb *CaptureBuffer) Write(data []byte) {
	// Lock the buffer to prevent concurrent writes or reads from interfering with the update process.
	cb.lock.Lock()
	defer cb.lock.Unlock()

	// Basic validation to check if the data length is sensible for audio data
	if len(data) == 0 {
		// Skip empty data
		return
	}

	if len(data)%cb.bytesPerSample != 0 {
		// Data length is not aligned with sample size, which might indicate corrupted data
		// Only log occasionally to avoid flooding logs
		if time.Now().Second()%10 == 0 {
			log.Printf("⚠️ Warning: Audio data length (%d) is not aligned with sample size (%d)",
				len(data), cb.bytesPerSample)
		}
	}

	if !cb.initialized {
		// Initialize the buffer's start time based on the current time.
		cb.startTime = time.Now()
		cb.initialized = true
	}

	// Store the current write index to determine if we've wrapped around the buffer.
	prevWriteIndex := cb.writeIndex

	// Copy the incoming data into the buffer starting at the current write index.
	bytesWritten := copy(cb.data[cb.writeIndex:], data)

	// Update the write index, wrapping around the buffer if necessary.
	cb.writeIndex = (cb.writeIndex + bytesWritten) % cb.bufferSize

	// Determine if the write operation has overwritten old data.
	if cb.writeIndex <= prevWriteIndex {
		// If old data has been overwritten, adjust startTime to maintain accurate timekeeping.
		cb.startTime = time.Now().Add(-cb.bufferDuration)
		if conf.Setting().Realtime.Audio.Export.Debug {
			log.Printf("Buffer wrapped during write, adjusting start time to %v", cb.startTime)
		}
	}
}

// ReadSegment extracts a segment of audio data based on precise start and end times, handling wraparounds.
// It waits until the current time is past the requested end time.
func (cb *CaptureBuffer) ReadSegment(requestedStartTime time.Time, duration int) ([]byte, error) {
	requestedEndTime := requestedStartTime.Add(time.Duration(duration) * time.Second)

	for {
		cb.lock.Lock()

		startOffset := requestedStartTime.Sub(cb.startTime)
		endOffset := requestedEndTime.Sub(cb.startTime)

		startIndex := int(startOffset.Seconds()) * cb.sampleRate * cb.bytesPerSample
		endIndex := int(endOffset.Seconds()) * cb.sampleRate * cb.bytesPerSample

		startIndex %= cb.bufferSize
		endIndex %= cb.bufferSize

		if startOffset < 0 {
			if cb.writeIndex == 0 || cb.writeIndex+int(startOffset.Seconds())*cb.sampleRate*cb.bytesPerSample > cb.bufferSize {
				cb.lock.Unlock()
				return nil, errors.New("requested start time is outside the buffer's current timeframe")
			}
			startIndex = (cb.bufferSize + startIndex) % cb.bufferSize
		}

		if endOffset < 0 || endOffset <= startOffset {
			cb.lock.Unlock()
			return nil, errors.New("requested times are outside the buffer's current timeframe")
		}

		// Wait until the current time is past the requested end time
		if time.Now().After(requestedEndTime) {
			var segment []byte
			if startIndex < endIndex {
				if conf.Setting().Realtime.Audio.Export.Debug {
					log.Printf("Reading segment from %d to %d", startIndex, endIndex)
				}
				segmentSize := endIndex - startIndex
				segment = make([]byte, segmentSize)
				copy(segment, cb.data[startIndex:endIndex])
			} else {
				if conf.Setting().Realtime.Audio.Export.Debug {
					log.Printf("Buffer wrapped during read, reading segment from %d to %d", startIndex, endIndex)
				}
				segmentSize := (cb.bufferSize - startIndex) + endIndex
				segment = make([]byte, segmentSize)
				firstPartSize := cb.bufferSize - startIndex
				copy(segment[:firstPartSize], cb.data[startIndex:])
				copy(segment[firstPartSize:], cb.data[:endIndex])
			}
			cb.lock.Unlock()
			return segment, nil
		}

		if conf.Setting().Realtime.Audio.Export.Debug {
			log.Printf("Buffer is not filled yet, waiting for data to be available")
		}
		cb.lock.Unlock()
		time.Sleep(1 * time.Second) // Sleep briefly to avoid busy waiting
	}
}
