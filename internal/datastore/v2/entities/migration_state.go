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

// MigrationState tracks the progress of database migration.
// This is a singleton table (only one row with ID=1).
type MigrationState struct {
	ID uint `gorm:"primaryKey"` // Singleton enforced by StateManager (id=1)
	State           MigrationStatus `gorm:"type:varchar(20);not null;default:'idle'"`
	CurrentPhase    MigrationPhase  `gorm:"type:varchar(20);not null;default:''"` // Current migration phase for UI display
	PhaseNumber     int             `gorm:"default:0"`                            // Current phase number (1-based)
	TotalPhases     int             `gorm:"default:0"`                            // Total number of phases
	StartedAt       *time.Time
	PhaseStartedAt  *time.Time                                        // When current phase started (for rate calculation)
	CompletedAt     *time.Time
	LastMigratedID  uint  `gorm:"default:0"` // Last legacy notes.id processed
	TotalRecords    int64 `gorm:"default:0"` // Total records for current phase
	MigratedRecords int64 `gorm:"default:0"` // Records migrated in current phase
	ErrorMessage     string `gorm:"type:text"`
	RelatedDataError string `gorm:"column:related_data_error;type:text"` // Error from related data migration (reviews, comments, locks, predictions)
	UpdatedAt        time.Time `gorm:"autoUpdateTime"`
}

// TableName returns the table name for GORM.
func (MigrationState) TableName() string {
	return "migration_state"
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
