package species

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPruneYearlyTimezoneWestOfUTC verifies that species first detected on the
// year start date are NOT pruned when the system runs in a timezone west of UTC.
//
// Bug: loadYearlyDataFromDatabase parses dates with time.Parse (UTC midnight),
// but pruneYearlyEntriesLocked computes the cutoff with now.Location() (local
// midnight). For timezones west of UTC, local midnight is a later instant than
// UTC midnight of the same date, so "Jan 1 00:00 UTC".Before("Jan 1 00:00 EST")
// is true and the entry is incorrectly pruned.
func TestPruneYearlyTimezoneWestOfUTC(t *testing.T) {
	t.Parallel()

	eastern, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	tracker := &SpeciesTracker{
		yearlyEnabled:   true,
		resetMonth:      1,
		resetDay:        1,
		currentYear:     2026,
		speciesThisYear: make(map[string]time.Time),
	}

	// Simulate DB-loaded date: time.Parse returns UTC midnight
	jan1UTC, _ := time.Parse(time.DateOnly, "2026-01-01")
	tracker.speciesThisYear["Cyanocitta cristata"] = jan1UTC // Blue Jay
	tracker.speciesThisYear["Spinus tristis"] = jan1UTC      // American Goldfinch

	// Also add a species first seen later (should never be pruned)
	mar15UTC, _ := time.Parse(time.DateOnly, "2026-03-15")
	tracker.speciesThisYear["Turdus migratorius"] = mar15UTC // American Robin

	// Prune from the perspective of May 6 in US Eastern timezone
	nowEastern := time.Date(2026, 5, 6, 10, 0, 0, 0, eastern)
	pruned := tracker.pruneYearlyEntriesLocked(nowEastern)

	assert.Equal(t, 0, pruned, "no current-year species should be pruned")
	assert.Len(t, tracker.speciesThisYear, 3,
		"all three species should remain in the yearly tracking map")
	assert.Contains(t, tracker.speciesThisYear, "Cyanocitta cristata",
		"Blue Jay (first seen Jan 1) must not be pruned")
	assert.Contains(t, tracker.speciesThisYear, "Spinus tristis",
		"American Goldfinch (first seen Jan 1) must not be pruned")
	assert.Contains(t, tracker.speciesThisYear, "Turdus migratorius",
		"American Robin (first seen Mar 15) must not be pruned")
}

// TestPruneYearlyTimezoneMultipleOffsets verifies the fix across several
// western timezones with varying UTC offsets.
func TestPruneYearlyTimezoneMultipleOffsets(t *testing.T) {
	t.Parallel()

	timezones := []string{
		"America/New_York",    // UTC-5 / UTC-4
		"America/Chicago",     // UTC-6 / UTC-5
		"America/Denver",      // UTC-7 / UTC-6
		"America/Los_Angeles", // UTC-8 / UTC-7
		"America/Anchorage",   // UTC-9 / UTC-8
		"Pacific/Honolulu",    // UTC-10
	}

	for _, tzName := range timezones {
		t.Run(tzName, func(t *testing.T) {
			t.Parallel()

			loc, err := time.LoadLocation(tzName)
			require.NoError(t, err)

			tracker := &SpeciesTracker{
				yearlyEnabled:   true,
				resetMonth:      1,
				resetDay:        1,
				currentYear:     2026,
				speciesThisYear: make(map[string]time.Time),
			}

			// Simulate DB-loaded Jan 1 date (UTC midnight)
			jan1UTC, _ := time.Parse(time.DateOnly, "2026-01-01")
			tracker.speciesThisYear["Corvus brachyrhynchos"] = jan1UTC

			now := time.Date(2026, 5, 6, 12, 0, 0, 0, loc)
			pruned := tracker.pruneYearlyEntriesLocked(now)

			assert.Equal(t, 0, pruned,
				"species first seen on year start date must not be pruned in %s", tzName)
			assert.Contains(t, tracker.speciesThisYear, "Corvus brachyrhynchos",
				"American Crow must remain in yearly map for %s", tzName)
		})
	}
}

// TestPruneYearlyStillRemovesPreviousYearEntries confirms that entries from a
// previous tracking year ARE correctly pruned (the fix must not break this).
func TestPruneYearlyStillRemovesPreviousYearEntries(t *testing.T) {
	t.Parallel()

	eastern, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	tracker := &SpeciesTracker{
		yearlyEnabled:   true,
		resetMonth:      1,
		resetDay:        1,
		currentYear:     2026,
		speciesThisYear: make(map[string]time.Time),
	}

	// Entry from previous year (should be pruned)
	dec15UTC, _ := time.Parse(time.DateOnly, "2025-12-15")
	tracker.speciesThisYear["Passer domesticus"] = dec15UTC // House Sparrow

	// Entry from current year (should NOT be pruned)
	jan15UTC, _ := time.Parse(time.DateOnly, "2026-01-15")
	tracker.speciesThisYear["Sitta pusilla"] = jan15UTC // Brown-headed Nuthatch

	now := time.Date(2026, 5, 6, 10, 0, 0, 0, eastern)
	pruned := tracker.pruneYearlyEntriesLocked(now)

	assert.Equal(t, 1, pruned, "only the previous-year entry should be pruned")
	assert.NotContains(t, tracker.speciesThisYear, "Passer domesticus",
		"House Sparrow (Dec 2025) should be pruned")
	assert.Contains(t, tracker.speciesThisYear, "Sitta pusilla",
		"Brown-headed Nuthatch (Jan 2026) should remain")
}

