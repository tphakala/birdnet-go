package species

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/testutil"
	"github.com/tphakala/birdnet-go/internal/wallclock"
)

const (
	anchorWindowDays   = 7
	anchorSpringName   = "spring"
	anchorYearlyFeb29  = 29
	anchorNovemberLast = 30
)

// anchorRow is one zone and calendar date the date-anchor tests run against.
type anchorRow struct {
	zone string
	date wallclock.Date
}

// anchorDate builds a wallclock.Date for a 2026 fixture.
func anchorDate(month time.Month, day int) wallclock.Date {
	return wallclock.Date{Year: 2026, Month: month, Day: day}
}

// anchorRows lists the dates whose anchors must follow the local calendar.
// Havana and Santiago skip midnight on the first date of their pair, and the
// derived date of the second one lands on the skipped midnight. The other
// zones shift the clock at another hour and are controls.
func anchorRows() []anchorRow {
	return []anchorRow{
		{"America/Havana", anchorDate(time.March, 8)},
		{"America/Havana", anchorDate(time.March, 9)},
		{"America/Santiago", anchorDate(time.September, 6)},
		{"America/Santiago", anchorDate(time.September, 7)},
		{"America/New_York", anchorDate(time.March, 8)},
		{"America/New_York", anchorDate(time.November, 1)},
		{"Europe/Helsinki", anchorDate(time.March, 29)},
		{"Europe/Helsinki", anchorDate(time.October, 25)},
		{"Australia/Lord_Howe", anchorDate(time.April, 5)},
		{"Australia/Lord_Howe", anchorDate(time.October, 4)},
	}
}

// anchorEdges returns the first instant of the local date and the last instant
// of the previous local date.
func anchorEdges(t *testing.T, row anchorRow) (loc *time.Location, first, before time.Time) {
	t.Helper()
	loc = testutil.ToolchainZones(t)[row.zone]
	require.NotNil(t, loc, "zone %s", row.zone)
	first, _ = wallclock.DayBounds(row.date, loc)
	return loc, first, first.Add(-time.Nanosecond)
}

// noveltyWindowDatastore records the period requested for detection dates.
type noveltyWindowDatastore struct {
	noveltyHistoryDatastore
	gotStartDate string
	gotEndDate   string
}

func (d *noveltyWindowDatastore) GetSpeciesDetectionDatesInPeriod(_ context.Context, startDate, endDate string, _, _ int) ([]datastore.SpeciesDetectionDate, error) {
	d.gotStartDate, d.gotEndDate = startDate, endDate
	return nil, nil
}

func TestLoadNoveltyEpisodes_QueryWindowUsesLocalDate(t *testing.T) {
	t.Parallel()

	rows := append(anchorRows(), anchorRow{"America/Havana", anchorDate(time.March, 16)})
	for _, row := range rows {
		t.Run(row.zone+"/"+row.date.String(), func(t *testing.T) {
			t.Parallel()

			loc, first, before := anchorEdges(t, row)
			_, end := wallclock.DayBounds(row.date, loc)
			for name, now := range map[string]time.Time{
				"first_instant": first,
				"last_instant":  end.Add(-time.Nanosecond),
			} {
				ds := &noveltyWindowDatastore{}
				tracker := testNoveltyTracker(anchorWindowDays)
				tracker.ds = ds

				require.NoError(t, tracker.loadNoveltyEpisodesFromDatabase(t.Context(), now), name)
				assert.Equal(t, row.date.String(), ds.gotEndDate, "%s: window ends on the local date", name)
				assert.Equal(t, row.date.AddDays(-(anchorWindowDays + 1)).String(), ds.gotStartDate,
					"%s: window starts window+1 calendar days earlier", name)
			}
			assert.Equal(t, row.date.AddDays(-1), wallclock.DateOf(before), "before is the previous local date")
		})
	}
}

// newAnchorYearTracker returns a tracker whose yearly reset falls on date.
func newAnchorYearTracker(date wallclock.Date) *SpeciesTracker {
	return &SpeciesTracker{
		yearlyEnabled:   true,
		resetMonth:      int(date.Month),
		resetDay:        date.Day,
		speciesThisYear: make(map[string]time.Time),
	}
}

