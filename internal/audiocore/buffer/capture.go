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

// ErrInsufficientData is returned by ReadSegment when the requested time range
// extends beyond the data that has actually been written to the buffer. This
// happens when the buffer has not yet been fully populated and the caller
// requests a segment from the unfilled region.
var ErrInsufficientData = errors.NewStd("requested segment exceeds written data")

// CaptureBuffer is a circular byte buffer for storing PCM audio data with
// monotonic sample-counter-based offset tracking. Audio is written
// continuously; callers read back an arbitrary time window using ReadSegment.
//
// Internally, byte offsets are derived from a monotonically increasing
// totalBytesWritten counter rather than wall-clock timestamps. Wall-clock
// time (startTime) is recorded on the first Write for external API use only.
//
// All exported methods are safe for concurrent use.
type CaptureBuffer struct {
	data              []byte
	writeIndex        int
	writtenBytes      int   // total bytes currently in the buffer (capped at bufferSize)
	totalBytesWritten int64 // monotonic counter of all bytes ever written
	sampleRate        int
	bytesPerSample    int
	bufferSize        int
	bufferDuration    time.Duration
	startTime         time.Time // wall-clock anchor for external APIs
	initialized       bool
	wrapped           bool // true after the write pointer has wrapped at least once
	lock              sync.Mutex
	source            string
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

// Write appends PCM data to the circular buffer. The first call records a
// wall-clock anchor time for external APIs. The monotonic totalBytesWritten
// counter is always advanced, providing jitter-free byte offset calculations.
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
	cb.totalBytesWritten += int64(dataLen)
	cb.writtenBytes += dataLen
	if cb.writtenBytes > cb.bufferSize {
		cb.writtenBytes = cb.bufferSize
	}

	// Once the buffer has filled (wrapped at least once), continuously
	// update startTime to reflect the oldest data still in the ring.
	// Before the first wrap, startTime stays at the initial Write() time.
	if cb.writeIndex <= prevIndex && dataLen > 0 {
		cb.wrapped = true
	}
	if cb.wrapped {
		cb.startTime = time.Now().Add(-cb.bufferDuration)
	}

	return nil
}

// ReadSegment extracts the audio data between startTime and endTime from the
// circular buffer. Both times are expressed as wall-clock times that fall
// within the range [cb.startTime, cb.startTime+bufferDuration].
//
// Internally, time offsets are converted to byte positions using the monotonic
// sample counter (totalBytesWritten) rather than wall-clock arithmetic, which
// eliminates jitter from scheduling delays and system clock adjustments.
//
// Returns ErrInsufficientData if the requested range extends beyond the data
// that has actually been written (e.g. before the buffer is fully populated).
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

	// Clamp startTime to the oldest available data so that requests for
	// audio slightly before the buffer window still return what we have
	// rather than failing outright. cb.startTime is kept current by Write()
	// which refreshes it on every call after the buffer wraps.
	if startTime.Before(cb.startTime) {
		startTime = cb.startTime
	}

	// After clamping, re-check that we still have a valid time range.
	// Clamping can make startTime == endTime (zero-length read).
	if !endTime.After(startTime) {
		return nil, errors.Newf("requested time range is empty after clamping to buffer window").
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

	// Sanity: endOffset must be non-negative.
	if endOffset < 0 {
		return nil, errors.Newf("endTime is before the buffer's start time").
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "capture_buffer_read_segment").
			Context("source", cb.source).
			Context("end_time", endTime.Format(time.RFC3339Nano)).
			Context("buffer_start_time", cb.startTime.Format(time.RFC3339Nano)).
			Build()
	}

	// Convert time offsets to byte positions using sample counts for PCM
	// alignment (important for 16-bit audio).
	startSample := int(startOffset.Seconds() * float64(cb.sampleRate))
	endSample := int(endOffset.Seconds() * float64(cb.sampleRate))
	startByteOffset := startSample * cb.bytesPerSample
	endByteOffset := endSample * cb.bytesPerSample

	// Validate that the requested range falls within actually-written data.
	// Before the buffer is fully populated, regions beyond writtenBytes
	// contain zero-filled memory which would produce silent audio.
	if endByteOffset > cb.writtenBytes {
		return nil, errors.New(ErrInsufficientData).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "capture_buffer_read_segment").
			Context("source", cb.source).
			Context("requested_end_byte", endByteOffset).
			Context("written_bytes", cb.writtenBytes).
			Build()
	}

	// Derive the circular-buffer base offset from the monotonic byte
	// counter. The logical window (duration * sampleRate * bytesPerSample)
	// may be smaller than bufferSize due to alignment padding. Using
	// logicalWindowBytes ensures the base offset points to the oldest
	// valid sample, not into alignment padding bytes.
	logicalWindowBytes := min(cb.writtenBytes, int(cb.bufferDuration.Seconds())*cb.sampleRate*cb.bytesPerSample)
	baseOffset := 0
	if cb.wrapped {
		baseOffset = int((cb.totalBytesWritten - int64(logicalWindowBytes)) % int64(cb.bufferSize))
	}
	startIdx := (baseOffset + startByteOffset) % cb.bufferSize
	endIdx := (baseOffset + endByteOffset) % cb.bufferSize

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

// TotalBytesWritten returns the monotonically increasing count of all bytes
// ever written to the buffer. Unlike WrittenBytes (which is capped at buffer
// size), this counter never decreases and is used internally for jitter-free
// byte offset calculations.
//
// TotalBytesWritten is safe for concurrent use.
func (cb *CaptureBuffer) TotalBytesWritten() int64 {
	cb.lock.Lock()
	defer cb.lock.Unlock()
	return cb.totalBytesWritten
}

// Reset clears the buffer state and marks it as uninitialized. After Reset,
// the next Write call will re-establish the start time and counters. Reset is
// safe for concurrent use.
func (cb *CaptureBuffer) Reset() {
	cb.lock.Lock()
	defer cb.lock.Unlock()

	for i := range cb.data {
		cb.data[i] = 0
	}
	cb.writeIndex = 0
	cb.writtenBytes = 0
	cb.totalBytesWritten = 0
	cb.initialized = false
	cb.wrapped = false
	cb.startTime = time.Time{}
}
