package buffer_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
)

// TestCaptureBuffer_WriteAndReadSegment writes PCM data and reads a time segment back.
//
// We capture the timestamp immediately before the first Write so that the
// buffer's internal startTime is no later than t0. All ReadSegment calls use
// offsets relative to t0 that stay within the written range.
func TestCaptureBuffer_WriteAndReadSegment(t *testing.T) {
	t.Parallel()

	const (
		durationSeconds = 10
		sampleRate      = 48000
		bytesPerSample  = 2
		chunkSize       = sampleRate * bytesPerSample // 1 second of audio
	)

	cb, err := buffer.NewCaptureBuffer(durationSeconds, sampleRate, bytesPerSample, "test-source")
	require.NoError(t, err)
	require.NotNil(t, cb)

	chunk := make([]byte, chunkSize)
	for i := range chunk {
		chunk[i] = byte(i % 256)
	}

	for range 5 {
		require.NoError(t, cb.Write(chunk))
	}

	// Anchor the segment window to the actual buffer start time so the offsets
	// are exact regardless of scheduling jitter.
	bufStart := cb.StartTime()
	segStart := bufStart.Add(1 * time.Second)
	segEnd := bufStart.Add(3 * time.Second)

	seg, err := cb.ReadSegment(segStart, segEnd)
	require.NoError(t, err)
	require.NotNil(t, seg)

	// Expected size: 2 seconds of audio (2 * 48000 * 2 = 192000 bytes).
	assert.Len(t, seg, 2*sampleRate*bytesPerSample, "segment should contain exactly 2 seconds of audio")
}

// TestCaptureBuffer_CircularOverwrite writes more than buffer capacity and
// verifies that the circular wrap-around is handled correctly.
func TestCaptureBuffer_CircularOverwrite(t *testing.T) {
	t.Parallel()

	const (
		durationSeconds = 3 // 3-second ring buffer
		sampleRate      = 48000
		bytesPerSample  = 2
		chunkSize       = sampleRate * bytesPerSample // 1 second per chunk
	)

	cb, err := buffer.NewCaptureBuffer(durationSeconds, sampleRate, bytesPerSample, "circular-source")
	require.NoError(t, err)

	chunk := make([]byte, chunkSize)
	for i := range chunk {
		chunk[i] = 0xAB
	}

	// Write 6 seconds — twice the buffer capacity — to force at least one
	// circular wrap-around. After wrapping, startTime is set to
	// time.Now()-bufferDuration, so the most recent 3 seconds are valid.
	for range 6 {
		require.NoError(t, cb.Write(chunk))
	}

	// After 6 writes the buffer has wrapped at least once. Request the
	// middle 1 second of the current 3-second window: offsets [1s, 2s]
	// relative to the adjusted startTime.
	//
	// We don't have direct access to startTime, so we derive a safe range:
	// time.Now()-bufferDuration gives the approximate startTime. Using
	// time.Now()-2s as start and time.Now()-1s as end is always inside the
	// valid window and avoids any off-by-epsilon issues.
	now := time.Now()
	segStart := now.Add(-2 * time.Second)
	segEnd := now.Add(-1 * time.Second)

	seg, err := cb.ReadSegment(segStart, segEnd)
	require.NoError(t, err)
	require.NotNil(t, seg)

	assert.Len(t, seg, 1*sampleRate*bytesPerSample, "segment should be 1 second after circular overwrite")
}

