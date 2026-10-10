package diskmanager

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

// TestCleanupSummaryNotKeepingUp verifies the primary WARN predicate: a usage
// run that spent its whole time budget while disk usage rose.
func TestCleanupSummaryNotKeepingUp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s    cleanupSummary
		want bool
	}{
		// Usage is a whole percent, so a working drain on a large disk can leave it
		// unchanged over one run; that must not WARN.
		{"usage, budget stop, usage unchanged", cleanupSummary{stats: cleanupStats{StopReason: stopTimeBudget}, usageBefore: 90, usageAfter: 90, usageThreshold: 80}, false},
		{"usage, budget stop, usage rising", cleanupSummary{stats: cleanupStats{StopReason: stopTimeBudget}, usageBefore: 90, usageAfter: 92, usageThreshold: 80}, true},
		{"usage, budget stop, usage falling", cleanupSummary{stats: cleanupStats{StopReason: stopTimeBudget}, usageBefore: 90, usageAfter: 85, usageThreshold: 80}, false},
		{"age policy", cleanupSummary{stats: cleanupStats{StopReason: stopTimeBudget}, usageBefore: unknownUsagePercent, usageAfter: 90, usageThreshold: unknownUsagePercent}, false},
		{"usage not measured after", cleanupSummary{stats: cleanupStats{StopReason: stopTimeBudget}, usageBefore: 90, usageAfter: unknownUsagePercent, usageThreshold: 80}, false},
		{"usage not measured before", cleanupSummary{stats: cleanupStats{StopReason: stopTimeBudget}, usageBefore: unknownUsagePercent, usageAfter: 90, usageThreshold: 80}, false},
		{"settings changed stop", cleanupSummary{stats: cleanupStats{StopReason: stopSettingsChanged}, usageBefore: 90, usageAfter: 90, usageThreshold: 80}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.s.notKeepingUp())
		})
	}
}

// TestCleanupSummaryUsageStillOverTarget verifies the secondary WARN predicate:
// a usage-based run that left the disk at or above its configured target. The predicate must not fire for the age
// policy (no target) or when the final usage could not be measured.
func TestCleanupSummaryUsageStillOverTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		usageAfter     int
		usageThreshold int
		want           bool
	}{
		{"over target", 95, 80, true},
		{"exactly at target", 80, 80, true},
		{"under target", 70, 80, false},
		{"no target (age policy)", 95, unknownUsagePercent, false},
		{"zero target treated as not applicable", 95, 0, false},
		{"usage not measured", unknownUsagePercent, 80, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := cleanupSummary{usageAfter: tt.usageAfter, usageThreshold: tt.usageThreshold}
			assert.Equal(t, tt.want, s.usageStillOverTarget())
		})
	}
}

