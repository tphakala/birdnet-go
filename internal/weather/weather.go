package weather

import (
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// Package-level logger for weather service
var (
	weatherLogger   *slog.Logger
	weatherLevelVar = new(slog.LevelVar) // Dynamic level control
	// weatherLogCloser func() error // Closer function for the file logger
	// TODO: Call weatherLogCloser during graceful shutdown
)

func init() {
	var err error
	initialLevel := slog.LevelDebug // Set desired initial level here
	weatherLevelVar.Set(initialLevel)

	// Default level is Info, adjust if needed or read from config
	weatherLogger, _, err = logging.NewFileLogger("logs/weather.log", "weather", weatherLevelVar)
	if err != nil {
		// Fallback or handle error appropriately
		// Using the global logger for this critical setup error
		logging.Error("Failed to initialize weather file logger", "error", err)
		// Fallback to a disabled logger (writes to io.Discard) but respects the level var
		fbHandler := slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: weatherLevelVar})
		weatherLogger = slog.New(fbHandler).With("service", "weather")
		logging.Warn("Weather service falling back to default logger due to file logger initialization error.")
	}
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
	default:
		return nil, errors.New(fmt.Errorf("invalid weather provider: %s", settings.Realtime.Weather.Provider)).
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

	// Create daily events data
	dailyEvents := &datastore.DailyEvents{
		Date:     data.Time.Format("2006-01-02"),
		Country:  data.Location.Country,
		CityName: data.Location.City,
	}

	// Save daily events data
	if err := s.db.SaveDailyEvents(dailyEvents); err != nil {
		// Log the error before returning
		weatherLogger.Error("Failed to save daily events to database", "error", err, "date", dailyEvents.Date, "city", dailyEvents.CityName)
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
		Time:          data.Time,
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
		weatherLogger.Error("Failed to save hourly weather to database", "error", err, "time", hourlyWeather.Time)
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

	weatherLogger.Debug("Successfully saved weather data to database", "time", data.Time, "city", data.Location.City)
	return nil
}

// validateWeatherData performs basic validation on weather data
func validateWeatherData(data *datastore.HourlyWeather) error {
	if data.Temperature < -273.15 {
		return errors.New(fmt.Errorf("temperature cannot be below absolute zero: %f", data.Temperature)).
			Component("weather").
			Category(errors.CategoryValidation).
			Context("temperature", fmt.Sprintf("%.2f", data.Temperature)).
			Build()
	}
	if data.WindSpeed < 0 {
		return errors.New(fmt.Errorf("wind speed cannot be negative: %f", data.WindSpeed)).
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
	weatherLogger.Info("Starting weather polling service",
		"provider", s.settings.Realtime.Weather.Provider,
		"interval_minutes", s.settings.Realtime.Weather.PollInterval,
	)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial fetch
	if err := s.fetchAndSave(); err != nil {
		// Error is already logged within fetchAndSave
		weatherLogger.Warn("Initial weather fetch failed", "error", err)
	}

	for {
		select {
		case <-ticker.C:
			weatherLogger.Info("Polling weather data...")
			if err := s.fetchAndSave(); err != nil {
				// Error is logged within fetchAndSave, maybe just warn here?
				weatherLogger.Warn("Weather fetch poll failed", "error", err)
			}
		case <-stopChan:
			weatherLogger.Info("Stopping weather polling service")
			return
		}
	}
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
		// Provider should log the specific error, we log the failure context here
		weatherLogger.Error("Failed to fetch weather data from provider",
			"provider", s.settings.Realtime.Weather.Provider,
			"error", err, // Keep the wrapped error message
		)
		// Return the original error for upstream handling
		return errors.New(err).
			Component("weather").
			Category(errors.CategoryNetwork).
			Context("operation", "fetch_weather_data").
			Context("provider", s.settings.Realtime.Weather.Provider).
			Build()
	}

	// Log successful fetch details using the weatherLogger
	weatherLogger.Info("Successfully fetched weather data",
		"provider", s.settings.Realtime.Weather.Provider,
		"time", data.Time.Format("2006-01-02 15:04:05"),
		"temp_c", data.Temperature.Current,
		"wind_mps", data.Wind.Speed,
		"humidity_pct", data.Humidity,
		"pressure_hpa", data.Pressure,
		"description", data.Description,
		"city", data.Location.City,
	)

	if err := s.SaveWeatherData(data); err != nil {
		// Error is logged within SaveWeatherData
		weatherLogger.Error("Failed to save fetched weather data", "error", err)
		// Return the original error from SaveWeatherData
		return err // No need to wrap again, SaveWeatherData already logs context
	}

	return nil
}
