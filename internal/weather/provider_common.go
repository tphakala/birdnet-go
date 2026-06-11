package weather

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/tphakala/birdnet-go/internal/branding"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
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

// redactedValue replaces sensitive query-parameter values in log-safe URLs.
const redactedValue = "***MASKED***"

// maskedURLOnError is returned by maskURLForLog when the URL cannot be parsed.
// The raw URL embeds the API key and coordinates, so it is never returned on a
// parse failure; this fully redacted placeholder is returned instead (fail
// closed).
const maskedURLOnError = "[unparseable-url-redacted]"

// sensitiveQueryParams lists the query parameters that must be redacted before a
// weather API request URL is logged. The key parameters (appid for OpenWeather,
// apiKey for Wunderground) authenticate the account, and the coordinates
// (lat/lon, used by OpenWeather and yr.no) are PII that reveal the user's
// location, so neither may reach a log sink in cleartext.
var sensitiveQueryParams = []string{"appid", "apiKey", "lat", "lon"}

// maskURLForLog returns a log-safe representation of a weather provider request
// URL. It redacts the API key and the location coordinates while preserving
// non-sensitive parameters (units, format, ...) that aid debugging. On a parse
// failure it fails closed, returning a fully redacted placeholder rather than
// risk echoing the raw URL, which embeds the key and coordinates.
func maskURLForLog(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return maskedURLOnError
	}
	q := parsed.Query()
	for _, p := range sensitiveQueryParams {
		if q.Has(p) {
			q.Set(p, redactedValue)
		}
	}
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

// sleepWithContext waits for d to elapse or for ctx to be cancelled, whichever
// happens first, returning ctx.Err() on cancellation. Using it in place of
// time.Sleep lets a shutdown abort an in-progress retry backoff instead of
// blocking for the full delay. time.After is leak-free on Go 1.23+ (an
// unreferenced timer is garbage collected even if it has not fired), so it is
// preferred here over the more verbose time.NewTimer/Stop dance.
func sleepWithContext(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// weatherResponseHandler processes a single HTTP response inside the shared
// retry loop. It returns the response body on success. retry==true asks the
// executor to back off and try again (a transient failure on a non-final
// attempt). A non-nil err is returned to the caller verbatim, so a handler can
// surface sentinel errors (e.g. ErrWeatherAuthFailed, ErrWeatherDataNotModified)
// that errors.Is must still match upstream. The handler owns closing resp.Body.
type weatherResponseHandler func(resp *http.Response, attemptLog logger.Logger, isLastAttempt bool) (body []byte, retry bool, err error)

// executeWeatherRequest runs req through up to MaxRetries attempts with the
// injected client, delegating per-status handling to handle. It centralizes the
// retry loop, the transport-error scrubbing, the context-aware backoff, and the
// "max retries exceeded" terminal error that the yr.no and OpenWeather providers
// previously duplicated. The same request is reused across attempts, which is
// safe for the GET/NoBody weather requests: http.Client.Timeout is applied fresh
// on every Do, and no request body needs replaying. A cancelled context is
// returned verbatim (not wrapped or retried) so callers can treat shutdown as
// benign rather than a provider failure.
func executeWeatherRequest(ctx context.Context, client *http.Client, req *http.Request, provider string, log logger.Logger, handle weatherResponseHandler) ([]byte, error) {
	for i := range MaxRetries {
		isLastAttempt := i == MaxRetries-1
		attemptLogger := log.With(
			logger.Int("attempt", i+1),
			logger.Int("max_attempts", MaxRetries))

		resp, err := client.Do(req) //nolint:bodyclose // resp.Body is closed by the handle callback on every path; bodyclose cannot trace the close through the function pointer.
		if err != nil {
			// A cancelled context means shutdown (or an aborted on-demand
			// request), not a provider failure: surface it directly so callers
			// skip the failure/backoff bookkeeping.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// Scrub before logging and wrapping: net/http wraps transport
			// failures in a *url.Error whose Error() embeds the request URL,
			// which may carry the API key or the user's coordinates.
			attemptLogger.Warn("HTTP request failed", logger.SanitizedError(err))
			if isLastAttempt {
				return nil, newWeatherErrorWithRetries(privacy.WrapError(err), errors.CategoryNetwork, "weather_api_request", provider)
			}
			if serr := sleepWithContext(ctx, RetryDelay); serr != nil {
				return nil, serr
			}
			continue
		}

		attemptLogger.Debug("Received HTTP response", logger.Int("status_code", resp.StatusCode))

		body, retry, err := handle(resp, attemptLogger, isLastAttempt)
		if err != nil {
			return nil, err
		}
		if retry {
			if serr := sleepWithContext(ctx, RetryDelay); serr != nil {
				return nil, serr
			}
			continue
		}
		return body, nil
	}

	return nil, newWeatherErrorWithRetries(
		fmt.Errorf("max retries exceeded"),
		errors.CategoryNetwork,
		"weather_api_request",
		provider,
	)
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
