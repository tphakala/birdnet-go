package conf

import (
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
)

var (
	speciesListMutex sync.RWMutex
)

// UpdateIncludedSpecies updates the included species list in the RangeFilter
func (s *Settings) UpdateIncludedSpecies(species []string) {
	speciesListMutex.Lock()
	defer speciesListMutex.Unlock()
	s.BirdNET.RangeFilter.Species = make([]string, len(species))
	copy(s.BirdNET.RangeFilter.Species, species)
	s.BirdNET.RangeFilter.LastUpdated = time.Now()
}

// GetIncludedSpecies returns the current included species list from the RangeFilter
func (s *Settings) GetIncludedSpecies() []string {
	speciesListMutex.RLock()
	defer speciesListMutex.RUnlock()
	speciesCopy := make([]string, len(s.BirdNET.RangeFilter.Species))
	copy(speciesCopy, s.BirdNET.RangeFilter.Species)
	return speciesCopy
}

// IsSpeciesIncluded checks if a given scientific name matches the scientific name part of any included species
func (s *Settings) IsSpeciesIncluded(result string) bool {
	speciesListMutex.RLock()
	defer speciesListMutex.RUnlock()
	
	if result == "Aves sp._bird sp._bird1" {
		return true
	}

	for _, fullSpeciesString := range s.BirdNET.RangeFilter.Species {
		// Check if the full species string starts with our search term
		if strings.HasPrefix(fullSpeciesString, result) {
			return true
		}
	}
	return false
}

// ShouldUpdateRangeFilterToday atomically checks if the range filter should be updated today.
// This function ensures that only ONE goroutine will trigger the update on any given day,
// preventing race conditions where multiple concurrent detections could trigger multiple
// range filter rebuilds simultaneously.
//
// Returns true only for the FIRST caller on a given day (midnight to midnight).
// Subsequent callers on the same day will return false.
//
// This solves GitHub issue #1357 where species appeared in detections that weren't in the
// range filter due to concurrent range filter updates creating inconsistent states.
func (s *Settings) ShouldUpdateRangeFilterToday() bool {
	speciesListMutex.Lock()
	defer speciesListMutex.Unlock()

	today := time.Now().Truncate(24 * time.Hour)

	// Check if we need to update (last update was before today)
	if s.BirdNET.RangeFilter.LastUpdated.Before(today) {
		// Atomically mark as updated to prevent other goroutines from also updating
		s.BirdNET.RangeFilter.LastUpdated = today

		// Log the update decision for debugging
		if s.Debug {
			GetLogger().Debug("Scheduled range filter update",
				logger.String("date", today.Format("2006-01-02")),
				logger.String("last_updated", s.BirdNET.RangeFilter.LastUpdated.Format("2006-01-02 15:04:05")))
		}

		return true
	}

	return false
}

// GetLastRangeFilterUpdate returns the last time the range filter was updated.
// This is thread-safe and uses the same mutex as other range filter operations.
func (s *Settings) GetLastRangeFilterUpdate() time.Time {
	speciesListMutex.RLock()
	defer speciesListMutex.RUnlock()
	return s.BirdNET.RangeFilter.LastUpdated
}

// ResetRangeFilterUpdateFlag resets the LastUpdated timestamp to allow retry of failed updates.
// This should be called when range filter update fails (e.g., network error, API failure)
// to allow the update to be retried on the next detection instead of waiting until tomorrow.
//
// This is thread-safe and uses the same mutex as other range filter operations.
func (s *Settings) ResetRangeFilterUpdateFlag() {
	speciesListMutex.Lock()
	defer speciesListMutex.Unlock()
	// Set to zero time to indicate update is needed
	s.BirdNET.RangeFilter.LastUpdated = time.Time{}
}
