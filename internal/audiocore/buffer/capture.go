package buffer

import (
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// captureBufferAlignment is the byte alignment used when sizing the backing
// slice. Buffer sizes are rounded up to the nearest multiple of this value
// to ensure PCM sample boundaries are always respected.
const captureBufferAlignment = 2048

// CaptureBuffer is a circular byte buffer for storing PCM audio data with
// wall-clock timestamp tracking. Audio is written continuously; callers read
// back an arbitrary time window using ReadSegment.
//
// All exported methods are safe for concurrent use.
type CaptureBuffer struct {
	data           []byte
	writeIndex     int
	writtenBytes   int // total bytes written (capped at bufferSize)
	sampleRate     int
	bytesPerSample int
	bufferSize     int
	bufferDuration time.Duration
	startTime      time.Time
	initialized    bool
	wrapped        bool // true after the write pointer has wrapped at least once
	lock           sync.Mutex
	source         string
}

// NewCaptureBuffer creates a CaptureBuffer that holds durationSeconds of PCM
// audio sampled at sampleRate Hz with bytesPerSample bytes per sample.
//
// The backing byte slice is aligned to captureBufferAlignment (2048) bytes.
// Returns an error if any parameter is invalid.
func NewCaptureBuffer(durationSeconds, sampleRate, bytesPerSample int, sourceID string) (*CaptureBuffer, error) {
	if durationSeconds <= 0 {
		return nil, errors.Newf("invalid capture buffer duration: %d seconds, must be greater than 0", durationSeconds).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "new_capture_buffer").
			Context("source", sourceID).
			Context("duration_seconds", durationSeconds).
			Build()
	}
	if sampleRate <= 0 {
		return nil, errors.Newf("invalid sample rate: %d Hz, must be greater than 0", sampleRate).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "new_capture_buffer").
			Context("source", sourceID).
			Context("sample_rate", sampleRate).
			Build()
	}
	if bytesPerSample <= 0 {
		return nil, errors.Newf("invalid bytes per sample: %d, must be greater than 0", bytesPerSample).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "new_capture_buffer").
			Context("source", sourceID).
			Context("bytes_per_sample", bytesPerSample).
			Build()
	}
	if sourceID == "" {
		return nil, errors.Newf("empty source ID provided for capture buffer").
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "new_capture_buffer").
			Build()
	}

	rawSize := durationSeconds * sampleRate * bytesPerSample
	alignedSize := ((rawSize + captureBufferAlignment - 1) / captureBufferAlignment) * captureBufferAlignment

	// Note: bufferDuration is based on the requested durationSeconds, not the
	// aligned buffer size. The alignment rounding adds at most
	// captureBufferAlignment-1 bytes (~21 ms at 48 kHz/16-bit), which is
	// negligible for timestamp calculations and avoids drift from accumulating
	// rounding errors.
	return &CaptureBuffer{
		data:           make([]byte, alignedSize),
		sampleRate:     sampleRate,
		bytesPerSample: bytesPerSample,
		bufferSize:     alignedSize,
		bufferDuration: time.Duration(durationSeconds) * time.Second,
		source:         sourceID,
	}, nil
}

// Write appends PCM data to the circular buffer. The first call initialises
// the wall-clock start time. When the write pointer wraps around the buffer
// end, the start time is adjusted to reflect that the oldest data has been
// overwritten.
//
// Empty data is silently ignored. Write is safe for concurrent use.
func (cb *CaptureBuffer) Write(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	cb.lock.Lock()
	defer cb.lock.Unlock()

	if !cb.initialized {
		cb.startTime = time.Now()
		cb.initialized = true
	}

	prevIndex := cb.writeIndex
	dataLen := len(data)

	// If data is larger than the buffer, keep only the tail.
	if dataLen > cb.bufferSize {
		data = data[dataLen-cb.bufferSize:]
		dataLen = cb.bufferSize
	}

	remaining := cb.bufferSize - cb.writeIndex
	if dataLen <= remaining {
		copy(cb.data[cb.writeIndex:], data)
	} else {
		copy(cb.data[cb.writeIndex:], data[:remaining])
		copy(cb.data[0:], data[remaining:])
	}
	cb.writeIndex = (cb.writeIndex + dataLen) % cb.bufferSize
	cb.writtenBytes += dataLen
	if cb.writtenBytes > cb.bufferSize {
		cb.writtenBytes = cb.bufferSize
	}

	// If the write pointer wrapped (or reached zero), the oldest data was
	// overwritten.  Slide the logical start time back by a full buffer
	// duration so that timestamp-to-byte offset calculations stay consistent.
	if cb.writeIndex <= prevIndex && dataLen > 0 {
		cb.startTime = time.Now().Add(-cb.bufferDuration)
		cb.wrapped = true
	}

	return nil
}

