package repository

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// WeatherRepository handles weather data operations.
type WeatherRepository interface {
	// DailyEvents operations
	SaveDailyEvents(ctx context.Context, events *entities.DailyEvents) error
	GetDailyEvents(ctx context.Context, date string) (*entities.DailyEvents, error)

	// HourlyWeather operations
	SaveHourlyWeather(ctx context.Context, weather *entities.HourlyWeather) error
	GetHourlyWeather(ctx context.Context, date string) ([]entities.HourlyWeather, error)
	GetHourlyWeatherInLocation(ctx context.Context, date string, loc *time.Location) ([]entities.HourlyWeather, error)
	LatestHourlyWeather(ctx context.Context) (*entities.HourlyWeather, error)

	// Bulk operations for migration
	GetAllDailyEvents(ctx context.Context) ([]entities.DailyEvents, error)
	SaveAllDailyEvents(ctx context.Context, events []entities.DailyEvents) (int, error)
	SaveAllHourlyWeather(ctx context.Context, weather []entities.HourlyWeather) (int, error)
}
