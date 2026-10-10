package diskmanager

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// drainTestSpecies is the species of the files makeDrainFiles writes.
const drainTestSpecies = "species_a"

// makeDrainFiles writes n 1-byte clips of drainTestSpecies into dir, all older
// than a 7 day cutoff, and returns them sorted oldest first as the age loop
// expects.
func makeDrainFiles(t *testing.T, dir string, n int) []FileInfo {
	t.Helper()
	files := make([]FileInfo, 0, n)
	for i := range n {
		path := filepath.Join(dir, fmt.Sprintf("%s_80p_%03d.wav", drainTestSpecies, i))
		require.NoError(t, os.WriteFile(path, []byte("a"), 0o600))
		files = append(files, FileInfo{
			Path:      path,
			Species:   drainTestSpecies,
			Timestamp: time.Now().Add(-time.Duration(1000-i) * time.Hour),
			Size:      1,
		})
	}
	return files
}

// drainCutoff is a retention cutoff of 7 days ago; makeDrainFiles clips are older.
func drainCutoff() int64 { return time.Now().Add(-168 * time.Hour).Unix() }

// runAge runs the age loop over files with minClips 0 and the given run.
func runAge(files []FileInfo, run *deletionRun) (deleted []string, stats cleanupStats) {
	_, deleted, stats, _ = processAgeBasedDeletionLoop(files, buildSpeciesTotalCountMap(files), 0, false, run, drainCutoff())
	run.finish()
	return deleted, stats
}

func TestNextDeletionDelay(t *testing.T) {
	t.Parallel()

	cfg := defaultPacingConfig()
	tests := []struct {
		name    string
		latency time.Duration
		want    time.Duration
	}{
		{"zero latency gets the floor", 0, 100 * time.Millisecond},
		{"20 ms latency still gets the floor", 20 * time.Millisecond, 100 * time.Millisecond},
		{"100 ms latency waits four times as long", 100 * time.Millisecond, 400 * time.Millisecond},
		{"very slow deletion is capped", 10 * time.Second, 2 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, nextDeletionDelay(tt.latency, cfg))
		})
	}
}

func TestDeletionRun_PacesEveryAttemptIncludingErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	files := makeDrainFiles(t, dir, 1)
	// A non-empty directory named like a clip: os.Remove fails on it.
	blocked := filepath.Join(dir, drainTestSpecies+"_80p_blocked.wav")
	require.NoError(t, os.MkdirAll(blocked, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(blocked, "inner"), []byte("x"), 0o600))
	files = append(files, FileInfo{Path: blocked, Species: drainTestSpecies, Timestamp: time.Now().Add(-500 * time.Hour), Size: 1})

	run, env := newTestRun(t)
	_, stats := runAge(files, run)

	assert.Equal(t, 1, stats.Deleted)
	assert.Equal(t, 1, stats.Errors)
	require.Len(t, env.sleeps, 2, "the deletion and the failed attempt are each followed by a wait")
	for _, d := range env.sleeps {
		assert.GreaterOrEqual(t, d, minDeletionInterval)
	}
}

func TestDeletionRun_ReleasesEachDeletedPathOnce(t *testing.T) {
	t.Parallel()

	t.Run("full run", func(t *testing.T) {
		t.Parallel()
		files := makeDrainFiles(t, t.TempDir(), 5)
		run, env := newTestRun(t)

		deleted, stats := runAge(files, run)

		require.Len(t, deleted, 5)
		assert.Equal(t, [][]string{
			{files[0].Path, files[1].Path},
			{files[2].Path, files[3].Path},
			{files[4].Path},
		}, env.releases, "full batches release at their boundary, the tail from finish")
		assert.Equal(t, 3, run.batches)
		assert.Equal(t, int64(5), run.recordsCleared)
		assert.Equal(t, stopExhausted, stats.StopReason)
		run.finish()
		assert.Len(t, env.releases, 3, "finish is idempotent")
	})

	t.Run("quit after the third attempt", func(t *testing.T) {
		t.Parallel()
		files := makeDrainFiles(t, t.TempDir(), 5)
		// Sleeps in order: attempt 1, attempt 2, batch pause, attempt 3. Quit
		// closes during the wait after the third attempt.
		run, env := newTestRun(t, func(e *drainTestEnv, _ *deletionRun) { e.quitAfterSleeps = 4 })

		deleted, stats := runAge(files, run)

		assert.Equal(t, []string{files[0].Path, files[1].Path, files[2].Path}, deleted)
		assert.Equal(t, [][]string{
			{files[0].Path, files[1].Path},
			{files[2].Path},
		}, env.releases, "the quit-time tail is released exactly once and nothing after it")
		assert.Equal(t, stopQuit, stats.StopReason)
		assert.FileExists(t, files[3].Path)
	})
}

