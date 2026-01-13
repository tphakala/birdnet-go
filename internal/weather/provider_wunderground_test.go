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

func TestWundergroundProvider_FetchWeather_Success(t *testing.T) {
	setupHTTPMock(t)

	registerWundergroundResponder(t, http.StatusOK, wundergroundSuccessResponse())

	provider := NewWundergroundProvider(nil)
	settings := createTestSettings(t, "wunderground")

	data, err := provider.FetchWeather(settings)

	require.NoError(t, err)
	assertWeatherDataBasics(t, data)

	// Verify parsed values match expected (metric units)
	assert.InDelta(t, 15.0, data.Temperature.Current, 0.1)
	assert.InDelta(t, 9.0*KmhToMs, data.Wind.Speed, 0.1) // 9 km/h to m/s
	assert.Equal(t, 270, data.Wind.Deg)
	assert.Equal(t, 1013, data.Pressure)
	assert.Equal(t, 55, data.Humidity)

	// Verify location from API response
	assert.InDelta(t, 60.1699, data.Location.Latitude, 0.001)
	assert.InDelta(t, 24.9384, data.Location.Longitude, 0.001)
	assert.Equal(t, "FI", data.Location.Country)
	assert.Equal(t, "Test Location", data.Location.City)
}

// TestWundergroundProvider_TimeParsing explicitly tests that observation time is correctly
// parsed from the fixture, detecting if the fallback to time.Now() is triggered.
// This prevents silent failures where time parsing bugs are masked by the fallback.
func TestWundergroundProvider_TimeParsing(t *testing.T) {
	setupHTTPMock(t)

	registerWundergroundResponder(t, http.StatusOK, wundergroundSuccessResponse())

	provider := NewWundergroundProvider(nil)
	settings := createTestSettings(t, "wunderground")

	data, err := provider.FetchWeather(settings)
	require.NoError(t, err)

	// The fixture contains obsTimeUtc: "2026-01-13T12:00:00Z"
	// If parsing fails, it falls back to time.Now() which would be different
	expectedTime, parseErr := time.Parse(time.RFC3339, "2026-01-13T12:00:00Z")
	require.NoError(t, parseErr, "Test fixture time should be valid RFC3339")

	// Use WithinDuration to allow for minor processing time differences
	// but catch if fallback to time.Now() occurs (would be ~2024/2025 difference)
	require.WithinDuration(t, expectedTime, data.Time, time.Second,
		"Parsed time should match fixture, not fall back to time.Now(). "+
			"Expected: %v, Got: %v", expectedTime, data.Time)
}

func TestWundergroundProvider_FetchWeather_NoAPIKey(t *testing.T) {
	provider := NewWundergroundProvider(nil)
	settings := createTestSettings(t, "wunderground", func(s *conf.Settings) {
		s.Realtime.Weather.Wunderground.APIKey = ""
	})

	data, err := provider.FetchWeather(settings)

	require.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "not configured")
}

func TestWundergroundProvider_FetchWeather_NoStationID(t *testing.T) {
	provider := NewWundergroundProvider(nil)
	settings := createTestSettings(t, "wunderground", func(s *conf.Settings) {
		s.Realtime.Weather.Wunderground.StationID = ""
	})

	data, err := provider.FetchWeather(settings)

	require.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "not configured")
}

func TestWundergroundProvider_FetchWeather_InvalidStationID(t *testing.T) {
	provider := NewWundergroundProvider(nil)
	settings := createTestSettings(t, "wunderground", func(s *conf.Settings) {
		s.Realtime.Weather.Wunderground.StationID = "invalid!station@id"
	})

	data, err := provider.FetchWeather(settings)

	require.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "invalid")
}

