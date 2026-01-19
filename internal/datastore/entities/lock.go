package entities

import "time"

// NoteLockEntity represents the lock status of a Note.
// Maps to the 'note_locks' table.
type NoteLockEntity struct {
	ID       uint      `gorm:"primaryKey"`
	NoteID   uint      `gorm:"uniqueIndex;not null;constraint:OnDelete:CASCADE,OnUpdate:CASCADE;foreignKey:NoteID;references:ID"`
	LockedAt time.Time `gorm:"index;not null"`
}

// TableName ensures GORM uses the existing table name.
func (NoteLockEntity) TableName() string {
	return "note_locks"
}
