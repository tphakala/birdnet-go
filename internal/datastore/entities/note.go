// Package entities contains GORM models that map directly to database tables.
// These are persistence-layer structures separate from the domain model.
package entities

import "time"

// NoteEntity is the GORM model for the 'notes' table.
// Maps to the EXISTING database schema for backward compatibility.
type NoteEntity struct {
	ID             uint   `gorm:"primaryKey"`
	SourceNode     string
	Date           string `gorm:"index:idx_notes_date;index:idx_notes_date_commonname_confidence;index:idx_notes_sciname_date;index:idx_notes_sciname_date_optimized,priority:2"`
	Time           string `gorm:"index:idx_notes_time"`
	BeginTime      time.Time
	EndTime        time.Time
	SpeciesCode    string
	ScientificName string  `gorm:"index:idx_notes_sciname;index:idx_notes_sciname_date;index:idx_notes_sciname_date_optimized,priority:1"`
	CommonName     string  `gorm:"index:idx_notes_comname;index:idx_notes_date_commonname_confidence"`
	Confidence     float64 `gorm:"index:idx_notes_date_commonname_confidence"`
	Latitude       float64
	Longitude      float64
	Threshold      float64
	Sensitivity    float64
	ClipName       string
	ProcessingTime time.Duration

	// Relationships
	Results  []ResultsEntity     `gorm:"foreignKey:NoteID;constraint:OnDelete:CASCADE"`
	Review   *NoteReviewEntity   `gorm:"foreignKey:NoteID;constraint:OnDelete:CASCADE"`
	Comments []NoteCommentEntity `gorm:"foreignKey:NoteID;constraint:OnDelete:CASCADE"`
	Lock     *NoteLockEntity     `gorm:"foreignKey:NoteID;constraint:OnDelete:CASCADE"`
}

// TableName ensures GORM uses the existing table name.
func (NoteEntity) TableName() string {
	return "notes"
}
