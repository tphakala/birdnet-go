package v2

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/diagnostics"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/privacy"
	"github.com/tphakala/birdnet-go/internal/telemetry"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// migrationStateID is the ID of the singleton migration state record.
// The migration state table always contains exactly one row with this ID.
const migrationStateID = 1

// Migration phase number constants.
const (
	MigrationPhaseNumberNone        = 0 // No active phase (idle/completed/cancelled)
	MigrationPhaseNumberDetections  = 1 // Phase 1: Migrating detection records
	MigrationPhaseNumberPredictions = 2 // Phase 2: Migrating predictions and related data
	MigrationTotalPhases            = 2 // Total number of migration phases
)

// ErrRelatedSyncNotRecorded is returned when the related data sync cursors are
// not recorded for the current migration attempt: the related data was copied
// by an older version, or the attempt changed while a sync pass was running.
var ErrRelatedSyncNotRecorded = errors.NewStd("related data sync cursors are not recorded for this migration attempt")

// StateManager manages the migration state machine and tracks progress.
// It provides thread-safe access to the migration state.
// State transitions use atomic SQL updates to ensure multi-process safety.
type StateManager struct {
	db *gorm.DB
	mu sync.RWMutex
}

// NewStateManager creates a new migration state manager.
func NewStateManager(db *gorm.DB) *StateManager {
	return &StateManager{
		db: db,
	}
}

// GetState returns the current migration state.
func (m *StateManager) GetState() (*entities.MigrationState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var state entities.MigrationState
	if err := m.db.First(&state).Error; err != nil {
		return nil, fmt.Errorf("failed to get migration state: %w", err)
	}
	return &state, nil
}

// StartMigration transitions from IDLE to INITIALIZING.
// It clears the error fields, the recorded auxiliary data outcome and the related
// data sync cursors, and the dirty-ID set left by an earlier run, in the same
// transaction. A refused transition leaves the dirty-ID set untouched.
// Uses atomic update to ensure multi-process safety.
func (m *StateManager) StartMigration(totalRecords int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	updates := map[string]any{
		"state":                         entities.MigrationStatusInitializing,
		"current_phase":                 entities.MigrationPhaseDetections,
		"phase_number":                  MigrationPhaseNumberDetections,
		"total_phases":                  MigrationTotalPhases,
		"started_at":                    &now,
		"phase_started_at":              &now, // Track phase start for rate calculation
		"total_records":                 totalRecords,
		"migrated_records":              0,
		"last_migrated_id":              0,
		"error_message":                 "",
		"related_data_error":            "",
		"completed_at":                  nil,
		"auxiliary_data_status":         entities.AuxiliaryDataStatusUnknown,
		"auxiliary_data_error":          "",
		"auxiliary_data_started_at":     nil,
		"related_sync_started_at":       nil,
		"related_comments_cursor":       nil,
		"related_predictions_note_id":   nil,
		"related_predictions_result_id": nil,
		"related_reconciled_at":         nil,
	}

	matched, err := m.updateGuardedClearingDirtyIDs(entities.MigrationStatusIdle, updates)
	if err != nil {
		reportStateTransitionError(string(entities.MigrationStatusIdle), string(entities.MigrationStatusInitializing), err)
		return fmt.Errorf("failed to start migration: %w", err)
	}

	if matched == 0 {
		var current entities.MigrationState
		if err := m.db.First(&current).Error; err != nil {
			return fmt.Errorf("failed to get current state: %w", err)
		}
		startErr := fmt.Errorf("cannot start migration: current state is %s, expected idle", current.State)
		reportStateTransitionError(string(entities.MigrationStatusIdle), string(entities.MigrationStatusInitializing), startErr)
		return startErr
	}

	events.Emit(context.Background(), "migration", "state_transition", "Migration state changed", map[string]any{
		"from_state":    "idle",
		"to_state":      "initializing",
		"total_records": totalRecords,
	})

	return nil
}

// TransitionToDualWrite transitions from INITIALIZING to DUAL_WRITE.
func (m *StateManager) TransitionToDualWrite() error {
	if err := m.transitionState(entities.MigrationStatusInitializing, entities.MigrationStatusDualWrite); err != nil {
		return err
	}

	events.Emit(context.Background(), "migration", "state_transition", "Migration state changed", map[string]any{
		"from_state": "initializing",
		"to_state":   "dual_write",
	})

	return nil
}

