package weather

import (
	"net/http"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// createTestSettings creates test settings with configurable provider.
func createTestSettings(t *testing.T, provider string, opts ...func(*conf.Settings)) *conf.Settings {
	t.Helper()

	settings := &conf.Settings{
		BirdNET: conf.BirdNETConfig{
			Latitude:  60.1699, // Helsinki
			Longitude: 24.9384,
		},
		Realtime: conf.RealtimeSettings{
			Weather: conf.WeatherSettings{
				Provider:     provider,
				PollInterval: 60,
				OpenWeather: conf.OpenWeatherSettings{
					Enabled:  true,
					APIKey:   "test-api-key",
					Endpoint: "https://api.openweathermap.org/data/2.5/weather",
					Units:    "metric",
				},
				Wunderground: conf.WundergroundSettings{
					APIKey:    "test-api-key",
					StationID: "KTEST123",
					Endpoint:  "https://api.weather.com/v2/pws/observations/current",
					Units:     "m",
				},
			},
		},
	}

	for _, opt := range opts {
		opt(settings)
	}

	return settings
}

// createTestWeatherData creates valid WeatherData for testing.
func createTestWeatherData(t *testing.T, opts ...func(*WeatherData)) *WeatherData {
	t.Helper()

	data := &WeatherData{
		Time: time.Now().UTC(),
		Location: Location{
			Latitude:  60.1699,
			Longitude: 24.9384,
			Country:   "FI",
			City:      "Helsinki",
		},
		Temperature: Temperature{
			Current:   15.4,
			FeelsLike: 14.2,
			Min:       12.0,
			Max:       18.0,
		},
		Wind: Wind{
			Speed: 3.4,
			Deg:   220,
			Gust:  5.8,
		},
		Precipitation: Precipitation{
			Amount: 0.0,
			Type:   "",
		},
		Clouds:      45,
		Visibility:  10000,
		Pressure:    1012,
		Humidity:    62,
		Description: "partly cloudy",
		Icon:        "03",
	}

	for _, opt := range opts {
		opt(data)
	}

	return data
}

// setupHTTPMock activates httpmock and returns a cleanup function.
func setupHTTPMock(t *testing.T) {
	t.Helper()
	httpmock.Activate()
	t.Cleanup(httpmock.DeactivateAndReset)
}

// yrNoSuccessResponse returns a valid Yr.no API response JSON string.
func yrNoSuccessResponse() string {
	return `{
  "type": "Feature",
  "geometry": {
    "type": "Point",
    "coordinates": [24.9384, 60.1699, 10]
  },
  "properties": {
    "meta": {
      "updated_at": "2026-01-13T10:00:00Z",
      "units": {
        "air_pressure_at_sea_level": "hPa",
        "air_temperature": "celsius",
        "cloud_area_fraction": "%",
        "precipitation_amount": "mm",
        "relative_humidity": "%",
        "wind_from_direction": "degrees",
        "wind_speed": "m/s"
      }
    },
    "timeseries": [
      {
        "time": "2026-01-13T12:00:00Z",
        "data": {
          "instant": {
            "details": {
              "air_pressure_at_sea_level": 1012.5,
              "air_temperature": 15.4,
              "cloud_area_fraction": 45.2,
              "relative_humidity": 62.0,
              "wind_from_direction": 220.5,
              "wind_speed": 3.4,
              "wind_speed_of_gust": 5.8
            }
          },
          "next_1_hours": {
            "summary": {
              "symbol_code": "partlycloudy_day"
            },
            "details": {
              "precipitation_amount": 0.0
            }
          }
        }
      }
    ]
  }
}`
}

// openWeatherSuccessResponse returns a valid OpenWeather API response JSON string.
func openWeatherSuccessResponse() string {
	return `{
  "coord": { "lon": 24.9384, "lat": 60.1699 },
  "weather": [{ "id": 803, "main": "Clouds", "description": "broken clouds", "icon": "04d" }],
  "base": "stations",
  "main": { "temp": 14.55, "feels_like": 13.88, "temp_min": 13.33, "temp_max": 15.65, "pressure": 1014, "humidity": 72 },
  "visibility": 10000,
  "wind": { "speed": 4.12, "deg": 240, "gust": 7.5 },
  "clouds": { "all": 75 },
  "dt": 1736769600,
  "sys": { "type": 2, "id": 2006068, "country": "FI", "sunrise": 1736748345, "sunset": 1736779789 },
  "timezone": 7200,
  "id": 658225,
  "name": "Helsinki",
  "cod": 200
}`
}

// wundergroundSuccessResponse returns a valid Weather Underground API response JSON string.
func wundergroundSuccessResponse() string {
	return `{
  "observations": [{
    "stationID": "KTEST123",
    "obsTimeUtc": "2026-01-13T12:00:00Z",
    "obsTimeLocal": "2026-01-13 14:00:00",
    "neighborhood": "Test Location",
    "softwareType": "TestSoftware",
    "country": "FI",
    "solarRadiation": 450.5,
    "lon": 24.9384,
    "lat": 60.1699,
    "uv": 2.0,
    "winddir": 270,
    "humidity": 55.0,
    "qcStatus": 1,
    "imperial": {
      "temp": 59.0,
      "heatIndex": 59.0,
      "dewpt": 42.8,
      "windChill": 56.0,
      "windSpeed": 5.6,
      "windGust": 9.2,
      "pressure": 29.92,
      "precipRate": 0.0,
      "precipTotal": 0.0,
      "elev": 33.0
    },
    "metric": {
      "temp": 15.0,
      "heatIndex": 15.0,
      "dewpt": 6.0,
      "windChill": 13.3,
      "windSpeed": 9.0,
      "windGust": 14.8,
      "pressure": 1013.2,
      "precipRate": 0.0,
      "precipTotal": 0.0,
      "elev": 10.0
    }
  }]
}`
}

// wundergroundTestErrorResponse returns a Wunderground API error response for tests.
func wundergroundTestErrorResponse(code, message string) string {
	return `{
  "metadata": {"transaction_id": "test-txn-123"},
  "success": false,
  "errors": [{
    "error": {
      "code": "` + code + `",
      "message": "` + message + `"
    }
  }]
}`
}

// registerYrNoResponder registers a mock responder for Yr.no API.
func registerYrNoResponder(t *testing.T, statusCode int, body string, headers map[string]string) {
	t.Helper()

	responder := func(req *http.Request) (*http.Response, error) {
		resp := httpmock.NewStringResponse(statusCode, body)
		for k, v := range headers {
			resp.Header.Set(k, v)
		}
		return resp, nil
	}

	httpmock.RegisterResponder("GET", `=~^https://api\.met\.no/weatherapi/locationforecast/2\.0/complete`,
		responder)
}

// registerOpenWeatherResponder registers a mock responder for OpenWeather API.
func registerOpenWeatherResponder(t *testing.T, statusCode int, body string) {
	t.Helper()

	httpmock.RegisterResponder("GET", `=~^https://api\.openweathermap\.org/data/2\.5/weather`,
		httpmock.NewStringResponder(statusCode, body))
}

// registerWundergroundResponder registers a mock responder for Wunderground API.
func registerWundergroundResponder(t *testing.T, statusCode int, body string) {
	t.Helper()

	httpmock.RegisterResponder("GET", `=~^https://api\.weather\.com/v2/pws/observations/current`,
		httpmock.NewStringResponder(statusCode, body))
}

// assertWeatherDataBasics validates basic WeatherData fields.
func assertWeatherDataBasics(t *testing.T, data *WeatherData) {
	t.Helper()

	require.NotNil(t, data, "WeatherData should not be nil")
	require.False(t, data.Time.IsZero(), "Time should not be zero")
}
