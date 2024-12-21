package weather

import (
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

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
	if settings.Realtime.Weather.YrNo.Enabled {
		provider = NewYrNoProvider()
	} else if settings.Realtime.Weather.OpenWeather.Enabled {
		provider = NewOpenWeatherProvider()
	} else {
		return nil, fmt.Errorf("no weather provider enabled in configuration")
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
		return fmt.Errorf("failed to save hourly weather: %w", err)
	}

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
	var interval time.Duration
	if s.settings.Realtime.Weather.YrNo.Enabled {
		interval = time.Duration(s.settings.Realtime.Weather.YrNo.Interval) * time.Minute
	} else {
		interval = time.Duration(s.settings.Realtime.Weather.OpenWeather.Interval) * time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial fetch
	if err := s.fetchAndSave(); err != nil {
		fmt.Printf("Initial weather fetch failed: %v\n", err)
	}

	for {
		select {
		case <-ticker.C:
			if err := s.fetchAndSave(); err != nil {
				fmt.Printf("Weather fetch failed: %v\n", err)
			}
		case <-stopChan:
			return
		}
	}
}

// fetchAndSave fetches weather data and saves it to the database
func (s *Service) fetchAndSave() error {
	data, err := s.provider.FetchWeather(s.settings)
	if err != nil {
		return fmt.Errorf("failed to fetch weather data: %w", err)
	}

	if err := s.SaveWeatherData(data); err != nil {
		return fmt.Errorf("failed to save weather data: %w", err)
	}

	return nil
}