// Pause transitions from DUAL_WRITE or MIGRATING to PAUSED.
// Uses atomic update to ensure multi-process safety.
// Note: VALIDATING state uses Fail() instead of Pause() for validation failures.
func (m *StateManager) Pause() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pausableStates := []entities.MigrationStatus{
		entities.MigrationStatusDualWrite,
		entities.MigrationStatusMigrating,
	}

	// Read the current state before the update so we can emit the actual from_state.
	var currentState entities.MigrationState
	if err := m.db.First(&currentState).Error; err != nil {
		currentState.State = "unknown"
	}

	result := m.db.Model(&entities.MigrationState{}).
		Where("id = ? AND state IN ?", migrationStateID, pausableStates).
		Update("state", entities.MigrationStatusPaused)

	if result.Error != nil {
		return fmt.Errorf("failed to pause migration: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		var current entities.MigrationState
		if err := m.db.First(&current).Error; err != nil {
			return fmt.Errorf("failed to get current state: %w", err)
		}
		return fmt.Errorf("cannot pause migration: current state is %s", current.State)
	}

	events.Emit(context.Background(), "migration", "state_transition", "Migration state changed", map[string]any{
		"from_state": string(currentState.State),
		"to_state":   "paused",
	})

	return nil
}

// Resume transitions from PAUSED to DUAL_WRITE.
//
// Resume always transitions to DUAL_WRITE regardless of which state was active when paused
// (DUAL_WRITE, MIGRATING, or VALIDATING). This apparent state "regression" (e.g., from
// VALIDATING back to DUAL_WRITE) is safe because the migration worker uses LastMigratedID
// to track progress. When processing resumes, the worker queries for records with IDs greater
// than LastMigratedID, so no records are re-processed or duplicated.
//
// For example, if the migration was paused during VALIDATING at ID 50000, resuming to
// DUAL_WRITE simply restarts the state machine loop. The worker will see LastMigratedID=50000
// and continue from ID 50001, eventually transitioning back through MIGRATING → VALIDATING.
func (m *StateManager) Resume() error {
	if err := m.transitionState(entities.MigrationStatusPaused, entities.MigrationStatusDualWrite); err != nil {
		return err
	}

	events.Emit(context.Background(), "migration", "state_transition", "Migration state changed", map[string]any{
		"from_state": "paused",
		"to_state":   "dual_write",
	})

	return nil
}

// Fail transitions from VALIDATING to FAILED.
// This is called when validation fails with a non-recoverable error.
// Unlike Pause, this indicates a validation failure that requires explicit
// retry rather than a simple resume.
func (m *StateManager) Fail() error {
	if err := m.transitionState(entities.MigrationStatusValidating, entities.MigrationStatusFailed); err != nil {
		return err
	}

	events.Emit(context.Background(), "migration", "state_transition", "Migration state changed", map[string]any{
		"from_state": "validating",
		"to_state":   "failed",
	})

	return nil
}

// RetryValidation transitions from FAILED back to VALIDATING.
// This allows re-running validation after a failure, without restarting the
// entire migration from DUAL_WRITE (which Resume would do from PAUSED).
func (m *StateManager) RetryValidation() error {
	if err := m.transitionState(entities.MigrationStatusFailed, entities.MigrationStatusValidating); err != nil {
		return err
	}

	events.Emit(context.Background(), "migration", "state_transition", "Migration state changed", map[string]any{
		"from_state": "failed",
		"to_state":   "validating",
	})

	return nil
}

// TransitionToMigrating transitions from DUAL_WRITE to MIGRATING.
// This is called when the migration worker starts processing records.
func (m *StateManager) TransitionToMigrating() error {
	if err := m.transitionState(entities.MigrationStatusDualWrite, entities.MigrationStatusMigrating); err != nil {
		return err
	}

	events.Emit(context.Background(), "migration", "state_transition", "Migration state changed", map[string]any{
		"from_state": "dual_write",
		"to_state":   "migrating",
	})

	return nil
}

