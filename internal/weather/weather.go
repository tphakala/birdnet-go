package weather

import (
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// getLogger returns the weather service logger.
// Fetched dynamically to ensure it uses the current centralized logger.
func getLogger() logger.Logger {
	return logger.Global().Module("weather")
}

// Provider represents a weather data provider interface
type Provider interface {
	FetchWeather(settings *conf.Settings) (*WeatherData, error)
}

// Service handles weather data operations
type Service struct {
	provider Provider
	db       datastore.Interface
	settings *conf.Settings
	metrics  *metrics.WeatherMetrics
}

// WeatherData represents the common structure for weather data across providers
type WeatherData struct {
	Time          time.Time
	Location      Location
	Temperature   Temperature
	Wind          Wind
	Precipitation Precipitation
	Clouds        int
	Visibility    int
	Pressure      int
	Humidity      int
	Description   string
	Icon          string
}

type Location struct {
	Latitude  float64
	Longitude float64
	Country   string
	City      string
}

type Temperature struct {
	Current   float64
	FeelsLike float64
	Min       float64
	Max       float64
}

type Wind struct {
	Speed float64
	Deg   int
	Gust  float64
}

type Precipitation struct {
	Amount float64
	Type   string // rain, snow, etc.
}

// NewService creates a new weather service with the specified provider
func NewService(settings *conf.Settings, db datastore.Interface, weatherMetrics *metrics.WeatherMetrics) (*Service, error) {
	var provider Provider

	// Select weather provider based on configuration
	switch settings.Realtime.Weather.Provider {
	case "yrno":
		provider = NewYrNoProvider()
	case "openweather":
		provider = NewOpenWeatherProvider()
	case "wunderground":
		provider = NewWundergroundProvider(nil)
	default:
		return nil, errors.Newf("invalid weather provider: %s", settings.Realtime.Weather.Provider).
			Component("weather").
			Category(errors.CategoryConfiguration).
			Context("provider", settings.Realtime.Weather.Provider).
			Build()
	}

	return &Service{
		provider: provider,
		db:       db,
		settings: settings,
		metrics:  weatherMetrics,
	}, nil
}

// SaveWeatherData saves the weather data to the database
func (s *Service) SaveWeatherData(data *WeatherData) error {
	// Track operation duration
	start := time.Now()
	defer func() {
		if s.metrics != nil {
			s.metrics.RecordWeatherDbDuration("save_weather_data", time.Since(start).Seconds())
		}
	}()

	// Store weather time in UTC for consistent database comparisons.
	// SQLite compares timestamps as strings, so mixing timezone offsets
	// (e.g., +13:00 vs +00:00) produces incorrect results.
	// The read path (buildHourlyWeatherResponse) converts to local time for display.
	utcTime := data.Time.UTC()

	// Use local time only for the date string used by DailyEvents
	localDate := utcTime.In(time.Local).Format(time.DateOnly)

	// Create daily events data
	dailyEvents := &datastore.DailyEvents{
		Date:     localDate,
		Country:  data.Location.Country,
		CityName: data.Location.City,
	}

	// Save daily events data
	if err := s.db.SaveDailyEvents(dailyEvents); err != nil {
		// Log the error before returning
		getLogger().Error("Failed to save daily events to database",
			logger.Error(err),
			logger.String("date", dailyEvents.Date),
			logger.String("city", dailyEvents.CityName))
		if s.metrics != nil {
			s.metrics.RecordWeatherDbError("save_daily_events", "database_error")
		}
		return errors.New(err).
			Component("weather").
			Category(errors.CategoryDatabase).
			Context("operation", "save_daily_events").
			Context("date", dailyEvents.Date).
			Build()
	}
	if s.metrics != nil {
		s.metrics.RecordWeatherDbOperation("save_daily_events", "success")
	}

	// Create hourly weather data
	hourlyWeather := &datastore.HourlyWeather{
		DailyEventsID: dailyEvents.ID,
		Time:          utcTime,
		Temperature:   data.Temperature.Current,
		FeelsLike:     data.Temperature.FeelsLike,
		TempMin:       data.Temperature.Min,
		TempMax:       data.Temperature.Max,
		Pressure:      data.Pressure,
		Humidity:      data.Humidity,
		Visibility:    data.Visibility,
		WindSpeed:     data.Wind.Speed,
		WindDeg:       data.Wind.Deg,
		WindGust:      data.Wind.Gust,
		Clouds:        data.Clouds,
		WeatherDesc:   data.Description,
		WeatherIcon:   data.Icon,
	}

	// Basic validation
	if err := validateWeatherData(hourlyWeather); err != nil {
		return err
	}

	// Save hourly weather data
	if err := s.db.SaveHourlyWeather(hourlyWeather); err != nil {
		// Log the error before returning
		getLogger().Error("Failed to save hourly weather to database",
			logger.Error(err),
			logger.Time("time", hourlyWeather.Time))
		if s.metrics != nil {
			s.metrics.RecordWeatherDbError("save_hourly_weather", "database_error")
		}
		// Return the error directly as SaveHourlyWeather already wraps it properly
		return err
	}
	if s.metrics != nil {
		s.metrics.RecordWeatherDbOperation("save_hourly_weather", "success")
		// Update current weather gauges
		s.metrics.UpdateWeatherGauges(
			data.Temperature.Current,
			float64(data.Humidity),
			float64(data.Pressure),
			data.Wind.Speed,
			float64(data.Visibility),
		)
	}

	getLogger().Debug("Successfully saved weather data to database",
		logger.Time("time", utcTime),
		logger.String("city", data.Location.City))
	return nil
}

// absoluteZeroCelsius is the lowest possible temperature in Celsius
const absoluteZeroCelsius = -273.15

// validateWeatherData performs basic validation on weather data
func validateWeatherData(data *datastore.HourlyWeather) error {
	if data.Temperature < absoluteZeroCelsius {
		return errors.Newf("temperature cannot be below absolute zero: %f", data.Temperature).
			Component("weather").
			Category(errors.CategoryValidation).
			Context("temperature", fmt.Sprintf("%.2f", data.Temperature)).
			Build()
	}
	if data.WindSpeed < 0 {
		return errors.Newf("wind speed cannot be negative: %f", data.WindSpeed).
			Component("weather").
			Category(errors.CategoryValidation).
			Context("wind_speed", fmt.Sprintf("%.2f", data.WindSpeed)).
			Build()
	}
	return nil
}

// StartPolling starts the weather polling service
func (s *Service) StartPolling(stopChan <-chan struct{}) {
	interval := time.Duration(s.settings.Realtime.Weather.PollInterval) * time.Minute

	// Use the dedicated weather logger
	getLogger().Info("Starting weather polling service",
		logger.String("provider", s.settings.Realtime.Weather.Provider),
		logger.Int("interval_minutes", s.settings.Realtime.Weather.PollInterval))

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial fetch (errors logged within fetchAndSave)
	_ = s.fetchAndSave()

	for {
		select {
		case <-ticker.C:
			getLogger().Debug("Polling weather data...")
			// Errors logged within fetchAndSave
			_ = s.fetchAndSave()
		case <-stopChan:
			getLogger().Info("Stopping weather polling service")
			return
		}
	}
}

// Poll fetches weather data once and saves it to the database.
// This is useful for on-demand updates or testing the fetch-save cycle.
// Returns nil on success or if data is not modified (304 response).
func (s *Service) Poll() error {
	return s.fetchAndSave()
}

// fetchAndSave fetches weather data and saves it to the database
func (s *Service) fetchAndSave() error {
	// Track fetch duration
	fetchStart := time.Now()

	// FetchWeather should now internally log its start/end/errors
	data, err := s.provider.FetchWeather(s.settings)

	// Record fetch metrics
	if s.metrics != nil {
		s.metrics.RecordWeatherFetchDuration(s.settings.Realtime.Weather.Provider, time.Since(fetchStart).Seconds())
		if err != nil {
			s.metrics.RecordWeatherFetch(s.settings.Realtime.Weather.Provider, "error")
			s.metrics.RecordWeatherFetchError(s.settings.Realtime.Weather.Provider, "fetch_error")
		} else {
			s.metrics.RecordWeatherFetch(s.settings.Realtime.Weather.Provider, "success")
		}
	}

	if err != nil {
		// Handle "not modified" as a success case - no new data to save
		if errors.Is(err, ErrWeatherDataNotModified) {
			getLogger().Debug("Weather data not modified since last fetch",
				logger.String("provider", s.settings.Realtime.Weather.Provider))
			return nil // Not an error, just no new data
		}

		// Provider should log the specific error, we log the failure context here
		getLogger().Error("Failed to fetch weather data from provider",
			logger.String("provider", s.settings.Realtime.Weather.Provider),
			logger.Error(err))
		// Return the original error for upstream handling
		return errors.New(err).
			Component("weather").
			Category(errors.CategoryNetwork).
			Context("operation", "fetch_weather_data").
			Context("provider", s.settings.Realtime.Weather.Provider).
			Build()
	}

	// Convert to local time for logging. SaveWeatherData handles its own
	// timezone conversion for storage.
	localTimeForLog := data.Time.In(time.Local)

	getLogger().Info("Successfully fetched weather data",
		logger.String("provider", s.settings.Realtime.Weather.Provider),
		logger.String("time", localTimeForLog.Format("2006-01-02 15:04:05-07:00")),
		logger.Float64("temp_c", data.Temperature.Current),
		logger.Float64("wind_mps", data.Wind.Speed),
		logger.Int("humidity_pct", data.Humidity),
		logger.Int("pressure_hpa", data.Pressure),
		logger.String("description", data.Description),
		logger.String("city", data.Location.City))

	// Errors logged within SaveWeatherData
	return s.SaveWeatherData(data)
}
