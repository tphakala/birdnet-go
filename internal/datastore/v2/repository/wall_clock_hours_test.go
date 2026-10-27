package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/testutil"
	"github.com/tphakala/birdnet-go/internal/wallclock"
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
			// The Unix epoch is 19:00 on 1969-12-31 in New York (UTC-5): rows whose local time is
			// before 1970 must still land in their true hour.
			zone: "America/New_York",
			rows: []hourRow{
				{utc(1969, time.December, 31, 23, 59, 59), 18},
				{utc(1970, time.January, 1, 0, 30, 0), 19},
				{utc(1970, time.January, 1, 4, 59, 59), 23},
				{utc(1970, time.January, 1, 5, 0, 0), 0},
				{utc(2026, time.January, 15, 17, 0, 0), 12},
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
					{"start only", &jan1, nil, func(r hourRow) bool { return r.at.Unix() >= jan1 }},
				}
				seenDays := map[string]bool{}
				for _, row := range zc.rows {
					local := row.at.In(zone)
					day := local.Format(time.DateOnly)
					if seenDays[day] {
						continue
					}
					seenDays[day] = true
					dayStart, dayEnd := wallclock.DayBounds(wallclock.DateOf(local), zone)
					start, end := dayStart.Unix(), dayEnd.Unix()
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
		segments []wallclock.Segment
		fallback int
		want     string
	}{
		{
			name: "equal offsets collapse to a literal",
			segments: []wallclock.Segment{
				{Start: 0, End: 100, OffsetSeconds: winter},
				{Start: 100, End: 200, OffsetSeconds: winter},
			},
			want: "7200",
		},
		{
			name: "two offsets give a CASE with the newest branch first",
			segments: []wallclock.Segment{
				{Start: 0, End: changeAt, OffsetSeconds: summer},
				{Start: changeAt, End: changeAt + 100, OffsetSeconds: winter},
			},
			want: "CASE WHEN c >= 1792890000 THEN 7200 ELSE 10800 END",
		},
		{
			name: "equal offsets that are not adjacent keep every branch",
			segments: []wallclock.Segment{
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

// TestWallClockExprNilLocationIsUTC pins that a nil location means UTC in the SQL builders,
// whatever time.Local is, although wallclock treats a nil location as time.Local. It swaps
// time.Local, so it must not run in parallel.
func TestWallClockExprNilLocationIsUTC(t *testing.T) {
	helsinki := testutil.ToolchainZones(t)["Europe/Helsinki"]
	require.NotNil(t, helsinki)
	previous := time.Local
	time.Local = helsinki
	t.Cleanup(func() { time.Local = previous })

	// The span crosses both 2026 Helsinki DST changes, so a zone-aware expression would differ.
	from := utc(2026, time.January, 1, 0, 0, 0).Unix()
	to := utc(2026, time.December, 31, 0, 0, 0).Unix()

	for _, isMySQL := range []bool{false, true} {
		assert.Equal(t,
			WallClockDateExpr("d.detected_at", time.UTC, from, to, isMySQL),
			WallClockDateExpr("d.detected_at", nil, from, to, isMySQL),
			"WallClockDateExpr, isMySQL=%v", isMySQL)

		repo := &detectionRepository{isMySQL: isMySQL}
		assert.Equal(t,
			repo.wallClockHourExpr("d.detected_at", time.UTC, from, to),
			repo.wallClockHourExpr("d.detected_at", nil, from, to),
			"wallClockHourExpr, isMySQL=%v", isMySQL)
	}
}

// TestWallClockSQLText pins the exact text of the hour, date and year builders for both
// dialects. The MySQL forms are otherwise only exercised by the integration suites, and small
// edits to them change behaviour: DATE() instead of DATE_FORMAT turns the date into a time.Time
// under parseTime=True, FLOOR(x / n) depends on div_precision_increment, and naming the shifted
// column twice doubles a long offset CASE.
func TestWallClockSQLText(t *testing.T) {
	t.Parallel()

	const column, offset = "d.detected_at", "-18000"
	tests := []struct {
		name  string
		build func(column, offsetSQL string, isMySQL bool) string
		mysql bool
		want  string
	}{
		{"sqlite hour", wallClockHourSQL, false, "((((d.detected_at + -18000) % 86400) + 86400) % 86400) / 3600"},
		{"mysql hour", wallClockHourSQL, true, "((((d.detected_at + -18000) % 86400) + 86400) % 86400) DIV 3600"},
		{"sqlite date", wallClockDateSQL, false, "date(d.detected_at + -18000, 'unixepoch')"},
		{"mysql date", wallClockDateSQL, true, "DATE_FORMAT(DATE_ADD('1970-01-01', INTERVAL (d.detected_at + -18000) SECOND), '%Y-%m-%d')"},
		{"sqlite year", wallClockYearSQL, false, "CAST(strftime('%Y', d.detected_at + -18000, 'unixepoch') AS INTEGER)"},
		{"mysql year", wallClockYearSQL, true, "YEAR(DATE_ADD('1970-01-01', INTERVAL (d.detected_at + -18000) SECOND))"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.build(column, offset, tc.mysql))
		})
	}
}
