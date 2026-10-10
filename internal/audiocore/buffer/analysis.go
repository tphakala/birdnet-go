package buffer

import (
	"sync"

	"github.com/smallnest/ringbuffer"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// noopRelease is returned from Read when no pool is in use or when there was
// no window to release. Safe to call any number of times.
func noopRelease() {}

// AnalysisBufferStats holds cumulative counters of one AnalysisBuffer since it
// was allocated. They are never reset, so the difference between two snapshots
// of the same buffer is never negative. A snapshot from a different buffer
// instance (after a reallocation) must not be compared with this one.
type AnalysisBufferStats struct {
	// WrittenBytes is the total number of bytes passed to Write.
	WrittenBytes int64
	// LostBytes is the total number of bytes that never reach a reader: buffered
	// bytes discarded by overwrite mode to make room for newer audio, plus every
	// byte of a write refused for being larger than the ring.
	LostBytes int64
	// WindowsRead is the total number of full windows returned by Read.
	WindowsRead int64
}

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
	windowSize  int    // overlapSize + readSize, cached for pool lookups
	sourceID    string // identifier used in log messages
	mu          sync.Mutex
	log         logger.Logger
	windowPool  *BytePool // nil when unpooled; sized to windowSize when set

	// Cumulative counters, guarded by mu; see AnalysisBufferStats.
	writtenBytes int64
	lostBytes    int64
	windowsRead  int64
}

// NewAnalysisBuffer creates an AnalysisBuffer with a ring buffer of the given
// capacity. overlapSize bytes from the end of each read are prepended to the
// next read to implement sliding-window analysis. readSize is the number of
// fresh bytes consumed from the ring per Read call; the total returned slice
// length is overlapSize+readSize.
//
// bufMgr is optional: when non-nil, the window slices that Read returns are
// sourced from bufMgr.BytePoolFor(overlapSize+readSize) and must be returned
// via the release func that Read returns. When bufMgr is nil (test paths),
// window slices are allocated per Read and the release func is a no-op.
//
// Returns an error if any dimension is not positive or if sourceID is empty.
func NewAnalysisBuffer(capacity, overlapSize, readSize int, sourceID string, log logger.Logger, bufMgr *Manager) (*AnalysisBuffer, error) {
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

	windowSize := overlapSize + readSize
	var windowPool *BytePool
	if bufMgr != nil {
		windowPool = bufMgr.BytePoolFor(windowSize)
	}

	return &AnalysisBuffer{
		ring:        ring,
		overlapSize: overlapSize,
		readSize:    readSize,
		windowSize:  windowSize,
		sourceID:    sourceID,
		log:         log,
		windowPool:  windowPool,
	}, nil
}

// Write appends data to the ring buffer. When the ring is full, the oldest
// bytes are overwritten (overwrite mode), and the cumulative written and lost
// byte counters (see Stats) are advanced. A write larger than the ring is
// refused whole and counted as lost, leaving the buffered audio untouched.
//
// Write is safe for concurrent use.
func (ab *AnalysisBuffer) Write(data []byte) error {
	ab.mu.Lock()
	ab.writtenBytes += int64(len(data))
	var err error
	if len(data) > ab.ring.Capacity() {
		// The ring cannot hold the write, and in overwrite mode it would move
		// its read pointer past the buffered audio, so refuse it.
		ab.lostBytes += int64(len(data))
		err = ringbuffer.ErrTooMuchDataToWrite
	} else {
		// In overwrite mode the ring advances its read pointer by len(data)
		// minus the free space, so that many of the oldest bytes are discarded
		// unread.
		ab.lostBytes += int64(max(len(data)-ab.ring.Free(), 0))
		_, err = ab.ring.Write(data)
	}
	ab.mu.Unlock()

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

// Read returns the next analysis window plus a release func that returns the
// window's backing slice to the pool. release is never nil and is idempotent:
// callers should "defer release()" to guarantee the slice is returned even on
// error paths.
//
// (nil, noopRelease, nil) is still the "try again later" signal when the ring
// has not yet accumulated readSize bytes.
//
// Read is safe for concurrent use. The returned release func is bound to a
// single call chain and must not be invoked from multiple goroutines; in
// practice callers "defer release()" in the same goroutine as the Read call.
func (ab *AnalysisBuffer) Read() (window []byte, release func(), err error) {
	ab.mu.Lock()
	defer ab.mu.Unlock()

	if ab.ring.Length() < ab.readSize {
		return nil, noopRelease, nil
	}

	if ab.windowPool != nil {
		window = ab.windowPool.Get()
	} else {
		window = make([]byte, ab.windowSize)
	}

	if ab.overlapSize > 0 {
		if len(ab.prevData) == ab.overlapSize {
			copy(window[:ab.overlapSize], ab.prevData)
		} else {
			clear(window[:ab.overlapSize])
		}
	}

	n, readErr := ab.ring.Read(window[ab.overlapSize : ab.overlapSize+ab.readSize])
	if readErr != nil {
		if ab.windowPool != nil {
			ab.windowPool.Put(window)
		}
		return nil, noopRelease, errors.New(readErr).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("operation", "analysis_buffer_read").
			Context("source_id", ab.sourceID).
			Context("requested_bytes", ab.readSize).
			Context("bytes_read", n).
			Build()
	}

	if n < ab.readSize {
		clear(window[ab.overlapSize+n:])
	}

	if ab.overlapSize > 0 {
		if ab.prevData == nil {
			ab.prevData = make([]byte, ab.overlapSize)
		}
		// Carry the window's last overlapSize valid bytes forward as the next
		// window's overlap. Those bytes are window[n : overlapSize+n]: on a full
		// read n == readSize, so this is window[readSize : windowSize], the correct
		// overlap region for ANY overlap ratio, including overlap > readSize (more
		// than 50% of the window). The slice stays within the valid region because
		// overlapSize+n <= overlapSize+readSize == windowSize. It correctly retains
		// the older overlap that carried in from window[:overlapSize], which the
		// previous readSize-vs-overlapSize split dropped.
		copy(ab.prevData, window[n:ab.overlapSize+n])
	}

	ab.windowsRead++

	pool := ab.windowPool
	released := false
	release = func() {
		if released || pool == nil {
			return
		}
		released = true
		pool.Put(window)
	}
	return window, release, nil
}

// WindowSize returns the total window size (overlapSize + readSize) in bytes.
// This is the length of the slice returned by Read().
func (ab *AnalysisBuffer) WindowSize() int {
	return ab.windowSize
}

// Stats returns a snapshot of the cumulative counters. It is safe for
// concurrent use.
func (ab *AnalysisBuffer) Stats() AnalysisBufferStats {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	return AnalysisBufferStats{
		WrittenBytes: ab.writtenBytes,
		LostBytes:    ab.lostBytes,
		WindowsRead:  ab.windowsRead,
	}
}

// Reset clears the ring buffer and the overlap state.
// Useful for testing or restarting a source. The cumulative counters reported
// by Stats are not reset.
func (ab *AnalysisBuffer) Reset() {
	ab.mu.Lock()
	ab.ring.Reset()
	ab.prevData = nil
	ab.mu.Unlock()
}
