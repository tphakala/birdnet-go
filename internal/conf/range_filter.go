package conf

import (
	"sync"
	"time"
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

// IsSpeciesIncluded checks if a given species is in the included list of the RangeFilter
func (s *Settings) IsSpeciesIncluded(species string) bool {
	speciesListMutex.RLock()
	defer speciesListMutex.RUnlock()
	for _, s := range s.BirdNET.RangeFilter.Species {
		if s == species {
			return true
		}
	}
	return false
}
