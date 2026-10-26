package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hourRow is a detection instant with the wall-clock hour the station sees it in.
type hourRow struct {
	at       time.Time // always built in UTC, never from an ambiguous local wall time
	wantHour int
}

func utc(year int, month time.Month, day, hour, minute, second int) time.Time {
	return time.Date(year, month, day, hour, minute, second, 0, time.UTC)
}

// wallClockZoneCases are the zones and rows shared by the hour predicate tests. Helsinki covers
// both 2026 DST changes (including the repeated 03:xx hour and the skipped 03:xx hour), and
// Lord Howe covers the southern hemisphere and a 30 minute DST shift.
func wallClockZoneCases() []struct {
	zone string
	rows []hourRow
} {
	return []struct {
		zone string
		rows []hourRow
	}{
		{
			zone: "Europe/Helsinki",
			rows: []hourRow{
				{utc(2026, time.January, 15, 6, 30, 0), 8},
				{utc(2026, time.July, 15, 5, 30, 0), 8},
				// Spring forward 2026-03-29 01:00Z: 03:00 EET becomes 04:00 EEST.
				{utc(2026, time.March, 29, 0, 59, 59), 2},
				{utc(2026, time.March, 29, 1, 0, 0), 4},
				{utc(2026, time.March, 29, 1, 15, 0), 4},
				{utc(2026, time.March, 29, 20, 59, 59), 23},
				// Fall back 2026-10-25 01:00Z: 04:00 EEST becomes 03:00 EET.
				{utc(2026, time.October, 24, 23, 50, 0), 2},
				{utc(2026, time.October, 25, 0, 10, 0), 3},
				{utc(2026, time.October, 25, 0, 59, 59), 3},
				{utc(2026, time.October, 25, 1, 0, 0), 3},
				{utc(2026, time.October, 25, 1, 40, 0), 3},
				{utc(2026, time.October, 25, 2, 5, 0), 4},
				{utc(2026, time.October, 25, 21, 15, 0), 23},
				{utc(2026, time.October, 25, 21, 59, 59), 23},
			},
		},
		{
			zone: "Australia/Lord_Howe",
			rows: []hourRow{
				// Fall back 2026-04-05 02:00 +11 becomes 01:30 +10:30.
				{utc(2026, time.April, 4, 14, 45, 0), 1},
				{utc(2026, time.April, 4, 15, 15, 0), 1},
				{utc(2026, time.April, 4, 15, 35, 0), 2},
				// Spring forward 2026-10-04 02:00 +10:30 becomes 02:30 +11.
				{utc(2026, time.October, 3, 15, 29, 0), 1},
				{utc(2026, time.October, 3, 15, 30, 0), 2},
				{utc(2026, time.June, 15, 0, 0, 0), 10},
				{utc(2026, time.January, 15, 0, 0, 0), 11},
			},
		},
	}
}

// TestSearch_IncludedHoursUseEachDetectionsWallClockHour pins that the hour predicate reads each
// row's own wall-clock hour, whatever the clock says now and whichever DST regime the row is in.
func TestSearch_IncludedHoursUseEachDetectionsWallClockHour(t *testing.T) {
	ctx := t.Context()
	clocks := []time.Time{
		utc(2026, time.December, 15, 12, 0, 0),
		utc(2027, time.July, 15, 12, 0, 0),
	}

	for _, zc := range wallClockZoneCases() {
		zone, err := time.LoadLocation(zc.zone)
		require.NoError(t, err)

		for _, clock := range clocks {
			t.Run(fmt.Sprintf("%s now %s", zc.zone, clock.Format(time.DateOnly)), func(t *testing.T) {
				db := setupDetectionTestDB(t)
				repo := &detectionRepository{db: db, now: func() time.Time { return clock }}
				for _, row := range zc.rows {
					createTestDetection(t, db, row.at.Unix())
				}

				type window struct {
					name       string
					start, end *int64
					include    func(hourRow) bool
				}
				jan1 := time.Date(2026, time.January, 1, 0, 0, 0, 0, zone).Unix()
				windows := []window{
					{"open ended", nil, nil, func(hourRow) bool { return true }},
					{"start only", &jan1, nil, func(hourRow) bool { return true }},
				}
				seenDays := map[string]bool{}
				for _, row := range zc.rows {
					local := row.at.In(zone)
					day := local.Format(time.DateOnly)
					if seenDays[day] {
						continue
					}
					seenDays[day] = true
					start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone).Unix()
					end := exclusiveDayEnd(local.Year(), local.Month(), local.Day(), zone)
					windows = append(windows, window{"day " + day, &start, &end, func(r hourRow) bool {
						return r.at.In(zone).Format(time.DateOnly) == day
					}})
				}

				for _, w := range windows {
					for hour := range wallClockHoursPerDay {
						var want []int64
						for _, row := range zc.rows {
							if row.wantHour == hour && w.include(row) {
								want = append(want, row.at.Unix())
							}
						}
						filters := &SearchFilters{
							StartTime:     w.start,
							EndTime:       w.end,
							IncludedHours: []int{hour},
							TimeZone:      zone,
						}
						results, total, err := repo.Search(ctx, filters)
						require.NoError(t, err)
						got := make([]int64, 0, len(results))
						for _, d := range results {
							got = append(got, d.DetectedAt)
						}
						assert.ElementsMatch(t, want, got, "%s hour %d", w.name, hour)
						assert.Equal(t, int64(len(want)), total, "%s hour %d total", w.name, hour)

						count, err := repo.CountSearch(ctx, filters)
						require.NoError(t, err)
						assert.Equal(t, int64(len(want)), count, "%s hour %d count", w.name, hour)
					}
				}
			})
		}
	}
}

