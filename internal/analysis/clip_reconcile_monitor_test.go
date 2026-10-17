package analysis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// waitRecorder is a wait function for runClipReconcileLoop that records each
// requested delay and reports shutdown once allow waits have been granted.
type waitRecorder struct {
	delays []time.Duration
	allow  int
}

func (w *waitRecorder) wait(_ <-chan struct{}, d time.Duration) bool {
	w.delays = append(w.delays, d)
	return len(w.delays) <= w.allow
}

// scriptedPasses returns a pass function for runClipReconcileLoop that reports
// the given idle flags in order, never asks to stop, and counts its calls.
func scriptedPasses(idle []bool, calls *int) func() (bool, bool) {
	return func() (bool, bool) {
		i := *calls
		*calls++
		return false, idle[i]
	}
}

func TestRunClipReconcileLoop(t *testing.T) {
	t.Parallel()

	t.Run("shutdown before the startup delay ends runs no pass", func(t *testing.T) {
		t.Parallel()
		w := &waitRecorder{allow: 0}
		passes := 0
		runClipReconcileLoop(nil, w.wait, func() (bool, bool) { passes++; return false, false })
		assert.Zero(t, passes)
		assert.Equal(t, []time.Duration{clipReconcileStartupDelay}, w.delays)
	})

	t.Run("a pass that asks to stop ends the loop without another wait", func(t *testing.T) {
		t.Parallel()
		w := &waitRecorder{allow: 100}
		passes := 0
		runClipReconcileLoop(nil, w.wait, func() (bool, bool) { passes++; return passes == 3, false })
		assert.Equal(t, 3, passes)
		assert.Equal(t, []time.Duration{clipReconcileStartupDelay, clipReconcilePassInterval, clipReconcilePassInterval}, w.delays,
			"the startup delay once, then one interval after each pass that did not stop")
	})

	t.Run("shutdown during the interval wait ends the loop after one pass", func(t *testing.T) {
		t.Parallel()
		w := &waitRecorder{allow: 1}
		passes := 0
		runClipReconcileLoop(nil, w.wait, func() (bool, bool) { passes++; return false, true })
		assert.Equal(t, 1, passes)
		assert.Equal(t, []time.Duration{clipReconcileStartupDelay, clipReconcilePassInterval}, w.delays)
	})

	t.Run("idle passes double the interval up to the cap and stay there", func(t *testing.T) {
		t.Parallel()
		w := &waitRecorder{allow: 100}
		calls := 0
		idle := []bool{true, true, true, true, true, true, true, false}
		next := scriptedPasses(idle, &calls)
		runClipReconcileLoop(nil, w.wait, func() (bool, bool) {
			_, i := next()
			return calls == len(idle), i
		})
		h := time.Hour
		assert.Equal(t, []time.Duration{clipReconcileStartupDelay, h, 2 * h, 4 * h, 8 * h, 16 * h, 24 * h, 24 * h}, w.delays)
		assert.Equal(t, clipReconcileMaxPassInterval, 24*h)
	})

	t.Run("a pass that changed something resets the interval", func(t *testing.T) {
		t.Parallel()
		w := &waitRecorder{allow: 100}
		calls := 0
		idle := []bool{false, true, true, false, true, true, true}
		next := scriptedPasses(idle, &calls)
		runClipReconcileLoop(nil, w.wait, func() (bool, bool) {
			_, i := next()
			return calls == len(idle), i
		})
		h := time.Hour
		assert.Equal(t, []time.Duration{clipReconcileStartupDelay, h, h, 2 * h, h, h, 2 * h}, w.delays,
			"a non-idle pass (cleared, retained, aborted or write errors), first or later, returns the wait to one interval and doubling restarts from there")
	})

	t.Run("shutdown in the middle of the back-off sequence ends the loop", func(t *testing.T) {
		t.Parallel()
		w := &waitRecorder{allow: 3}
		passes := 0
		runClipReconcileLoop(nil, w.wait, func() (bool, bool) { passes++; return false, true })
		assert.Equal(t, 3, passes)
		h := time.Hour
		assert.Equal(t, []time.Duration{clipReconcileStartupDelay, h, 2 * h, 4 * h}, w.delays)
	})
}

// TestRunClipReconcileLoopIntervalLog pins that the loop logs the chosen wait only
// when it differs from the one it last logged.
func TestRunClipReconcileLoopIntervalLog(t *testing.T) {
	// Not parallel: logtest.Capture swaps the process-global logger.
	const changed = "clip reconcile pass interval changed"
	run := func(t *testing.T, idle []bool) string {
		t.Helper()
		w := &waitRecorder{allow: 100}
		calls := 0
		next := scriptedPasses(idle, &calls)
		return logtest.Capture(t, func() {
			runClipReconcileLoop(nil, w.wait, func() (bool, bool) {
				_, i := next()
				return calls == len(idle), i
			})
		})
	}

	t.Run("logs each doubling and each reset, not an unchanged wait", func(t *testing.T) {
		// Waits after passes 1 to 5: 1h, 2h, 1h, 1h, 2h.
		out := run(t, []bool{true, true, false, true, true, true})
		assert.Equal(t, 3, strings.Count(out, changed), out)
	})

	t.Run("stays quiet while the wait sits at the cap", func(t *testing.T) {
		// Waits after passes 1 to 8: 1h, 2h, 4h, 8h, 16h, 24h, 24h, 24h.
		out := run(t, []bool{true, true, true, true, true, true, true, true, true})
		assert.Equal(t, 5, strings.Count(out, changed), out)
	})
}

