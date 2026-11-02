// prerequisites_run_test.go: tests for the background prerequisites runner.
package importsapi

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test step IDs used by the runner tests.
const (
	testStepA = "step_a"
	testStepB = "step_b"
	testStepC = "step_c"
)

// passCheck returns a passing critical check with the given ID.
func passCheck(id string) PrerequisiteCheck {
	return PrerequisiteCheck{ID: id, Name: "Name " + id, Status: CheckStatusPassed, Severity: CheckSeverityCritical}
}

// failCheck returns a failed critical check with the given ID.
func failCheck(id string) PrerequisiteCheck {
	return PrerequisiteCheck{ID: id, Name: "Name " + id, Status: CheckStatusFailed, Severity: CheckSeverityCritical}
}

// fakeStep builds a step that sleeps for d, counts its executions and returns
// the check built by mk.
func fakeStep(id string, d time.Duration, runs *atomic.Int32, mk func(id string) PrerequisiteCheck) prereqStep {
	return prereqStep{
		id:   id,
		name: "Name " + id,
		run: func(context.Context) []PrerequisiteCheck {
			runs.Add(1)
			if d > 0 {
				time.Sleep(d)
			}
			return []PrerequisiteCheck{mk(id)}
		},
	}
}

// summarizeForTest is a minimal finish function for the runner tests.
func summarizeForTest(checks []PrerequisiteCheck) PrerequisitesResponse {
	resp := PrerequisitesResponse{Checks: checks, AllPassed: true}
	for i := range checks {
		if checks[i].Severity == CheckSeverityCritical && (checks[i].Status == CheckStatusFailed || checks[i].Status == CheckStatusError) {
			resp.CriticalFailures++
			resp.AllPassed = false
		}
	}
	resp.CanStartMigration = resp.AllPassed
	return resp
}

// stepsOf wraps a fixed slice as the runner's steps provider.
func stepsOf(steps ...prereqStep) func(*recordCountSink) []prereqStep {
	return func(*recordCountSink) []prereqStep { return steps }
}

func TestPrereqRunner_ConcurrentStartsJoinOneRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var runs atomic.Int32
		release := make(chan struct{})
		step := prereqStep{id: testStepA, name: "A", run: func(context.Context) []PrerequisiteCheck {
			runs.Add(1)
			<-release
			return []PrerequisiteCheck{passCheck(testStepA)}
		}}
		r := &prereqRunner{}
		fp := dbFingerprint{}

		first := r.start(t.Context(), false, fp, stepsOf(step), summarizeForTest)
		second := r.start(t.Context(), false, fp, stepsOf(step), summarizeForTest)
		forced := r.start(t.Context(), true, fp, stepsOf(step), summarizeForTest)
		synctest.Wait()

		assert.Equal(t, PrereqRunStatusRunning, first.Status)
		assert.Equal(t, first.RunID, second.RunID)
		assert.Equal(t, first.RunID, forced.RunID)
		assert.Equal(t, PrereqRunStatusRunning, forced.Status)
		assert.Equal(t, int32(1), runs.Load(), "joined starts must not execute the step again")

		close(release)
		synctest.Wait()
		assert.Equal(t, PrereqRunStatusCompleted, r.snapshot().Status)
	})
}

func TestPrereqRunner_ReportsProgressDuringSlowStep(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var runs atomic.Int32
		const slow = 6 * time.Minute
		r := &prereqRunner{}
		steps := stepsOf(
			fakeStep(testStepA, 0, &runs, passCheck),
			fakeStep(testStepB, slow, &runs, passCheck),
			fakeStep(testStepC, 0, &runs, passCheck),
		)
		r.start(t.Context(), false, dbFingerprint{}, steps, summarizeForTest)
		synctest.Wait()

		snap := r.snapshot()
		require.Equal(t, PrereqRunStatusRunning, snap.Status)
		require.NotNil(t, snap.Progress)
		assert.Equal(t, 1, snap.Progress.CompletedSteps)
		assert.Equal(t, 3, snap.Progress.TotalSteps)
		assert.Equal(t, testStepB, snap.Progress.CurrentCheckID)
		assert.Equal(t, "Name "+testStepB, snap.Progress.CurrentCheckName)
		require.Len(t, snap.Checks, 1)
		assert.Equal(t, testStepA, snap.Checks[0].ID)
		assert.False(t, snap.AllPassed)
		assert.False(t, snap.CanStartMigration)

		time.Sleep(time.Minute)
		synctest.Wait()
		later := r.snapshot()
		require.NotNil(t, later.Progress)
		assert.Equal(t, time.Minute.Milliseconds(), later.Progress.CurrentCheckElapsedMs-snap.Progress.CurrentCheckElapsedMs)

		time.Sleep(slow)
		synctest.Wait()
		done := r.snapshot()
		assert.Equal(t, PrereqRunStatusCompleted, done.Status)
		assert.Len(t, done.Checks, 3)
		assert.Nil(t, done.Progress)
		assert.False(t, done.CheckedAt.IsZero())
	})
}

