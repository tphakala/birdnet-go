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

	ab, err := buffer.NewAnalysisBuffer(capacity, overlapSize, readSize, "test-source", newTestLogger())
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
	for range 10 {
		got, err = ab.Read()
		require.NoError(t, err)
		if got != nil {
			break
		}
		require.NoError(t, ab.Write(payload))
	}

	require.NotNil(t, got, "expected to read data back after enough writes")
	assert.Len(t, got, overlapSize+readSize, "read should return overlapSize+readSize bytes")
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

	ab, err := buffer.NewAnalysisBuffer(capacity, overlapSize, readSize, "overwrite-source", newTestLogger())
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

	_, err := buffer.NewAnalysisBuffer(4096, 1024, 512, "test-source", newTestLogger())
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

	ab, err := buffer.NewAnalysisBuffer(capacity, overlapSize, readSize, "overlap-source", newTestLogger())
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
	for range 20 {
		writeChunk(0xAA)
		first, readErr = ab.Read()
		require.NoError(t, readErr)
		if first != nil {
			break
		}
	}
	require.NotNil(t, first, "first read should eventually return data")
	assert.Len(t, first, totalSize)

	// The tail of `first` becomes the overlap for the next read.
	// Write another chunk so a second read succeeds.
	for range 5 {
		writeChunk(0xBB)
		second, secErr := ab.Read()
		require.NoError(t, secErr)
		if second != nil {
			// The first overlapSize bytes of second must equal the last overlapSize
			// bytes of first (the overlap region).
			assert.Equal(t, first[readSize:], second[:overlapSize],
				"overlap region of second read must match tail of first read")
			return
		}
	}
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
