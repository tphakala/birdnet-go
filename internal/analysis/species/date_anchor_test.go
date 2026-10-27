package species

import (
	"context"
	"maps"
	"slices"
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

			tracker.currentYear = d.Year - 1 // tracking year in force the day before
			assert.True(t, tracker.shouldResetYear(wallclock.Noon(first)), "reset fires on the reset date")
			assert.False(t, tracker.shouldResetYear(wallclock.Noon(before)), "no reset the day before")
		})
	}

	t.Run("february_29_reset_in_a_common_year", func(t *testing.T) {
		t.Parallel()

		tracker := newAnchorYearTracker(wallclock.Date{Year: 2027, Month: time.February, Day: anchorYearlyFeb29})
		tracker.currentYear = 2026

		assert.True(t, tracker.shouldResetYear(time.Date(2027, time.March, 1, 12, 0, 0, 0, time.UTC)),
			"a configured February 29 resets on March 1 in a common year")
		assert.False(t, tracker.shouldResetYear(time.Date(2027, time.February, 28, 12, 0, 0, 0, time.UTC)))
	})

	t.Run("february_29_reset_year_ranges_and_prune_follow_the_normalised_date", func(t *testing.T) {
		t.Parallel()

		feb29 := wallclock.Date{Year: 2027, Month: time.February, Day: anchorYearlyFeb29}
		tests := []struct {
			name      string
			now       time.Time
			wantYear  int
			wantStart string
			wantEnd   string
		}{
			{
				name:      "common_year_before_march_1",
				now:       time.Date(2027, time.February, 28, 12, 0, 0, 0, time.UTC),
				wantYear:  2026,
				wantStart: "2026-03-01",
				wantEnd:   "2027-02-28",
			},
			{
				name:      "common_year_on_march_1",
				now:       time.Date(2027, time.March, 1, 12, 0, 0, 0, time.UTC),
				wantYear:  2027,
				wantStart: "2027-03-01",
				wantEnd:   "2028-02-28",
			},
			{
				name:      "leap_year_on_february_29",
				now:       time.Date(2028, time.February, 29, 12, 0, 0, 0, time.UTC),
				wantYear:  2028,
				wantStart: "2028-02-29",
				wantEnd:   "2029-02-28",
			},
			{
				name:      "leap_year_before_february_29",
				now:       time.Date(2028, time.February, 28, 12, 0, 0, 0, time.UTC),
				wantYear:  2027,
				wantStart: "2027-03-01",
				wantEnd:   "2028-02-28",
			},
			{
				// The year after a leap year, before the normalised reset date: the tracking year
				// started on the real February 29, so the prune cutoff is that day and not March 1.
				name:      "year_after_a_leap_year_before_the_reset",
				now:       time.Date(2025, time.February, 14, 12, 0, 0, 0, time.UTC),
				wantYear:  2024,
				wantStart: "2024-02-29",
				wantEnd:   "2025-02-28",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				tracker := newAnchorYearTracker(feb29)
				assert.Equal(t, tt.wantYear, tracker.getTrackingYear(tt.now))
				start, end := tracker.getYearDateRange(tt.now)
				assert.Equal(t, tt.wantStart, start)
				assert.Equal(t, tt.wantEnd, end)

				// The prune cutoff is the range start: the day before it is pruned.
				startDate, err := wallclock.ParseDate(tt.wantStart)
				require.NoError(t, err)
				beforeStart := time.Date(startDate.Year, startDate.Month, startDate.Day-1, 12, 0, 0, 0, time.UTC)
				onStart := time.Date(startDate.Year, startDate.Month, startDate.Day, 12, 0, 0, 0, time.UTC)
				tracker.speciesThisYear["previous"] = beforeStart
				tracker.speciesThisYear["current"] = onStart
				assert.Equal(t, 1, tracker.pruneYearlyEntriesLocked(tt.now))
				assert.Contains(t, tracker.speciesThisYear, "current")
			})
		}
	})
}

// newAnchorSeasonTracker returns a seasonal tracker whose spring starts on date.
// It computes seasons for the given time instead of reusing the season cached
// for the real clock at construction.
func newAnchorSeasonTracker(ds SpeciesDatastore, date wallclock.Date) *SpeciesTracker {
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
				anchorSpringName: {StartMonth: int(date.Month), StartDay: date.Day},
			},
		},
	})
	tracker.seasonCacheTTL = 0
	return tracker
}

