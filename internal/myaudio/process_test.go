package myaudio

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// resetOverrunTracker resets the package-level overrun tracker to zero state
// and registers a t.Cleanup to restore it after the test completes.
func resetOverrunTracker(t *testing.T) {
	t.Helper()
	clearTracker := func() {
		overrunTracker.mu.Lock()
		defer overrunTracker.mu.Unlock()
		overrunTracker.overrunCount = 0
		overrunTracker.maxElapsed = 0
		overrunTracker.bufferLength = 0
		overrunTracker.windowStart = time.Time{}
	}
	clearTracker()
	t.Cleanup(clearTracker)
}

func TestRecordBufferOverrun_CountsOverruns(t *testing.T) {
	resetOverrunTracker(t)

	// Record several overruns within the window
	for range 5 {
		recordBufferOverrun(250*time.Millisecond, 200*time.Millisecond)
	}

	overrunTracker.mu.Lock()
	defer overrunTracker.mu.Unlock()
	assert.Equal(t, int64(5), overrunTracker.overrunCount)
}

func TestRecordBufferOverrun_TracksMaxElapsed(t *testing.T) {
	resetOverrunTracker(t)

	recordBufferOverrun(210*time.Millisecond, 200*time.Millisecond)
	recordBufferOverrun(260*time.Millisecond, 200*time.Millisecond)
	recordBufferOverrun(230*time.Millisecond, 200*time.Millisecond)

	overrunTracker.mu.Lock()
	defer overrunTracker.mu.Unlock()
	assert.Equal(t, 260*time.Millisecond, overrunTracker.maxElapsed)
	assert.Equal(t, 200*time.Millisecond, overrunTracker.bufferLength)
}

func TestRecordBufferOverrun_InitializesWindowOnFirstCall(t *testing.T) {
	resetOverrunTracker(t)

	before := time.Now()
	recordBufferOverrun(250*time.Millisecond, 200*time.Millisecond)
	after := time.Now()

	overrunTracker.mu.Lock()
	defer overrunTracker.mu.Unlock()
	assert.False(t, overrunTracker.windowStart.IsZero(), "window start should be set")
	assert.False(t, overrunTracker.windowStart.Before(before), "window start should be >= before")
	assert.False(t, overrunTracker.windowStart.After(after), "window start should be <= after")
}

func TestRecordBufferOverrun_WindowResetAfterCooldown(t *testing.T) {
	resetOverrunTracker(t)

	// Set window start to past (simulate cooldown elapsed)
	overrunTracker.mu.Lock()
	overrunTracker.windowStart = time.Now().Add(-2 * bufferOverrunReportCooldown)
	overrunTracker.overrunCount = 5 // below threshold — should not report
	overrunTracker.maxElapsed = 250 * time.Millisecond
	overrunTracker.mu.Unlock()

	// This call triggers window expiry + reset
	recordBufferOverrun(220*time.Millisecond, 200*time.Millisecond)

	overrunTracker.mu.Lock()
	defer overrunTracker.mu.Unlock()
	// Counter should be 1 (only the new overrun after reset)
	assert.Equal(t, int64(1), overrunTracker.overrunCount)
	// maxElapsed should be from the new overrun only
	assert.Equal(t, 220*time.Millisecond, overrunTracker.maxElapsed)
}

func TestRecordBufferOverrun_ResetAfterThresholdReport(t *testing.T) {
	resetOverrunTracker(t)

	// Set window start to past with count at threshold
	overrunTracker.mu.Lock()
	overrunTracker.windowStart = time.Now().Add(-2 * bufferOverrunReportCooldown)
	overrunTracker.overrunCount = bufferOverrunMinCount // at threshold — would report if telemetry enabled
	overrunTracker.maxElapsed = 300 * time.Millisecond
	overrunTracker.bufferLength = 200 * time.Millisecond
	overrunTracker.mu.Unlock()

	// Trigger window expiry — report fires (silently, telemetry disabled in test)
	recordBufferOverrun(210*time.Millisecond, 200*time.Millisecond)

	overrunTracker.mu.Lock()
	defer overrunTracker.mu.Unlock()
	// Counters reset, then new overrun recorded
	assert.Equal(t, int64(1), overrunTracker.overrunCount)
	assert.Equal(t, 210*time.Millisecond, overrunTracker.maxElapsed)
}

func TestRecordBufferOverrun_NoResetBeforeCooldown(t *testing.T) {
	resetOverrunTracker(t)

	// Record overruns — window just started, cooldown hasn't elapsed
	for range bufferOverrunMinCount + 5 {
		recordBufferOverrun(250*time.Millisecond, 200*time.Millisecond)
	}

	overrunTracker.mu.Lock()
	defer overrunTracker.mu.Unlock()
	// All overruns should be accumulated, no reset
	assert.Equal(t, int64(bufferOverrunMinCount+5), overrunTracker.overrunCount)
}

func TestRecordBufferOverrun_BufferLengthTracksWorstCase(t *testing.T) {
	resetOverrunTracker(t)

	// Different buffer lengths — should track the one associated with max elapsed
	recordBufferOverrun(210*time.Millisecond, 200*time.Millisecond)
	recordBufferOverrun(260*time.Millisecond, 150*time.Millisecond) // worst overrun, different buffer length
	recordBufferOverrun(230*time.Millisecond, 180*time.Millisecond)

	overrunTracker.mu.Lock()
	defer overrunTracker.mu.Unlock()
	assert.Equal(t, 260*time.Millisecond, overrunTracker.maxElapsed)
	assert.Equal(t, 150*time.Millisecond, overrunTracker.bufferLength, "bufferLength should match the worst-case overrun")
}
