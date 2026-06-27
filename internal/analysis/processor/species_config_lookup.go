// species_config_lookup.go provides helper functions for looking up species configurations.
package processor

import (
	"strings"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/openfauna"
)

// lookupSpeciesConfig looks up a species configuration by either common name or scientific name.
// This provides consistent behavior with include/exclude list matching (see matchesSpecies in range_filter.go).
//
// The function uses a two-tier lookup strategy:
//   - Fast path: O(1) lookup by lowercase common name (most common case)
//   - Fallback: O(n) iteration checking scientific name (allows users to configure by scientific name)
//
// Parameters:
//   - configMap: The species config map from settings (keys are normalized to lowercase)
//   - commonName: The common name to look up (e.g., "American Robin")
//   - scientificName: The scientific name to look up as fallback (e.g., "Turdus migratorius")
//
// Returns:
//   - config: The species configuration if found
//   - found: true if a matching config was found
func lookupSpeciesConfig(configMap map[string]conf.SpeciesConfig, commonName, scientificName string) (conf.SpeciesConfig, bool) {
	if configMap == nil {
		return conf.SpeciesConfig{}, false
	}

	// Fast path: O(1) lookup by lowercase common name (most common case)
	if commonName != "" {
		commonNameLower := strings.ToLower(commonName)
		if config, exists := configMap[commonNameLower]; exists {
			return config, true
		}
	}

	// Fallback: O(n) iteration checking scientific name (case-insensitive). Run an
	// exact-key pass first so an exact scientific-name match always wins
	// deterministically, even when the config holds both a legacy name and its
	// canonical replacement (Go map iteration order is randomized). Only when no exact
	// key matches do we fall back to canonical-alias matching, so a config entry keyed
	// on a legacy/alias scientific name still matches the canonical name the detection
	// now carries. CanonicalName is identity for non-aliased names, so this preserves
	// existing behavior for species without a reclassification.
	if scientificName != "" {
		for key, config := range configMap {
			if strings.EqualFold(key, scientificName) {
				return config, true
			}
		}
		canonicalSci := openfauna.CanonicalName(scientificName)
		for key, config := range configMap {
			if strings.EqualFold(openfauna.CanonicalName(key), canonicalSci) {
				return config, true
			}
		}
	}

	return conf.SpeciesConfig{}, false
}
