package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/diskmanager"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

// TestLogReconcileResult pins the level, message and counters of the pass-end
// log line for each outcome of a reconcile pass.
func TestLogReconcileResult(t *testing.T) {
	// Not parallel: logtest.Capture swaps the process-global logger.
	tests := []struct {
		name   string
		result diskmanager.ReconcileResult
		want   []string
		absent []string
	}{
		{
			name:   "completed clean",
			result: diskmanager.ReconcileResult{Scanned: 10, Cleared: 4, Retained: 1, HeldNoEvidence: 2, HeldMissingDir: 3},
			want:   []string{"level=INFO", "clip reconcile pass completed", "cleared=4", "retained=1", "held_no_evidence=2", "held_missing_dir=3", "write_errors=0"},
			absent: []string{"with write errors"},
		},
		{
			name:   "completed with write errors",
			result: diskmanager.ReconcileResult{Scanned: 10, WriteErrors: 2},
			want:   []string{"level=WARN", "clip reconcile pass completed with write errors", "write_errors=2"},
			absent: []string{"level=INFO"},
		},
		{
			name:   "aborted without write errors",
			result: diskmanager.ReconcileResult{Scanned: 7, Cleared: 5, HeldNoEvidence: 9, Aborted: true, AbortReason: "export directory changed"},
			want:   []string{"level=DEBUG", "clip reconcile pass aborted", "reason=\"export directory changed\"", "cleared=5", "held_no_evidence=9"},
			absent: []string{"level=WARN"},
		},
		{
			name:   "aborted with write errors",
			result: diskmanager.ReconcileResult{Scanned: 7, WriteErrors: 1, Aborted: true, AbortReason: "read error"},
			want:   []string{"level=WARN", "clip reconcile pass aborted with write errors", "reason=\"read error\"", "write_errors=1"},
			absent: []string{"level=DEBUG"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := logtest.Capture(t, func() {
				logReconcileResult(logger.Global().Module("diskmanager"), &tt.result)
			})
			for _, w := range tt.want {
				assert.Contains(t, out, w)
			}
			for _, a := range tt.absent {
				assert.NotContains(t, out, a)
			}
		})
	}
}