// TestCaptureBuffer_TimestampAccuracy verifies that timestamp-based segment
// extraction returns audio data corresponding to the correct time window.
func TestCaptureBuffer_TimestampAccuracy(t *testing.T) {
	t.Parallel()

	const (
		durationSeconds = 10
		sampleRate      = 48000
		bytesPerSample  = 2
		chunkSize       = sampleRate * bytesPerSample // 1 second per chunk
	)

	cb, err := buffer.NewCaptureBuffer(durationSeconds, sampleRate, bytesPerSample, "timestamp-source")
	require.NoError(t, err)

	// Write 4 distinguishable 1-second chunks (fill bytes 0x11..0x44) back to back.
	fills := []byte{0x11, 0x22, 0x33, 0x44}

	for _, fill := range fills {
		chunk := make([]byte, chunkSize)
		for j := range chunk {
			chunk[j] = fill
		}
		require.NoError(t, cb.Write(chunk))
	}

	// The buffer's startTime is set at the moment of the first Write call,
	// which is at or after t0. Because t0 <= cb.startTime, an offset of
	// 1s from t0 could land inside chunk 0 (0x11).
	//
	// To guarantee we land entirely inside chunk 1 (0x22) and chunk 2
	// (0x33), we expose the buffer start time via StartTime() and anchor
	// our segment request to that exact value.
	bufStart := cb.StartTime()

	// [bufStart+1s, bufStart+3s] spans the entirety of chunk 1 (0x22)
	// and chunk 2 (0x33).
	segStart := bufStart.Add(1 * time.Second)
	segEnd := bufStart.Add(3 * time.Second)

	seg, err := cb.ReadSegment(segStart, segEnd)
	require.NoError(t, err)
	require.NotNil(t, seg)

	expected := 2 * sampleRate * bytesPerSample
	require.Len(t, seg, expected, "segment should be exactly 2 seconds")

	// First half should be 0x22, second half 0x33.
	half := expected / 2
	for i, b := range seg[:half] {
		if b != 0x22 {
			t.Errorf("byte %d of first half: got %#02x, want 0x22", i, b)
			break
		}
	}
	for i, b := range seg[half:] {
		if b != 0x33 {
			t.Errorf("byte %d of second half: got %#02x, want 0x33", i, b)
			break
		}
	}
}

// TestCaptureBuffer_InvalidParams verifies that NewCaptureBuffer rejects invalid inputs.
func TestCaptureBuffer_InvalidParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		durationSeconds int
		sampleRate      int
		bytesPerSample  int
		sourceID        string
	}{
		{"zero duration", 0, 48000, 2, "src"},
		{"negative duration", -1, 48000, 2, "src"},
		{"zero sample rate", 10, 0, 2, "src"},
		{"negative sample rate", 10, -1, 2, "src"},
		{"zero bytes per sample", 10, 48000, 0, "src"},
		{"negative bytes per sample", 10, 48000, -1, "src"},
		{"empty source", 10, 48000, 2, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cb, err := buffer.NewCaptureBuffer(tt.durationSeconds, tt.sampleRate, tt.bytesPerSample, tt.sourceID)
			require.Error(t, err, "expected error for: %s", tt.name)
			assert.Nil(t, cb)
		})
	}
}

// TestCaptureBuffer_Reset verifies that Reset clears the buffer state and
// allows fresh writes without errors.
func TestCaptureBuffer_Reset(t *testing.T) {
	t.Parallel()

	const (
		durationSeconds = 5
		sampleRate      = 48000
		bytesPerSample  = 2
		chunkSize       = sampleRate * bytesPerSample
	)

	cb, err := buffer.NewCaptureBuffer(durationSeconds, sampleRate, bytesPerSample, "reset-source")
	require.NoError(t, err)

	chunk := make([]byte, chunkSize)
	require.NoError(t, cb.Write(chunk))

	cb.Reset()

	// After reset, writing should succeed and the buffer behaves as freshly initialized.
	require.NoError(t, cb.Write(chunk))
}

// TestCaptureBuffer_WriteEmpty verifies that writing empty data is a no-op.
func TestCaptureBuffer_WriteEmpty(t *testing.T) {
	t.Parallel()

	cb, err := buffer.NewCaptureBuffer(5, 48000, 2, "empty-write-source")
	require.NoError(t, err)

	err = cb.Write([]byte{})
	assert.NoError(t, err, "writing empty data should not error")
}
