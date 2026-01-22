package detection

import (
	"strings"
)

// Species holds parsed species identification.
type Species struct {
	ScientificName string // e.g., "Turdus merula"
	CommonName     string // e.g., "Common Blackbird"
	Code           string // eBird species code (may be empty for custom models)
}

// ParseSpeciesString extracts the scientific name, common name, and species code
// from a BirdNET species string.
//
// Supported formats:
//   - "ScientificName_CommonName_SpeciesCode" (3 parts)
//   - "ScientificName_CommonName" (2 parts, most common)
//   - "Common Name" (space-separated, likely just common name)
//
// For custom models with species not in the eBird taxonomy, the species code
// might be empty or a placeholder.
func ParseSpeciesString(species string) Species {
	// Sanitize input - trim whitespace and remove carriage returns
	species = strings.TrimSpace(species)
	species = strings.ReplaceAll(species, "\r", "")

	// Check if the string is empty or contains special characters that would break parsing
	if species == "" || strings.Contains(species, "\t") || strings.Contains(species, "\n") {
		return Species{
			ScientificName: species,
			CommonName:     species,
			Code:           "",
		}
	}

	// Split the species string by "_" separator
	parts := strings.SplitN(species, "_", 3) // Split into 3 parts at most

	// Format 1: "ScientificName_CommonName_SpeciesCode" (3 parts)
	if len(parts) == 3 {
		return Species{
			ScientificName: parts[0],
			CommonName:     parts[1],
			Code:           parts[2],
		}
	}

	// Format 2: "ScientificName_CommonName" (2 parts) - most common format
	if len(parts) == 2 {
		return Species{
			ScientificName: parts[0],
			CommonName:     parts[1],
			Code:           "",
		}
	}

	// If we got here, the format doesn't match expected patterns
	// Check if it has spaces instead, like "Common Blackbird" with no scientific name
	if len(parts) == 1 && strings.Contains(species, " ") {
		// This is likely just a common name without scientific name
		return Species{
			ScientificName: "",
			CommonName:     species,
			Code:           "",
		}
	}

	// Default fallback - return the original string for all parts
	return Species{
		ScientificName: species,
		CommonName:     species,
		Code:           "",
	}
}

// String returns a formatted string representation of the species.
// Uses CommonName if available, falls back to ScientificName.
func (s Species) String() string {
	if s.CommonName != "" {
		return s.CommonName
	}
	return s.ScientificName
}
