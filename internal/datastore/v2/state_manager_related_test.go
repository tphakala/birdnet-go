package v2

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// startedAttempt moves a fresh state manager to a started migration attempt.
func startedAttempt(t *testing.T, sm *StateManager) {
	t.Helper()
	require.NoError(t, sm.StartMigration(10))
}

// relatedColumns reads the five related sync columns of the state row.
func relatedColumns(t *testing.T, sm *StateManager) *entities.MigrationState {
	t.Helper()
	state, err := sm.GetState()
	require.NoError(t, err)
	return state
}

func requireRelatedColumnsCleared(t *testing.T, state *entities.MigrationState) {
	t.Helper()
	assert.Nil(t, state.RelatedSyncStartedAt)
	assert.Nil(t, state.RelatedCommentsCursor)
	assert.Nil(t, state.RelatedPredictionsNoteID)
	assert.Nil(t, state.RelatedPredictionsResultID)
	assert.Nil(t, state.RelatedReconciledAt)
}

func TestStateManager_RelatedSync(t *testing.T) {
	t.Run("begin binds the attempt and a second call resumes", func(t *testing.T) {
		sm, cleanup := setupStateManager(t)
		defer cleanup()
		startedAttempt(t, sm)

		cursors, err := sm.BeginRelatedSync()
		require.NoError(t, err)
		assert.Equal(t, entities.RelatedSyncCursors{}, cursors)

		require.NoError(t, sm.SetRelatedCommentsCursor(7))
		require.NoError(t, sm.SetRelatedPredictionsCursor(5, 9))
		reconciled := time.Now().Truncate(time.Millisecond)
		require.NoError(t, sm.SetRelatedReconciledAt(reconciled))

		resumed, err := sm.BeginRelatedSync()
		require.NoError(t, err)
		assert.Equal(t, entities.RelatedSyncCursors{CommentID: 7, PredictionNoteID: 5, PredictionResultID: 9}, resumed,
			"a rerun for the same attempt must resume from the stored cursors")

		state := relatedColumns(t, sm)
		require.NotNil(t, state.RelatedReconciledAt)
		assert.True(t, state.RelatedReconciledAt.Equal(reconciled), "resume must keep the reconcile time")
	})

	t.Run("begin without an attempt is refused", func(t *testing.T) {
		sm, cleanup := setupStateManager(t)
		defer cleanup()

		_, err := sm.BeginRelatedSync()
		require.ErrorIs(t, err, ErrRelatedSyncNotRecorded)
	})

	t.Run("start, cancel and rollback clear all five columns", func(t *testing.T) {
		clearing := map[string]func(t *testing.T, sm *StateManager){
			"cancel": func(t *testing.T, sm *StateManager) {
				t.Helper()
				require.NoError(t, sm.Cancel())
			},
			"rollback": func(t *testing.T, sm *StateManager) {
				t.Helper()
				require.NoError(t, sm.TransitionToDualWrite())
				require.NoError(t, sm.TransitionToMigrating())
				require.NoError(t, sm.TransitionToValidating())
				require.NoError(t, sm.TransitionToCutover())
				require.NoError(t, sm.Complete())
				require.NoError(t, sm.Rollback())
			},
			"start": func(t *testing.T, sm *StateManager) {
				t.Helper()
				require.NoError(t, sm.Cancel())
				// Cancel already cleared; set the columns again to prove StartMigration clears them.
				require.NoError(t, sm.db.Model(&entities.MigrationState{}).Where("id = ?", migrationStateID).
					Updates(map[string]any{
						"related_comments_cursor":       4,
						"related_predictions_note_id":   4,
						"related_predictions_result_id": 4,
						"related_sync_started_at":       time.Now(),
						"related_reconciled_at":         time.Now(),
					}).Error)
				require.NoError(t, sm.StartMigration(1))
			},
		}
		for name, reset := range clearing {
			t.Run(name, func(t *testing.T) {
				sm, cleanup := setupStateManager(t)
				defer cleanup()
				startedAttempt(t, sm)
				_, err := sm.BeginRelatedSync()
				require.NoError(t, err)
				require.NoError(t, sm.SetRelatedCommentsCursor(3))
				require.NoError(t, sm.SetRelatedPredictionsCursor(3, 3))
				require.NoError(t, sm.SetRelatedReconciledAt(time.Now()))

				reset(t, sm)

				requireRelatedColumnsCleared(t, relatedColumns(t, sm))
			})
		}
	})

	t.Run("a stale marker is not usable and begin reinitialises", func(t *testing.T) {
		sm, cleanup := setupStateManager(t)
		defer cleanup()
		startedAttempt(t, sm)
		_, err := sm.BeginRelatedSync()
		require.NoError(t, err)
		require.NoError(t, sm.SetRelatedCommentsCursor(8))

		// What an older attempt (or build) leaves behind: a marker that differs from started_at.
		require.NoError(t, sm.db.Model(&entities.MigrationState{}).Where("id = ?", migrationStateID).
			Update("related_sync_started_at", time.Now().Add(-time.Hour)).Error)

		_, ok := relatedColumns(t, sm).RelatedSyncCursors()
		assert.False(t, ok, "cursors of another attempt must not be resumed")

		require.ErrorIs(t, sm.SetRelatedCommentsCursor(9), ErrRelatedSyncNotRecorded)
		require.ErrorIs(t, sm.SetRelatedPredictionsCursor(9, 9), ErrRelatedSyncNotRecorded)
		require.ErrorIs(t, sm.SetRelatedReconciledAt(time.Now()), ErrRelatedSyncNotRecorded)
		require.NotNil(t, relatedColumns(t, sm).RelatedCommentsCursor)
		assert.Equal(t, uint(8), *relatedColumns(t, sm).RelatedCommentsCursor, "a refused write must change nothing")

		cursors, err := sm.BeginRelatedSync()
		require.NoError(t, err)
		assert.Equal(t, entities.RelatedSyncCursors{}, cursors, "a stale marker restarts from zero")
	})

	t.Run("filter dirty ids returns only stored ids", func(t *testing.T) {
		sm, cleanup := setupStateManager(t)
		defer cleanup()

		none, err := sm.FilterDirtyIDs(nil)
		require.NoError(t, err)
		assert.Empty(t, none)

		require.NoError(t, sm.AddDirtyID(3))
		require.NoError(t, sm.AddDirtyID(9))
		got, err := sm.FilterDirtyIDs([]uint{9, 4, 3, 100})
		require.NoError(t, err)
		assert.Equal(t, []uint{3, 9}, got)
	})
}

