package soundevent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/labels/soundevent"
)

func TestCategoryOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		label   string
		wantCat soundevent.Category
		wantOK  bool
	}{
		// Full-label positives - one per category
		{"mechanical", "power_tool", soundevent.CategoryMechanical, true},
		{"human", "speech", soundevent.CategoryHuman, true},
		{"animal", "dog", soundevent.CategoryAnimal, true},
		{"music", "piano", soundevent.CategoryMusic, true},
		{"environment", "rain", soundevent.CategoryEnvironment, true},
		{"noise", "buzz", soundevent.CategoryNoise, true},
		{"device", "clock", soundevent.CategoryDevice, true},
		// Case-insensitivity
		{"uppercase", "POWER_TOOL", soundevent.CategoryMechanical, true},
		{"mixed case", "Speech", soundevent.CategoryHuman, true},
		// Negatives
		{"truncated token not a full label", "Power", soundevent.Category(""), false},
		{"real bird binomial", "Turdus merula", soundevent.Category(""), false},
		{"underscore-joined binomial not a class", "turdus_merula", soundevent.Category(""), false},
		{"empty string", "", soundevent.Category(""), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := soundevent.CategoryOf(tc.label)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantCat, got)
		})
	}
}

func TestIsLabel(t *testing.T) {
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
		// BirdNET v2.x raw labels match on the part before the first underscore.
		{"BirdNET-only class", "Gun_Gun", true},
		{"BirdNET bare class name without underscore", "Gun", true},
		{"BirdNET class with localized common half", "Dog_Hund", true},
		{"BirdNET multi-word class prefix", "Human vocal_Human vocal", true},
		{"BirdNET class is case-insensitive", "human vocal_human vocal", true},
		{"Perch full label with whitespace and CR", " power_tool\r", true},
		{"class word in the common half does not match", "Cercomacra nigricans_Jet Antbird", false},
		{"BirdNET bird label", "Turdus merula_Common Blackbird", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := soundevent.IsLabel(tc.label)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestIsName(t *testing.T) {
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
		// BirdNET v2.x stored names
		{"BirdNET stored name in lowercase", "human vocal", true},
		{"legacy BirdNET class Jet", "Jet", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := soundevent.IsName(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestCategories(t *testing.T) {
	t.Parallel()

	cats := soundevent.Categories()

	// Must have exactly seven entries.
	assert.Len(t, cats, 7)

	// No duplicates.
	seen := make(map[soundevent.Category]struct{}, len(cats))
	for _, c := range cats {
		_, dup := seen[c]
		assert.False(t, dup, "duplicate category %q in Categories()", c)
		seen[c] = struct{}{}
	}

	// Every returned value must be one of the seven exported constants.
	valid := map[soundevent.Category]struct{}{
		soundevent.CategoryHuman:       {},
		soundevent.CategoryAnimal:      {},
		soundevent.CategoryMusic:       {},
		soundevent.CategoryMechanical:  {},
		soundevent.CategoryEnvironment: {},
		soundevent.CategoryNoise:       {},
		soundevent.CategoryDevice:      {},
	}
	for _, c := range cats {
		_, ok := valid[c]
		assert.True(t, ok, "Categories() returned unexpected value %q", c)
	}
}

func TestIsMultiWordClassPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"class with longer sibling", "Car", true},
		{"lowercase", "engine", true},
		{"class without longer sibling", "Dog", false},
		{"multi-word class", "Car_passing_by", false},
		{"first token that is not a class", "Domestic", false},
		{"bird", "Turdus merula", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, soundevent.IsMultiWordClassPrefix(tt.input))
		})
	}
}

func TestMultiWordLabelNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		label        string
		wantIdentity string
		wantDisplay  string
		wantOK       bool
	}{
		{"two underscores domestic sounds", "Domestic_sounds_and_home_sounds", "Domestic_sounds_and_home_sounds", "Domestic sounds and home sounds", true},
		{"one underscore", "Power_tool", "Power_tool", "Power tool", true},
		{"two underscore words", "Car_passing_by", "Car_passing_by", "Car passing by", true},
		{"four underscores", "Male_speech_and_man_speaking", "Male_speech_and_man_speaking", "Male speech and man speaking", true},
		{"parentheses", "Bathtub_(filling_or_washing)", "Bathtub_(filling_or_washing)", "Bathtub (filling or washing)", true},
		{"hyphenated first token", "Fixed-wing_aircraft_and_airplane", "Fixed-wing_aircraft_and_airplane", "Fixed-wing aircraft and airplane", true},
		{"lowercase keeps case", "power_tool", "power_tool", "power tool", true},
		{"trailing CR stripped", "Power_tool\r", "Power_tool", "Power tool", true},
		{"surrounding spaces stripped", "  Power_tool \r\n", "Power_tool", "Power tool", true},
		{"single token", "Dog", "", "", false},
		{"binomial", "Turdus merula", "", "", false},
		{"BirdNET two-part", "Turdus merula_Common Blackbird", "", "", false},
		{"BirdNET dog species", "Canis familiaris_Dog", "", "", false},
		{"unknown underscore label", "dog_bark", "", "", false},
		{"prefix of a class", "Domestic_sounds", "", "", false},
		{"empty", "", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			identity, display, ok := soundevent.MultiWordLabelNames(tt.label)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantIdentity, identity)
			assert.Equal(t, tt.wantDisplay, display)
		})
	}
}
