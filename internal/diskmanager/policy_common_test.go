package diskmanager

import (
	"bytes"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// captureCleanupLogs swaps in a process-global logger that writes to a buffer,
// runs fn, restores the previous global, and returns everything logged. Used to
// assert that logCleanupSummary emits (or withholds) the WARN escalation.
func captureCleanupLogs(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	cfg := &logger.LoggingConfig{
		DefaultLevel: "debug",
		Console:      &logger.ConsoleOutput{Enabled: false},
		FileOutput:   &logger.FileOutput{Enabled: false},
	}
	cl, err := logger.NewCentralLogger(cfg, handler)
	require.NoError(t, err, "failed to create test logger")

	oldGlobal := logger.Global()
	logger.SetGlobal(cl)
	t.Cleanup(func() { logger.SetGlobal(oldGlobal) })

	fn()
	return buf.String()
}

// TestHumanizeBytes verifies the human-readable byte rendering used in the
// cleanup summary line. Boundaries (exactly 1 KiB, sub-KiB) are covered so a
// support dump reads freed space at a glance.
func TestHumanizeBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		bytes int64
		want  string
	}{
		{"zero", 0, "0 B"},
		{"sub-kib", 512, "512 B"},
		{"one-kib-boundary", 1024, "1.0 KB"},
		{"kilobytes", 1536, "1.5 KB"},
		{"megabytes", 150 * 1024 * 1024, "150.0 MB"},
		{"gigabytes", 18 * 1024 * 1024 * 1024, "18.0 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, humanizeBytes(tt.bytes))
		})
	}
}

// TestCleanupSummaryHitDeletionCap verifies the primary WARN predicate: a run
// is escalated when it was rate-limited by the per-run deletion cap while
// candidate files still remained (GitHub #4059).
func TestCleanupSummaryHitDeletionCap(t *testing.T) {
	t.Parallel()

	capped := cleanupSummary{stats: cleanupStats{CapHit: true}}
	uncapped := cleanupSummary{stats: cleanupStats{CapHit: false}}
	assert.True(t, capped.hitDeletionCap(), "a capped run must escalate")
	assert.False(t, uncapped.hitDeletionCap(), "an uncapped run must not escalate on the cap predicate")
}

// TestCleanupSummaryUsageStillOverTarget verifies the secondary WARN predicate:
// a usage-based run that finished without hitting the cap but left the disk at
// or above its configured target. The predicate must not fire for the age
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
	// Not parallel: captureCleanupLogs swaps the process-global logger.

	t.Run("age cap-hit emits WARN without usage sentinels", func(t *testing.T) {
		s := &cleanupSummary{
			policy: "age", stats: cleanupStats{Scanned: 3, Deleted: 3, BytesFreed: 15, CapHit: true},
			duration: time.Second, maxDeletions: 1000,
			usageBefore: unknownUsagePercent, usageAfter: unknownUsagePercent, usageThreshold: unknownUsagePercent,
		}
		out := captureCleanupLogs(t, func() { logCleanupSummary(s) })
		assert.Contains(t, out, "level=WARN", "a capped run must escalate to WARN")
		assert.Contains(t, out, "per-run deletion limit")
		assert.NotContains(t, out, "-1", "the -1 usage sentinel must not appear for the age policy")
		assert.NotContains(t, out, "usage_threshold_pct", "age policy has no usage target to log")
	})

	t.Run("usage over-target emits WARN with usage fields", func(t *testing.T) {
		s := &cleanupSummary{
			policy: "usage", stats: cleanupStats{Scanned: 10, Deleted: 2, BytesFreed: 2048},
			duration: time.Second, maxDeletions: 1000,
			usageBefore: 95, usageAfter: 94, usageThreshold: 80,
		}
		out := captureCleanupLogs(t, func() { logCleanupSummary(s) })
		assert.Contains(t, out, "level=WARN", "finishing above target must escalate to WARN")
		assert.Contains(t, out, "at or above the configured target")
		assert.Contains(t, out, "usage_after_pct=94")
		assert.Contains(t, out, "usage_threshold_pct=80")
	})

	t.Run("healthy usage run does not WARN", func(t *testing.T) {
		s := &cleanupSummary{
			policy: "usage", stats: cleanupStats{Scanned: 10, Deleted: 10, BytesFreed: 4096},
			duration: time.Second, maxDeletions: 1000,
			usageBefore: 95, usageAfter: 70, usageThreshold: 80,
		}
		out := captureCleanupLogs(t, func() { logCleanupSummary(s) })
		assert.Contains(t, out, "cleanup run summary", "the INFO summary is always emitted")
		assert.NotContains(t, out, "level=WARN", "a run that reached target must not escalate")
	})
}
