package species

import (
	"maps"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/wallclock"
)

const (
	readSeenEarly    = "Parus major"        // last heard before the next season's lookback
	readSeenLate     = "Erithacus rubecula" // last heard inside the next season's lookback
	readNeverSeen    = "Turdus merula"      // no detections at all
	readResetMonth   = int(time.July)       // tracking year starts July 1
	readWindowDays   = 7                    // seasonal and yearly window in days
	readTrackingYear = 2026                 // tracking year of the fixture detections
)

// readTestSettings enables yearly tracking with a July 1 reset, so the tracking
// year differs from the calendar year, and the standard seasons.
func readTestSettings() *conf.SpeciesTrackingSettings {
	return &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: readWindowDays,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			WindowDays: readWindowDays,
			ResetMonth: readResetMonth,
			ResetDay:   1,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: readWindowDays,
			Seasons:    carryoverSeasons(),
		},
	}
}

// newReadTestTracker returns a tracker built in summer 2026 that has seen
// readSeenEarly on September 10 and readSeenLate on September 18. The fall
// carry-over lookback (window 7, fall starts September 22) begins September 15.
func newReadTestTracker() *SpeciesTracker {
	tracker := newTrackerFromSettingsAt(nil, readTestSettings(), carryoverDate(time.September, 10))
	tracker.UpdateSpecies(readSeenEarly, carryoverDate(time.September, 10))
	tracker.UpdateSpecies(readSeenLate, carryoverDate(time.September, 18))
	return tracker
}

// periodSnapshot is the period state a read must leave untouched.
type periodSnapshot struct {
	currentYear          int
	currentSeason        string
	speciesThisYear      map[string]time.Time
	speciesBySeason      map[string]map[string]time.Time
	seasonCarryover      map[string]struct{}
	seasonCarryoverStart wallclock.Date
	statusCacheLen       int
}

func snapshotPeriods(tracker *SpeciesTracker) periodSnapshot {
	tracker.mu.RLock()
	defer tracker.mu.RUnlock()

	bySeason := make(map[string]map[string]time.Time, len(tracker.speciesBySeason))
	for season, species := range tracker.speciesBySeason {
		bySeason[season] = maps.Clone(species)
	}
	return periodSnapshot{
		currentYear:          tracker.currentYear,
		currentSeason:        tracker.currentSeason,
		speciesThisYear:      maps.Clone(tracker.speciesThisYear),
		speciesBySeason:      bySeason,
		seasonCarryover:      maps.Clone(tracker.seasonCarryover),
		seasonCarryoverStart: tracker.seasonCarryoverStart,
		statusCacheLen:       len(tracker.statusCache),
	}
}

func TestGetBatchSpeciesStatus_ReadDoesNotMovePeriods(t *testing.T) {
	t.Parallel()

	names := []string{readSeenEarly, readSeenLate, readNeverSeen}

	tests := []struct {
		name string
		at   time.Time
	}{
		{"same_day_as_last_detection", carryoverDate(time.September, 18)},
		{"previous_season", carryoverDate(time.May, 1)},
		{"next_season_answers_carryover_from_last_seen", carryoverDate(time.September, 29)},
		{"next_tracking_year_sees_empty_yearly_set", time.Date(carryoverYear+1, time.July, 5, carryoverHour, 0, 0, 0, time.UTC)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			batch := newReadTestTracker()
			require.Equal(t, readTrackingYear, batch.currentYear, "fixture sits in the tracking year")
			before := snapshotPeriods(batch)

			got := batch.GetBatchSpeciesStatus(names, tt.at)

			// The single-species read on an identical tracker is the reference:
			// it advances the periods the way a live read would.
			reference := newReadTestTracker()
			for _, name := range names {
				assert.Equal(t, reference.GetSpeciesStatus(name, tt.at), got[name], "status of %s", name)
			}

			assert.Equal(t, before, snapshotPeriods(batch), "a batch read must not move the periods")
		})
	}
}

func TestLastSeenCarriesOver_Boundaries(t *testing.T) {
	t.Parallel()

	tracker := newTrackerFromSettingsAt(nil, readTestSettings(), carryoverDate(time.September, 10))
	fallStart := wallclock.DateOf(carryoverDate(time.September, 22))

	assert.False(t, tracker.lastSeenCarriesOver(carryoverDate(time.September, 14), fallStart), "day before the lookback")
	assert.True(t, tracker.lastSeenCarriesOver(carryoverDate(time.September, 15), fallStart), "first day of the lookback")
	assert.True(t, tracker.lastSeenCarriesOver(carryoverDate(time.September, 21), fallStart), "day before the season")
	assert.False(t, tracker.lastSeenCarriesOver(carryoverDate(time.September, 22), fallStart), "first day of the season")
}