// TransitionToValidating transitions from MIGRATING to VALIDATING.
// This is called when all records have been migrated.
func (m *StateManager) TransitionToValidating() error {
	if err := m.transitionState(entities.MigrationStatusMigrating, entities.MigrationStatusValidating); err != nil {
		return err
	}

	events.Emit(context.Background(), "migration", "state_transition", "Migration state changed", map[string]any{
		"from_state": "migrating",
		"to_state":   "validating",
	})

	return nil
}

// TransitionToValidatingWithAuxiliaryOutcome transitions from MIGRATING to
// VALIDATING and records the auxiliary data outcome in the same UPDATE, so the
// outcome exists if and only if the transition happened after the auxiliary
// step. The attempt marker (started_at) is copied in SQL. The UPDATE affects no
// row, and an error is returned, when the state is no longer MIGRATING.
func (m *StateManager) TransitionToValidatingWithAuxiliaryOutcome(status entities.AuxiliaryDataStatus, errMsg string) error {
	updates := map[string]any{
		"auxiliary_data_status":     status,
		"auxiliary_data_error":      errMsg,
		"auxiliary_data_started_at": gorm.Expr("started_at"),
	}
	if err := m.transitionStateWith(entities.MigrationStatusMigrating, entities.MigrationStatusValidating, updates); err != nil {
		return err
	}

	events.Emit(context.Background(), "migration", "state_transition", "Migration state changed", map[string]any{
		"from_state": "migrating",
		"to_state":   "validating",
	})

	return nil
}

// TransitionToCutover transitions from VALIDATING to CUTOVER.
// This is called when validation passes.
func (m *StateManager) TransitionToCutover() error {
	if err := m.transitionState(entities.MigrationStatusValidating, entities.MigrationStatusCutover); err != nil {
		return err
	}

	events.Emit(context.Background(), "migration", "state_transition", "Migration state changed", map[string]any{
		"from_state": "validating",
		"to_state":   "cutover",
	})

	return nil
}

// Complete transitions from CUTOVER to COMPLETED.
// Uses atomic update to ensure multi-process safety.
func (m *StateManager) Complete() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	updates := map[string]any{
		"state":        entities.MigrationStatusCompleted,
		"completed_at": &now,
	}

	result := m.db.Model(&entities.MigrationState{}).
		Where("id = ? AND state = ?", migrationStateID, entities.MigrationStatusCutover).
		Updates(updates)

	if result.Error != nil {
		reportStateTransitionError(string(entities.MigrationStatusCutover), string(entities.MigrationStatusCompleted), result.Error)
		return fmt.Errorf("failed to complete migration: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		var current entities.MigrationState
		if err := m.db.First(&current).Error; err != nil {
			return fmt.Errorf("failed to get current state: %w", err)
		}
		completeErr := fmt.Errorf("cannot complete migration: current state is %s, expected cutover", current.State)
		reportStateTransitionError(string(entities.MigrationStatusCutover), string(entities.MigrationStatusCompleted), completeErr)
		return completeErr
	}

	events.Emit(context.Background(), "migration", "state_transition", "Migration state changed", map[string]any{
		"from_state": "cutover",
		"to_state":   "completed",
	})

	return nil
}

// Cancel transitions any active state back to IDLE.
// It clears the recorded auxiliary data outcome and the related data sync cursors.
// Uses atomic update to ensure multi-process safety.
func (m *StateManager) Cancel() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cancellableStates := []entities.MigrationStatus{
		entities.MigrationStatusInitializing,
		entities.MigrationStatusDualWrite,
		entities.MigrationStatusPaused,
		entities.MigrationStatusFailed,
		entities.MigrationStatusMigrating,
		entities.MigrationStatusValidating,
		entities.MigrationStatusCutover,
	}

	updates := map[string]any{
		"state":                         entities.MigrationStatusIdle,
		"current_phase":                 entities.MigrationPhaseNone,
		"phase_number":                  MigrationPhaseNumberNone,
		"total_phases":                  MigrationPhaseNumberNone,
		"started_at":                    nil,
		"completed_at":                  nil,
		"total_records":                 0,
		"migrated_records":              0,
		"last_migrated_id":              0,
		"error_message":                 "",
		"related_data_error":            "",
		"auxiliary_data_status":         entities.AuxiliaryDataStatusUnknown,
		"auxiliary_data_error":          "",
		"auxiliary_data_started_at":     nil,
		"related_sync_started_at":       nil,
		"related_comments_cursor":       nil,
		"related_predictions_note_id":   nil,
		"related_predictions_result_id": nil,
		"related_reconciled_at":         nil,
	}

	result := m.db.Model(&entities.MigrationState{}).
		Where("id = ? AND state IN ?", migrationStateID, cancellableStates).
		Updates(updates)

	if result.Error != nil {
		reportStateTransitionError("cancellable", string(entities.MigrationStatusIdle), result.Error)
		return fmt.Errorf("failed to cancel migration: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		var current entities.MigrationState
		if err := m.db.First(&current).Error; err != nil {
			return fmt.Errorf("failed to get current state: %w", err)
		}
		cancelErr := fmt.Errorf("cannot cancel migration: current state is %s", current.State)
		reportStateTransitionError("cancellable", string(entities.MigrationStatusIdle), cancelErr)
		return cancelErr
	}

	return nil
}

