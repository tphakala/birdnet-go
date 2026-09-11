package weather

import (
	"net/http"
	"testing"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// TestBuildPirateWeatherURL_RejectsSchemelessEndpoint mirrors the OpenWeather/
// Wunderground guard: a misconfigured custom endpoint that omits the scheme
// must be rejected up front with a configuration error rather than failing
// later with an opaque "unsupported protocol scheme".
func TestBuildPirateWeatherURL_RejectsSchemelessEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
	}{
		{"no_scheme", "api.custom-pirateweather.org"},
		{"no_scheme_with_path", "api.custom-pirateweather.org/forecast"},
		{"scheme_without_host", "https://"},
		{"non_http_scheme", "ftp://api.custom-pirateweather.org"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := createTestSettings(t, "pirateweather", func(s *conf.Settings) {
				s.Realtime.Weather.PirateWeather.Endpoint = tt.endpoint
			})

			_, err := buildPirateWeatherURL(settings, "test-key")

			require.Error(t, err)
			var ee *errors.EnhancedError
			require.ErrorAs(t, err, &ee)
			assert.Equal(t, string(errors.CategoryConfiguration), ee.GetCategory(),
				"a malformed endpoint must classify as a configuration error")
		})
	}
}

// TestBuildPirateWeatherURL_KeyAndCoordsInPath verifies the API key and
// coordinates are placed in the URL path (not the query string), matching
// Pirate Weather's Dark-Sky-style /forecast/{apikey}/{lat},{lon} format.
func TestBuildPirateWeatherURL_KeyAndCoordsInPath(t *testing.T) {
	settings := createTestSettings(t, "pirateweather")

	apiURL, err := buildPirateWeatherURL(settings, "my-secret-key")

	require.NoError(t, err)
	assert.Contains(t, apiURL, "/forecast/my-secret-key/60.170,24.938")
	assert.Contains(t, apiURL, "units=si")
	assert.NotContains(t, apiURL, "appid=", "API key must not also appear as a query parameter")
}

func TestMaskPirateWeatherURL_RedactsPath(t *testing.T) {
	masked := maskPirateWeatherURL("https://api.pirateweather.net/forecast/my-secret-key/60.170,24.938?units=si")

	assert.NotContains(t, masked, "my-secret-key")
	assert.NotContains(t, masked, "60.170")
	assert.Contains(t, masked, "https://api.pirateweather.net")
}

func TestPirateWeatherProvider_FetchWeather_Success(t *testing.T) {
	setupHTTPMock(t)

	registerPirateWeatherResponder(t, http.StatusOK, pirateWeatherSuccessResponse())

	provider := NewPirateWeatherProvider(nil)
	settings := createTestSettings(t, "pirateweather")

	data, err := provider.FetchWeather(t.Context(), settings)

	require.NoError(t, err)
	assertWeatherDataBasics(t, data)

	assert.InDelta(t, 14.55, data.Temperature.Current, 0.01)
	assert.InDelta(t, 13.88, data.Temperature.FeelsLike, 0.01)
	assert.InDelta(t, 4.12, data.Wind.Speed, 0.01)
	assert.Equal(t, 240, data.Wind.Deg)
	assert.InDelta(t, 7.5, data.Wind.Gust, 0.01)
	assert.Equal(t, 1014, data.Pressure)
	assert.Equal(t, 72, data.Humidity)      // 0.72 -> 72%
	assert.Equal(t, 75, data.Clouds)        // 0.75 -> 75%
	assert.Equal(t, 10000, data.Visibility) // 10.0km -> 10000m

	assert.InDelta(t, 60.1699, data.Location.Latitude, 0.001)
	assert.InDelta(t, 24.9384, data.Location.Longitude, 0.001)

	assert.Equal(t, "Partly Cloudy", data.Description)
	assert.Equal(t, string(IconPartlyCloudy), data.Icon)
	assert.Equal(t, "Clouds", data.WeatherMain)
	assert.Empty(t, data.Precipitation.Type, "precipType \"none\" must map to an empty string")
}

func TestPirateWeatherProvider_FetchWeather_NoAPIKey(t *testing.T) {
	provider := NewPirateWeatherProvider(nil)
	settings := createTestSettings(t, "pirateweather", func(s *conf.Settings) {
		s.Realtime.Weather.PirateWeather.APIKey = ""
	})

	data, err := provider.FetchWeather(t.Context(), settings)

	require.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "API key not configured")
}

// TestPirateWeatherProvider_FetchWeather_Unauthorized_ReturnsSentinel guards the
// 401 classification: an HTTP 401 must map to the ErrWeatherAuthFailed sentinel
// and must NOT be retried.
func TestPirateWeatherProvider_FetchWeather_Unauthorized_ReturnsSentinel(t *testing.T) {
	setupHTTPMock(t)

	callCount := 0
	httpmock.RegisterResponder("GET", `=~^https://api\.pirateweather\.net/forecast/`,
		func(_ *http.Request) (*http.Response, error) {
			callCount++
			return httpmock.NewStringResponse(http.StatusUnauthorized, `{"error": true, "message": "invalid key"}`), nil
		})

	provider := NewPirateWeatherProvider(nil)
	settings := createTestSettings(t, "pirateweather")

	data, err := provider.FetchWeather(t.Context(), settings)

	require.Error(t, err)
	assert.Nil(t, data)
	require.ErrorIs(t, err, ErrWeatherAuthFailed, "401 must map to the auth-failed sentinel")
	assert.Equal(t, 1, callCount, "a 401 must not be retried")
}

// TestPirateWeatherProvider_FetchWeather_Forbidden_ReturnsSentinel guards the
// 403 classification, mirroring the 401 sentinel test above.
func TestPirateWeatherProvider_FetchWeather_Forbidden_ReturnsSentinel(t *testing.T) {
	setupHTTPMock(t)

	callCount := 0
	httpmock.RegisterResponder("GET", `=~^https://api\.pirateweather\.net/forecast/`,
		func(_ *http.Request) (*http.Response, error) {
			callCount++
			return httpmock.NewStringResponse(http.StatusForbidden, `{"error": true, "message": "forbidden"}`), nil
		})

	provider := NewPirateWeatherProvider(nil)
	settings := createTestSettings(t, "pirateweather")

	data, err := provider.FetchWeather(t.Context(), settings)

	require.Error(t, err)
	assert.Nil(t, data)
	require.ErrorIs(t, err, ErrWeatherAuthFailed, "403 must map to the auth-failed sentinel")
	assert.Equal(t, 1, callCount, "a 403 must not be retried")
}

func TestPirateWeatherProvider_FetchWeather_InvalidJSON(t *testing.T) {
	setupHTTPMock(t)

	registerPirateWeatherResponder(t, http.StatusOK, `{invalid json`)

	provider := NewPirateWeatherProvider(nil)
	settings := createTestSettings(t, "pirateweather")

	data, err := provider.FetchWeather(t.Context(), settings)

	require.Error(t, err)
	assert.Nil(t, data)
}

func TestMapPirateWeatherResponse_PrecipitationPresent(t *testing.T) {
	data := &PirateWeatherResponse{Latitude: 1, Longitude: 2}
	data.Currently.Time = 1736769600
	data.Currently.Icon = "snow"
	data.Currently.Summary = "Snow"
	data.Currently.PrecipIntensity = 2.5
	data.Currently.PrecipType = "snow"

	mapped := mapPirateWeatherResponse(data)

	assert.InDelta(t, 2.5, mapped.Precipitation.Amount, 0.001)
	assert.Equal(t, "snow", mapped.Precipitation.Type)
	assert.Equal(t, string(IconSnow), mapped.Icon)
}
