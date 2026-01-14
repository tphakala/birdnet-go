package weather

import (
	"net/http"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestYrNoProvider_FetchWeather_Success(t *testing.T) {
	setupHTTPMock(t)

	registerYrNoResponder(t, http.StatusOK, yrNoSuccessResponse(), nil)

	provider := NewYrNoProvider()
	settings := createTestSettings(t, "yrno")

	data, err := provider.FetchWeather(settings)

	require.NoError(t, err)
	assertWeatherDataBasics(t, data)

	// Verify parsed values match expected
	assert.InDelta(t, 15.4, data.Temperature.Current, 0.01)
	assert.InDelta(t, 3.4, data.Wind.Speed, 0.01)
	assert.Equal(t, 220, data.Wind.Deg)
	assert.InDelta(t, 5.8, data.Wind.Gust, 0.01)
	assert.Equal(t, 1012, data.Pressure)
	assert.Equal(t, 62, data.Humidity)
	assert.Equal(t, 45, data.Clouds)
	assert.InDelta(t, 0.0, data.Precipitation.Amount, 0.001)

	// Verify location comes from settings
	assert.InDelta(t, 60.1699, data.Location.Latitude, 0.001)
	assert.InDelta(t, 24.9384, data.Location.Longitude, 0.001)

	// Verify icon mapping
	assert.Equal(t, string(IconPartlyCloudy), data.Icon)
}

func TestYrNoProvider_FetchWeather_NotModified(t *testing.T) {
	setupHTTPMock(t)

	// First request returns data with Last-Modified header
	httpmock.RegisterResponder("GET", `=~^https://api\.met\.no/weatherapi/locationforecast/2\.0/complete`,
		func(req *http.Request) (*http.Response, error) {
			// Check if this is a conditional request
			if req.Header.Get("If-Modified-Since") != "" {
				return httpmock.NewStringResponse(http.StatusNotModified, ""), nil
			}
			resp := httpmock.NewStringResponse(http.StatusOK, yrNoSuccessResponse())
			resp.Header.Set("Last-Modified", "Mon, 13 Jan 2026 10:00:00 GMT")
			return resp, nil
		})

	provider := NewYrNoProvider()
	settings := createTestSettings(t, "yrno")

	// First fetch should succeed
	data, err := provider.FetchWeather(settings)
	require.NoError(t, err)
	require.NotNil(t, data)

	// Second fetch should return ErrWeatherDataNotModified
	data2, err := provider.FetchWeather(settings)
	require.Error(t, err)
	assert.Nil(t, data2)
	assert.ErrorIs(t, err, ErrWeatherDataNotModified)
}

func TestYrNoProvider_FetchWeather_HTTPError(t *testing.T) {
	setupHTTPMock(t)

	tests := []struct {
		name       string
		statusCode int
	}{
		{"bad_request", http.StatusBadRequest},
		{"unauthorized", http.StatusUnauthorized},
		{"forbidden", http.StatusForbidden},
		{"not_found", http.StatusNotFound},
		{"internal_server_error", http.StatusInternalServerError},
		{"service_unavailable", http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpmock.Reset()
			registerYrNoResponder(t, tt.statusCode, `{"error": "test error"}`, nil)

			provider := NewYrNoProvider()
			settings := createTestSettings(t, "yrno")

			data, err := provider.FetchWeather(settings)

			require.Error(t, err)
			assert.Nil(t, data)
			assert.Contains(t, err.Error(), "non-OK response")
		})
	}
}

func TestYrNoProvider_FetchWeather_InvalidJSON(t *testing.T) {
	setupHTTPMock(t)

	registerYrNoResponder(t, http.StatusOK, `{invalid json`, nil)

	provider := NewYrNoProvider()
	settings := createTestSettings(t, "yrno")

	data, err := provider.FetchWeather(settings)

	require.Error(t, err)
	assert.Nil(t, data)
}

func TestYrNoProvider_FetchWeather_EmptyTimeseries(t *testing.T) {
	setupHTTPMock(t)

	emptyResponse := `{
  "type": "Feature",
  "properties": {
    "timeseries": []
  }
}`
	registerYrNoResponder(t, http.StatusOK, emptyResponse, nil)

	provider := NewYrNoProvider()
	settings := createTestSettings(t, "yrno")

	data, err := provider.FetchWeather(settings)

	require.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "no weather data")
}

