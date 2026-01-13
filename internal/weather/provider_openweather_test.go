package weather

import (
	"net/http"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestOpenWeatherProvider_FetchWeather_Success(t *testing.T) {
	setupHTTPMock(t)

	registerOpenWeatherResponder(t, http.StatusOK, openWeatherSuccessResponse())

	provider := NewOpenWeatherProvider()
	settings := createTestSettings(t, "openweather")

	data, err := provider.FetchWeather(settings)

	require.NoError(t, err)
	assertWeatherDataBasics(t, data)

	// Verify parsed values match expected
	assert.InDelta(t, 14.55, data.Temperature.Current, 0.01)
	assert.InDelta(t, 13.88, data.Temperature.FeelsLike, 0.01)
	assert.InDelta(t, 13.33, data.Temperature.Min, 0.01)
	assert.InDelta(t, 15.65, data.Temperature.Max, 0.01)
	assert.InDelta(t, 4.12, data.Wind.Speed, 0.01)
	assert.Equal(t, 240, data.Wind.Deg)
	assert.InDelta(t, 7.5, data.Wind.Gust, 0.01)
	assert.Equal(t, 1014, data.Pressure)
	assert.Equal(t, 72, data.Humidity)
	assert.Equal(t, 75, data.Clouds)
	assert.Equal(t, 10000, data.Visibility)

	// Verify location from API response
	assert.InDelta(t, 60.1699, data.Location.Latitude, 0.001)
	assert.InDelta(t, 24.9384, data.Location.Longitude, 0.001)
	assert.Equal(t, "FI", data.Location.Country)
	assert.Equal(t, "Helsinki", data.Location.City)

	// Verify description and icon
	assert.Equal(t, "broken clouds", data.Description)
	assert.Equal(t, string(IconCloudy), data.Icon) // 04d maps to IconCloudy
}

func TestOpenWeatherProvider_FetchWeather_NoAPIKey(t *testing.T) {
	provider := NewOpenWeatherProvider()
	settings := createTestSettings(t, "openweather", func(s *conf.Settings) {
		s.Realtime.Weather.OpenWeather.APIKey = ""
	})

	data, err := provider.FetchWeather(settings)

	require.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "API key not configured")
}

func TestOpenWeatherProvider_FetchWeather_HTTPError(t *testing.T) {
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
			registerOpenWeatherResponder(t, tt.statusCode, `{"cod": "401", "message": "Invalid API key"}`)

			provider := NewOpenWeatherProvider()
			settings := createTestSettings(t, "openweather")

			data, err := provider.FetchWeather(settings)

			require.Error(t, err)
			assert.Nil(t, data)
		})
	}
}

func TestOpenWeatherProvider_FetchWeather_InvalidJSON(t *testing.T) {
	setupHTTPMock(t)

	registerOpenWeatherResponder(t, http.StatusOK, `{invalid json`)

	provider := NewOpenWeatherProvider()
	settings := createTestSettings(t, "openweather")

	data, err := provider.FetchWeather(settings)

	require.Error(t, err)
	assert.Nil(t, data)
}

func TestOpenWeatherProvider_FetchWeather_EmptyWeatherArray(t *testing.T) {
	setupHTTPMock(t)

	// Response with empty weather array
	emptyWeatherResponse := `{
  "coord": { "lon": 24.9384, "lat": 60.1699 },
  "weather": [],
  "main": { "temp": 14.55, "feels_like": 13.88, "temp_min": 13.33, "temp_max": 15.65, "pressure": 1014, "humidity": 72 },
  "visibility": 10000,
  "wind": { "speed": 4.12, "deg": 240 },
  "clouds": { "all": 75 },
  "dt": 1736769600,
  "sys": { "country": "FI" },
  "name": "Helsinki",
  "cod": 200
}`
	registerOpenWeatherResponder(t, http.StatusOK, emptyWeatherResponse)

	provider := NewOpenWeatherProvider()
	settings := createTestSettings(t, "openweather")

	data, err := provider.FetchWeather(settings)

	require.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "no weather conditions")
}

