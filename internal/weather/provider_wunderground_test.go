package weather

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// TestBuildWundergroundURL_RejectsSchemelessEndpoint mirrors the OpenWeather
// guard: a custom endpoint without a scheme parses as a relative path and would
// produce a request http.NewRequest rejects with "unsupported protocol scheme".
// buildWundergroundURL must reject it with a configuration error instead.
func TestBuildWundergroundURL_RejectsSchemelessEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
	}{
		{"no_scheme", "api.custom-wunderground.com"},
		{"no_scheme_with_path", "api.custom-wunderground.com/v2/pws/observations/current"},
		{"scheme_without_host", "https://"},
		{"non_http_scheme", "ftp://api.custom-wunderground.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &wundergroundConfig{
				apiKey:    "test-api-key",
				stationID: "KTEST123",
				endpoint:  tt.endpoint,
			}

			_, err := buildWundergroundURL(cfg)

			require.Error(t, err)
			var ee *errors.EnhancedError
			require.ErrorAs(t, err, &ee)
			assert.Equal(t, string(errors.CategoryConfiguration), ee.GetCategory(),
				"a malformed endpoint must classify as a configuration error")
		})
	}
}

func TestWundergroundProvider_FetchWeather_Success(t *testing.T) {
	setupHTTPMock(t)

	registerWundergroundResponder(t, http.StatusOK, wundergroundSuccessResponse())

	provider := NewWundergroundProvider(nil)
	settings := createTestSettings(t, "wunderground")

	data, err := provider.FetchWeather(t.Context(), settings)

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

	data, err := provider.FetchWeather(t.Context(), settings)
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

// TestWundergroundProvider_FetchWeather_ImperialConfigIgnored verifies that even
// when the user configures Units="e" (imperial), the provider still requests metric
// from the API and parses temperature correctly. This is a regression test for #2662.
func TestWundergroundProvider_FetchWeather_ImperialConfigIgnored(t *testing.T) {
	setupHTTPMock(t)

	registerWundergroundResponder(t, http.StatusOK, wundergroundSuccessResponse())

	provider := NewWundergroundProvider(nil)
	settings := createTestSettings(t, "wunderground", func(s *conf.Settings) {
		s.Realtime.Weather.Wunderground.Units = "e" // User configured imperial
	})

	data, err := provider.FetchWeather(t.Context(), settings)

	require.NoError(t, err)
	assertWeatherDataBasics(t, data)

	// Temperature must still be correct — the provider always requests metric
	// regardless of the configured Units value
	assert.InDelta(t, 15.0, data.Temperature.Current, 0.1)
	assert.InDelta(t, 9.0*KmhToMs, data.Wind.Speed, 0.1)
	assert.Equal(t, 1013, data.Pressure)
}

func TestWundergroundProvider_FetchWeather_NoAPIKey(t *testing.T) {
	provider := NewWundergroundProvider(nil)
	settings := createTestSettings(t, "wunderground", func(s *conf.Settings) {
		s.Realtime.Weather.Wunderground.APIKey = ""
	})

	data, err := provider.FetchWeather(t.Context(), settings)

	require.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "not configured")
}

func TestWundergroundProvider_FetchWeather_NoStationID(t *testing.T) {
	provider := NewWundergroundProvider(nil)
	settings := createTestSettings(t, "wunderground", func(s *conf.Settings) {
		s.Realtime.Weather.Wunderground.StationID = ""
	})

	data, err := provider.FetchWeather(t.Context(), settings)

	require.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "not configured")
}

func TestWundergroundProvider_FetchWeather_InvalidStationID(t *testing.T) {
	provider := NewWundergroundProvider(nil)
	settings := createTestSettings(t, "wunderground", func(s *conf.Settings) {
		s.Realtime.Weather.Wunderground.StationID = "invalid!station@id"
	})

	data, err := provider.FetchWeather(t.Context(), settings)

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

			data, err := provider.FetchWeather(t.Context(), settings)

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

	data, err := provider.FetchWeather(t.Context(), settings)

	require.Error(t, err)
	assert.Nil(t, data)
}