// TestLogCleanupSummaryWarnEmission verifies that logCleanupSummary actually
// emits a WARN for each escalation condition, stays quiet on a healthy run, and
// never logs the unknownUsagePercent (-1) sentinel for a policy without a usage
// target (the age policy). It complements the predicate unit tests by covering
// the emission/routing the predicates feed, not just the decision.
func TestLogCleanupSummaryWarnEmission(t *testing.T) {
	// Not parallel: logtest.Capture swaps the process-global logger.

	t.Run("age summary has no usage fields and does not WARN on a budget stop with progress", func(t *testing.T) {
		s := &cleanupSummary{
			policy: "age", stats: cleanupStats{Scanned: 300, Deleted: 200, BytesFreed: 15, StopReason: stopTimeBudget, MoreWork: true, Batches: 1, RecordsCleared: 200},
			duration:    time.Second,
			usageBefore: unknownUsagePercent, usageAfter: unknownUsagePercent, usageThreshold: unknownUsagePercent,
		}
		out := logtest.Capture(t, func() { logCleanupSummary(s) })
		assert.Contains(t, out, "cleanup run summary")
		assert.Contains(t, out, "stop_reason=time_budget")
		assert.Contains(t, out, "more_work=true")
		assert.Contains(t, out, "batches=1")
		assert.Contains(t, out, "records_cleared=200")
		assert.NotContains(t, out, "level=WARN", "a budget stop with progress is normal during a drain")
		assert.NotContains(t, out, "cap_hit")
		// The age policy has no usage measurement or target, so none of the
		// usage_*_pct fields (which would carry the -1 sentinel) must be logged.
		// Assert the keys are absent rather than the bare "-1" substring, which
		// also occurs in the slog timestamp on many dates/timezones.
		assert.NotContains(t, out, "usage_before_pct", "age policy has no usage measurement to log")
		assert.NotContains(t, out, "usage_after_pct", "age policy has no usage measurement to log")
		assert.NotContains(t, out, "usage_threshold_pct", "age policy has no usage target to log")
	})

	t.Run("usage budget stop with a rising disk emits the not-keeping-up WARN", func(t *testing.T) {
		s := &cleanupSummary{
			policy: "usage", stats: cleanupStats{Scanned: 300, Deleted: 200, StopReason: stopTimeBudget, MoreWork: true},
			duration:    time.Second,
			usageBefore: 95, usageAfter: 96, usageThreshold: 80,
		}
		out := logtest.Capture(t, func() { logCleanupSummary(s) })
		assert.Contains(t, out, "level=WARN")
		assert.Contains(t, out, "disk usage rose during this run")
	})

	t.Run("usage over-target with a follow-up run pending does not WARN", func(t *testing.T) {
		s := &cleanupSummary{
			policy: "usage", stats: cleanupStats{Scanned: 300, Deleted: 200, StopReason: stopTimeBudget, MoreWork: true},
			duration:    time.Second,
			usageBefore: 95, usageAfter: 90, usageThreshold: 80,
		}
		out := logtest.Capture(t, func() { logCleanupSummary(s) })
		assert.Contains(t, out, "cleanup run summary")
		assert.NotContains(t, out, "level=WARN", "the next run is already scheduled, so over-target is expected")
	})

	t.Run("usage over-target after a quit does not WARN", func(t *testing.T) {
		s := &cleanupSummary{
			policy: "usage", stats: cleanupStats{Scanned: 10, Deleted: 2, StopReason: stopQuit},
			duration:    time.Second,
			usageBefore: 95, usageAfter: 90, usageThreshold: 80,
		}
		out := logtest.Capture(t, func() { logCleanupSummary(s) })
		assert.Contains(t, out, "stop_reason=quit")
		assert.NotContains(t, out, "level=WARN", "an interrupted run says nothing about whether the target is reachable")
	})

	t.Run("usage over-target emits WARN with usage fields", func(t *testing.T) {
		s := &cleanupSummary{
			policy: "usage", stats: cleanupStats{Scanned: 10, Deleted: 2, BytesFreed: 2048, StopReason: stopExhausted},
			duration:    time.Second,
			usageBefore: 95, usageAfter: 94, usageThreshold: 80,
		}
		out := logtest.Capture(t, func() { logCleanupSummary(s) })
		assert.Contains(t, out, "level=WARN", "finishing above target must escalate to WARN")
		assert.Contains(t, out, "at or above the configured target")
		assert.Contains(t, out, "usage_after_pct=94")
		assert.Contains(t, out, "usage_threshold_pct=80")
	})

	t.Run("healthy usage run does not WARN", func(t *testing.T) {
		s := &cleanupSummary{
			policy: "usage", stats: cleanupStats{Scanned: 10, Deleted: 10, BytesFreed: 4096, StopReason: stopBelowThreshold},
			duration:    time.Second,
			usageBefore: 95, usageAfter: 70, usageThreshold: 80,
		}
		out := logtest.Capture(t, func() { logCleanupSummary(s) })
		assert.Contains(t, out, "cleanup run summary", "the INFO summary is always emitted")
		assert.NotContains(t, out, "level=WARN", "a run that reached target must not escalate")
	})
}
