// this file defines ring buffer which is used for capturing audio clips
package myaudio

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
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
	source         string // Source identifier for metrics tracking
}

// map to store audio buffers for each audio source
var (
	captureBuffers map[string]*CaptureBuffer
	cbMutex        sync.RWMutex            // Mutex to protect access to the captureBuffers map
	captureMetrics *metrics.MyAudioMetrics // Global metrics instance for capture buffer operations
)

// init initializes the audioBuffers map
func init() {
	captureBuffers = make(map[string]*CaptureBuffer)
}

// SetCaptureMetrics sets the metrics instance for capture buffer operations
func SetCaptureMetrics(myAudioMetrics *metrics.MyAudioMetrics) {
	captureMetrics = myAudioMetrics
}

// AllocateCaptureBufferIfNeeded checks if a buffer exists and only allocates if needed.
// It returns nil if the buffer already exists or was successfully created.
// This function is thread-safe and prevents race conditions during allocation.
func AllocateCaptureBufferIfNeeded(durationSeconds, sampleRate, bytesPerSample int, source string) error {
	// Hold lock for entire operation to prevent race conditions
	cbMutex.Lock()
	defer cbMutex.Unlock()
	
	// Check if buffer already exists
	if _, exists := captureBuffers[source]; exists {
		return nil
	}
	
	// Buffer doesn't exist, allocate it while holding the lock
	return allocateCaptureBufferInternal(durationSeconds, sampleRate, bytesPerSample, source)
}

// AllocateCaptureBuffer initializes an audio buffer for a single source.
// It returns an error if initialization fails or if the input is invalid.
// 
// Metrics tracking:
// - myaudio_buffer_allocation_attempts_total{result="first_allocation"} - successful first allocations
// - myaudio_buffer_allocation_attempts_total{result="repeated_blocked"} - blocked repeated allocations
// - myaudio_buffer_allocation_attempts_total{result="error"} - failed allocations due to validation errors
//
// To detect repeated allocation issues, monitor the "repeated_blocked" counter per source.
func AllocateCaptureBuffer(durationSeconds, sampleRate, bytesPerSample int, source string) error {
	// Lock once for the entire operation
	cbMutex.Lock()
	defer cbMutex.Unlock()
	
	return allocateCaptureBufferInternal(durationSeconds, sampleRate, bytesPerSample, source)
}