// Rollback transitions from COMPLETED back to IDLE for rollback scenarios.
// It clears the recorded auxiliary data outcome, the related data sync cursors,
// and the dirty-ID set (the tail sync markers of the abandoned v2 data), in the
// same transaction. A refused transition leaves the dirty-ID set untouched.
// Uses atomic update to ensure multi-process safety.
func (m *StateManager) Rollback() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	updates := map[string]any{
		"state":                         entities.MigrationStatusIdle,
		"started_at":                    nil,
		"completed_at":                  nil,
		"total_records":                 0,
		"migrated_records":              0,
		"last_migrated_id":              0,
		"error_message":                 "",
		"related_data_error":            "",
		"auxiliary_data_status":         entities.AuxiliaryDataStatusUnknown,
		"auxiliary_data_error":          "",
		"auxiliary_data_started_at":     nil,
		"related_sync_started_at":       nil,
		"related_comments_cursor":       nil,
		"related_predictions_note_id":   nil,
		"related_predictions_result_id": nil,
		"related_reconciled_at":         nil,
	}

	matched, err := m.updateGuardedClearingDirtyIDs(entities.MigrationStatusCompleted, updates)
	if err != nil {
		reportStateTransitionError(string(entities.MigrationStatusCompleted), string(entities.MigrationStatusIdle), err)
		return fmt.Errorf("failed to rollback migration: %w", err)
	}

	if matched == 0 {
		var current entities.MigrationState
		if err := m.db.First(&current).Error; err != nil {
			return fmt.Errorf("failed to get current state: %w", err)
		}
		rollbackErr := fmt.Errorf("cannot rollback: current state is %s, expected completed", current.State)
		reportStateTransitionError(string(entities.MigrationStatusCompleted), string(entities.MigrationStatusIdle), rollbackErr)
		return rollbackErr
	}

	return nil
}

// BeginRelatedSync binds the related data sync cursors to the current migration
// attempt and returns them. The first call for an attempt copies started_at to
// related_sync_started_at in SQL and zeroes the cursors; later calls for the
// same attempt change nothing and return the stored cursors, so a rerun resumes.
// It returns ErrRelatedSyncNotRecorded when no attempt is recorded.
func (m *StateManager) BeginRelatedSync() (entities.RelatedSyncCursors, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := m.db.Model(&entities.MigrationState{}).
		Where("id = ? AND started_at IS NOT NULL AND (related_sync_started_at IS NULL OR related_sync_started_at <> started_at)", migrationStateID).
		Updates(map[string]any{
			"related_sync_started_at":       gorm.Expr("started_at"),
			"related_comments_cursor":       0,
			"related_predictions_note_id":   0,
			"related_predictions_result_id": 0,
			"related_reconciled_at":         nil,
		})
	if result.Error != nil {
		return entities.RelatedSyncCursors{}, fmt.Errorf("failed to begin related data sync: %w", result.Error)
	}

	var state entities.MigrationState
	if err := m.db.First(&state).Error; err != nil {
		return entities.RelatedSyncCursors{}, fmt.Errorf("failed to read related data sync cursors: %w", err)
	}
	cursors, ok := state.RelatedSyncCursors()
	if !ok {
		return entities.RelatedSyncCursors{}, ErrRelatedSyncNotRecorded
	}
	return cursors, nil
}

