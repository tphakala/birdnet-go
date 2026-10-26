package v2only

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/wallclock"
)

const (
	// dayWindowSeedStep is the spacing of the seeded detections. Half an hour puts at least one
	// row in every wall hour, including both occurrences of a repeated hour and the half-hour
	// shifts of Lord_Howe.
	dayWindowSeedStep = 30 * time.Minute
	// dayWindowSeedOffset moves the seeds off the hour boundaries.
	dayWindowSeedOffset = 10 * time.Minute
	// dayWindowSeedMargin extends the seeded range past both ends of a day, so rows just outside
	// the day exist to be excluded.
	dayWindowSeedMargin = 2 * time.Hour
	// dayWindowDrillLimit is larger than any hour or day of seeded rows.
	dayWindowDrillLimit = 1000
	hoursPerWallDay     = 24
)

// dstDayWindowZones are the zones of the dashboard acceptance test: DST changes at a whole hour
// before midnight (Helsinki, New York), a 30 minute change (Lord_Howe), a skipped midnight
// (Havana), a southern hemisphere zone (Sydney) and a zone without DST (Kolkata).
var dstDayWindowZones = []string{
	"Europe/Helsinki",
	"America/New_York",
	"Australia/Sydney",
	"Australia/Lord_Howe",
	"Asia/Kolkata",
	"America/Havana",
}

// dstDays returns the dates of year on which loc's local day is not 24 hours long, plus the date
// before each: the end bound of that earlier day is the start of the changed one.
func dstDays(year int, loc *time.Location) []wallclock.Date {
	var days []wallclock.Date
	seen := make(map[wallclock.Date]bool)
	add := func(d wallclock.Date) {
		if !seen[d] {
			seen[d] = true
			days = append(days, d)
		}
	}
	first := wallclock.Date{Year: year, Month: time.January, Day: 1}
	last := wallclock.Date{Year: year, Month: time.December, Day: 31}
	for d := range wallclock.Dates(first, last) {
		start, end := wallclock.DayBounds(d, loc)
		if end.Sub(start) != hoursPerWallDay*time.Hour {
			add(d.AddDays(-1))
			add(d)
		}
	}
	return days
}

// TestV2OnlyDatastore_DashboardCellsMatchDrillDownOnDSTDays seeds detections every half hour
// around each DST day of 2026 and checks that, for every wall hour, the dashboard's hourly cell
// equals both drill-down counts, and that the whole-day counts equal the sum of the cells.
func TestV2OnlyDatastore_DashboardCellsMatchDrillDownOnDSTDays(t *testing.T) {
	t.Parallel()

	for _, zone := range dstDayWindowZones {
		t.Run(zone, func(t *testing.T) {
			t.Parallel()
			loc, err := time.LoadLocation(zone)
			require.NoError(t, err)

			ds, cleanup := setupTestDatastore(t)
			defer cleanup()
			ds.timezone = loc

			const species = "Fringilla coelebs"
			days := dstDays(2026, loc)
			if zone == "Asia/Kolkata" {
				// No DST: one ordinary day keeps the zone as a control.
				days = []wallclock.Date{{Year: 2026, Month: time.June, Day: 1}}
			} else {
				require.NotEmpty(t, days)
			}

			type wallKey struct {
				date string
				hour int
			}
			want := make(map[wallKey]int)
			firstLast := make(map[string][2]time.Time) // wall date -> earliest and latest seeded instant
			seeded := make(map[int64]bool)
			for _, day := range days {
				start, end := wallclock.DayBounds(day, loc)
				from := start.Add(-dayWindowSeedMargin)
				to := end.Add(dayWindowSeedMargin)
				for at := from.Add(dayWindowSeedOffset); at.Before(to); at = at.Add(dayWindowSeedStep) {
					if seeded[at.Unix()] {
						continue
					}
					seeded[at.Unix()] = true
					seedDetection(t, ds, species, at)
					local := at.In(loc)
					date := local.Format(time.DateOnly)
					want[wallKey{date, local.Hour()}]++
					fl, ok := firstLast[date]
					if !ok {
						fl[0] = at
					}
					fl[1] = at
					firstLast[date] = fl
				}
			}

			for _, day := range days {
				date := day.String()
				cells, err := ds.GetBatchHourlyOccurrences(t.Context(), date, date, []string{species}, 0)
				require.NoError(t, err)

				dayTotal := 0
				for hour := range hoursPerWallDay {
					expected := want[wallKey{date, hour}]
					dayTotal += expected
					cell := cells[species][hour]
					hh := fmt.Sprintf("%02d", hour)

					count, err := ds.CountHourlyDetections(date, hh, 1)
					require.NoError(t, err)
					drill, err := ds.SpeciesDetections(species, date, hh, 0, true, dayWindowDrillLimit, 0)
					require.NoError(t, err)

					assert.Equal(t, expected, cell, "%s %s hour %d dashboard cell", zone, date, hour)
					assert.Equal(t, int64(cell), count, "%s %s hour %d CountHourlyDetections", zone, date, hour)
					assert.Len(t, drill, cell, "%s %s hour %d SpeciesDetections", zone, date, hour)
				}

				total, err := ds.CountSpeciesDetections(species, date, "", 0)
				require.NoError(t, err)
				assert.Equal(t, int64(dayTotal), total, "%s %s CountSpeciesDetections whole day", zone, date)
				dayRows, err := ds.SpeciesDetections(species, date, "", 0, true, dayWindowDrillLimit, 0)
				require.NoError(t, err)
				assert.Len(t, dayRows, dayTotal, "%s %s SpeciesDetections whole day", zone, date)
				top, err := ds.GetTopBirdsData(t.Context(), date, 0, 1)
				require.NoError(t, err)
				if dayTotal == 0 {
					assert.Empty(t, top, "%s %s GetTopBirdsData", zone, date)
				} else {
					require.Len(t, top, 1, "%s %s GetTopBirdsData", zone, date)
					fl := firstLast[date]
					assert.Equal(t, fl[0].In(loc).Format(time.TimeOnly), top[0].FirstTime, "%s %s GetTopBirdsData first", zone, date)
					assert.Equal(t, fl[1].In(loc).Format(time.TimeOnly), top[0].Time, "%s %s GetTopBirdsData last", zone, date)
				}
			}
		})
	}
}