func TestDeletionRun_LockChangesHonoredAtBoundary(t *testing.T) {
	t.Parallel()

	baseNames := func(files []FileInfo, idx ...int) []string {
		out := make([]string, 0, len(idx))
		for _, i := range idx {
			out = append(out, filepath.Base(files[i].Path))
		}
		return out
	}

	tests := []struct {
		name string
		// scanLocked marks files as locked in the scan snapshot.
		scanLocked []int
		// lockedAt returns the indexes locked at the n-th lock list read (0 is begin).
		lockedAt  func(call int) []int
		wantAlive []int
	}{
		{
			name:      "locked after the scan, before the first deletion",
			lockedAt:  func(int) []int { return []int{2} },
			wantAlive: []int{2},
		},
		{
			name: "locked between two boundaries",
			lockedAt: func(call int) []int {
				if call >= 2 {
					return []int{5}
				}
				return nil
			},
			wantAlive: []int{5},
		},
		{
			name:       "locked at the scan, unlocked before a boundary",
			scanLocked: []int{3},
			lockedAt: func(call int) []int {
				if call == 0 {
					return []int{3}
				}
				return nil
			},
			wantAlive: nil,
		},
	}
	// Each loop applies the refreshed lock set to a file on its own, so both
	// run the same table.
	loops := []struct {
		name string
		run  func(t *testing.T, files []FileInfo, run *deletionRun)
	}{
		{"age", func(_ *testing.T, files []FileInfo, run *deletionRun) { runAge(files, run) }},
		{"usage", func(t *testing.T, files []FileInfo, run *deletionRun) {
			t.Helper()
			// Usage stays far above the target, so only locks keep files.
			params := newUsageLoopTestParams(1000, 900, 80, 0)
			_, _, _, _, err := processUsageDeletionLoop(files, buildSpeciesSubDirCountMap(files), params, filepath.Dir(files[0].Path), run)
			run.finish()
			require.NoError(t, err)
		}},
	}
	for _, loop := range loops {
		for _, tt := range tests {
			t.Run(loop.name+": "+tt.name, func(t *testing.T) {
				t.Parallel()
				files := makeDrainFiles(t, t.TempDir(), 6)
				for _, i := range tt.scanLocked {
					files[i].Locked = true
				}
				run, _ := newTestRun(t, func(e *drainTestEnv, _ *deletionRun) {
					e.lockedClips = func(call int) ([]string, error) { return baseNames(files, tt.lockedAt(call)...), nil }
				})

				loop.run(t, files, run)

				alive := map[int]bool{}
				for _, i := range tt.wantAlive {
					alive[i] = true
				}
				for i, f := range files {
					if alive[i] {
						assert.FileExists(t, f.Path, "file %d must survive", i)
					} else {
						assert.NoFileExists(t, f.Path, "file %d must be deleted", i)
					}
				}
			})
		}
	}

	t.Run("sparse deletions still refresh on time", func(t *testing.T) {
		t.Parallel()
		const skipped = 12
		dir := t.TempDir()
		files := makeDrainFiles(t, dir, skipped+2)
		// files[0] is deletable; files[1..skipped] are locked in the scan and
		// skipped; the last file becomes locked from the third lock read on.
		for i := 1; i <= skipped; i++ {
			files[i].Locked = true
		}
		last := len(files) - 1
		run, env := newTestRun(t, func(e *drainTestEnv, r *deletionRun) {
			r.cfg.batchSize = 1000
			r.cfg.maxBatchDuration = 3 * time.Second
			e.tick = time.Second
			e.lockedClips = func(call int) ([]string, error) {
				idx := make([]int, 0, skipped+1)
				for i := 1; i <= skipped; i++ {
					idx = append(idx, i)
				}
				if call >= 2 {
					idx = append(idx, last)
				}
				return baseNames(files, idx...), nil
			}
		})

		_, stats := runAge(files, run)

		assert.GreaterOrEqual(t, env.lockedCalls, 4, "time-based boundaries must keep firing between sparse deletions")
		assert.FileExists(t, files[last].Path, "a lock added during the sparse gap must be honored")
		assert.Equal(t, 1, stats.Deleted, "only the first file is deletable")
		pauses := 0
		for _, d := range env.sleeps {
			if d == deletionBatchPause {
				pauses++
			}
		}
		assert.Equal(t, 1, pauses, "boundaries with an empty batch do not pause")
	})
}

