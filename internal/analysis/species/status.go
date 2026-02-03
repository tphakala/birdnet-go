// status.go - Species status calculation and caching

package species

import (
	"slices"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// GetSpeciesStatus returns the tracking status for a species with caching for performance
// This method implements cache-first lookup with TTL validation to minimize expensive computations
func (t *SpeciesTracker) GetSpeciesStatus(scientificName string, currentTime time.Time) SpeciesStatus {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check cache first - if valid entry exists within TTL and same year, return it
	if cached, exists := t.statusCache[scientificName]; exists {
		// Check if cache is still valid (within TTL and same year)
		cacheValid := currentTime.Sub(cached.timestamp) < t.cacheTTL
		sameYear := currentTime.Year() == cached.timestamp.Year()

		if cacheValid && sameYear {
			// Cache hit - return cached result directly
			return cached.status
		}
		// Cache expired or year changed - will recompute and update cache below
	}

	// Perform periodic cache cleanup to prevent unbounded growth
	// The cleanup function will check if enough time has passed
	t.cleanupExpiredCache(currentTime)

	// Cache miss or expired - compute fresh status
	t.checkAndResetPeriods(currentTime)
	currentSeason := t.getCurrentSeason(currentTime)

	// Build fresh status using the same logic as buildSpeciesStatusLocked but with buffer reuse
	status := t.buildSpeciesStatusWithBuffer(scientificName, currentTime, currentSeason)

	// Log computed status for debugging
	getLog().Debug("Species status computed",
		logger.String("species", scientificName),
		logger.String("current_time", currentTime.Format(time.DateTime)),
		logger.String("current_season", currentSeason),
		logger.Bool("is_new", status.IsNew),
		logger.Bool("is_new_this_year", status.IsNewThisYear),
		logger.Bool("is_new_this_season", status.IsNewThisSeason),
		logger.Int("days_since_first", status.DaysSinceFirst),
		logger.Int("days_this_year", status.DaysThisYear),
		logger.Int("days_this_season", status.DaysThisSeason))

	// Cache the computed result for future requests
	t.statusCache[scientificName] = cachedSpeciesStatus{
		status:    status,
		timestamp: currentTime,
	}

	return status
}

// getFirstYearlyDetection returns a pointer to the first detection time this year for a species.
// Returns nil if yearly tracking is disabled or species not seen this year.
func (t *SpeciesTracker) getFirstYearlyDetection(scientificName string) *time.Time {
	if !t.yearlyEnabled {
		return nil
	}
	if yearTime, exists := t.speciesThisYear[scientificName]; exists {
		timeCopy := yearTime
		return &timeCopy
	}
	return nil
}

// getFirstSeasonalDetection returns a pointer to the first detection time this season for a species.
// Returns nil if seasonal tracking is disabled or species not seen this season.
func (t *SpeciesTracker) getFirstSeasonalDetection(scientificName, currentSeason string) *time.Time {
	if !t.seasonalEnabled || t.speciesBySeason[currentSeason] == nil {
		return nil
	}
	if seasonTime, exists := t.speciesBySeason[currentSeason][scientificName]; exists {
		timeCopy := seasonTime
		return &timeCopy
	}
	return nil
}

// calculateDaysSince calculates days between two times, returning at minimum 0.
func calculateDaysSince(currentTime, referenceTime time.Time) int {
	days := int(currentTime.Sub(referenceTime).Hours() / hoursPerDay)
	return max(0, days)
}

// applyLifetimeStatus sets lifetime tracking fields on status.
func (t *SpeciesTracker) applyLifetimeStatus(status *SpeciesStatus, firstSeen time.Time, exists bool, currentTime time.Time) {
	if exists {
		status.FirstSeenTime = firstSeen
		status.DaysSinceFirst = calculateDaysSince(currentTime, firstSeen)
		status.IsNew = status.DaysSinceFirst <= t.windowDays
	} else {
		status.FirstSeenTime = currentTime
		status.IsNew = true
		status.DaysSinceFirst = 0
	}
}

// applyYearlyStatus sets yearly tracking fields on status.
func (t *SpeciesTracker) applyYearlyStatus(status *SpeciesStatus, firstThisYear *time.Time, currentTime time.Time) {
	if !t.yearlyEnabled {
		return
	}
	if firstThisYear != nil {
		status.DaysThisYear = calculateDaysSince(currentTime, *firstThisYear)
		status.IsNewThisYear = status.DaysThisYear <= t.yearlyWindowDays
	} else {
		status.IsNewThisYear = true
		status.DaysThisYear = 0
	}
}

// applySeasonalStatus sets seasonal tracking fields on status.
func (t *SpeciesTracker) applySeasonalStatus(status *SpeciesStatus, firstThisSeason *time.Time, currentTime time.Time) {
	if !t.seasonalEnabled {
		return
	}
	if firstThisSeason != nil {
		status.DaysThisSeason = calculateDaysSince(currentTime, *firstThisSeason)
		status.IsNewThisSeason = status.DaysThisSeason <= t.seasonalWindowDays
	} else {
		status.IsNewThisSeason = true
		status.DaysThisSeason = 0
	}
}

// buildSpeciesStatusWithBuffer builds species status reusing the pre-allocated buffer
// This method is used by GetSpeciesStatus to maintain the buffer optimization
func (t *SpeciesTracker) buildSpeciesStatusWithBuffer(scientificName string, currentTime time.Time, currentSeason string) SpeciesStatus {
	firstSeen, exists := t.speciesFirstSeen[scientificName]
	firstThisYear := t.getFirstYearlyDetection(scientificName)
	firstThisSeason := t.getFirstSeasonalDetection(scientificName, currentSeason)

	// Reuse the pre-allocated status buffer
	status := &t.statusBuffer
	*status = SpeciesStatus{
		LastUpdatedTime: currentTime,
		FirstThisYear:   firstThisYear,
		FirstThisSeason: firstThisSeason,
		CurrentSeason:   currentSeason,
		DaysSinceFirst:  -1,
		DaysThisYear:    -1,
		DaysThisSeason:  -1,
	}

	t.applyLifetimeStatus(status, firstSeen, exists, currentTime)
	t.applyYearlyStatus(status, firstThisYear, currentTime)
	t.applySeasonalStatus(status, firstThisSeason, currentTime)

	return *status
}

// cleanupExpiredCache removes expired entries and enforces size limits with LRU eviction
func (t *SpeciesTracker) cleanupExpiredCache(currentTime time.Time) {
	t.cleanupExpiredCacheWithForce(currentTime, false)
}

// collectExpiredCacheKeys returns keys of cache entries that have expired.
func (t *SpeciesTracker) collectExpiredCacheKeys(currentTime time.Time) []string {
	expiredKeys := make([]string, 0)
	for scientificName := range t.statusCache {
		if currentTime.Sub(t.statusCache[scientificName].timestamp) >= t.cacheTTL {
			expiredKeys = append(expiredKeys, scientificName)
		}
	}
	return expiredKeys
}

// cacheEntry represents a cache entry for LRU sorting
type cacheEntry struct {
	name      string
	timestamp time.Time
}

// evictOldestCacheEntries removes the oldest cache entries to bring cache size to target.
func (t *SpeciesTracker) evictOldestCacheEntries() {
	if len(t.statusCache) <= targetCacheSize {
		return
	}

	// Create a slice of entries for sorting
	entries := make([]cacheEntry, 0, len(t.statusCache))
	for name := range t.statusCache {
		entries = append(entries, cacheEntry{name: name, timestamp: t.statusCache[name].timestamp})
	}

	// Sort by timestamp (oldest first) using efficient sort
	slices.SortFunc(entries, func(a, b cacheEntry) int {
		return a.timestamp.Compare(b.timestamp)
	})

	// Remove oldest entries until we're at target size
	entriesToRemove := len(t.statusCache) - targetCacheSize
	for i := 0; i < entriesToRemove && i < len(entries); i++ {
		delete(t.statusCache, entries[i].name)
	}

	getLog().Debug("Cache cleanup completed",
		logger.Int("removed_count", entriesToRemove),
		logger.Int("remaining_count", len(t.statusCache)))
}

// cleanupExpiredCacheWithForce allows forcing cleanup even if recently performed (for testing)
func (t *SpeciesTracker) cleanupExpiredCacheWithForce(currentTime time.Time, force bool) {
	// Skip if recently performed (unless forced)
	if !force && currentTime.Sub(t.lastCacheCleanup) <= t.cacheTTL*cacheCleanupIntervalMultiple {
		return
	}

	// First pass: remove expired entries
	for _, key := range t.collectExpiredCacheKeys(currentTime) {
		delete(t.statusCache, key)
	}

	// Second pass: if still over limit, remove oldest entries (LRU)
	t.evictOldestCacheEntries()

	// Update cleanup timestamp
	t.lastCacheCleanup = currentTime
}

// GetBatchSpeciesStatus returns the tracking status for multiple species in a single operation
// This method significantly reduces mutex contention and redundant computations compared to
// calling GetSpeciesStatus individually for each species. It performs expensive operations
// like checkAndResetPeriods() and getCurrentSeason() only once for the entire batch.
func (t *SpeciesTracker) GetBatchSpeciesStatus(scientificNames []string, currentTime time.Time) map[string]SpeciesStatus {
	if len(scientificNames) == 0 {
		return make(map[string]SpeciesStatus)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Perform expensive operations only once for the entire batch
	t.checkAndResetPeriods(currentTime)
	currentSeason := t.getCurrentSeason(currentTime)

	// Pre-allocate result map with exact capacity
	results := make(map[string]SpeciesStatus, len(scientificNames))

	// Process each species using the cached season information
	for _, scientificName := range scientificNames {
		status := t.buildSpeciesStatusLocked(scientificName, currentTime, currentSeason)
		results[scientificName] = status
	}

	return results
}

// buildSpeciesStatusLocked builds a species status without acquiring locks or performing
// expensive period checks. This is used internally by GetBatchSpeciesStatus.
// Assumes the caller already holds the mutex lock.
func (t *SpeciesTracker) buildSpeciesStatusLocked(scientificName string, currentTime time.Time, currentSeason string) SpeciesStatus {
	firstSeen, exists := t.speciesFirstSeen[scientificName]
	firstThisYear := t.getFirstYearlyDetection(scientificName)
	firstThisSeason := t.getFirstSeasonalDetection(scientificName, currentSeason)

	// Build status struct (cannot reuse statusBuffer in batch operations)
	status := SpeciesStatus{
		LastUpdatedTime: currentTime,
		FirstThisYear:   firstThisYear,
		FirstThisSeason: firstThisSeason,
		CurrentSeason:   currentSeason,
		DaysSinceFirst:  -1,
		DaysThisYear:    -1,
		DaysThisSeason:  -1,
	}

	t.applyLifetimeStatus(&status, firstSeen, exists, currentTime)
	t.applyYearlyStatus(&status, firstThisYear, currentTime)
	t.applySeasonalStatus(&status, firstThisSeason, currentTime)

	return status
}
