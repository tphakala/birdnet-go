package datastore

import "time"

// Note represents a single observation data point
type Note struct {
	ID             uint `gorm:"primaryKey"`
	SourceNode     string
	Date           string `gorm:"index:idx_notes_date_commonname_confidence"`
	Time           string
	InputFile      string
	BeginTime      float64
	EndTime        float64
	SpeciesCode    string
	ScientificName string  `gorm:"index"`
	CommonName     string  `gorm:"index;index:idx_notes_date_commonname_confidence"`
	Confidence     float64 `gorm:"index:idx_notes_date_commonname_confidence"`
	Latitude       float64
	Longitude      float64
	Threshold      float64
	Sensitivity    float64
	ClipName       string
	ProcessingTime time.Duration
}

type NoteWithSpectrogram struct {
	Notes           []Note
	SpectrogramPath string
}
