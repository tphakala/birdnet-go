package migration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

func TestFilterBatchByConfidence(t *testing.T) {
	// Note: minPredictionConfidence is 0.3 (30%)
	tests := []struct {
		name     string
		batch    []datastore.Results
		expected int
	}{
		{
			name: "filters out low confidence",
			batch: []datastore.Results{
				{ID: 1, Species: "Parus major", Confidence: 0.10}, // below threshold
				{ID: 2, Species: "Parus major", Confidence: 0.30}, // at threshold
				{ID: 3, Species: "Parus major", Confidence: 0.50}, // above threshold
				{ID: 4, Species: "Parus major", Confidence: 0.29}, // below threshold
			},
			expected: 2, // only 0.30 and 0.50 pass
		},
		{
			name:     "empty batch returns empty",
			batch:    []datastore.Results{},
			expected: 0,
		},
		{
			name: "all above threshold",
			batch: []datastore.Results{
				{ID: 1, Species: "Parus major", Confidence: 0.35},
				{ID: 2, Species: "Parus major", Confidence: 0.95},
			},
			expected: 2,
		},
		{
			name: "all below threshold",
			batch: []datastore.Results{
				{ID: 1, Species: "Parus major", Confidence: 0.05},
				{ID: 2, Species: "Parus major", Confidence: 0.25},
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

// TestConvertResultsToPredictions_SameThresholdAsLabelFilter pins that a row
// below the label-resolution threshold is dropped even when its species is
// already cached, so the one-time copy and the tail pass keep the same rows.
func TestConvertResultsToPredictions_SameThresholdAsLabelFilter(t *testing.T) {
	m := &RelatedDataMigrator{}
	batch := []datastore.Results{
		{ID: 1, NoteID: 7, Species: "Parus major", Confidence: 0.80},
		{ID: 2, NoteID: 7, Species: "Cyanistes caeruleus", Confidence: 0.25},
		{ID: 3, NoteID: 7, Species: "Cyanistes caeruleus", Confidence: 0.30},
	}
	existing := map[uint]struct{}{7: {}}
	labels := map[string]uint{"Parus major": 1, "Cyanistes caeruleus": 2}

	predictions, skipped := m.convertResultsToPredictions(batch, existing, labels, &predictionRankTracker{})

	assert.Len(t, predictions, 2)
	assert.Equal(t, 1, skipped)
	for _, p := range predictions {
		assert.GreaterOrEqual(t, p.Confidence, float64(minPredictionConfidence))
	}
}