func TestYearlyTracking_ResetDateFollowsLocalDate(t *testing.T) {
	t.Parallel()

	for _, row := range anchorRows() {
		t.Run(row.zone+"/"+row.date.String(), func(t *testing.T) {
			t.Parallel()

			_, first, before := anchorEdges(t, row)
			d := row.date
			tracker := newAnchorYearTracker(d)

			assert.Equal(t, d.Year, tracker.getTrackingYear(first), "tracking year on the reset date")
			assert.Equal(t, d.Year-1, tracker.getTrackingYear(before), "tracking year the instant before")

			start, end := tracker.getYearDateRange(first)
			assert.Equal(t, d.String(), start, "year range start on the reset date")
			assert.Equal(t, wallclock.Date{Year: d.Year + 1, Month: d.Month, Day: d.Day}.AddDays(-1).String(), end,
				"year range end on the reset date")

			start, end = tracker.getYearDateRange(before)
			assert.Equal(t, wallclock.Date{Year: d.Year - 1, Month: d.Month, Day: d.Day}.String(), start,
				"previous year range start")
			assert.Equal(t, d.AddDays(-1).String(), end, "previous year range ends the day before the reset date")

			tracker.speciesThisYear["previous"] = before
			tracker.speciesThisYear["current"] = first
			assert.Equal(t, 1, tracker.pruneYearlyEntriesLocked(first), "only the previous-day entry is pruned")
			assert.Contains(t, tracker.speciesThisYear, "current")
			assert.NotContains(t, tracker.speciesThisYear, "previous")

			tracker.currentYear = d.Year
			assert.True(t, tracker.shouldResetYear(wallclock.Noon(first)), "reset fires on the reset date")
			assert.False(t, tracker.shouldResetYear(wallclock.Noon(before)), "no reset the day before")
		})
	}

	t.Run("february_29_reset_in_a_common_year", func(t *testing.T) {
		t.Parallel()

		tracker := newAnchorYearTracker(wallclock.Date{Year: 2027, Month: time.February, Day: anchorYearlyFeb29})
		tracker.currentYear = 2027

		assert.True(t, tracker.shouldResetYear(time.Date(2027, time.March, 1, 12, 0, 0, 0, time.UTC)),
			"a configured February 29 resets on March 1 in a common year")
		assert.False(t, tracker.shouldResetYear(time.Date(2027, time.February, 28, 12, 0, 0, 0, time.UTC)))
	})
}

func TestSeasons_StartOnLocalDate(t *testing.T) {
	t.Parallel()

	for _, row := range anchorRows() {
		t.Run(row.zone+"/"+row.date.String(), func(t *testing.T) {
			t.Parallel()

			_, first, before := anchorEdges(t, row)
			d := row.date
			ds := &carryoverHistoryDatastore{}
			tracker := NewTrackerFromSettings(ds, &conf.SpeciesTrackingSettings{
				Enabled:              true,
				NewSpeciesWindowDays: anchorWindowDays,
				SeasonalTracking: conf.SeasonalTrackingSettings{
					Enabled:    true,
					WindowDays: anchorWindowDays,
					Seasons: map[string]conf.Season{
						"winter":         {StartMonth: 12, StartDay: 21},
						"summer":         {StartMonth: 6, StartDay: 21},
						"fall":           {StartMonth: 9, StartDay: 22},
						anchorSpringName: {StartMonth: int(d.Month), StartDay: d.Day},
					},
				},
			})
			// Compute for the given time instead of reusing the season cached
			// for the real clock at construction, and skip the test year override.
			tracker.seasonCacheTTL = 0
			tracker.currentYear = 0

			assert.Equal(t, anchorSpringName, tracker.computeCurrentSeason(first), "season starts on its date")
			assert.NotEqual(t, anchorSpringName, tracker.computeCurrentSeason(before), "season not started the instant before")

			start, _ := tracker.getSeasonDateRange(anchorSpringName, first)
			assert.Equal(t, d.String(), start, "season range starts on its date")

			require.NoError(t, tracker.loadSeasonCarryoverFromDatabase(t.Context(), first))
			assert.Equal(t, d.AddDays(-anchorWindowDays).String(), ds.gotStartDate, "carry-over lookback starts one window before the season")
			assert.Equal(t, d.AddDays(-1).String(), ds.gotEndDate, "carry-over lookback ends the day before the season")
		})
	}
}

func TestGetSeasonDateRange_EndNormalisesLikeTimeDate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		season    string
		start     conf.Season
		now       time.Time
		wantStart string
		wantEnd   string
	}{
		{
			// November 30 plus three months overflows February into March.
			name:      "november_30_start_overflows_into_march",
			season:    "fall",
			start:     conf.Season{StartMonth: 11, StartDay: anchorNovemberLast},
			now:       time.Date(2026, time.December, 15, 12, 0, 0, 0, time.UTC),
			wantStart: "2026-11-30",
			wantEnd:   "2027-03-01",
		},
		{
			// A configured February 29 is March 1 in a common year.
			name:      "february_29_start_is_march_1_in_a_common_year",
			season:    anchorSpringName,
			start:     conf.Season{StartMonth: 2, StartDay: anchorYearlyFeb29},
			now:       time.Date(2027, time.April, 15, 12, 0, 0, 0, time.UTC),
			wantStart: "2027-03-01",
			wantEnd:   "2027-05-31",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			seasons := map[string]conf.Season{
				"winter": {StartMonth: 12, StartDay: 21},
				"summer": {StartMonth: 6, StartDay: 21},
				"fall":   {StartMonth: 9, StartDay: 22},
				"spring": {StartMonth: 3, StartDay: 20},
			}
			seasons[tt.season] = tt.start
			tracker := NewTrackerFromSettings(nil, &conf.SpeciesTrackingSettings{
				Enabled: true,
				SeasonalTracking: conf.SeasonalTrackingSettings{
					Enabled:    true,
					WindowDays: anchorWindowDays,
					Seasons:    seasons,
				},
			})
			tracker.currentYear = 0

			start, end := tracker.getSeasonDateRange(tt.season, tt.now)
			assert.Equal(t, tt.wantStart, start)
			assert.Equal(t, tt.wantEnd, end)
		})
	}
}
