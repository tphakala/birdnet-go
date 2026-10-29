//go:build integration

package v2

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// TestMySQL_MigrationState_RelatedSyncMarkerRoundTrip adds the related sync
// columns to a populated migration_states table through Initialize, binds the
// cursors to an attempt, and checks that the marker copied in SQL compares equal
// to started_at after a round trip. A marker that never compared equal would
// silently disable the tail sync on MySQL and MariaDB.
func TestMySQL_MigrationState_RelatedSyncMarkerRoundTrip(t *testing.T) {
	for _, useV2Prefix := range []bool{true, false} {
		t.Run(fmt.Sprintf("useV2Prefix=%t", useV2Prefix), func(t *testing.T) {
			dropAllTables(t, coexTestDB, coexTestDBName)

			mgr := newTestMySQLManager(t, useV2Prefix)
			t.Cleanup(func() {
				_ = mgr.Delete()
				_ = mgr.Close()
			})

			// A table from before the auxiliary and related sync columns existed.
			table := mgr.TablePrefix() + "migration_states"
			started := time.Now().Add(-time.Hour)
			require.NoError(t, mgr.DB().Table(table).AutoMigrate(&preAuxMigrationState{}))
			require.NoError(t, mgr.DB().Table(table).Create(&preAuxMigrationState{
				ID: 1, State: entities.MigrationStatusIdle, StartedAt: &started,
			}).Error)
			require.NoError(t, mgr.Initialize())

			sm := NewStateManager(mgr.DB())
			state, err := sm.GetState()
			require.NoError(t, err)
			_, ok := state.RelatedSyncCursors()
			assert.False(t, ok, "a row from before the columns existed must read as unrecorded")

			require.NoError(t, sm.StartMigration(10))
			cursors, err := sm.BeginRelatedSync()
			require.NoError(t, err)
			assert.Equal(t, entities.RelatedSyncCursors{}, cursors)

			require.NoError(t, sm.SetRelatedCommentsCursor(6))
			require.NoError(t, sm.SetRelatedPredictionsCursor(4, 8))
			require.NoError(t, sm.SetRelatedReconciledAt(time.Now()))

			state, err = sm.GetState()
			require.NoError(t, err)
			resumed, ok := state.RelatedSyncCursors()
			require.True(t, ok, "the marker must equal started_at after a MySQL round trip")
			assert.Equal(t, entities.RelatedSyncCursors{CommentID: 6, PredictionNoteID: 4, PredictionResultID: 8}, resumed)
			assert.NotNil(t, state.RelatedReconciledAt)
		})
	}
}