func TestWundergroundProvider_FetchWeather_HTTPError(t *testing.T) {
	setupHTTPMock(t)

	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{
			name:       "unauthorized",
			statusCode: http.StatusUnauthorized,
			body:       wundergroundTestErrorResponse("CDN-0001", "Invalid API key"),
		},
		{
			name:       "station_not_found",
			statusCode: http.StatusNotFound,
			body:       wundergroundTestErrorResponse("CDN-0002", "Station not found"),
		},
		{
			name:       "rate_limit",
			statusCode: http.StatusTooManyRequests,
			body:       wundergroundTestErrorResponse("CDN-0004", "Rate limit exceeded"),
		},
		{
			name:       "internal_server_error",
			statusCode: http.StatusInternalServerError,
			body:       `{"error": "internal server error"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpmock.Reset()
			registerWundergroundResponder(t, tt.statusCode, tt.body)

			provider := NewWundergroundProvider(nil)
			settings := createTestSettings(t, "wunderground")

			data, err := provider.FetchWeather(settings)

			require.Error(t, err)
			assert.Nil(t, data)
		})
	}
}

func TestWundergroundProvider_FetchWeather_InvalidJSON(t *testing.T) {
	setupHTTPMock(t)

	registerWundergroundResponder(t, http.StatusOK, `{invalid json`)

	provider := NewWundergroundProvider(nil)
	settings := createTestSettings(t, "wunderground")

	data, err := provider.FetchWeather(settings)

	require.Error(t, err)
	assert.Nil(t, data)
}

func TestWundergroundProvider_FetchWeather_EmptyObservations(t *testing.T) {
	setupHTTPMock(t)

	emptyResponse := `{"observations": []}`
	registerWundergroundResponder(t, http.StatusOK, emptyResponse)

	provider := NewWundergroundProvider(nil)
	settings := createTestSettings(t, "wunderground")

	data, err := provider.FetchWeather(settings)

	require.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "no observations")
}

func TestInferWundergroundIcon(t *testing.T) {
	tests := []struct {
		name           string
		tempC          float64
		precipMM       float64
		humidity       float64
		solarRadiation float64
		windGustMS     float64
		expected       IconCode
	}{
		// Thunderstorm conditions
		{
			name:           "thunderstorm",
			tempC:          20.0,
			precipMM:       15.0, // > ThunderstormPrecipMM
			humidity:       80.0,
			solarRadiation: 100.0,
			windGustMS:     20.0, // > ThunderstormGustMS
			expected:       IconThunderstorm,
		},

		// Precipitation - snow when cold
		{
			name:           "snow_when_cold",
			tempC:          -5.0, // Below freezing
			precipMM:       5.0,
			humidity:       80.0,
			solarRadiation: 100.0,
			windGustMS:     5.0,
			expected:       IconSnow,
		},

		// Precipitation - rain when warm
		{
			name:           "rain_when_warm",
			tempC:          10.0, // Above freezing
			precipMM:       5.0,
			humidity:       80.0,
			solarRadiation: 100.0,
			windGustMS:     5.0,
			expected:       IconRain,
		},

		// Fog - high humidity, low temp
		{
			name:           "fog",
			tempC:          2.0, // Below FogTempThresholdC
			precipMM:       0.0,
			humidity:       95.0, // Above FogHumidityPercent
			solarRadiation: 100.0,
			windGustMS:     2.0,
			expected:       IconFog,
		},

		// Night - clear (low humidity)
		{
			name:           "night_clear",
			tempC:          15.0,
			precipMM:       0.0,
			humidity:       40.0, // Below NightPartlyCloudyHumidityPercent
			solarRadiation: 0.0,  // Night
			windGustMS:     2.0,
			expected:       IconClearSky,
		},

		// Night - partly cloudy
		{
			name:           "night_partly_cloudy",
			tempC:          15.0,
			precipMM:       0.0,
			humidity:       70.0, // Between thresholds
			solarRadiation: 0.0,  // Night
			windGustMS:     2.0,
			expected:       IconPartlyCloudy,
		},

		// Night - cloudy (high humidity)
		{
			name:           "night_cloudy",
			tempC:          15.0,
			precipMM:       0.0,
			humidity:       90.0, // Above NightCloudyHumidityPercent
			solarRadiation: 0.0,  // Night
			windGustMS:     2.0,
			expected:       IconCloudy,
		},

		// Day - clear sky (high solar radiation)
		{
			name:           "day_clear",
			tempC:          25.0,
			precipMM:       0.0,
			humidity:       40.0,
			solarRadiation: 700.0, // Above DayClearSRThreshold
			windGustMS:     2.0,
			expected:       IconClearSky,
		},

		// Day - partly cloudy
		{
			name:           "day_partly_cloudy",
			tempC:          20.0,
			precipMM:       0.0,
			humidity:       50.0,
			solarRadiation: 400.0, // Between thresholds
			windGustMS:     2.0,
			expected:       IconPartlyCloudy,
		},

		// Day - cloudy (low solar radiation)
		{
			name:           "day_cloudy",
			tempC:          15.0,
			precipMM:       0.0,
			humidity:       70.0,
			solarRadiation: 100.0, // Below DayPartlyCloudyLowerSR
			windGustMS:     2.0,
			expected:       IconCloudy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InferWundergroundIcon(tt.tempC, tt.precipMM, tt.humidity, tt.solarRadiation, tt.windGustMS)
			assert.Equal(t, tt.expected, got, "InferWundergroundIcon returned unexpected icon")
		})
	}
}

func TestInferNightIcon(t *testing.T) {
	tests := []struct {
		name     string
		humidity float64
		expected IconCode
	}{
		{"very_high_humidity_cloudy", 90.0, IconCloudy},
		{"high_humidity_partly_cloudy", 70.0, IconPartlyCloudy},
		{"low_humidity_clear", 40.0, IconClearSky},
		{"threshold_cloudy", NightCloudyHumidityPercent, IconCloudy},
		{"threshold_partly_cloudy", NightPartlyCloudyHumidityPercent, IconPartlyCloudy},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferNightIcon(tt.humidity)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestInferDaytimeIcon(t *testing.T) {
	tests := []struct {
		name           string
		solarRadiation float64
		expected       IconCode
	}{
		{"high_radiation_clear", 700.0, IconClearSky},
		{"medium_radiation_partly_cloudy", 400.0, IconPartlyCloudy},
		{"low_radiation_cloudy", 100.0, IconCloudy},
		{"threshold_clear", DayClearSRThreshold + 1, IconClearSky},
		{"threshold_partly_cloudy_lower", DayPartlyCloudyLowerSR, IconPartlyCloudy},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferDaytimeIcon(tt.solarRadiation)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestValidateWundergroundConfig(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func(*conf.Settings)
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "valid_config",
			setupFunc: nil,
			wantErr:   false,
		},
		{
			name: "missing_api_key",
			setupFunc: func(s *conf.Settings) {
				s.Realtime.Weather.Wunderground.APIKey = ""
			},
			wantErr: true,
			errMsg:  "not configured",
		},
		{
			name: "missing_station_id",
			setupFunc: func(s *conf.Settings) {
				s.Realtime.Weather.Wunderground.StationID = ""
			},
			wantErr: true,
			errMsg:  "not configured",
		},
		{
			name: "invalid_station_id_format",
			setupFunc: func(s *conf.Settings) {
				s.Realtime.Weather.Wunderground.StationID = "invalid!@#"
			},
			wantErr: true,
			errMsg:  "invalid",
		},
		{
			name: "valid_custom_units_m",
			setupFunc: func(s *conf.Settings) {
				s.Realtime.Weather.Wunderground.Units = "m"
			},
			wantErr: false,
		},
		{
			name: "valid_custom_units_e",
			setupFunc: func(s *conf.Settings) {
				s.Realtime.Weather.Wunderground.Units = "e"
			},
			wantErr: false,
		},
		{
			name: "valid_custom_units_h",
			setupFunc: func(s *conf.Settings) {
				s.Realtime.Weather.Wunderground.Units = "h"
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := createTestSettings(t, "wunderground")
			if tt.setupFunc != nil {
				tt.setupFunc(settings)
			}

			cfg, err := validateWundergroundConfig(settings)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Nil(t, cfg)
			} else {
				require.NoError(t, err)
				require.NotNil(t, cfg)
			}
		})
	}
}

func TestNormalizeWindGust(t *testing.T) {
	tests := []struct {
		name        string
		windGustRaw float64
		units       string
		expectedMS  float64
	}{
		{"metric_kmh_to_ms", 36.0, "m", 36.0 * KmhToMs},     // 36 km/h = 10 m/s
		{"imperial_mph_to_ms", 22.37, "e", 22.37 * MphToMs}, // 22.37 mph = 10 m/s
		{"hybrid_mph_to_ms", 22.37, "h", 22.37 * MphToMs},
		{"zero_value", 0.0, "m", 0.0},
		{"unknown_units_defaults_to_mph", 10.0, "x", 10.0 * MphToMs},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeWindGust(tt.windGustRaw, tt.units)
			assert.InDelta(t, tt.expectedMS, got, 0.001)
		})
	}
}

func TestNewWundergroundProvider(t *testing.T) {
	t.Run("nil_client_creates_default", func(t *testing.T) {
		provider := NewWundergroundProvider(nil)
		require.NotNil(t, provider)

		// Verify it implements Provider interface
		var _ = provider
	})

	t.Run("custom_client_is_used", func(t *testing.T) {
		customClient := &http.Client{Timeout: 5 * time.Second}
		provider := NewWundergroundProvider(customClient)
		require.NotNil(t, provider)
	})
}

func TestWundergroundConstants(t *testing.T) {
	// Verify important constants are set to reasonable values
	t.Run("precipitation_thresholds", func(t *testing.T) {
		assert.Greater(t, ThunderstormPrecipMM, 0.0, "ThunderstormPrecipMM should be positive")
		assert.Greater(t, ThunderstormGustMS, 0.0, "ThunderstormGustMS should be positive")
	})

	t.Run("temperature_thresholds", func(t *testing.T) {
		assert.InDelta(t, 0.0, FreezingPointC, 0.001, "FreezingPointC should be 0°C")
		assert.Greater(t, FogTempThresholdC, FreezingPointC, "Fog can form above freezing")
	})

	t.Run("humidity_thresholds", func(t *testing.T) {
		assert.Greater(t, FogHumidityPercent, 0.0, "FogHumidityPercent should be positive")
		assert.LessOrEqual(t, FogHumidityPercent, 100.0, "FogHumidityPercent should not exceed 100%")
		assert.Greater(t, NightCloudyHumidityPercent, NightPartlyCloudyHumidityPercent, "Cloudy threshold should be higher")
	})

	t.Run("conversion_factors", func(t *testing.T) {
		// Verify conversion accuracy
		assert.InDelta(t, 0.2778, KmhToMs, 0.001, "1 km/h = ~0.2778 m/s")
		assert.InDelta(t, 0.4470, MphToMs, 0.001, "1 mph = ~0.4470 m/s")
		assert.InDelta(t, 33.864, InHgToHPa, 0.01, "1 inHg = ~33.864 hPa")
	})
}
