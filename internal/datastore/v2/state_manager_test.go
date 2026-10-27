package v2

import (
	"fmt"
	"testing"
	"time"

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
	err = sm.IncrementProgress(500, 500)
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

// TestStateManager_UpdatePhaseProgress_KeepsWatermark verifies that the
// phase progress write sets only the migrated record count and leaves the
// detections cursor (last_migrated_id) alone.
func TestStateManager_UpdatePhaseProgress_KeepsWatermark(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	require.NoError(t, sm.StartMigration(1000))
	require.NoError(t, sm.TransitionToDualWrite())
	require.NoError(t, sm.TransitionToMigrating())
	require.NoError(t, sm.IncrementProgress(500, 500))
	require.NoError(t, sm.SetPhaseWithProgress(
		entities.MigrationPhasePredictions, MigrationPhaseNumberPredictions, MigrationTotalPhases, 3000))

	require.NoError(t, sm.UpdatePhaseProgress(1200))

	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, uint(500), state.LastMigratedID, "phase progress must not move the detections watermark")
	assert.Equal(t, int64(1200), state.MigratedRecords)
	assert.Equal(t, int64(3000), state.TotalRecords)
}

func TestStateManager_GetProgress(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	require.NoError(t, sm.StartMigration(1000))
	require.NoError(t, sm.TransitionToDualWrite())
	require.NoError(t, sm.IncrementProgress(500, 500))

	migrated, total, lastID, err := sm.GetProgress()
	require.NoError(t, err)
	assert.Equal(t, int64(500), migrated)
	assert.Equal(t, int64(1000), total)
	assert.Equal(t, uint(500), lastID)
}

func TestStateManager_Fail(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	// Setup: get to VALIDATING state
	require.NoError(t, sm.StartMigration(1000))
	require.NoError(t, sm.TransitionToDualWrite())
	require.NoError(t, sm.TransitionToMigrating())
	require.NoError(t, sm.TransitionToValidating())

	// Fail
	err := sm.Fail()
	require.NoError(t, err)

	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, entities.MigrationStatusFailed, state.State)
}

func TestStateManager_Fail_FailsFromWrongState(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	// Setup: get to DUAL_WRITE state
	require.NoError(t, sm.StartMigration(1000))
	require.NoError(t, sm.TransitionToDualWrite())

	// Fail should fail from DUAL_WRITE (only valid from VALIDATING)
	err := sm.Fail()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected validating")
}

func TestStateManager_RetryValidation(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	// Setup: get to FAILED state
	require.NoError(t, sm.StartMigration(1000))
	require.NoError(t, sm.TransitionToDualWrite())
	require.NoError(t, sm.TransitionToMigrating())
	require.NoError(t, sm.TransitionToValidating())
	require.NoError(t, sm.Fail())

	// RetryValidation
	err := sm.RetryValidation()
	require.NoError(t, err)

	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, entities.MigrationStatusValidating, state.State)
}

func TestStateManager_RetryValidation_FailsFromWrongState(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	// Try to retry from idle
	err := sm.RetryValidation()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected failed")
}

func TestStateManager_Cancel_FromFailed(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	// Setup: get to FAILED state
	require.NoError(t, sm.StartMigration(1000))
	require.NoError(t, sm.TransitionToDualWrite())
	require.NoError(t, sm.TransitionToMigrating())
	require.NoError(t, sm.TransitionToValidating())
	require.NoError(t, sm.Fail())

	// Cancel from FAILED
	err := sm.Cancel()
	require.NoError(t, err)

	state, err := sm.GetState()
	require.NoError(t, err)
	assert.Equal(t, entities.MigrationStatusIdle, state.State)
}

func TestStateManager_Pause_FailsFromValidating(t *testing.T) {
	sm, cleanup := setupStateManager(t)
	defer cleanup()

	// Setup: get to VALIDATING state
	require.NoError(t, sm.StartMigration(1000))
	require.NoError(t, sm.TransitionToDualWrite())
	require.NoError(t, sm.TransitionToMigrating())
	require.NoError(t, sm.TransitionToValidating())

	// Pause should fail from VALIDATING (must use Fail() instead)
	err := sm.Pause()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot pause")
}

func TestMigrationState_CanRetryValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state    entities.MigrationStatus
		expected bool
	}{
		{entities.MigrationStatusIdle, false},
		{entities.MigrationStatusDualWrite, false},
		{entities.MigrationStatusPaused, false},
		{entities.MigrationStatusMigrating, false},
		{entities.MigrationStatusValidating, false},
		{entities.MigrationStatusFailed, true},
		{entities.MigrationStatusCompleted, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			t.Parallel()
			state := &entities.MigrationState{State: tt.state}
			assert.Equal(t, tt.expected, state.CanRetryValidation())
		})
	}
}

