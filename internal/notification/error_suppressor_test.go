package notification

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorSuppressor_FirstFailureAlwaysReported(t *testing.T) {
	t.Parallel()
	suppressor := NewErrorSuppressor(nil, &NoopTelemetryReporter{})

	result := suppressor.ShouldReport("webhook-1")
	assert.True(t, result, "first failure should always be reported")
}

func TestErrorSuppressor_SubsequentFailuresSuppressed(t *testing.T) {
	t.Parallel()
	suppressor := NewErrorSuppressor(nil, &NoopTelemetryReporter{})

	// First failure - reported
	assert.True(t, suppressor.ShouldReport("webhook-1"))

	// Subsequent failures - suppressed
	assert.False(t, suppressor.ShouldReport("webhook-1"))
	assert.False(t, suppressor.ShouldReport("webhook-1"))
	assert.False(t, suppressor.ShouldReport("webhook-1"))
}

func TestErrorSuppressor_DifferentProvidersIndependent(t *testing.T) {
	t.Parallel()
	suppressor := NewErrorSuppressor(nil, &NoopTelemetryReporter{})

	// First failure for provider A - reported
	assert.True(t, suppressor.ShouldReport("webhook-A"))

	// First failure for provider B - also reported (independent)
	assert.True(t, suppressor.ShouldReport("webhook-B"))

	// Second failure for provider A - suppressed
	assert.False(t, suppressor.ShouldReport("webhook-A"))

	// Second failure for provider B - suppressed
	assert.False(t, suppressor.ShouldReport("webhook-B"))
}

func TestErrorSuppressor_RecoveryResetsState(t *testing.T) {
	t.Parallel()
	suppressor := NewErrorSuppressor(nil, &NoopTelemetryReporter{})

	// Build up failures
	assert.True(t, suppressor.ShouldReport("webhook-1"))
	assert.False(t, suppressor.ShouldReport("webhook-1"))
	assert.False(t, suppressor.ShouldReport("webhook-1"))

	// Record success
	suppressor.RecordSuccess("webhook-1")

	// After recovery, first failure should be reported again
	assert.True(t, suppressor.ShouldReport("webhook-1"))
}

func TestErrorSuppressor_GetSuppressedCount(t *testing.T) {
	t.Parallel()
	suppressor := NewErrorSuppressor(nil, &NoopTelemetryReporter{})

	// No failures yet
	assert.Equal(t, 0, suppressor.GetSuppressedCount("webhook-1"))

	// Record some failures
	suppressor.ShouldReport("webhook-1")
	assert.Equal(t, 1, suppressor.GetSuppressedCount("webhook-1"))

	suppressor.ShouldReport("webhook-1")
	assert.Equal(t, 2, suppressor.GetSuppressedCount("webhook-1"))

	suppressor.ShouldReport("webhook-1")
	assert.Equal(t, 3, suppressor.GetSuppressedCount("webhook-1"))

	// Recovery resets
	suppressor.RecordSuccess("webhook-1")
	assert.Equal(t, 0, suppressor.GetSuppressedCount("webhook-1"))
}

func TestErrorSuppressor_RecordFailureSetsErrorMessage(t *testing.T) {
	t.Parallel()
	suppressor := NewErrorSuppressor(nil, &NoopTelemetryReporter{})

	suppressor.ShouldReport("webhook-1")
	suppressor.RecordFailure("webhook-1", "connection timeout")

	// Verify sample error is recorded
	suppressor.mu.Lock()
	state := suppressor.states["webhook-1"]
	require.NotNil(t, state)
	assert.Equal(t, "connection timeout", state.sampleError)
	suppressor.mu.Unlock()
}

func TestErrorSuppressor_RecoveryWithNoFailuresIsNoop(t *testing.T) {
	t.Parallel()
	suppressor := NewErrorSuppressor(nil, &NoopTelemetryReporter{})

	// RecordSuccess on a provider with no failures should not panic
	suppressor.RecordSuccess("webhook-1")

	// State should still be clean
	assert.Equal(t, 0, suppressor.GetSuppressedCount("webhook-1"))
}

func TestErrorSuppressor_RecoveryWithTelemetry(t *testing.T) {
	t.Parallel()

	reporter := &captureTelemetryReporter{}
	suppressor := NewErrorSuppressor(nil, reporter)

	// Build up failures
	suppressor.ShouldReport("webhook-1")
	suppressor.RecordFailure("webhook-1", "timeout error")
	suppressor.ShouldReport("webhook-1")
	suppressor.ShouldReport("webhook-1")

	// Recover
	suppressor.RecordSuccess("webhook-1")

	// Should have captured a recovery event
	require.Len(t, reporter.events, 1)
	assert.Contains(t, reporter.events[0].message, "recovered")
	assert.Contains(t, reporter.events[0].tags["event_type"], "provider_recovery")
}