func TestSeasons_StartOnLocalDate(t *testing.T) {
	t.Parallel()

	for _, row := range anchorRows() {
		t.Run(row.zone+"/"+row.date.String(), func(t *testing.T) {
			t.Parallel()

			_, first, before := anchorEdges(t, row)
			d := row.date
			ds := &carryoverHistoryDatastore{}
			tracker := newAnchorSeasonTracker(ds, d)

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

func TestSeasonCarryover_LiveRolloverOnLocalDate(t *testing.T) {
	t.Parallel()

	rows := []anchorRow{
		{"America/Havana", anchorDate(time.March, 8)},
		{"America/Santiago", anchorDate(time.September, 6)},
	}
	for _, row := range rows {
		t.Run(row.zone+"/"+row.date.String(), func(t *testing.T) {
			t.Parallel()

			loc := testutil.ToolchainZones(t)[row.zone]
			require.NotNil(t, loc)
			d := row.date
			lookbackFirst, _ := wallclock.DayBounds(d.AddDays(-anchorWindowDays), loc)
			first, _ := wallclock.DayBounds(d, loc)

			const (
				justBefore = "Cyanocitta cristata" // last heard the final instant before the season
				lookbackIn = "Hirundo rustica"     // last heard the first instant of the lookback
				tooOld     = "Setophaga palmarum"  // last heard the instant before the lookback
			)
			tracker := newAnchorSeasonTracker(nil, d)
			tracker.UpdateSpecies(tooOld, lookbackFirst.Add(-time.Nanosecond))
			tracker.UpdateSpecies(lookbackIn, lookbackFirst)
			tracker.UpdateSpecies(justBefore, first.Add(-time.Nanosecond))

			// The first detection of the season triggers the rollover.
			tracker.UpdateSpecies("Turdus migratorius", first)

			assert.Contains(t, tracker.seasonCarryover, justBefore, "heard the day before the season")
			assert.Contains(t, tracker.seasonCarryover, lookbackIn, "the lookback starts a whole window before the season")
			assert.NotContains(t, tracker.seasonCarryover, tooOld, "heard before the lookback")
			assert.Equal(t, d, tracker.seasonCarryoverStart)
		})
	}
}

func TestIsSameSeasonPeriod_BoundaryIsLocalCalendarDay(t *testing.T) {
	t.Parallel()

	havana := testutil.ToolchainZones(t)["America/Havana"]
	require.NotNil(t, havana)
	d := anchorDate(time.March, 8) // Havana skips midnight on this date
	tracker := newAnchorSeasonTracker(nil, d)

	onBoundaryDay, _ := wallclock.DayBounds(d, havana)
	nextDay, _ := wallclock.DayBounds(d.AddDays(1), havana)

	assert.False(t, tracker.isSameSeasonPeriod(onBoundaryDay, nextDay),
		"a season boundary on the first date of the pair makes the pair cross it")
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

			start, end := tracker.getSeasonDateRange(tt.season, tt.now)
			assert.Equal(t, tt.wantStart, start)
			assert.Equal(t, tt.wantEnd, end)
		})
	}
}

// resetStep is one operation in a tracking-year reset sequence and the state
// expected after it.
type resetStep struct {
	op       string // "detect", "status", "batch" or "seed"
	species  string
	at       time.Time
	wantYear int
	wantSet  []string
}

func resetNoon(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 12, 0, 0, 0, time.UTC)
}

// newResetTestSettings returns yearly tracking settings with the given reset date.
func newResetTestSettings(resetMonth, resetDay int) *conf.SpeciesTrackingSettings {
	return &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 30,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			WindowDays: 30,
			ResetMonth: resetMonth,
			ResetDay:   resetDay,
		},
	}
}

func runResetSteps(t *testing.T, tracker *SpeciesTracker, steps []resetStep) {
	t.Helper()

	for i, step := range steps {
		switch step.op {
		case "detect":
			tracker.UpdateSpecies(step.species, step.at)
		case "status":
			tracker.GetSpeciesStatus(step.species, step.at)
		case "batch":
			tracker.GetBatchSpeciesStatus([]string{step.species}, step.at)
		case "seed":
			tracker.mu.Lock()
			tracker.speciesThisYear[step.species] = step.at
			tracker.mu.Unlock()
		default:
			t.Fatalf("step %d: unknown op %q", i, step.op)
		}

		tracker.mu.RLock()
		gotYear := tracker.currentYear
		gotSet := slices.Collect(maps.Keys(tracker.speciesThisYear))
		tracker.mu.RUnlock()

		assert.Equal(t, step.wantYear, gotYear, "step %d (%s %s at %s): tracking year", i, step.op, step.species, step.at)
		assert.ElementsMatch(t, step.wantSet, gotSet, "step %d (%s %s at %s): yearly set", i, step.op, step.species, step.at)
	}
}

