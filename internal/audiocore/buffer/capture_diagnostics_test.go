package buffer

import (
	"regexp"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/logger/logtest"
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

// TestCaptureBufferWriteDiagnosticsGapAndLag checks the gap and lag a write
// reports: steady one-second chunks log nothing, while a stall of three extra
// seconds shows as a four-second gap with the written audio two seconds behind
// (5 s elapsed, 3 s written, and the first chunk mapped to the time of its own
// write).
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
// write is treated as a first write again, with no gap from before the reset,
// and that the first lap is reported again: once before Reset and once after.
func TestCaptureBufferResetClearsWriteDiagnostics(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		cb, chunk := newDiagBuffer(t)
		laps := func() int {
			n := 0
			for range 5 {
				if cb.write(chunk).firstLap {
					n++
				}
			}
			return n
		}

		require.Equal(t, 1, laps(), "five chunks wrap the 4 s ring once")
		time.Sleep(10 * time.Second)
		cb.Reset()

		after := cb.write(chunk)
		assert.Zero(t, after.gap)
		assert.False(t, after.logged())
		assert.Equal(t, 1, laps(), "the first lap is reported again after Reset")
	})
}

// logTime returns the time logged for key in captured text-handler output.
func logTime(t *testing.T, out, key string) time.Time {
	t.Helper()
	m := regexp.MustCompile(key + `=(\S+)`).FindStringSubmatch(out)
	require.Len(t, m, 2, "no %s in the log output", key)
	ts, err := time.Parse(time.RFC3339Nano, m[1])
	require.NoError(t, err)
	return ts
}

// TestCaptureBufferWarmupLogKeepsRequestedStart checks that a read failing in
// the warmup window logs the start the caller asked for, one second before the
// first write, rather than the start clamped to the buffer. Not parallel: it
// replaces the global logger.
func TestCaptureBufferWarmupLogKeepsRequestedStart(t *testing.T) {
	logs := logtest.CaptureBuffer(t)

	synctest.Test(t, func(t *testing.T) {
		cb, chunk := newDiagBuffer(t)
		cb.write(chunk)

		now := time.Now()
		_, err := cb.ReadSegment(now.Add(-time.Second), now.Add(3*time.Second))
		require.ErrorIs(t, err, ErrInsufficientData)
	})

	out := logs.String()
	require.Contains(t, out, "capture buffer read segment during warmup")
	assert.Equal(t, time.Second, logTime(t, out, "buffer_start").Sub(logTime(t, out, "requested_start")))
}

// TestCaptureBufferWriteLogsGapAboveThreshold checks the gap line through
// Write: a pause of exactly the threshold logs nothing, one nanosecond more
// logs the gap with its fields. Not parallel: it replaces the global logger.
func TestCaptureBufferWriteLogsGapAboveThreshold(t *testing.T) {
	logs := logtest.CaptureBuffer(t)

	synctest.Test(t, func(t *testing.T) {
		cb, chunk := newDiagBuffer(t)
		require.NoError(t, cb.Write(chunk))

		time.Sleep(captureWriteGapLogThreshold)
		require.NoError(t, cb.Write(chunk))
		assert.NotContains(t, logs.String(), "capture buffer write gap", "a pause of exactly the threshold is not a gap")

		time.Sleep(captureWriteGapLogThreshold + time.Nanosecond)
		require.NoError(t, cb.Write(chunk))
	})

	out := logs.String()
	assert.Equal(t, 1, strings.Count(out, "capture buffer write gap"))
	assert.Contains(t, out, "source=diag-test")
	assert.Contains(t, out, "gap_ms=2000 ")
	assert.Contains(t, out, "lag_ms=1000", "4 s since the first write, 3 s of audio written")
}

// TestCaptureBufferWriteLogsFirstLapOnce checks the first lap line through
// Write: twelve one-second writes into the 4 s ring wrap twice but log
// the first lap once, at minus one chunk of lag. Not parallel: it replaces the
// global logger.
func TestCaptureBufferWriteLogsFirstLapOnce(t *testing.T) {
	logs := logtest.CaptureBuffer(t)

	synctest.Test(t, func(t *testing.T) {
		cb, chunk := newDiagBuffer(t)
		for i := range 12 {
			if i > 0 {
				time.Sleep(time.Second)
			}
			require.NoError(t, cb.Write(chunk))
		}
	})

	out := logs.String()
	assert.Equal(t, 1, strings.Count(out, "capture buffer completed its first lap"))
	assert.Contains(t, out, "source=diag-test")
	assert.Contains(t, out, "lag_ms=-1000")
	assert.NotContains(t, out, "capture buffer write gap")
}

// TestCaptureBufferWriteLogsStalledFirstLap checks that a write which both
// follows a stall and completes the first lap logs both lines: four steady
// chunks, a 3 s pause, then the fifth chunk wraps the ring. Not parallel: it
// replaces the global logger.
func TestCaptureBufferWriteLogsStalledFirstLap(t *testing.T) {
	logs := logtest.CaptureBuffer(t)

	synctest.Test(t, func(t *testing.T) {
		cb, chunk := newDiagBuffer(t)
		for i := range 4 {
			if i > 0 {
				time.Sleep(time.Second)
			}
			require.NoError(t, cb.Write(chunk))
		}
		time.Sleep(3 * time.Second)
		require.NoError(t, cb.Write(chunk))
	})

	out := logs.String()
	assert.Equal(t, 1, strings.Count(out, "capture buffer write gap"))
	assert.Equal(t, 1, strings.Count(out, "capture buffer completed its first lap"))
	assert.Equal(t, 2, strings.Count(out, "lag_ms=1000"), "6 s since the first write, 5 s of audio written")
}

// TestCaptureBufferWriteDiagnosticsWholeRingWrite checks that a single write of
// the whole ring, or more, completes the first lap, and that a second whole-ring
// write, which wraps again, does not report it again.
func TestCaptureBufferWriteDiagnosticsWholeRingWrite(t *testing.T) {
	t.Parallel()

	for _, extra := range []int{0, diagChunkBytes} {
		synctest.Test(t, func(t *testing.T) {
			cb, _ := newDiagBuffer(t)
			ring := make([]byte, cb.Capacity()+extra)

			assert.True(t, cb.write(ring).firstLap, "write of %d bytes past the ring", extra)
			assert.False(t, cb.write(ring).firstLap)
		})
	}
}

// TestCaptureBufferWriteDiagnosticsThresholdIsExclusive checks that a pause of
// exactly the threshold is not a logged write and one nanosecond more is.
func TestCaptureBufferWriteDiagnosticsThresholdIsExclusive(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		cb, chunk := newDiagBuffer(t)
		cb.write(chunk)

		time.Sleep(captureWriteGapLogThreshold)
		assert.False(t, cb.write(chunk).logged())

		time.Sleep(captureWriteGapLogThreshold + time.Nanosecond)
		assert.True(t, cb.write(chunk).logged())
	})
}
