// Package labels provides lazy label resolution for multi-model support.
// It handles parsing raw model output labels (BirdNET, Perch, bat models)
// and resolving them to normalized Label entities.
package labels

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// Label type name constants.
const (
	LabelTypeSpecies     = "species"
	LabelTypeNoise       = "noise"
	LabelTypeEnvironment = "environment"
	LabelTypeDevice      = "device"
	LabelTypeUnknown     = "unknown"
)

// ParsedLabel contains the extracted components from a raw model label.
type ParsedLabel struct {
	ScientificName string
	CommonName     string // May be empty (e.g., Perch)
	LabelType      string // "species", "noise", "environment", "device", "unknown"
	TaxonomicClass string // "Aves", "Chiroptera", or empty
}

// NonSpeciesLabels are known non-animal labels.
var NonSpeciesLabels = map[string]string{
	// Noise/silence
	"noise":      LabelTypeNoise,
	"silence":    LabelTypeNoise,
	"background": LabelTypeNoise,

	// Environment sounds
	"engine":    LabelTypeEnvironment,
	"train":     LabelTypeEnvironment,
	"wind":      LabelTypeEnvironment,
	"rain":      LabelTypeEnvironment,
	"thunder":   LabelTypeEnvironment,
	"water":     LabelTypeEnvironment,
	"fireworks": LabelTypeEnvironment,
	"siren":     LabelTypeEnvironment,

	// Device sounds
	"audiomoth": LabelTypeDevice,
	"other":     LabelTypeUnknown,
}

// ParseRawLabel extracts components from a model's raw label output.
// Handles formats:
//   - "Turdus merula_Common Blackbird" (BirdNET)
//   - "Eptesicus nilssonii_Nordfladdermus" (Bat model)
//   - "Turdus merula" (Perch - scientific only)
//   - "noise" (non-species)
func ParseRawLabel(rawLabel string, modelType entities.ModelType) ParsedLabel {
	rawLabel = strings.TrimSpace(rawLabel)

	// Check for non-species labels (case-insensitive)
	lowerLabel := strings.ToLower(rawLabel)
	if labelType, isNonSpecies := NonSpeciesLabels[lowerLabel]; isNonSpecies {
		return ParsedLabel{
			ScientificName: lowerLabel, // Store lowercase English
			LabelType:      labelType,
		}
	}

	// Parse species label
	parsed := ParsedLabel{
		LabelType: LabelTypeSpecies,
	}

	// Determine taxonomic class from model type
	switch modelType {
	case entities.ModelTypeBird:
		parsed.TaxonomicClass = "Aves"
	case entities.ModelTypeBat:
		parsed.TaxonomicClass = "Chiroptera"
	case entities.ModelTypeMulti:
		// Multi-type models don't have a specific taxonomic class
	}

	// Split on underscore to separate scientific name from common name
	parts := strings.SplitN(rawLabel, "_", 2)
	parsed.ScientificName = strings.TrimSpace(parts[0])

	if len(parts) > 1 {
		parsed.CommonName = strings.TrimSpace(parts[1])
	}

	return parsed
}

// IsValidScientificName performs basic validation on a scientific name.
func IsValidScientificName(name string) bool {
	if name == "" {
		return false
	}
	// Scientific names should have at least two words (genus species)
	// and start with an uppercase letter
	parts := strings.Fields(name)
	if len(parts) < 2 {
		return false
	}
	// First letter should be uppercase (genus)
	firstRune, _ := utf8.DecodeRuneInString(parts[0])
	return unicode.IsUpper(firstRune)
}