// pauseSlept reports whether the first batch pause has been slept: with a
// batch of 2, the sleeps are attempt 1, attempt 2, then the pause.
func pauseSlept(e *drainTestEnv) bool { return len(e.sleeps) >= 3 }

// TestDeletionRun_ChecksRunAfterTheBatchPause verifies that endBatch makes its
// stop decisions after the batch pause, so a change that happens during the
// pause is acted on before the next deletion, not one batch later. Each
// fixture flips only once the pause has been slept.
func TestDeletionRun_ChecksRunAfterTheBatchPause(t *testing.T) {
	t.Parallel()

	t.Run("a lock taken during the pause keeps the file", func(t *testing.T) {
		t.Parallel()
		files := makeDrainFiles(t, t.TempDir(), 4)
		run, env := newTestRun(t, func(e *drainTestEnv, _ *deletionRun) {
			e.lockedClips = func(int) ([]string, error) {
				if pauseSlept(e) {
					return []string{filepath.Base(files[2].Path)}, nil
				}
				return nil, nil
			}
		})

		runAge(files, run)

		require.GreaterOrEqual(t, len(env.sleeps), 3)
		require.Equal(t, deletionBatchPause, env.sleeps[2], "the third wait is the batch pause")
		assert.FileExists(t, files[2].Path, "locked during the pause")
		assert.NoFileExists(t, files[3].Path)
	})

	t.Run("a settings change during the pause stops the run", func(t *testing.T) {
		t.Parallel()
		files := makeDrainFiles(t, t.TempDir(), 4)
		run, _ := newTestRun(t, func(e *drainTestEnv, _ *deletionRun) {
			e.settingsChanged = func(int) bool { return pauseSlept(e) }
		})

		deleted, stats := runAge(files, run)

		assert.Len(t, deleted, 2, "no deletion after the pause that saw the change")
		assert.Equal(t, stopSettingsChanged, stats.StopReason)
	})

	t.Run("a budget spent during the pause stops the run", func(t *testing.T) {
		t.Parallel()
		files := makeDrainFiles(t, t.TempDir(), 4)
		run, _ := newTestRun(t, func(_ *drainTestEnv, r *deletionRun) {
			// The two attempts take 200 ms; only the 5 s pause spends the budget.
			r.cfg.runBudget = time.Second
		})

		deleted, stats := runAge(files, run)

		assert.Len(t, deleted, 2, "no deletion after the pause that spent the budget")
		assert.Equal(t, stopTimeBudget, stats.StopReason)
	})
}

