package analysis

import (
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// fakeStateReader replays a scripted sequence of GetState results and repeats
// the last one when the script runs out.
type fakeStateReader struct {
	mu    sync.Mutex
	steps []fakeStateStep
	pos   int
}

type fakeStateStep struct {
	state entities.MigrationStatus
	err   error
}

func (f *fakeStateReader) GetState() (*entities.MigrationState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	step := f.steps[min(f.pos, len(f.steps)-1)]
	f.pos++
	if step.err != nil {
		return nil, step.err
	}
	return &entities.MigrationState{State: step.state}, nil
}

// tagRecorder collects the values passed to the monitor's setTag.
type tagRecorder struct {
	mu   sync.Mutex
	tags []string
}

func (r *tagRecorder) set(v string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tags = append(r.tags, v)
}

func (r *tagRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.tags)
}

func TestMigrationStateTagValue(t *testing.T) {
	t.Parallel()

	known := []entities.MigrationStatus{
		entities.MigrationStatusIdle,
		entities.MigrationStatusInitializing,
		entities.MigrationStatusDualWrite,
		entities.MigrationStatusMigrating,
		entities.MigrationStatusValidating,
		entities.MigrationStatusCutover,
		entities.MigrationStatusCompleted,
		entities.MigrationStatusFailed,
		entities.MigrationStatusPaused,
	}
	for _, status := range known {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			got := migrationStateTagValue(&entities.MigrationState{State: status}, nil)
			assert.Equal(t, string(status), got)
		})
	}

	t.Run("unexpected status", func(t *testing.T) {
		t.Parallel()
		got := migrationStateTagValue(&entities.MigrationState{State: "/home/user/secret"}, nil)
		assert.Equal(t, migrationTagUnknown, got)
	})
	t.Run("read error", func(t *testing.T) {
		t.Parallel()
		got := migrationStateTagValue(nil, errors.NewStd("db closed"))
		assert.Equal(t, migrationTagUnavailable, got)
	})
	t.Run("nil state", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, migrationTagUnavailable, migrationStateTagValue(nil, nil))
	})
}

func TestLegacySchemaMonitor_TagFollowsStateAcrossTicks(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		reader := &fakeStateReader{steps: []fakeStateStep{
			{state: entities.MigrationStatusIdle},
			{state: entities.MigrationStatusIdle},
			{state: entities.MigrationStatusMigrating},
			{err: errors.NewStd("transient")},
			{state: entities.MigrationStatusFailed},
		}}
		rec := &tagRecorder{}
		m := newLegacySchemaMonitor(reader, time.Second, logger.Global().Module("test"))
		m.setTag = rec.set

		m.start()
		// start evaluates once synchronously: idle.
		require.Equal(t, []string{"idle"}, rec.snapshot())

		for range 4 {
			time.Sleep(time.Second)
			synctest.Wait()
		}
		m.stop()

		// Ticks: idle (unchanged), migrating, error (kept), failed.
		assert.Equal(t, []string{"idle", "migrating", "failed"}, rec.snapshot())

		time.Sleep(3 * time.Second)
		synctest.Wait()
		assert.Len(t, rec.snapshot(), 3, "no tag update after stop")

		m.stop() // idempotent
	})
}

func TestLegacySchemaMonitor_FirstReadFailureReportsUnavailable(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		reader := &fakeStateReader{steps: []fakeStateStep{
			{err: errors.NewStd("down")},
			{state: entities.MigrationStatusDualWrite},
		}}
		rec := &tagRecorder{}
		m := newLegacySchemaMonitor(reader, time.Second, logger.Global().Module("test"))
		m.setTag = rec.set

		m.start()
		time.Sleep(time.Second)
		synctest.Wait()
		m.stop()

		assert.Equal(t, []string{"unavailable", "dual_write"}, rec.snapshot())
	})
}
