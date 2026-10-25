package analysis

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/migration"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// fakeMetadataStore is an in-memory appMetadataStore.
type fakeMetadataStore struct {
	mu     sync.Mutex
	values map[string]string
	getErr error
	setErr error
	sets   int
}

func newFakeMetadataStore(initial string) *fakeMetadataStore {
	s := &fakeMetadataStore{values: map[string]string{}}
	if initial != "" {
		s.values[legacyReminderMetadataKey] = initial
	}
	return s
}

func (s *fakeMetadataStore) Get(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return "", s.getErr
	}
	return s.values[key], nil
}

func (s *fakeMetadataStore) Set(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setErr != nil {
		return s.setErr
	}
	s.values[key] = value
	s.sets++
	return nil
}

func (s *fakeMetadataStore) stored() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[legacyReminderMetadataKey]
}

func (s *fakeMetadataStore) setCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sets
}

func (s *fakeMetadataStore) setErrors(get, set error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getErr, s.setErr = get, set
}

// pushNoticeService records every created notification.
type pushNoticeService struct {
	mu        sync.Mutex
	created   []*notification.Notification
	createErr error
}

func (p *pushNoticeService) CreateWithMetadata(n *notification.Notification) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.createErr != nil {
		return p.createErr
	}
	p.created = append(p.created, n)
	return nil
}

func (p *pushNoticeService) Delete(string) error { return nil }

func (p *pushNoticeService) setCreateErr(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.createErr = err
}

// pushed returns the notifications created with the push delivery target.
func (p *pushNoticeService) pushed() []*notification.Notification {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []*notification.Notification
	for _, n := range p.created {
		if n.DeliveryTarget == notification.DeliveryTargetPush {
			out = append(out, n)
		}
	}
	return out
}

type reminderPush int

const (
	pushOff reminderPush = iota
	pushProvidersOff
	pushOn
)

func reminderSettings(p reminderPush) *conf.Settings {
	s := &conf.Settings{}
	s.Notification.Push.Enabled = p != pushOff
	s.Notification.Push.Providers = []conf.PushProviderConfig{{Enabled: p == pushOn}}
	return s
}

const testReminderPollInterval = 30 * time.Second

