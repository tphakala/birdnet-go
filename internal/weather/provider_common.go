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

// Sentinel errors for weather API responses
var (
	// ErrWeatherAuthFailed indicates the API returned HTTP 401 (unauthorized).
	// This typically means the API key is invalid or expired.
	ErrWeatherAuthFailed = errors.Newf("weather API authentication failed").
				Component("weather").Category(errors.CategoryConfiguration).Build()

	// ErrWeatherNoData indicates the API returned HTTP 204 (no content).
	// The station exists but has no current data available.
	ErrWeatherNoData = errors.Newf("weather station has no data available").
				Component("weather").Category(errors.CategoryNotFound).Build()

	// ErrWeatherDisabled indicates that weather functionality is disabled
	// because the provider is empty or unrecognized. This is an expected
	// configuration state, not a bug, so it must not be reported to Sentry.
	ErrWeatherDisabled = errors.Newf("weather service disabled").
				Component("weather").Category(errors.CategoryConfiguration).Build()
)

// Backoff constants for the polling service
const (
	// maxConsecutiveAuthFailures is the number of consecutive 401 errors
	// before the service stops retrying and requires a config change.
	maxConsecutiveAuthFailures = 3

	// initialBackoffDuration is the starting backoff delay after the first failure.
	initialBackoffDuration = 1 * time.Minute

	// maxBackoffDuration caps the exponential backoff.
	maxBackoffDuration = 1 * time.Hour

	// backoffMultiplier is the factor by which backoff increases after each failure.
	backoffMultiplier = 2
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
