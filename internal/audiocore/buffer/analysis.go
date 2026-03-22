package buffer

import (
	"sync"
	"time"

	"github.com/smallnest/ringbuffer"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Overwrite monitoring constants.
const (
	analysisOverwriteWindowDuration = 5 * time.Minute // sliding window for rate calculation
	analysisOverwriteRateThreshold  = 10              // percentage threshold to trigger warning
	analysisOverwriteMinWrites      = 50              // minimum writes before checking rate
	analysisOverwriteNotifyCooldown = 1 * time.Hour   // minimum time between warnings per source
)

// AnalysisBuffer is a lock-protected ring buffer designed for audio analysis.
// It maintains per-instance overlap tracking so that consecutive reads share
// a configurable number of bytes from the previous read, enabling BirdNET's
// sliding-window analysis without any global state.
//
// All exported methods are safe for concurrent use.
type AnalysisBuffer struct {
	ring        *ringbuffer.RingBuffer
	prevData    []byte // tail of the previous read, used as overlap prefix
	overlapSize int    // number of bytes to retain as overlap
	readSize    int    // number of bytes to read from the ring per call
	sourceID    string // identifier used in log messages
	tracker     *OverwriteTracker
	mu          sync.Mutex
	log         logger.Logger
}

// NewAnalysisBuffer creates an AnalysisBuffer with a ring buffer of the given
// capacity. overlapSize bytes from the end of each read are prepended to the
// next read to implement sliding-window analysis. readSize is the number of
// fresh bytes consumed from the ring per Read call; the total returned slice
// length is overlapSize+readSize.
//
// Returns an error if any dimension is not positive or if sourceID is empty.
func NewAnalysisBuffer(capacity, overlapSize, readSize int, sourceID string, log logger.Logger) (*AnalysisBuffer, error) {
	if capacity <= 0 {
		return nil, errors.Newf("invalid analysis buffer capacity: %d, must be greater than 0", capacity).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "new_analysis_buffer").
			Context("source_id", sourceID).
			Context("requested_capacity", capacity).
			Build()
	}
	if overlapSize < 0 {
		return nil, errors.Newf("invalid overlap size: %d, must be >= 0", overlapSize).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "new_analysis_buffer").
			Context("source_id", sourceID).
			Build()
	}
	if readSize <= 0 {
		return nil, errors.Newf("invalid read size: %d, must be greater than 0", readSize).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "new_analysis_buffer").
			Context("source_id", sourceID).
			Build()
	}
	if readSize < overlapSize {
		return nil, errors.Newf("read size %d must be >= overlap size %d", readSize, overlapSize).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "new_analysis_buffer").
			Context("source_id", sourceID).
			Context("read_size", readSize).
			Context("overlap_size", overlapSize).
			Build()
	}
	if capacity < readSize {
		return nil, errors.Newf("capacity %d must be >= read size %d", capacity, readSize).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "new_analysis_buffer").
			Context("source_id", sourceID).
			Context("capacity", capacity).
			Context("read_size", readSize).
			Build()
	}
	if sourceID == "" {
		return nil, errors.Newf("source ID must not be empty").
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "new_analysis_buffer").
			Build()
	}

	ring := ringbuffer.New(capacity)
	if ring == nil {
		return nil, errors.Newf("failed to allocate ring buffer for analysis buffer").
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("operation", "new_analysis_buffer").
			Context("source_id", sourceID).
			Context("requested_capacity", capacity).
			Build()
	}
	ring.SetOverwrite(true)

	trackerOpts := OverwriteTrackerOpts{
		WindowDuration: analysisOverwriteWindowDuration,
		RateThreshold:  analysisOverwriteRateThreshold,
		MinWrites:      analysisOverwriteMinWrites,
		NotifyCooldown: analysisOverwriteNotifyCooldown,
		Logger:         log,
	}

	return &AnalysisBuffer{
		ring:        ring,
		overlapSize: overlapSize,
		readSize:    readSize,
		sourceID:    sourceID,
		tracker:     NewOverwriteTracker(trackerOpts),
		log:         log,
	}, nil
}

// Write appends data to the ring buffer. When the ring is full, the oldest
// bytes are overwritten (overwrite mode). The overwrite tracker is updated on
// every call so that the rate monitor has an accurate picture of backpressure.
//
// Write is safe for concurrent use.
func (ab *AnalysisBuffer) Write(data []byte) error {
	ab.mu.Lock()
	willOverwrite := len(data) > ab.ring.Free()
	_, err := ab.ring.Write(data)
	ab.mu.Unlock()

	ab.tracker.RecordWrite()
	if willOverwrite {
		ab.tracker.RecordOverwrite()
	}
	ab.tracker.CheckAndNotify(ab.sourceID)

	if err != nil {
		return errors.New(err).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("operation", "analysis_buffer_write").
			Context("source_id", ab.sourceID).
			Context("data_size", len(data)).
			Build()
	}
	return nil
}

// Read returns the next analysis window as a byte slice of length
// overlapSize+readSize. The first overlapSize bytes are the tail of the
// previous successful read (the overlap region); the remaining readSize bytes
// are freshly consumed from the ring buffer.
//
// Returns (nil, nil) when there is not yet enough data in the ring buffer for
// a full readSize-byte read. Callers should treat nil data as "try again
// later" rather than an error.
//
// Read is safe for concurrent use.
func (ab *AnalysisBuffer) Read() ([]byte, error) {
	ab.mu.Lock()
	defer ab.mu.Unlock()

	// Wait until the ring holds at least readSize bytes.
	if ab.ring.Length() < ab.readSize {
		return nil, nil
	}

	fresh := make([]byte, ab.readSize)
	n, err := ab.ring.Read(fresh)
	if err != nil {
		return nil, errors.New(err).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("operation", "analysis_buffer_read").
			Context("source_id", ab.sourceID).
			Context("requested_bytes", ab.readSize).
			Context("bytes_read", n).
			Build()
	}

	// Build the full window: overlap prefix + fresh bytes.
	window := make([]byte, ab.overlapSize+ab.readSize)
	if ab.overlapSize > 0 && len(ab.prevData) == ab.overlapSize {
		copy(window[:ab.overlapSize], ab.prevData)
	}
	copy(window[ab.overlapSize:], fresh[:n])

	// Advance the overlap cursor: save the last overlapSize bytes of fresh data.
	if ab.overlapSize > 0 {
		if ab.prevData == nil {
			ab.prevData = make([]byte, ab.overlapSize)
		}
		if n >= ab.overlapSize {
			copy(ab.prevData, fresh[n-ab.overlapSize:n])
		} else {
			// Fewer fresh bytes than overlap — zero-pad the beginning,
			// place available data at the end so the overlap slice
			// always has exactly overlapSize length.
			clear(ab.prevData)
			copy(ab.prevData[ab.overlapSize-n:], fresh[:n])
		}
	}

	return window, nil
}

// OverwriteCount returns the number of overwrite events recorded in the
// current tracking window. This is useful in tests to assert that the
// overwrite path was exercised.
func (ab *AnalysisBuffer) OverwriteCount() int64 {
	ab.tracker.mu.Lock()
	defer ab.tracker.mu.Unlock()
	return ab.tracker.overwriteCount
}

// Reset clears the ring buffer and resets all overlap and tracking state.
// Useful for testing or restarting a source.
func (ab *AnalysisBuffer) Reset() {
	ab.mu.Lock()
	ab.ring.Reset()
	ab.prevData = nil
	ab.mu.Unlock()
	ab.tracker.Reset()
}
