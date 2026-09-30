package species

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

const (
	carryoverResident = "Cyanocitta cristata" // heard right up to the season boundary
	carryoverMigrant  = "Hirundo rustica"     // absent for months, then returns
	carryoverNewcomer = "Setophaga palmarum"  // first ever detection in the new season
	carryoverWindow   = 7                     // seasonal window in days
	carryoverYear     = 2026                  // fall starts September 22
	carryoverHour     = 8                     // detection hour for every fixture
)

// carryoverDate returns a fixture timestamp in carryoverYear.
func carryoverDate(month time.Month, day int) time.Time {
	return time.Date(carryoverYear, month, day, carryoverHour, 0, 0, 0, time.UTC)
}

// newCarryoverTracker returns a tracker with only seasonal tracking enabled and
// the standard Northern Hemisphere season boundaries.
func newCarryoverTracker(t *testing.T, ds SpeciesDatastore) *SpeciesTracker {
	t.Helper()
	return NewTrackerFromSettings(ds, &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: carryoverWindow,
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: carryoverWindow,
			Seasons: map[string]conf.Season{
				"spring": {StartMonth: 3, StartDay: 20},
				"summer": {StartMonth: 6, StartDay: 21},
				"fall":   {StartMonth: 9, StartDay: 22},
				"winter": {StartMonth: 12, StartDay: 21},
			},
		},
	})
}

func TestSeasonCarryover_LiveRollover(t *testing.T) {
	t.Parallel()

	tracker := newCarryoverTracker(t, nil)

	// Summer detections, before the fall boundary.
	tracker.UpdateSpecies(carryoverMigrant, carryoverDate(time.April, 10))
	tracker.UpdateSpecies(carryoverResident, carryoverDate(time.September, 20))

	// First fall detections.
	tracker.UpdateSpecies(carryoverResident, carryoverDate(time.September, 22))
	tracker.UpdateSpecies(carryoverMigrant, carryoverDate(time.September, 23))
	tracker.UpdateSpecies(carryoverNewcomer, carryoverDate(time.September, 24))

	checkTime := carryoverDate(time.September, 29)

	resident := tracker.GetSpeciesStatus(carryoverResident, checkTime)
	assert.False(t, resident.IsNewThisSeason, "species heard just before fall began is not new this fall")
	assert.Equal(t, 7, resident.DaysThisSeason)
	assert.Equal(t, "fall", resident.CurrentSeason)

	migrant := tracker.GetSpeciesStatus(carryoverMigrant, checkTime)
	assert.True(t, migrant.IsNewThisSeason, "species returning after a long absence is new this fall")

	newcomer := tracker.GetSpeciesStatus(carryoverNewcomer, checkTime)
	assert.True(t, newcomer.IsNewThisSeason, "first ever detection is new this fall")
}

func TestSeasonCarryover_PastDateViewKeepsCurrentSeasonSet(t *testing.T) {
	t.Parallel()

	tracker := newCarryoverTracker(t, nil)
	tracker.UpdateSpecies(carryoverResident, carryoverDate(time.September, 20))
	tracker.UpdateSpecies(carryoverResident, carryoverDate(time.September, 22))

	// Viewing a summer date flips the tracker's current season back and forth;
	// the fall carry-over set must survive the round trip.
	tracker.GetSpeciesStatus(carryoverResident, carryoverDate(time.August, 1))
	status := tracker.GetSpeciesStatus(carryoverResident, carryoverDate(time.September, 29))

	assert.False(t, status.IsNewThisSeason)
}

func TestSeasonCarryover_LiveRolloverInvalidatesStatusCache(t *testing.T) {
	t.Parallel()

	tracker := newCarryoverTracker(t, nil)

	// One second apart, straddling the fall equinox, well within the status
	// cache's TTL (30s).
	summerBoundary := time.Date(carryoverYear, time.September, 21, 23, 59, 59, 0, time.UTC)
	fallBoundary := time.Date(carryoverYear, time.September, 22, 0, 0, 0, 0, time.UTC)

	// Cache a summer-season status for a species the rollover below never
	// touches directly.
	cached := tracker.GetSpeciesStatus(carryoverNewcomer, summerBoundary)
	require.Equal(t, "summer", cached.CurrentSeason)

	// A different species crossing the boundary triggers the live rollover.
	tracker.UpdateSpecies(carryoverResident, fallBoundary)

	// Querying the cached species again, still inside the cache TTL, must
	// reflect the new season rather than the stale cached entry.
	status := tracker.GetSpeciesStatus(carryoverNewcomer, fallBoundary)
	assert.Equal(t, "fall", status.CurrentSeason, "season rollover must invalidate the status cache")
}

func TestLoadSeasonCarryoverFromDatabase_PreservesOnEmptyResult(t *testing.T) {
	t.Parallel()

	ds := &carryoverHistoryDatastore{
		detectionDates: []datastore.SpeciesDetectionDate{
			{ScientificName: carryoverResident, Date: "2026-09-21"},
		},
	}
	tracker := newCarryoverTracker(t, ds)
	now := carryoverDate(time.September, 29)

	require.NoError(t, tracker.loadSeasonCarryoverFromDatabase(t.Context(), now))
	_, seenAfterFirstLoad := tracker.seasonCarryover[carryoverResident]
	require.True(t, seenAfterFirstLoad, "first load should record the resident species")

	// A later sync for the same season boundary returns no rows (e.g. a
	// transient read); the existing carry-over set must survive it.
	ds.detectionDates = nil
	require.NoError(t, tracker.loadSeasonCarryoverFromDatabase(t.Context(), now))

	_, stillSeen := tracker.seasonCarryover[carryoverResident]
	assert.True(t, stillSeen, "an empty re-read for the same season boundary must not wipe existing carry-over data")
}

func TestLoadSeasonCarryoverFromDatabase(t *testing.T) {
	t.Parallel()

	ds := &carryoverHistoryDatastore{
		detectionDates: []datastore.SpeciesDetectionDate{
			{ScientificName: carryoverResident, Date: "2026-09-21"},
		},
	}
	tracker := newCarryoverTracker(t, ds)

	now := carryoverDate(time.September, 29)
	require.NoError(t, tracker.loadSeasonCarryoverFromDatabase(t.Context(), now))
	assert.Equal(t, "2026-09-15", ds.gotStartDate, "lookback starts one window before fall")
	assert.Equal(t, "2026-09-21", ds.gotEndDate, "lookback ends the day before fall")

	tracker.UpdateSpecies(carryoverResident, carryoverDate(time.September, 22))
	tracker.UpdateSpecies(carryoverMigrant, carryoverDate(time.September, 23))

	assert.False(t, tracker.GetSpeciesStatus(carryoverResident, now).IsNewThisSeason)
	assert.True(t, tracker.GetSpeciesStatus(carryoverMigrant, now).IsNewThisSeason)
}

// carryoverHistoryDatastore records the period requested for detection dates.
type carryoverHistoryDatastore struct {
	noveltyHistoryDatastore
	gotStartDate string
	gotEndDate   string
}

func (d *carryoverHistoryDatastore) GetSpeciesDetectionDatesInPeriod(_ context.Context, startDate, endDate string, _, _ int) ([]datastore.SpeciesDetectionDate, error) {
	d.gotStartDate, d.gotEndDate = startDate, endDate
	return d.detectionDates, nil
}
