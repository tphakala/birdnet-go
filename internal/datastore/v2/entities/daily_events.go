package entities

// DailyEvents stores daily weather metadata (sunrise/sunset).
// This mirrors the legacy daily_events table structure.
type DailyEvents struct {
	ID       uint   `gorm:"primaryKey"`
	Date     string `gorm:"uniqueIndex;size:10"` // YYYY-MM-DD
	Sunrise  int64  // Unix timestamp
	Sunset   int64  // Unix timestamp
	Country  string `gorm:"size:100"`
	CityName string `gorm:"size:200"`
}

// TableName returns the table name for GORM.
func (DailyEvents) TableName() string {
	return "daily_events"
}
