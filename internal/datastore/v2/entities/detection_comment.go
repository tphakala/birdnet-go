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

	// Relationship
	Detection *Detection `gorm:"foreignKey:DetectionID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
}

// TableName returns the table name for GORM.
func (DetectionComment) TableName() string {
	return "detection_comments"
}