// updateRelatedSync applies updates to the state row only while the related
// data sync cursors belong to the current attempt. It returns
// ErrRelatedSyncNotRecorded when no row matches.
func (m *StateManager) updateRelatedSync(updates map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := m.db.Model(&entities.MigrationState{}).
		Where("id = ? AND related_sync_started_at = started_at", migrationStateID).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("failed to update related data sync state: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrRelatedSyncNotRecorded
	}
	return nil
}

// SetRelatedCommentsCursor records the last legacy comment id handled by the
// related data sync. It returns ErrRelatedSyncNotRecorded when the cursors do
// not belong to the current attempt.
func (m *StateManager) SetRelatedCommentsCursor(id uint) error {
	return m.updateRelatedSync(map[string]any{"related_comments_cursor": id})
}

// SetRelatedPredictionsCursor records the last legacy results row (note id,
// result id) handled by the related data sync. It returns
// ErrRelatedSyncNotRecorded when the cursors do not belong to the current attempt.
func (m *StateManager) SetRelatedPredictionsCursor(noteID, resultID uint) error {
	return m.updateRelatedSync(map[string]any{
		"related_predictions_note_id":   noteID,
		"related_predictions_result_id": resultID,
	})
}

// SetRelatedReconciledAt records the start time of a completed review and lock
// reconcile. It returns ErrRelatedSyncNotRecorded when the cursors do not belong
// to the current attempt.
func (m *StateManager) SetRelatedReconciledAt(at time.Time) error {
	return m.updateRelatedSync(map[string]any{"related_reconciled_at": at})
}

// UpdatePhaseProgress sets the progress numerator of the current migration
// phase. It never touches last_migrated_id, because that column is the
// detections cursor used by resume, tail sync and the startup reconciliation
// scan. Only IncrementProgress, called by the detections pass and tail sync,
// advances it, and StartMigration, Cancel and Rollback reset it. Code that
// reports progress for any other phase uses this method.
func (m *StateManager) UpdatePhaseProgress(migratedRecords int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.Model(&entities.MigrationState{}).
		Where("id = ?", migrationStateID).
		Update("migrated_records", migratedRecords).Error
}

// IncrementProgress increments the migrated records count by delta and sets
// last_migrated_id, the detections cursor. Only the detections pass and tail
// sync call it; progress for other phases goes through UpdatePhaseProgress.
func (m *StateManager) IncrementProgress(lastMigratedID uint, delta int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.Model(&entities.MigrationState{}).
		Where("id = ?", migrationStateID).
		Updates(map[string]any{
			"last_migrated_id": lastMigratedID,
			"migrated_records": gorm.Expr("migrated_records + ?", delta),
		}).Error
}

// SetError records an error message in the migration state.
func (m *StateManager) SetError(errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.Model(&entities.MigrationState{}).Where("id = ?", migrationStateID).Update("error_message", errMsg).Error
}

// ClearError clears any error message.
func (m *StateManager) ClearError() error {
	return m.SetError("")
}

// SetRelatedDataError records an error message from related data migration.
// This tracks errors from migrating reviews, comments, locks, and predictions.
func (m *StateManager) SetRelatedDataError(errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.Model(&entities.MigrationState{}).Where("id = ?", migrationStateID).Update("related_data_error", errMsg).Error
}

// SetCurrentPhase updates the current migration phase for UI display.
// This helps the UI show which phase of migration is active (detections, predictions, etc).
func (m *StateManager) SetCurrentPhase(phase entities.MigrationPhase) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.db.Model(&entities.MigrationState{}).Where("id = ?", migrationStateID).Update("current_phase", phase).Error
}

// SetPhaseWithProgress sets the current phase and resets progress counters for that phase.
// Used when transitioning to a new phase (like predictions) that has its own progress tracking.
// phaseNumber is 1-based (e.g., phase 1 of 2, phase 2 of 2).
func (m *StateManager) SetPhaseWithProgress(phase entities.MigrationPhase, phaseNumber, totalPhases int, totalRecords int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	updates := map[string]any{
		"current_phase":    phase,
		"phase_number":     phaseNumber,
		"total_phases":     totalPhases,
		"total_records":    totalRecords,
		"migrated_records": int64(0), // Explicit int64 to ensure GORM doesn't skip zero value
		"phase_started_at": &now,     // Track phase start for rate calculation
	}

	result := m.db.Model(&entities.MigrationState{}).Where("id = ?", migrationStateID).Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("failed to set phase with progress: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("no rows affected when setting phase %s", phase)
	}
	return nil
}

