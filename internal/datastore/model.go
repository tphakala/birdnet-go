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
	Comment        string `gorm:"type:text"`
	ProcessingTime time.Duration
	Results        []Results `gorm:"foreignKey:NoteID"`
}

// Result represents the identification result with a species name and its confidence level, linked to a Note.
type Results struct {
	ID         uint `gorm:"primaryKey"`
	NoteID     uint // Foreign key to associate with Note
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
