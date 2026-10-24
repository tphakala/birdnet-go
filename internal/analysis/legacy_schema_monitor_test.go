package analysis

import (
	"slices"
	"sync"
	"sync/atomic"
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

func (r *tagRecorder) set(v string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tags = append(r.tags, v)
	return true
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
			// Never read while the monitor runs; a poll after stop would publish it.
			{state: entities.MigrationStatusDualWrite},
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

func TestLegacySchemaMonitor_RetriesTagWhileTelemetryIsOff(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		reader := &fakeStateReader{steps: []fakeStateStep{{state: entities.MigrationStatusIdle}}}
		var mu sync.Mutex
		var calls []string
		enabled := false
		m := newLegacySchemaMonitor(reader, time.Second, logger.Global().Module("test"))
		m.setTag = func(v string) bool {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, v)
			return enabled
		}

		m.start()
		time.Sleep(time.Second)
		synctest.Wait()
		mu.Lock()
		enabled = true // the user opts in later
		mu.Unlock()
		time.Sleep(time.Second)
		synctest.Wait()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		m.stop()

		mu.Lock()
		defer mu.Unlock()
		// Offered on start and the next tick (rejected), accepted on the third
		// evaluation, then not offered again.
		assert.Equal(t, []string{"idle", "idle", "idle"}, calls)
	})
}

func TestLegacySchemaMonitor_RetriesTagWhileReadsFail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		steps []fakeStateStep
		want  []string
	}{
		{
			name:  "reads fail from the start",
			steps: []fakeStateStep{{err: errors.NewStd("down")}},
			// Offered on start and the next tick (rejected), accepted on the
			// third evaluation, then not offered again.
			want: []string{"unavailable", "unavailable", "unavailable"},
		},
		{
			name:  "reads fail after one good read",
			steps: []fakeStateStep{{state: entities.MigrationStatusMigrating}, {err: errors.NewStd("down")}},
			want:  []string{"migrating", "migrating", "migrating"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				reader := &fakeStateReader{steps: tt.steps}
				var mu sync.Mutex
				var calls []string
				enabled := false
				m := newLegacySchemaMonitor(reader, time.Second, logger.Global().Module("test"))
				m.setTag = func(v string) bool {
					mu.Lock()
					defer mu.Unlock()
					calls = append(calls, v)
					return enabled
				}

				m.start()
				time.Sleep(time.Second)
				synctest.Wait()
				mu.Lock()
				enabled = true // the user opts in while reads keep failing
				mu.Unlock()
				time.Sleep(time.Second)
				synctest.Wait()
				time.Sleep(2 * time.Second)
				synctest.Wait()
				m.stop()

				mu.Lock()
				defer mu.Unlock()
				assert.Equal(t, tt.want, calls)
			})
		})
	}
}

// gatedStateReader returns idle for the first read and blocks every later read
// until release is closed, so a test can stop the monitor mid-read.
type gatedStateReader struct {
	reads   atomic.Int32
	release chan struct{}
}

func (g *gatedStateReader) GetState() (*entities.MigrationState, error) {
	if g.reads.Add(1) > 1 {
		<-g.release
	}
	return &entities.MigrationState{State: entities.MigrationStatusIdle}, nil
}

func TestLegacySchemaMonitor_StopWaitsForInFlightRead(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		reader := &gatedStateReader{release: make(chan struct{})}
		m := newLegacySchemaMonitor(reader, time.Second, logger.Global().Module("test"))
		m.setTag = (&tagRecorder{}).set

		m.start()
		time.Sleep(time.Second)
		synctest.Wait() // the second read is now blocked inside GetState
		require.EqualValues(t, 2, reader.reads.Load())

		var stopped atomic.Bool
		go func() {
			m.stop()
			stopped.Store(true)
		}()
		synctest.Wait()
		assert.False(t, stopped.Load(), "stop must wait for the in-flight read")

		close(reader.release)
		synctest.Wait()
		assert.True(t, stopped.Load(), "stop returns once the read finishes")
	})
}

func TestLegacySchemaMonitor_StopBeforeStart(t *testing.T) {
	t.Parallel()

	reader := &fakeStateReader{steps: []fakeStateStep{{state: entities.MigrationStatusIdle}}}
	m := newLegacySchemaMonitor(reader, time.Second, logger.Global().Module("test"))
	m.setTag = (&tagRecorder{}).set

	assert.NotPanics(t, func() {
		m.stop()
		m.stop()
	})
	assert.Zero(t, reader.pos, "a stopped monitor that never started reads nothing")
}
