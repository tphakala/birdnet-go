// update.go - Species tracking update operations

package species

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// updateLifetimeTrackingLocked updates lifetime tracking data. Returns true if this is a new species.
// Assumes lock is held.
func (t *SpeciesTracker) updateLifetimeTrackingLocked(scientificName string, detectionTime time.Time) bool {
	firstSeen, exists := t.speciesFirstSeen[scientificName]

	if !exists {
		t.speciesFirstSeen[scientificName] = detectionTime
		getLog().Debug("New lifetime species detected",
			logger.String("species", scientificName),
			logger.String("detection_time", detectionTime.Format(time.DateTime)))
		return true
	}

	if detectionTime.Before(firstSeen) {
		t.speciesFirstSeen[scientificName] = detectionTime
		getLog().Debug("Updated lifetime first seen to earlier date",
			logger.String("species", scientificName),
			logger.String("old_date", firstSeen.Format(time.DateOnly)),
			logger.String("new_date", detectionTime.Format(time.DateOnly)))
	}
	return false
}

// updateLastSeenLocked updates the most recent detection time for a species.
// Assumes lock is held.
func (t *SpeciesTracker) updateLastSeenLocked(scientificName string, detectionTime time.Time) {
	lastSeen, exists := t.speciesLastSeen[scientificName]
	if !exists || detectionTime.After(lastSeen) {
		t.speciesLastSeen[scientificName] = detectionTime
	}
}

// inactiveNoveltyStatus returns the sentinel status used when no novelty
// episode is active for the current detection.
func inactiveNoveltyStatus(daysSinceLastSeen int) NoveltyStatus {
	return NoveltyStatus{
		DaysSinceLastSeen:    daysSinceLastSeen,
		NoveltyEpisodeDays:   inactiveNoveltyValue,
		NoveltyEpisodeActive: false,
	}
}

// calculateNoveltyStatusLocked calculates and updates novelty episode state.
// It must be called before speciesLastSeen is updated for the current detection.
// Assumes lock is held.
func (t *SpeciesTracker) calculateNoveltyStatusLocked(scientificName string, detectionTime time.Time) NoveltyStatus {
	daysSinceLastSeen := inactiveNoveltyValue
	if lastSeen, exists := t.speciesLastSeen[scientificName]; exists {
		daysSinceLastSeen = calculateDaysSince(detectionTime, lastSeen)
	}

	if episode, exists := t.noveltyEpisodes[scientificName]; exists {
		episode.DaysSinceLastSeen = daysSinceLastSeen
		daysActive := calculateDaysSince(detectionTime, episode.NoveltyEpisodeStart)
		if daysActive <= t.windowDays {
			t.noveltyEpisodes[scientificName] = episode
			return episode
		}
		delete(t.noveltyEpisodes, scientificName)
	}

	var status NoveltyStatus
	switch {
	case daysSinceLastSeen == inactiveNoveltyValue:
		status = NoveltyStatus{
			DaysSinceLastSeen:    inactiveNoveltyValue,
			NoveltyEpisodeDays:   firstEverNoveltyEpisodeDays,
			NoveltyEpisodeStart:  detectionTime,
			NoveltyEpisodeActive: true,
		}
	case daysSinceLastSeen > 0:
		status = NoveltyStatus{
			DaysSinceLastSeen:    daysSinceLastSeen,
			NoveltyEpisodeDays:   daysSinceLastSeen,
			NoveltyEpisodeStart:  detectionTime,
			NoveltyEpisodeActive: true,
		}
	default:
		return inactiveNoveltyStatus(daysSinceLastSeen)
	}

	t.noveltyEpisodes[scientificName] = status
	return status
}

// updateYearlyTrackingLocked updates yearly tracking data. Assumes lock is held.
func (t *SpeciesTracker) updateYearlyTrackingLocked(scientificName string, detectionTime time.Time) {
	if !t.yearlyEnabled || !t.isWithinCurrentYear(detectionTime) {
		return
	}

	existingTime, yearExists := t.speciesThisYear[scientificName]
	if !yearExists {
		t.speciesThisYear[scientificName] = detectionTime
		getLog().Debug("New species for this year",
			logger.String("species", scientificName),
			logger.String("detection_time", detectionTime.Format(time.DateTime)),
			logger.Int("current_year", t.currentYear))
	} else if detectionTime.Before(existingTime) {
		t.speciesThisYear[scientificName] = detectionTime
		getLog().Debug("Updated yearly first seen to earlier date",
			logger.String("species", scientificName),
			logger.String("old_date", existingTime.Format(time.DateOnly)),
			logger.String("new_date", detectionTime.Format(time.DateOnly)),
			logger.Int("current_year", t.currentYear))
	}
}

// updateSeasonalTrackingLocked updates seasonal tracking data. Assumes lock is held.
func (t *SpeciesTracker) updateSeasonalTrackingLocked(scientificName string, detectionTime time.Time) {
	if !t.seasonalEnabled {
		return
	}

	currentSeason := t.getCurrentSeason(detectionTime)
	if t.speciesBySeason[currentSeason] == nil {
		t.speciesBySeason[currentSeason] = make(map[string]time.Time)
	}

	if _, seasonExists := t.speciesBySeason[currentSeason][scientificName]; !seasonExists {
		t.speciesBySeason[currentSeason][scientificName] = detectionTime
		getLog().Debug("New species for this season",
			logger.String("species", scientificName),
			logger.String("season", currentSeason),
			logger.String("detection_time", detectionTime.Format(time.DateTime)))
	}
}