func TestReconcileIdle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		result diskmanager.ReconcileResult
		want   bool
	}{
		{"nothing changed", diskmanager.ReconcileResult{Scanned: 5}, true},
		{"held rows alone are no change", diskmanager.ReconcileResult{HeldNoEvidence: 3, HeldMissingDir: 2}, true},
		{"cleared rows", diskmanager.ReconcileResult{Cleared: 1}, false},
		{"retained rows", diskmanager.ReconcileResult{Retained: 1}, false},
		{"aborted", diskmanager.ReconcileResult{Aborted: true}, false},
		{"write errors", diskmanager.ReconcileResult{WriteErrors: 1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, reconcileIdle(&tt.result))
		})
	}
}

// fakeReconcileStore serves a fixed list of clip references to the reconcile pass
// and records what it was asked to clear.
type fakeReconcileStore struct {
	refs    []diskmanager.ClipReference
	reads   int
	cleared []string
}

func (f *fakeReconcileStore) GetNoteClipReferences(afterID uint, limit int) ([]diskmanager.ClipReference, error) {
	f.reads++
	var out []diskmanager.ClipReference
	for _, r := range f.refs {
		if r.ID > afterID && len(out) < limit {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeReconcileStore) ClearNoteClipPathsByNames(names []string) (int64, error) {
	f.cleared = append(f.cleared, names...)
	return int64(len(names)), nil
}

func (f *fakeReconcileStore) RetainNoteSpectrogramsByClipNames(names []string) (int64, error) {
	return int64(len(names)), nil
}

// reconcileFixture writes one present clip and returns refs for it and for a
// missing clip that follows it, both old enough to be judged.
func reconcileFixture(t *testing.T) (baseDir string, store *fakeReconcileStore) {
	t.Helper()
	baseDir = t.TempDir()
	dir := filepath.Join(baseDir, "2026", "01")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "present.wav"), []byte("x"), 0o600))
	old := time.Now().Add(-time.Hour)
	return baseDir, &fakeReconcileStore{refs: []diskmanager.ClipReference{
		{ID: 1, ClipName: "2026/01/present.wav", CompletionTime: old},
		{ID: 2, ClipName: "2026/01/gone.wav", CompletionTime: old},
	}}
}

func TestRunClipReconcilePass(t *testing.T) {
	// Not parallel: logtest.Capture swaps the process-global logger.
	t.Run("unconfigured export path skips the pass without reading the store", func(t *testing.T) {
		store := &fakeReconcileStore{}
		stop, idle := false, true
		out := logtest.Capture(t, func() {
			stop, idle = runClipReconcilePass(GetLogger(), make(chan struct{}), store, func() string { return "" })
		})
		assert.False(t, stop)
		assert.False(t, idle, "a skipped pass must not back the interval off")
		assert.Zero(t, store.reads)
		assert.Contains(t, out, "export path not configured")
	})

	t.Run("a pass clears the missing clip and logs the completion", func(t *testing.T) {
		baseDir, store := reconcileFixture(t)
		stop, idle := true, true
		out := logtest.Capture(t, func() {
			stop, idle = runClipReconcilePass(GetLogger(), make(chan struct{}), store, func() string { return baseDir })
		})
		assert.False(t, stop)
		assert.False(t, idle, "a pass that cleared a row resets the interval")
		assert.Equal(t, []string{"2026/01/gone.wav"}, store.cleared)
		assert.Contains(t, out, "clip reconcile pass completed")
		assert.Contains(t, out, "cleared=1")
	})

	t.Run("shutdown during the pass asks the loop to stop and logs nothing", func(t *testing.T) {
		baseDir, store := reconcileFixture(t)
		quit := make(chan struct{})
		close(quit)
		stop := false
		out := logtest.Capture(t, func() {
			stop, _ = runClipReconcilePass(GetLogger(), quit, store, func() string { return baseDir })
		})
		assert.True(t, stop)
		assert.Empty(t, store.cleared)
		assert.NotContains(t, out, "clip reconcile pass")
	})

	t.Run("an export path change after the pass started aborts it before any read", func(t *testing.T) {
		baseDir, store := reconcileFixture(t)
		calls := 0
		exportDir := func() string {
			calls++
			if calls == 1 {
				return baseDir
			}
			return baseDir + "-moved"
		}
		stop, idle := true, true
		out := logtest.Capture(t, func() {
			stop, idle = runClipReconcilePass(GetLogger(), make(chan struct{}), store, exportDir)
		})
		assert.False(t, stop)
		assert.False(t, idle, "an aborted pass resets the interval")
		assert.Zero(t, store.reads, "pathCurrent must make the pass abort before it reads the store")
		assert.Empty(t, store.cleared)
		assert.Contains(t, out, "clip reconcile pass aborted")
	})

	t.Run("a pass over a clean library reports idle", func(t *testing.T) {
		baseDir, store := reconcileFixture(t)
		store.refs = store.refs[:1]
		stop, idle := true, false
		logtest.Capture(t, func() {
			stop, idle = runClipReconcilePass(GetLogger(), make(chan struct{}), store, func() string { return baseDir })
		})
		assert.False(t, stop)
		assert.True(t, idle)
		assert.Empty(t, store.cleared)
	})
}
