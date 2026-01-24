package entities

import "time"

// DetectionReview stores verification status for a detection.
// This replaces the legacy 'note_reviews' table.
type DetectionReview struct {
	ID          uint      `gorm:"primaryKey"`
	DetectionID uint      `gorm:"not null;uniqueIndex"`
	Verified    string    `gorm:"type:varchar(20);not null"` // 'correct', 'false_positive'
	CreatedAt   time.Time `gorm:"autoCreateTime;index"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime"`

	// Relationship
	Detection *Detection `gorm:"foreignKey:DetectionID;constraint:OnDelete:CASCADE"`
}

// TableName returns the table name for GORM.
func (DetectionReview) TableName() string {
	return "detection_reviews"
}