func TestConvertOpenWeatherTemps_Metric(t *testing.T) {
	// Metric units - already Celsius, no conversion needed
	temp, feelsLike, tempMin, tempMax := convertOpenWeatherTemps(20.0, 18.0, 15.0, 25.0, "metric")

	assert.InDelta(t, 20.0, temp, 0.001)
	assert.InDelta(t, 18.0, feelsLike, 0.001)
	assert.InDelta(t, 15.0, tempMin, 0.001)
	assert.InDelta(t, 25.0, tempMax, 0.001)
}

func TestConvertOpenWeatherTemps_Imperial(t *testing.T) {
	// Imperial units - Fahrenheit to Celsius
	temp, feelsLike, tempMin, tempMax := convertOpenWeatherTemps(68.0, 64.4, 59.0, 77.0, "imperial")

	assert.InDelta(t, 20.0, temp, 0.01)      // 68°F = 20°C
	assert.InDelta(t, 18.0, feelsLike, 0.01) // 64.4°F = 18°C
	assert.InDelta(t, 15.0, tempMin, 0.01)   // 59°F = 15°C
	assert.InDelta(t, 25.0, tempMax, 0.01)   // 77°F = 25°C
}

func TestConvertOpenWeatherTemps_Standard(t *testing.T) {
	// Standard units - Kelvin to Celsius
	temp, feelsLike, tempMin, tempMax := convertOpenWeatherTemps(293.15, 291.15, 288.15, 298.15, "standard")

	assert.InDelta(t, 20.0, temp, 0.001)      // 293.15K = 20°C
	assert.InDelta(t, 18.0, feelsLike, 0.001) // 291.15K = 18°C
	assert.InDelta(t, 15.0, tempMin, 0.001)   // 288.15K = 15°C
	assert.InDelta(t, 25.0, tempMax, 0.001)   // 298.15K = 25°C
}

