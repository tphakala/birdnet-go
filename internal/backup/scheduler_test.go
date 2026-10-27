package backup

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/testutil"
)

const (
	zoneNewYork  = "America/New_York"
	zoneHelsinki = "Europe/Helsinki"

	// dailySchedule is the weekday value of a daily schedule.
	dailySchedule = time.Weekday(-1)
)

// nextRunFor returns calculateNextRun for a schedule evaluated at now.
func nextRunFor(now time.Time, hour, minute int, weekday time.Weekday, weekly bool) time.Time {
	return (&Scheduler{}).calculateNextRun(now, hour, minute, weekday, weekly)
}

func TestCalculateNextRun_SkippedTimeRunsAtTransitionInstant(t *testing.T) {
	t.Parallel()
	zones := testutil.ToolchainZones(t)

	tests := []struct {
		name       string
		hour       int
		minute     int
		now        time.Time // local time before the spring-forward gap
		weekday    time.Weekday
		weekly     bool
		transition time.Time
	}{
		{
			name:       "daily New York",
			hour:       2,
			minute:     30,
			now:        time.Date(2026, 3, 8, 0, 10, 0, 0, zones[zoneNewYork]),
			weekday:    dailySchedule,
			transition: time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC), // 02:00 EST becomes 03:00 EDT
		},
		{
			name:       "daily Helsinki",
			hour:       3,
			minute:     30,
			now:        time.Date(2026, 3, 29, 0, 10, 0, 0, zones[zoneHelsinki]),
			weekday:    dailySchedule,
			transition: time.Date(2026, 3, 29, 1, 0, 0, 0, time.UTC), // 03:00 EET becomes 04:00 EEST
		},
		{
			name:       "weekly New York on the transition Sunday",
			hour:       2,
			minute:     30,
			now:        time.Date(2026, 3, 4, 12, 0, 0, 0, zones[zoneNewYork]),
			weekday:    time.Sunday,
			weekly:     true,
			transition: time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := nextRunFor(tt.now, tt.hour, tt.minute, tt.weekday, tt.weekly)
			assert.True(t, tt.transition.Equal(got), "got %v, want %v", got, tt.transition)
		})
	}
}

func TestCalculateNextRun_SpringForwardDayLaterRunsKeepWallTime(t *testing.T) {
	t.Parallel()
	loc := testutil.ToolchainZones(t)[zoneNewYork]

	// Run on the skipped day at the transition instant, then recompute: the next run is the
	// 02:30 wall time of the following day (EDT).
	now := time.Date(2026, 3, 8, 3, 0, 0, 0, loc).Add(time.Minute)
	got := nextRunFor(now, 2, 30, dailySchedule, false)
	assert.True(t, time.Date(2026, 3, 9, 2, 30, 0, 0, loc).Equal(got), "got %v", got)
}

func TestCalculateNextRun_RepeatedTimeRunsOnce(t *testing.T) {
	t.Parallel()
	loc := testutil.ToolchainZones(t)[zoneNewYork]
	// 2026-11-01 01:00-02:00 is repeated: 01:30 EDT (05:30 UTC) then 01:30 EST (06:30 UTC).
	first := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)
	second := first.Add(time.Hour)

	// Before the transition the schedule targets the first 01:30.
	before := time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
	next := nextRunFor(before, 1, 30, dailySchedule, false)
	require.True(t, first.Equal(next), "got %v", next)

	// The scheduler fires on the first tick after it, then recomputes from that tick. Ticks in
	// the repeated hour, including the second 01:30 (EST), must not schedule today again.
	for _, tick := range []time.Time{
		first, // the tick that fires the run lands exactly on it
		first.Add(time.Second),
		first.Add(30 * time.Minute),
		second,
		second.Add(time.Second),
	} {
		after := nextRunFor(tick.In(loc), 1, 30, dailySchedule, false)
		assert.True(t, time.Date(2026, 11, 2, 1, 30, 0, 0, loc).Equal(after), "tick %v: got %v", tick, after)
	}
}

func TestCalculateNextRun_WeeklyRepeatedTimeRunsOnce(t *testing.T) {
	t.Parallel()
	loc := testutil.ToolchainZones(t)[zoneNewYork]
	first := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC) // Sunday 01:30 EDT

	tick := first.In(loc) // the tick that fires the run lands exactly on it
	got := nextRunFor(tick, 1, 30, time.Sunday, true)
	assert.True(t, time.Date(2026, 11, 8, 1, 30, 0, 0, loc).Equal(got), "got %v", got)

	tick = first.Add(time.Second).In(loc)
	got = nextRunFor(tick, 1, 30, time.Sunday, true)
	assert.True(t, time.Date(2026, 11, 8, 1, 30, 0, 0, loc).Equal(got), "got %v", got)

	tick = first.Add(time.Hour).Add(time.Second).In(loc) // after the second 01:30
	got = nextRunFor(tick, 1, 30, time.Sunday, true)
	assert.True(t, time.Date(2026, 11, 8, 1, 30, 0, 0, loc).Equal(got), "got %v", got)
}