func TestWundergroundProvider_FetchWeather_EmptyObservations(t *testing.T) {
	setupHTTPMock(t)

	emptyResponse := `{"observations": []}`
	registerWundergroundResponder(t, http.StatusOK, emptyResponse)

	provider := NewWundergroundProvider(nil)
	settings := createTestSettings(t, "wunderground")

	data, err := provider.FetchWeather(t.Context(), settings)

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
			name: "units_field_ignored",
			setupFunc: func(s *conf.Settings) {
				// Units in settings are ignored; we always request metric from the API.
				s.Realtime.Weather.Wunderground.Units = "e"
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

// TestCalculateFeelsLike guards every branch of the feels-like selection logic,
// including the NaN guards that prevent a bogus heat-index/wind-chill reading
// from replacing the actual temperature. All values are metric (Celsius, m/s).
func TestCalculateFeelsLike(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		m    weatherMeasurements
		want float64
	}{
		{
			name: "hot uses heat index",
			m:    weatherMeasurements{temp: 30, heatIndex: 33},
			want: 33,
		},
		{
			name: "hot but non-positive heat index falls back to temp",
			m:    weatherMeasurements{temp: 30, heatIndex: 0},
			want: 30,
		},
		{
			name: "hot but NaN heat index falls back to temp",
			m:    weatherMeasurements{temp: 30, heatIndex: math.NaN()},
			want: 30,
		},
		{
			name: "hot boundary at threshold uses heat index",
			m:    weatherMeasurements{temp: MetricHotTempC, heatIndex: 29},
			want: 29,
		},
		{
			name: "cold and windy uses wind chill",
			m:    weatherMeasurements{temp: 5, windSpeed: 2.0, windChill: 2.0},
			want: 2.0,
		},
		{
			name: "cold but calm falls back to temp",
			m:    weatherMeasurements{temp: 5, windSpeed: 1.0, windChill: 2.0},
			want: 5,
		},
		{
			name: "cold and windy but NaN wind chill falls back to temp",
			m:    weatherMeasurements{temp: 5, windSpeed: 2.0, windChill: math.NaN()},
			want: 5,
		},
		{
			name: "cold boundary at threshold uses wind chill",
			m:    weatherMeasurements{temp: MetricColdTempC, windSpeed: 2.0, windChill: 8.0},
			want: 8.0,
		},
		{
			name: "moderate temperature uses raw temp",
			m:    weatherMeasurements{temp: 20, heatIndex: 99, windChill: -99},
			want: 20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := calculateFeelsLike(tt.m)
			assert.InDelta(t, tt.want, got, 0.001)
		})
	}
}

// TestHandleWundergroundRequestError_CancellationIsBenign guards that a parent
// context cancellation (shutdown) surfaces as raw context.Canceled so
// fetchAndSave treats it as benign, while a per-request deadline stays a real
// categorized timeout failure. Wunderground keeps its own one-shot request path,
// so this must match the shared executeWeatherRequest cancellation behavior.
func TestHandleWundergroundRequestError_CancellationIsBenign(t *testing.T) {
	t.Parallel()

	t.Run("parent cancellation is benign", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		// net/http returns a *url.Error embedding the request URL on cancellation.
		transportErr := &url.Error{Op: "Get", URL: "https://api.weather.com/v2/pws/observations/current?apiKey=secret", Err: context.Canceled}

		got := handleWundergroundRequestError(ctx, transportErr)

		require.ErrorIs(t, got, context.Canceled, "cancellation must surface as context.Canceled so fetchAndSave skips backoff")
		assert.Equal(t, context.Canceled, got, "cancellation must be returned unwrapped, not as a categorized weather error")
	})

	t.Run("deadline is a real timeout failure", func(t *testing.T) {
		t.Parallel()
		// A context whose deadline is already in the past reports DeadlineExceeded.
		ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Hour))
		defer cancel()
		transportErr := &url.Error{Op: "Get", URL: "https://api.weather.com/v2/pws/observations/current?apiKey=secret", Err: context.DeadlineExceeded}

		got := handleWundergroundRequestError(ctx, transportErr)

		require.Error(t, got)
		require.NotErrorIs(t, got, context.Canceled, "a deadline must NOT be treated as a benign cancellation")
		var ee *errors.EnhancedError
		require.ErrorAs(t, got, &ee)
		assert.Equal(t, string(errors.CategoryTimeout), ee.GetCategory(), "a deadline must classify as a timeout failure")
		assert.NotContains(t, got.Error(), "secret", "the API key must be scrubbed from the wrapped error")
	})
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
	})
}
