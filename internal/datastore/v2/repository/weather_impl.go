package repository

import (
	"context"
	"errors"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// weatherRepository implements WeatherRepository.
type weatherRepository struct {
	db          *gorm.DB
	useV2Prefix bool
	isMySQL     bool // Dialect flag: true for MySQL (UNIX_TIMESTAMP), false for SQLite (strftime)
}

// NewWeatherRepository creates a new WeatherRepository.
// Parameters:
//   - db: GORM database connection
//   - useV2Prefix: true to use v2_ table prefix (MySQL migration mode)
//   - isMySQL: true for MySQL dialect (affects date/time SQL expressions)
func NewWeatherRepository(db *gorm.DB, useV2Prefix, isMySQL bool) WeatherRepository {
	return &weatherRepository{
		db:          db,
		useV2Prefix: useV2Prefix,
		isMySQL:     isMySQL,
	}
}

func (r *weatherRepository) dailyEventsTable() string {
	if r.useV2Prefix {
		return tableV2DailyEvents
	}
	return tableDailyEvents
}

func (r *weatherRepository) hourlyWeatherTable() string {
	if r.useV2Prefix {
		return tableV2HourlyWeathers
	}
	return tableHourlyWeathers
}

// SaveDailyEvents saves or updates daily events (upsert).
func (r *weatherRepository) SaveDailyEvents(ctx context.Context, events *entities.DailyEvents) error {
	return r.db.WithContext(ctx).Table(r.dailyEventsTable()).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "date"}},
			UpdateAll: true,
		}).
		Create(events).Error
}

// GetDailyEvents retrieves daily events by date.
func (r *weatherRepository) GetDailyEvents(ctx context.Context, date string) (*entities.DailyEvents, error) {
	var events entities.DailyEvents
	err := r.db.WithContext(ctx).Table(r.dailyEventsTable()).
		Where("date = ?", date).
		First(&events).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrDailyEventsNotFound
	}
	if err != nil {
		return nil, err
	}
	return &events, nil
}

// SaveHourlyWeather saves hourly weather data.
func (r *weatherRepository) SaveHourlyWeather(ctx context.Context, weather *entities.HourlyWeather) error {
	return r.db.WithContext(ctx).Table(r.hourlyWeatherTable()).Create(weather).Error
}

// GetHourlyWeather retrieves hourly weather for a date.
func (r *weatherRepository) GetHourlyWeather(ctx context.Context, date string) ([]entities.HourlyWeather, error) {
	var weather []entities.HourlyWeather

	// First get the daily events ID for this date
	dailyEvents, err := r.GetDailyEvents(ctx, date)
	if err != nil {
		if errors.Is(err, ErrDailyEventsNotFound) {
			return []entities.HourlyWeather{}, nil
		}
		return nil, err
	}

	err = r.db.WithContext(ctx).Table(r.hourlyWeatherTable()).
		Where("daily_events_id = ?", dailyEvents.ID).
		Order("time ASC").
		Find(&weather).Error
	return weather, err
}

// GetHourlyWeatherInLocation retrieves hourly weather for a date in a specific timezone.
func (r *weatherRepository) GetHourlyWeatherInLocation(ctx context.Context, date string, loc *time.Location) ([]entities.HourlyWeather, error) {
	// Parse the date in the given location
	startOfDay, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return nil, err
	}
	// Use AddDate instead of Add(24*time.Hour) to correctly handle DST transitions
	// where days can be 23 hours (spring forward) or 25 hours (fall back)
	endOfDay := startOfDay.AddDate(0, 0, 1)

	var weather []entities.HourlyWeather
	// Convert timestamps to Unix epoch seconds for comparison.
	// This handles legacy records stored with local timezone offsets alongside new UTC records.
	var whereClause string
	if r.isMySQL {
		whereClause = "UNIX_TIMESTAMP(time) >= UNIX_TIMESTAMP(?) AND UNIX_TIMESTAMP(time) < UNIX_TIMESTAMP(?)"
	} else {
		whereClause = "strftime('%s', time) >= strftime('%s', ?) AND strftime('%s', time) < strftime('%s', ?)"
	}

	err = r.db.WithContext(ctx).Table(r.hourlyWeatherTable()).
		Where(whereClause, startOfDay, endOfDay).
		Order("time ASC").
		Find(&weather).Error
	return weather, err
}

// LatestHourlyWeather retrieves the most recent hourly weather entry.
func (r *weatherRepository) LatestHourlyWeather(ctx context.Context) (*entities.HourlyWeather, error) {
	var weather entities.HourlyWeather
	err := r.db.WithContext(ctx).Table(r.hourlyWeatherTable()).
		Order("time DESC").
		First(&weather).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrHourlyWeatherNotFound
	}
	if err != nil {
		return nil, err
	}
	return &weather, nil
}

// GetAllDailyEvents retrieves all daily events.
// Used for building ID mapping during migration.
func (r *weatherRepository) GetAllDailyEvents(ctx context.Context) ([]entities.DailyEvents, error) {
	var events []entities.DailyEvents
	err := r.db.WithContext(ctx).Table(r.dailyEventsTable()).
		Order("date ASC").
		Find(&events).Error
	return events, err
}

// SaveAllDailyEvents saves multiple daily events in batches.
// Uses upsert to handle conflicts (same date = skip).
// Returns count of successfully processed records.
func (r *weatherRepository) SaveAllDailyEvents(ctx context.Context, events []entities.DailyEvents) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}

	var saved int
	batchSize := 100

	for i := 0; i < len(events); i += batchSize {
		end := min(i+batchSize, len(events))
		batch := events[i:end]

		err := r.db.WithContext(ctx).Table(r.dailyEventsTable()).
			Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "date"}},
				DoNothing: true,
			}).
			Create(&batch).Error
		if err != nil {
			return saved, err
		}
		saved += len(batch)
	}

	return saved, nil
}

// SaveAllHourlyWeather saves multiple hourly weather records in batches.
// Caller must ensure DailyEventsID values are already remapped to V2 IDs.
// Returns count of successfully saved records.
func (r *weatherRepository) SaveAllHourlyWeather(ctx context.Context, weather []entities.HourlyWeather) (int, error) {
	if len(weather) == 0 {
		return 0, nil
	}

	var saved int
	batchSize := 500

	for i := 0; i < len(weather); i += batchSize {
		end := min(i+batchSize, len(weather))
		batch := weather[i:end]

		err := r.db.WithContext(ctx).Table(r.hourlyWeatherTable()).
			Create(&batch).Error
		if err != nil {
			return saved, err
		}
		saved += len(batch)
	}

	return saved, nil
}