func TestPrereqRunner_ReuseAcrossRuns(t *testing.T) {
	otherFP := dbFingerprint{MySQLEnabled: true, MySQLHost: "db.example"}
	type action struct {
		advance     time.Duration
		force       bool
		fp          dbFingerprint
		reset       bool
		failing     bool
		wantRuns    int32
		wantRunID   uint64
		description string
	}
	tests := []struct {
		name    string
		actions []action
	}{
		{"passing result is reused until forced or stale", []action{
			{description: "first run", wantRuns: 1, wantRunID: 1},
			{description: "reused at +1 min", advance: time.Minute, wantRuns: 1, wantRunID: 1},
			{description: "forced", force: true, wantRuns: 2, wantRunID: 2},
			{description: "stale at +31 min", advance: 31 * time.Minute, wantRuns: 3, wantRunID: 3},
		}},
		{"failing result is not reused", []action{
			{description: "first run fails", failing: true, wantRuns: 1, wantRunID: 1},
			{description: "re-run at +1 min", advance: time.Minute, failing: true, wantRuns: 2, wantRunID: 2},
		}},
		{"changed database settings re-run", []action{
			{description: "first run", wantRuns: 1, wantRunID: 1},
			{description: "different fingerprint", fp: otherFP, wantRuns: 2, wantRunID: 2},
		}},
		{"reset drops the result", []action{
			{description: "first run", wantRuns: 1, wantRunID: 1},
			{description: "after reset", reset: true, wantRuns: 2, wantRunID: 2},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var runs atomic.Int32
				r := &prereqRunner{}
				for _, a := range tt.actions {
					if a.advance > 0 {
						time.Sleep(a.advance)
					}
					if a.reset {
						r.reset()
					}
					mk := passCheck
					if a.failing {
						mk = failCheck
					}
					snap := r.start(t.Context(), a.force, a.fp, stepsOf(fakeStep(testStepA, 0, &runs, mk)), summarizeForTest)
					synctest.Wait()
					assert.Equal(t, a.wantRuns, runs.Load(), a.description)
					assert.Equal(t, a.wantRunID, snap.RunID, a.description)
					assert.Equal(t, a.wantRunID, r.snapshot().RunID, a.description)
				}
			})
		})
	}
}

func TestPrereqRunner_ResetDuringRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &prereqRunner{}
		release := make(chan struct{})
		var run1Err error
		var calls atomic.Int32
		step := prereqStep{id: testStepA, name: "A", run: func(ctx context.Context) []PrerequisiteCheck {
			if calls.Add(1) == 1 {
				<-release
				run1Err = ctx.Err()
				return []PrerequisiteCheck{failCheck("run1_only")}
			}
			return []PrerequisiteCheck{passCheck("run2_only")}
		}}

		first := r.start(t.Context(), false, dbFingerprint{}, stepsOf(step), summarizeForTest)
		synctest.Wait()
		r.reset()
		second := r.start(t.Context(), false, dbFingerprint{}, stepsOf(step), summarizeForTest)
		synctest.Wait()
		require.Equal(t, PrereqRunStatusCompleted, r.snapshot().Status)

		close(release)
		synctest.Wait()

		require.Error(t, run1Err, "reset must cancel the first run's context")
		snap := r.snapshot()
		assert.NotEqual(t, first.RunID, second.RunID)
		assert.Equal(t, second.RunID, snap.RunID)
		assert.Equal(t, PrereqRunStatusCompleted, snap.Status)
		require.Len(t, snap.Checks, 1)
		assert.Equal(t, "run2_only", snap.Checks[0].ID)
		assert.True(t, snap.AllPassed, "the cancelled run must not overwrite the committed result")
	})
}

