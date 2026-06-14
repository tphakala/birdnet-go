package weather

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// allIconCodes lists every standardized icon code so the derivation helpers are
// exercised exhaustively (a new IconCode constant without a mapping entry shows
// up here as a missing case).
var allIconCodes = []IconCode{
	IconClearSky, IconFair, IconPartlyCloudy, IconCloudy, IconRainShowers,
	IconRain, IconThunderstorm, IconSleet, IconSnow, IconFog, IconUnknown,
}

func TestWeatherMainFromIconCode(t *testing.T) {
	t.Parallel()
	cases := map[IconCode]string{
		IconClearSky:     "Clear",
		IconFair:         "Clear",
		IconPartlyCloudy: "Clouds",
		IconCloudy:       "Clouds",
		IconRainShowers:  "Rain",
		IconRain:         "Rain",
		IconThunderstorm: "Thunderstorm",
		IconSleet:        "Sleet",
		IconSnow:         "Snow",
		IconFog:          "Fog",
		IconUnknown:      "",
	}
	// Guard: every icon constant must have an expectation here.
	require.Len(t, cases, len(allIconCodes))
	for _, code := range allIconCodes {
		want, ok := cases[code]
		require.Truef(t, ok, "missing expectation for icon %q", code)
		assert.Equalf(t, want, weatherMainFromIconCode(code), "weather_main for icon %q", code)
	}
}

func TestPrecipTypeFromIconCode(t *testing.T) {
	t.Parallel()
	cases := map[IconCode]string{
		IconClearSky:     "",
		IconFair:         "",
		IconPartlyCloudy: "",
		IconCloudy:       "",
		IconRainShowers:  "rain",
		IconRain:         "rain",
		IconThunderstorm: "rain",
		IconSleet:        "sleet",
		IconSnow:         "snow",
		IconFog:          "",
		IconUnknown:      "",
	}
	require.Len(t, cases, len(allIconCodes))
	for _, code := range allIconCodes {
		want, ok := cases[code]
		require.Truef(t, ok, "missing expectation for icon %q", code)
		assert.Equalf(t, want, precipTypeFromIconCode(code), "precip type for icon %q", code)
	}
}

// TestMapYrResponse_WeatherMainAndPrecipType covers the default provider:
// weather_main is derived from the standardized icon, and precipitation type is set
// only when an amount is reported.
func TestMapYrResponse_WeatherMainAndPrecipType(t *testing.T) {
	t.Parallel()

	newResponse := func(symbol string, precip float64) *YrResponse {
		response := &YrResponse{}
		response.Properties.Timeseries = make([]struct {
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
		}, 1)
		response.Properties.Timeseries[0].Time = time.Date(2026, 1, 13, 12, 0, 0, 0, time.UTC)
		response.Properties.Timeseries[0].Data.Next1Hours.Summary.SymbolCode = symbol
		response.Properties.Timeseries[0].Data.Next1Hours.Details.PrecipitationAmount = precip
		return response
	}

	settings := createTestSettings(t, "yrno")

	t.Run("rain_with_amount", func(t *testing.T) {
		t.Parallel()
		result := mapYrResponseToWeatherData(newResponse("rain", 0.5), settings)
		require.NotNil(t, result)
		assert.Equal(t, "Rain", result.WeatherMain)
		assert.InDelta(t, 0.5, result.Precipitation.Amount, 0.001)
		assert.Equal(t, "rain", result.Precipitation.Type)
	})

	t.Run("clear_no_precip", func(t *testing.T) {
		t.Parallel()
		result := mapYrResponseToWeatherData(newResponse("clearsky_day", 0), settings)
		require.NotNil(t, result)
		assert.Equal(t, "Clear", result.WeatherMain)
		assert.InDelta(t, 0.0, result.Precipitation.Amount, 0.001)
		assert.Empty(t, result.Precipitation.Type, "no precip type when amount is zero")
	})
}