func TestYearlyTracking_ResetFollowsTrackingYear(t *testing.T) {
	t.Parallel()

	const (
		resetMonth = int(time.July)
		resetDay   = 1
	)

	tests := []struct {
		name        string
		startYear   int
		constructAt time.Time // when non-zero, build through the clock seam and expect startYear
		steps       []resetStep
	}{
		{
			name:      "custom_reset_survives_new_year",
			startYear: 2026,
			steps: []resetStep{
				{"detect", "A", resetNoon(2026, time.December, 20), 2026, []string{"A"}},
				{"detect", "B", resetNoon(2027, time.January, 1), 2026, []string{"A", "B"}},
				{"detect", "C", resetNoon(2027, time.January, 2), 2026, []string{"A", "B", "C"}},
				{"detect", "D", resetNoon(2027, time.March, 15), 2026, []string{"A", "B", "C", "D"}},
				{"detect", "E", resetNoon(2027, time.June, 30), 2026, []string{"A", "B", "C", "D", "E"}},
			},
		},
		{
			name:      "custom_reset_fires_once_on_reset_date",
			startYear: 2026,
			steps: []resetStep{
				{"detect", "A", resetNoon(2027, time.June, 30), 2026, []string{"A"}},
				{"detect", "B", resetNoon(2027, time.July, 1), 2027, []string{"B"}},
				{"detect", "C", time.Date(2027, time.July, 1, 18, 0, 0, 0, time.UTC), 2027, []string{"B", "C"}},
				{"detect", "D", resetNoon(2027, time.July, 2), 2027, []string{"B", "C", "D"}},
			},
		},
		{
			name:        "constructor_seeded_before_reset_date_then_gap",
			startYear:   2026,
			constructAt: resetNoon(2027, time.February, 10),
			steps: []resetStep{
				{"detect", "A", resetNoon(2027, time.February, 10), 2026, []string{"A"}},
				{"seed", "Stale", resetNoon(2027, time.February, 1), 2026, []string{"A", "Stale"}},
				{"detect", "A2", resetNoon(2027, time.June, 28), 2026, []string{"A", "Stale", "A2"}},
				// Nothing is detected on the reset day; the first later call resets.
				{"detect", "B", resetNoon(2027, time.July, 3), 2027, []string{"B"}},
			},
		},
		{
			name:      "past_dated_status_read_does_not_reset",
			startYear: 2027,
			steps: []resetStep{
				{"detect", "A", resetNoon(2027, time.July, 3), 2027, []string{"A"}},
				{"status", "A", time.Date(2027, time.June, 20, 23, 59, 59, 0, time.UTC), 2027, []string{"A"}},
			},
		},
		{
			name:      "status_cache_does_not_hide_reset",
			startYear: 2026,
			steps: []resetStep{
				{"detect", "A", resetNoon(2027, time.June, 30), 2026, []string{"A"}},
				{"status", "A", time.Date(2027, time.June, 30, 23, 59, 45, 0, time.UTC), 2026, []string{"A"}},
				// Within the status cache TTL of the read above, but in the next tracking year.
				{"status", "A", time.Date(2027, time.July, 1, 0, 0, 5, 0, time.UTC), 2027, nil},
			},
		},
		{
			name:        "later_tracking_year_batch_read_does_not_reset",
			startYear:   2026,
			constructAt: resetNoon(2027, time.February, 10),
			steps: []resetStep{
				{"detect", "A", resetNoon(2027, time.February, 10), 2026, []string{"A"}},
				// A dashboard read for a date in the next tracking year.
				{"batch", "A", resetNoon(2027, time.July, 5), 2026, []string{"A"}},
				{"detect", "B", resetNoon(2027, time.March, 1), 2026, []string{"A", "B"}},
				{"detect", "C", resetNoon(2027, time.July, 2), 2027, []string{"C"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var tracker *SpeciesTracker
			if tt.constructAt.IsZero() {
				tracker = NewTrackerFromSettings(nil, newResetTestSettings(resetMonth, resetDay))
				tracker.currentYear = tt.startYear
			} else {
				tracker = newTrackerFromSettingsAt(nil, newResetTestSettings(resetMonth, resetDay), tt.constructAt)
				require.Equal(t, tt.startYear, tracker.currentYear, "constructor seeds the tracking year")
			}

			runResetSteps(t, tracker, tt.steps)
		})
	}

	t.Run("constructor_seeds_new_tracking_year_after_reset_date", func(t *testing.T) {
		t.Parallel()

		tracker := newTrackerFromSettingsAt(nil, newResetTestSettings(resetMonth, resetDay), resetNoon(2027, time.August, 1))
		assert.Equal(t, 2027, tracker.currentYear)
	})
}
