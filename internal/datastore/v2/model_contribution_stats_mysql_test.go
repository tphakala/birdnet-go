//go:build integration

package v2

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
)

// TestMySQL_GetModelContributionStats runs the model contribution statistics
// queries on MySQL under both table layouts. It catches ONLY_FULL_GROUP_BY
// rejections, DECIMAL results of SUM that do not scan into int64, and a wrong
// table name under the v2_ prefix.
func TestMySQL_GetModelContributionStats(t *testing.T) {
	for _, useV2Prefix := range []bool{true, false} {
		t.Run(fmt.Sprintf("useV2Prefix=%t", useV2Prefix), func(t *testing.T) {
			dropAllTables(t, coexTestDB, coexTestDBName)

			mgr := newTestMySQLManager(t, useV2Prefix)
			t.Cleanup(func() {
				_ = mgr.Delete()
				_ = mgr.Close()
			})
			require.NoError(t, mgr.Initialize())
			db := mgr.DB()

			labelType := entities.LabelType{Name: "stats_test_species"}
			require.NoError(t, db.Create(&labelType).Error)
			modelA := entities.AIModel{Name: "StatsA", Version: "1", ModelType: entities.ModelTypeBird}
			modelB := entities.AIModel{Name: "StatsB", Version: "1", ModelType: entities.ModelTypeBird}
			require.NoError(t, db.Create(&modelA).Error)
			require.NoError(t, db.Create(&modelB).Error)
			label := entities.Label{ScientificName: "Turdus merula", ModelID: modelA.ID, LabelTypeID: labelType.ID}
			require.NoError(t, db.Create(&label).Error)

			newDetection := func(detectedAt int64) uint {
				t.Helper()
				det := entities.Detection{LabelID: label.ID, ModelID: modelA.ID, DetectedAt: detectedAt, Confidence: 0.8}
				require.NoError(t, db.Create(&det).Error)
				return det.ID
			}
			newContribution := func(detectionID, modelID uint, hits int, maxConf float64, minConf, sum *float64) {
				t.Helper()
				row := entities.DetectionModelContribution{
					DetectionID: detectionID, ModelID: modelID, HitCount: hits, MaxConfidence: maxConf,
				}
				if minConf != nil {
					first, last := int64(1), int64(2)
					row.FirstHitAt, row.LastHitAt = &first, &last
					row.MinConfidence, row.ConfidenceSum = minConf, sum
				}
				require.NoError(t, db.Create(&row).Error)
			}

			// d1: A only with stats; d2: A with stats + B without stats, false_positive.
			d1, d2 := newDetection(1000), newDetection(1500)
			minA1, sumA1 := 0.6, 2.1
			minA2, sumA2 := 0.7, 1.5
			newContribution(d1, modelA.ID, 3, 0.9, &minA1, &sumA1)
			newContribution(d2, modelA.ID, 2, 0.8, &minA2, &sumA2)
			newContribution(d2, modelB.ID, 1, 0.75, nil, nil)
			require.NoError(t, db.Create(&entities.DetectionReview{DetectionID: d2, Verified: entities.VerificationFalsePositive}).Error)

			repo := repository.NewInsightsRepository(db, useV2Prefix, true)
			stats, err := repo.GetModelContributionStats(t.Context(), 999, 2000)
			require.NoError(t, err)

			assert.Equal(t, int64(2), stats.TotalDetections)
			assert.Equal(t, int64(2), stats.DetectionsWithContributions)
			assert.Equal(t, int64(6), stats.TotalHits)
			require.Len(t, stats.Models, 2)

			a, b := &stats.Models[0], &stats.Models[1]
			assert.Equal(t, modelA.ID, a.ModelID)
			assert.Equal(t, int64(2), a.Detections)
			assert.Equal(t, int64(5), a.Hits)
			assert.Equal(t, int64(1), a.SoleDetections)
			assert.Equal(t, int64(2), a.DetectionsWithStats)
			require.NotNil(t, a.MinConfidence)
			assert.InDelta(t, 0.6, *a.MinConfidence, 1e-9)
			require.NotNil(t, a.MeanConfidence)
			assert.InDelta(t, 3.6/5.0, *a.MeanConfidence, 1e-9)
			assert.Equal(t, int64(1), a.FalsePositive)
			assert.Equal(t, int64(1), a.Unreviewed)

			assert.Equal(t, modelB.ID, b.ModelID)
			assert.Equal(t, int64(1), b.Detections)
			assert.Equal(t, int64(0), b.SoleDetections)
			assert.Equal(t, int64(0), b.DetectionsWithStats)
			assert.Nil(t, b.MinConfidence)
			assert.Nil(t, b.MeanConfidence, "a model with no recorded stats has no mean")
			assert.InDelta(t, 0.75, b.MaxConfidence, 1e-9, "the maximum comes from every row, including rows without stats")
			assert.Equal(t, int64(1), b.FalsePositive)

			assert.Equal(t, []repository.ModelAgreement{
				{ModelAID: modelA.ID, ModelBID: modelB.ID, SharedDetections: 1},
			}, stats.Agreements)
		})
	}
}
