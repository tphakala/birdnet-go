// taxonomy.go contains functions for working with eBird taxonomy codes
package birdnet

import (
	_ "embed" // For embedding data
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

//go:embed data/eBird_taxonomy_codes_2021E.json
var taxonomyData []byte

// TaxonomyMap is a bidirectional mapping between eBird codes and species names
type TaxonomyMap map[string]string

// LoadTaxonomyData loads the eBird taxonomy data from the embedded file
// or from a custom file if provided.
func LoadTaxonomyData(customPath string) (TaxonomyMap, error) {
	var data []byte
	var err error

	if customPath != "" {
		// Load from custom file if provided
		data, err = os.ReadFile(customPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read custom taxonomy file %s: %w", customPath, err)
		}
	} else {
		// Use embedded data
		data = taxonomyData
	}

	var taxonomyMap TaxonomyMap
	err = json.Unmarshal(data, &taxonomyMap)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal taxonomy data: %w", err)
	}

	return taxonomyMap, nil
}

// GetSpeciesCodeFromName returns the eBird species code for a given species name
// in the format "ScientificName_CommonName".
func GetSpeciesCodeFromName(taxonomyMap TaxonomyMap, speciesName string) (string, bool) {
	code, exists := taxonomyMap[speciesName]
	return code, exists
}

// GetSpeciesNameFromCode returns the species name in the format "ScientificName_CommonName"
// for a given eBird species code.
func GetSpeciesNameFromCode(taxonomyMap TaxonomyMap, code string) (string, bool) {
	name, exists := taxonomyMap[code]
	return name, exists
}

// SplitSpeciesName splits a species name string into scientific and common names
func SplitSpeciesName(speciesName string) (scientific, common string) {
	// Check if the string is empty
	if speciesName == "" {
		return "", ""
	}

	// Split by underscore
	parts := strings.Split(speciesName, "_")

	if len(parts) >= 2 {
		// Format: "ScientificName_CommonName[_SpeciesCode]"
		return parts[0], parts[1]
	} else if len(parts) == 1 && strings.Contains(speciesName, " ") {
		// This might be just a common name without scientific name format
		return "", speciesName
	}

	// If we couldn't parse it properly, return the original string as scientific name
	return speciesName, ""
}

// IsTaxonomyComplete checks if the taxonomy map has all the species in the labels
func IsTaxonomyComplete(taxonomyMap TaxonomyMap, labels []string) (complete bool, missing []string) {
	missing = []string{}

	for _, label := range labels {
		_, exists := taxonomyMap[label]
		if !exists {
			missing = append(missing, label)
		}
	}

	return len(missing) == 0, missing
}
