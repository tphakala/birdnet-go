package v2

import (
	"fmt"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// migrationStateID is the ID of the singleton migration state record.
// The migration state table always contains exactly one row with this ID.
const migrationStateID = 1

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
// Uses atomic update to ensure multi-process safety.
func (m *StateManager) StartMigration(totalRecords int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	updates := map[string]any{
		"state":            entities.MigrationStatusInitializing,
		"started_at":       &now,
		"total_records":    totalRecords,
		"migrated_records": 0,
		"last_migrated_id": 0,
		"error_message":    "",
		"completed_at":     nil,
	}

	result := m.db.Model(&entities.MigrationState{}).
		Where("id = ? AND state = ?", migrationStateID, entities.MigrationStatusIdle).
		Updates(updates)

	if result.Error != nil {
		return fmt.Errorf("failed to start migration: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		var current entities.MigrationState
		if err := m.db.First(&current).Error; err != nil {
			return fmt.Errorf("failed to get current state: %w", err)
		}
		return fmt.Errorf("cannot start migration: current state is %s, expected idle", current.State)
	}

	return nil
}

// TransitionToDualWrite transitions from INITIALIZING to DUAL_WRITE.
func (m *StateManager) TransitionToDualWrite() error {
	return m.transitionState(entities.MigrationStatusInitializing, entities.MigrationStatusDualWrite)
}

// Pause transitions from DUAL_WRITE, MIGRATING, or VALIDATING to PAUSED.
// Uses atomic update to ensure multi-process safety.
func (m *StateManager) Pause() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pausableStates := []entities.MigrationStatus{
		entities.MigrationStatusDualWrite,
		entities.MigrationStatusMigrating,
		entities.MigrationStatusValidating,
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
	return m.transitionState(entities.MigrationStatusPaused, entities.MigrationStatusDualWrite)
}

// TransitionToMigrating transitions from DUAL_WRITE to MIGRATING.
// This is called when the migration worker starts processing records.
func (m *StateManager) TransitionToMigrating() error {
	return m.transitionState(entities.MigrationStatusDualWrite, entities.MigrationStatusMigrating)
}

// TransitionToValidating transitions from MIGRATING to VALIDATING.
// This is called when all records have been migrated.
func (m *StateManager) TransitionToValidating() error {
	return m.transitionState(entities.MigrationStatusMigrating, entities.MigrationStatusValidating)
}

// TransitionToCutover transitions from VALIDATING to CUTOVER.
// This is called when validation passes.
func (m *StateManager) TransitionToCutover() error {
	return m.transitionState(entities.MigrationStatusValidating, entities.MigrationStatusCutover)
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
		return fmt.Errorf("failed to complete migration: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		var current entities.MigrationState
		if err := m.db.First(&current).Error; err != nil {
			return fmt.Errorf("failed to get current state: %w", err)
		}
		return fmt.Errorf("cannot complete migration: current state is %s, expected cutover", current.State)
	}

	return nil
}

// Cancel transitions any active state back to IDLE.
// Uses atomic update to ensure multi-process safety.
func (m *StateManager) Cancel() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cancellableStates := []entities.MigrationStatus{
		entities.MigrationStatusInitializing,
		entities.MigrationStatusDualWrite,
		entities.MigrationStatusPaused,
		entities.MigrationStatusMigrating,
		entities.MigrationStatusValidating,
		entities.MigrationStatusCutover,
	}

	updates := map[string]any{
		"state":            entities.MigrationStatusIdle,
		"started_at":       nil,
		"completed_at":     nil,
		"total_records":    0,
		"migrated_records": 0,
		"last_migrated_id": 0,
		"error_message":    "",
	}

	result := m.db.Model(&entities.MigrationState{}).
		Where("id = ? AND state IN ?", migrationStateID, cancellableStates).
		Updates(updates)

	if result.Error != nil {
		return fmt.Errorf("failed to cancel migration: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		var current entities.MigrationState
		if err := m.db.First(&current).Error; err != nil {
			return fmt.Errorf("failed to get current state: %w", err)
		}
		return fmt.Errorf("cannot cancel migration: current state is %s", current.State)
	}

	return nil
}

// Rollback transitions from COMPLETED back to IDLE for rollback scenarios.
// Uses atomic update to ensure multi-process safety.
func (m *StateManager) Rollback() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	updates := map[string]any{
		"state":            entities.MigrationStatusIdle,
		"started_at":       nil,
		"completed_at":     nil,
		"total_records":    0,
		"migrated_records": 0,
		"last_migrated_id": 0,
		"error_message":    "",
	}

	result := m.db.Model(&entities.MigrationState{}).
		Where("id = ? AND state = ?", migrationStateID, entities.MigrationStatusCompleted).
		Updates(updates)

	if result.Error != nil {
		return fmt.Errorf("failed to rollback migration: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		var current entities.MigrationState
		if err := m.db.First(&current).Error; err != nil {
			return fmt.Errorf("failed to get current state: %w", err)
		}
		return fmt.Errorf("cannot rollback: current state is %s, expected completed", current.State)
	}

	return nil
}

// UpdateProgress updates the migration progress counters.
func (m *StateManager) UpdateProgress(lastMigratedID uint, migratedRecords int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	updates := map[string]any{
		"last_migrated_id": lastMigratedID,
		"migrated_records": migratedRecords,
	}

	return m.db.Model(&entities.MigrationState{}).Where("id = ?", migrationStateID).Updates(updates).Error
}

// IncrementProgress increments the migrated records count by delta.
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

// transitionState is a helper that validates and performs a state transition.
// Uses atomic SQL update with WHERE clause for multi-process safety.
func (m *StateManager) transitionState(from, to entities.MigrationStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := m.db.Model(&entities.MigrationState{}).
		Where("id = ? AND state = ?", migrationStateID, from).
		Update("state", to)

	if result.Error != nil {
		return fmt.Errorf("failed to transition state: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		var current entities.MigrationState
		if err := m.db.First(&current).Error; err != nil {
			return fmt.Errorf("failed to get current state: %w", err)
		}
		return fmt.Errorf("cannot transition to %s: current state is %s, expected %s", to, current.State, from)
	}

	return nil
}

// IsInDualWriteMode returns true if dual-write is active.
// Dual-write is active during DUAL_WRITE, MIGRATING, VALIDATING, and CUTOVER states.
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
// These methods track detection IDs that failed to write to V2 during dual-write.
// Unlike in-memory tracking, these persist across restarts.

// AddDirtyID records a detection ID that failed to write to V2.
// This is called when saveToV2 fails during dual-write.
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

// GetDirtyIDCount returns the count of dirty IDs awaiting re-sync.
func (m *StateManager) GetDirtyIDCount() (int64, error) {
	var count int64
	if err := m.db.Model(&entities.MigrationDirtyID{}).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("failed to count dirty IDs: %w", err)
	}
	return count, nil
}

// ClearDirtyIDs removes all dirty IDs (used after successful full reconciliation).
func (m *StateManager) ClearDirtyIDs() error {
	return m.db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&entities.MigrationDirtyID{}).Error
}