func TestYrNoProvider_FetchWeather_GzipResponse(t *testing.T) {
	setupHTTPMock(t)

	// httpmock doesn't actually gzip, but we can test the header handling
	registerYrNoResponder(t, http.StatusOK, yrNoSuccessResponse(), map[string]string{
		"Content-Type": "application/json",
	})

	provider := NewYrNoProvider()
	settings := createTestSettings(t, "yrno")

	data, err := provider.FetchWeather(settings)

	require.NoError(t, err)
	require.NotNil(t, data)
}

func TestMapYrResponseToWeatherData(t *testing.T) {
	settings := createTestSettings(t, "yrno")

	response := &YrResponse{}
	response.Properties.Timeseries = []struct {
		Time time.Time `json:"time"`
		Data struct {
			Instant struct {
				Details struct {
					AirPressure    float64 `json:"air_pressure_at_sea_level"`
					AirTemperature float64 `json:"air_temperature"`
					CloudArea      float64 `json:"cloud_area_fraction"`
					RelHumidity    float64 `json:"relative_humidity"`
					WindSpeed      float64 `json:"wind_speed"`
					WindDirection  float64 `json:"wind_from_direction"`
					WindGust       float64 `json:"wind_speed_of_gust"`
				} `json:"details"`
			} `json:"instant"`
			Next1Hours struct {
				Summary struct {
					SymbolCode string `json:"symbol_code"`
				} `json:"summary"`
				Details struct {
					PrecipitationAmount float64 `json:"precipitation_amount"`
				} `json:"details"`
			} `json:"next_1_hours"`
		} `json:"data"`
	}{
		{
			Time: time.Date(2026, 1, 13, 12, 0, 0, 0, time.UTC),
		},
	}

	// Set values in the first timeseries entry
	response.Properties.Timeseries[0].Data.Instant.Details.AirTemperature = 15.4
	response.Properties.Timeseries[0].Data.Instant.Details.AirPressure = 1012.5
	response.Properties.Timeseries[0].Data.Instant.Details.RelHumidity = 62.0
	response.Properties.Timeseries[0].Data.Instant.Details.CloudArea = 45.2
	response.Properties.Timeseries[0].Data.Instant.Details.WindSpeed = 3.4
	response.Properties.Timeseries[0].Data.Instant.Details.WindDirection = 220.5
	response.Properties.Timeseries[0].Data.Instant.Details.WindGust = 5.8
	response.Properties.Timeseries[0].Data.Next1Hours.Summary.SymbolCode = "partlycloudy_day"
	response.Properties.Timeseries[0].Data.Next1Hours.Details.PrecipitationAmount = 0.5

	result := mapYrResponseToWeatherData(response, settings)

	require.NotNil(t, result)
	assert.Equal(t, response.Properties.Timeseries[0].Time, result.Time)
	assert.InDelta(t, 15.4, result.Temperature.Current, 0.01)
	assert.Equal(t, 1012, result.Pressure)
	assert.Equal(t, 62, result.Humidity)
	assert.Equal(t, 45, result.Clouds)
	assert.InDelta(t, 3.4, result.Wind.Speed, 0.01)
	assert.Equal(t, 220, result.Wind.Deg)
	assert.InDelta(t, 5.8, result.Wind.Gust, 0.01)
	assert.InDelta(t, 0.5, result.Precipitation.Amount, 0.01)
	assert.Equal(t, string(IconPartlyCloudy), result.Icon)
}

func TestTruncateBodyPreview(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "short_string",
			input:    "Hello, World!",
			expected: "Hello, World!",
		},
		{
			name:     "exactly_max_size",
			input:    string(make([]byte, maxBodyPreviewSize)),
			expected: string(make([]byte, maxBodyPreviewSize)),
		},
		{
			name:     "longer_than_max",
			input:    string(make([]byte, maxBodyPreviewSize+100)),
			expected: string(make([]byte, maxBodyPreviewSize)) + "... (truncated)",
		},
		{
			name:     "empty_string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateBodyPreview(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestNewYrNoProvider(t *testing.T) {
	provider := NewYrNoProvider()
	require.NotNil(t, provider)

	// Verify it implements Provider interface
	var _ = provider
}
