package conf

import "time"

// UpdateIncludedSpecies updates the included species list in the RangeFilter
func (s *Settings) UpdateIncludedSpecies(species []string) {
	s.BirdNET.RangeFilter.speciesListMutex.Lock()
	defer s.BirdNET.RangeFilter.speciesListMutex.Unlock()
	s.BirdNET.RangeFilter.Species = species
	s.BirdNET.RangeFilter.LastUpdated = time.Now()
}

// GetIncludedSpecies returns the current included species list from the RangeFilter
func (s *Settings) GetIncludedSpecies() []string {
	s.BirdNET.RangeFilter.speciesListMutex.RLock()
	defer s.BirdNET.RangeFilter.speciesListMutex.RUnlock()
	return s.BirdNET.RangeFilter.Species
}

// IsSpeciesIncluded checks if a given species is in the included list of the RangeFilter
func (s *Settings) IsSpeciesIncluded(species string) bool {
	s.BirdNET.RangeFilter.speciesListMutex.RLock()
	defer s.BirdNET.RangeFilter.speciesListMutex.RUnlock()
	for _, s := range s.BirdNET.RangeFilter.Species {
		if s == species {
			return true
		}
	}
	return false
}
