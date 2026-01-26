package entities

import "time"

// HourlyWeather stores hourly weather data.
// This mirrors the legacy hourly_weathers table structure.
type HourlyWeather struct {
	ID            uint      `gorm:"primaryKey"`
	DailyEventsID uint      `gorm:"index"` // Foreign key to DailyEvents
	Time          time.Time `gorm:"index"`
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
	WeatherMain   string `gorm:"size:50"`
	WeatherDesc   string `gorm:"size:200"`
	WeatherIcon   string `gorm:"size:20"`
	CreatedAt     time.Time `gorm:"autoCreateTime"`
}

// TableName returns the table name for GORM.
func (HourlyWeather) TableName() string {
	return "hourly_weathers"
}
