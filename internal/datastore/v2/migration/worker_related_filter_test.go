package migration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

func TestFilterBatchByConfidence(t *testing.T) {
	// Note: minPredictionConfidence is 0.2 (20%)
	tests := []struct {
		name     string
		batch    []datastore.Results
		expected int
	}{
		{
			name: "filters out low confidence",
			batch: []datastore.Results{
				{ID: 1, Species: "Parus major", Confidence: 0.10},  // below threshold
				{ID: 2, Species: "Parus major", Confidence: 0.20},  // at threshold
				{ID: 3, Species: "Parus major", Confidence: 0.50},  // above threshold
				{ID: 4, Species: "Parus major", Confidence: 0.19},  // below threshold
			},
			expected: 2, // only 0.20 and 0.50 pass
		},
		{
			name:     "empty batch returns empty",
			batch:    []datastore.Results{},
			expected: 0,
		},
		{
			name: "all above threshold",
			batch: []datastore.Results{
				{ID: 1, Species: "Parus major", Confidence: 0.25},
				{ID: 2, Species: "Parus major", Confidence: 0.95},
			},
			expected: 2,
		},
		{
			name: "all below threshold",
			batch: []datastore.Results{
				{ID: 1, Species: "Parus major", Confidence: 0.05},
				{ID: 2, Species: "Parus major", Confidence: 0.15},
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterBatchByConfidence(tt.batch)
			assert.Len(t, result, tt.expected)
		})
	}
}