func TestMigrationState_FailedIsNotActive(t *testing.T) {
	t.Parallel()

	state := &entities.MigrationState{State: entities.MigrationStatusFailed}
	assert.False(t, state.IsActive())
}

func TestMigrationState_FailedCanCancel(t *testing.T) {
	t.Parallel()

	state := &entities.MigrationState{State: entities.MigrationStatusFailed}
	assert.True(t, state.CanCancel())
}

func TestMigrationState_FailedCannotResume(t *testing.T) {
	t.Parallel()

	state := &entities.MigrationState{State: entities.MigrationStatusFailed}
	assert.False(t, state.CanResume())
}

func TestReportStateTransitionError_NilSafe(t *testing.T) {
	t.Parallel()
	assert.NotPanics(t, func() {
		reportStateTransitionError("IDLE", "DUAL_WRITE", fmt.Errorf("no rows affected"))
	})
}

// auxOutcomeStep is one step of a TestStateManager_AuxiliaryDataOutcome_Transitions
// sequence. After run, the outcome read back must equal want, and the state
// wantState when set. A step with wantErr must fail without changing the state.
type auxOutcomeStep struct {
	name      string
	run       func(sm *StateManager) error
	wantErr   bool
	want      entities.AuxiliaryDataStatus
	wantState entities.MigrationStatus
	// wantCleared asserts the raw outcome columns are empty.
	wantCleared bool
}

// toMigrating drives a fresh state manager from idle to migrating.
func toMigrating(sm *StateManager) error {
	if err := sm.StartMigration(10); err != nil {
		return err
	}
	if err := sm.TransitionToDualWrite(); err != nil {
		return err
	}
	return sm.TransitionToMigrating()
}

// toCompleted drives a validating state manager through cutover to completed.
func toCompleted(sm *StateManager) error {
	if err := sm.TransitionToCutover(); err != nil {
		return err
	}
	return sm.Complete()
}

func TestStateManager_AuxiliaryDataOutcome_Transitions(t *testing.T) {
	t.Parallel()

	const (
		failMsg = "threshold: 0 of 2 migrated, 2 failed"
		unknown = entities.AuxiliaryDataStatusUnknown
		failed  = entities.AuxiliaryDataStatusFailed
		okay    = entities.AuxiliaryDataStatusSucceeded
	)
	recordFailed := func(sm *StateManager) error {
		return sm.TransitionToValidatingWithAuxiliaryOutcome(failed, failMsg)
	}
	recordSucceeded := func(sm *StateManager) error {
		return sm.TransitionToValidatingWithAuxiliaryOutcome(okay, "")
	}

	tests := []struct {
		name  string
		steps []auxOutcomeStep
	}{
		{
			name: "failed outcome survives fail, retry, cutover and complete",
			steps: []auxOutcomeStep{
				{name: "migrating", run: toMigrating, want: unknown},
				{name: "validating", run: recordFailed, want: failed},
				{name: "fail", run: (*StateManager).Fail, want: failed},
				{name: "retry", run: (*StateManager).RetryValidation, want: failed},
				{name: "completed", run: toCompleted, want: failed, wantState: entities.MigrationStatusCompleted},
			},
		},
		{
			name: "rollback and a new migration read as unknown",
			steps: []auxOutcomeStep{
				{name: "migrating", run: toMigrating},
				{name: "validating", run: recordSucceeded, want: okay},
				{name: "completed", run: toCompleted, want: okay},
				{name: "rollback", run: (*StateManager).Rollback, want: unknown, wantCleared: true},
				{name: "new attempt", run: func(sm *StateManager) error { return sm.StartMigration(5) }, want: unknown, wantCleared: true},
			},
		},
		{
			name: "cancel clears the recorded outcome",
			steps: []auxOutcomeStep{
				{name: "migrating", run: toMigrating},
				{name: "validating", run: recordFailed, want: failed},
				{name: "cancel", run: (*StateManager).Cancel, want: unknown, wantCleared: true},
			},
		},
		{
			name: "recording from paused changes nothing",
			steps: []auxOutcomeStep{
				{name: "migrating", run: toMigrating},
				{name: "pause", run: (*StateManager).Pause},
				{name: "record refused", run: recordSucceeded, wantErr: true, want: unknown, wantState: entities.MigrationStatusPaused, wantCleared: true},
			},
		},
		{
			name: "recording after cancel changes nothing",
			steps: []auxOutcomeStep{
				{name: "migrating", run: toMigrating},
				{name: "cancel", run: (*StateManager).Cancel},
				{name: "record refused", run: recordSucceeded, wantErr: true, want: unknown, wantState: entities.MigrationStatusIdle, wantCleared: true},
			},
		},
		{
			name: "an older binary's new attempt reads as unknown",
			steps: []auxOutcomeStep{
				{name: "migrating", run: toMigrating},
				{name: "validating", run: recordSucceeded, want: okay},
				{name: "completed", run: toCompleted, want: okay},
				{name: "older binary rollback and restart", run: func(sm *StateManager) error {
					// An older binary resets only the columns it knows and sets a
					// new started_at, leaving the auxiliary columns untouched.
					return sm.db.Exec(
						"UPDATE migration_states SET state = ?, started_at = ?, completed_at = NULL WHERE id = 1",
						entities.MigrationStatusCompleted, time.Now().Add(time.Hour)).Error
				}, want: unknown},
			},
		},
		{
			name: "leftover columns on an idle row read as unknown",
			steps: []auxOutcomeStep{
				{name: "migrating", run: toMigrating},
				{name: "validating", run: recordSucceeded, want: okay},
				{name: "reset to idle by a tool", run: func(sm *StateManager) error {
					return sm.db.Exec("UPDATE migration_states SET state = ? WHERE id = 1", entities.MigrationStatusIdle).Error
				}, want: unknown},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sm, cleanup := setupStateManager(t)
			defer cleanup()
			for _, step := range tt.steps {
				err := step.run(sm)
				if step.wantErr {
					require.Error(t, err, step.name)
				} else {
					require.NoError(t, err, step.name)
				}
				state, getErr := sm.GetState()
				require.NoError(t, getErr, step.name)
				if step.wantState != "" {
					assert.Equal(t, step.wantState, state.State, step.name)
				}
				assert.Equal(t, step.want, state.AuxiliaryDataOutcome(), step.name)
				if step.want == failed {
					assert.Equal(t, failMsg, state.AuxiliaryDataError, step.name)
				}
				if step.wantCleared {
					assert.Empty(t, state.AuxiliaryDataStatus, step.name)
					assert.Empty(t, state.AuxiliaryDataError, step.name)
					assert.Nil(t, state.AuxiliaryDataStartedAt, step.name)
				}
			}
		})
	}
}

