// policy_drain.go - paced, batched deletion shared by the retention policies.
//
// A cleanup run walks the export tree once, then deletes in paced batches. The
// pacing exists to avoid saturating slow SD cards with bursts of deletion
// writes; it caps the peak rate, not the total work per run, so a large backlog
// drains continuously across batches.
package diskmanager

import (
	"context"
	"path/filepath"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// minDeletionInterval is the shortest wait after a deletion attempt. It is
	// the floor of the paced rate (about 10 deletions per second).
	minDeletionInterval = 100 * time.Millisecond

	// maxDeletionInterval is the ceiling of the adaptive wait after a deletion.
	maxDeletionInterval = 2 * time.Second

	// slowDeletionBackoffFactor multiplies the observed deletion latency to get
	// the wait, so cleanup holds the disk about a fifth of the time when
	// deletions are slow. Below 25 ms of latency the floor wins.
	slowDeletionBackoffFactor = 4

	// deletionBatchSize is the number of deletions after which a batch boundary
	// is due.
	deletionBatchSize = 200

	// maxDeletionBatchDuration is the longest time between batch boundaries.
	// A boundary refreshes the lock list and checks settings, so this bounds
	// how stale either can be, however sparse the deletions are.
	maxDeletionBatchDuration = 30 * time.Second

	// deletionBatchPause is the pause after a batch that wrote anything. It
	// gives the filesystem time to commit its journal between bursts of unlink
	// writes. It is a heuristic; it is not measured how much it helps.
	deletionBatchPause = 5 * time.Second

	// maxCleanupRunDuration is the deletion time budget of one run. Its clock
	// starts in begin, after the scan and sort. When it is spent the run
	// stops at the next batch boundary and a new run rescans, which bounds how
	// long one scan snapshot (file list, species counts, age cutoff) is used.
	// The snapshot is older than that by the scan time. Locks and settings are
	// re-checked at each boundary, and clips that arrive after the scan are
	// not in the snapshot, so they are never deleted by this run.
	maxCleanupRunDuration = 30 * time.Minute
)

// Reasons a cleanup run stopped, logged as stop_reason in the run summary.
const (
	stopExhausted         = "exhausted"
	stopBelowThreshold    = "below_threshold"
	stopTimeBudget        = "time_budget"
	stopSettingsChanged   = "settings_changed"
	stopLockRefreshFailed = "lock_refresh_failed"
	stopQuit              = "quit"
	stopTooManyErrors     = "too_many_errors"
)

// pacingConfig holds the tunables of a deletionRun. Tests use small values.
type pacingConfig struct {
	minInterval      time.Duration // shortest wait after a deletion attempt
	maxInterval      time.Duration // longest adaptive wait after a deletion attempt
	backoffFactor    int           // wait = backoffFactor x observed latency, clamped
	batchSize        int           // deletions per batch
	maxBatchDuration time.Duration // longest time between boundaries
	batchPause       time.Duration // pause after a batch that wrote
	runBudget        time.Duration // time budget of the whole run
}

// defaultPacingConfig returns the production pacing.
func defaultPacingConfig() pacingConfig {
	return pacingConfig{
		minInterval:      minDeletionInterval,
		maxInterval:      maxDeletionInterval,
		backoffFactor:    slowDeletionBackoffFactor,
		batchSize:        deletionBatchSize,
		maxBatchDuration: maxDeletionBatchDuration,
		batchPause:       deletionBatchPause,
		runBudget:        maxCleanupRunDuration,
	}
}

// nextDeletionDelay returns the wait after a deletion attempt that took
// latency: backoffFactor x latency, clamped to [minInterval, maxInterval]. The
// adaptive part only lengthens the wait, so the peak rate never exceeds the
// floor's.
func nextDeletionDelay(latency time.Duration, cfg pacingConfig) time.Duration {
	return min(max(time.Duration(cfg.backoffFactor)*latency, cfg.minInterval), cfg.maxInterval)
}

