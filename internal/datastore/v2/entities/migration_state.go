package entities

import "time"

// MigrationStatus represents the state of database migration.
type MigrationStatus string

const (
	MigrationStatusIdle         MigrationStatus = "idle"
	MigrationStatusInitializing MigrationStatus = "initializing"
	MigrationStatusDualWrite    MigrationStatus = "dual_write"
	MigrationStatusPaused       MigrationStatus = "paused"
	MigrationStatusMigrating    MigrationStatus = "migrating"
	MigrationStatusValidating   MigrationStatus = "validating"
	MigrationStatusCutover      MigrationStatus = "cutover"
	MigrationStatusCompleted    MigrationStatus = "completed"
	MigrationStatusFailed       MigrationStatus = "failed"
)

// MigrationPhase represents which phase of migration is currently active.
type MigrationPhase string

const (
	MigrationPhaseNone        MigrationPhase = ""            // Not migrating
	MigrationPhaseDetections  MigrationPhase = "detections"  // Migrating detection records
	MigrationPhaseReviews     MigrationPhase = "reviews"     // Migrating reviews
	MigrationPhaseComments    MigrationPhase = "comments"    // Migrating comments
	MigrationPhaseLocks       MigrationPhase = "locks"       // Migrating locks
	MigrationPhasePredictions MigrationPhase = "predictions" // Migrating predictions (largest)
)

// AuxiliaryDataStatus is the recorded outcome of the auxiliary data migration
// (image cache, dynamic thresholds, threshold events, notification history,
// daily events and hourly weather).
type AuxiliaryDataStatus string

const (
	// AuxiliaryDataStatusUnknown means no outcome was recorded for the current
	// migration attempt (rows from before the outcome was recorded, or an
	// attempt that never reached the auxiliary step).
	AuxiliaryDataStatusUnknown AuxiliaryDataStatus = ""
	// AuxiliaryDataStatusSucceeded means every selected row was copied or was a
	// legacy row that cannot exist in v2.
	AuxiliaryDataStatusSucceeded AuxiliaryDataStatus = "succeeded"
	// AuxiliaryDataStatusFailed means at least one section failed to copy rows
	// that should have been copied; AuxiliaryDataError summarises the sections.
	AuxiliaryDataStatusFailed AuxiliaryDataStatus = "failed"
	// AuxiliaryDataStatusNotRequired means there was nothing to migrate (fresh
	// v2-only install).
	AuxiliaryDataStatusNotRequired AuxiliaryDataStatus = "not_required"
)

// MigrationState tracks the progress of database migration.
// This is a singleton table (only one row with ID=1).
type MigrationState struct {
	ID               uint            `gorm:"primaryKey"` // Singleton enforced by StateManager (id=1)
	State            MigrationStatus `gorm:"type:varchar(20);not null;default:'idle'"`
	CurrentPhase     MigrationPhase  `gorm:"type:varchar(20);not null;default:''"` // Current migration phase for UI display
	PhaseNumber      int             `gorm:"default:0"`                            // Current phase number (1-based)
	TotalPhases      int             `gorm:"default:0"`                            // Total number of phases
	StartedAt        *time.Time
	PhaseStartedAt   *time.Time // When current phase started (for rate calculation)
	CompletedAt      *time.Time
	LastMigratedID   uint   `gorm:"default:0"` // Last legacy notes.id processed
	TotalRecords     int64  `gorm:"default:0"` // Total records for current phase
	MigratedRecords  int64  `gorm:"default:0"` // Records migrated in current phase
	ErrorMessage     string `gorm:"type:text"`
	RelatedDataError string `gorm:"column:related_data_error;type:text"` // Error from related data migration (reviews, comments, locks, predictions)
	// AuxiliaryDataStatus is the auxiliary data outcome. Read it only through
	// AuxiliaryDataOutcome. "succeeded" promises that the rows the auxiliary
	// step selected were copied at that step. It does not cover rows written to
	// the legacy database afterwards, nor rows the step never selects (image
	// caches of providers other than wikimedia, inactive notification history,
	// threshold events beyond the 100 most recent per species).
	AuxiliaryDataStatus AuxiliaryDataStatus `gorm:"column:auxiliary_data_status;type:varchar(20);not null;default:''"`
	// AuxiliaryDataError summarises the failed sections; empty unless failed.
	AuxiliaryDataError string `gorm:"column:auxiliary_data_error;type:text"`
	// AuxiliaryDataStartedAt is a copy of StartedAt taken in the same UPDATE that
	// recorded the outcome. It ties the outcome to one migration attempt.
	AuxiliaryDataStartedAt *time.Time `gorm:"column:auxiliary_data_started_at"`
	// RelatedSyncStartedAt is a copy of StartedAt taken in the same UPDATE that
	// initialised the related data sync cursors. It ties the cursors to one
	// migration attempt. Read the cursors only through RelatedSyncCursors.
	RelatedSyncStartedAt *time.Time `gorm:"column:related_sync_started_at"`
	// RelatedCommentsCursor is the last legacy note_comments.id handled.
	RelatedCommentsCursor *uint `gorm:"column:related_comments_cursor"`
	// RelatedPredictionsNoteID is the legacy results.note_id of the last
	// prediction row handled; the cursor always sits on a note boundary.
	RelatedPredictionsNoteID *uint `gorm:"column:related_predictions_note_id"`
	// RelatedPredictionsResultID is the legacy results.id of the last prediction
	// row handled.
	RelatedPredictionsResultID *uint `gorm:"column:related_predictions_result_id"`
	// RelatedReconciledAt is the start time of the last completed review and
	// lock reconcile. Legacy review and lock edits older than this are in v2.
	RelatedReconciledAt *time.Time `gorm:"column:related_reconciled_at"`
	UpdatedAt           time.Time  `gorm:"autoUpdateTime"`
}