func TestPrereqRunner_RunEndsFailed(t *testing.T) {
	tests := []struct {
		name      string
		step      func(ctx context.Context) []PrerequisiteCheck
		wantError string
	}{
		{"panicking step", func(context.Context) []PrerequisiteCheck { panic("boom") }, msgPrereqRunPanicked},
		{"step outliving the run timeout", func(context.Context) []PrerequisiteCheck {
			time.Sleep(prereqRunTimeout + time.Minute)
			return []PrerequisiteCheck{passCheck(testStepA)}
		}, msgPrereqRunTimedOut},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := &prereqRunner{}
				step := prereqStep{id: testStepA, name: "A", run: tt.step}
				started := r.start(t.Context(), false, dbFingerprint{}, stepsOf(step), summarizeForTest)
				time.Sleep(prereqRunTimeout + 2*time.Minute)
				synctest.Wait()

				snap := r.snapshot()
				assert.Equal(t, PrereqRunStatusFailed, snap.Status)
				assert.Contains(t, snap.Error, tt.wantError)
				assert.Equal(t, started.RunID, snap.RunID)

				again := r.start(t.Context(), false, dbFingerprint{}, stepsOf(step), summarizeForTest)
				assert.Greater(t, again.RunID, started.RunID, "a failed run is never reused")
				time.Sleep(prereqRunTimeout + 2*time.Minute)
				synctest.Wait()
			})
		})
	}
}

func TestPrereqRunner_CancelledParent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var wg sync.WaitGroup
		r := &prereqRunner{spawn: func(fn func()) { wg.Go(fn) }}
		var runs atomic.Int32

		// A cancelled parent at start spawns nothing.
		dead, cancelDead := context.WithCancel(t.Context())
		cancelDead()
		snap := r.start(dead, false, dbFingerprint{}, stepsOf(fakeStep(testStepA, 0, &runs, passCheck)), summarizeForTest)
		assert.Equal(t, PrereqRunStatusFailed, snap.Status)
		assert.Equal(t, msgPrereqRunShutdown, snap.Error)
		wg.Wait()
		assert.Equal(t, int32(0), runs.Load())

		// A parent cancelled mid-run ends the run failed and the goroutine returns.
		parent, cancel := context.WithCancel(t.Context())
		blocking := prereqStep{id: testStepA, name: "A", run: func(ctx context.Context) []PrerequisiteCheck {
			<-ctx.Done()
			return []PrerequisiteCheck{passCheck(testStepA)}
		}}
		r.start(parent, false, dbFingerprint{}, stepsOf(blocking), summarizeForTest)
		synctest.Wait()
		cancel()
		wg.Wait()

		final := r.snapshot()
		assert.Equal(t, PrereqRunStatusFailed, final.Status)
		assert.Equal(t, msgPrereqRunCancelled, final.Error)
	})
}

// TestPrereqRunner_CancelledAfterLastStep pins that a run whose context ends
// after the final step returned, while the result is being built, is failed and
// commits nothing instead of reporting completed.
func TestPrereqRunner_CancelledAfterLastStep(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var wg sync.WaitGroup
		r := &prereqRunner{spawn: func(fn func()) { wg.Go(fn) }}
		var runs atomic.Int32
		parent, cancel := context.WithCancel(t.Context())
		finish := func(checks []PrerequisiteCheck) PrerequisitesResponse {
			cancel() // the server stops after the last step, before the commit
			return summarizeForTest(checks)
		}

		r.start(parent, false, dbFingerprint{}, stepsOf(fakeStep(testStepA, 0, &runs, passCheck)), finish)
		wg.Wait()

		snap := r.snapshot()
		assert.Equal(t, PrereqRunStatusFailed, snap.Status)
		assert.Equal(t, msgPrereqRunCancelled, snap.Error)
		_, reason := r.passingResult(dbFingerprint{})
		assert.Equal(t, prereqReuseNoResult, reason, "a failed run must not leave a reusable result")
	})
}

// TestPrereqRunner_StepIgnoringContext pins that a step which never looks at its
// context cannot keep the run at running: the run fails when the context ends,
// and a panic the abandoned step raises later is dropped instead of crashing.
func TestPrereqRunner_StepIgnoringContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &prereqRunner{}
		release := make(chan struct{})
		step := prereqStep{id: testStepA, name: "A", run: func(context.Context) []PrerequisiteCheck {
			<-release
			panic("late failure of an abandoned step")
		}}
		parent, cancel := context.WithCancel(t.Context())

		r.start(parent, false, dbFingerprint{}, stepsOf(step), summarizeForTest)
		synctest.Wait()
		require.Equal(t, PrereqRunStatusRunning, r.snapshot().Status)

		cancel()
		synctest.Wait()
		snap := r.snapshot()
		assert.Equal(t, PrereqRunStatusFailed, snap.Status)
		assert.NotEmpty(t, snap.Error)

		close(release)
		synctest.Wait()
		assert.Equal(t, PrereqRunStatusFailed, r.snapshot().Status, "a late result must not change the failed run")
	})
}

