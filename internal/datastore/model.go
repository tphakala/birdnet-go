package datastore

import "time"

// Note represents a single observation data point
type Note struct {
	ID             uint `gorm:"primaryKey"`
	SourceNode     string
	Date           string `gorm:"index:idx_notes_date;index:idx_notes_date_commonname_confidence"`
	Time           string `gorm:"index:idx_notes_time"`
	InputFile      string
	BeginTime      time.Time
	EndTime        time.Time
	SpeciesCode    string
	ScientificName string  `gorm:"index:idx_notes_sciname"`
	CommonName     string  `gorm:"index:idx_notes_comname;index:idx_notes_date_commonname_confidence"`
	Confidence     float64 `gorm:"index:idx_notes_date_commonname_confidence"`
	Latitude       float64
	Longitude      float64
	Threshold      float64
	Sensitivity    float64
	ClipName       string
	Comment        string `gorm:"type:text"`
	ProcessingTime time.Duration
}
