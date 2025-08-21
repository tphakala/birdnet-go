package weather

import (
	"net/http"
	"time"
)

// NewYrNoProvider creates a new Yr.no weather provider
func NewYrNoProvider() Provider {
	return &YrNoProvider{}
}

// NewOpenWeatherProvider creates a new OpenWeather provider
func NewOpenWeatherProvider() Provider {
	return &OpenWeatherProvider{}
}

// Provider implementations
type YrNoProvider struct {
	lastModified string
}
type OpenWeatherProvider struct{}

// NewWundergroundProvider creates a new WeatherUnderground provider with shared HTTP client
func NewWundergroundProvider(client *http.Client) Provider {
	if client == nil {
		client = &http.Client{
			Timeout: 30 * time.Second,
		}
	}
	return &WundergroundProvider{
		httpClient: client,
	}
}

// WundergroundProvider implements the Provider interface for WeatherUnderground
type WundergroundProvider struct {
	httpClient *http.Client
}