// UpdateSpecies updates the first seen time for a species if necessary
// Returns true if this is a new species detection
func (t *SpeciesTracker) UpdateSpecies(scientificName string, detectionTime time.Time) bool {
	// While the background load is in flight the maps are not yet populated, so
	// every species would look new. Suppress (return not-new) and skip recording;
	// the detection is still persisted by the caller and tracked from the next one.
	if t.warming.Load() {
		return false
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Check and reset periods if needed
	t.checkAndResetPeriods(detectionTime)

	// Update all tracking systems
	isNewSpecies := t.updateLifetimeTrackingLocked(scientificName, detectionTime)
	t.updateLastSeenLocked(scientificName, detectionTime)
	t.updateYearlyTrackingLocked(scientificName, detectionTime)
	t.updateSeasonalTrackingLocked(scientificName, detectionTime)

	// Invalidate cache entry for this species to ensure fresh status calculations
	delete(t.statusCache, scientificName)

	return isNewSpecies
}

// IsNewSpecies checks if a species is considered "new" within the configured window
func (t *SpeciesTracker) IsNewSpecies(scientificName string) bool {
	// Suppress while warming: the maps are empty mid-load, so an unguarded check
	// would report every species as never-seen-before.
	if t.warming.Load() {
		return false
	}

	t.mu.RLock()
	firstSeen, exists := t.speciesFirstSeen[scientificName]
	t.mu.RUnlock()

	if !exists {
		return true // Never seen before
	}

	daysSince := calculateDaysSince(time.Now(), firstSeen)
	return daysSince <= t.windowDays
}

// checkAndUpdateLifetimeLocked checks species status and updates lifetime tracking.
// Returns (isNew, daysSinceFirstSeen). Assumes lock is held.
func (t *SpeciesTracker) checkAndUpdateLifetimeLocked(scientificName string, detectionTime time.Time) (isNew bool, daysSinceFirstSeen int) {
	firstSeen, exists := t.speciesFirstSeen[scientificName]

	if !exists {
		t.speciesFirstSeen[scientificName] = detectionTime
		return true, 0
	}

	if detectionTime.Before(firstSeen) {
		t.speciesFirstSeen[scientificName] = detectionTime
		return true, 0
	}

	// Calculate calendar days since first seen (DST-safe, clamped to 0)
	daysSince := calculateDaysSince(detectionTime, firstSeen)
	return daysSince <= t.windowDays, daysSince
}

// addYearlyIfNewLocked adds species to yearly tracking if not already present. Assumes lock is held.
func (t *SpeciesTracker) addYearlyIfNewLocked(scientificName string, detectionTime time.Time) {
	if !t.yearlyEnabled || !t.isWithinCurrentYear(detectionTime) {
		return
	}
	if _, exists := t.speciesThisYear[scientificName]; !exists {
		t.speciesThisYear[scientificName] = detectionTime
	}
}

// addSeasonalIfNewLocked adds species to seasonal tracking if not already present. Assumes lock is held.
func (t *SpeciesTracker) addSeasonalIfNewLocked(scientificName string, detectionTime time.Time) {
	if !t.seasonalEnabled {
		return
	}
	currentSeason := t.getCurrentSeason(detectionTime)
	if t.speciesBySeason[currentSeason] == nil {
		t.speciesBySeason[currentSeason] = make(map[string]time.Time)
	}
	if _, exists := t.speciesBySeason[currentSeason][scientificName]; !exists {
		t.speciesBySeason[currentSeason][scientificName] = detectionTime
	}
}

// CheckAndUpdateSpecies atomically checks if a species is new and updates the tracker
// This prevents race conditions where multiple concurrent detections of the same species
// could all be considered "new" before any of them update the tracker.
// Returns (isNew, daysSinceFirstSeen)
func (t *SpeciesTracker) CheckAndUpdateSpecies(scientificName string, detectionTime time.Time) (isNew bool, daysSinceFirstSeen int) {
	isNew, daysSinceFirstSeen, _ = t.CheckAndUpdateSpeciesWithNovelty(scientificName, detectionTime)
	return
}

// CheckAndUpdateSpeciesWithNovelty atomically checks species status, updates
// tracking, and returns novelty episode details for alert rules.
func (t *SpeciesTracker) CheckAndUpdateSpeciesWithNovelty(scientificName string, detectionTime time.Time) (isNew bool, daysSinceFirstSeen int, novelty NoveltyStatus) {
	// While warming, suppress new-species/novelty and skip recording so the empty
	// maps cannot produce a spurious first-detection. Checked before t.mu so it
	// never blocks on the lock the background loader holds.
	//
	// daysSinceFirstSeen is 0 here, NOT the -1 "unknown" sentinel used by
	// warmingSpeciesStatus: this value flows to events.NewDetectionEvent, which
	// rejects a negative daysSinceFirstSeen. A -1 would drop the ordinary
	// detection.occurred event for every detection during warm-up and break
	// alert rules. With isNew=false the value is not interpreted as "new today".
	if t.warming.Load() {
		return false, 0, inactiveNoveltyStatus(inactiveNoveltyValue)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.checkAndResetPeriods(detectionTime)

	novelty = t.calculateNoveltyStatusLocked(scientificName, detectionTime)
	isNew, daysSinceFirstSeen = t.checkAndUpdateLifetimeLocked(scientificName, detectionTime)
	t.updateLastSeenLocked(scientificName, detectionTime)
	t.addYearlyIfNewLocked(scientificName, detectionTime)
	t.addSeasonalIfNewLocked(scientificName, detectionTime)

	// Invalidate cache entry for this species to ensure fresh status calculations
	delete(t.statusCache, scientificName)

	return
}
