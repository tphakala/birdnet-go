package backup

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

	tick := first.Add(time.Second).In(loc)
	got := nextRunFor(tick, 1, 30, time.Sunday, true)
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
	assert.True(t, time.Date(2026, 6, 14, 3, 0, 0, 0, loc).Equal(nextRunFor(now, 3, 0, time.Sunday, true)))
	assert.True(t, time.Date(2026, 6, 15, 3, 0, 0, 0, loc).Equal(nextRunFor(now, 3, 0, time.Monday, true)))
}