// retentionSnapshot holds the settings that decide which clips a run may
// delete. A run compares it with the live settings at every batch boundary and
// ends when they differ, so a change takes effect without a restart.
// CheckInterval and Debug are left out on purpose: they do not change
// eligibility.
type retentionSnapshot struct {
	path             string
	policy           string
	maxAge           string
	maxUsage         string
	minClips         int
	keepSpectrograms bool
}

// newRetentionSnapshot captures the eligibility-relevant retention settings.
func newRetentionSnapshot(baseDir string, r *conf.RetentionSettings) retentionSnapshot {
	return retentionSnapshot{
		path:             baseDir,
		policy:           r.Policy,
		maxAge:           r.MaxAge,
		maxUsage:         r.MaxUsage,
		minClips:         r.MinClips,
		keepSpectrograms: r.KeepSpectrograms,
	}
}

// deletionRun drives one paced cleanup run: it batches deletions, releases each
// batch's database references, re-checks locks and settings at every batch
// boundary, and enforces the pacing and the time budget. The collaborators are
// plain funcs so tests can inject a clock and sleeps.
type deletionRun struct {
	policy string
	quit   <-chan struct{}
	cfg    pacingConfig

	now             func() time.Time
	sleep           func(quit <-chan struct{}, d time.Duration) bool // false when quit closed
	lockedClips     func() ([]string, error)                         // nil: skip lock refresh
	settingsChanged func() bool                                      // nil: never changed
	release         func(paths []string) (retained, cleared int64)   // nil: no-op

	locked map[string]struct{} // basenames of locked clips at the last refresh; nil: never refreshed

	startedAt       time.Time
	lastBoundaryAt  time.Time
	batch           []string
	deletedTotal    int
	batches         int
	recordsRetained int64
	recordsCleared  int64
	stopReason      string
}

// newDeletionRun wires a run for production use.
func newDeletionRun(policy string, quit <-chan struct{}, db Interface, baseDir string, keepSpectrograms bool, start *retentionSnapshot) *deletionRun {
	return &deletionRun{
		policy: policy,
		quit:   quit,
		cfg:    defaultPacingConfig(),
		now:    time.Now,
		sleep:  reconcileSleep,
		lockedClips: func() ([]string, error) {
			return getLockedClips(db)
		},
		settingsChanged: func() bool {
			s := conf.Setting()
			return newRetentionSnapshot(s.Realtime.Audio.Export.Path, &s.Realtime.Audio.Export.Retention) != *start
		},
		release: func(paths []string) (int64, int64) {
			return releaseDeletedClipPaths(db, paths, baseDir, policy, keepSpectrograms)
		},
	}
}

// refreshLocks re-reads the locked clip list into the run's lock set, keyed by
// basename the same way the scan matches it. Files are not re-marked here;
// markLocked applies the set to each file when the loop reaches it, so a
// refresh costs one read of the lock list, not a pass over every unvisited file.
func (r *deletionRun) refreshLocks() error {
	if r.lockedClips == nil {
		return nil
	}
	clips, err := r.lockedClips()
	if err != nil {
		return err
	}
	locked := make(map[string]struct{}, len(clips))
	for _, p := range clips {
		locked[filepath.Base(p)] = struct{}{}
	}
	r.locked = locked
	return nil
}

// markLocked sets file.Locked from the lock set of the last refresh. The loops
// call it on each file just before deciding about it. Before any refresh (or
// when lock refresh is disabled) it keeps the lock state from the scan.
func (r *deletionRun) markLocked(file *FileInfo) {
	if r.locked == nil {
		return
	}
	_, file.Locked = r.locked[filepath.Base(file.Path)]
}