func TestDeletionRun_LockRefreshErrorStops(t *testing.T) {
	t.Parallel()

	t.Run("error before the first deletion", func(t *testing.T) {
		t.Parallel()
		files := makeDrainFiles(t, t.TempDir(), 3)
		run, _ := newTestRun(t, func(e *drainTestEnv, _ *deletionRun) {
			e.lockedClips = func(int) ([]string, error) { return nil, assert.AnError }
		})

		deleted, stats := runAge(files, run)

		assert.Empty(t, deleted)
		assert.Equal(t, stopLockRefreshFailed, stats.StopReason)
		assert.False(t, stats.MoreWork)
		for _, f := range files {
			assert.FileExists(t, f.Path)
		}
	})

	t.Run("error at a later boundary", func(t *testing.T) {
		t.Parallel()
		files := makeDrainFiles(t, t.TempDir(), 6)
		run, env := newTestRun(t, func(e *drainTestEnv, _ *deletionRun) {
			e.lockedClips = func(call int) ([]string, error) {
				if call == 0 {
					return nil, nil
				}
				return nil, assert.AnError
			}
		})

		deleted, stats := runAge(files, run)

		assert.Equal(t, []string{files[0].Path, files[1].Path}, deleted)
		assert.Equal(t, stopLockRefreshFailed, stats.StopReason)
		assert.False(t, stats.MoreWork)
		assert.Equal(t, [][]string{{files[0].Path, files[1].Path}}, env.releases, "the closed batch is still released")
		assert.FileExists(t, files[2].Path)
	})
}

func TestDeletionRun_SettingsChangeEndsRun(t *testing.T) {
	t.Parallel()

	t.Run("change seen at the first boundary", func(t *testing.T) {
		t.Parallel()
		files := makeDrainFiles(t, t.TempDir(), 6)
		run, _ := newTestRun(t, func(e *drainTestEnv, _ *deletionRun) {
			e.settingsChanged = func(call int) bool { return call > 0 } // unchanged at begin
		})

		deleted, stats := runAge(files, run)

		assert.Len(t, deleted, 2, "no deletions after the boundary that saw the change")
		assert.Equal(t, stopSettingsChanged, stats.StopReason)
		assert.True(t, stats.MoreWork, "a fresh run with the new settings should start soon")
		assert.FileExists(t, files[2].Path)
	})

	t.Run("change seen before any deletion", func(t *testing.T) {
		t.Parallel()
		files := makeDrainFiles(t, t.TempDir(), 3)
		run, _ := newTestRun(t, func(e *drainTestEnv, r *deletionRun) {
			r.cfg.maxBatchDuration = 0                                  // a boundary is due at the first file
			e.settingsChanged = func(call int) bool { return call > 0 } // unchanged at begin
		})

		deleted, stats := runAge(files, run)

		assert.Empty(t, deleted)
		assert.Equal(t, stopSettingsChanged, stats.StopReason)
		assert.True(t, stats.MoreWork, "the new settings should take effect soon, even when nothing was deleted yet")
	})

	// The settings snapshot is taken before the scan, so a change made while the
	// tree is walked must stop the run before its first deletion, not one batch later.
	changedDuringScan := func(e *drainTestEnv, _ *deletionRun) {
		e.settingsChanged = func(call int) bool { return call == 0 }
	}

	t.Run("age: change made during the scan", func(t *testing.T) {
		t.Parallel()
		files := makeDrainFiles(t, t.TempDir(), 3)
		run, _ := newTestRun(t, changedDuringScan)

		deleted, stats := runAge(files, run)

		assert.Empty(t, deleted, "nothing may be deleted under the stale settings")
		assert.Equal(t, stopSettingsChanged, stats.StopReason)
		assert.True(t, stats.MoreWork)
		assert.FileExists(t, files[0].Path)
	})

	t.Run("usage: change made during the scan", func(t *testing.T) {
		t.Parallel()
		testDir := t.TempDir()
		files := makeDrainFiles(t, testDir, 3)
		run, _ := newTestRun(t, changedDuringScan)

		params := newUsageLoopTestParams(1000, 900, 80, 0)
		deletedCount, _, _, stats, loopErr := processUsageDeletionLoop(files, buildSpeciesSubDirCountMap(files), params, testDir, run)
		run.finish()

		require.NoError(t, loopErr)
		assert.Equal(t, 0, deletedCount, "nothing may be deleted under the stale settings")
		assert.Equal(t, stopSettingsChanged, stats.StopReason)
		assert.True(t, stats.MoreWork)
		assert.FileExists(t, files[0].Path)
	})
}

