package v2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// setupStateManager creates a test SQLite manager with initialized schema.
// Returns the state manager and a cleanup function.
func setupStateManager(t *testing.T) (sm *StateManager, cleanup func()) {
	t.Helper()

	tmpDir := t.TempDir()
	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)

	err = mgr.Initialize()
	require.NoError(t, err)

	stateManager := NewStateManager(mgr.DB())

	return stateManager, func() { _ = mgr.Close() }
}

func TestStateManager_GetState(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, entities.MigrationStatusIdle, state.State)
	assert.Equal(t, uint(1), state.ID)
}

func TestStateManager_StartMigration(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	err := sm.StartMigration(1000)
	require.NoError(t, err)

	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, entities.MigrationStatusInitializing, state.State)
	assert.Equal(t, int64(1000), state.TotalRecords)
	assert.Equal(t, int64(0), state.MigratedRecords)
	assert.NotNil(t, state.StartedAt)
}

func TestStateManager_StartMigration_FailsIfNotIdle(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	// Start migration
	err := sm.StartMigration(1000)
	require.NoError(t, err)

	// Try to start again - should fail
	err = sm.StartMigration(2000)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected idle")
}

func TestStateManager_TransitionToDualWrite(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	err := sm.StartMigration(1000)
	require.NoError(t, err)

	err = sm.TransitionToDualWrite()
	require.NoError(t, err)

	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, entities.MigrationStatusDualWrite, state.State)
}

func TestStateManager_TransitionToDualWrite_FailsIfNotInitializing(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	// Try to transition to dual_write from idle - should fail
	err := sm.TransitionToDualWrite()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected initializing")
}

func TestStateManager_Pause(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	// Setup: get to DUAL_WRITE state
	err := sm.StartMigration(1000)
	require.NoError(t, err)
	err = sm.TransitionToDualWrite()
	require.NoError(t, err)

	// Pause
	err = sm.Pause()
	require.NoError(t, err)

	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, entities.MigrationStatusPaused, state.State)
}

func TestStateManager_Pause_FailsIfCantPause(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	// Try to pause from idle - should fail
	err := sm.Pause()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot pause")
}

func TestStateManager_Resume(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	// Setup: get to PAUSED state
	err := sm.StartMigration(1000)
	require.NoError(t, err)
	err = sm.TransitionToDualWrite()
	require.NoError(t, err)
	err = sm.Pause()
	require.NoError(t, err)

	// Resume
	err = sm.Resume()
	require.NoError(t, err)

	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, entities.MigrationStatusDualWrite, state.State)
}

func TestStateManager_FullMigrationCycle(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	// IDLE -> INITIALIZING
	err := sm.StartMigration(1000)
	require.NoError(t, err)

	// INITIALIZING -> DUAL_WRITE
	err = sm.TransitionToDualWrite()
	require.NoError(t, err)

	// DUAL_WRITE -> MIGRATING
	err = sm.TransitionToMigrating()
	require.NoError(t, err)

	// Update progress
	err = sm.UpdateProgress(500, 500)
	require.NoError(t, err)

	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, uint(500), state.LastMigratedID)
	assert.Equal(t, int64(500), state.MigratedRecords)

	// MIGRATING -> VALIDATING
	err = sm.TransitionToValidating()
	require.NoError(t, err)

	// VALIDATING -> CUTOVER
	err = sm.TransitionToCutover()
	require.NoError(t, err)

	// CUTOVER -> COMPLETED
	err = sm.Complete()
	require.NoError(t, err)

	state, err = sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, entities.MigrationStatusCompleted, state.State)
	assert.NotNil(t, state.CompletedAt)
}

func TestStateManager_Cancel(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	// Start migration
	err := sm.StartMigration(1000)
	require.NoError(t, err)
	err = sm.TransitionToDualWrite()
	require.NoError(t, err)

	// Cancel
	err = sm.Cancel()
	require.NoError(t, err)

	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, entities.MigrationStatusIdle, state.State)
	assert.Equal(t, int64(0), state.TotalRecords)
	assert.Equal(t, int64(0), state.MigratedRecords)
}