func TestZoneOffsetSQL(t *testing.T) {
	t.Parallel()

	const (
		changeAt = 1792890000
		winter   = 7200
		summer   = 10800
	)
	tests := []struct {
		name     string
		segments []ZoneOffsetSegment
		fallback int
		want     string
	}{
		{
			name: "equal offsets collapse to a literal",
			segments: []ZoneOffsetSegment{
				{Start: 0, End: 100, OffsetSeconds: winter},
				{Start: 100, End: 200, OffsetSeconds: winter},
			},
			want: "7200",
		},
		{
			name: "two offsets give a CASE with the newest branch first",
			segments: []ZoneOffsetSegment{
				{Start: 0, End: changeAt, OffsetSeconds: summer},
				{Start: changeAt, End: changeAt + 100, OffsetSeconds: winter},
			},
			want: "CASE WHEN c >= 1792890000 THEN 7200 ELSE 10800 END",
		},
		{
			name: "equal offsets that are not adjacent keep every branch",
			segments: []ZoneOffsetSegment{
				{Start: 0, End: 100, OffsetSeconds: summer},
				{Start: 100, End: changeAt, OffsetSeconds: winter},
				{Start: changeAt, End: changeAt + 100, OffsetSeconds: summer},
			},
			want: "CASE WHEN c >= 1792890000 THEN 10800 WHEN c >= 100 THEN 7200 ELSE 10800 END",
		},
		{
			name:     "no segments use the fallback",
			fallback: -18000,
			want:     "-18000",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, zoneOffsetSQL("c", tc.segments, tc.fallback))
		})
	}
}

// TestExclusiveDayEnd pins the end of a local day on days where the next midnight is not
// 24 hours after this one, including a zone whose DST change skips midnight itself.
func TestExclusiveDayEnd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		zone  string
		year  int
		month time.Month
		day   int
		want  time.Time
	}{
		{"fall back day is 25 hours long", "Europe/Helsinki", 2026, time.October, 25, utc(2026, time.October, 25, 22, 0, 0)},
		{"spring forward day is 23 hours long", "Europe/Helsinki", 2026, time.March, 29, utc(2026, time.March, 29, 21, 0, 0)},
		// Havana skips 00:00-01:00 on 2026-03-08, so the day ends when 01:00 -04:00 begins.
		{"day before a skipped midnight", "America/Havana", 2026, time.March, 7, utc(2026, time.March, 8, 5, 0, 0)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			zone, err := time.LoadLocation(tc.zone)
			require.NoError(t, err)
			assert.Equal(t, tc.want.Unix(), exclusiveDayEnd(tc.year, tc.month, tc.day, zone))
		})
	}
}

// TestHourFilterSpan pins the span the hour offsets are enumerated over: an open end follows the
// repository clock, and an explicit bound is kept within the epoch and one leap year past the
// clock, so the SQL does not grow with the date a caller types.
func TestHourFilterSpan(t *testing.T) {
	t.Parallel()

	clock := utc(2026, time.June, 1, 12, 0, 0)
	const leapYearSeconds = 366 * 24 * 3600
	repo := &detectionRepository{now: func() time.Time { return clock }}
	at := func(year int) *int64 {
		unix := utc(year, time.January, 1, 0, 0, 0).Unix()
		return &unix
	}
	farFuture := utc(9999, time.December, 31, 0, 0, 0).Unix()
	beforeEpoch := utc(1, time.January, 1, 0, 0, 0).Unix()

	tests := []struct {
		name     string
		filters  SearchFilters
		wantFrom int64
		wantTo   int64
	}{
		{"open range runs from the epoch to the clock", SearchFilters{}, 0, clock.Unix()},
		{"start and end inside the window are kept", SearchFilters{StartTime: at(2020), EndTime: at(2026)}, *at(2020), *at(2026)},
		{"end past the margin is clamped", SearchFilters{EndTime: &farFuture}, 0, clock.Unix() + leapYearSeconds},
		{"start before the epoch is clamped", SearchFilters{StartTime: &beforeEpoch}, 0, clock.Unix()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			from, to := repo.hourFilterSpan(&tc.filters)
			assert.Equal(t, tc.wantFrom, from, "from")
			assert.Equal(t, tc.wantTo, to, "to")
		})
	}
}
