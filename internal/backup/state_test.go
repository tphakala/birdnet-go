package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// concurrentTargetWriters is the number of goroutines updating distinct targets.
	concurrentTargetWriters = 16
	// concurrentMissedWriters is the number of goroutines recording missed backups.
	concurrentMissedWriters = 8
	// concurrentScheduleWriters is the number of goroutines per schedule type
	// updating schedule state.
	concurrentScheduleWriters = 4
	// concurrentReaders is the number of goroutines looping over the getters.
	concurrentReaders = 4

	// testTargetName is the target used by single-target tests.
	testTargetName = "local"
	// testMissedReason is the reason recorded by missed backup tests.
	testMissedReason = "test reason"
)

func TestStateManager_MutatorsDoNotDeadlockAndPersist(t *testing.T) {
	t.Parallel()

	nextRun := time.Date(2026, 6, 16, 3, 0, 0, 0, time.UTC)
	schedule := &BackupSchedule{Hour: 3, Weekday: dailySchedule, NextRun: nextRun}
	metadata := &Metadata{ID: "backup-1", Timestamp: nextRun, Size: 10}

	tests := []struct {
		name   string
		seed   func(sm *StateManager)
		mutate func(sm *StateManager) error
		verify func(t *testing.T, sm *StateManager)
	}{
		{
			name:   "UpdateScheduleState",
			mutate: func(sm *StateManager) error { return sm.UpdateScheduleState(schedule, false) },
			verify: func(t *testing.T, sm *StateManager) {
				t.Helper()
				got := sm.GetScheduleState(schedule)
				assert.Equal(t, 2, got.FailureCount)
				assert.True(t, got.NextScheduled.Equal(nextRun), "NextScheduled = %v, want %v", got.NextScheduled, nextRun)
			},
		},
		{
			name: "AddMissedBackup",
			mutate: func(sm *StateManager) error {
				return sm.AddMissedBackup(schedule, testMissedReason)
			},
			verify: func(t *testing.T, sm *StateManager) {
				t.Helper()
				missed := sm.GetMissedBackups()
				require.Len(t, missed, 2)
				for _, m := range missed {
					assert.Equal(t, testMissedReason, m.Reason)
				}
			},
		},
		{
			name:   "UpdateTargetState",
			mutate: func(sm *StateManager) error { return sm.UpdateTargetState(testTargetName, metadata, "success") },
			verify: func(t *testing.T, sm *StateManager) {
				t.Helper()
				got := sm.GetTargetState(testTargetName)
				assert.Equal(t, metadata.ID, got.LastBackupID)
				assert.Equal(t, 2, got.TotalBackups)
			},
		},
		{
			name: "UpdateStats",
			mutate: func(sm *StateManager) error {
				return sm.UpdateStats(map[string]BackupStats{testTargetName: {TotalBackups: 7}})
			},
			verify: func(t *testing.T, sm *StateManager) {
				t.Helper()
				assert.Equal(t, 7, sm.GetStats()[testTargetName].TotalBackups)
			},
		},
		{
			name: "ClearMissedBackups",
			seed: func(sm *StateManager) {
				sm.state.MissedRuns = append(sm.state.MissedRuns, MissedBackup{Reason: testMissedReason})
			},
			mutate: func(sm *StateManager) error { return sm.ClearMissedBackups() },
			verify: func(t *testing.T, sm *StateManager) {
				t.Helper()
				assert.Empty(t, sm.GetMissedBackups())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sm, path := newTestStateManager(t)
			if tt.seed != nil {
				tt.seed(sm)
			}

			// Twice: the second call needs the write lock, so it also catches
			// a read lock leaked by the first call's save.
			for call := range 2 {
				var err error
				requireReturns(t, func() { err = tt.mutate(sm) }, fmt.Sprintf("mutator call %d did not return", call+1))
				require.NoError(t, err)
			}

			requireReturns(t, func() {
				_ = sm.GetScheduleState(schedule)
				_ = sm.GetTargetState(testTargetName)
				_ = sm.GetMissedBackups()
				_ = sm.GetStats()
			}, "getters did not return after the mutator")

			tt.verify(t, newStateManagerAt(path))
		})
	}
}

func TestStateManager_PanicInMutationReleasesLock(t *testing.T) {
	t.Parallel()
	sm, _ := newTestStateManager(t)

	var recovered any
	requireReturns(t, func() {
		defer func() { recovered = recover() }()
		_ = sm.update(func(*BackupState) { panic("mutation failed") })
	}, "update did not return after a panicking mutation")
	require.NotNil(t, recovered)

	var err error
	requireReturns(t, func() {
		_ = sm.GetMissedBackups()
		err = sm.ClearMissedBackups()
	}, "state lock was left held after a panicking mutation")
	require.NoError(t, err)
}