// TestMigrationState_AuxiliaryColumnsOnExistingRow checks that Initialize adds
// the auxiliary outcome columns to a migration_states table that already holds a
// completed row, and that the row reads back as an unknown outcome.
func TestMigrationState_AuxiliaryColumnsOnExistingRow(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	// Build the table in its shape before the auxiliary columns existed.
	db := mgr.DB()
	require.NoError(t, db.AutoMigrate(&preAuxMigrationState{}))
	started := time.Now().Add(-time.Hour)
	require.NoError(t, db.Create(&preAuxMigrationState{
		ID: 1, State: entities.MigrationStatusCompleted, StartedAt: &started,
	}).Error)

	require.NoError(t, mgr.Initialize())

	state, err := NewStateManager(mgr.DB()).GetState()
	require.NoError(t, err)
	assert.Equal(t, entities.MigrationStatusCompleted, state.State)
	assert.Equal(t, entities.AuxiliaryDataStatusUnknown, state.AuxiliaryDataOutcome())

	var rawStatus string
	require.NoError(t, mgr.DB().Raw("SELECT auxiliary_data_status FROM migration_states WHERE id = 1").Scan(&rawStatus).Error)
	assert.Empty(t, rawStatus)
}

// preAuxMigrationState mirrors entities.MigrationState before the auxiliary
// outcome columns were added.
type preAuxMigrationState struct {
	ID               uint                     `gorm:"primaryKey"`
	State            entities.MigrationStatus `gorm:"type:varchar(20);not null;default:'idle'"`
	CurrentPhase     entities.MigrationPhase  `gorm:"type:varchar(20);not null;default:''"`
	PhaseNumber      int                      `gorm:"default:0"`
	TotalPhases      int                      `gorm:"default:0"`
	StartedAt        *time.Time
	PhaseStartedAt   *time.Time
	CompletedAt      *time.Time
	LastMigratedID   uint      `gorm:"default:0"`
	TotalRecords     int64     `gorm:"default:0"`
	MigratedRecords  int64     `gorm:"default:0"`
	ErrorMessage     string    `gorm:"type:text"`
	RelatedDataError string    `gorm:"column:related_data_error;type:text"`
	UpdatedAt        time.Time `gorm:"autoUpdateTime"`
}

// TableName maps the pre-auxiliary shape onto the real table.
func (preAuxMigrationState) TableName() string { return "migration_states" }
