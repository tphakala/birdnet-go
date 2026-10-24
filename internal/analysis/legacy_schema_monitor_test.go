package analysis

import (
	"bytes"
	"encoding/json"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/migration"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
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
		m := newLegacySchemaMonitor(reader, time.Second, logger.Global().Module("test"), false)
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
		m := newLegacySchemaMonitor(reader, time.Second, logger.Global().Module("test"), false)
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
		m := newLegacySchemaMonitor(reader, time.Second, logger.Global().Module("test"), false)
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
				m := newLegacySchemaMonitor(reader, time.Second, logger.Global().Module("test"), false)
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
		m := newLegacySchemaMonitor(reader, time.Second, logger.Global().Module("test"), false)
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
	m := newLegacySchemaMonitor(reader, time.Second, logger.Global().Module("test"), false)
	m.setTag = (&tagRecorder{}).set

	assert.NotPanics(t, func() {
		m.stop()
		m.stop()
	})
	assert.Zero(t, reader.pos, "a stopped monitor that never started reads nothing")
}

func TestLegacySchemaMonitor_StartAfterStopReadsNothing(t *testing.T) {
	t.Parallel()

	reader := &fakeStateReader{steps: []fakeStateStep{{state: entities.MigrationStatusIdle}}}
	rec := &tagRecorder{}
	m := newLegacySchemaMonitor(reader, time.Second, logger.Global().Module("test"), false)
	m.setTag = rec.set

	m.stop()
	m.start()
	m.stop()

	assert.Zero(t, reader.pos, "a stopped monitor must not read the database")
	assert.Empty(t, rec.snapshot(), "a stopped monitor must not publish a tag")
}

func TestLegacySchemaMonitor_SecondStartIsIgnored(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		reader := &fakeStateReader{steps: []fakeStateStep{{state: entities.MigrationStatusIdle}}}
		m := newLegacySchemaMonitor(reader, time.Second, logger.Global().Module("test"), false)
		m.setTag = (&tagRecorder{}).set

		m.start()
		m.start()
		assert.Equal(t, 1, reader.pos, "only the first start reads")

		assert.NotPanics(t, m.stop, "a second start must not add a second poll goroutine")
	})
}

// wantLegacyCreate describes one expected bell notice.
type wantLegacyCreate struct {
	kind        migration.LegacyNoticeKind
	priority    notification.Priority
	title       string
	messagePart string // a fragment that tells the kinds' English messages apart
	titleKey    string // literal key as spelled in en.json
	messageKey  string
}

func TestLegacySchemaMonitor_BellNoticeAcrossTransitions(t *testing.T) {
	t.Parallel()

	idle := fakeStateStep{state: entities.MigrationStatusIdle}
	step := func(s entities.MigrationStatus) fakeStateStep { return fakeStateStep{state: s} }
	notStarted := wantLegacyCreate{
		kind: migration.LegacyNoticeNotStarted, priority: notification.PriorityMedium,
		title: "Database upgrade required", messagePart: "Open System > Database to run the migration",
		titleKey: "notifications.content.migration.legacyNotStartedTitle", messageKey: "notifications.content.migration.legacyNotStartedMessage",
	}
	inProgress := wantLegacyCreate{
		kind: migration.LegacyNoticeInProgress, priority: notification.PriorityMedium,
		title: "Database migration in progress", messagePart: "Keep BirdNET-Go running",
		titleKey: "notifications.content.migration.legacyInProgressTitle", messageKey: "notifications.content.migration.legacyInProgressMessage",
	}
	needsAttention := wantLegacyCreate{
		kind: migration.LegacyNoticeNeedsAttention, priority: notification.PriorityHigh,
		title: "Database migration needs attention", messagePart: "paused or has failed",
		titleKey: "notifications.content.migration.legacyNeedsAttentionTitle", messageKey: "notifications.content.migration.legacyNeedsAttentionMessage",
	}
	restart := wantLegacyCreate{
		kind: migration.LegacyNoticeRestartRequired, priority: notification.PriorityMedium,
		title: "Restart to finish the database migration", messagePart: "Restart BirdNET-Go to switch",
		titleKey: "notifications.content.migration.legacyRestartRequiredTitle", messageKey: "notifications.content.migration.legacyRestartRequiredMessage",
	}

	tests := []struct {
		name        string
		steps       []fakeStateStep
		want        []wantLegacyCreate
		wantDeletes int
	}{
		{
			name:  "a repeated state raises once and never re-raises",
			steps: []fakeStateStep{idle, idle, idle},
			want:  []wantLegacyCreate{notStarted},
		},
		{
			name:        "moves within one kind are no-ops",
			steps:       []fakeStateStep{idle, step(entities.MigrationStatusDualWrite), step(entities.MigrationStatusMigrating), step(entities.MigrationStatusValidating)},
			want:        []wantLegacyCreate{notStarted, inProgress},
			wantDeletes: 1,
		},
		{
			name:        "pause raises a high priority notice and resume replaces it",
			steps:       []fakeStateStep{step(entities.MigrationStatusDualWrite), step(entities.MigrationStatusPaused), step(entities.MigrationStatusDualWrite)},
			want:        []wantLegacyCreate{inProgress, needsAttention, inProgress},
			wantDeletes: 2,
		},
		{
			name:  "a transient read error keeps the notice",
			steps: []fakeStateStep{step(entities.MigrationStatusMigrating), {err: errors.NewStd("transient")}, step(entities.MigrationStatusMigrating)},
			want:  []wantLegacyCreate{inProgress},
		},
		{
			name:        "completion asks for a restart and a rollback returns to not started",
			steps:       []fakeStateStep{step(entities.MigrationStatusValidating), step(entities.MigrationStatusCompleted), idle},
			want:        []wantLegacyCreate{inProgress, restart, notStarted},
			wantDeletes: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				rec := newCadenceNoticeRecorder(t, len(tt.want), tt.wantDeletes)
				m := newLegacySchemaMonitor(&fakeStateReader{steps: tt.steps}, time.Second, logger.Global().Module("test"), true)
				m.setTag = (&tagRecorder{}).set
				m.service = func() notification.NoticeService { return rec }

				m.start()
				for range len(tt.steps) - 1 {
					time.Sleep(time.Second)
					synctest.Wait()
				}
				m.stop()

				require.Len(t, rec.created, len(tt.want))
				for i, w := range tt.want {
					n := rec.created[i]
					assert.Equal(t, notification.TypeWarning, n.Type)
					assert.Equal(t, w.priority, n.Priority, "create %d", i)
					assert.Equal(t, notification.ComponentDatabase, n.Component)
					assert.Equal(t, notification.DeliveryTargetBell, n.DeliveryTarget)
					assert.Equal(t, w.titleKey, n.TitleKey, "create %d", i)
					assert.Equal(t, w.messageKey, n.MessageKey, "create %d", i)
					assert.Equal(t, w.title, n.Title, "create %d", i)
					assert.Contains(t, n.Message, w.messagePart, "create %d", i)
				}
			})
		})
	}
}

