package processor

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	v2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/datastore/v2only"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	resetTestSpecies      = "blue jay"
	resetTestValidHours   = 24
	resetTestBaseValue    = 0.75
	resetTestLearnedValue = 0.7
	resetTestEventLimit   = 10
	resetTestWarnMarker   = "level=WARN"
	// resetTestRelearnTicks is the number of persistence ticks run after the drain.
	resetTestRelearnTicks   = 2
	resetTestPreviousLevel  = 0
	resetTestLearnedLevel   = 1
	resetTestExpectedEvents = 1
)

// newV2ThresholdTestStore opens a v2-only SQLite datastore with a dynamic
// threshold repository, the store a v2 installation runs the processor against.
func newV2ThresholdTestStore(t *testing.T) *v2only.Datastore {
	t.Helper()
	manager, err := v2.NewSQLiteManager(v2.Config{
		DirectPath: filepath.Join(t.TempDir(), "birdnet.db"),
	})
	require.NoError(t, err)
	require.NoError(t, manager.Initialize())

	db := manager.DB()
	ds, err := v2only.New(&v2only.Config{
		Manager:   manager,
		Detection: repository.NewDetectionRepository(db, nil, false, false),
		Label:     repository.NewLabelRepository(db, nil, false, false),
		Model:     repository.NewModelRepository(db, nil, false, false),
		Source:    repository.NewAudioSourceRepository(db, nil, false, false),
		Threshold: repository.NewDynamicThresholdRepository(db, nil, false, false),
	})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, ds.Close()) })
	return ds
}

// captureGlobalLog replaces the global logger with one writing to the returned
// buffer and restores the previous logger on cleanup. Callers must not run in
// parallel.
func captureGlobalLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	capture := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	cl, err := logger.NewCentralLogger(
		&logger.LoggingConfig{
			Console:       &logger.ConsoleOutput{Enabled: false},
			FileOutput:    &logger.FileOutput{Enabled: false},
			ModuleOutputs: logger.DisabledModuleOutputs(),
			DefaultLevel:  "debug",
		},
		capture,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cl.Close() })
	prev := logger.Global()
	logger.SetGlobal(cl)
	t.Cleanup(func() { logger.SetGlobal(prev) })
	return &buf
}

// TestResetDynamicThreshold_V2Store_DrainClearsResetAndRelearnPersists checks
// that on the v2 store the first drain empties pendingResets after a reset, logs
// no warning, and that a species re-learned afterwards keeps its row and events.
// Not parallel: it replaces the global logger.
func TestResetDynamicThreshold_V2Store_DrainClearsResetAndRelearnPersists(t *testing.T) {
	cases := []struct {
		name            string
		persistedBefore bool
	}{
		{name: "persisted before reset", persistedBefore: true},
		{name: "never persisted", persistedBefore: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := newV2ThresholdTestStore(t)
			p := createTestProcessor()
			p.Ds = ds

			now := time.Now()
			newEntry := func() *DynamicThreshold {
				return &DynamicThreshold{
					Level:         resetTestLearnedLevel,
					BaseThreshold: resetTestBaseValue,
					Timer:         now.Add(resetTestValidHours * time.Hour),
					ValidHours:    resetTestValidHours,
					FirstCreated:  now,
					LastTriggered: now,
				}
			}

			if tc.persistedBefore {
				p.DynamicThresholds[resetTestSpecies] = newEntry()
				require.NoError(t, p.persistDynamicThresholds(t.Context()))
				_, err := ds.GetDynamicThreshold(resetTestSpecies)
				require.NoError(t, err, "row must exist before the reset")
			}

			logBuf := captureGlobalLog(t)
			require.NoError(t, p.ResetDynamicThreshold(resetTestSpecies))
			require.NoError(t, p.persistDynamicThresholds(t.Context()))

			p.thresholdsMutex.RLock()
			pending := len(p.pendingResets)
			p.thresholdsMutex.RUnlock()
			assert.Zero(t, pending, "drain must clear the reset instead of requeueing it")

			_, err := ds.GetDynamicThreshold(resetTestSpecies)
			require.ErrorIs(t, err, repository.ErrDynamicThresholdNotFound, "reset must have removed the row")
			assert.NotContains(t, logBuf.String(), resetTestWarnMarker, "reset and drain must not warn")

			// Re-learn after the drain: the row and its events must persist.
			p.thresholdsMutex.Lock()
			p.DynamicThresholds[resetTestSpecies] = newEntry()
			p.thresholdsMutex.Unlock()
			require.NoError(t, ds.SaveThresholdEvent(&datastore.ThresholdEvent{
				SpeciesName:   resetTestSpecies,
				PreviousLevel: resetTestPreviousLevel,
				NewLevel:      resetTestLearnedLevel,
				PreviousValue: resetTestBaseValue,
				NewValue:      resetTestLearnedValue,
				ChangeReason:  changeReasonHighConfidence,
				CreatedAt:     now,
			}))

			for tick := 1; tick <= resetTestRelearnTicks; tick++ {
				require.NoError(t, p.persistDynamicThresholds(t.Context()))
				_, err := ds.GetDynamicThreshold(resetTestSpecies)
				require.NoError(t, err, "tick %d: re-learned row must exist", tick)
				events, err := ds.GetThresholdEvents(resetTestSpecies, resetTestEventLimit)
				require.NoError(t, err)
				assert.Len(t, events, resetTestExpectedEvents, "tick %d: re-learned events must survive", tick)
			}
			assert.NotContains(t, logBuf.String(), resetTestWarnMarker, "re-learn ticks must not warn")
		})
	}
}

