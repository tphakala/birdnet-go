package entities

import "time"

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