func TestDeletionRun_TimeBudget(t *testing.T) {
	t.Parallel()

	// Two attempts wait 100 ms each, so a 150 ms budget is spent at the first boundary.
	spent := func(_ *drainTestEnv, r *deletionRun) { r.cfg.runBudget = 150 * time.Millisecond }

	t.Run("age: next file still old", func(t *testing.T) {
		t.Parallel()
		files := makeDrainFiles(t, t.TempDir(), 4)
		run, _ := newTestRun(t, spent)

		deleted, stats := runAge(files, run)

		assert.Len(t, deleted, 2)
		assert.Equal(t, stopTimeBudget, stats.StopReason)
		assert.True(t, stats.MoreWork)
		assert.True(t, run.moreWork(true))
		assert.False(t, run.moreWork(false), "no remaining work, no follow-up")
	})

	t.Run("age: next file newer than the cutoff", func(t *testing.T) {
		t.Parallel()
		files := makeDrainFiles(t, t.TempDir(), 3)
		files[2].Timestamp = time.Now().Add(-time.Hour)
		run, _ := newTestRun(t, spent)

		deleted, stats := runAge(files, run)

		assert.Len(t, deleted, 2)
		assert.Equal(t, stopTimeBudget, stats.StopReason)
		assert.False(t, stats.MoreWork, "everything left is too new to delete")
	})

	t.Run("usage: still over the threshold", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		files := makeDrainFiles(t, dir, 4)
		run, _ := newTestRun(t, spent)
		params := newUsageLoopTestParams(1000, 500, 0, 0)

		_, _, _, stats, err := processUsageDeletionLoop(files, buildSpeciesSubDirCountMap(files), params, dir, run)

		require.NoError(t, err)
		assert.Equal(t, stopTimeBudget, stats.StopReason)
		assert.True(t, stats.MoreWork)
	})
}

// TestDeletionRun_NoSpinOnProtectedRemainder runs each policy repeatedly over a
// tree whose remaining clips are all locked or blocked by the minimum-clips
// guard, with a zero run budget and batches of one so the earlier runs stop on
// the budget. Once nothing deletable is left a run must delete nothing and not
// ask for a follow-up, so the monitor cannot rescan in a loop.
func TestDeletionRun_NoSpinOnProtectedRemainder(t *testing.T) {
	t.Parallel()

	const (
		minClips = 2
		maxRuns  = 8
	)
	// Oldest first: a locked file sits between deletable ones.
	names := []string{
		"bubo_bubo_80p_20200101T000001Z", // deletable
		"strix_aluco_80p_20200101T000002Z",
		"bubo_bubo_80p_20200101T000003Z", // deletable
		"strix_aluco_80p_20200101T000004Z",
		"bubo_bubo_80p_20200101T000005Z", // blocked by the minimum once two are left
		"bubo_bubo_80p_20200101T000006Z",
	}
	lockedNames := []string{names[1] + ".wav", names[3] + ".wav"}

	policies := []string{"age", "usage"}
	for _, policy := range policies {
		for _, mutate := range []string{"none", "add newer file and remove a locked one"} {
			t.Run(policy+"/"+mutate, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				for _, n := range names {
					writeFileIn(t, dir, "2020/01/"+n+".wav", "a")
				}
				db := &MockDB{}

				var lastDeleted, lastRuns int
				var lastMore bool
				for runNo := 1; runNo <= maxRuns; runNo++ {
					if runNo == 4 && mutate != "none" {
						writeFileIn(t, dir, "2020/01/bubo_bubo_80p_"+time.Now().Format("20060102T150405Z")+".wav", "a")
						require.NoError(t, os.Remove(filepath.Join(dir, "2020", "01", lockedNames[1])))
					}
					files, err := GetAudioFiles(dir, allowedFileTypes, db)
					require.NoError(t, err)
					run, _ := newTestRun(t, func(e *drainTestEnv, r *deletionRun) {
						r.cfg.batchSize = 1
						r.cfg.runBudget = 0
						e.lockedClips = func(int) ([]string, error) { return lockedNames, nil }
					})
					var stats cleanupStats
					if policy == "age" {
						sort.SliceStable(files, func(i, j int) bool { return files[i].Timestamp.Before(files[j].Timestamp) })
						_, _, stats, err = processAgeBasedDeletionLoop(files, buildSpeciesTotalCountMap(files), minClips, false, run, drainCutoff())
					} else {
						counts := buildSpeciesSubDirCountMap(files)
						sortFilesForUsage(files, counts)
						_, _, _, stats, err = processUsageDeletionLoop(files, counts, newUsageLoopTestParams(1000, 500, 0, minClips), dir, run)
					}
					require.NoError(t, err)
					run.finish()
					lastDeleted, lastRuns, lastMore = stats.Deleted, runNo, stats.MoreWork
					if !stats.MoreWork && stats.Deleted == 0 {
						break
					}
				}

				assert.Equal(t, 0, lastDeleted, "the last run found nothing to delete")
				assert.False(t, lastMore, "no follow-up when nothing was deleted")
				assert.Less(t, lastRuns, maxRuns, "runs must settle")
			})
		}
	}
}