func TestStateManager_Cancel_FailsIfCompleted(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	// Get to completed state
	require.NoError(t, sm.StartMigration(10))
	require.NoError(t, sm.TransitionToDualWrite())
	require.NoError(t, sm.TransitionToMigrating())
	require.NoError(t, sm.TransitionToValidating())
	require.NoError(t, sm.TransitionToCutover())
	require.NoError(t, sm.Complete())

	// Cancel should fail for completed migration
	err := sm.Cancel()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot cancel")
}

func TestStateManager_Rollback(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	// Get to completed state
	require.NoError(t, sm.StartMigration(10))
	require.NoError(t, sm.TransitionToDualWrite())
	require.NoError(t, sm.TransitionToMigrating())
	require.NoError(t, sm.TransitionToValidating())
	require.NoError(t, sm.TransitionToCutover())
	require.NoError(t, sm.Complete())

	// Rollback
	err := sm.Rollback()
	require.NoError(t, err)

	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, entities.MigrationStatusIdle, state.State)
}

func TestStateManager_Rollback_FailsIfNotCompleted(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	// Try to rollback from idle
	err := sm.Rollback()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected completed")
}

func TestStateManager_IncrementProgress(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	require.NoError(t, sm.StartMigration(1000))
	require.NoError(t, sm.TransitionToDualWrite())
	require.NoError(t, sm.TransitionToMigrating())

	// Increment progress multiple times
	err := sm.IncrementProgress(100, 100)
	require.NoError(t, err)

	err = sm.IncrementProgress(200, 100)
	require.NoError(t, err)

	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, uint(200), state.LastMigratedID)
	assert.Equal(t, int64(200), state.MigratedRecords)
}

func TestStateManager_SetError(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	err := sm.SetError("test error message")
	require.NoError(t, err)

	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, "test error message", state.ErrorMessage)

	// Clear error
	err = sm.ClearError()
	require.NoError(t, err)

	state, err = sm.GetState()
	require.NoError(t, err)
	assert.Empty(t, state.ErrorMessage)
}

func TestStateManager_IsInDualWriteMode(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	// Initially not in dual write mode
	isDualWrite, err := sm.IsInDualWriteMode()
	require.NoError(t, err)
	assert.False(t, isDualWrite)

	// Enter dual write mode
	require.NoError(t, sm.StartMigration(10))
	require.NoError(t, sm.TransitionToDualWrite())

	isDualWrite, err = sm.IsInDualWriteMode()
	require.NoError(t, err)
	assert.True(t, isDualWrite)

	// Still in dual write during migrating
	require.NoError(t, sm.TransitionToMigrating())
	isDualWrite, err = sm.IsInDualWriteMode()
	require.NoError(t, err)
	assert.True(t, isDualWrite)
}

func TestStateManager_ShouldReadFromV2(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	// Initially should not read from v2
	shouldRead, err := sm.ShouldReadFromV2()
	require.NoError(t, err)
	assert.False(t, shouldRead)

	// Even during migration, still read from legacy
	require.NoError(t, sm.StartMigration(10))
	require.NoError(t, sm.TransitionToDualWrite())
	require.NoError(t, sm.TransitionToMigrating())

	shouldRead, err = sm.ShouldReadFromV2()
	require.NoError(t, err)
	assert.False(t, shouldRead)

	// After cutover, read from v2
	require.NoError(t, sm.TransitionToValidating())
	require.NoError(t, sm.TransitionToCutover())

	shouldRead, err = sm.ShouldReadFromV2()
	require.NoError(t, err)
	assert.True(t, shouldRead)
}

func TestStateManager_GetProgress(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	require.NoError(t, sm.StartMigration(1000))
	require.NoError(t, sm.TransitionToDualWrite())
	require.NoError(t, sm.UpdateProgress(500, 500))

	migrated, total, lastID, err := sm.GetProgress()
	require.NoError(t, err)
	assert.Equal(t, int64(500), migrated)
	assert.Equal(t, int64(1000), total)
	assert.Equal(t, uint(500), lastID)
}
