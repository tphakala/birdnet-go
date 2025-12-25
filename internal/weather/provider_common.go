package weather

import (
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	RequestTimeout = 10 * time.Second
	UserAgent      = "BirdNET-Go https://github.com/tphakala/birdnet-go"
	RetryDelay     = 2 * time.Second
	MaxRetries     = 3
)

// newWeatherError creates a standardized weather error with common fields
func newWeatherError(err error, category errors.ErrorCategory, operation, provider string) error {
	return errors.New(err).
		Component("weather").
		Category(category).
		Context("operation", operation).
		Context("provider", provider).
		Build()
}

// newWeatherErrorWithRetries creates a weather error that includes retry information
func newWeatherErrorWithRetries(err error, category errors.ErrorCategory, operation, provider string) error {
	return errors.New(err).
		Component("weather").
		Category(category).
		Context("operation", operation).
		Context("provider", provider).
		Context("max_retries", fmt.Sprintf("%d", MaxRetries)).
		Build()
}

// Temperature conversion constants
const (
	celsiusToFahrenheitScale  = 9.0 / 5.0
	celsiusToFahrenheitOffset = 32.0
	kelvinOffset              = 273.15
)

// FahrenheitToCelsius converts a temperature from Fahrenheit to Celsius.
func FahrenheitToCelsius(f float64) float64 {
	return (f - celsiusToFahrenheitOffset) / celsiusToFahrenheitScale
}

// KelvinToCelsius converts a temperature from Kelvin to Celsius.
func KelvinToCelsius(k float64) float64 {
	return k - kelvinOffset
}
