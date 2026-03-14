package entities

// DailyEvents stores daily weather metadata (sunrise/sunset).
// This mirrors the legacy daily_events table structure.
type DailyEvents struct {
	ID               uint    `gorm:"primaryKey"`
	Date             string  `gorm:"uniqueIndex;size:10"` // YYYY-MM-DD
	Sunrise          int64   // Unix timestamp
	Sunset           int64   // Unix timestamp
	Country          string  `gorm:"size:100"`
	CityName         string  `gorm:"size:200"`
	MoonPhase        float64 // 0–27.99 raw phase value
	MoonIllumination float64 // 0–100 percentage
}
