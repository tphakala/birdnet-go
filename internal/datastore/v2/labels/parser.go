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

// ParsedLabel contains the extracted components from a raw model label.
type ParsedLabel struct {
	ScientificName string
	CommonName     string // May be empty (e.g., Perch)
	LabelType      entities.LabelType
	TaxonomicClass string // "Aves", "Chiroptera", or empty
}

// NonSpeciesLabels are known non-animal labels.
var NonSpeciesLabels = map[string]entities.LabelType{
	// Noise/silence
	"noise":      entities.LabelTypeNoise,
	"silence":    entities.LabelTypeNoise,
	"background": entities.LabelTypeNoise,

	// Environment sounds
	"engine":    entities.LabelTypeEnvironment,
	"train":     entities.LabelTypeEnvironment,
	"wind":      entities.LabelTypeEnvironment,
	"rain":      entities.LabelTypeEnvironment,
	"thunder":   entities.LabelTypeEnvironment,
	"water":     entities.LabelTypeEnvironment,
	"fireworks": entities.LabelTypeEnvironment,
	"siren":     entities.LabelTypeEnvironment,

	// Device sounds
	"audiomoth": entities.LabelTypeDevice,
	"other":     entities.LabelTypeUnknown,
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
		LabelType: entities.LabelTypeSpecies,
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