// TestMapOpenWeatherResponse_WeatherMainAndPrecip covers OpenWeather:
// native weather_main is carried through, and rain/snow volume is mapped with snow
// taking precedence when both are present.
func TestMapOpenWeatherResponse_WeatherMainAndPrecip(t *testing.T) {
	t.Parallel()
	settings := createTestSettings(t, "openweather", func(s *conf.Settings) {
		s.Realtime.Weather.OpenWeather.Units = "metric"
	})

	newResponse := func(main string, rain, snow float64) *OpenWeatherResponse {
		response := &OpenWeatherResponse{}
		response.Dt = 1736769600
		response.Weather = []struct {
			ID          int    `json:"id"`
			Main        string `json:"main"`
			Description string `json:"description"`
			Icon        string `json:"icon"`
		}{
			{ID: 500, Main: main, Description: "light rain", Icon: "10d"},
		}
		response.Rain.OneHour = rain
		response.Snow.OneHour = snow
		return response
	}

	t.Run("rain", func(t *testing.T) {
		t.Parallel()
		result := mapOpenWeatherResponse(newResponse("Rain", 1.25, 0), settings)
		require.NotNil(t, result)
		assert.Equal(t, "Rain", result.WeatherMain)
		assert.InDelta(t, 1.25, result.Precipitation.Amount, 0.001)
		assert.Equal(t, "rain", result.Precipitation.Type)
	})

	t.Run("snow_takes_precedence", func(t *testing.T) {
		t.Parallel()
		result := mapOpenWeatherResponse(newResponse("Snow", 0.4, 2.0), settings)
		require.NotNil(t, result)
		assert.Equal(t, "Snow", result.WeatherMain)
		assert.InDelta(t, 2.0, result.Precipitation.Amount, 0.001)
		assert.Equal(t, "snow", result.Precipitation.Type)
	})

	t.Run("dry", func(t *testing.T) {
		t.Parallel()
		result := mapOpenWeatherResponse(newResponse("Clear", 0, 0), settings)
		require.NotNil(t, result)
		assert.Equal(t, "Clear", result.WeatherMain)
		assert.InDelta(t, 0.0, result.Precipitation.Amount, 0.001)
		assert.Empty(t, result.Precipitation.Type)
	})
}

// TestMapWundergroundResponse_WeatherMainAndPrecip covers Wunderground:
// the precipitation rate becomes the persisted amount, the type is inferred from the
// icon (snow below freezing), and weather_main is derived from the inferred icon.
func TestMapWundergroundResponse_WeatherMainAndPrecip(t *testing.T) {
	t.Parallel()

	newResponse := func(temp, precipRate float64) *wundergroundResponse {
		return &wundergroundResponse{
			Observations: []wundergroundObservation{
				{
					ObsTimeUtc:     "2026-01-13T12:00:00Z",
					Neighborhood:   "Testville",
					SolarRadiation: 200,
					Humidity:       70,
					Metric: wundergroundMeasurementData{
						Temp:       temp,
						PrecipRate: precipRate,
						Pressure:   1012,
					},
				},
			},
		}
	}

	t.Run("snow_below_freezing", func(t *testing.T) {
		t.Parallel()
		result := mapWundergroundResponse(newResponse(-3.0, 1.5), getLogger())
		require.NotNil(t, result)
		assert.InDelta(t, 1.5, result.Precipitation.Amount, 0.001)
		assert.Equal(t, "snow", result.Precipitation.Type)
		assert.Equal(t, "Snow", result.WeatherMain)
	})

	t.Run("rain_above_freezing", func(t *testing.T) {
		t.Parallel()
		result := mapWundergroundResponse(newResponse(8.0, 2.0), getLogger())
		require.NotNil(t, result)
		assert.InDelta(t, 2.0, result.Precipitation.Amount, 0.001)
		assert.Equal(t, "rain", result.Precipitation.Type)
		assert.Equal(t, "Rain", result.WeatherMain)
	})

	t.Run("dry_no_type", func(t *testing.T) {
		t.Parallel()
		result := mapWundergroundResponse(newResponse(20.0, 0), getLogger())
		require.NotNil(t, result)
		assert.InDelta(t, 0.0, result.Precipitation.Amount, 0.001)
		assert.Empty(t, result.Precipitation.Type)
	})
}
