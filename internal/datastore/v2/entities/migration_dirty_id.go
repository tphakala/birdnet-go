package entities

import "time"

// MigrationDirtyID tracks detection IDs that the migration worker failed to write to V2.
// These IDs need to be reconciled by the migration worker.
type MigrationDirtyID struct {
	DetectionID uint      `gorm:"primaryKey"`
	CreatedAt   time.Time `gorm:"autoCreateTime"`
}
