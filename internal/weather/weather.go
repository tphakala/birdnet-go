package weather

import (
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logging"
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
		logging.Warn("Weather service falling back to a disabled logger due to initialization error.")
		fbHandler := slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: weatherLevelVar})
		weatherLogger = slog.New(fbHandler).With("service", "weather")

		if weatherLogger == nil { // This check is likely redundant now, but harmless
			// If even the default logger isn't initialized, panic is reasonable
			// as logging is fundamental.
			panic(fmt.Sprintf("Failed to initialize any logger for weather service: %v", err))
		}
		logging.Warn("Weather service falling back to default logger due to file logger initialization error.")
	} else {
		logging.Info("Weather file logger initialized successfully", "path", "logs/weather.log")
	}

	// Store the closer function if you have a mechanism to call it on shutdown
	// weatherLogCloser = closer
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
func NewService(settings *conf.Settings, db datastore.Interface) (*Service, error) {
	var provider Provider

	// Select weather provider based on configuration
	switch settings.Realtime.Weather.Provider {
	case "yrno":
		provider = NewYrNoProvider()
	case "openweather":
		provider = NewOpenWeatherProvider()
	default:
		return nil, fmt.Errorf("invalid weather provider: %s", settings.Realtime.Weather.Provider)
	}

	return &Service{
		provider: provider,
		db:       db,
		settings: settings,
	}, nil
}

// SaveWeatherData saves the weather data to the database
func (s *Service) SaveWeatherData(data *WeatherData) error {
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
		return fmt.Errorf("failed to save daily events: %w", err)
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
		return fmt.Errorf("failed to save hourly weather: %w", err)
	}

	weatherLogger.Debug("Successfully saved weather data to database", "time", data.Time, "city", data.Location.City)
	return nil
}

// validateWeatherData performs basic validation on weather data
func validateWeatherData(data *datastore.HourlyWeather) error {
	if data.Temperature < -273.15 {
		return fmt.Errorf("temperature cannot be below absolute zero: %f", data.Temperature)
	}
	if data.WindSpeed < 0 {
		return fmt.Errorf("wind speed cannot be negative: %f", data.WindSpeed)
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
	// FetchWeather should now internally log its start/end/errors
	data, err := s.provider.FetchWeather(s.settings)
	if err != nil {
		// Provider should log the specific error, we log the failure context here
		weatherLogger.Error("Failed to fetch weather data from provider",
			"provider", s.settings.Realtime.Weather.Provider,
			"error", err, // Keep the wrapped error message
		)
		// Return the original error for upstream handling
		return fmt.Errorf("failed to fetch weather data: %w", err)
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
