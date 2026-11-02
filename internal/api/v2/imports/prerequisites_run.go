// internal/api/v2/imports/prerequisites_run.go
package importsapi

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Prerequisites run status values reported in PrerequisitesResponse.Status.
// "failed" means the run itself did not finish (timeout, shutdown, panic);
// failing checks still produce "completed".
const (
	PrereqRunStatusIdle      = "idle"
	PrereqRunStatusRunning   = "running"
	PrereqRunStatusCompleted = "completed"
	PrereqRunStatusFailed    = "failed"
)

const (
	// prereqResultMaxAge is how long a completed passing result is reused by a
	// non-forced start of a run.
	prereqResultMaxAge = 30 * time.Minute
	// prereqRunTimeout bounds a whole run. It covers the live integrity scan
	// fallback and MySQL CHECK TABLE on large tables.
	prereqRunTimeout = 30 * time.Minute

	// Error texts stored on a failed run.
	msgPrereqRunTimedOut   = "The checks took longer than the time limit"
	msgPrereqRunCancelled  = "The checks were cancelled because the server is shutting down"
	msgPrereqRunPanicked   = "The checks stopped because of an internal error"
	msgPrereqRunShutdown   = "The server is shutting down"
	prereqRunTimeoutFormat = "%s (%s)"
)

// PrerequisitesProgress reports how far a running prerequisites run has got.
type PrerequisitesProgress struct {
	CompletedSteps int    `json:"completed_steps"`
	TotalSteps     int    `json:"total_steps"`
	CurrentCheckID string `json:"current_check_id,omitempty"`
	// CurrentCheckName is the display name of the check being run.
	CurrentCheckName string `json:"current_check_name,omitempty"`
	// CurrentCheckElapsedMs is computed on the server when the snapshot is
	// taken, so browser clock skew does not matter.
	CurrentCheckElapsedMs int64 `json:"current_check_elapsed_ms,omitempty"`
}

// prereqStep is one unit of a prerequisites run. A step returns a slice so a
// check that yields zero to two rows (the MySQL configuration warnings) stays one step.
type prereqStep struct {
	id   string
	name string
	// reuseAtStart marks the checks that are slow or touch the single legacy
	// connection; a migration start takes them from a passing run instead of
	// running them again.
	reuseAtStart bool
	run          func(ctx context.Context) []PrerequisiteCheck
}

// recordCountSink carries the legacy record count out of the record_count step.
// Steps run one after another in the run goroutine, so it needs no lock.
type recordCountSink struct {
	count int64
	known bool
}

// dbFingerprint identifies the database settings a result was produced for. It
// is comparable, so reuse is a plain equality test.
type dbFingerprint struct {
	MySQLEnabled  bool
	SQLitePath    string
	MySQLHost     string
	MySQLPort     string
	MySQLDatabase string
}

// prereqReuseReason says why a stored result cannot be reused. The zero value
// means it can.
type prereqReuseReason string

// Reasons a stored result cannot be reused.
const (
	prereqReuseOK              prereqReuseReason = ""
	prereqReuseNoResult        prereqReuseReason = "no_result"
	prereqReuseRunning         prereqReuseReason = "running"
	prereqReuseFailedCheck     prereqReuseReason = "failed_check"
	prereqReuseStale           prereqReuseReason = "stale"
	prereqReuseSettingsChanged prereqReuseReason = "settings_changed"
)

// prereqReusable is a committed passing result together with the record count
// of the run that produced it.
type prereqReusable struct {
	Response         PrerequisitesResponse
	RecordCount      int64
	RecordCountKnown bool
}

// prereqRunner owns the single prerequisites run slot of a process. Its zero
// value is ready to use. A run commits its result only while its generation is
// still current, so reset() can abandon a run without it touching later state.
type prereqRunner struct {
	mu sync.Mutex

	status        string
	runID         uint64
	startedAt     time.Time
	finishedAt    time.Time
	checks        []PrerequisiteCheck
	progress      PrerequisitesProgress
	stepStartedAt time.Time
	errMsg        string

	// Committed together at completion, never mid-run.
	result                 *PrerequisitesResponse
	resultRecordCount      int64
	resultRecordCountKnown bool
	resultFingerprint      dbFingerprint

	generation uint64
	cancelRun  context.CancelFunc

	// now is the clock; nil means time.Now. spawn starts the run goroutine; nil
	// means a plain go statement, the handler passes Core.Go.
	now   func() time.Time
	spawn func(func())
}

