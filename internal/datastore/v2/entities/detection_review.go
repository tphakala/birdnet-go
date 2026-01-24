package entities

import "time"

// VerificationStatus represents the review status of a detection.
type VerificationStatus string

const (
	VerificationCorrect       VerificationStatus = "correct"
	VerificationFalsePositive VerificationStatus = "false_positive"
)

// DetectionReview stores verification status for a detection.
// This replaces the legacy 'note_reviews' table.
type DetectionReview struct {
	ID          uint               `gorm:"primaryKey"`
	DetectionID uint               `gorm:"not null;uniqueIndex"`
	Verified    VerificationStatus `gorm:"type:varchar(20);not null"`
	CreatedAt   time.Time          `gorm:"autoCreateTime;index"`
	UpdatedAt   time.Time          `gorm:"autoUpdateTime"`

	// Relationship
	Detection *Detection `gorm:"foreignKey:DetectionID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
}

// TableName returns the table name for GORM.
func (DetectionReview) TableName() string {
	return "detection_reviews"
}