// TestPruneYearlyCustomResetDateTimezone verifies the timezone fix with a
// non-January-1 reset date (e.g., July 1 for southern hemisphere users).
func TestPruneYearlyCustomResetDateTimezone(t *testing.T) {
	t.Parallel()

	pacific, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	tracker := &SpeciesTracker{
		yearlyEnabled:   true,
		resetMonth:      7,
		resetDay:        1,
		currentYear:     2026,
		speciesThisYear: make(map[string]time.Time),
	}

	// Species first seen on the custom reset date (July 1), loaded from DB as UTC midnight
	jul1UTC, _ := time.Parse(time.DateOnly, "2026-07-01")
	tracker.speciesThisYear["Zenaida macroura"] = jul1UTC // Mourning Dove

	// Species from before the reset date (should be pruned)
	jun15UTC, _ := time.Parse(time.DateOnly, "2026-06-15")
	tracker.speciesThisYear["Sturnus vulgaris"] = jun15UTC // European Starling

	now := time.Date(2026, 8, 15, 10, 0, 0, 0, pacific)
	pruned := tracker.pruneYearlyEntriesLocked(now)

	assert.Equal(t, 1, pruned, "only the pre-reset entry should be pruned")
	assert.Contains(t, tracker.speciesThisYear, "Zenaida macroura",
		"Mourning Dove (first seen on reset date Jul 1) must not be pruned")
	assert.NotContains(t, tracker.speciesThisYear, "Sturnus vulgaris",
		"European Starling (before reset date) should be pruned")
}

// TestIsSeasonOldTimezoneWestOfUTC verifies that isSeasonOld uses date-only
// comparison. A species detected the day AFTER the cutoff date must keep the
// season alive, even when the UTC-midnight timestamp is before the cutoff
// instant in local time.
func TestIsSeasonOldTimezoneWestOfUTC(t *testing.T) {
	t.Parallel()

	eastern, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	// Species detected on May 7, 2025 (loaded from DB as UTC midnight)
	may7UTC, _ := time.Parse(time.DateOnly, "2025-05-07")
	seasonMap := map[string]time.Time{
		"Setophaga petechia": may7UTC, // Yellow Warbler
	}

	// now = May 7, 2026 10:00 Eastern; cutoff = May 7, 2025 10:00 Eastern
	// In UTC: cutoff = May 7, 2025 14:00 UTC
	// Bug: May 7 00:00 UTC.After(May 7 14:00 UTC) = false, so the season
	// is incorrectly considered old despite having the same calendar date.
	now := time.Date(2026, 5, 7, 10, 0, 0, 0, eastern)
	cutoff := now.AddDate(-1, 0, 0)

	result := isSeasonOld(seasonMap, cutoff)
	assert.False(t, result,
		"season with entries on the cutoff calendar date should not be considered old")
}

// TestPruneLifetimeTimezoneWestOfUTC verifies the lifetime pruning also uses
// date-safe comparison. The lifetime cutoff is 10 years ago, so this is less
// likely to trigger in practice, but the same pattern should be consistent.
func TestPruneLifetimeTimezoneWestOfUTC(t *testing.T) {
	t.Parallel()

	eastern, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	tracker := &SpeciesTracker{
		speciesFirstSeen: make(map[string]time.Time),
	}

	// Species first seen exactly 10 years ago (the cutoff boundary)
	// Loaded from DB as UTC midnight
	boundaryDate, _ := time.Parse(time.DateOnly, "2016-05-06")
	tracker.speciesFirstSeen["Melospiza melodia"] = boundaryDate // Song Sparrow

	// Species from 10 years + 1 day ago (should be pruned)
	oldDate, _ := time.Parse(time.DateOnly, "2016-05-05")
	tracker.speciesFirstSeen["Agelaius phoeniceus"] = oldDate // Red-winged Blackbird

	now := time.Date(2026, 5, 6, 10, 0, 0, 0, eastern)
	pruned := tracker.pruneLifetimeEntriesLocked(now)

	assert.Equal(t, 1, pruned, "only the entry older than 10 years should be pruned")
	assert.Contains(t, tracker.speciesFirstSeen, "Melospiza melodia",
		"Song Sparrow (exactly at cutoff) must not be pruned")
	assert.NotContains(t, tracker.speciesFirstSeen, "Agelaius phoeniceus",
		"Red-winged Blackbird (older than cutoff) should be pruned")
}
