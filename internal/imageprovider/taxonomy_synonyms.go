// taxonomy_synonyms.go: Provides scientific name synonym mappings for taxonomy changes.
// BirdNET V2.4 uses 2021E taxonomy, but image providers (Wikipedia, Avicommons) may use
// updated taxonomy where species have been reclassified to different genera.
// This map allows fallback lookups when the primary name fails.
package imageprovider

import "strings"

// taxonomySynonyms maps BirdNET scientific names (2021E taxonomy) to their updated
// equivalents in newer taxonomy. When an image lookup fails with the BirdNET name,
// the provider retries with the synonym.
//
// Format: "old name" -> "new name"
// Names are stored in their canonical form (Title Case) and compared case-insensitively.
var taxonomySynonyms = map[string]string{
	// Accipiter → Astur reclassification (IOC 2022+)
	"Accipiter cooperii":      "Astur cooperii",
	"Accipiter gentilis":      "Astur gentilis",
	"Accipiter bicolor":       "Astur bicolor",
	"Accipiter melanoleucus":  "Astur melanoleucus",
	"Accipiter striatus":      "Astur striatus",
	"Accipiter superciliosus": "Astur superciliosus",

	// Corvus → Coloeus reclassification
	"Corvus monedula":  "Coloeus monedula",
	"Corvus dauuricus": "Coloeus dauuricus",
}

// forwardSynonyms maps lowercase BirdNET names to updated names.
// reverseSynonyms maps lowercase updated names to BirdNET names.
// Both are built at init time from taxonomySynonyms for fast lookup.
var (
	forwardSynonyms map[string]string
	reverseSynonyms map[string]string
)

func init() {
	forwardSynonyms = make(map[string]string, len(taxonomySynonyms))
	reverseSynonyms = make(map[string]string, len(taxonomySynonyms))
	for old, updated := range taxonomySynonyms {
		forwardSynonyms[strings.ToLower(old)] = updated
		reverseSynonyms[strings.ToLower(updated)] = old
	}
}

// GetTaxonomySynonym returns the alternate scientific name for a given name, if one exists.
// It checks both directions: BirdNET name → updated name, and updated name → BirdNET name.
// Returns the synonym and true if found, or empty string and false otherwise.
func GetTaxonomySynonym(scientificName string) (string, bool) {
	normalized := strings.ToLower(scientificName)

	// Check forward: BirdNET name → updated name
	if updated, found := forwardSynonyms[normalized]; found {
		return updated, true
	}

	// Check reverse: updated name → BirdNET name
	if original, found := reverseSynonyms[normalized]; found {
		return original, true
	}

	return "", false
}
