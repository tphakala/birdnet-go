package v2only

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractScientificName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "concatenated scientific and common name",
			input:    "Picus viridis_vihertikka",
			expected: "Picus viridis",
		},
		{
			name:     "concatenated with species code",
			input:    "Parus major_Great Tit_gretit1",
			expected: "Parus major",
		},
		{
			name:     "scientific name only",
			input:    "Turdus merula",
			expected: "Turdus merula",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "non-species label",
			input:    "noise",
			expected: "noise",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractScientificName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
