package diskmanager

import (
	"slices"
	"testing"
	"time"
)

// drainTestEnv holds the fakes behind a deletionRun built by newTestRun. Nothing
// here sleeps for real: the fake clock advances only inside the fake sleep (and
// by tick on every now call when tick is set).
type drainTestEnv struct {
	clock time.Time
	tick  time.Duration // added to the clock after each now call

	sleeps          []time.Duration // every requested wait, in order
	quit            chan struct{}
	quitAfterSleeps int // close quit during the Nth sleep (0: never)
	failSleepAt     int // make the Nth sleep report an interruption without closing quit (0: never)

	lockedCalls     int
	sleepsAtLock    []int                            // len(sleeps) when each lock list read happened
	lockedClips     func(call int) ([]string, error) // nil: lock refresh disabled
	settingsChanged func(call int) bool              // nil: never changed
	settingsCalls   int

	releases [][]string
}

// newTestRun builds a deletionRun with a small pacing config (batch of 2, long
// durations) and the fakes in the returned env. opts adjust env or the run
// before use.
func newTestRun(t *testing.T, opts ...func(*drainTestEnv, *deletionRun)) (*deletionRun, *drainTestEnv) {
	t.Helper()
	env := &drainTestEnv{
		clock: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		quit:  make(chan struct{}),
	}
	run := &deletionRun{
		policy: "test",
		quit:   env.quit,
		cfg: pacingConfig{
			minInterval:      minDeletionInterval,
			maxInterval:      maxDeletionInterval,
			backoffFactor:    slowDeletionBackoffFactor,
			batchSize:        2,
			maxBatchDuration: time.Hour,
			batchPause:       deletionBatchPause,
			runBudget:        time.Hour,
		},
		now: func() time.Time {
			now := env.clock
			env.clock = env.clock.Add(env.tick)
			return now
		},
		sleep: func(_ <-chan struct{}, d time.Duration) bool {
			env.sleeps = append(env.sleeps, d)
			env.clock = env.clock.Add(d)
			select {
			case <-env.quit:
				return false
			default:
			}
			if env.failSleepAt > 0 && len(env.sleeps) == env.failSleepAt {
				return false
			}
			if env.quitAfterSleeps > 0 && len(env.sleeps) == env.quitAfterSleeps {
				close(env.quit)
				return false
			}
			return true
		},
		release: func(paths []string) (int64, int64) {
			env.releases = append(env.releases, slices.Clone(paths))
			return 0, int64(len(paths))
		},
	}
	for _, opt := range opts {
		opt(env, run)
	}
	// The funcs read env at call time, so tests may set the hooks in opts.
	if env.lockedClips != nil {
		run.lockedClips = func() ([]string, error) {
			call := env.lockedCalls
			env.lockedCalls++
			env.sleepsAtLock = append(env.sleepsAtLock, len(env.sleeps))
			return env.lockedClips(call)
		}
	}
	if env.settingsChanged != nil {
		run.settingsChanged = func() bool {
			call := env.settingsCalls
			env.settingsCalls++
			return env.settingsChanged(call)
		}
	}
	return run, env
}
