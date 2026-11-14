//go:build integration

package v2

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"gorm.io/gorm"
)

const (
	neighborsSpeciesName     = "Parus major"
	neighborsOtherName       = "Cyanistes caeruleus"
	neighborsCaptureName     = "test:capture_neighbor_statements"
	neighborsExplainSubquery = "SUBQUERY"
	neighborsExtraColumn     = "Extra"
	neighborsSelectTypeCol   = "select_type"
	neighborsOptimizedAway   = "Select tables optimized away"
	neighborsNoMinMaxRow     = "No matching min/max row"
	neighborsMinStatements   = 3
)

// TestMySQL_GetNeighborIDs runs the neighbor lookup through the production
// MySQL DSN under both table layouts. It pins that the v2_ table names are used,
// that a NULL aggregate and an unsigned BIGINT scan through the MySQL driver, and
// that every subquery of every statement is answered from one index seek. A
// rewrite to ORDER BY id LIMIT 1 returns the same rows, and SQLite plans it as a
// seek too, but MySQL 8.4 walks the primary key for it.
func TestMySQL_GetNeighborIDs(t *testing.T) {
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
			ctx := t.Context()

			labelType := entities.LabelType{Name: "neighbors_test"}
			require.NoError(t, db.Create(&labelType).Error)
			modelA := entities.AIModel{Name: "NeighborsA", Version: "1", ModelType: entities.ModelTypeBird}
			modelB := entities.AIModel{Name: "NeighborsB", Version: "1", ModelType: entities.ModelTypeBird}
			require.NoError(t, db.Create(&modelA).Error)
			require.NoError(t, db.Create(&modelB).Error)
			newLabel := func(name string, model entities.AIModel) *entities.Label {
				l := &entities.Label{ScientificName: name, ModelID: model.ID, LabelTypeID: labelType.ID}
				require.NoError(t, db.Create(l).Error)
				return l
			}
			parusA, parusB, other := newLabel(neighborsSpeciesName, modelA), newLabel(neighborsSpeciesName, modelB), newLabel(neighborsOtherName, modelA)
			add := func(l *entities.Label) uint {
				d := &entities.Detection{LabelID: l.ID, ModelID: l.ModelID, Confidence: 0.8, DetectedAt: 1000}
				require.NoError(t, db.Create(d).Error)
				return d.ID
			}
			first, second, lone, fourth, fifth := add(parusA), add(parusB), add(other), add(parusA), add(parusB)

			repo := repository.NewDetectionRepository(db, nil, useV2Prefix, true)

			var statements []string
			require.NoError(t, db.Callback().Row().After("gorm:row").Register(neighborsCaptureName, func(tx *gorm.DB) {
				statements = append(statements, tx.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...))
			}))
			// Crosses the two models: the species chain runs parusA, parusB, parusA, parusB.
			middle, err := repo.GetNeighborIDs(ctx, fourth)
			// Remove the capture before any EXPLAIN, which goes through the same chain.
			require.NoError(t, db.Callback().Row().Remove(neighborsCaptureName))
			require.NoError(t, err)
			assert.Equal(t, repository.DetectionNeighbors{Previous: lone, Next: fifth, PreviousSameSpecies: second, NextSameSpecies: fifth}, *middle)

			got, err := repo.GetNeighborIDs(ctx, lone)
			require.NoError(t, err)
			assert.Equal(t, repository.DetectionNeighbors{Previous: second, Next: fourth}, *got, "the only row of its species has no same species neighbors")

			got, err = repo.GetNeighborIDs(ctx, first)
			require.NoError(t, err)
			assert.Equal(t, repository.DetectionNeighbors{Next: second, NextSameSpecies: second}, *got, "the lowest row has no previous neighbors")

			require.GreaterOrEqual(t, len(statements), neighborsMinStatements, "the overall pair and one pair per label")
			for _, statement := range statements {
				var plan []map[string]any
				require.NoError(t, db.Raw("EXPLAIN "+statement).Scan(&plan).Error, statement)
				for _, row := range plan {
					if !strings.EqualFold(fmt.Sprint(row[neighborsSelectTypeCol]), neighborsExplainSubquery) {
						continue
					}
					extra := fmt.Sprint(row[neighborsExtraColumn])
					assert.True(t, extra == neighborsOptimizedAway || extra == neighborsNoMinMaxRow,
						"subquery is not answered from one index seek: %v (%s)", row, statement)
				}
			}
		})
	}
}
