package weather

import (
	"net/http"
	"time"
)

// DefaultHTTPClientTimeout is the default timeout for HTTP clients when not specified
const DefaultHTTPClientTimeout = 30 * time.Second

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
			Timeout: DefaultHTTPClientTimeout,
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
