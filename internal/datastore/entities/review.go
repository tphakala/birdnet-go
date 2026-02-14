package entities

import "time"

// VerificationStatus represents the review status values.
type VerificationStatus string

const (
	// VerificationCorrect indicates a detection is verified as correct.
	VerificationCorrect VerificationStatus = "correct"
	// VerificationFalsePositive indicates a detection is marked as a false positive.
	VerificationFalsePositive VerificationStatus = "false_positive"
)

// NoteReviewEntity represents the review status of a Note.
// Maps to the 'note_reviews' table.
type NoteReviewEntity struct {
	ID        uint      `gorm:"primaryKey"`
	NoteID    uint      `gorm:"uniqueIndex;not null;constraint:OnDelete:CASCADE,OnUpdate:CASCADE;foreignKey:NoteID;references:ID"`
	Verified  string    `gorm:"type:varchar(20)"` // Values: "correct", "false_positive"
	CreatedAt time.Time `gorm:"index"`
	UpdatedAt time.Time
}

// TableName ensures GORM uses the existing table name.
func (NoteReviewEntity) TableName() string {
	return "note_reviews"
}
