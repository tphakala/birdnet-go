package buffer

import (
	"cmp"
	"slices"
	"sync"
	"sync/atomic"
	"time"

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

	// lastWriteAt is the time of the latest accepted, non-empty Write; guarded by
	// mu. Zero until the first such Write and after Reset.
	lastWriteAt time.Time

	// Ring-space stream positions, guarded by mu. writePos counts the bytes the
	// ring accepted; readPos counts the bytes read or dropped, so the ring holds
	// [readPos, writePos). A splice at position p means the audio just before p
	// and the audio at p are not adjacent in time: construction and Reset (the
	// zero-filled overlap head), overwrite loss (the oldest unread bytes went
	// missing) and a refused write (its bytes never entered the ring).
	writePos int64
	readPos  int64
	// lastSplice is the latest splice at or before the read position; splices
	// ahead of it, in the unread backlog, wait in pendingSplices (ascending).
	// skipped is the sum of the bytes refused by consumed splices: reported
	// positions add it to the ring position so that audio refused at a splice
	// leaves a hole in the position space.
	lastSplice     int64
	skipped        int64
	pendingSplices []splice
	// generation identifies one stretch of this buffer's stream: it is drawn from
	// nextGeneration at construction and again on Reset, so no two buffer
	// instances or resets share one and positions from different generations are
	// never comparable.
	generation uint64
}

// nextGeneration hands out stream generations process wide. A buffer rebuilt for
// the same source (model change, watchdog restart, reconfigure) restarts its
// positions at zero; drawing generations from one counter keeps the new buffer's
// positions from being compared with the old buffer's.
var nextGeneration atomic.Uint64

// newGeneration returns a generation no buffer or Reset has used before. Zero is
// never returned: it means "no positions" to consumers.
func newGeneration() uint64 { return nextGeneration.Add(1) }

// splice marks a point in the ring where the audio before and after are not
// adjacent in time. skip is the number of audio bytes lost at that point without
// ever entering the ring (a refused write); zero for loss that removed bytes
// already counted in the ring positions.
type splice struct {
	pos  int64
	skip int64
}

// WindowInfo describes one analysis window as returned by ReadWindow. It is
// computed under the buffer lock together with the read, so it cannot be skewed
// by a concurrent Write or Reset.
type WindowInfo struct {
	// LastWriteAt is the time of the latest Write when the window was read; zero
	// before the first Write. The newest unread byte, and so the end of the
	// audio still queued behind this window, was written then.
	LastWriteAt time.Time
	// Backlog is the number of bytes still unread in the ring after this read.
	// The capture time of the window's last sample is LastWriteAt minus the
	// duration of Backlog bytes.
	Backlog int
	// FreshBytes is how many bytes at the end of the window are new audio: the
	// step read from the ring, or the whole window when its overlap head is zero
	// filled. The rest repeats audio an earlier window already carried.
	FreshBytes int
	// Gap reports that the window's fresh part is not contiguous with the audio
	// before it: a splice (construction, Reset, overwrite loss or a refused write)
	// lies at or inside the fresh part. A splice in the overlap head only shortens
	// ContiguousBytes.
	Gap bool
	// ContiguousBytes is how many bytes at the end of the window are genuine
	// audio, adjacent in time with no splice: the window end minus the later of
	// the head start and the latest splice. A zero-filled or pre-loss head is
	// not counted.
	ContiguousBytes int
	// EndPos is the stream position just after the window's last byte: the ring
	// position plus every byte refused before it, so audio lost without entering
	// the ring still leaves a hole. EndPos minus ContiguousBytes is where the
	// contiguous tail starts. Two windows of the same Generation are adjacent when
	// the later tail starts at or before the earlier EndPos.
	EndPos int64
	// Generation identifies the stretch of stream EndPos belongs to. It is unique
	// per buffer instance and per Reset (see newGeneration), so a rebuilt buffer
	// never shares one with the buffer it replaces.
	Generation uint64
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
// The first window read is reported as a gap in its WindowInfo and counts only
// its fresh part as contiguous.
//
// Each buffer reports its own stream generation, never shared with another
// buffer or an earlier Reset, so a buffer built to replace another one is not
// mistaken for its continuation.
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
		// The first window's overlap head is zero filled, so the stream starts
		// with a splice at position 0.
		pendingSplices: []splice{{pos: 0}},
		generation:     newGeneration(),
	}, nil
}