// begin starts the run clock and refreshes the lock set before the first
// deletion, since the scan snapshot is older than the sort. It
// returns false, with stopReason set, when the lock list cannot be read (the
// run fails closed rather than delete on a stale lock state) or when the
// retention settings changed during the scan (the run would otherwise delete
// a first batch under the old settings).
func (r *deletionRun) begin() bool {
	r.startedAt = r.now()
	r.lastBoundaryAt = r.startedAt
	if err := r.refreshLocks(); err != nil {
		r.failLockRefresh(err)
		return false
	}
	if r.settingsChanged != nil && r.settingsChanged() {
		r.stopReason = stopSettingsChanged
		return false
	}
	return true
}

func (r *deletionRun) failLockRefresh(err error) {
	GetLogger().Warn("Cleanup run stopped: could not refresh the locked clip list",
		logger.String("policy", r.policy),
		logger.Error(err))
	r.stopReason = stopLockRefreshFailed
}

// boundaryDue reports whether the next iteration must start with endBatch:
// the batch is full or maxBatchDuration has passed since the last boundary.
func (r *deletionRun) boundaryDue() bool {
	return len(r.batch) >= r.cfg.batchSize || r.now().Sub(r.lastBoundaryAt) >= r.cfg.maxBatchDuration
}

// releaseBatch hands the current batch to the release func and resets it.
func (r *deletionRun) releaseBatch() {
	if len(r.batch) == 0 {
		return
	}
	if r.release != nil {
		retained, cleared := r.release(r.batch)
		r.recordsRetained += retained
		r.recordsCleared += cleared
	}
	r.batches++
	r.batch = r.batch[:0]
}

// endBatch closes the current batch at a boundary. It releases the batch's
// database references and pauses when the batch wrote anything. Then it checks
// quit, the time budget and the settings, and refreshes the lock set, so each
// of those decisions is the last step before deletion resumes. It returns
// false, with stopReason set, when the run must end.
func (r *deletionRun) endBatch() bool {
	wrote := len(r.batch) > 0
	r.releaseBatch()

	if wrote && !r.sleep(r.quit, r.cfg.batchPause) {
		r.stopReason = stopQuit
		return false
	}
	select {
	case <-r.quit:
		r.stopReason = stopQuit
		return false
	default:
	}
	if r.now().Sub(r.startedAt) >= r.cfg.runBudget {
		r.stopReason = stopTimeBudget
		return false
	}
	if r.settingsChanged != nil && r.settingsChanged() {
		r.stopReason = stopSettingsChanged
		return false
	}
	if err := r.refreshLocks(); err != nil {
		r.failLockRefresh(err)
		return false
	}
	r.lastBoundaryAt = r.now()
	return true
}

// afterDeletionAttempt records the outcome of a deletion attempt that touched
// the disk and waits before the next one. A failed attempt waits too, so the
// peak rate holds on error paths. It returns false, with stopReason set, when
// quit closed during the wait.
func (r *deletionRun) afterDeletionAttempt(path string, deleted bool, latency time.Duration) bool {
	if deleted {
		r.batch = append(r.batch, path)
		r.deletedTotal++
	}
	if !r.sleep(r.quit, nextDeletionDelay(latency, r.cfg)) {
		r.stopReason = stopQuit
		return false
	}
	return true
}

// moreWork reports whether a follow-up run should start soon: the run stopped
// on a settings change (the follow-up takes a fresh settings snapshot, so it
// does not stop on that change again), or on its time budget after deleting
// something while remainingWork says deletable candidates are left. A run that
// deleted nothing never asks for one on its budget, so protected files cannot
// cause a rescan loop.
func (r *deletionRun) moreWork(remainingWork bool) bool {
	return r.stopReason == stopSettingsChanged || (r.stopReason == stopTimeBudget && r.deletedTotal > 0 && remainingWork)
}

// finish releases the tail batch. It is safe to call more than once.
func (r *deletionRun) finish() {
	r.releaseBatch()
}

// quitContext returns a context that is cancelled when quit closes or when the
// returned cancel func is called. The caller must call cancel.
func quitContext(quit <-chan struct{}) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	select {
	case <-quit:
		cancel() // already closed: cancel now so a scan started at once sees it
		return ctx, cancel
	default:
	}
	go func() {
		select {
		case <-quit:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}