// TestV2OnlyDatastore_ParseDateRangeFollowsLocalDays pins parseDateRange on the days where a
// local midnight does not exist or is shifted: the range is [start of first day, start of the
// day after the last), and consecutive days share a boundary.
func TestV2OnlyDatastore_ParseDateRangeFollowsLocalDays(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		zone      string
		start     string
		end       string
		wantStart string
		wantEnd   string
	}{
		{"Havana skipped midnight", "America/Havana", "2026-03-08", "2026-03-08", "2026-03-08T01:00:00-04:00", "2026-03-09T00:00:00-04:00"},
		{"Havana day before the skipped midnight", "America/Havana", "2026-03-07", "2026-03-07", "2026-03-07T00:00:00-05:00", "2026-03-08T01:00:00-04:00"},
		{"Santiago day before the skipped midnight", "America/Santiago", "2026-09-05", "2026-09-05", "2026-09-05T00:00:00-04:00", "2026-09-06T01:00:00-03:00"},
		{"New York fall back", "America/New_York", "2026-11-01", "2026-11-01", "2026-11-01T00:00:00-04:00", "2026-11-02T00:00:00-05:00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			loc, err := time.LoadLocation(tt.zone)
			require.NoError(t, err)
			ds := &Datastore{timezone: loc}

			start, end, err := ds.parseDateRange(tt.start, tt.end)
			require.NoError(t, err)
			assert.Equal(t, mustRFC3339(t, tt.wantStart).Unix(), start, "start")
			assert.Equal(t, mustRFC3339(t, tt.wantEnd).Unix(), end, "end")
		})
	}
}

// TestV2OnlyDatastore_GetSpeciesLastDetectionDateBeforeSkippedMidnight pins that the cutoff of
// a date whose local midnight does not exist is the start of that day, so a detection in the
// last hour of the day before is still found.
func TestV2OnlyDatastore_GetSpeciesLastDetectionDateBeforeSkippedMidnight(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	havana, err := time.LoadLocation("America/Havana")
	require.NoError(t, err)
	ds.timezone = havana

	const species = "Fringilla coelebs"
	// 23:30 on 2026-03-07 Havana time, 30 minutes before the clock jumps from 00:00 to 01:00.
	seedDetection(t, ds, species, mustRFC3339(t, "2026-03-07T23:30:00-05:00"))

	got, err := ds.GetSpeciesLastDetectionDateBefore(t.Context(), species, "2026-03-08")
	require.NoError(t, err)
	assert.Equal(t, "2026-03-07", got)
}

func mustRFC3339(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return ts
}
