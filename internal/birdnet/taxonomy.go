// taxonomy.go contains functions for working with eBird taxonomy codes
package birdnet

import (
	"crypto/sha256"
	_ "embed" // For embedding data
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

//go:embed data/eBird_taxonomy_codes_2021E.json
var taxonomyData []byte

// TaxonomyMap is a bidirectional mapping between eBird codes and species names
type TaxonomyMap map[string]string

// ScientificNameIndex maps scientific names to their corresponding codes
type ScientificNameIndex map[string]string

// LoadTaxonomyData loads the eBird taxonomy data from the embedded file
// or from a custom file if provided.
func LoadTaxonomyData(customPath string) (TaxonomyMap, ScientificNameIndex, error) {
	var data []byte
	var err error

	if customPath != "" {
		// Load from custom file if provided
		data, err = os.ReadFile(customPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read custom taxonomy file %s: %w", customPath, err)
		}
	} else {
		// Use embedded data
		data = taxonomyData
	}

	var taxonomyMap TaxonomyMap
	err = json.Unmarshal(data, &taxonomyMap)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal taxonomy data: %w", err)
	}

	// Create the scientific name index
	scientificIndex := CreateScientificNameIndex(taxonomyMap)

	return taxonomyMap, scientificIndex, nil
}

// CreateScientificNameIndex builds an index mapping scientific names to codes
func CreateScientificNameIndex(taxonomyMap TaxonomyMap) ScientificNameIndex {
	index := make(ScientificNameIndex)
	for taxonName, taxonCode := range taxonomyMap {
		if strings.Contains(taxonName, "_") {
			parts := strings.SplitN(taxonName, "_", 2)
			if len(parts) == 2 {
				scientificName := strings.TrimSpace(parts[0])
				index[scientificName] = taxonCode
			}
		}
	}
	return index
}

// GeneratePlaceholderCode generates a placeholder eBird code for species not in the taxonomy map
// This ensures we have a consistent identifier even for species without official codes
func GeneratePlaceholderCode(speciesName string) string {
	// Create a hash of the species name to ensure uniqueness
	hash := sha256.Sum256([]byte(speciesName))
	// Use the first 6 characters of the hex-encoded hash
	shortHash := hex.EncodeToString(hash[:])[:6]

	// If species name has scientific and common components, extract them
	parts := strings.Split(speciesName, "_")
	var prefix string

	if len(parts) >= 2 {
		// Use first letters of scientific name as prefix
		scientific := parts[0]
		words := strings.Fields(scientific)
		if len(words) >= 2 {
			// Get first letter of genus and species (typically first two words)
			prefix = strings.ToUpper(string(words[0][0]) + string(words[1][0]))
		} else if len(words) == 1 {
			// Just use first two letters of the single word
			if len(words[0]) >= 2 {
				prefix = strings.ToUpper(words[0][:2])
			} else {
				prefix = strings.ToUpper(words[0])
			}
		}
	}

	if prefix == "" {
		// Fallback if we couldn't extract a meaningful prefix
		prefix = "XX"
	}

	// Combine prefix with hash to create a code-like identifier
	return fmt.Sprintf("%s%s", prefix, shortHash)
}

// GetSpeciesCodeFromName returns the eBird species code for a given species name.
// Extracts and uses only the scientific name portion for lookup.
// If species is not found in the taxonomy map, generates a placeholder code.
func GetSpeciesCodeFromName(taxonomyMap TaxonomyMap, scientificIndex ScientificNameIndex, speciesName string) (string, bool) {
	// Extract scientific name from input
	var scientificName string

	// Handle "Scientific (Common)" format
	if strings.Contains(speciesName, " (") && strings.HasSuffix(speciesName, ")") {
		parts := strings.SplitN(speciesName, " (", 2)
		if len(parts) == 2 {
			scientificName = strings.TrimSpace(parts[0])
		}
	} else if strings.Contains(speciesName, "_") {
		// Handle "Scientific_Common" format
		parts := strings.SplitN(speciesName, "_", 2)
		if len(parts) == 2 {
			scientificName = strings.TrimSpace(parts[0])
		}
	}

	// If we couldn't extract a scientific name, use the whole input
	if scientificName == "" {
		scientificName = speciesName
	}

	// Look up in the scientific name index (O(1) operation)
	if code, exists := scientificIndex[scientificName]; exists {
		return code, true
	}

	// If not found in taxonomy, create a placeholder code
	return GeneratePlaceholderCode(speciesName), false
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
