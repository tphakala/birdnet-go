// Package buffer_test contains tests for the buffer subpackage.
package buffer_test

import (
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// newTestLogger returns a silent logger suitable for tests.
func newTestLogger() logger.Logger {
	return logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC)
}

// TestAnalysisBuffer_WriteRead verifies that data written to the buffer can be
// read back and forms the expected output bytes.
func TestAnalysisBuffer_WriteRead(t *testing.T) {
	t.Parallel()

	const (
		capacity    = 64 * 1024 // 64 KiB ring buffer
		overlapSize = 512
		readSize    = 1024
	)

	ab, err := buffer.NewAnalysisBuffer(capacity, overlapSize, readSize, "test-source", newTestLogger(), nil)
	require.NoError(t, err)

	// Write enough data so that one full read is possible (readSize bytes).
	payload := make([]byte, readSize)
	for i := range payload {
		payload[i] = byte(i % 256)
	}
	require.NoError(t, ab.Write(payload))

	// The first read should return nil because prevData+ring hasn't reached
	// overlapSize+readSize yet (no previous overlap data yet).
	// Keep writing and reading until we get data back.
	var got []byte
	var release func()
	for range 10 {
		got, release, err = ab.Read()
		require.NoError(t, err)
		if got != nil {
			break
		}
		release()
		require.NoError(t, ab.Write(payload))
	}

	require.NotNil(t, got, "expected to read data back after enough writes")
	assert.Len(t, got, overlapSize+readSize, "read should return overlapSize+readSize bytes")
	release()
}

// TestAnalysisBuffer_Overwrite verifies that filling the buffer past capacity
// triggers overwrite tracking (the tracker records overwrites).
func TestAnalysisBuffer_Overwrite(t *testing.T) {
	t.Parallel()

	const (
		capacity    = 4 * 1024 // small 4 KiB buffer to force overwrites quickly
		overlapSize = 128
		readSize    = 256
	)

	ab, err := buffer.NewAnalysisBuffer(capacity, overlapSize, readSize, "overwrite-source", newTestLogger(), nil)
	require.NoError(t, err)

	// Write data that exceeds the buffer capacity to force overwrites.
	chunk := make([]byte, 1024)
	for i := range chunk {
		chunk[i] = byte(i % 256)
	}

	var overwriteSeen bool
	for range 20 {
		writeErr := ab.Write(chunk)
		require.NoError(t, writeErr)
		if ab.OverwriteCount() > 0 {
			overwriteSeen = true
			break
		}
	}

	assert.True(t, overwriteSeen, "expected overwrite to be recorded after filling buffer")
}

