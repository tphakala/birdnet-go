// internal/httpcontroller/utils.go - Utility functions for the HTTP controller package
package httpcontroller

import (
	"sort"
	"strings"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// prepareLocalesData returns sorted locale data to be used for select menu on the main settings page
func (s *Server) prepareLocalesData() []LocaleData {
	var locales []LocaleData
	for code, name := range conf.LocaleCodes {
		locales = append(locales, LocaleData{Code: code, Name: name})
	}
	sort.Slice(locales, func(i, j int) bool {
		return locales[i].Name < locales[j].Name
	})
	return locales
}

// prepareSpeciesData prepares species data for predictive entry on Dog Bark Species List
func (s *Server) prepareSpeciesData() []string {
	var preparedSpecies []string
	var scientificNames []string
	var commonNames []string

	// Split species entry into scientific and common names
	for _, species := range s.Settings.BirdNET.RangeFilter.Species {
		parts := strings.Split(species, "_")
		if len(parts) >= 2 {
			scientificNames = append(scientificNames, strings.TrimSpace(parts[0]))
			commonNames = append(commonNames, strings.TrimSpace(parts[1]))
		}
	}

	// Sort both slices alphabetically
	sort.Strings(scientificNames)
	sort.Strings(commonNames)

	// Combine common names first, then scientific names
	preparedSpecies = append(preparedSpecies, commonNames...)
	preparedSpecies = append(preparedSpecies, scientificNames...)

	return removeDuplicates(preparedSpecies)
}

// removeDuplicates removes duplicate entries from a slice of strings
func removeDuplicates(slice []string) []string {
	keys := make(map[string]bool)
	list := []string{}
	for _, entry := range slice {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}
