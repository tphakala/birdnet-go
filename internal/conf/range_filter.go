package conf

import (
	"strings"
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

// IsSpeciesIncluded checks if a given scientific name matches the scientific name part of any included species
func (s *Settings) IsSpeciesIncluded(scientificName string) bool {
	speciesListMutex.RLock()
	defer speciesListMutex.RUnlock()

	// Add underscore to the scientific name to prevent partial matches
	searchTerm := scientificName + "_"

	for _, fullSpeciesString := range s.BirdNET.RangeFilter.Species {
		// Check if the full species string starts with our search term
		if strings.HasPrefix(fullSpeciesString, searchTerm) {
			return true
		}
	}
	return false
}