func TestStateManager_FailedSaveReturnsErrorAndReleasesLocks(t *testing.T) {
	t.Parallel()

	// The state file's parent is a regular file, so creating the directory fails.
	blocker := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))
	sm := newStateManagerAt(filepath.Join(blocker, stateFileName))
	metadata := &Metadata{ID: "backup-1"}

	// Twice: the second call needs saveMu, which the first failed save must release.
	for call := range 2 {
		var err error
		requireReturns(t, func() { err = sm.UpdateTargetState(testTargetName, metadata, "success") },
			fmt.Sprintf("UpdateTargetState call %d did not return after a failed save", call+1))
		require.Error(t, err)
	}

	// The mutation stays in memory.
	assert.Equal(t, 2, sm.GetTargetState(testTargetName).TotalBackups)
}

func TestStateManager_UpdateStatsDoesNotAliasCallerMap(t *testing.T) {
	t.Parallel()
	sm, _ := newTestStateManager(t)

	stats := map[string]BackupStats{testTargetName: {TotalBackups: 1}}
	var err error
	requireReturns(t, func() { err = sm.UpdateStats(stats) }, "UpdateStats did not return")
	require.NoError(t, err)

	stats[testTargetName] = BackupStats{TotalBackups: 99}
	assert.Equal(t, 1, sm.GetStats()[testTargetName].TotalBackups)
}

func TestStateManager_ConcurrentMutationsAllPersisted(t *testing.T) {
	t.Parallel()
	sm, path := newTestStateManager(t)

	daily := &BackupSchedule{Weekday: dailySchedule, NextRun: time.Date(2026, 6, 16, 3, 0, 0, 0, time.UTC)}
	weekly := &BackupSchedule{Weekday: time.Sunday, IsWeekly: true, NextRun: time.Date(2026, 6, 21, 3, 0, 0, 0, time.UTC)}

	// One result per writer: targets, missed, two schedule writers per slot, one stats writer.
	total := concurrentTargetWriters + concurrentMissedWriters + 2*concurrentScheduleWriters + 1
	errCh := make(chan error, total)
	stop := make(chan struct{})

	requireReturns(t, func() {
		var writers, readers sync.WaitGroup
		for i := range concurrentTargetWriters {
			writers.Go(func() {
				name := fmt.Sprintf("target-%d", i)
				errCh <- sm.UpdateTargetState(name, &Metadata{ID: name, Size: 1}, "success")
			})
		}
		for range concurrentMissedWriters {
			writers.Go(func() { errCh <- sm.AddMissedBackup(daily, testMissedReason) })
		}
		for range concurrentScheduleWriters {
			writers.Go(func() { errCh <- sm.UpdateScheduleState(daily, true) })
			writers.Go(func() { errCh <- sm.UpdateScheduleState(weekly, false) })
		}
		for range concurrentReaders {
			readers.Go(func() {
				for {
					select {
					case <-stop:
						return
					default:
					}
					_ = sm.GetScheduleState(daily)
					_ = sm.GetTargetState(testTargetName)
					_ = sm.GetMissedBackups()
					_ = sm.GetStats()
				}
			})
		}
		// The stats writer joins the writer group so the stats map is written too.
		writers.Go(func() {
			errCh <- sm.UpdateStats(map[string]BackupStats{testTargetName: {TotalBackups: 1}})
		})
		writers.Wait()
		close(stop)
		readers.Wait()
	}, "concurrent mutations did not finish")
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}

	reloaded := newStateManagerAt(path)
	for i := range concurrentTargetWriters {
		name := fmt.Sprintf("target-%d", i)
		assert.Equal(t, name, reloaded.GetTargetState(name).LastBackupID, "target %s missing from the saved file", name)
	}
	assert.Len(t, reloaded.GetMissedBackups(), concurrentMissedWriters)

	dailyState := reloaded.GetScheduleState(daily)
	assert.Zero(t, dailyState.FailureCount)
	assert.False(t, dailyState.LastSuccessful.IsZero(), "daily schedule success missing from the saved file")
	assert.Equal(t, concurrentScheduleWriters, reloaded.GetScheduleState(weekly).FailureCount)
	assert.Equal(t, 1, reloaded.GetStats()[testTargetName].TotalBackups)
}
