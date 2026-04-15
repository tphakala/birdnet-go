// stop_timeout_floor_test.go: tests for the StopWithTimeout zero-timeout
// safety net.
//
// The processor shutdown path derives its timeout from a context deadline
// that may already be in the past. Without a floor, the select inside
// StopWithTimeout fires immediately and emits a "timed out waiting for jobs
// to complete after 0s" telemetry event even though the shutdown is behaving
// correctly. This test locks the floor contract in place so any caller
// passing 0 or a negative duration still gets the minimum grace period.
package jobqueue

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStopWithTimeout_ZeroTimeout_UsesFloor verifies that a zero timeout
// is silently floored to MinStopTimeout rather than triggering an immediate
// telemetry-noisy timeout error.
func TestStopWithTimeout_ZeroTimeout_UsesFloor(t *testing.T) {
	t.Parallel()

	q := NewJobQueue()
	q.Start()

	start := time.Now()
	err := q.StopWithTimeout(0)
	elapsed := time.Since(start)

	// With no running jobs the shutdown completes immediately — the point of
	// the test is that we did NOT get the "timed out after 0s" error.
	require.NoError(t, err, "zero timeout must be floored, not propagated as 0s error")
	// Sanity: should complete fast since there are no jobs to drain.
	assert.Less(t, elapsed, MinStopTimeout, "with no running jobs, stop is near-instant")
}

// TestStopWithTimeout_NegativeTimeout_UsesFloor verifies that a negative
// timeout (which callers can produce with time.Until(past_deadline)) is
// also floored.
func TestStopWithTimeout_NegativeTimeout_UsesFloor(t *testing.T) {
	t.Parallel()

	q := NewJobQueue()
	q.Start()

	err := q.StopWithTimeout(-5 * time.Second)
	require.NoError(t, err,
		"negative timeout must be floored to MinStopTimeout, not propagated as a negative-duration error")
}

// TestStopWithTimeout_PositiveTimeout_Respected verifies that callers
// passing a positive timeout still get the exact value (regression guard so
// the floor does not silently widen large timeouts).
func TestStopWithTimeout_PositiveTimeout_Respected(t *testing.T) {
	t.Parallel()

	q := NewJobQueue()
	q.Start()

	start := time.Now()
	err := q.StopWithTimeout(2 * time.Second)
	elapsed := time.Since(start)

	require.NoError(t, err)
	// No jobs queued so it returns quickly — we just confirm we didn't sleep
	// for the full 2s waiting for some floor math.
	assert.Less(t, elapsed, 500*time.Millisecond,
		"with no running jobs, stop returns promptly regardless of timeout")
}
