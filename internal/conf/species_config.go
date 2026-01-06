// species_config.go contains species-specific configuration utilities
package conf

import "strings"

// NormalizeSpeciesConfigKeys returns a new map with all keys converted to lowercase.
// This ensures case-insensitive matching when looking up species configurations.
// If input is nil, returns an empty map.
func NormalizeSpeciesConfigKeys(config map[string]SpeciesConfig) map[string]SpeciesConfig {
	if config == nil {
		return make(map[string]SpeciesConfig)
	}

	normalized := make(map[string]SpeciesConfig, len(config))
	for key, value := range config {
		normalizedKey := strings.ToLower(key)
		normalized[normalizedKey] = value
	}
	return normalized
}