func TestErrorSuppressor_SingleFailureRecoveryNoTelemetry(t *testing.T) {
	t.Parallel()

	reporter := &captureTelemetryReporter{}
	suppressor := NewErrorSuppressor(nil, reporter)

	// Single failure then recovery - no recovery telemetry (not a "streak")
	suppressor.ShouldReport("webhook-1")
	suppressor.RecordSuccess("webhook-1")

	// Should NOT have captured a recovery event (only 1 failure, no suppression)
	assert.Empty(t, reporter.events)
}

// TestNextReminderInterval_ExponentialBackoff verifies the schedule produced
// by nextReminderInterval against the documented 5m/10m/20m/40m/... ramp with
// the configured 24h cap. Using a direct unit test here (instead of exercising
// ShouldReport with synctest) keeps the assertions on the pure math, so a
// regression in the formula will surface immediately.
func TestNextReminderInterval_ExponentialBackoff(t *testing.T) {
	t.Parallel()

	// Expected schedule derived from the constants in error_suppressor.go:
	// base = 5m, multiplier = 2, cap = 24h.
	tests := []struct {
		name        string
		reportCount int
		want        time.Duration
	}{
		{"below_minimum_returns_base", 0, suppressionReminderInterval},
		{"first_reminder_is_base", 1, 5 * time.Minute},
		{"second_reminder_doubles", 2, 10 * time.Minute},
		{"third_reminder_doubles_again", 3, 20 * time.Minute},
		{"fourth_reminder_doubles_again", 4, 40 * time.Minute},
		{"very_high_count_caps_at_max", 100, maxSuppressionReminderInterval},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := nextReminderInterval(tc.reportCount)
			assert.Equal(t, tc.want, got,
				"reportCount=%d: want %v, got %v", tc.reportCount, tc.want, got)
		})
	}
}

// TestErrorSuppressor_BackoffGrowsPerReminder checks that ShouldReport defers
// a second reminder until the exponential schedule has elapsed. We manipulate
// lastReportTime directly because running for real would take 5+ minutes.
func TestErrorSuppressor_BackoffGrowsPerReminder(t *testing.T) {
	t.Parallel()

	suppressor := NewErrorSuppressor(nil, &NoopTelemetryReporter{})

	// First failure is always reported and sets reportCount=1.
	require.True(t, suppressor.ShouldReport("webhook-1"))

	// Second failure within the first interval stays suppressed.
	require.False(t, suppressor.ShouldReport("webhook-1"),
		"consecutive failures inside the base interval must be suppressed")

	// Rewind lastReportTime so the base interval appears to have elapsed;
	// reportCount is still 1 so the next allowed report is exactly at
	// nextReminderInterval(1) = base. Scope the lock in a closure so defer
	// releases it before the next ShouldReport call (which also takes the lock).
	func() {
		suppressor.mu.Lock()
		defer suppressor.mu.Unlock()
		state := suppressor.states["webhook-1"]
		require.NotNil(t, state, "state should exist after ShouldReport")
		require.Equal(t, 1, state.reportCount)
		state.lastReportTime = time.Now().Add(-suppressionReminderInterval - time.Second)
	}()

	// Now the second reminder should fire, advancing reportCount to 2.
	require.True(t, suppressor.ShouldReport("webhook-1"),
		"reminder should fire once the base interval elapses")

	// A third failure *just* after the base interval must still be suppressed
	// because the schedule has doubled to 2*base for reportCount=2.
	func() {
		suppressor.mu.Lock()
		defer suppressor.mu.Unlock()
		state := suppressor.states["webhook-1"]
		require.Equal(t, 2, state.reportCount,
			"reportCount must advance after a reminder is emitted")
		state.lastReportTime = time.Now().Add(-(suppressionReminderInterval + time.Second))
	}()

	assert.False(t, suppressor.ShouldReport("webhook-1"),
		"reminder must be deferred because the interval has doubled")
}

// captureTelemetryReporter captures telemetry events for testing.
type captureTelemetryReporter struct {
	events []capturedEvent
}

type capturedEvent struct {
	message  string
	level    string
	tags     map[string]string
	contexts map[string]any
}

func (r *captureTelemetryReporter) CaptureError(err error, component string) {}

func (r *captureTelemetryReporter) CaptureEvent(message, level string, tags map[string]string, contexts map[string]any) {
	r.events = append(r.events, capturedEvent{
		message:  message,
		level:    level,
		tags:     tags,
		contexts: contexts,
	})
}

func (r *captureTelemetryReporter) IsEnabled() bool { return true }