// preRelatedSyncComment mirrors entities.DetectionComment before legacy_id existed.
type preRelatedSyncComment struct {
	ID          uint      `gorm:"primaryKey"`
	DetectionID uint      `gorm:"not null;index"`
	Entry       string    `gorm:"type:text;not null"`
	CreatedAt   time.Time `gorm:"autoCreateTime;index"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime"`
}

// TableName maps the old shape onto the real table.
func (preRelatedSyncComment) TableName() string { return "detection_comments" }

// TestInitialize_AddsRelatedSyncColumnsToExistingTables upgrades a database
// whose detection_comments and migration_states tables predate the related sync
// columns: Initialize must add the columns and the unique index, keep the rows,
// and still allow several comments without a legacy id.
func TestInitialize_AddsRelatedSyncColumnsToExistingTables(t *testing.T) {
	mgr, err := NewSQLiteManager(Config{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	db := mgr.DB()
	require.NoError(t, db.AutoMigrate(&preAuxMigrationState{}, &preRelatedSyncComment{}))
	started := time.Now().Add(-time.Hour)
	require.NoError(t, db.Create(&preAuxMigrationState{ID: 1, State: entities.MigrationStatusCompleted, StartedAt: &started}).Error)
	require.NoError(t, db.Create(&[]preRelatedSyncComment{
		{DetectionID: 1, Entry: "old one"},
		{DetectionID: 1, Entry: "old two"},
	}).Error)

	// The old rows reference no detection; let the table rebuild keep them.
	require.NoError(t, db.Exec("PRAGMA foreign_keys = OFF").Error)

	require.NoError(t, mgr.Initialize())

	assert.True(t, db.Migrator().HasColumn(&entities.DetectionComment{}, "legacy_id"))
	for _, column := range []string{"related_sync_started_at", "related_comments_cursor", "related_predictions_note_id", "related_predictions_result_id", "related_reconciled_at"} {
		assert.True(t, db.Migrator().HasColumn(&entities.MigrationState{}, column), column)
	}

	var count int64
	require.NoError(t, db.Model(&entities.DetectionComment{}).Count(&count).Error)
	assert.Equal(t, int64(2), count, "existing comments must be kept")

	var nullLegacy int64
	require.NoError(t, db.Model(&entities.DetectionComment{}).Where("legacy_id IS NULL").Count(&nullLegacy).Error)
	assert.Equal(t, int64(2), nullLegacy)

	// The state row of the older build has no cursors and must read as unrecorded.
	state, err := NewStateManager(db).GetState()
	require.NoError(t, err)
	_, ok := state.RelatedSyncCursors()
	assert.False(t, ok)

	// The unique index allows any number of NULLs and rejects a repeated legacy id.
	// Inserts here reference no detection, so keep foreign keys off for them too.
	legacyID := uint(5)
	require.NoError(t, db.Create(&entities.DetectionComment{DetectionID: 1, Entry: "a", LegacyID: &legacyID}).Error)
	require.NoError(t, db.Create(&entities.DetectionComment{DetectionID: 1, Entry: "b"}).Error)
	require.NoError(t, db.Create(&entities.DetectionComment{DetectionID: 1, Entry: "c"}).Error)
	require.Error(t, db.Create(&entities.DetectionComment{DetectionID: 1, Entry: "d", LegacyID: &legacyID}).Error)
}
