package entities

import "time"

// NoteCommentEntity represents user comments on a detection.
// Maps to the 'note_comments' table.
type NoteCommentEntity struct {
	ID        uint      `gorm:"primaryKey"`
	NoteID    uint      `gorm:"index;not null;constraint:OnDelete:CASCADE,OnUpdate:CASCADE;foreignKey:NoteID;references:ID"`
	Entry     string    `gorm:"type:text"`
	CreatedAt time.Time `gorm:"index"`
	UpdatedAt time.Time
}

// TableName ensures GORM uses the existing table name.
func (NoteCommentEntity) TableName() string {
	return "note_comments"
}
