// species_config.go contains species-specific configuration utilities
package conf

import "strings"

// NormalizeSpeciesConfigKeys returns a new map with all keys converted to lowercase.
// This ensures case-insensitive matching when looking up species configurations.
// If input is nil, returns an empty map.
//
// When there are duplicate keys after normalization (e.g., both "bird a" and "Bird A"),
// the mixed-case key takes precedence over the lowercase key. This is important for
// PATCH operations where the payload uses mixed-case keys that should overwrite
// existing lowercase keys from the original config.
func NormalizeSpeciesConfigKeys(config map[string]SpeciesConfig) map[string]SpeciesConfig {
	if config == nil {
		return make(map[string]SpeciesConfig)
	}

	normalized := make(map[string]SpeciesConfig, len(config))

	// First pass: add all already-lowercase keys
	// These are typically from the existing config
	for key, value := range config {
		if key == strings.ToLower(key) {
			normalized[key] = value
		}
	}

	// Second pass: add non-lowercase keys (normalized)
	// These typically come from API updates and should overwrite existing values
	for key, value := range config {
		if key != strings.ToLower(key) {
			normalizedKey := strings.ToLower(key)
			normalized[normalizedKey] = value
		}
	}

	return normalized
}
