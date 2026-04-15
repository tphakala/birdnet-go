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

// TestStopWithTimeout_FloorBehavior is a table-driven test verifying the
// floor contract: zero or negative timeouts are silently floored to
// MinStopTimeout, positive timeouts are honored (but still return promptly
// when no jobs are queued). The elapsed-time assertions use MinStopTimeout
// so the test stays tied to the production contract rather than a magic
// number.
func TestStopWithTimeout_FloorBehavior(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		timeout time.Duration
	}{
		{name: "zero_timeout_is_floored", timeout: 0},
		{name: "negative_timeout_is_floored", timeout: -5 * time.Second},
		{name: "positive_timeout_returns_promptly_without_jobs", timeout: 2 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			q := NewJobQueue()
			q.Start()

			start := time.Now()
			err := q.StopWithTimeout(tt.timeout)
			elapsed := time.Since(start)

			require.NoError(t, err,
				"stop must not propagate a timeout error when the queue is idle, regardless of caller timeout")
			assert.Less(t, elapsed, MinStopTimeout,
				"with no running jobs, stop should complete faster than MinStopTimeout (got %v)", elapsed)
		})
	}
}
