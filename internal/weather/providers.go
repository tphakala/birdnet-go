package weather

import (
	"net/http"
	"sync"
)

// newDefaultHTTPClient builds the fallback HTTP client used when a provider is
// constructed without an injected client (e.g. directly in tests). It leaves
// Transport nil so it uses http.DefaultTransport, which keeps it interceptable
// by httpmock in tests, and bounds each request with RequestTimeout.
func newDefaultHTTPClient() *http.Client {
	return &http.Client{Timeout: RequestTimeout}
}

// NewYrNoProvider creates a new Yr.no weather provider with a shared HTTP client.
// A nil client falls back to a default client (see newDefaultHTTPClient).
func NewYrNoProvider(client *http.Client) Provider {
	if client == nil {
		client = newDefaultHTTPClient()
	}
	return &YrNoProvider{httpClient: client}
}

// NewOpenWeatherProvider creates a new OpenWeather provider with a shared HTTP
// client. A nil client falls back to a default client (see newDefaultHTTPClient).
func NewOpenWeatherProvider(client *http.Client) Provider {
	if client == nil {
		client = newDefaultHTTPClient()
	}
	return &OpenWeatherProvider{httpClient: client}
}

// NewWundergroundProvider creates a new WeatherUnderground provider with a shared
// HTTP client. A nil client falls back to a default client (see
// newDefaultHTTPClient).
func NewWundergroundProvider(client *http.Client) Provider {
	if client == nil {
		client = newDefaultHTTPClient()
	}
	return &WundergroundProvider{httpClient: client}
}

// Provider implementations
type YrNoProvider struct {
	httpClient   *http.Client
	mu           sync.Mutex
	lastModified string
}

// OpenWeatherProvider implements the Provider interface for OpenWeather.
type OpenWeatherProvider struct {
	httpClient *http.Client
}

// WundergroundProvider implements the Provider interface for WeatherUnderground
type WundergroundProvider struct {
	httpClient *http.Client
}