var reminderT0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func TestLegacyReminder_DueAcrossRunsAndRestarts(t *testing.T) {
	t.Parallel()

	const (
		day   = 24 * time.Hour
		delay = legacyReminderStartupDelay
		tick  = testReminderPollInterval
	)
	type step struct {
		advance     time.Duration
		push        reminderPush
		kind        migration.LegacyNoticeKind
		createFails bool
		wantPushes  int // cumulative
	}
	on := func(advance time.Duration, want int) step {
		return step{advance: advance, push: pushOn, kind: migration.LegacyNoticeNotStarted, wantPushes: want}
	}
	stamp := func(d time.Duration) string { return reminderT0.Add(d).Format(time.RFC3339) }

	tests := []struct {
		name       string
		stored     string
		setFails   bool
		buildDate  string
		steps      []step
		wantSets   int
		wantStored func(t *testing.T, stored string)
	}{
		{
			name:     "baseline: one after the startup delay, the next a week later",
			steps:    []step{on(0, 0), on(delay-time.Minute, 0), on(time.Minute, 1), on(6*day, 1), on(day, 2)},
			wantSets: 2,
		},
		{
			name:     "restart within the interval does not resend",
			stored:   stamp(-day),
			steps:    []step{on(0, 0), on(delay, 0), on(5*day, 0), on(day, 1)},
			wantSets: 1,
		},
		{
			name:     "overdue reminder is sent after the startup delay",
			stored:   stamp(-8 * day),
			steps:    []step{on(0, 0), on(delay-time.Minute, 0), on(time.Minute, 1)},
			wantSets: 1,
		},
		{
			name:   "garbage value is replaced by the real time",
			stored: "not a time",
			steps:  []step{on(0, 0), on(delay, 1)},
			wantStored: func(t *testing.T, stored string) {
				t.Helper()
				assert.Equal(t, stamp(delay), stored)
			},
			wantSets: 1,
		},
		{
			name:      "clock behind the build date sends and stores nothing until it catches up",
			stored:    stamp(-8 * day), // overdue by the clock, which cannot be trusted yet
			buildDate: stamp(10 * day),
			steps:     []step{on(0, 0), on(delay, 0), on(day, 0), on(9*day, 1), on(6*day, 1), on(day, 2)},
			wantSets:  2,
		},
		{
			name:      "stored time in the future is never due",
			stored:    stamp(30 * day),
			buildDate: stamp(-day),
			steps:     []step{on(0, 0), on(delay, 0), on(20*day, 0)},
			wantSets:  0,
		},
		{
			name: "hot reload: nothing while push is off, one tick of readiness once on",
			steps: []step{
				{advance: 0, push: pushOff, kind: migration.LegacyNoticeNotStarted},
				{advance: delay + time.Hour, push: pushOff, kind: migration.LegacyNoticeNotStarted},
				on(tick, 0),
				on(tick, 1),
			},
			wantSets: 1,
		},
		{
			name: "providers all off",
			steps: []step{
				{advance: 0, push: pushProvidersOff, kind: migration.LegacyNoticeNotStarted},
				{advance: delay + time.Hour, push: pushProvidersOff, kind: migration.LegacyNoticeNotStarted},
			},
		},
		{
			name: "only not_started and needs_attention push",
			steps: []step{
				{advance: 0, push: pushOn, kind: migration.LegacyNoticeInProgress},
				{advance: delay, push: pushOn, kind: migration.LegacyNoticeRestartRequired},
				{advance: tick, push: pushOn, kind: migration.LegacyNoticeNone},
				{advance: tick, push: pushOn, kind: migration.LegacyNoticeNeedsAttention, wantPushes: 1},
			},
			wantSets: 1,
		},
		{
			name:     "a failed write does not cause a resend within the interval",
			setFails: true,
			steps:    []step{on(0, 0), on(delay, 1), on(time.Hour, 1), on(day, 1), on(7*day, 2)},
			wantSets: 0, // every Set fails in this row
		},
		{
			name: "a failed create is retried on the next tick",
			steps: []step{
				on(0, 0),
				{advance: delay, push: pushOn, kind: migration.LegacyNoticeNotStarted, createFails: true},
				on(tick, 1),
				on(tick, 1),
			},
			wantSets: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeMetadataStore(tt.stored)
			if tt.setFails {
				store.setErrors(nil, errors.NewStd("disk full"))
			}
			svc := &pushNoticeService{}
			now := reminderT0
			push := pushOn
			r := newLegacyReminder(store, func() *conf.Settings { return reminderSettings(push) },
				tt.buildDate, testReminderPollInterval, logger.Global().Module("test"))
			r.now = func() time.Time { return now }
			if tt.buildDate != "" {
				require.False(t, r.buildDate.IsZero())
			}

			for i, s := range tt.steps {
				now = now.Add(s.advance)
				push = s.push
				if s.createFails {
					svc.setCreateErr(errors.NewStd("rate limit exceeded"))
				} else {
					svc.setCreateErr(nil)
				}
				r.check(s.kind, svc)
				require.Len(t, svc.pushed(), s.wantPushes, "step %d", i)
			}
			assert.Equal(t, tt.wantSets, store.setCount())
			if tt.wantStored != nil {
				tt.wantStored(t, store.stored())
			}
		})
	}
}

