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

// TestCaptureBufferWriteDiagnosticsGapAndLag checks the gap and lag a write
// reports: steady one-second chunks keep the lag at minus one chunk (the first
// chunk is mapped to the time of its own write), and a stall of three extra
// seconds shows as a four-second gap and a lag that grows by three seconds.
func TestCaptureBufferWriteDiagnosticsGapAndLag(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		cb, err := NewCaptureBuffer(diagSeconds, diagSampleRate, diagBytesPerSample, "diag-test")
		require.NoError(t, err)
		chunk := make([]byte, diagChunkBytes)

		first := cb.write(chunk)
		assert.Zero(t, first.gap, "the first write has no previous write")
		assert.Equal(t, -time.Second, first.lag)

		time.Sleep(time.Second)
		steady := cb.write(chunk)
		assert.Equal(t, time.Second, steady.gap)
		assert.Equal(t, -time.Second, steady.lag)

		time.Sleep(4 * time.Second)
		stalled := cb.write(chunk)
		assert.Equal(t, 4*time.Second, stalled.gap)
		assert.Equal(t, 2*time.Second, stalled.lag, "5 s elapsed, 3 s of audio written")
	})
}

// TestCaptureBufferWriteDiagnosticsFirstLapOnce checks that exactly one write,
// the one that wraps the ring for the first time, reports the first lap, and
// that it carries the elapsed time since the first write.
func TestCaptureBufferWriteDiagnosticsFirstLapOnce(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		cb, err := NewCaptureBuffer(diagSeconds, diagSampleRate, diagBytesPerSample, "diag-test")
		require.NoError(t, err)
		chunk := make([]byte, diagChunkBytes)

		var laps []int
		var lapElapsed time.Duration
		for i := range 12 {
			if i > 0 {
				time.Sleep(time.Second)
			}
			if d := cb.write(chunk); d.firstLap {
				laps = append(laps, i)
				lapElapsed = d.elapsed
			}
		}

		require.Equal(t, []int{4}, laps, "only the fifth write completes the first lap")
		assert.Equal(t, 4*time.Second, lapElapsed)
	})
}

// TestCaptureBufferResetClearsWriteDiagnostics checks that after Reset the next
// write is treated as a first write again, with no gap from before the reset.
func TestCaptureBufferResetClearsWriteDiagnostics(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		cb, err := NewCaptureBuffer(diagSeconds, diagSampleRate, diagBytesPerSample, "diag-test")
		require.NoError(t, err)
		chunk := make([]byte, diagChunkBytes)

		cb.write(chunk)
		time.Sleep(10 * time.Second)
		cb.Reset()

		after := cb.write(chunk)
		assert.Zero(t, after.gap)
		assert.Equal(t, -time.Second, after.lag)
	})
}