// reportStateTransitionError reports a migration state transition failure to Sentry.
func reportStateTransitionError(from, to string, err error) {
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", "migration-state")
		scope.SetTag("transition", from+"->"+to)
		scope.SetFingerprint([]string{"migration-state", "transition-failed", from, to})

		scrubbedErr := privacy.ScrubMessage(err.Error())
		scope.SetContext("state_transition", map[string]any{
			"from":  from,
			"to":    to,
			"error": scrubbedErr,
		})

		telemetry.CaptureMessage(
			fmt.Sprintf("Migration state transition failed: %s -> %s", from, to),
			sentry.LevelError,
			"migration-state",
		)
	})
}

// transitionState is a helper that validates and performs a state transition.
// It sets nothing but the state; transitionStateWith adds columns to the UPDATE.
// Uses atomic SQL update with WHERE clause for multi-process safety.
func (m *StateManager) transitionState(from, to entities.MigrationStatus) error {
	return m.transitionStateWith(from, to, nil)
}

// transitionStateWith performs the guarded transition and sets extra columns
// in the same UPDATE.
func (m *StateManager) transitionStateWith(from, to entities.MigrationStatus, extra map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	updates := map[string]any{"state": to}
	maps.Copy(updates, extra)

	result := m.db.Model(&entities.MigrationState{}).
		Where("id = ? AND state = ?", migrationStateID, from).
		Updates(updates)

	if result.Error != nil {
		reportStateTransitionError(string(from), string(to), result.Error)
		return fmt.Errorf("failed to transition state: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		var current entities.MigrationState
		if err := m.db.First(&current).Error; err != nil {
			return fmt.Errorf("failed to get current state: %w", err)
		}
		transitionErr := fmt.Errorf("cannot transition to %s: current state is %s, expected %s", to, current.State, from)
		reportStateTransitionError(string(from), string(to), transitionErr)
		return transitionErr
	}

	// Journal the transition (best-effort, DB-independent trail).
	diagnostics.RecordMigration(diagnostics.Default(),
		migrationPhaseForTransition(to), string(from), string(to), 0)

	return nil
}

// migrationPhaseForTransition maps a target migration state to the journal
// phase label: started / completed / failed / transition.
func migrationPhaseForTransition(to entities.MigrationStatus) string {
	switch to {
	case entities.MigrationStatusDualWrite:
		return "started"
	case entities.MigrationStatusCompleted:
		return "completed"
	case entities.MigrationStatusFailed:
		return "failed"
	default:
		return "transition"
	}
}

// IsInDualWriteMode returns true if the migration state is DUAL_WRITE, MIGRATING,
// VALIDATING, or CUTOVER. It only reports the persisted state: live saves go to the
// legacy store and the migration worker copies them to V2.
func (m *StateManager) IsInDualWriteMode() (bool, error) {
	state, err := m.GetState()
	if err != nil {
		return false, err
	}

	switch state.State {
	case entities.MigrationStatusDualWrite,
		entities.MigrationStatusMigrating,
		entities.MigrationStatusValidating,
		entities.MigrationStatusCutover:
		return true, nil
	default:
		return false, nil
	}
}

// ShouldReadFromV2 returns true if reads should come from v2 database.
// This is only true during CUTOVER and COMPLETED states.
func (m *StateManager) ShouldReadFromV2() (bool, error) {
	state, err := m.GetState()
	if err != nil {
		return false, err
	}

	switch state.State {
	case entities.MigrationStatusCutover, entities.MigrationStatusCompleted:
		return true, nil
	default:
		return false, nil
	}
}

// GetProgress returns the current migration progress.
func (m *StateManager) GetProgress() (migratedRecords, totalRecords int64, lastMigratedID uint, err error) {
	state, err := m.GetState()
	if err != nil {
		return 0, 0, 0, err
	}
	return state.MigratedRecords, state.TotalRecords, state.LastMigratedID, nil
}

// ============================================================================
// Dirty ID Tracking
// ============================================================================
// These methods track detection IDs the migration worker failed to write to V2.
// Unlike in-memory tracking, these persist across restarts.
// A marker belongs to one migration run: StartMigration and Rollback clear the
// set in their own transaction, and the cancel handler clears it after Cancel.
// StateManager.Cancel itself leaves it alone.

// AddDirtyID records a detection ID that failed to write to V2.
// The migration worker calls it when copying a record to V2 fails.
func (m *StateManager) AddDirtyID(detectionID uint) error {
	dirty := entities.MigrationDirtyID{DetectionID: detectionID}
	// Use ON CONFLICT IGNORE semantics - if already dirty, that's fine
	return m.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&dirty).Error
}

