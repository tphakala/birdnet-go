package weather

// NewYrNoProvider creates a new Yr.no weather provider
func NewYrNoProvider() Provider {
	return &YrNoProvider{}
}

// NewOpenWeatherProvider creates a new OpenWeather provider
func NewOpenWeatherProvider() Provider {
	return &OpenWeatherProvider{}
}

// Provider implementations (moved from their respective packages)
type YrNoProvider struct{}
type OpenWeatherProvider struct{}
