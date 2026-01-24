package entities

import "time"

// DetectionLock prevents modification/deletion of a detection.
// This replaces the legacy 'note_locks' table.
type DetectionLock struct {
	ID          uint      `gorm:"primaryKey"`
	DetectionID uint      `gorm:"not null;uniqueIndex"`
	LockedAt    time.Time `gorm:"autoCreateTime;index"`

	// Relationship
	Detection *Detection `gorm:"foreignKey:DetectionID;constraint:OnDelete:CASCADE"`
}

// TableName returns the table name for GORM.
func (DetectionLock) TableName() string {
	return "detection_locks"
}
