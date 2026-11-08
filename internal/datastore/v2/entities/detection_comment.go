package entities

import "time"

// DetectionComment stores user comments on a detection.
// This replaces the legacy 'note_comments' table.
type DetectionComment struct {
	ID          uint      `gorm:"primaryKey"`
	DetectionID uint      `gorm:"not null;index"`
	Entry       string    `gorm:"type:text;not null"`
	CreatedAt   time.Time `gorm:"autoCreateTime;index"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime"`

	// LegacyID is the legacy note_comments.id this row was copied from, NULL for
	// comments created in v2. It is unique so a repeated copy of the same legacy
	// comment is skipped instead of duplicated.
	LegacyID *uint `gorm:"uniqueIndex"`

	// Relationship
	Detection *Detection `gorm:"foreignKey:DetectionID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
}
