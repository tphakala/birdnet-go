package entities

// ResultsEntity represents additional species predictions for a detection.
// Maps to the 'results' table containing secondary predictions.
type ResultsEntity struct {
	ID         uint `gorm:"primaryKey"`
	NoteID     uint `gorm:"index;not null;constraint:OnDelete:CASCADE,OnUpdate:CASCADE;foreignKey:NoteID;references:ID"`
	Species    string
	Confidence float32
}

// TableName ensures GORM uses the existing table name.
func (ResultsEntity) TableName() string {
	return "results"
}

// Copy creates a deep copy of the ResultsEntity.
func (r ResultsEntity) Copy() ResultsEntity {
	return ResultsEntity{
		ID:         r.ID,
		NoteID:     r.NoteID,
		Species:    r.Species,
		Confidence: r.Confidence,
	}
}
