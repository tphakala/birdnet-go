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

const (
	statsSpeciesName   = "Parus major"
	statsLegacyName    = "Parus major_Great Tit"
	statsOtherName     = "Cyanistes caeruleus"
	statsEmptyName     = "Turdus merula"
	statsConfidenceTol = 1e-9
)

// TestMySQL_GetSpeciesStats runs the species statistics through the production
// MySQL DSN under both table layouts. It pins what SQLite cannot: the v2_ table
// names in the hand-built statement, the aggregate column types through the MySQL
// driver (COUNT, MIN, AVG and MAX over a CASE), NULL aggregates, and the IN
// expansion of unsigned IDs. There is no plan assertion: the range versus full
// scan choice is cost based and flips with table size.
func TestMySQL_GetSpeciesStats(t *testing.T) {
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

			labelType := entities.LabelType{Name: "species_stats_test"}
			require.NoError(t, db.Create(&labelType).Error)
			modelA := entities.AIModel{Name: "StatsA", Version: "1", ModelType: entities.ModelTypeBird}
			modelB := entities.AIModel{Name: "StatsB", Version: "1", ModelType: entities.ModelTypeBird}
			require.NoError(t, db.Create(&modelA).Error)
			require.NoError(t, db.Create(&modelB).Error)
			newLabel := func(name string, model entities.AIModel) *entities.Label {
				l := &entities.Label{ScientificName: name, ModelID: model.ID, LabelTypeID: labelType.ID}
				require.NoError(t, db.Create(l).Error)
				return l
			}
			add := func(l *entities.Label, at int64, conf float64, review entities.VerificationStatus) {
				d := &entities.Detection{LabelID: l.ID, ModelID: l.ModelID, Confidence: conf, DetectedAt: at}
				require.NoError(t, db.Create(d).Error)
				if review != "" {
					require.NoError(t, db.Create(&entities.DetectionReview{DetectionID: d.ID, Verified: review}).Error)
				}
			}

			parusA, parusB, legacy := newLabel(statsSpeciesName, modelA), newLabel(statsSpeciesName, modelB), newLabel(statsLegacyName, modelA)
			other := newLabel(statsOtherName, modelA)
			newLabel(statsEmptyName, modelA)
			add(parusA, 1000, 0.95, entities.VerificationFalsePositive)
			add(parusA, 2000, 0.60, "")
			add(parusB, 3000, 0.80, entities.VerificationCorrect)
			add(legacy, 4000, 0.40, "")
			add(other, 1500, 0.99, "")

			repo := repository.NewDetectionRepository(db, nil, useV2Prefix, true)

			got, err := repo.GetSpeciesStats(t.Context(), statsSpeciesName)
			require.NoError(t, err)
			assert.Equal(t, int64(3), got.Detections)
			assert.Equal(t, int64(2000), got.FirstDetectedAt)
			assert.Equal(t, int64(4000), got.LastDetectedAt)
			assert.InDelta(t, (0.60+0.80+0.40)/3, got.AvgConfidence, statsConfidenceTol)
			assert.InDelta(t, 0.80, got.MaxConfidence, statsConfidenceTol)
			assert.Equal(t, int64(1), got.CorrectReviews)
			assert.Equal(t, int64(1), got.FalsePositiveReviews)

			// Entered in the legacy form, the same species.
			viaLegacy, err := repo.GetSpeciesStats(t.Context(), statsLegacyName)
			require.NoError(t, err)
			assert.Equal(t, got, viaLegacy)

			// A label with no detections gives zeros and NULL aggregates scan cleanly.
			zero, err := repo.GetSpeciesStats(t.Context(), statsEmptyName)
			require.NoError(t, err)
			assert.Equal(t, &repository.SpeciesStats{}, zero)
		})
	}
}
