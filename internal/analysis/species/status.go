// status.go - Species status calculation and caching

package species

import (
	"slices"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/wallclock"
)

// warmingSpeciesStatus is the suppressed status returned while the background
// historical load is in flight. Every "new" flag is false so the warm-up window
// never produces a spurious new-species result; the periods are reported as
// unknown (sentinel -1 / empty season) until the load completes.
//
// FirstSeenTime is deliberately left as the zero value, not currentTime: some
// consumers (the SSE feed and the detections REST handler) derive "is new
// species" as `!FirstSeenTime.IsZero() && note.Date == FirstSeenTime`, so a
// today-dated FirstSeenTime here would flag every live detection as new during
// warm-up, exactly the spurious result this suppression exists to prevent.
func warmingSpeciesStatus(currentTime time.Time) SpeciesStatus {
	return SpeciesStatus{
		FirstSeenTime:     time.Time{},
		IsNew:             false,
		DaysSinceFirst:    -1,
		LastUpdatedTime:   currentTime,
		DaysSinceLastSeen: -1,
		IsNewThisYear:     false,
		IsNewThisSeason:   false,
		DaysThisYear:      -1,
		DaysThisSeason:    -1,
	}
}

// GetSpeciesStatus returns the tracking status for a species with caching for performance
// This method implements cache-first lookup with TTL validation to minimize expensive computations
func (t *SpeciesTracker) GetSpeciesStatus(scientificName string, currentTime time.Time) SpeciesStatus {
	// Suppress new-species status while the background load populates the maps.
	// Checked before t.mu so it never blocks on the lock the loader holds.
	if t.warming.Load() {
		return warmingSpeciesStatus(currentTime)
	}

	// Collapse taxonomic aliases so the status (and its cache entry) keys this taxon
	// identically to its loaded history regardless of which name the caller passed.
	scientificName = canonicalSpeciesName(scientificName)

	t.mu.Lock()
	defer t.mu.Unlock()

	// Check cache first - if valid entry exists within TTL and same tracking year, return it
	if cached, exists := t.statusCache[scientificName]; exists {
		// Check if cache is still valid (within TTL and same tracking year)
		cacheValid := currentTime.Sub(cached.timestamp) < t.cacheTTL
		sameTrackingYear := t.getTrackingYear(currentTime) == t.getTrackingYear(cached.timestamp)

		if cacheValid && sameTrackingYear {
			// Cache hit - return cached result directly
			return cached.status
		}
		// Cache expired or tracking year changed - will recompute and update cache below
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

// calculateDaysSince calculates calendar days between two times, returning at minimum 0.
// It counts dates, each read in its own location, so a DST change does not shift the result.
func calculateDaysSince(currentTime, referenceTime time.Time) int {
	return max(0, wallclock.CalendarDaysBetween(referenceTime, currentTime))
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
//
// A species carried over from the end of the previous season is never "new
// this season"; otherwise every species heard on a season's first day,
// residents included, would be flagged for the whole window.
func (t *SpeciesTracker) applySeasonalStatus(status *SpeciesStatus, firstThisSeason *time.Time, carriedOver bool, currentTime time.Time) {
	if !t.seasonalEnabled {
		return
	}
	if firstThisSeason != nil {
		status.DaysThisSeason = calculateDaysSince(currentTime, *firstThisSeason)
		status.IsNewThisSeason = !carriedOver && status.DaysThisSeason <= t.seasonalWindowDays
	} else {
		status.IsNewThisSeason = !carriedOver
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
		LastUpdatedTime:   currentTime,
		FirstThisYear:     firstThisYear,
		FirstThisSeason:   firstThisSeason,
		CurrentSeason:     currentSeason,
		DaysSinceFirst:    -1,
		DaysSinceLastSeen: -1,
		DaysThisYear:      -1,
		DaysThisSeason:    -1,
	}

	t.applyLifetimeStatus(status, firstSeen, exists, currentTime)
	t.applyYearlyStatus(status, firstThisYear, currentTime)
	t.applySeasonalStatus(status, firstThisSeason, t.isSeasonCarryoverLocked(scientificName, currentSeason, currentTime), currentTime)
	t.applyNoveltyStatus(status, scientificName)

	return *status
}

// applyNoveltyStatus sets the absence-gap field from the active novelty episode.
// DaysSinceLastSeen is the number of days since the species was previously
// detected before the current return; it stays -1 for first-ever detections.
func (t *SpeciesTracker) applyNoveltyStatus(status *SpeciesStatus, scientificName string) {
	if episode, exists := t.noveltyEpisodes[scientificName]; exists {
		status.DaysSinceLastSeen = episode.DaysSinceLastSeen
	}
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
// like getCurrentSeason() only once for the entire batch. It never moves the yearly or
// seasonal periods: a read for any date, past or future, leaves the tracking state where
// live detections put it, and the statuses are what a period reset for that date would give.
func (t *SpeciesTracker) GetBatchSpeciesStatus(scientificNames []string, currentTime time.Time) map[string]SpeciesStatus {
	if len(scientificNames) == 0 {
		return make(map[string]SpeciesStatus)
	}

	// Suppress new-species status while the background load populates the maps.
	if t.warming.Load() {
		results := make(map[string]SpeciesStatus, len(scientificNames))
		for _, scientificName := range scientificNames {
			results[scientificName] = warmingSpeciesStatus(currentTime)
		}
		return results
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Work out the periods once for the entire batch, without changing the tracker
	view := t.statusReadViewLocked(currentTime)

	// Pre-allocate result map with exact capacity
	results := make(map[string]SpeciesStatus, len(scientificNames))

	// Process each species using the cached season information. The result map is
	// keyed by the caller's input name; only the internal lookup is canonicalized so
	// an alias and its canonical name resolve to the same tracked history.
	for _, scientificName := range scientificNames {
		status := t.buildSpeciesStatusLocked(canonicalSpeciesName(scientificName), currentTime, view)
		results[scientificName] = status
	}

	return results
}

// statusReadView is the period state one batch status read sees. It is built
// without changing the tracker, so a read for any date leaves the yearly and
// seasonal periods where live detections put them.
type statusReadView struct {
	season string
	// yearlySetStale is true when the read falls in a later tracking year than
	// currentYear, so the stored yearly set does not apply to it.
	yearlySetStale bool
	// carryoverFromLastSeen is true when the read is in a different season whose
	// occurrence starts after seasonCarryoverStart: carry-over is then answered
	// from last-seen times, as a live rollover would rebuild the set.
	// carryoverStart is that occurrence's start.
	carryoverFromLastSeen bool
	carryoverStart        wallclock.Date
}

// statusReadViewLocked returns the period state a status read for currentTime
// sees, mirroring what checkAndResetPeriods would change without changing it.
// Assumes the caller holds the write lock (getCurrentSeason updates its memo).
func (t *SpeciesTracker) statusReadViewLocked(currentTime time.Time) statusReadView {
	view := statusReadView{
		season:         t.getCurrentSeason(currentTime),
		yearlySetStale: t.yearlyEnabled && t.shouldResetYear(currentTime),
	}
	if t.seasonalEnabled && view.season != t.currentSeason {
		start, ok := t.seasonStartDate(view.season, currentTime)
		if ok && start.Compare(t.seasonCarryoverStart) > 0 {
			view.carryoverFromLastSeen = true
			view.carryoverStart = start
		}
	}
	return view
}

// isSeasonCarryoverInView reports whether the species was already present when
// the view's season began.
func (t *SpeciesTracker) isSeasonCarryoverInView(scientificName string, currentTime time.Time, view statusReadView) bool {
	if !view.carryoverFromLastSeen {
		return t.isSeasonCarryoverLocked(scientificName, view.season, currentTime)
	}
	lastSeen, seen := t.speciesLastSeen[scientificName]
	return seen && t.lastSeenCarriesOver(lastSeen, view.carryoverStart)
}

// buildSpeciesStatusLocked builds a species status from a read-only period view
// without acquiring locks or checking periods. This is used internally by
// GetBatchSpeciesStatus.
// Assumes the caller already holds the mutex lock.
func (t *SpeciesTracker) buildSpeciesStatusLocked(scientificName string, currentTime time.Time, view statusReadView) SpeciesStatus {
	firstSeen, exists := t.speciesFirstSeen[scientificName]
	var firstThisYear *time.Time
	if !view.yearlySetStale {
		firstThisYear = t.getFirstYearlyDetection(scientificName)
	}
	firstThisSeason := t.getFirstSeasonalDetection(scientificName, view.season)

	// Build status struct (cannot reuse statusBuffer in batch operations)
	status := SpeciesStatus{
		LastUpdatedTime:   currentTime,
		FirstThisYear:     firstThisYear,
		FirstThisSeason:   firstThisSeason,
		CurrentSeason:     view.season,
		DaysSinceFirst:    -1,
		DaysSinceLastSeen: -1,
		DaysThisYear:      -1,
		DaysThisSeason:    -1,
	}

	t.applyLifetimeStatus(&status, firstSeen, exists, currentTime)
	t.applyYearlyStatus(&status, firstThisYear, currentTime)
	t.applySeasonalStatus(&status, firstThisSeason, t.isSeasonCarryoverInView(scientificName, currentTime, view), currentTime)
	t.applyNoveltyStatus(&status, scientificName)

	return status
}
