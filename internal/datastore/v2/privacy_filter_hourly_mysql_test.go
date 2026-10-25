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

// TestMySQL_PrivacyFilterHourlyAdditiveUpsert runs the additive upsert on MySQL
// under both table layouts. It catches MySQL-only rendering or semantics
// (GREATEST, ON DUPLICATE KEY with expressions), a wrong v2_ table name, and a
// drop list that orders the table after audio_sources (the foreign key would make
// Delete fail).
func TestMySQL_PrivacyFilterHourlyAdditiveUpsert(t *testing.T) {
	for _, useV2Prefix := range []bool{true, false} {
		t.Run(fmt.Sprintf("useV2Prefix=%t", useV2Prefix), func(t *testing.T) {
			dropAllTables(t, coexTestDB, coexTestDBName)

			mgr := newTestMySQLManager(t, useV2Prefix)
			t.Cleanup(func() { _ = mgr.Close() })
			require.NoError(t, mgr.Initialize())
			db := mgr.DB()

			src := entities.AudioSource{SourceURI: "mysql-test", NodeName: "n", SourceType: entities.SourceTypeFile}
			require.NoError(t, db.Create(&src).Error)

			repo := repository.NewPrivacyFilterHourlyRepository(db, nil, useV2Prefix, true)
			ctx := t.Context()
			row := func(modelID string, threshold int, hits int64, score float64) *entities.PrivacyFilterHourly {
				return &entities.PrivacyFilterHourly{
					HourStart: 3600 * 100, SourceID: src.ID, ModelID: modelID, TriggerType: "label",
					ThresholdMilli: threshold, Hits: hits, MarginLt50: hits, Discards: 1, MaxScore: score,
				}
			}
			// Lower score first, then higher, then lower again: GREATEST must keep 0.9.
			require.NoError(t, repo.AddDelta(ctx, row("m", 50, 2, 0.7)))
			require.NoError(t, repo.AddDelta(ctx, row("m", 50, 3, 0.9)))
			require.NoError(t, repo.AddDelta(ctx, row("m", 50, 1, 0.8)))
			require.NoError(t, repo.AddDelta(ctx, row("m", 60, 1, 0.5)))
			require.NoError(t, repo.AddDelta(ctx, row("other", 50, 1, 0.5)))

			table := "privacy_filter_hourlies"
			if useV2Prefix {
				table = V2TableName(table)
			}
			var rows []entities.PrivacyFilterHourly
			require.NoError(t, db.Table(table).Order("id").Find(&rows).Error)
			require.Len(t, rows, 3)
			assert.Equal(t, int64(6), rows[0].Hits)
			assert.Equal(t, int64(6), rows[0].MarginLt50)
			assert.Equal(t, int64(3), rows[0].Discards)
			assert.InDelta(t, 0.9, rows[0].MaxScore, 1e-9)

			require.NoError(t, mgr.Delete())
		})
	}
}
