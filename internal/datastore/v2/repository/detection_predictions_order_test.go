package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// TestGetPredictions_OrdersByRank pins that GetPredictions returns one
// detection's predictions in ascending rank order. The MySQL integration test
// covers the quoting of the rank column.
func TestGetPredictions_OrdersByRank(t *testing.T) {
	t.Parallel()

	db := setupDetectionTestDB(t)
	require.NoError(t, db.AutoMigrate(&entities.DetectionPrediction{}))

	repo := NewDetectionRepository(db, nil, false, false)
	ctx := t.Context()

	const detectionID = uint(1)
	require.NoError(t, repo.SavePredictionsBatch(ctx, []*entities.DetectionPrediction{
		{DetectionID: detectionID, LabelID: 3, Confidence: 0.2, Rank: 3},
		{DetectionID: detectionID, LabelID: 1, Confidence: 0.6, Rank: 1},
		{DetectionID: detectionID, LabelID: 2, Confidence: 0.4, Rank: 2},
		{DetectionID: detectionID + 1, LabelID: 9, Confidence: 0.9, Rank: 1},
	}))

	preds, err := repo.GetPredictions(ctx, detectionID)
	require.NoError(t, err)
	require.Len(t, preds, 3)
	for i, p := range preds {
		assert.Equal(t, i+1, p.Rank, "prediction %d out of rank order", i)
		assert.Equal(t, detectionID, p.DetectionID)
	}
}
