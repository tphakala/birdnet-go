package v2only

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestV2OnlyDatastore_CountDetectionsMatchListing pins CountSpeciesDetections and
// CountHourlyDetections to the number of rows the matching unpaginated listing
// (SpeciesDetections / GetHourlyDetections with limit 0) returns, so a count and the
// listing it describes always agree. Times are seeded around the
// Europe/Helsinki spring-forward day so the hour and day windows cross a DST change.
func TestV2OnlyDatastore_CountDetectionsMatchListing(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	helsinki, err := time.LoadLocation("Europe/Helsinki")
	require.NoError(t, err)
	ds.timezone = helsinki

	const (
		finch = "Fringilla coelebs"
		robin = "Erithacus rubecula"
	)
	at := func(day, hour, minute int) time.Time {
		return time.Date(2024, time.March, day, hour, minute, 0, 0, helsinki)
	}
	// 2024-03-31 skips 03:00-04:00 local in Helsinki, so that day is 23 hours long. The
	// hour windows are wall-clock hours (an "hour" of 06 is 06:00-07:00 on the clock),
	// so hour 03 does not exist on that day and its window is empty.
	seeds := []struct {
		species string
		at      time.Time
	}{
		{finch, at(30, 23, 59)},
		{finch, at(31, 0, 0)},
		{finch, at(31, 2, 30)},
		{finch, at(31, 4, 0)},
		{finch, at(31, 4, 59)},
		{finch, at(31, 6, 15)},
		{finch, at(31, 23, 59)},
		{finch, time.Date(2024, time.April, 1, 0, 0, 0, 0, helsinki)},
		{robin, at(31, 2, 0)},
		{robin, at(31, 6, 0)},
	}
	for _, s := range seeds {
		seedDetection(t, ds, s.species, s.at)
	}

	t.Run("species", func(t *testing.T) {
		tests := []struct {
			name, species, date, hour string
			duration                  int
			want                      int64
		}{
			{"all time", finch, "", "", 0, 8},
			{"whole DST day", finch, "2024-03-31", "", 0, 6},
			{"hour before the gap", finch, "2024-03-31", "02", 1, 1},
			{"window across the gap", finch, "2024-03-31", "02", 3, 3},
			{"skipped hour is empty", finch, "2024-03-31", "03", 1, 0},
			{"zero duration defaults to one hour", finch, "2024-03-31", "04", 0, 2},
			{"other species same day", robin, "2024-03-31", "", 0, 2},
			{"unknown species", "Nonexistent species", "2024-03-31", "", 0, 0},
			{"all species one hour", "", "2024-03-31", "06", 1, 2},
			{"unparsable date means all time", finch, "not-a-date", "", 0, 8},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got, err := ds.CountSpeciesDetections(tt.species, tt.date, tt.hour, tt.duration)
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)

				listed, err := ds.SpeciesDetections(tt.species, tt.date, tt.hour, tt.duration, false, 0, 0)
				require.NoError(t, err)
				assert.Len(t, listed, int(got), "count must match the unpaginated listing")
			})
		}
	})

	t.Run("hourly", func(t *testing.T) {
		tests := []struct {
			name, date, hour string
			duration         int
			want             int64
		}{
			{"one hour", "2024-03-31", "06", 1, 2},
			{"window across the gap", "2024-03-31", "02", 3, 4},
			// The window ends at the next local midnight, so the 2024-04-01 00:00 finch is
			// not included: 8 rows on the 31st.
			{"24 hours from midnight is exactly the local day", "2024-03-31", "00", 24, 8},
			{"window ending past midnight stops at the end of the day", "2024-03-31", "23", 2, 1},
			{"zero duration is empty", "2024-03-31", "06", 0, 0},
			{"day before", "2024-03-30", "23", 1, 1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got, err := ds.CountHourlyDetections(tt.date, tt.hour, tt.duration)
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)

				listed, err := ds.GetHourlyDetections(tt.date, tt.hour, tt.duration, 0, 0)
				require.NoError(t, err)
				assert.Len(t, listed, int(got), "count must match the unpaginated listing")
			})
		}
	})

	t.Run("invalid input errors", func(t *testing.T) {
		_, err := ds.CountHourlyDetections("not-a-date", "06", 1)
		require.Error(t, err)
		_, err = ds.CountHourlyDetections("2024-03-31", "xx", 1)
		require.Error(t, err)
		_, err = ds.CountSpeciesDetections(finch, "2024-03-31", "xx", 1)
		require.Error(t, err)
	})
}