// RemoveDirtyID removes a detection ID from the dirty list after successful re-sync.
func (m *StateManager) RemoveDirtyID(detectionID uint) error {
	return m.db.Delete(&entities.MigrationDirtyID{}, detectionID).Error
}

// GetDirtyIDs returns detection IDs that need re-sync with pagination.
// Pass limit=0 to get all IDs (use with caution - may cause OOM with large datasets).
// The offset parameter allows cursor-based pagination for processing large numbers of dirty IDs.
func (m *StateManager) GetDirtyIDs(limit, offset int) ([]uint, error) {
	var dirtyRecords []entities.MigrationDirtyID
	query := m.db.Order("detection_id ASC")
	if limit > 0 {
		query = query.Limit(limit).Offset(offset)
	}
	if err := query.Find(&dirtyRecords).Error; err != nil {
		return nil, fmt.Errorf("failed to get dirty IDs: %w", err)
	}

	ids := make([]uint, len(dirtyRecords))
	for i, r := range dirtyRecords {
		ids[i] = r.DetectionID
	}
	return ids, nil
}

// GetDirtyIDsBatch returns a batch of dirty IDs for processing.
// This is the recommended method for reconciliation to avoid memory issues.
// Returns IDs ordered by detection_id for consistent iteration.
func (m *StateManager) GetDirtyIDsBatch(batchSize int) ([]uint, error) {
	return m.GetDirtyIDs(batchSize, 0)
}

// FilterDirtyIDs returns the subset of ids that are in the dirty list.
// Empty input returns nil without a query.
func (m *StateManager) FilterDirtyIDs(ids []uint) ([]uint, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var found []uint
	err := m.db.Model(&entities.MigrationDirtyID{}).
		Where("detection_id IN ?", ids).
		Order("detection_id ASC").
		Pluck("detection_id", &found).Error
	if err != nil {
		return nil, fmt.Errorf("failed to filter dirty IDs: %w", err)
	}
	return found, nil
}

// GetDirtyIDCount returns the count of dirty IDs awaiting re-sync.
func (m *StateManager) GetDirtyIDCount() (int64, error) {
	var count int64
	if err := m.db.Model(&entities.MigrationDirtyID{}).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("failed to count dirty IDs: %w", err)
	}
	return count, nil
}

// ClearDirtyIDs removes all dirty IDs. CancelMigration calls it; StartMigration
// and Rollback clear the set in their own transaction.
func (m *StateManager) ClearDirtyIDs() error {
	return deleteAllDirtyIDs(m.db)
}

// deleteAllDirtyIDs removes every dirty ID through db, which may be a
// transaction handle.
func deleteAllDirtyIDs(db *gorm.DB) error {
	return db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&entities.MigrationDirtyID{}).Error
}

// updateGuardedClearingDirtyIDs applies updates to the migration state row when
// it is in the from state, and removes every dirty ID in the same transaction.
// It returns the number of matched state rows; with 0 nothing was changed or
// deleted. The caller holds m.mu. Everything inside the transaction must use
// tx, not m.db: the SQLite handle has a single pooled connection.
func (m *StateManager) updateGuardedClearingDirtyIDs(from entities.MigrationStatus, updates map[string]any) (matched int64, err error) {
	err = m.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&entities.MigrationState{}).
			Where("id = ? AND state = ?", migrationStateID, from).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		matched = result.RowsAffected
		if matched == 0 {
			return nil
		}
		if err := deleteAllDirtyIDs(tx); err != nil {
			return fmt.Errorf("clear dirty IDs: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return matched, nil
}
