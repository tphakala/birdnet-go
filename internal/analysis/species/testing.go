// testing.go - Testing utilities for species tracking

package species

import (
	"time"
)

// SetCurrentYearForTesting sets the current year for testing purposes only.
//
// ⚠️  WARNING: THIS METHOD IS STRICTLY FOR TESTING AND SHOULD NEVER BE USED IN PRODUCTION CODE ⚠️
//
// This method bypasses the normal year tracking logic and directly manipulates the internal
// currentYear field, which can lead to:
// - Inconsistent tracking data between lifetime, yearly, and seasonal periods
// - Cache invalidation issues that may cause incorrect species status calculations
// - Data corruption if the year doesn't match the actual system time
// - Broken yearly reset logic that relies on time-based transitions
//
// Using this method in production code will result in unpredictable behavior and should be
// avoided at all costs. It exists solely to enable controlled testing scenarios where
// specific year boundaries need to be simulated.
//
// This method provides controlled access to the currentYear field for test scenarios only.
func (t *SpeciesTracker) SetCurrentYearForTesting(year int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.currentYear = year
}

// SetCurrentSeasonForTesting sets the current season for testing purposes only.
//
// ⚠️  WARNING: THIS METHOD IS STRICTLY FOR TESTING AND SHOULD NEVER BE USED IN PRODUCTION CODE ⚠️
//
// This method bypasses the normal season detection logic and directly manipulates the internal
// cached season state, which can lead to:
// - Incorrect seasonal tracking calculations that don't match the actual time of year
// - Inconsistent seasonal data that doesn't align with other tracking periods
// - Cache corruption if the season doesn't match the actual system time
// - Broken seasonal reset logic that relies on time-based transitions
//
// Use this method only in controlled test environments where you need to simulate
// specific seasonal tracking scenarios.
//
// This method provides controlled access to the season cache for test scenarios only.
func (t *SpeciesTracker) SetCurrentSeasonForTesting(season string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	t.cachedSeason = season
	t.seasonCacheTime = now    // When we cached it (for TTL check)
	t.seasonCacheForTime = now // Input time for which we cached (for period check)
}

// IsSeasonMapInitialized checks if the season map is properly initialized for the given season.
// This method provides safe access to internal state for testing purposes.
func (t *SpeciesTracker) IsSeasonMapInitialized(season string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if !t.seasonalEnabled {
		return false
	}

	return t.speciesBySeason != nil && t.speciesBySeason[season] != nil
}

// GetSeasonMapCount returns the number of species tracked for the given season.
// This method provides safe access to internal state for testing purposes.
func (t *SpeciesTracker) GetSeasonMapCount(season string) int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if !t.seasonalEnabled || t.speciesBySeason == nil || t.speciesBySeason[season] == nil {
		return 0
	}

	return len(t.speciesBySeason[season])
}

// ExpireCacheForTesting forces cache expiration for the given species for testing purposes.
// This method should only be used in tests to simulate cache expiration without
// manipulating internal state directly.
func (t *SpeciesTracker) ExpireCacheForTesting(scientificName string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if cached, exists := t.statusCache[scientificName]; exists {
		// Set timestamp to expired (1 hour ago)
		cached.timestamp = time.Now().Add(defaultCacheExpiredAge)
		t.statusCache[scientificName] = cached
	}
}

// ClearCacheForTesting clears the entire status cache for testing purposes.
// This method should only be used in tests.
func (t *SpeciesTracker) ClearCacheForTesting() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.statusCache = make(map[string]cachedSpeciesStatus)
}
