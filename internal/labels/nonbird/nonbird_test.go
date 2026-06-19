package nonbird_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/labels/nonbird"
)

func TestCategoryOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		label   string
		wantCat nonbird.Category
		wantOK  bool
	}{
		// Full-label positives - one per category
		{"mechanical", "power_tool", nonbird.CategoryMechanical, true},
		{"human", "speech", nonbird.CategoryHuman, true},
		{"animal", "dog", nonbird.CategoryAnimal, true},
		{"music", "piano", nonbird.CategoryMusic, true},
		{"environment", "rain", nonbird.CategoryEnvironment, true},
		{"noise", "buzz", nonbird.CategoryNoise, true},
		{"device", "clock", nonbird.CategoryDevice, true},
		// Case-insensitivity
		{"uppercase", "POWER_TOOL", nonbird.CategoryMechanical, true},
		{"mixed case", "Speech", nonbird.CategoryHuman, true},
		// Negatives
		{"truncated token not a full label", "Power", nonbird.Category(""), false},
		{"real bird binomial", "Turdus merula", nonbird.Category(""), false},
		{"underscore-joined binomial not a class", "turdus_merula", nonbird.Category(""), false},
		{"empty string", "", nonbird.Category(""), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := nonbird.CategoryOf(tc.label)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantCat, got)
		})
	}
}

func TestIsNonSpeciesLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		label string
		want  bool
	}{
		{"full label - speech", "speech", true},
		{"full label - power_tool", "power_tool", true},
		{"full label - dog", "dog", true},
		{"full label - rain", "rain", true},
		{"real bird binomial", "Turdus merula", false},
		{"truncated token", "Power", false},
		{"empty string", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := nonbird.IsNonSpeciesLabel(tc.label)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestIsNonBirdName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		// From semantics examples in the brief
		{"first token of power_tool", "Power", true},
		{"full label power_tool", "power_tool", true},
		{"single-token full key Engine", "Engine", true},
		{"first token of human_voice", "human", true},
		{"first token of fixed-wing_aircraft", "fixed-wing", true},
		{"single-token tick-tock", "tick-tock", true},
		{"real bird binomial", "Turdus merula", false},
		{"underscore-joined binomial", "turdus_merula", false},
		{"empty string", "", false},
		// Case-insensitivity extras
		{"uppercase full key", "DOG", true},
		{"mixed case first token", "MALE", true},
		{"lowercase first token", "bass", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := nonbird.IsNonBirdName(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}
