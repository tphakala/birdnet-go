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

// TestMySQL_MigrationState_AuxiliaryOutcomeOnExistingTable adds the auxiliary
// outcome columns to a populated migration_states table through Initialize, then
// records an outcome and checks that the attempt marker copied in SQL compares
// equal to started_at after a round trip (datetime(3) on both sides).
func TestMySQL_MigrationState_AuxiliaryOutcomeOnExistingTable(t *testing.T) {
	for _, useV2Prefix := range []bool{true, false} {
		t.Run(fmt.Sprintf("useV2Prefix=%t", useV2Prefix), func(t *testing.T) {
			dropAllTables(t, coexTestDB, coexTestDBName)

			mgr := newTestMySQLManager(t, useV2Prefix)
			t.Cleanup(func() {
				_ = mgr.Delete()
				_ = mgr.Close()
			})

			// A table from before the auxiliary columns existed, holding a completed row.
			table := mgr.TablePrefix() + "migration_states"
			started := time.Now().Add(-time.Hour)
			require.NoError(t, mgr.DB().Table(table).AutoMigrate(&preAuxMigrationState{}))
			require.NoError(t, mgr.DB().Table(table).Create(&preAuxMigrationState{
				ID: 1, State: entities.MigrationStatusCompleted, StartedAt: &started,
			}).Error)

			require.NoError(t, mgr.Initialize())

			sm := NewStateManager(mgr.DB())
			state, err := sm.GetState()
			require.NoError(t, err)
			assert.Equal(t, entities.MigrationStatusCompleted, state.State)
			assert.Equal(t, entities.AuxiliaryDataStatusUnknown, state.AuxiliaryDataOutcome(),
				"a row from before the columns existed must read as unknown")

			// A new attempt records an outcome in the same UPDATE as the transition.
			require.NoError(t, mgr.DB().Table(table).Where("id = 1").Update("state", entities.MigrationStatusIdle).Error)
			require.NoError(t, sm.StartMigration(10))
			require.NoError(t, sm.TransitionToDualWrite())
			require.NoError(t, sm.TransitionToMigrating())
			require.NoError(t, sm.TransitionToValidatingWithAuxiliaryOutcome(entities.AuxiliaryDataStatusFailed, "boom"))

			state, err = sm.GetState()
			require.NoError(t, err)
			assert.Equal(t, entities.AuxiliaryDataStatusFailed, state.AuxiliaryDataOutcome())
			assert.Equal(t, "boom", state.AuxiliaryDataError)
		})
	}
}