func TestNewRetentionSnapshot(t *testing.T) {
	t.Parallel()

	base := conf.RetentionSettings{Policy: "age", MaxAge: "30d", MaxUsage: "80%", MinClips: 10, KeepSpectrograms: false, CheckInterval: 15}
	start := newRetentionSnapshot("/clips", &base)

	t.Run("untracked fields do not matter", func(t *testing.T) {
		t.Parallel()
		changed := base
		changed.CheckInterval = 1
		changed.Debug = true
		assert.Equal(t, start, newRetentionSnapshot("/clips", &changed))
	})

	tracked := map[string]func(r *conf.RetentionSettings) string{
		"policy":           func(r *conf.RetentionSettings) string { r.Policy = "usage"; return "/clips" },
		"max age":          func(r *conf.RetentionSettings) string { r.MaxAge = "7d"; return "/clips" },
		"max usage":        func(r *conf.RetentionSettings) string { r.MaxUsage = "90%"; return "/clips" },
		"min clips":        func(r *conf.RetentionSettings) string { r.MinClips = 5; return "/clips" },
		"keep spectrogram": func(r *conf.RetentionSettings) string { r.KeepSpectrograms = true; return "/clips" },
		"export path":      func(*conf.RetentionSettings) string { return "/other" },
	}
	for name, mutate := range tracked {
		t.Run(name+" is tracked", func(t *testing.T) {
			t.Parallel()
			changed := base
			path := mutate(&changed)
			assert.NotEqual(t, start, newRetentionSnapshot(path, &changed))
		})
	}
}

// TestPrepareInitialCleanup_QuitDuringScanIsNotAnError replaces the global
// settings snapshot, so it is not parallel.
func TestPrepareInitialCleanup_QuitDuringScanIsNotAnError(t *testing.T) {
	baseDir := t.TempDir()
	settings := conftest.NewTestSettings().WithAudioExport(baseDir, "wav", "96k").Apply()
	settings.Realtime.Audio.Export.Retention.Policy = "age"
	t.Cleanup(func() { conftest.NewTestSettings().Apply() })
	writeFileIn(t, baseDir, "2020/01/bubo_bubo_80p_20200101T000001Z.wav", "a")

	open := make(chan struct{})
	files, _, _, proceed, _ := prepareInitialCleanup(open, &MockDB{})
	require.True(t, proceed, "control: without a quit the scan proceeds")
	require.Len(t, files, 1)

	closed := make(chan struct{})
	close(closed)
	var result CleanupResult
	files, _, _, proceed, result = prepareInitialCleanup(closed, &MockDB{})
	assert.False(t, proceed)
	assert.Empty(t, files)
	assert.NoError(t, result.Err, "a scan cancelled by shutdown is not an error")
}

