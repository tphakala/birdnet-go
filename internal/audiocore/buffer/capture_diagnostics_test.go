package buffer

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test buffer shape: 1000 Hz, 16-bit, so one chunk of diagChunkBytes holds one
// second of audio and the 4 s ring (8192 bytes after alignment) wraps on the
// fifth chunk.
const (
	diagSampleRate     = 1000
	diagBytesPerSample = 2
	diagSeconds        = 4
	diagChunkBytes     = diagSampleRate * diagBytesPerSample
)

// newDiagBuffer returns an empty test buffer and a one-second chunk for it.
func newDiagBuffer(t *testing.T) (cb *CaptureBuffer, chunk []byte) {
	t.Helper()
	cb, err := NewCaptureBuffer(diagSeconds, diagSampleRate, diagBytesPerSample, "diag-test")
	require.NoError(t, err)
	return cb, make([]byte, diagChunkBytes)
}

// TestCaptureBufferWriteDiagnosticsGapAndLag checks the gap a write reports and
// that the lag is only computed for a logged write: steady one-second chunks
// log nothing, while a stall of three extra seconds shows as a four-second gap
// with the written audio two seconds behind (5 s elapsed, 3 s written, and the
// first chunk mapped to the time of its own write).
func TestCaptureBufferWriteDiagnosticsGapAndLag(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		cb, chunk := newDiagBuffer(t)

		first := cb.write(chunk)
		assert.Zero(t, first.gap, "the first write has no previous write")
		assert.False(t, first.logged())

		time.Sleep(time.Second)
		steady := cb.write(chunk)
		assert.Equal(t, time.Second, steady.gap)
		assert.False(t, steady.logged())
		assert.Zero(t, steady.lag, "lag is not computed for a write that logs nothing")

		time.Sleep(4 * time.Second)
		stalled := cb.write(chunk)
		assert.True(t, stalled.logged())
		assert.Equal(t, 4*time.Second, stalled.gap)
		assert.Equal(t, 2*time.Second, stalled.lag)
	})
}

// TestCaptureBufferWriteDiagnosticsFirstLapOnce checks that exactly one write,
// the one that wraps the ring for the first time, reports the first lap, and
// that steady capture reaches it at minus one chunk of lag.
func TestCaptureBufferWriteDiagnosticsFirstLapOnce(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		cb, chunk := newDiagBuffer(t)

		var laps []int
		var lapLag time.Duration
		for i := range 12 {
			if i > 0 {
				time.Sleep(time.Second)
			}
			if d := cb.write(chunk); d.firstLap {
				laps = append(laps, i)
				lapLag = d.lag
			}
		}

		require.Equal(t, []int{4}, laps, "only the fifth write completes the first lap")
		assert.Equal(t, -time.Second, lapLag)
	})
}

// TestCaptureBufferResetClearsWriteDiagnostics checks that after Reset the next
// write is treated as a first write again, with no gap from before the reset.
func TestCaptureBufferResetClearsWriteDiagnostics(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		cb, chunk := newDiagBuffer(t)

		cb.write(chunk)
		time.Sleep(10 * time.Second)
		cb.Reset()

		after := cb.write(chunk)
		assert.Zero(t, after.gap)
		assert.False(t, after.logged())
	})
}
