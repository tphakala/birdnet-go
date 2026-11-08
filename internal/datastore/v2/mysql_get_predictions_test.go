//go:build integration

package v2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
)

// TestMySQL_GetPredictions_OrdersByRank runs GetPredictions through the
// production MySQL DSN. rank is a reserved word on MySQL, so the ORDER BY
// column has to be quoted for the statement to parse.
func TestMySQL_GetPredictions_OrdersByRank(t *testing.T) {
	dropAllTables(t, coexTestDB, coexTestDBName)

	mgr := newTestMySQLManager(t, true)
	require.NoError(t, mgr.Initialize())
	t.Cleanup(func() { _ = mgr.Close() })

	const detectionID = uint(1)
	seedDetection(t, detectionID, nil)

	db := mgr.DB()
	labelType := entities.LabelType{Name: "get_predictions_species"}
	require.NoError(t, db.Create(&labelType).Error)
	model := entities.AIModel{Name: "GetPredictions", Version: "1", ModelType: entities.ModelTypeBird}
	require.NoError(t, db.Create(&model).Error)

	names := []string{"Turdus merula", "Parus major", "Erithacus rubecula"}
	labelIDs := make([]uint, len(names))
	for i, name := range names {
		label := entities.Label{ScientificName: name, ModelID: model.ID, LabelTypeID: labelType.ID}
		require.NoError(t, db.Create(&label).Error)
		labelIDs[i] = label.ID
	}

	repo := repository.NewDetectionRepository(db, nil, true, true)
	ctx := t.Context()
	require.NoError(t, repo.SavePredictionsBatch(ctx, []*entities.DetectionPrediction{
		{DetectionID: detectionID, LabelID: labelIDs[2], Confidence: 0.2, Rank: 3},
		{DetectionID: detectionID, LabelID: labelIDs[0], Confidence: 0.6, Rank: 1},
		{DetectionID: detectionID, LabelID: labelIDs[1], Confidence: 0.4, Rank: 2},
	}))

	preds, err := repo.GetPredictions(ctx, detectionID)
	require.NoError(t, err)
	require.Len(t, preds, len(names))
	for i, p := range preds {
		assert.Equal(t, i+1, p.Rank, "prediction %d out of rank order", i)
	}
}