func TestDeletionRun_SlowDeletionLengthensTheWait(t *testing.T) {
	t.Parallel()
	const latency = 200 * time.Millisecond

	t.Run("age", func(t *testing.T) {
		t.Parallel()
		files := makeDrainFiles(t, t.TempDir(), 1)
		// The fake clock advances by one tick per now call, so the two calls
		// around a deletion measure exactly one tick.
		run, env := newTestRun(t, func(e *drainTestEnv, _ *deletionRun) { e.tick = latency })

		runAge(files, run)

		require.Len(t, env.sleeps, 1)
		assert.Equal(t, slowDeletionBackoffFactor*latency, env.sleeps[0])
	})

	t.Run("usage", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		files := makeDrainFiles(t, dir, 1)
		run, env := newTestRun(t, func(e *drainTestEnv, _ *deletionRun) { e.tick = latency })

		_, _, _, _, err := processUsageDeletionLoop(files, buildSpeciesSubDirCountMap(files), newUsageLoopTestParams(1000, 500, 0, 0), dir, run)

		require.NoError(t, err)
		require.Len(t, env.sleeps, 1)
		assert.Equal(t, slowDeletionBackoffFactor*latency, env.sleeps[0])
	})
}

func TestDeletionRun_InterruptedBatchPauseStopsBeforeTheNextFile(t *testing.T) {
	t.Parallel()
	files := makeDrainFiles(t, t.TempDir(), 5)
	// Sleeps in order: attempt 1, attempt 2, batch pause. The pause reports an
	// interruption without closing quit, so only the pause's own result can
	// stop the run.
	run, env := newTestRun(t, func(e *drainTestEnv, _ *deletionRun) { e.failSleepAt = 3 })

	deleted, stats := runAge(files, run)

	assert.Equal(t, []string{files[0].Path, files[1].Path}, deleted)
	assert.Equal(t, stopQuit, stats.StopReason)
	assert.False(t, stats.MoreWork)
	assert.Equal(t, [][]string{{files[0].Path, files[1].Path}}, env.releases)
	assert.FileExists(t, files[2].Path, "nothing is deleted after the pause was interrupted")
}

func TestDeletionRun_SpentBudgetWithNothingDeletedAsksForNoFollowUp(t *testing.T) {
	t.Parallel()
	files := makeDrainFiles(t, t.TempDir(), 3)
	for i := range files {
		files[i].Locked = true
	}
	// A boundary is due at the first file (zero batch duration) and the budget is
	// already spent, with nothing deletable in the list.
	run, _ := newTestRun(t, func(_ *drainTestEnv, r *deletionRun) {
		r.cfg.maxBatchDuration = 0
		r.cfg.runBudget = 0
	})

	deleted, stats := runAge(files, run)

	assert.Empty(t, deleted)
	assert.Equal(t, stopTimeBudget, stats.StopReason)
	assert.False(t, stats.MoreWork, "a budget stop that deleted nothing must not trigger a rescan loop")
}

// TestNewDeletionRun_ReadsLiveSettingsAndLocks pins the production wiring of
// the closures. It replaces the global settings snapshot, so it is not parallel.
func TestNewDeletionRun_ReadsLiveSettingsAndLocks(t *testing.T) {
	baseDir := t.TempDir()
	settings := conftest.NewTestSettings().WithAudioExport(baseDir, "wav", "96k").Apply()
	settings.Realtime.Audio.Export.Retention.Policy = "age"
	t.Cleanup(func() { conftest.NewTestSettings().Apply() })

	start := newRetentionSnapshot(baseDir, &settings.Realtime.Audio.Export.Retention)
	db := &lockedDB{MockDB: &MockDB{}, locked: []string{"2020/01/locked.wav"}}
	run := newDeletionRun("age", make(chan struct{}), db, baseDir, false, &start)

	assert.False(t, run.settingsChanged(), "unchanged settings must not end the run")

	settings.Realtime.Audio.Export.Retention.MinClips++
	assert.True(t, run.settingsChanged(), "an edited tracked setting must end the run")

	locked, err := run.lockedClips()
	require.NoError(t, err)
	assert.Equal(t, []string{"2020/01/locked.wav"}, locked, "the lock list comes from the run's database")
}

// lockedDB is a MockDB that reports a fixed list of locked clip paths.
type lockedDB struct {
	*MockDB
	locked []string
}

func (d *lockedDB) GetLockedNotesClipPaths() ([]string, error) { return d.locked, nil }
