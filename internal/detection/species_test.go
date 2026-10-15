package detection

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractScientificName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"standard label", "Turdus merula_Common Blackbird", "Turdus merula"},
		{"with species code", "Parus major_Great Tit_gretit1", "Parus major"},
		{"locale label", "Picus viridis_vihertikka", "Picus viridis"},
		{"no underscore", "NoUnderscore", "NoUnderscore"},
		{"extra underscores", "Sci_Common_Extra", "Sci"},
		{"scientific name only", "Turdus merula", "Turdus merula"},
		{"non-species label", "noise", "noise"},
		{"empty string", "", ""},
		{"trailing carriage return", "Turdus merula_Common Blackbird\r", "Turdus merula"},
		{"carriage return before underscore", "Turdus merula\r_Common Blackbird", "Turdus merula"},
		{"leading whitespace", "  Turdus merula_Common Blackbird", "Turdus merula"},
		{"trailing whitespace scientific only", "Turdus merula  ", "Turdus merula"},
		{"carriage return scientific only", "Turdus merula\r", "Turdus merula"},
		{"Perch binomial", "Glaucidium passerinum", "Glaucidium passerinum"},
		{"Perch single-token class", "Dog", "Dog"},
		{"Perch one-underscore class", "Power_tool", "Power_tool"},
		{"Perch two-word class", "Car_passing_by", "Car_passing_by"},
		{"Perch domestic sounds class", "Domestic_sounds_and_home_sounds", "Domestic_sounds_and_home_sounds"},
		{"Perch domestic animals class", "Domestic_animals_and_pets", "Domestic_animals_and_pets"},
		{"Perch class with CRLF", "Power_tool\r", "Power_tool"},
		{"legacy stored fragment", "Domestic", "Domestic"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, ExtractScientificName(tt.input))
		})
	}
}

func TestParseSpeciesString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected Species
	}{
		{
			name:  "three parts with species code",
			input: "Turdus merula_Common Blackbird_eurbla",
			expected: Species{
				ScientificName: "Turdus merula",
				CommonName:     "Common Blackbird",
				Code:           "eurbla",
			},
		},
		{
			name:  "two parts standard format",
			input: "Turdus merula_Common Blackbird",
			expected: Species{
				ScientificName: "Turdus merula",
				CommonName:     "Common Blackbird",
				Code:           "",
			},
		},
		{
			name:  "space-separated without underscore uses full string as scientific name",
			input: "Common Blackbird",
			expected: Species{
				ScientificName: "Common Blackbird",
				CommonName:     "Common Blackbird",
				Code:           "",
			},
		},
		{
			name:  "single word fallback",
			input: "Blackbird",
			expected: Species{
				ScientificName: "Blackbird",
				CommonName:     "Blackbird",
				Code:           "",
			},
		},
		{
			name:  "empty string",
			input: "",
			expected: Species{
				ScientificName: "",
				CommonName:     "",
				Code:           "",
			},
		},
		{
			name:  "whitespace only is trimmed to empty",
			input: "   ",
			expected: Species{
				ScientificName: "",
				CommonName:     "",
				Code:           "",
			},
		},
		{
			name:  "input with tab returns as-is",
			input: "Species\twith\ttab",
			expected: Species{
				ScientificName: "Species\twith\ttab",
				CommonName:     "Species\twith\ttab",
				Code:           "",
			},
		},
		{
			name:  "input with newline returns as-is",
			input: "Species\nwith\nnewline",
			expected: Species{
				ScientificName: "Species\nwith\nnewline",
				CommonName:     "Species\nwith\nnewline",
				Code:           "",
			},
		},
		{
			name:  "leading and trailing whitespace trimmed",
			input: "  Turdus merula_Common Blackbird  ",
			expected: Species{
				ScientificName: "Turdus merula",
				CommonName:     "Common Blackbird",
				Code:           "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ParseSpeciesString(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSpeciesString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		species  Species
		expected string
	}{
		{
			name:     "prefers common name",
			species:  Species{ScientificName: "Turdus merula", CommonName: "Common Blackbird"},
			expected: "Common Blackbird",
		},
		{
			name:     "falls back to scientific name",
			species:  Species{ScientificName: "Turdus merula", CommonName: ""},
			expected: "Turdus merula",
		},
		{
			name:     "empty species returns empty",
			species:  Species{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.species.String())
		})
	}
}

func TestExtractScientificName_Idempotent(t *testing.T) {
	t.Parallel()
	inputs := []string{
		"Turdus merula_Common Blackbird",
		"Parus major_Great Tit_gretit1",
		"Glaucidium passerinum",
		"Dog",
		"Power_tool",
		"Domestic_sounds_and_home_sounds",
		"Bathtub_(filling_or_washing)",
		"Domestic",
		"dog_bark",
		"",
	}
	for _, in := range inputs {
		once := ExtractScientificName(in)
		assert.Equal(t, once, ExtractScientificName(once), "input %q", in)
	}
}

func TestParseSpeciesString_PerchSoundClass(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected Species
	}{
		{"domestic sounds", "Domestic_sounds_and_home_sounds", Species{ScientificName: "Domestic_sounds_and_home_sounds", CommonName: "Domestic sounds and home sounds"}},
		{"car passing by", "Car_passing_by", Species{ScientificName: "Car_passing_by", CommonName: "Car passing by"}},
		{"power tool", "Power_tool", Species{ScientificName: "Power_tool", CommonName: "Power tool"}},
		{"BirdNET three-part unchanged", "Turdus merula_Common Blackbird_eurbla", Species{ScientificName: "Turdus merula", CommonName: "Common Blackbird", Code: "eurbla"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, ParseSpeciesString(tt.input))
		})
	}
}

func TestSpecies_StoredLabel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   Species
		want string
	}{
		{"BirdNET two parts", Species{ScientificName: "Turdus merula", CommonName: "Common Blackbird"}, "Turdus merula_Common Blackbird"},
		{"BirdNET with code", Species{ScientificName: "Turdus merula", CommonName: "Common Blackbird", Code: "eurbla"}, "Turdus merula_Common Blackbird_eurbla"},
		{"Perch multi-word class", Species{ScientificName: "Domestic_sounds_and_home_sounds", CommonName: "Domestic sounds and home sounds"}, "Domestic_sounds_and_home_sounds"},
		{"Perch class with placeholder code", Species{ScientificName: "Domestic_sounds_and_home_sounds", CommonName: "Domestic sounds and home sounds", Code: "DO1a2b3c"}, "Domestic_sounds_and_home_sounds"},
		{"Perch class with localized common name", Species{ScientificName: "Power_tool", CommonName: "Sähkötyökalu"}, "Power_tool"},
		{"Perch class with empty common name", Species{ScientificName: "Power_tool"}, "Power_tool"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.in.StoredLabel()
			assert.Equal(t, tt.want, got)
			// The stored form must parse back to the same identity.
			assert.Equal(t, tt.in.ScientificName, ParseSpeciesString(got).ScientificName)
			assert.Equal(t, tt.in.ScientificName, ExtractScientificName(got))
		})
	}
}