// Write appends data to the ring buffer. When the ring is full, the oldest
// bytes are overwritten (overwrite mode), and the cumulative written and lost
// byte counters (see Stats) are advanced. A write larger than the ring is
// refused whole and counted as lost, leaving the buffered audio untouched.
//
// Only an accepted, non-empty write records the time reported in WindowInfo: a
// refused write adds no audio to the ring, and an empty one adds nothing. A
// refused write and overwrite loss each record a splice, so the window that
// joins the audio on either side of the loss is the one reported as a gap. The
// bytes of a refused write count toward the positions reported in WindowInfo, so
// the loss leaves a hole there even when the ring had been drained.
//
// Write is safe for concurrent use.
func (ab *AnalysisBuffer) Write(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	now := time.Now()
	ab.mu.Lock()
	ab.writtenBytes += int64(len(data))
	var err error
	if len(data) > ab.ring.Capacity() {
		// The ring cannot hold the write, and in overwrite mode it would move
		// its read pointer past the buffered audio, so refuse it. The audio on
		// either side of the refused bytes is no longer adjacent.
		ab.lostBytes += int64(len(data))
		ab.addSpliceLocked(splice{pos: ab.writePos, skip: int64(len(data))})
		err = ringbuffer.ErrTooMuchDataToWrite
	} else {
		// In overwrite mode the ring advances its read pointer by len(data)
		// minus the free space, so that many of the oldest bytes are discarded
		// unread.
		dropped := int64(max(len(data)-ab.ring.Free(), 0))
		ab.lostBytes += dropped
		if dropped > 0 {
			ab.readPos += dropped
			ab.addSpliceLocked(splice{pos: ab.readPos})
		}
		if _, err = ab.ring.Write(data); err == nil {
			ab.writePos += int64(len(data))
			ab.lastWriteAt = now
		}
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
	window, _, release, err = ab.ReadWindow()
	return window, release, err
}

// ReadWindow is Read plus a WindowInfo describing the window, computed under the
// same lock as the read: its gap flag, contiguous tail, stream positions and the
// backlog and write time its audio end is derived from, and the stream position
// and generation consumers use to tell whether two windows are adjacent. info is
// the zero value when no window is returned.
func (ab *AnalysisBuffer) ReadWindow() (window []byte, info WindowInfo, release func(), err error) {
	ab.mu.Lock()
	defer ab.mu.Unlock()

	if ab.ring.Length() < ab.readSize {
		return nil, WindowInfo{}, noopRelease, nil
	}

	if ab.windowPool != nil {
		window = ab.windowPool.Get()
	} else {
		window = make([]byte, ab.windowSize)
	}

	// fresh counts the window bytes that are new audio: the step read from the
	// ring, or the whole window when the overlap head is zero filled.
	freshStart := ab.readPos
	fresh := ab.readSize
	if ab.overlapSize > 0 {
		if len(ab.prevData) == ab.overlapSize {
			copy(window[:ab.overlapSize], ab.prevData)
		} else {
			clear(window[:ab.overlapSize])
			fresh = ab.windowSize
		}
	}

	n, readErr := ab.ring.Read(window[ab.overlapSize : ab.overlapSize+ab.readSize])
	if readErr != nil {
		if ab.windowPool != nil {
			ab.windowPool.Put(window)
		}
		return nil, WindowInfo{}, noopRelease, errors.New(readErr).
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
	ab.readPos += int64(n)
	// Splices before the window end are now behind it: one inside the fresh part
	// makes the window a gap, and the latest one bounds its contiguous tail.
	gap := false
	for len(ab.pendingSplices) > 0 && ab.pendingSplices[0].pos < ab.readPos {
		sp := ab.pendingSplices[0]
		ab.pendingSplices = ab.pendingSplices[1:]
		gap = gap || sp.pos >= freshStart
		ab.lastSplice = max(ab.lastSplice, sp.pos)
		ab.skipped += sp.skip
	}
	tailStart := max(freshStart-int64(ab.overlapSize), ab.lastSplice)
	info = WindowInfo{
		LastWriteAt:     ab.lastWriteAt,
		Backlog:         ab.ring.Length(),
		FreshBytes:      fresh,
		Gap:             gap,
		ContiguousBytes: int(ab.readPos - tailStart),
		EndPos:          ab.readPos + ab.skipped,
		Generation:      ab.generation,
	}

	pool := ab.windowPool
	released := false
	release = func() {
		if released || pool == nil {
			return
		}
		released = true
		pool.Put(window)
	}
	return window, info, release, nil
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

// Reset clears the ring buffer, the overlap state and the last write time. The
// unread audio is dropped, so the next window starts with a splice and a zero
// filled overlap head, and the stream gets a new generation, so positions
// from before the Reset are never compared with later ones.
// Useful for testing or restarting a source. The cumulative counters reported
// by Stats are not reset.
func (ab *AnalysisBuffer) Reset() {
	ab.mu.Lock()
	ab.ring.Reset()
	ab.prevData = nil
	ab.readPos = ab.writePos
	ab.pendingSplices = ab.pendingSplices[:0]
	ab.addSpliceLocked(splice{pos: ab.readPos})
	ab.generation = newGeneration()
	ab.lastWriteAt = time.Time{}
	ab.mu.Unlock()
}

// addSpliceLocked records a splice, keeping pendingSplices ascending by position
// and merging splices at the same position (their skipped bytes add up). The
// caller must hold mu.
func (ab *AnalysisBuffer) addSpliceLocked(sp splice) {
	i, found := slices.BinarySearchFunc(ab.pendingSplices, sp.pos, func(e splice, pos int64) int {
		return cmp.Compare(e.pos, pos)
	})
	if found {
		ab.pendingSplices[i].skip += sp.skip
		return
	}
	ab.pendingSplices = slices.Insert(ab.pendingSplices, i, sp)
}
