// testing.go - Testing utilities for species tracking
//
// ⚠️  THIS FILE CONTAINS TEST-ONLY METHODS ⚠️
//
// All methods in this file are strictly for testing purposes and include runtime
// guards that will panic if called outside of test execution. These methods
// bypass normal validation and can corrupt internal state if misused.
//
// These methods are exported (rather than in _test.go files) because they are
// used by integration tests in other packages (e.g., internal/analysis/processor).

package species

import (
	"testing"
	"time"
)

// panicIfNotTesting panics if called outside of test execution.
// This is a runtime guard to prevent accidental production usage of test-only methods.
func panicIfNotTesting() {
	if !testing.Testing() {
		panic("species: test-only method called outside of test execution")
	}
}

// SetCurrentYearForTesting sets the current year for testing purposes only.
// Panics if called outside of test execution.
//
// This method bypasses the normal year tracking logic and directly manipulates the internal
// currentYear field, which can lead to inconsistent tracking data if misused.
func (t *SpeciesTracker) SetCurrentYearForTesting(year int) {
	panicIfNotTesting()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.currentYear = year
}

// SetCurrentSeasonForTesting sets the current season for testing purposes only.
// Panics if called outside of test execution.
//
// This method bypasses the normal season detection logic and directly manipulates the internal
// cached season state, which can lead to inconsistent seasonal data if misused.
func (t *SpeciesTracker) SetCurrentSeasonForTesting(season string) {
	panicIfNotTesting()
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	t.cachedSeason = season
	t.seasonCacheTime = now    // When we cached it (for TTL check)
	t.seasonCacheForTime = now // Input time for which we cached (for period check)
}

// IsSeasonMapInitialized checks if the season map is properly initialized for the given season.
// Panics if called outside of test execution.
func (t *SpeciesTracker) IsSeasonMapInitialized(season string) bool {
	panicIfNotTesting()
	t.mu.RLock()
	defer t.mu.RUnlock()

	if !t.seasonalEnabled {
		return false
	}

	return t.speciesBySeason != nil && t.speciesBySeason[season] != nil
}

// GetSeasonMapCount returns the number of species tracked for the given season.
// Panics if called outside of test execution.
func (t *SpeciesTracker) GetSeasonMapCount(season string) int {
	panicIfNotTesting()
	t.mu.RLock()
	defer t.mu.RUnlock()

	if !t.seasonalEnabled || t.speciesBySeason == nil || t.speciesBySeason[season] == nil {
		return 0
	}

	return len(t.speciesBySeason[season])
}

// ExpireCacheForTesting forces cache expiration for the given species for testing purposes.
// Panics if called outside of test execution.
func (t *SpeciesTracker) ExpireCacheForTesting(scientificName string) {
	panicIfNotTesting()
	t.mu.Lock()
	defer t.mu.Unlock()

	if cached, exists := t.statusCache[scientificName]; exists {
		// Set timestamp to expired (1 hour ago)
		cached.timestamp = time.Now().Add(defaultCacheExpiredAge)
		t.statusCache[scientificName] = cached
	}
}

// ClearCacheForTesting clears the entire status cache for testing purposes.
// Panics if called outside of test execution.
func (t *SpeciesTracker) ClearCacheForTesting() {
	panicIfNotTesting()
	t.mu.Lock()
	defer t.mu.Unlock()

	t.statusCache = make(map[string]cachedSpeciesStatus)
}