func TestLegacyReminder_StoreReadFailureSendsNothingUntilItRecovers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		storedAge  time.Duration
		wantPushes int
	}{
		{"recent stored time is honored after recovery", time.Hour, 0},
		{"overdue reminder goes out after recovery", 8 * 24 * time.Hour, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeMetadataStore(reminderT0.Add(-tt.storedAge).Format(time.RFC3339))
			store.setErrors(errors.NewStd("db busy"), nil)
			svc := &pushNoticeService{}
			now := reminderT0
			r := newLegacyReminder(store, func() *conf.Settings { return reminderSettings(pushOn) }, "",
				testReminderPollInterval, logger.Global().Module("test"))
			r.now = func() time.Time { return now }

			r.check(migration.LegacyNoticeNotStarted, svc)
			now = now.Add(legacyReminderStartupDelay)
			r.check(migration.LegacyNoticeNotStarted, svc)
			require.Empty(t, svc.pushed(), "an unreadable last-sent time must not cause a send")

			store.setErrors(nil, nil)
			now = now.Add(testReminderPollInterval)
			r.check(migration.LegacyNoticeNotStarted, svc)
			assert.Len(t, svc.pushed(), tt.wantPushes)
		})
	}
}

func TestLegacyReminder_NilServiceSendsAndRecordsNothing(t *testing.T) {
	t.Parallel()

	store := newFakeMetadataStore("")
	now := reminderT0
	r := newLegacyReminder(store, func() *conf.Settings { return reminderSettings(pushOn) }, "",
		testReminderPollInterval, logger.Global().Module("test"))
	r.now = func() time.Time { return now }

	r.check(migration.LegacyNoticeNotStarted, nil)
	now = now.Add(legacyReminderStartupDelay)
	assert.NotPanics(t, func() { r.check(migration.LegacyNoticeNotStarted, nil) })
	assert.Zero(t, store.setCount())

	svc := &pushNoticeService{}
	r.check(migration.LegacyNoticeNotStarted, svc)
	assert.Len(t, svc.pushed(), 1, "the service coming up later still gets the reminder")
}

func TestLegacyReminder_NotificationShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind     migration.LegacyNoticeKind
		priority notification.Priority
	}{
		{migration.LegacyNoticeNotStarted, notification.PriorityMedium},
		{migration.LegacyNoticeNeedsAttention, notification.PriorityHigh},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			t.Parallel()

			svc := &pushNoticeService{}
			now := reminderT0
			r := newLegacyReminder(newFakeMetadataStore(""), func() *conf.Settings { return reminderSettings(pushOn) }, "",
				testReminderPollInterval, logger.Global().Module("test"))
			r.now = func() time.Time { return now }
			r.check(tt.kind, svc)
			now = now.Add(legacyReminderStartupDelay)
			r.check(tt.kind, svc)

			got := svc.pushed()
			require.Len(t, got, 1)
			want := legacyNoticeKinds[tt.kind]
			n := got[0]
			assert.Equal(t, notification.TypeWarning, n.Type)
			assert.Equal(t, tt.priority, n.Priority)
			assert.Equal(t, notification.ComponentDatabase, n.Component)
			assert.Equal(t, notification.DeliveryTargetPush, n.DeliveryTarget)
			assert.Equal(t, want.title, n.Title)
			assert.Equal(t, want.message, n.Message)
			assert.Equal(t, want.titleKey, n.TitleKey)
			assert.Equal(t, want.messageKey, n.MessageKey)
		})
	}
}

func TestLegacySchemaMonitor_ReminderRunsOnEveryTick(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		// A read error after the first good read takes evaluate's early return;
		// the reminder must still run on the kept kind.
		reader := &fakeStateReader{steps: []fakeStateStep{
			{state: entities.MigrationStatusIdle},
			{err: errors.NewStd("transient")},
		}}
		store := newFakeMetadataStore("")
		svc := &pushNoticeService{}
		m := newLegacySchemaMonitor(reader, time.Second, logger.Global().Module("test"), true)
		m.setTag = (&tagRecorder{}).set
		m.service = func() notification.NoticeService { return svc }
		m.setReminder(newLegacyReminder(store, func() *conf.Settings { return reminderSettings(pushOn) }, "",
			m.interval, logger.Global().Module("test")))

		m.start()
		time.Sleep(legacyReminderStartupDelay + time.Minute)
		synctest.Wait()
		m.stop()

		require.Len(t, svc.pushed(), 1)
		assert.NotEmpty(t, store.stored())
		assert.Equal(t, 1, store.setCount())
	})
}