func TestCalculateNextRun_OrdinaryDays(t *testing.T) {
	t.Parallel()
	loc := testutil.ToolchainZones(t)[zoneHelsinki]

	// Wednesday 2026-06-10.
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, loc)
	assert.True(t, time.Date(2026, 6, 10, 13, 15, 0, 0, loc).Equal(nextRunFor(now, 13, 15, dailySchedule, false)))
	assert.True(t, time.Date(2026, 6, 11, 11, 15, 0, 0, loc).Equal(nextRunFor(now, 11, 15, dailySchedule, false)))
	assert.True(t, time.Date(2026, 6, 10, 13, 0, 0, 0, loc).Equal(nextRunFor(now, 13, 0, time.Wednesday, true)))
	assert.True(t, time.Date(2026, 6, 17, 11, 0, 0, 0, loc).Equal(nextRunFor(now, 11, 0, time.Wednesday, true)))
	// 02:30 on Helsinki's spring-forward day is not skipped (03:00 to 04:00 is), so it keeps its time.
	spring := time.Date(2026, 3, 29, 0, 10, 0, 0, loc)
	assert.True(t, time.Date(2026, 3, 29, 2, 30, 0, 0, loc).Equal(nextRunFor(spring, 2, 30, dailySchedule, false)))
	// A recompute exactly at the run time moves on, so a tick that fires a run cannot fire it again.
	atRun := time.Date(2026, 6, 10, 13, 15, 0, 0, loc)
	assert.True(t, time.Date(2026, 6, 11, 13, 15, 0, 0, loc).Equal(nextRunFor(atRun, 13, 15, dailySchedule, false)))
	assert.True(t, time.Date(2026, 6, 17, 13, 15, 0, 0, loc).Equal(nextRunFor(atRun, 13, 15, time.Wednesday, true)))
	assert.True(t, time.Date(2026, 6, 14, 3, 0, 0, 0, loc).Equal(nextRunFor(now, 3, 0, time.Sunday, true)))
	assert.True(t, time.Date(2026, 6, 15, 3, 0, 0, 0, loc).Equal(nextRunFor(now, 3, 0, time.Monday, true)))
}

// Europe/Helsinki 2026-10-25 (a Sunday) repeats 03:00 to 04:00: 03:30 EEST is 00:30 UTC and
// 03:30 EET is 01:30 UTC. Unlike New York, time.Date returns the second occurrence there, so
// these tests fail if the run time is resolved with time.Date instead of wallclock.At.
func TestCalculateNextRun_HelsinkiRepeatedTimeRunsOnce(t *testing.T) {
	t.Parallel()
	loc := testutil.ToolchainZones(t)[zoneHelsinki]
	first := time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC)
	second := first.Add(time.Hour)

	tests := []struct {
		name    string
		weekday time.Weekday
		weekly  bool
		next    time.Time
	}{
		{"daily", dailySchedule, false, time.Date(2026, 10, 26, 1, 30, 0, 0, time.UTC)},
		{"weekly", time.Sunday, true, time.Date(2026, 11, 1, 1, 30, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Before the repeated hour the run is the first 03:30.
			before := time.Date(2026, 10, 25, 0, 0, 0, 0, loc)
			got := nextRunFor(before, 3, 30, tt.weekday, tt.weekly)
			require.True(t, first.Equal(got), "before: got %v", got)

			for _, tick := range []time.Time{first, first.Add(time.Second), second, second.Add(time.Second)} {
				got := nextRunFor(tick.In(loc), 3, 30, tt.weekday, tt.weekly)
				assert.True(t, tt.next.Equal(got), "tick %v: got %v, want %v", tick, got, tt.next)
			}
		})
	}
}

// A tick exactly on the transition instant of a skipped time fires the run once and moves on.
func TestCalculateNextRun_SkippedTimeTransitionTickMovesOn(t *testing.T) {
	t.Parallel()
	zones := testutil.ToolchainZones(t)

	tests := []struct {
		name   string
		zone   string
		hour   int
		minute int
		tick   time.Time
		next   time.Time
	}{
		{"New York", zoneNewYork, 2, 30, time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC), time.Date(2026, 3, 9, 6, 30, 0, 0, time.UTC)},
		{"Helsinki", zoneHelsinki, 3, 30, time.Date(2026, 3, 29, 1, 0, 0, 0, time.UTC), time.Date(2026, 3, 30, 0, 30, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := nextRunFor(tt.tick.In(zones[tt.zone]), tt.hour, tt.minute, dailySchedule, false)
			assert.True(t, tt.next.Equal(got), "got %v, want %v", got, tt.next)
		})
	}
}

func TestScheduler_RunBackupReleasesRunningBackup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		targets      map[string]Target
		wantFailures int
		wantMissed   int
	}{
		{name: "successful run", targets: map[string]Target{}},
		{name: "failed run", targets: map[string]Target{"t": dummyTarget{}}, wantFailures: 1, wantMissed: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sm, _ := newTestStateManager(t)
			m := &Manager{
				config:       &conf.BackupConfig{Enabled: true},
				sources:      map[string]Source{},
				targets:      tt.targets,
				logger:       GetLogger().Module("test"),
				stateManager: sm,
			}
			s, err := NewScheduler(m, nil, sm)
			require.NoError(t, err)

			schedule := BackupSchedule{Weekday: dailySchedule, NextRun: time.Date(2026, 6, 16, 3, 0, 0, 0, time.UTC)}

			// checkSchedules takes this lock before it spawns runBackup.
			s.runningBackup.Lock()
			requireReturns(t, func() { s.runBackup(&schedule) }, "runBackup did not return")
			require.True(t, s.runningBackup.TryLock(), "runBackup must release runningBackup")

			got := sm.GetScheduleState(&schedule)
			assert.Equal(t, tt.wantFailures, got.FailureCount)
			assert.Equal(t, tt.wantFailures == 0, !got.LastSuccessful.IsZero())
			assert.Len(t, sm.GetMissedBackups(), tt.wantMissed)
		})
	}
}
