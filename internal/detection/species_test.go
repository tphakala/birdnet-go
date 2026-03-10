package detection

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