// clock returns the current time through the now seam.
func (r *prereqRunner) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// start begins a run, or returns the snapshot of the run in progress or of a
// reusable result. A start while a run is in progress joins it whatever force
// says. parent bounds the run (a closed browser tab must not cancel a shared
// run, so callers pass the server lifecycle context, never a request context).
func (r *prereqRunner) start(parent context.Context, force bool, fp dbFingerprint, steps func(*recordCountSink) []prereqStep, finish func([]PrerequisiteCheck) PrerequisitesResponse) PrerequisitesResponse {
	if parent.Err() != nil {
		return PrerequisitesResponse{Checks: []PrerequisiteCheck{}, Status: PrereqRunStatusFailed, Error: msgPrereqRunShutdown}
	}

	r.mu.Lock()
	if r.status == PrereqRunStatusRunning {
		snap := r.snapshotLocked()
		r.mu.Unlock()
		return snap
	}
	if !force && r.reuseReasonLocked(fp) == prereqReuseOK {
		snap := r.snapshotLocked()
		r.mu.Unlock()
		return snap
	}

	sink := &recordCountSink{}
	runSteps := steps(sink)
	ctx, cancel := context.WithTimeout(parent, prereqRunTimeout)
	r.runID++
	r.generation++
	gen, runID := r.generation, r.runID
	r.status = PrereqRunStatusRunning
	r.startedAt = r.clock()
	r.finishedAt = time.Time{}
	r.checks = nil
	r.errMsg = ""
	r.progress = PrerequisitesProgress{TotalSteps: len(runSteps)}
	r.stepStartedAt = time.Time{}
	r.cancelRun = cancel
	snap := r.snapshotLocked()
	r.mu.Unlock()

	body := func() { r.execute(ctx, cancel, gen, runID, fp, runSteps, sink, finish) }
	if r.spawn != nil {
		r.spawn(body)
	} else {
		go body()
	}
	return snap
}

// execute is the run goroutine. It never outlives its context: every step gets
// ctx, and the loop stops as soon as ctx ends.
func (r *prereqRunner) execute(ctx context.Context, cancel context.CancelFunc, gen, runID uint64, fp dbFingerprint, steps []prereqStep, sink *recordCountSink, finish func([]PrerequisiteCheck) PrerequisitesResponse) {
	defer cancel()
	defer func() {
		if rec := recover(); rec != nil {
			r.fail(gen, fmt.Sprintf("%s (run %d)", msgPrereqRunPanicked, runID))
		}
	}()

	for i := range steps {
		step := &steps[i]
		r.mu.Lock()
		if r.generation != gen {
			r.mu.Unlock()
			return
		}
		r.progress.CurrentCheckID = step.id
		r.progress.CurrentCheckName = step.name
		r.stepStartedAt = r.clock()
		r.mu.Unlock()

		out := step.run(ctx)

		r.mu.Lock()
		if r.generation != gen {
			r.mu.Unlock()
			return
		}
		if ctx.Err() != nil {
			r.failLocked(ctxFailureMessage(ctx))
			r.mu.Unlock()
			return
		}
		r.checks = append(r.checks, out...)
		r.progress.CompletedSteps++
		r.mu.Unlock()
	}

	r.mu.Lock()
	if r.generation != gen {
		r.mu.Unlock()
		return
	}
	checks := slices.Clone(r.checks)
	r.mu.Unlock()

	result := finish(checks)

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.generation != gen {
		return
	}
	r.finishedAt = r.clock()
	result.CheckedAt = r.finishedAt
	r.result = &result
	r.resultRecordCount = sink.count
	r.resultRecordCountKnown = sink.known
	r.resultFingerprint = fp
	r.status = PrereqRunStatusCompleted
	r.progress = PrerequisitesProgress{}
	r.stepStartedAt = time.Time{}
	r.cancelRun = nil
}

// ctxFailureMessage names why ctx ended.
func ctxFailureMessage(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Sprintf(prereqRunTimeoutFormat, msgPrereqRunTimedOut, prereqRunTimeout)
	}
	return msgPrereqRunCancelled
}