// allocateCaptureBufferInternal performs the actual buffer allocation.
// It must be called with cbMutex already held.
func allocateCaptureBufferInternal(durationSeconds, sampleRate, bytesPerSample int, source string) error {
	start := time.Now()
	
	// Track allocation attempt
	if captureMetrics != nil {
		captureMetrics.RecordBufferAllocationAttempt("capture", source, "attempted")
	}

	// Validate inputs
	if durationSeconds <= 0 {
		enhancedErr := errors.Newf("invalid capture buffer duration: %d seconds, must be greater than 0", durationSeconds).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "allocate_capture_buffer").
			Context("source", source).
			Context("duration_seconds", durationSeconds).
			Build()

		if captureMetrics != nil {
			captureMetrics.RecordBufferAllocation("capture", source, "error")
			captureMetrics.RecordBufferAllocationError("capture", source, "invalid_duration")
			captureMetrics.RecordBufferAllocationAttempt("capture", source, "error")
		}
		return enhancedErr
	}
	if sampleRate <= 0 {
		enhancedErr := errors.Newf("invalid sample rate: %d Hz, must be greater than 0", sampleRate).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "allocate_capture_buffer").
			Context("source", source).
			Context("sample_rate", sampleRate).
			Build()

		if captureMetrics != nil {
			captureMetrics.RecordBufferAllocation("capture", source, "error")
			captureMetrics.RecordBufferAllocationError("capture", source, "invalid_sample_rate")
			captureMetrics.RecordBufferAllocationAttempt("capture", source, "error")
		}
		return enhancedErr
	}
	if bytesPerSample <= 0 {
		enhancedErr := errors.Newf("invalid bytes per sample: %d, must be greater than 0", bytesPerSample).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "allocate_capture_buffer").
			Context("source", source).
			Context("bytes_per_sample", bytesPerSample).
			Build()

		if captureMetrics != nil {
			captureMetrics.RecordBufferAllocation("capture", source, "error")
			captureMetrics.RecordBufferAllocationError("capture", source, "invalid_bytes_per_sample")
			captureMetrics.RecordBufferAllocationAttempt("capture", source, "error")
		}
		return enhancedErr
	}
	if source == "" {
		enhancedErr := errors.Newf("empty source name provided for capture buffer allocation").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "allocate_capture_buffer").
			Build()

		if captureMetrics != nil {
			captureMetrics.RecordBufferAllocation("capture", "unknown", "error")
			captureMetrics.RecordBufferAllocationError("capture", "unknown", "empty_source")
			captureMetrics.RecordBufferAllocationAttempt("capture", "unknown", "error")
		}
		return enhancedErr
	}

	// Calculate buffer size and check memory requirements
	bufferSize := durationSeconds * sampleRate * bytesPerSample
	alignedBufferSize := ((bufferSize + 2047) / 2048) * 2048 // Round up to the nearest multiple of 2048

	// Only prevent extremely large allocations (e.g. over 1GB)
	if alignedBufferSize > 1<<30 { // 1GB
		enhancedErr := errors.Newf("requested capture buffer size too large: %d bytes (>1GB)", alignedBufferSize).
			Component("myaudio").
			Category(errors.CategorySystem).
			Context("operation", "allocate_capture_buffer").
			Context("source", source).
			Context("requested_size", alignedBufferSize).
			Context("max_allowed_size", 1<<30).
			Build()

		if captureMetrics != nil {
			captureMetrics.RecordBufferAllocation("capture", source, "error")
			captureMetrics.RecordBufferAllocationError("capture", source, "size_too_large")
			captureMetrics.RecordBufferAllocationAttempt("capture", source, "error")
		}
		return enhancedErr
	}


	// Create new buffer
	cb := NewCaptureBuffer(durationSeconds, sampleRate, bytesPerSample, source)
	if cb == nil {
		enhancedErr := errors.Newf("failed to create capture buffer for source: %s", source).
			Component("myaudio").
			Category(errors.CategorySystem).
			Context("operation", "allocate_capture_buffer").
			Context("source", source).
			Context("buffer_size", alignedBufferSize).
			Build()

		if captureMetrics != nil {
			captureMetrics.RecordBufferAllocation("capture", source, "error")
			captureMetrics.RecordBufferAllocationError("capture", source, "creation_failed")
			captureMetrics.RecordBufferAllocationAttempt("capture", source, "error")
		}
		return enhancedErr
	}

	// Check if buffer already exists (caller must hold cbMutex)
	if _, exists := captureBuffers[source]; exists {
		// Log repeated allocation attempt
		log.Printf("⚠️ Buffer allocation blocked: buffer already exists for source %s", source)
		
		enhancedErr := errors.Newf("capture buffer already exists for source: %s", source).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "allocate_capture_buffer").
			Context("source", source).
			Build()

		if captureMetrics != nil {
			captureMetrics.RecordBufferAllocation("capture", source, "error")
			captureMetrics.RecordBufferAllocationError("capture", source, "already_exists")
			captureMetrics.RecordBufferAllocationAttempt("capture", source, "repeated_blocked")
		}
		return enhancedErr
	}

	captureBuffers[source] = cb

	// Record successful allocation metrics
	if captureMetrics != nil {
		duration := time.Since(start).Seconds()
		captureMetrics.RecordBufferAllocation("capture", source, "success")
		captureMetrics.RecordBufferAllocationDuration("capture", source, duration)
		captureMetrics.RecordBufferAllocationAttempt("capture", source, "first_allocation")
		captureMetrics.RecordBufferAllocationSize("capture", source, alignedBufferSize)
		captureMetrics.UpdateBufferCapacity("capture", source, alignedBufferSize)
		captureMetrics.UpdateBufferSize("capture", source, 0) // Empty at start
		captureMetrics.UpdateBufferUtilization("capture", source, 0.0)
	}

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
		if err := AllocateCaptureBufferIfNeeded(durationSeconds, sampleRate, bytesPerSample, source); err != nil {
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
func NewCaptureBuffer(durationSeconds, sampleRate, bytesPerSample int, source string) *CaptureBuffer {
	bufferSize := durationSeconds * sampleRate * bytesPerSample
	alignedBufferSize := ((bufferSize + 2047) / 2048) * 2048 // Round up to the nearest multiple of 2048
	cb := &CaptureBuffer{
		data:           make([]byte, alignedBufferSize),
		sampleRate:     sampleRate,
		bytesPerSample: bytesPerSample,
		bufferSize:     alignedBufferSize,
		bufferDuration: time.Second * time.Duration(durationSeconds),
		initialized:    false,
		source:         source,
	}

	return cb
}

// Write adds PCM audio data to the buffer, ensuring thread safety and accurate timekeeping.
func (cb *CaptureBuffer) Write(data []byte) {
	start := time.Now()

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

		// Record audio data validation error
		if captureMetrics != nil {
			captureMetrics.RecordAudioDataValidationError(cb.source, "alignment")
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

	// Record metrics for buffer write
	if captureMetrics != nil {
		duration := time.Since(start).Seconds()
		captureMetrics.RecordBufferWrite("capture", cb.source, "success")
		captureMetrics.RecordBufferWriteDuration("capture", cb.source, duration)
		captureMetrics.RecordBufferWriteBytes("capture", cb.source, bytesWritten)

		// Update buffer utilization
		utilization := float64(cb.writeIndex) / float64(cb.bufferSize)
		captureMetrics.UpdateBufferUtilization("capture", cb.source, utilization)
		captureMetrics.UpdateBufferSize("capture", cb.source, cb.writeIndex)
	}

	// Determine if the write operation has overwritten old data.
	if cb.writeIndex <= prevWriteIndex {
		// If old data has been overwritten, adjust startTime to maintain accurate timekeeping.
		cb.startTime = time.Now().Add(-cb.bufferDuration)
		if conf.Setting().Realtime.Audio.Export.Debug {
			log.Printf("Buffer wrapped during write, adjusting start time to %v", cb.startTime)
		}

		// Record buffer wraparound
		if captureMetrics != nil {
			captureMetrics.RecordBufferWraparound("capture", cb.source)
		}
	}
}

// ReadSegment extracts a segment of audio data based on precise start and end times, handling wraparounds.
// It waits until the current time is past the requested end time.
func (cb *CaptureBuffer) ReadSegment(requestedStartTime time.Time, duration int) ([]byte, error) {
	operationStart := time.Now()
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

				enhancedErr := errors.Newf("requested start time is outside the buffer's current timeframe").
					Component("myaudio").
					Category(errors.CategoryValidation).
					Context("operation", "read_capture_buffer_segment").
					Context("requested_start_time", requestedStartTime.Format(time.RFC3339Nano)).
					Context("buffer_start_time", cb.startTime.Format(time.RFC3339Nano)).
					Context("start_offset_seconds", startOffset.Seconds()).
					Context("buffer_duration_seconds", cb.bufferDuration.Seconds()).
					Build()

				if captureMetrics != nil {
					captureMetrics.RecordCaptureBufferSegmentRead(cb.source, "error")
					captureMetrics.RecordCaptureBufferTimestampError(cb.source, "outside_timeframe")
				}
				return nil, enhancedErr
			}
			startIndex = (cb.bufferSize + startIndex) % cb.bufferSize
		}

		if endOffset < 0 || endOffset <= startOffset {
			cb.lock.Unlock()

			enhancedErr := errors.Newf("requested times are outside the buffer's current timeframe").
				Component("myaudio").
				Category(errors.CategoryValidation).
				Context("operation", "read_capture_buffer_segment").
				Context("requested_start_time", requestedStartTime.Format(time.RFC3339Nano)).
				Context("requested_end_time", requestedEndTime.Format(time.RFC3339Nano)).
				Context("buffer_start_time", cb.startTime.Format(time.RFC3339Nano)).
				Context("start_offset_seconds", startOffset.Seconds()).
				Context("end_offset_seconds", endOffset.Seconds()).
				Build()

			if captureMetrics != nil {
				captureMetrics.RecordCaptureBufferSegmentRead(cb.source, "error")
				captureMetrics.RecordCaptureBufferTimestampError(cb.source, "invalid_duration")
			}
			return nil, enhancedErr
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

			// Record successful read metrics
			if captureMetrics != nil {
				totalDuration := time.Since(operationStart).Seconds()
				captureMetrics.RecordCaptureBufferSegmentRead(cb.source, "success")
				captureMetrics.RecordCaptureBufferSegmentReadDuration(cb.source, totalDuration)
				captureMetrics.RecordBufferReadBytes("capture", cb.source, len(segment))
			}

			return segment, nil
		}

		if conf.Setting().Realtime.Audio.Export.Debug {
			log.Printf("Buffer is not filled yet, waiting for data to be available")
		}
		cb.lock.Unlock()
		time.Sleep(1 * time.Second) // Sleep briefly to avoid busy waiting
	}
}
