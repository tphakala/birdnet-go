// model.go this code defines the data model for the application
package datastore

import "time"

// Note represents a single observation data point
type Note struct {
	ID         uint `gorm:"primaryKey"`
	SourceNode string
	Date       string `gorm:"index:idx_notes_date;index:idx_notes_date_commonname_confidence"`
	Time       string `gorm:"index:idx_notes_time"`
	//InputFile      string
	Source         string
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
	ProcessingTime time.Duration
	Results        []Results     `gorm:"foreignKey:NoteID;constraint:OnDelete:CASCADE"`
	Review         *NoteReview   `gorm:"foreignKey:NoteID;constraint:OnDelete:CASCADE"` // One-to-one relationship with cascade delete
	Comments       []NoteComment `gorm:"foreignKey:NoteID;constraint:OnDelete:CASCADE"` // One-to-many relationship with cascade delete

	// Virtual field to maintain compatibility with templates
	Verified string `gorm:"-"` // This will be populated from Review.Verified
}

// Result represents the identification result with a species name and its confidence level, linked to a Note.
type Results struct {
	ID         uint `gorm:"primaryKey"`
	NoteID     uint `gorm:"index;not null;constraint:OnDelete:CASCADE,OnUpdate:CASCADE;foreignKey:NoteID;references:ID"` // Foreign key to associate with Note
	Species    string
	Confidence float32
}

// Copy creates a deep copy of the Results struct
func (r Results) Copy() Results {
	return Results{
		ID:         r.ID,
		NoteID:     r.NoteID,
		Species:    r.Species,
		Confidence: r.Confidence,
	}
}

// NoteReview represents the review status of a Note
// GORM will automatically create table name as 'note_reviews'
type NoteReview struct {
	ID        uint      `gorm:"primaryKey"`
	NoteID    uint      `gorm:"uniqueIndex;not null;constraint:OnDelete:CASCADE,OnUpdate:CASCADE;foreignKey:NoteID;references:ID"` // Foreign key to associate with Note
	Verified  string    `gorm:"type:varchar(20)"`                                                                                  // Values: "correct", "false_positive"
	CreatedAt time.Time `gorm:"index"`                                                                                             // When the review was created
	UpdatedAt time.Time // When the review was last updated
}

// NoteComment represents user comments on a detection
// GORM will automatically create table name as 'note_comments'
type NoteComment struct {
	ID        uint      `gorm:"primaryKey"`
	NoteID    uint      `gorm:"index;not null;constraint:OnDelete:CASCADE,OnUpdate:CASCADE;foreignKey:NoteID;references:ID"` // Foreign key to associate with Note
	Entry     string    `gorm:"type:text"`                                                                                   // The actual comment text
	CreatedAt time.Time `gorm:"index"`                                                                                       // When the comment was created
	UpdatedAt time.Time // When the comment was last updated
}

// DailyEvents represents the daily weather data that doesn't change throughout the day
type DailyEvents struct {
	ID       uint   `gorm:"primaryKey"`
	Date     string `gorm:"index:idx_dailyweather_date"`
	Sunrise  int64
	Sunset   int64
	Country  string
	CityName string
}

// HourlyWeather represents the hourly weather data that changes throughout the day
type HourlyWeather struct {
	ID            uint `gorm:"primaryKey"`
	DailyEventsID uint `gorm:"index"` // Foreign key to associate with DailyEvents
	Time          time.Time
	Temperature   float64
	FeelsLike     float64
	TempMin       float64
	TempMax       float64
	Pressure      int
	Humidity      int
	Visibility    int
	WindSpeed     float64
	WindDeg       int
	WindGust      float64
	Clouds        int
	WeatherMain   string
	WeatherDesc   string
	WeatherIcon   string
}
