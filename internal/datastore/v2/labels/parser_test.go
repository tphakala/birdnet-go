package labels

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

func TestParseRawLabel_BirdNET(t *testing.T) {
	tests := []struct {
		name     string
		rawLabel string
		expected ParsedLabel
	}{
		{
			name:     "standard bird",
			rawLabel: "Turdus merula_Common Blackbird",
			expected: ParsedLabel{
				ScientificName: "Turdus merula",
				CommonName:     "Common Blackbird",
				LabelType:      entities.LabelTypeSpecies,
				TaxonomicClass: "Aves",
			},
		},
		{
			name:     "noise label",
			rawLabel: "noise",
			expected: ParsedLabel{
				ScientificName: "noise",
				LabelType:      entities.LabelTypeNoise,
			},
		},
		{
			name:     "noise uppercase",
			rawLabel: "NOISE",
			expected: ParsedLabel{
				ScientificName: "noise",
				LabelType:      entities.LabelTypeNoise,
			},
		},
		{
			name:     "whitespace handling",
			rawLabel: "  Turdus merula_Common Blackbird  ",
			expected: ParsedLabel{
				ScientificName: "Turdus merula",
				CommonName:     "Common Blackbird",
				LabelType:      entities.LabelTypeSpecies,
				TaxonomicClass: "Aves",
			},
		},
		{
			name:     "environment sound",
			rawLabel: "wind",
			expected: ParsedLabel{
				ScientificName: "wind",
				LabelType:      entities.LabelTypeEnvironment,
			},
		},
		{
			name:     "device sound",
			rawLabel: "audiomoth",
			expected: ParsedLabel{
				ScientificName: "audiomoth",
				LabelType:      entities.LabelTypeDevice,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseRawLabel(tt.rawLabel, entities.ModelTypeBird)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseRawLabel_BatModel(t *testing.T) {
	result := ParseRawLabel("Eptesicus nilssonii_Nordfladdermus", entities.ModelTypeBat)

	assert.Equal(t, "Eptesicus nilssonii", result.ScientificName)
	assert.Equal(t, "Nordfladdermus", result.CommonName)
	assert.Equal(t, entities.LabelTypeSpecies, result.LabelType)
	assert.Equal(t, "Chiroptera", result.TaxonomicClass)
}

func TestParseRawLabel_Perch(t *testing.T) {
	result := ParseRawLabel("Turdus merula", entities.ModelTypeMulti)

	assert.Equal(t, "Turdus merula", result.ScientificName)
	assert.Empty(t, result.CommonName)
	assert.Equal(t, entities.LabelTypeSpecies, result.LabelType)
	// Multi model type does not set taxonomic class
	assert.Empty(t, result.TaxonomicClass)
}

func TestParseRawLabel_MultipleUnderscores(t *testing.T) {
	// Some common names might contain underscores
	result := ParseRawLabel("Genus species_Common_Name_Here", entities.ModelTypeBird)

	assert.Equal(t, "Genus species", result.ScientificName)
	assert.Equal(t, "Common_Name_Here", result.CommonName) // Preserves underscores in common name
	assert.Equal(t, entities.LabelTypeSpecies, result.LabelType)
}

func TestIsValidScientificName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"valid two-word name", "Turdus merula", true},
		{"valid three-word name", "Eptesicus nilssonii subspecies", true},
		{"noise label", "noise", false},
		{"empty string", "", false},
		{"single word", "singleword", false},
		{"lowercase genus", "turdus merula", false},
		{"all lowercase", "turdus merula", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsValidScientificName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNonSpeciesLabels_Coverage(t *testing.T) {
	// Verify all known non-species labels are properly categorized
	noiseLabels := []string{"noise", "silence", "background"}
	for _, label := range noiseLabels {
		result := ParseRawLabel(label, entities.ModelTypeBird)
		assert.Equal(t, entities.LabelTypeNoise, result.LabelType, "Expected %s to be noise", label)
	}

	envLabels := []string{"engine", "train", "wind", "rain", "thunder", "water", "fireworks", "siren"}
	for _, label := range envLabels {
		result := ParseRawLabel(label, entities.ModelTypeBird)
		assert.Equal(t, entities.LabelTypeEnvironment, result.LabelType, "Expected %s to be environment", label)
	}

	deviceLabels := []string{"audiomoth"}
	for _, label := range deviceLabels {
		result := ParseRawLabel(label, entities.ModelTypeBird)
		assert.Equal(t, entities.LabelTypeDevice, result.LabelType, "Expected %s to be device", label)
	}

	unknownLabels := []string{"other"}
	for _, label := range unknownLabels {
		result := ParseRawLabel(label, entities.ModelTypeBird)
		assert.Equal(t, entities.LabelTypeUnknown, result.LabelType, "Expected %s to be unknown", label)
	}
}
