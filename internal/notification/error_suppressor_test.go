package notification

import (
	"testing"

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
