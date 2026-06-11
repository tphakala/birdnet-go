package weather

import (
	"fmt"
	"net/url"
	"time"

	"github.com/tphakala/birdnet-go/internal/branding"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	RequestTimeout = 10 * time.Second
	RetryDelay     = 2 * time.Second
	MaxRetries     = 3
)

// UserAgent returns the HTTP User-Agent header value for outbound weather API
// requests, built from the configured project identity so forks identify
// themselves rather than the upstream project.
func UserAgent() string {
	return branding.Name() + " " + branding.RepoURL()
}

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

	// ErrWeatherDataNotModified indicates the data has not changed since
	// the last request (e.g., HTTP 304). Not an error condition.
	ErrWeatherDataNotModified = errors.Newf("weather data not modified").
					Component("weather").Category(errors.CategoryNotFound).Build()
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

// validateEndpointScheme ensures a user-configured custom weather endpoint is an
// absolute http(s) URL. url.Parse accepts a scheme-less value such as
// "api.example.com" as a relative path (empty Scheme and Host); the request
// would then fail later with an opaque "unsupported protocol scheme", so reject
// it here with a clear configuration error instead. The endpoint value is left
// out of the message so a misconfigured URL is not echoed into logs.
func validateEndpointScheme(u *url.URL, provider string) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return newWeatherError(
			fmt.Errorf("weather endpoint must be an absolute http(s) URL with a scheme (e.g. https://...)"),
			errors.CategoryConfiguration, "validate_endpoint_scheme", provider,
		)
	}
	if u.Host == "" {
		return newWeatherError(
			fmt.Errorf("weather endpoint must include a host"),
			errors.CategoryConfiguration, "validate_endpoint_host", provider,
		)
	}
	return nil
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