// TestAnalysisBuffer_ReadSizeLessThanOverlapSize verifies that the constructor
// rejects readSize < overlapSize with a validation error.
func TestAnalysisBuffer_ReadSizeLessThanOverlapSize(t *testing.T) {
	t.Parallel()

	_, err := buffer.NewAnalysisBuffer(4096, 1024, 512, "test-source", newTestLogger(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read size 512 must be >= overlap size 1024")
}

// TestAnalysisBuffer_OverlapRead verifies that consecutive reads include the
// correct overlap from the previous read.
func TestAnalysisBuffer_OverlapRead(t *testing.T) {
	t.Parallel()

	const (
		capacity    = 64 * 1024
		overlapSize = 512
		readSize    = 1024
		totalSize   = overlapSize + readSize
	)

	ab, err := buffer.NewAnalysisBuffer(capacity, overlapSize, readSize, "overlap-source", newTestLogger(), nil)
	require.NoError(t, err)

	// Helper: write enough bytes for one full chunk to be readable.
	writeChunk := func(fill byte) {
		t.Helper()
		buf := make([]byte, readSize)
		for i := range buf {
			buf[i] = fill
		}
		require.NoError(t, ab.Write(buf))
	}

	// Pump writes until we get a first successful read.
	var first []byte
	var readErr error
	var firstRelease func()
	for range 20 {
		writeChunk(0xAA)
		first, firstRelease, readErr = ab.Read()
		require.NoError(t, readErr)
		if first != nil {
			break
		}
		firstRelease()
	}
	require.NotNil(t, first, "first read should eventually return data")
	assert.Len(t, first, totalSize)

	// The tail of `first` becomes the overlap for the next read.
	// Write another chunk so a second read succeeds.
	for range 5 {
		writeChunk(0xBB)
		second, secondRelease, secErr := ab.Read()
		require.NoError(t, secErr)
		if second != nil {
			// The first overlapSize bytes of second must equal the last overlapSize
			// bytes of first (the overlap region).
			assert.Equal(t, first[readSize:], second[:overlapSize],
				"overlap region of second read must match tail of first read")
			secondRelease()
			firstRelease()
			return
		}
		secondRelease()
	}
	firstRelease()
	t.Fatal("second read did not return data in time")
}

// TestOverwriteTracker_RateCalculation verifies that the overwrite rate is
// correctly computed within the sliding window.
func TestOverwriteTracker_RateCalculation(t *testing.T) {
	t.Parallel()

	opts := buffer.OverwriteTrackerOpts{
		WindowDuration: 5 * time.Minute,
		RateThreshold:  10,
		MinWrites:      50,
		NotifyCooldown: 1 * time.Hour,
		Logger:         newTestLogger(),
	}
	tracker := buffer.NewOverwriteTracker(opts)

	// Record 100 writes with 20 overwrites → 20% rate.
	for range 80 {
		tracker.RecordWrite()
	}
	for range 20 {
		tracker.RecordWrite()
		tracker.RecordOverwrite()
	}

	rate := tracker.OverwriteRate()
	assert.InDelta(t, 20.0, rate, 0.01, "overwrite rate should be approximately 20%")

	// After Reset, rate should be zero.
	tracker.Reset()
	assert.InDelta(t, 0.0, tracker.OverwriteRate(), 0.01, "rate should be zero after Reset")
}

// TestAnalysisBuffer_Read_ContentParity asserts that consecutive Read calls
// return byte-identical windows to what the pre-refactor implementation would
// produce: an overlapSize prefix of prior fresh tail bytes, followed by
// readSize fresh bytes from the ring. Guards the in-place ring.Read refactor
// against off-by-one errors and prevData advancement regressions.
func TestAnalysisBuffer_Read_ContentParity(t *testing.T) {
	t.Parallel()
	const (
		capacity    = 4096
		overlapSize = 32
		readSize    = 128
	)
	ab, err := buffer.NewAnalysisBuffer(capacity, overlapSize, readSize, "parity-source", newTestLogger(), nil)
	require.NoError(t, err)

	// Write a monotonically increasing byte stream so we can predict contents.
	stream := make([]byte, readSize*4)
	for i := range stream {
		stream[i] = byte(i & 0xFF)
	}
	require.NoError(t, ab.Write(stream))

	// First Read: no prior overlap; prefix is zero-filled, fresh region is
	// stream[0:readSize].
	win1, release1, err := ab.Read()
	require.NoError(t, err)
	require.Len(t, win1, overlapSize+readSize)

	wantPrefix1 := make([]byte, overlapSize) // zeros
	assert.Equal(t, wantPrefix1, win1[:overlapSize], "first Read prefix must be zero")
	assert.Equal(t, stream[0:readSize], win1[overlapSize:], "first Read fresh region")
	release1()

	// Second Read: prefix is the tail overlapSize bytes of the previous
	// fresh region (stream[readSize-overlapSize:readSize]); fresh is
	// stream[readSize:2*readSize].
	win2, release2, err := ab.Read()
	require.NoError(t, err)
	require.Len(t, win2, overlapSize+readSize)

	assert.Equal(t, stream[readSize-overlapSize:readSize], win2[:overlapSize], "second Read prefix")
	assert.Equal(t, stream[readSize:2*readSize], win2[overlapSize:], "second Read fresh region")
	release2()
}

// TestAnalysisBuffer_Read_ReleaseIdempotent verifies that calling the release
// func multiple times is safe and does not double-Put to the pool.
func TestAnalysisBuffer_Read_ReleaseIdempotent(t *testing.T) {
	t.Parallel()
	const (
		capacity    = 4096
		overlapSize = 32
		readSize    = 128
	)
	mgr := buffer.NewManager(newTestLogger())
	ab, err := buffer.NewAnalysisBuffer(capacity, overlapSize, readSize, "idemp-source", newTestLogger(), mgr)
	require.NoError(t, err)

	require.NoError(t, ab.Write(make([]byte, readSize*2)))

	_, release, err := ab.Read()
	require.NoError(t, err)
	require.NotNil(t, release)

	pool := mgr.BytePoolFor(overlapSize + readSize)
	require.NotNil(t, pool)

	beforeStats := pool.GetStats()
	release()
	release()
	release()
	afterStats := pool.GetStats()

	assert.Equal(t, beforeStats.Discarded, afterStats.Discarded,
		"release must not double-Put (which would increment Discarded on a mismatched second call)")
}

// TestAnalysisBuffer_Read_TryAgainLaterReleaseIsNoop asserts the sentinel
// release on the try-again-later path is safe.
func TestAnalysisBuffer_Read_TryAgainLaterReleaseIsNoop(t *testing.T) {
	t.Parallel()
	mgr := buffer.NewManager(newTestLogger())
	ab, err := buffer.NewAnalysisBuffer(4096, 32, 128, "try-again-source", newTestLogger(), mgr)
	require.NoError(t, err)

	data, release, err := ab.Read()
	require.NoError(t, err)
	assert.Nil(t, data)
	require.NotNil(t, release)

	pool := mgr.BytePoolFor(32 + 128)
	require.NotNil(t, pool)
	before := pool.GetStats()
	release()
	release()
	after := pool.GetStats()
	assert.Equal(t, before, after, "try-again-later release must be a no-op")
}

// TestAnalysisBuffer_Read_BoundedAllocsWhenWarm asserts the pooled Read path
// stays within a small, fixed allocation budget per iteration once the pool
// is warm. See the per-allocation accounting in the assertion comment.
func TestAnalysisBuffer_Read_BoundedAllocsWhenWarm(t *testing.T) {
	const (
		capacity    = 32768
		overlapSize = 128
		readSize    = 512
	)
	mgr := buffer.NewManager(newTestLogger())
	ab, err := buffer.NewAnalysisBuffer(capacity, overlapSize, readSize, "bounded-alloc-source", newTestLogger(), mgr)
	require.NoError(t, err)

	payload := make([]byte, readSize*2)
	for i := range payload {
		payload[i] = byte(i)
	}

	require.NoError(t, ab.Write(payload))
	_, release, err := ab.Read()
	require.NoError(t, err)
	release()

	allocs := testing.AllocsPerRun(100, func() {
		if err := ab.Write(payload); err != nil {
			t.Fatal(err)
		}
		_, release, err := ab.Read()
		if err != nil {
			t.Fatal(err)
		}
		release()
	})

	// The pooled Read path retains three small unavoidable allocations per
	// call on top of whatever sync.Pool happens to cache:
	//   1. the release closure escapes because it is returned to the caller,
	//   2. the closure captures a local `released` bool by reference,
	//   3. buffer.BytePool.Put boxes the []byte into an interface{} to store
	//      it in sync.Pool (accepted project-wide via SA6002 nolint).
	// Under -race the counter can tick up to 4; budget 5 to keep the test
	// stable while still catching a regression to the per-Read
	// make([]byte, windowSize) that Task 2 eliminated (a ~640-byte alloc
	// per tick which would blow this budget immediately).
	const maxAllocsPerWarmRead = 5
	assert.LessOrEqualf(t, allocs, float64(maxAllocsPerWarmRead),
		"AnalysisBuffer.Read + Write loop must stay within %d allocs/op once warm; got %v. A large jump usually means the window slice returned to make() rather than the pool.",
		maxAllocsPerWarmRead, allocs)
}

// BenchmarkAnalysisBuffer_Read reports the per-Read allocation rate on the
// pooled path. Advisory only; the regression gate is
// TestAnalysisBuffer_Read_BoundedAllocsWhenWarm.
func BenchmarkAnalysisBuffer_Read(b *testing.B) {
	const (
		capacity    = 32768
		overlapSize = 128
		readSize    = 512
	)
	mgr := buffer.NewManager(newTestLogger())
	ab, err := buffer.NewAnalysisBuffer(capacity, overlapSize, readSize, "bench-source", newTestLogger(), mgr)
	require.NoError(b, err)

	payload := make([]byte, readSize*2)
	for i := range payload {
		payload[i] = byte(i)
	}

	// Warm.
	require.NoError(b, ab.Write(payload))
	_, release, err := ab.Read()
	require.NoError(b, err)
	release()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := ab.Write(payload); err != nil {
			b.Fatal(err)
		}
		_, release, err := ab.Read()
		if err != nil {
			b.Fatal(err)
		}
		release()
	}
}
