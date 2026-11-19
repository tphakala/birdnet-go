package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHasLifeList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		enabled  bool
		species  []string
		expected bool
	}{
		{"enabled with species", true, []string{"Turdus merula_Common Blackbird"}, true},
		{"enabled but empty list", true, nil, false},
		{"disabled with species", false, []string{"Turdus merula_Common Blackbird"}, false},
		{"disabled and empty", false, nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := &Settings{}
			settings.Realtime.LifeList.Enabled = tt.enabled
			settings.Realtime.LifeList.Species = tt.species
			assert.Equal(t, tt.expected, settings.HasLifeList())
		})
	}
}

func TestIsOnLifeList(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	settings.Realtime.LifeList.Species = []string{
		"Turdus merula_Common Blackbird",
		"Parus major_Great Tit",
		"Corvus corax_Northern Raven",
	}

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"full label match", "Turdus merula_Common Blackbird", true},
		{"scientific name only", "Turdus merula", true},
		{"case insensitive", "TURDUS MERULA", true},
		{"case insensitive full label", "PARUS MAJOR_Great Tit", true},
		{"not on list", "Ficedula hypoleuca_Pied Flycatcher", false},
		{"not on list sci only", "Ficedula hypoleuca", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, settings.IsOnLifeList(tt.input))
		})
	}
}

// TestIsOnLifeList_TaxonomicAlias verifies that a legacy classifier label is
// matched against a life-list entry via the OpenFauna alias map, so a species
// exported from eBird under one taxonomic name still matches a BirdNET
// detection reported under a different (legacy or current) name for the same
// species.
func TestIsOnLifeList_TaxonomicAlias(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	// Life list holds the legacy name, as an older eBird export might.
	settings.Realtime.LifeList.Species = []string{"Streptopelia senegalensis_Laughing Dove"}

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"canonical detection label resolves to legacy list entry", "Spilopelia senegalensis_Laughing Dove", true},
		{"canonical scientific only", "Spilopelia senegalensis", true},
		{"legacy label matches directly", "Streptopelia senegalensis_Laughing Dove", true},
		{"label with surrounding whitespace", "  Spilopelia senegalensis  ", true},
		{"label with CRLF carriage return", "Spilopelia senegalensis\r", true},
		{"unrelated species not on list", "Ficedula hypoleuca", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, settings.IsOnLifeList(tt.input))
		})
	}
}