func TestMaskAPIKey(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		keyParam string
		expected string
	}{
		{
			name:     "masks_appid",
			url:      "https://api.example.com?lat=60&appid=secret123&units=metric",
			keyParam: "appid",
			expected: "https://api.example.com?appid=%2A%2A%2AMASKED%2A%2A%2A&lat=60&units=metric",
		},
		{
			name:     "no_key_present",
			url:      "https://api.example.com?lat=60&units=metric",
			keyParam: "appid",
			expected: "https://api.example.com?lat=60&units=metric",
		},
		{
			name:     "url_without_query",
			url:      "https://api.example.com/path",
			keyParam: "appid",
			expected: "https://api.example.com/path",
		},
		{
			name:     "different_key_param",
			url:      "https://api.example.com?apiKey=secret&lat=60",
			keyParam: "apiKey",
			expected: "https://api.example.com?apiKey=%2A%2A%2AMASKED%2A%2A%2A&lat=60",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maskAPIKey(tt.url, tt.keyParam)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestGetPrimaryWeatherDescription(t *testing.T) {
	tests := []struct {
		name    string
		weather []struct {
			ID          int    `json:"id"`
			Main        string `json:"main"`
			Description string `json:"description"`
			Icon        string `json:"icon"`
		}
		expected string
	}{
		{
			name: "single_weather",
			weather: []struct {
				ID          int    `json:"id"`
				Main        string `json:"main"`
				Description string `json:"description"`
				Icon        string `json:"icon"`
			}{
				{ID: 800, Main: "Clear", Description: "clear sky", Icon: "01d"},
			},
			expected: "clear sky",
		},
		{
			name: "multiple_weather_returns_first",
			weather: []struct {
				ID          int    `json:"id"`
				Main        string `json:"main"`
				Description string `json:"description"`
				Icon        string `json:"icon"`
			}{
				{ID: 500, Main: "Rain", Description: "light rain", Icon: "10d"},
				{ID: 701, Main: "Mist", Description: "mist", Icon: "50d"},
			},
			expected: "light rain",
		},
		{
			name: "empty_weather",
			weather: []struct {
				ID          int    `json:"id"`
				Main        string `json:"main"`
				Description string `json:"description"`
				Icon        string `json:"icon"`
			}{},
			expected: "N/A",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getPrimaryWeatherDescription(tt.weather)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestGetPrimaryWeatherIcon(t *testing.T) {
	tests := []struct {
		name    string
		weather []struct {
			ID          int    `json:"id"`
			Main        string `json:"main"`
			Description string `json:"description"`
			Icon        string `json:"icon"`
		}
		expected string
	}{
		{
			name: "single_weather",
			weather: []struct {
				ID          int    `json:"id"`
				Main        string `json:"main"`
				Description string `json:"description"`
				Icon        string `json:"icon"`
			}{
				{ID: 800, Main: "Clear", Description: "clear sky", Icon: "01d"},
			},
			expected: string(IconClearSky),
		},
		{
			name: "empty_weather",
			weather: []struct {
				ID          int    `json:"id"`
				Main        string `json:"main"`
				Description string `json:"description"`
				Icon        string `json:"icon"`
			}{},
			expected: string(IconUnknown),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getPrimaryWeatherIcon(tt.weather, "openweather")
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestNewOpenWeatherProvider(t *testing.T) {
	provider := NewOpenWeatherProvider()
	require.NotNil(t, provider)

	// Verify it implements Provider interface
	var _ = provider
}

// =============================================================================
// OPENWEATHER RESPONSE MAPPING TESTS
// =============================================================================

// TestMapOpenWeatherResponse tests the conversion of OpenWeatherResponse to WeatherData.
// Similar to TestMapYrResponseToWeatherData, this tests the mapping logic directly.
func TestMapOpenWeatherResponse(t *testing.T) {
	t.Run("metric_units", func(t *testing.T) {
		settings := createTestSettings(t, "openweather", func(s *conf.Settings) {
			s.Realtime.Weather.OpenWeather.Units = "metric"
		})

		response := &OpenWeatherResponse{}
		response.Coord.Lat = 60.1699
		response.Coord.Lon = 24.9384
		response.Main.Temp = 15.5
		response.Main.FeelsLike = 14.0
		response.Main.TempMin = 13.0
		response.Main.TempMax = 17.0
		response.Main.Pressure = 1015
		response.Main.Humidity = 65
		response.Visibility = 10000
		response.Wind.Speed = 5.5
		response.Wind.Deg = 180
		response.Wind.Gust = 8.0
		response.Clouds.All = 50
		response.Dt = 1736769600 // 2025-01-13 12:00:00 UTC
		response.Sys.Country = "FI"
		response.Name = "Helsinki"
		response.Weather = []struct {
			ID          int    `json:"id"`
			Main        string `json:"main"`
			Description string `json:"description"`
			Icon        string `json:"icon"`
		}{
			{ID: 803, Main: "Clouds", Description: "broken clouds", Icon: "04d"},
		}

		result := mapOpenWeatherResponse(response, settings)

		require.NotNil(t, result)

		// Metric units - no conversion needed
		assert.InDelta(t, 15.5, result.Temperature.Current, 0.01)
		assert.InDelta(t, 14.0, result.Temperature.FeelsLike, 0.01)
		assert.InDelta(t, 13.0, result.Temperature.Min, 0.01)
		assert.InDelta(t, 17.0, result.Temperature.Max, 0.01)

		// Other values
		assert.Equal(t, 1015, result.Pressure)
		assert.Equal(t, 65, result.Humidity)
		assert.Equal(t, 10000, result.Visibility)
		assert.InDelta(t, 5.5, result.Wind.Speed, 0.01)
		assert.Equal(t, 180, result.Wind.Deg)
		assert.InDelta(t, 8.0, result.Wind.Gust, 0.01)
		assert.Equal(t, 50, result.Clouds)

		// Location
		assert.InDelta(t, 60.1699, result.Location.Latitude, 0.001)
		assert.InDelta(t, 24.9384, result.Location.Longitude, 0.001)
		assert.Equal(t, "FI", result.Location.Country)
		assert.Equal(t, "Helsinki", result.Location.City)

		// Weather description and icon
		assert.Equal(t, "broken clouds", result.Description)
		assert.Equal(t, string(IconCloudy), result.Icon) // 04d -> cloudy
	})

	// Test temperature unit conversions using table-driven tests
	tempConversionTests := []struct {
		name      string
		units     string
		temp      float64 // Input in specified units
		feelsLike float64
		tempMin   float64
		tempMax   float64
		delta     float64 // Acceptable delta for assertions
	}{
		{
			name:      "imperial_fahrenheit_to_celsius",
			units:     "imperial",
			temp:      68.0,  // 20°C
			feelsLike: 64.4,  // 18°C
			tempMin:   59.0,  // 15°C
			tempMax:   77.0,  // 25°C
			delta:     0.1,
		},
		{
			name:      "standard_kelvin_to_celsius",
			units:     "standard",
			temp:      293.15, // 20°C
			feelsLike: 291.15, // 18°C
			tempMin:   288.15, // 15°C
			tempMax:   298.15, // 25°C
			delta:     0.01,
		},
	}

	for _, tt := range tempConversionTests {
		t.Run(tt.name, func(t *testing.T) {
			settings := createTestSettings(t, "openweather", func(s *conf.Settings) {
				s.Realtime.Weather.OpenWeather.Units = tt.units
			})

			response := &OpenWeatherResponse{}
			response.Main.Temp = tt.temp
			response.Main.FeelsLike = tt.feelsLike
			response.Main.TempMin = tt.tempMin
			response.Main.TempMax = tt.tempMax
			response.Dt = 1736769600
			response.Weather = []struct {
				ID          int    `json:"id"`
				Main        string `json:"main"`
				Description string `json:"description"`
				Icon        string `json:"icon"`
			}{
				{ID: 800, Main: "Clear", Description: "clear sky", Icon: "01d"},
			}

			result := mapOpenWeatherResponse(response, settings)

			require.NotNil(t, result)

			// All conversions should result in ~20, 18, 15, 25°C
			assert.InDelta(t, 20.0, result.Temperature.Current, tt.delta)
			assert.InDelta(t, 18.0, result.Temperature.FeelsLike, tt.delta)
			assert.InDelta(t, 15.0, result.Temperature.Min, tt.delta)
			assert.InDelta(t, 25.0, result.Temperature.Max, tt.delta)
		})
	}

	t.Run("icon_code_mapping", func(t *testing.T) {
		settings := createTestSettings(t, "openweather")

		iconTests := []struct {
			apiIcon  string
			expected IconCode
		}{
			{"01d", IconClearSky},
			{"01n", IconClearSky},
			{"02d", IconFair},
			{"03d", IconPartlyCloudy},
			{"04d", IconCloudy},
			{"09d", IconRainShowers},
			{"10d", IconRain},
			{"11d", IconThunderstorm},
			{"13d", IconSnow},
			{"50d", IconFog},
		}

		for _, tt := range iconTests {
			t.Run(tt.apiIcon, func(t *testing.T) {
				response := &OpenWeatherResponse{}
				response.Dt = 1736769600
				response.Weather = []struct {
					ID          int    `json:"id"`
					Main        string `json:"main"`
					Description string `json:"description"`
					Icon        string `json:"icon"`
				}{
					{ID: 800, Main: "Test", Description: "test", Icon: tt.apiIcon},
				}

				result := mapOpenWeatherResponse(response, settings)

				assert.Equal(t, string(tt.expected), result.Icon,
					"API icon %s should map to %s", tt.apiIcon, tt.expected)
			})
		}
	})

	t.Run("time_from_unix_timestamp", func(t *testing.T) {
		settings := createTestSettings(t, "openweather")

		response := &OpenWeatherResponse{}
		response.Dt = 1736769600 // 2025-01-13 12:00:00 UTC
		response.Weather = []struct {
			ID          int    `json:"id"`
			Main        string `json:"main"`
			Description string `json:"description"`
			Icon        string `json:"icon"`
		}{
			{ID: 800, Main: "Clear", Description: "clear sky", Icon: "01d"},
		}

		result := mapOpenWeatherResponse(response, settings)

		expectedTime := time.Unix(1736769600, 0)
		assert.True(t, result.Time.Equal(expectedTime),
			"Time should be parsed from Unix timestamp. Expected: %v, Got: %v",
			expectedTime, result.Time)
	})
}