// TestResetDynamicThreshold_V2Store_RelearnBeforeNextCycleKeepsRowAndEvents checks
// that a species reset and re-learned before the next persistence cycle keeps its
// row and its new events on every following cycle, for a single reset and for
// reset-all (two code paths).
func TestResetDynamicThreshold_V2Store_RelearnBeforeNextCycleKeepsRowAndEvents(t *testing.T) {
	cases := []struct {
		name  string
		reset func(t *testing.T, p *Processor)
	}{
		{
			name: "single reset",
			reset: func(t *testing.T, p *Processor) {
				t.Helper()
				require.NoError(t, p.ResetDynamicThreshold(resetTestSpecies))
			},
		},
		{
			name: "reset all",
			reset: func(t *testing.T, p *Processor) {
				t.Helper()
				_, err := p.ResetAllDynamicThresholds()
				require.NoError(t, err)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := newV2ThresholdTestStore(t)
			p := createTestProcessor()
			p.Ds = ds

			now := time.Now()
			newEntry := func() *DynamicThreshold {
				return &DynamicThreshold{
					Level:         resetTestLearnedLevel,
					BaseThreshold: resetTestBaseValue,
					Timer:         now.Add(resetTestValidHours * time.Hour),
					ValidHours:    resetTestValidHours,
					FirstCreated:  now,
					LastTriggered: now,
				}
			}

			p.DynamicThresholds[resetTestSpecies] = newEntry()
			require.NoError(t, p.persistDynamicThresholds(t.Context()))

			tc.reset(t, p)

			// Re-learn before the next cycle runs.
			p.thresholdsMutex.Lock()
			p.DynamicThresholds[resetTestSpecies] = newEntry()
			p.thresholdsMutex.Unlock()
			require.NoError(t, ds.SaveThresholdEvent(&datastore.ThresholdEvent{
				SpeciesName:   resetTestSpecies,
				PreviousLevel: resetTestPreviousLevel,
				NewLevel:      resetTestLearnedLevel,
				PreviousValue: resetTestBaseValue,
				NewValue:      resetTestLearnedValue,
				ChangeReason:  changeReasonHighConfidence,
				CreatedAt:     now,
			}))

			for tick := 1; tick <= resetTestRelearnTicks; tick++ {
				require.NoError(t, p.persistDynamicThresholds(t.Context()))
				_, err := ds.GetDynamicThreshold(resetTestSpecies)
				require.NoError(t, err, "tick %d: re-learned row must exist", tick)
				events, err := ds.GetThresholdEvents(resetTestSpecies, resetTestEventLimit)
				require.NoError(t, err)
				assert.Len(t, events, resetTestExpectedEvents, "tick %d: re-learned events must survive", tick)
			}
		})
	}
}