// TestV2OnlyDatastore_HourWindowsMatchDashboardBuckets pins the hour drill-down to the
// wall-clock hours the dashboard buckets detections into, on days where a zone
// transition skips or repeats an hour. For every hour of the day the count, the
// listing and the dashboard cell must agree, and every listed row must read that hour on
// the clock.
func TestV2OnlyDatastore_HourWindowsMatchDashboardBuckets(t *testing.T) {
	t.Parallel()

	const (
		sci          = "Fringilla coelebs"
		seedStep     = 15 * time.Minute
		seedMargin   = time.Hour
		perHourSeeds = 4 // one seed every seedStep
	)
	tests := []struct {
		name     string
		zone     string
		date     string
		wantHour map[int]int // hours that differ from perHourSeeds
	}{
		{"spring gap after time.Date", "Europe/Helsinki", "2024-03-31", map[int]int{3: 0}},
		{"fall overlap second occurrence", "Europe/Helsinki", "2026-10-25", map[int]int{3: 2 * perHourSeeds}},
		{"spring gap before time.Date", "America/New_York", "2026-03-08", map[int]int{2: 0}},
		{"southern fall overlap", "Australia/Sydney", "2026-04-05", map[int]int{2: 2 * perHourSeeds}},
		{"half-hour offset control", "Asia/Kolkata", "2026-10-25", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ds, cleanup := setupTestDatastore(t)
			defer cleanup()
			loc, err := time.LoadLocation(tt.zone)
			require.NoError(t, err)
			ds.timezone = loc

			day, err := time.ParseInLocation(time.DateOnly, tt.date, loc)
			require.NoError(t, err)
			// Seed from before the day to after it, so nothing outside the date may leak in.
			for at := day.Add(-seedMargin); at.Before(day.AddDate(0, 0, 1).Add(seedMargin)); at = at.Add(seedStep) {
				seedDetection(t, ds, sci, at)
			}

			cells, err := ds.GetBatchHourlyOccurrences(t.Context(), tt.date, tt.date, []string{sci}, 0)
			require.NoError(t, err)
			cell := cells[sci]

			total := 0
			for h := range hoursPerDay {
				want := perHourSeeds
				if w, ok := tt.wantHour[h]; ok {
					want = w
				}
				hour := fmt.Sprintf("%02d", h)
				assert.Equal(t, want, cell[h], "dashboard cell for hour %s", hour)
				total += cell[h]

				hourly, err := ds.CountHourlyDetections(tt.date, hour, 1)
				require.NoError(t, err)
				assert.EqualValues(t, want, hourly, "hourly count for hour %s", hour)
				species, err := ds.CountSpeciesDetections(sci, tt.date, hour, 1)
				require.NoError(t, err)
				assert.EqualValues(t, want, species, "species count for hour %s", hour)

				hourlyRows, err := ds.GetHourlyDetections(tt.date, hour, 1, 0, 0)
				require.NoError(t, err)
				speciesRows, err := ds.SpeciesDetections(sci, tt.date, hour, 1, false, 0, 0)
				require.NoError(t, err)
				assert.Len(t, hourlyRows, want, "hourly listing for hour %s", hour)
				assert.Len(t, speciesRows, want, "species listing for hour %s", hour)
				for _, n := range append(hourlyRows, speciesRows...) {
					assert.Equal(t, tt.date, n.Date)
					assert.Equal(t, hour, n.Time[:2], "listed row %s must read hour %s", n.Time, hour)
				}
			}

			dayCount, err := ds.CountSpeciesDetections(sci, tt.date, "", 0)
			require.NoError(t, err)
			assert.EqualValues(t, total, dayCount, "hour windows must partition the day window")
		})
	}
}
