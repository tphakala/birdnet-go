package weather

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/url"
	"testing"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// sentinelAPIKey is a recognizable, non-secret token used to prove that a
// configured API key never reaches logs or wrapped errors. It uses only
// URL-safe characters so it survives URL building unescaped, making any leak
// trivially detectable with a substring check.
const sentinelAPIKey = "SENTINELKEY0123456789DONOTLEAK"

// captureWeatherLogs redirects the global logger to an in-memory buffer for the
// duration of the test and returns the buffer. The weather package logs through
// logger.Global().Module("weather"), so this captures everything the providers
// emit while a test runs. It swaps process-wide global state, so tests using it
// must not run with t.Parallel().
func captureWeatherLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	capture := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	cl, err := logger.NewCentralLogger(
		&logger.LoggingConfig{
			Console:      &logger.ConsoleOutput{Enabled: false},
			FileOutput:   &logger.FileOutput{Enabled: false},
			DefaultLevel: "debug",
		},
		capture,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cl.Close() })

	prev := logger.Global()
	logger.SetGlobal(cl)
	t.Cleanup(func() { logger.SetGlobal(prev) })

	return &buf
}

// TestOpenWeatherProvider_TransportError_DoesNotLeakAPIKey is a regression guard
// for the API-key-in-logs vulnerability. net/http wraps transport failures in a
// *url.Error whose Error() embeds the full request URL, including the appid
// query parameter. The provider must scrub that error before logging it and
// before returning it, or the key lands in application logs and uploaded
// support dumps.
func TestOpenWeatherProvider_TransportError_DoesNotLeakAPIKey(t *testing.T) {
	setupHTTPMock(t)
	logs := captureWeatherLogs(t)

	// Force every attempt's client.Do to fail at the transport layer so the
	// provider exercises its failure-log and error-wrap paths.
	httpmock.RegisterResponder("GET", `=~^https://api\.openweathermap\.org/data/2\.5/weather`,
		httpmock.NewErrorResponder(errors.NewStd("simulated transport failure")))

	provider := NewOpenWeatherProvider()
	settings := createTestSettings(t, "openweather", func(s *conf.Settings) {
		s.Realtime.Weather.OpenWeather.APIKey = sentinelAPIKey
	})

	data, err := provider.FetchWeather(settings)

	require.Error(t, err)
	assert.Nil(t, data)
	assert.NotContains(t, err.Error(), sentinelAPIKey, "returned error must not embed the API key")
	assert.NotContains(t, logs.String(), sentinelAPIKey, "logs must not embed the API key")
}

// TestWundergroundProvider_TransportError_DoesNotLeakAPIKey is a regression
// guard for the Wunderground transport-failure path. The raw *url.Error carries
// the apiKey query parameter; the wrapped error returned upstream must be
// scrubbed so a plain logger.Error in weather.go cannot leak it.
func TestWundergroundProvider_TransportError_DoesNotLeakAPIKey(t *testing.T) {
	setupHTTPMock(t)
	logs := captureWeatherLogs(t)

	httpmock.RegisterResponder("GET", `=~^https://api\.weather\.com/v2/pws/observations/current`,
		httpmock.NewErrorResponder(errors.NewStd("simulated transport failure")))

	provider := NewWundergroundProvider(nil)
	settings := createTestSettings(t, "wunderground", func(s *conf.Settings) {
		s.Realtime.Weather.Wunderground.APIKey = sentinelAPIKey
	})

	data, err := provider.FetchWeather(settings)

	require.Error(t, err)
	assert.Nil(t, data)
	assert.NotContains(t, err.Error(), sentinelAPIKey, "returned error must not embed the API key")
	assert.NotContains(t, logs.String(), sentinelAPIKey, "logs must not embed the API key")
}

// TestMaskAPIKey_FailsClosedOnParseError verifies that maskAPIKey never returns
// the raw URL when url.Parse fails. The raw URL embeds the API key, so the
// previous behavior of returning it on a parse error leaked the key at the Info
// log that masks the fetch URL.
func TestMaskAPIKey_FailsClosedOnParseError(t *testing.T) {
	// A raw DEL control character (0x7f) makes url.Parse fail.
	rawURL := "https://api.example.com/path\x7f?appid=" + sentinelAPIKey

	got := maskAPIKey(rawURL, "appid")

	assert.NotContains(t, got, sentinelAPIKey, "a parse failure must not return the raw URL with the key")
	assert.Equal(t, maskedURLOnError, got, "a parse failure must return the fail-closed placeholder")
}

// TestBuildOpenWeatherURL_EscapesAPIKey verifies the OpenWeather request URL is
// built with url.Values so a key containing characters that require escaping
// produces a well-formed URL rather than the malformed string fmt.Sprintf would
// emit (which http.NewRequest rejects, leaking the raw key in the *url.Error).
func TestBuildOpenWeatherURL_EscapesAPIKey(t *testing.T) {
	settings := createTestSettings(t, "openweather")
	keyWithSpecials := "key with spaces&appid=injected/+"

	rawURL, err := buildOpenWeatherURL(settings, keyWithSpecials)
	require.NoError(t, err)

	// The built URL must be well-formed (this is what fmt.Sprintf failed to guarantee).
	parsed, parseErr := url.Parse(rawURL)
	require.NoError(t, parseErr, "built URL must be parseable")

	// The raw key must be escaped, not embedded verbatim.
	assert.NotContains(t, rawURL, keyWithSpecials, "the API key must be escaped in the query string")

	// Round-trip: decoding the query yields the original key, and only one appid.
	assert.Equal(t, keyWithSpecials, parsed.Query().Get("appid"), "appid must round-trip through escaping")

	// maskAPIKey must mask the (now valid) URL rather than falling back to the
	// parse-failure placeholder.
	masked := maskAPIKey(rawURL, "appid")
	assert.NotEqual(t, maskedURLOnError, masked, "a well-formed URL must not hit the fail-closed path")
	assert.NotContains(t, masked, "key with spaces", "the masked URL must not reveal the key")
}

// TestOpenWeatherProvider_HTTP401_AuthFailed guards the sentinel classification
// that drives the auth-disable and backoff logic: a 401 from OpenWeather must
// map to ErrWeatherAuthFailed, mirroring the Wunderground 401 guard.
func TestOpenWeatherProvider_HTTP401_AuthFailed(t *testing.T) {
	setupHTTPMock(t)

	registerOpenWeatherResponder(t, http.StatusUnauthorized, `{"cod": 401, "message": "Invalid API key"}`)

	provider := NewOpenWeatherProvider()
	settings := createTestSettings(t, "openweather")

	data, err := provider.FetchWeather(settings)

	require.Error(t, err)
	assert.Nil(t, data)
	assert.ErrorIs(t, err, ErrWeatherAuthFailed, "HTTP 401 must classify as the auth-failed sentinel")
}
