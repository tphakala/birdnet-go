package ffmpeg

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResetForSilenceTimeout_ClearsCircuitBreaker is a regression test for a
// silence-watchdog restart leaving the circuit breaker open. recordFailure sets
// circuitOpenTime once the consecutive-failure count crosses a threshold, and
// isCircuitOpen keys off circuitOpenTime (not the count), so a silence restart
// that tipped the count over the threshold must not leave the breaker open: a
// silent-but-connected source is recoverable and should keep retrying. The prior
// failure count is nonzero, per the review scenario.
func TestResetForSilenceTimeout_ClearsCircuitBreaker(t *testing.T) {
	t.Parallel()

	s := &Stream{}
	// Simulate a prior run of failures that opened the circuit breaker.
	s.consecutiveFailures = circuitBreakerThreshold
	s.circuitOpenTime = time.Now()
	s.restartCount = 5
	require.True(t, s.isCircuitOpen(), "precondition: the breaker is open")

	s.resetForSilenceTimeout()

	assert.False(t, s.isCircuitOpen(), "a silence timeout must not leave the circuit open")
	assert.Equal(t, circuitBreakerThreshold-1, s.consecutiveFailures, "only this event's failure increment is undone")
	assert.Zero(t, s.restartCount, "restart count is reset")
}

// TestResetForSilenceTimeout_FloorsAtZero ensures the consecutive-failure
// decrement does not underflow when there was no prior failure recorded.
func TestResetForSilenceTimeout_FloorsAtZero(t *testing.T) {
	t.Parallel()

	s := &Stream{}
	s.resetForSilenceTimeout()

	assert.Zero(t, s.consecutiveFailures)
	assert.False(t, s.isCircuitOpen())
}