// AuxiliaryDataOutcome returns the auxiliary data outcome recorded for the
// current migration attempt, or AuxiliaryDataStatusUnknown when it cannot be
// shown to belong to it: no status stored, a state that cannot hold an
// outcome, or AuxiliaryDataStartedAt differing from StartedAt. Both
// timestamps come from database columns, so compare them only after a round
// trip through the database.
func (m *MigrationState) AuxiliaryDataOutcome() AuxiliaryDataStatus {
	if m.AuxiliaryDataStatus == AuxiliaryDataStatusUnknown {
		return AuxiliaryDataStatusUnknown
	}
	switch m.State {
	case MigrationStatusValidating, MigrationStatusFailed, MigrationStatusCutover, MigrationStatusCompleted:
	default:
		return AuxiliaryDataStatusUnknown
	}
	if m.AuxiliaryDataStartedAt == nil || m.StartedAt == nil || !m.AuxiliaryDataStartedAt.Equal(*m.StartedAt) {
		return AuxiliaryDataStatusUnknown
	}
	return m.AuxiliaryDataStatus
}

// RelatedSyncCursors holds the persisted positions of the related data sync.
type RelatedSyncCursors struct {
	// CommentID is the last legacy note_comments.id handled.
	CommentID uint
	// PredictionNoteID and PredictionResultID are the last legacy results row
	// handled, as the (note_id, id) keyset position.
	PredictionNoteID   uint
	PredictionResultID uint
}

// RelatedSyncCursors returns the related data sync cursors recorded for the
// current migration attempt. ok is false when they cannot be shown to belong to
// it: no cursors stored (a related copy by an older version), or
// RelatedSyncStartedAt differing from StartedAt. Both timestamps come from
// database columns, so compare them only after a round trip through the
// database.
func (m *MigrationState) RelatedSyncCursors() (cursors RelatedSyncCursors, ok bool) {
	if m.RelatedSyncStartedAt == nil || m.StartedAt == nil ||
		m.RelatedCommentsCursor == nil || m.RelatedPredictionsNoteID == nil || m.RelatedPredictionsResultID == nil {
		return RelatedSyncCursors{}, false
	}
	if !m.RelatedSyncStartedAt.Equal(*m.StartedAt) {
		return RelatedSyncCursors{}, false
	}
	return RelatedSyncCursors{
		CommentID:          *m.RelatedCommentsCursor,
		PredictionNoteID:   *m.RelatedPredictionsNoteID,
		PredictionResultID: *m.RelatedPredictionsResultID,
	}, true
}

// Progress returns the migration progress as a percentage (0-100).
func (m *MigrationState) Progress() float64 {
	if m.TotalRecords == 0 {
		return 0
	}
	return float64(m.MigratedRecords) / float64(m.TotalRecords) * 100
}

// IsActive returns true if migration is currently in progress.
func (m *MigrationState) IsActive() bool {
	switch m.State {
	case MigrationStatusInitializing, MigrationStatusDualWrite, MigrationStatusMigrating, MigrationStatusValidating, MigrationStatusCutover:
		return true
	default:
		return false
	}
}

// CanPause returns true if the migration can be paused.
func (m *MigrationState) CanPause() bool {
	return m.State == MigrationStatusDualWrite || m.State == MigrationStatusMigrating
}

// CanResume returns true if the migration can be resumed.
func (m *MigrationState) CanResume() bool {
	return m.State == MigrationStatusPaused
}

// CanCancel returns true if the migration can be cancelled.
func (m *MigrationState) CanCancel() bool {
	return m.State != MigrationStatusCompleted && m.State != MigrationStatusIdle
}

// CanRetryValidation returns true if validation can be retried.
// This is only possible when migration has failed due to validation errors.
func (m *MigrationState) CanRetryValidation() bool {
	return m.State == MigrationStatusFailed
}
