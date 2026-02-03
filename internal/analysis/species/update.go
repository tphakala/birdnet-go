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
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check and reset periods if needed
	t.checkAndResetPeriods(detectionTime)

	// Update all tracking systems
	isNewSpecies := t.updateLifetimeTrackingLocked(scientificName, detectionTime)
	t.updateYearlyTrackingLocked(scientificName, detectionTime)
	t.updateSeasonalTrackingLocked(scientificName, detectionTime)

	// Invalidate cache entry for this species to ensure fresh status calculations
	delete(t.statusCache, scientificName)

	return isNewSpecies
}

// IsNewSpecies checks if a species is considered "new" within the configured window
func (t *SpeciesTracker) IsNewSpecies(scientificName string) bool {
	t.mu.RLock()
	firstSeen, exists := t.speciesFirstSeen[scientificName]
	t.mu.RUnlock()

	if !exists {
		return true // Never seen before
	}

	daysSince := int(time.Since(firstSeen).Hours() / hoursPerDay)
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

	// Calculate days since first seen
	daysSince := int(detectionTime.Sub(firstSeen) / (hoursPerDay * time.Hour))
	if daysSince < 0 {
		// Handle anomaly: treat as earliest detection
		getLog().Debug("Negative days calculation detected - treating as earliest detection",
			logger.String("species", scientificName),
			logger.String("detection_time", detectionTime.Format("2006-01-02 15:04:05.000")),
			logger.String("first_seen", firstSeen.Format("2006-01-02 15:04:05.000")))
		t.speciesFirstSeen[scientificName] = detectionTime
		return true, 0
	}

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
	t.mu.Lock()
	defer t.mu.Unlock()

	t.checkAndResetPeriods(detectionTime)

	isNew, daysSinceFirstSeen = t.checkAndUpdateLifetimeLocked(scientificName, detectionTime)
	t.addYearlyIfNewLocked(scientificName, detectionTime)
	t.addSeasonalIfNewLocked(scientificName, detectionTime)

	// Invalidate cache entry for this species to ensure fresh status calculations
	delete(t.statusCache, scientificName)

	return
}
