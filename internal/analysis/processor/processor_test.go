package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

func TestConvertToAdditionalResults_DeduplicatesByScientificName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                  string
		input                 []datastore.Results
		primaryScientificName string
		expectedCount         int
		expectedFirst         string
		expectedFirstC        float64
	}{
		{
			name: "duplicate_species_keeps_highest_confidence",
			input: []datastore.Results{
				{Species: "Periparus ater_kuusitiainen", Confidence: 0.99},
				{Species: "Parus major_talitiainen", Confidence: 0.50},
				{Species: "Periparus ater_kuusitiainen", Confidence: 0.92},
			},
			primaryScientificName: "",
			expectedCount:         2,
			expectedFirst:         "Periparus ater",
			expectedFirstC:        0.99,
		},
		{
			name: "duplicate_lower_confidence_first",
			input: []datastore.Results{
				{Species: "Periparus ater_kuusitiainen", Confidence: 0.50},
				{Species: "Parus major_talitiainen", Confidence: 0.30},
				{Species: "Periparus ater_kuusitiainen", Confidence: 0.95},
			},
			primaryScientificName: "",
			expectedCount:         2,
			expectedFirst:         "Periparus ater",
			expectedFirstC:        0.95,
		},
		{
			name: "multiple_different_duplicates",
			input: []datastore.Results{
				{Species: "Periparus ater_kuusitiainen", Confidence: 0.99},
				{Species: "Parus major_talitiainen", Confidence: 0.80},
				{Species: "Corvus corax_korppi", Confidence: 0.70},
				{Species: "Periparus ater_kuusitiainen", Confidence: 0.92},
				{Species: "Parus major_talitiainen", Confidence: 0.85},
			},
			primaryScientificName: "",
			expectedCount:         3,
			expectedFirst:         "Periparus ater",
			expectedFirstC:        0.99,
		},
		{
			name: "no_duplicates_unchanged",
			input: []datastore.Results{
				{Species: "Periparus ater_kuusitiainen", Confidence: 0.99},
				{Species: "Parus major_talitiainen", Confidence: 0.50},
				{Species: "Corvus corax_korppi", Confidence: 0.30},
			},
			primaryScientificName: "",
			expectedCount:         3,
			expectedFirst:         "Periparus ater",
			expectedFirstC:        0.99,
		},
		{
			name: "excludes_primary_species",
			input: []datastore.Results{
				{Species: "Periparus ater_kuusitiainen", Confidence: 0.99},
				{Species: "Parus major_talitiainen", Confidence: 0.50},
				{Species: "Corvus corax_korppi", Confidence: 0.30},
			},
			primaryScientificName: "Periparus ater",
			expectedCount:         2,
			expectedFirst:         "Parus major",
			expectedFirstC:        0.50,
		},
		{
			name: "excludes_primary_with_duplicates",
			input: []datastore.Results{
				{Species: "Periparus ater_kuusitiainen", Confidence: 0.99},
				{Species: "Parus major_talitiainen", Confidence: 0.50},
				{Species: "Periparus ater_kuusitiainen", Confidence: 0.92},
			},
			primaryScientificName: "Periparus ater",
			expectedCount:         1,
			expectedFirst:         "Parus major",
			expectedFirstC:        0.50,
		},
		{
			name:                  "empty_input",
			input:                 []datastore.Results{},
			primaryScientificName: "",
			expectedCount:         0,
		},
		{
			name:                  "nil_input",
			input:                 nil,
			primaryScientificName: "",
			expectedCount:         0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := convertToAdditionalResults(tt.input, tt.primaryScientificName)
			require.Len(t, result, tt.expectedCount)

			if tt.expectedCount > 0 {
				assert.Equal(t, tt.expectedFirst, result[0].Species.ScientificName)
				assert.InDelta(t, tt.expectedFirstC, result[0].Confidence, 0.001)
			}

			// Verify no duplicate scientific names in output
			seen := make(map[string]bool)
			for _, r := range result {
				assert.False(t, seen[r.Species.ScientificName],
					"duplicate scientific name in output: %s", r.Species.ScientificName)
				seen[r.Species.ScientificName] = true
			}
		})
	}
}