func TestLegacySchemaMonitor_FailedCreateIsRetriedByTheNextTick(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		rec := newCadenceNoticeRecorder(t, 2, 0)
		rec.setCreateErr(errors.NewStd("rate limited"))
		m := newLegacySchemaMonitor(&fakeStateReader{steps: []fakeStateStep{{state: entities.MigrationStatusIdle}}}, time.Second, logger.Global().Module("test"), true)
		m.setTag = (&tagRecorder{}).set
		m.service = func() notification.NoticeService { return rec }

		m.start() // the create fails
		rec.setCreateErr(nil)
		time.Sleep(time.Second)
		synctest.Wait()
		time.Sleep(2 * time.Second) // no further create once raised
		synctest.Wait()
		m.stop()

		require.Len(t, rec.created, 1)
		assert.Equal(t, notification.MsgMigrationLegacyNotStartedTitle, rec.created[0].TitleKey)
	})
}

func TestLegacySchemaMonitor_NoNoticeBeforeTheFirstSuccessfulRead(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		rec := newCadenceNoticeRecorder(t, 1, 0)
		reader := &fakeStateReader{steps: []fakeStateStep{
			{err: errors.NewStd("unreadable")},
			{err: errors.NewStd("unreadable")},
			{state: entities.MigrationStatusIdle},
		}}
		m := newLegacySchemaMonitor(reader, time.Second, logger.Global().Module("test"), true)
		m.setTag = (&tagRecorder{}).set
		m.service = func() notification.NoticeService { return rec }

		m.start() // first read fails
		time.Sleep(time.Second)
		synctest.Wait() // second read fails
		created, deleted := rec.counts()
		assert.Zero(t, created, "an unreadable state raises no notice")
		assert.Zero(t, deleted)

		time.Sleep(time.Second)
		synctest.Wait() // third read succeeds
		m.stop()

		created, deleted = rec.counts()
		assert.Equal(t, 1, created, "the first good read raises the notice")
		assert.Zero(t, deleted)
	})
}

func TestLegacySchemaMonitor_NoNoticeOnV2Schema(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		rec := newCadenceNoticeRecorder(t, 0, 0)
		m := newLegacySchemaMonitor(&fakeStateReader{steps: []fakeStateStep{{state: entities.MigrationStatusIdle}}}, time.Second, logger.Global().Module("test"), false)
		m.setTag = (&tagRecorder{}).set
		m.service = func() notification.NoticeService { return rec }

		m.start()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		m.stop()
	})
}

func TestLegacySchemaMonitor_StartupWarnOnce(t *testing.T) {
	t.Parallel()

	warnRecords := func(t *testing.T, legacy bool) []map[string]any {
		t.Helper()
		var buf bytes.Buffer
		var records []map[string]any
		synctest.Test(t, func(t *testing.T) {
			log := logger.NewSlogLogger(&buf, logger.LogLevelInfo, time.UTC)
			m := newLegacySchemaMonitor(&fakeStateReader{steps: []fakeStateStep{{state: entities.MigrationStatusIdle}}}, time.Second, log, legacy)
			m.setTag = (&tagRecorder{}).set
			m.service = func() notification.NoticeService { return nil }

			m.start()
			time.Sleep(3 * time.Second)
			synctest.Wait()
			m.stop()
		})
		for line := range bytes.Lines(bytes.TrimSpace(buf.Bytes())) {
			var rec map[string]any
			require.NoError(t, json.Unmarshal(line, &rec))
			if rec["level"] == "WARN" {
				records = append(records, rec)
			}
		}
		return records
	}

	t.Run("legacy schema warns once with the notice kind", func(t *testing.T) {
		t.Parallel()
		records := warnRecords(t, true)
		require.Len(t, records, 1)
		assert.Equal(t, string(migration.LegacyNoticeNotStarted), records[0]["notice_kind"])
		assert.Equal(t, "idle", records[0]["migration_state"])
		assert.Equal(t, "legacy_schema_check", records[0]["operation"])
	})
	t.Run("v2 schema logs no warning", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, warnRecords(t, false))
	})
}
