package classifier

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/detection"
)

// TestGeneratePlaceholderCode_MultibyteSingleWordScientificName guards against
// slicing a single-word scientific name by bytes. A genus whose first rune spans
// 3+ bytes (e.g. CJK) was cut mid-rune by words[0][:2], so strings.ToUpper
// sanitized the partial bytes into U+FFFD replacement characters and the prefix
// became garbage. The prefix must instead be the first two runes, uppercased.
func TestGeneratePlaceholderCode_MultibyteSingleWordScientificName(t *testing.T) {
	t.Parallel()

	// "中a" is a single Fields() word whose first rune is 3 bytes; the second
	// element forces the underscore branch that builds a prefix.
	code := GeneratePlaceholderCode("中a_Common")

	assert.True(t, utf8.ValidString(code), "placeholder code must be valid UTF-8")
	assert.False(t, strings.ContainsRune(code, utf8.RuneError),
		"placeholder code must not contain a U+FFFD replacement character")
	assert.True(t, strings.HasPrefix(code, "中A"),
		"prefix must be the first two runes uppercased, got %q", code)
}

// TestGeneratePlaceholderCode_MultibyteBinomialScientificName guards the two-word
// branch: it must use the first rune of each word, not the first byte. Reading
// the first byte of a multibyte genus/species produces the wrong characters.
func TestGeneratePlaceholderCode_MultibyteBinomialScientificName(t *testing.T) {
	t.Parallel()

	// Two Fields() words, each starting with a 3-byte rune.
	code := GeneratePlaceholderCode("中文 鸟类_Common")

	assert.True(t, utf8.ValidString(code), "placeholder code must be valid UTF-8")
	assert.True(t, strings.HasPrefix(code, "中鸟"),
		"prefix must be the first rune of each word uppercased, got %q", code)
}

// TestRemapLegacyCode verifies that retired eBird species codes are
// remapped to their current replacements.
func TestRemapLegacyCode(t *testing.T) {
	t.Parallel()
	t.Attr("component", "birdnet-taxonomy")
	t.Attr("category", "remapping")

	tests := []struct {
		name     string
		code     string
		expected string
	}{
		{
			name:     "legacy hergul remapped to amhgul1",
			code:     "hergul",
			expected: "amhgul1",
		},
		{
			name:     "non-legacy code unchanged",
			code:     "amerob",
			expected: "amerob",
		},
		{
			name:     "empty code unchanged",
			code:     "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := RemapLegacyCode(tt.code)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestSplitSpeciesName verifies parsing of species name formats used by
// different models (BirdNET underscore format, Perch binomial names, etc.).
func TestSplitSpeciesName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		input          string
		wantScientific string
		wantCommon     string
	}{
		{"BirdNET underscore format", "Glaucidium passerinum_Eurasian Pygmy-Owl", "Glaucidium passerinum", "Eurasian Pygmy-Owl"},
		{"BirdNET with species code", "Turdus merula_Common Blackbird_eurbla", "Turdus merula", "Common Blackbird"},
		{"Perch binomial scientific name", "Glaucidium passerinum", "Glaucidium passerinum", ""},
		{"Perch binomial Turdus merula", "Turdus merula", "Turdus merula", ""},
		{"Perch binomial Parus major", "Parus major", "Parus major", ""},
		{"unknown underscore label splits as BirdNET format", "dog_bark", "dog", "bark"},
		{"Perch single-token class", "Dog", "Dog", ""},
		{"Perch multi-word class", "Domestic_sounds_and_home_sounds", "Domestic_sounds_and_home_sounds", "Domestic sounds and home sounds"},
		{"Perch one-underscore class", "Power_tool", "Power_tool", "Power tool"},
		{"common name only", "Eurasian Pygmy-Owl", "", "Eurasian Pygmy-Owl"},
		{"single word", "unknown", "unknown", ""},
		{"empty string", "", "", ""},
		{"both words capitalized (not binomial)", "Great Tit", "", "Great Tit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotSci, gotCommon := SplitSpeciesName(tt.input)
			assert.Equal(t, tt.wantScientific, gotSci, "scientific name")
			assert.Equal(t, tt.wantCommon, gotCommon, "common name")
		})
	}
}

// TestGetSpeciesCodeFromName_LegacyRemapping verifies that looking up
// Herring Gull by name returns the remapped code "amhgul1" instead
// of the retired "hergul".
func TestGetSpeciesCodeFromName_LegacyRemapping(t *testing.T) {
	t.Parallel()
	t.Attr("component", "birdnet-taxonomy")
	t.Attr("category", "remapping")

	taxonomyMap, scientificIndex, err := LoadTaxonomyData("")
	require.NoError(t, err, "failed to load embedded taxonomy data")

	// Herring Gull should return the remapped code
	code, found := GetSpeciesCodeFromName(taxonomyMap, scientificIndex, "Larus argentatus_Herring Gull")
	require.True(t, found, "Herring Gull should exist in taxonomy")
	assert.Equal(t, "amhgul1", code, "legacy hergul should be remapped to amhgul1")

	// Also test with scientific-name-only format
	code2, found2 := GetSpeciesCodeFromName(taxonomyMap, scientificIndex, "Larus argentatus (Herring Gull)")
	require.True(t, found2, "Herring Gull should be found with parenthetical format")
	assert.Equal(t, "amhgul1", code2, "legacy hergul should be remapped to amhgul1")
}

// TestGetSpeciesCodeFromName_NonLegacyUnchanged verifies that species
// without legacy remappings return their original codes.
func TestGetSpeciesCodeFromName_NonLegacyUnchanged(t *testing.T) {
	t.Parallel()
	t.Attr("component", "birdnet-taxonomy")
	t.Attr("category", "remapping")

	taxonomyMap, scientificIndex, err := LoadTaxonomyData("")
	require.NoError(t, err, "failed to load embedded taxonomy data")

	// American Robin should return its normal code unchanged
	code, found := GetSpeciesCodeFromName(taxonomyMap, scientificIndex, "Turdus migratorius_American Robin")
	require.True(t, found, "American Robin should exist in taxonomy")
	assert.Equal(t, "amerob", code, "non-legacy code should be returned unchanged")
}

// TestLabelSplitters_AgreeOnScientificName pins that every label splitter keys a
// label identically, so the primary detection, additional results and stored
// names agree.
func TestLabelSplitters_AgreeOnScientificName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		label       string
		classCommon bool
	}{
		{"BirdNET two-part", "Turdus merula_Common Blackbird", false},
		{"BirdNET three-part", "Turdus merula_Common Blackbird_eurbla", false},
		{"Perch binomial", "Glaucidium passerinum", false},
		{"single token", "Dog", false},
		{"one underscore class", "Power_tool", true},
		{"two underscore class", "Car_passing_by", true},
		{"many underscore class", "Male_speech_and_man_speaking", true},
		{"domestic sounds", "Domestic_sounds_and_home_sounds", true},
		{"domestic animals", "Domestic_animals_and_pets", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sci, common := SplitSpeciesName(tt.label)
			parsed := detection.ParseSpeciesString(tt.label)
			assert.Equal(t, sci, parsed.ScientificName)
			assert.Equal(t, sci, detection.ExtractScientificName(tt.label))
			if tt.classCommon {
				assert.Equal(t, common, parsed.CommonName)
			}
		})
	}

	sciA, _ := SplitSpeciesName("Domestic_sounds_and_home_sounds")
	sciB, _ := SplitSpeciesName("Domestic_animals_and_pets")
	assert.NotEqual(t, sciA, sciB)
}