// fail marks the run of generation gen failed, unless a newer run owns the slot.
func (r *prereqRunner) fail(gen uint64, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.generation != gen {
		return
	}
	r.failLocked(msg)
}

// failLocked marks the current run failed. The caller holds mu.
func (r *prereqRunner) failLocked(msg string) {
	r.status = PrereqRunStatusFailed
	r.errMsg = msg
	r.finishedAt = r.clock()
	r.progress = PrerequisitesProgress{}
	r.stepStartedAt = time.Time{}
	r.cancelRun = nil
}

// reuseReasonLocked reports whether the committed result can be reused for fp.
// The caller holds mu.
func (r *prereqRunner) reuseReasonLocked(fp dbFingerprint) prereqReuseReason {
	switch {
	case r.status == PrereqRunStatusRunning:
		return prereqReuseRunning
	case r.status != PrereqRunStatusCompleted || r.result == nil:
		return prereqReuseNoResult
	case !r.result.AllPassed:
		return prereqReuseFailedCheck
	case r.resultFingerprint != fp:
		return prereqReuseSettingsChanged
	case r.clock().Sub(r.finishedAt) >= prereqResultMaxAge:
		return prereqReuseStale
	}
	return prereqReuseOK
}

// snapshot returns a copy of the current state; idle before any run.
func (r *prereqRunner) snapshot() PrerequisitesResponse {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked()
}

// snapshotLocked builds the response for the current state. The caller holds mu.
func (r *prereqRunner) snapshotLocked() PrerequisitesResponse {
	switch r.status {
	case PrereqRunStatusCompleted:
		resp := *r.result
		resp.Checks = cloneChecks(resp.Checks)
		resp.Status = PrereqRunStatusCompleted
		resp.RunID = r.runID
		resp.StartedAt = r.startedAt
		return resp
	case PrereqRunStatusRunning:
		progress := r.progress
		if !r.stepStartedAt.IsZero() {
			progress.CurrentCheckElapsedMs = r.clock().Sub(r.stepStartedAt).Milliseconds()
		}
		return PrerequisitesResponse{
			Checks:    cloneChecks(r.checks),
			Status:    PrereqRunStatusRunning,
			RunID:     r.runID,
			StartedAt: r.startedAt,
			Progress:  &progress,
		}
	case PrereqRunStatusFailed:
		return PrerequisitesResponse{
			Checks:    cloneChecks(r.checks),
			Status:    PrereqRunStatusFailed,
			RunID:     r.runID,
			StartedAt: r.startedAt,
			Error:     r.errMsg,
		}
	}
	return PrerequisitesResponse{Checks: []PrerequisiteCheck{}, Status: PrereqRunStatusIdle, RunID: r.runID}
}

// cloneChecks copies a check slice, returning a non-nil empty slice for nil so
// the JSON field is always an array.
func cloneChecks(checks []PrerequisiteCheck) []PrerequisiteCheck {
	if len(checks) == 0 {
		return []PrerequisiteCheck{}
	}
	return slices.Clone(checks)
}

// reset cancels an in-flight run, forgets the result and returns to idle. The
// cancelled run commits nothing because its generation is no longer current.
func (r *prereqRunner) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancelRun != nil {
		r.cancelRun()
		r.cancelRun = nil
	}
	r.generation++
	r.status = PrereqRunStatusIdle
	r.checks = nil
	r.progress = PrerequisitesProgress{}
	r.stepStartedAt = time.Time{}
	r.errMsg = ""
	r.result = nil
	r.resultRecordCount = 0
	r.resultRecordCountKnown = false
	r.resultFingerprint = dbFingerprint{}
}

// passingResult returns the committed result when it is a completed run that
// passed, matches fp and is fresh; otherwise it reports why not.
func (r *prereqRunner) passingResult(fp dbFingerprint) (prereqReusable, prereqReuseReason) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if reason := r.reuseReasonLocked(fp); reason != prereqReuseOK {
		return prereqReusable{}, reason
	}
	return prereqReusable{
		Response:         r.snapshotLocked(),
		RecordCount:      r.resultRecordCount,
		RecordCountKnown: r.resultRecordCountKnown,
	}, prereqReuseOK
}
