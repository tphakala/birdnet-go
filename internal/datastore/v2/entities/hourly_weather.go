package entities

import "time"

// HourlyWeather stores hourly weather data.
// It mirrors the legacy hourly_weathers table structure plus the v2-only
// Precipitation/PrecipitationType columns, which the deprecated legacy schema
// intentionally does not carry.
type HourlyWeather struct {
	ID                uint      `gorm:"primaryKey"`
	DailyEventsID     uint      `gorm:"index"` // Foreign key to DailyEvents
	Time              time.Time `gorm:"index"`
	Temperature       float64
	FeelsLike         float64
	TempMin           float64
	TempMax           float64
	Pressure          int
	Humidity          int
	Visibility        int
	WindSpeed         float64
	WindDeg           int
	WindGust          float64
	Clouds            int
	Precipitation     float64   // Precipitation amount in mm for the observation window
	PrecipitationType string    `gorm:"size:20"` // "rain", "snow", "sleet", or "" when none
	WeatherMain       string    `gorm:"size:50"`
	WeatherDesc       string    `gorm:"size:200"`
	WeatherIcon       string    `gorm:"size:20"`
	CreatedAt         time.Time `gorm:"autoCreateTime"`
}
