package entities

import "time"

// MigrationDirtyID tracks detection IDs that failed to write to V2 during dual-write.
// These IDs need to be reconciled by the migration worker.
type MigrationDirtyID struct {
	DetectionID uint      `gorm:"primaryKey"`
	CreatedAt   time.Time `gorm:"autoCreateTime"`
}

// TableName returns the table name for GORM.
func (MigrationDirtyID) TableName() string {
	return "migration_dirty_ids"
}