// ReadSegment extracts the audio data between startTime and endTime from the
// circular buffer. Both times are expressed as wall-clock times that fall
// within the range [cb.startTime, cb.startTime+bufferDuration].
//
// IMPORTANT: ReadSegment does NOT wait for future data. The caller is
// responsible for ensuring that endTime is in the past before calling.
// If endTime is after the current wall-clock time the function returns an
// error rather than blocking.
//
// ReadSegment is safe for concurrent use.
func (cb *CaptureBuffer) ReadSegment(startTime, endTime time.Time) ([]byte, error) {
	cb.lock.Lock()
	defer cb.lock.Unlock()

	if !cb.initialized {
		return nil, errors.Newf("capture buffer has no data yet").
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "capture_buffer_read_segment").
			Context("source", cb.source).
			Build()
	}

	if !endTime.After(startTime) {
		return nil, errors.Newf("endTime must be after startTime").
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "capture_buffer_read_segment").
			Context("source", cb.source).
			Context("start_time", startTime.Format(time.RFC3339Nano)).
			Context("end_time", endTime.Format(time.RFC3339Nano)).
			Build()
	}

	startOffset := startTime.Sub(cb.startTime)
	endOffset := endTime.Sub(cb.startTime)

	if startOffset < 0 {
		return nil, errors.Newf("startTime is before the buffer's oldest data").
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "capture_buffer_read_segment").
			Context("source", cb.source).
			Context("start_time", startTime.Format(time.RFC3339Nano)).
			Context("buffer_start_time", cb.startTime.Format(time.RFC3339Nano)).
			Build()
	}

	if endOffset > cb.bufferDuration {
		return nil, errors.Newf("endTime is beyond the buffer's current timeframe").
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "capture_buffer_read_segment").
			Context("source", cb.source).
			Context("end_time", endTime.Format(time.RFC3339Nano)).
			Context("buffer_end_time", cb.startTime.Add(cb.bufferDuration).Format(time.RFC3339Nano)).
			Build()
	}

	// Convert time offsets to byte indices.  Calculate via sample indices
	// first to guarantee PCM alignment (important for 16-bit audio).
	//
	// Before wrap-around, data starts at byte 0 and the offset is trivial.
	// After wrap-around, the oldest data lives at writeIndex (not at 0),
	// because startTime is reset to time.Now()-bufferDuration.  An offset
	// of 0 from startTime therefore maps to writeIndex.
	startSample := int(startOffset.Seconds() * float64(cb.sampleRate))
	endSample := int(endOffset.Seconds() * float64(cb.sampleRate))

	baseOffset := 0
	if cb.wrapped {
		baseOffset = cb.writeIndex
	}
	startIdx := (baseOffset + startSample*cb.bytesPerSample) % cb.bufferSize
	endIdx := (baseOffset + endSample*cb.bytesPerSample) % cb.bufferSize

	return cb.extractSegment(startIdx, endIdx), nil
}

// extractSegment copies bytes from startIdx to endIdx out of the circular
// buffer, handling wrap-around. Must be called with cb.lock held.
func (cb *CaptureBuffer) extractSegment(startIdx, endIdx int) []byte {
	if startIdx == endIdx {
		return []byte{}
	}

	if startIdx < endIdx {
		seg := make([]byte, endIdx-startIdx)
		copy(seg, cb.data[startIdx:endIdx])
		return seg
	}

	// Wrap-around: first part from startIdx to end, second part from 0 to endIdx.
	firstLen := cb.bufferSize - startIdx
	seg := make([]byte, firstLen+endIdx)
	copy(seg[:firstLen], cb.data[startIdx:])
	copy(seg[firstLen:], cb.data[:endIdx])
	return seg
}

// StartTime returns the wall-clock time corresponding to the first byte of the
// buffer. Returns the zero time if no data has been written yet.
//
// StartTime is safe for concurrent use.
func (cb *CaptureBuffer) StartTime() time.Time {
	cb.lock.Lock()
	defer cb.lock.Unlock()
	return cb.startTime
}

// WrittenBytes returns the total number of bytes written to the buffer,
// capped at the buffer size. Before the buffer has been completely filled,
// reads from unfilled regions will contain zeroes. Callers can compare
// WrittenBytes against the buffer size to determine if the buffer has been
// fully populated.
//
// WrittenBytes is safe for concurrent use.
func (cb *CaptureBuffer) WrittenBytes() int {
	cb.lock.Lock()
	defer cb.lock.Unlock()
	return cb.writtenBytes
}

// Reset clears the buffer state and marks it as uninitialized. After Reset,
// the next Write call will re-establish the start time. Reset is safe for
// concurrent use.
func (cb *CaptureBuffer) Reset() {
	cb.lock.Lock()
	defer cb.lock.Unlock()

	for i := range cb.data {
		cb.data[i] = 0
	}
	cb.writeIndex = 0
	cb.writtenBytes = 0
	cb.initialized = false
	cb.wrapped = false
	cb.startTime = time.Time{}
}