// TestPrereqRunner_SnapshotChecksNeverNull pins that every snapshot serializes
// its checks as an array, which the frontend reads without a null guard.
func TestPrereqRunner_SnapshotChecksNeverNull(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &prereqRunner{}
		release := make(chan struct{})
		slow := prereqStep{id: testStepA, name: "A", run: func(context.Context) []PrerequisiteCheck {
			<-release
			return []PrerequisiteCheck{passCheck(testStepA)}
		}}
		failing := prereqStep{id: testStepA, name: "A", run: func(context.Context) []PrerequisiteCheck { panic("boom") }}
		const emptyChecks = `"checks":[]`

		idle, err := json.Marshal(r.snapshot())
		require.NoError(t, err)
		assert.Contains(t, string(idle), emptyChecks)

		r.start(t.Context(), false, dbFingerprint{}, stepsOf(slow), summarizeForTest)
		synctest.Wait()
		running, err := json.Marshal(r.snapshot())
		require.NoError(t, err)
		assert.Contains(t, string(running), emptyChecks)
		close(release)
		synctest.Wait()

		r.start(t.Context(), true, dbFingerprint{}, stepsOf(failing), summarizeForTest)
		synctest.Wait()
		failed, err := json.Marshal(r.snapshot())
		require.NoError(t, err)
		assert.Contains(t, string(failed), emptyChecks)
	})
}

// TestPrereqRunner_PassingResultCarriesRecordCount pins that the count a run
// reports through its sink is committed with the result and returned with it,
// and that passingResult names why a result cannot be reused.
func TestPrereqRunner_PassingResultCarriesRecordCount(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const wantCount = int64(1234)
		r := &prereqRunner{}
		steps := func(sink *recordCountSink) []prereqStep {
			return []prereqStep{{id: testStepA, name: "A", run: func(context.Context) []PrerequisiteCheck {
				sink.count, sink.known = wantCount, true
				return []PrerequisiteCheck{passCheck(testStepA)}
			}}}
		}
		fp := dbFingerprint{}

		_, reason := r.passingResult(fp)
		assert.Equal(t, prereqReuseNoResult, reason)

		r.start(t.Context(), false, fp, steps, summarizeForTest)
		synctest.Wait()

		got, reason := r.passingResult(fp)
		assert.Equal(t, prereqReuseOK, reason)
		assert.Equal(t, wantCount, got.RecordCount)
		assert.True(t, got.RecordCountKnown)

		_, reason = r.passingResult(dbFingerprint{MySQLEnabled: true})
		assert.Equal(t, prereqReuseSettingsChanged, reason)

		time.Sleep(prereqResultMaxAge)
		_, reason = r.passingResult(fp)
		assert.Equal(t, prereqReuseStale, reason)
	})
}

// TestPrereqRunner_ReportsOnlyTheCurrentRun pins the observer hooks: a start
// that joins or reuses reports nothing, and a run abandoned by reset reports no
// outcome, so each run that matters is reported once.
func TestPrereqRunner_ReportsOnlyTheCurrentRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var started []uint64
		var ended []uint64
		r := &prereqRunner{
			onStart: func(id uint64, _ bool) { started = append(started, id) },
			onEnd:   func(res *PrerequisitesResponse, _ time.Duration) { ended = append(ended, res.RunID) },
		}
		release := make(chan struct{})
		var calls atomic.Int32
		step := prereqStep{id: testStepA, name: "A", run: func(context.Context) []PrerequisiteCheck {
			if calls.Add(1) == 1 {
				<-release
			}
			return []PrerequisiteCheck{passCheck(testStepA)}
		}}
		fp := dbFingerprint{}

		r.start(t.Context(), false, fp, stepsOf(step), summarizeForTest)
		r.start(t.Context(), false, fp, stepsOf(step), summarizeForTest) // joins
		synctest.Wait()
		r.reset()
		r.start(t.Context(), false, fp, stepsOf(step), summarizeForTest)
		synctest.Wait()
		r.start(t.Context(), false, fp, stepsOf(step), summarizeForTest) // reuses
		close(release)
		synctest.Wait()

		assert.Equal(t, []uint64{1, 2}, started)
		assert.Equal(t, []uint64{2}, ended, "the run abandoned by reset reports no outcome")
	})
}
