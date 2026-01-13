package weather

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetStandardIconCode_YrNo(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected IconCode
	}{
		// Clear sky variants
		{"clearsky_day", "clearsky_day", IconClearSky},
		{"clearsky_night", "clearsky_night", IconClearSky},
		{"clearsky_polartwilight", "clearsky_polartwilight", IconClearSky},

		// Fair weather variants
		{"fair_day", "fair_day", IconFair},
		{"fair_night", "fair_night", IconFair},

		// Partly cloudy variants
		{"partlycloudy_day", "partlycloudy_day", IconPartlyCloudy},
		{"partlycloudy_night", "partlycloudy_night", IconPartlyCloudy},

		// Cloudy
		{"cloudy", "cloudy", IconCloudy},

		// Fog
		{"fog", "fog", IconFog},

		// Rain showers
		{"lightrainshowers_day", "lightrainshowers_day", IconRainShowers},
		{"rainshowers_day", "rainshowers_day", IconRainShowers},
		{"heavyrainshowers_day", "heavyrainshowers_day", IconRainShowers},

		// Rain
		{"lightrain", "lightrain", IconRain},
		{"rain", "rain", IconRain},
		{"heavyrain", "heavyrain", IconRain},

		// Thunderstorms
		{"lightrainshowersandthunder_day", "lightrainshowersandthunder_day", IconThunderstorm},
		{"rainandthunder", "rainandthunder", IconThunderstorm},
		{"heavyrainandthunder", "heavyrainandthunder", IconThunderstorm},

		// Sleet
		{"lightsleet", "lightsleet", IconSleet},
		{"sleet", "sleet", IconSleet},
		{"heavysleet", "heavysleet", IconSleet},
		{"sleetshowers_day", "sleetshowers_day", IconSleet},

		// Snow
		{"lightsnow", "lightsnow", IconSnow},
		{"snow", "snow", IconSnow},
		{"heavysnow", "heavysnow", IconSnow},
		{"snowshowers_day", "snowshowers_day", IconSnow},

		// Thunder with sleet/snow (mapped to thunderstorm)
		{"sleetandthunder", "sleetandthunder", IconThunderstorm},
		{"snowandthunder", "snowandthunder", IconThunderstorm},

		// Yr.no typos (with extra 's')
		{"lightssleetshowersandthunder_day", "lightssleetshowersandthunder_day", IconThunderstorm},
		{"lightssnowshowersandthunder_day", "lightssnowshowersandthunder_day", IconThunderstorm},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetStandardIconCode(tt.code, "yrno")
			assert.Equal(t, tt.expected, got, "GetStandardIconCode(%q, yrno) = %v, want %v", tt.code, got, tt.expected)
		})
	}
}

func TestGetStandardIconCode_OpenWeather(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected IconCode
	}{
		// Clear sky
		{"clear_day", "01d", IconClearSky},
		{"clear_night", "01n", IconClearSky},

		// Few clouds (fair)
		{"few_clouds_day", "02d", IconFair},
		{"few_clouds_night", "02n", IconFair},

		// Scattered clouds (partly cloudy)
		{"scattered_clouds_day", "03d", IconPartlyCloudy},
		{"scattered_clouds_night", "03n", IconPartlyCloudy},

		// Broken clouds (cloudy)
		{"broken_clouds_day", "04d", IconCloudy},
		{"broken_clouds_night", "04n", IconCloudy},

		// Shower rain
		{"shower_rain_day", "09d", IconRainShowers},
		{"shower_rain_night", "09n", IconRainShowers},

		// Rain
		{"rain_day", "10d", IconRain},
		{"rain_night", "10n", IconRain},

		// Thunderstorm
		{"thunderstorm_day", "11d", IconThunderstorm},
		{"thunderstorm_night", "11n", IconThunderstorm},

		// Snow
		{"snow_day", "13d", IconSnow},
		{"snow_night", "13n", IconSnow},

		// Mist/Fog
		{"mist_day", "50d", IconFog},
		{"mist_night", "50n", IconFog},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetStandardIconCode(tt.code, "openweather")
			assert.Equal(t, tt.expected, got, "GetStandardIconCode(%q, openweather) = %v, want %v", tt.code, got, tt.expected)
		})
	}
}

func TestGetStandardIconCode_UnknownCode(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		provider string
	}{
		{"unknown_yrno_code", "nonexistent_weather", "yrno"},
		{"unknown_openweather_code", "99x", "openweather"},
		{"unknown_provider", "01d", "unknown_provider"},
		{"empty_code_yrno", "", "yrno"},
		{"empty_code_openweather", "", "openweather"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetStandardIconCode(tt.code, tt.provider)
			assert.Equal(t, IconUnknown, got, "Unknown code should return IconUnknown")
		})
	}
}

func TestIconConstants(t *testing.T) {
	// Verify all icon constants are properly defined
	icons := []IconCode{
		IconClearSky,
		IconFair,
		IconPartlyCloudy,
		IconCloudy,
		IconRainShowers,
		IconRain,
		IconThunderstorm,
		IconSleet,
		IconSnow,
		IconFog,
		IconUnknown,
	}

	for _, icon := range icons {
		t.Run(string(icon), func(t *testing.T) {
			assert.NotEmpty(t, icon, "Icon code should not be empty")
		})
	}
}

func TestIconDescription(t *testing.T) {
	// All standard icons should have a description
	icons := []IconCode{
		IconClearSky,
		IconFair,
		IconPartlyCloudy,
		IconCloudy,
		IconRainShowers,
		IconRain,
		IconThunderstorm,
		IconSleet,
		IconSnow,
		IconFog,
		IconUnknown,
	}

	for _, icon := range icons {
		t.Run(string(icon), func(t *testing.T) {
			desc, exists := IconDescription[icon]
			require.True(t, exists, "Icon %q should have a description", icon)
			assert.NotEmpty(t, desc, "Description for %q should not be empty", icon)
		})
	}
}

func TestYrNoSymbolToIcon_Coverage(t *testing.T) {
	// Verify all entries in YrNoSymbolToIcon map to valid icon codes
	validIcons := map[IconCode]bool{
		IconClearSky:     true,
		IconFair:         true,
		IconPartlyCloudy: true,
		IconCloudy:       true,
		IconRainShowers:  true,
		IconRain:         true,
		IconThunderstorm: true,
		IconSleet:        true,
		IconSnow:         true,
		IconFog:          true,
		IconUnknown:      true,
	}

	for symbol, icon := range YrNoSymbolToIcon {
		t.Run(symbol, func(t *testing.T) {
			assert.True(t, validIcons[icon], "YrNoSymbolToIcon[%q] = %q is not a valid icon", symbol, icon)
		})
	}
}

func TestOpenWeatherToIcon_Coverage(t *testing.T) {
	// Verify all entries in OpenWeatherToIcon map to valid icon codes
	validIcons := map[IconCode]bool{
		IconClearSky:     true,
		IconFair:         true,
		IconPartlyCloudy: true,
		IconCloudy:       true,
		IconRainShowers:  true,
		IconRain:         true,
		IconThunderstorm: true,
		IconSleet:        true,
		IconSnow:         true,
		IconFog:          true,
		IconUnknown:      true,
	}

	for code, icon := range OpenWeatherToIcon {
		t.Run(code, func(t *testing.T) {
			assert.True(t, validIcons[icon], "OpenWeatherToIcon[%q] = %q is not a valid icon", code, icon)
		})
	}
}

func TestOpenWeatherToIcon_DayNightParity(t *testing.T) {
	// Verify that day and night versions of each code map to the same icon
	dayNightPairs := []struct {
		day   string
		night string
	}{
		{"01d", "01n"}, // clear sky
		{"02d", "02n"}, // few clouds
		{"03d", "03n"}, // scattered clouds
		{"04d", "04n"}, // broken clouds
		{"09d", "09n"}, // shower rain
		{"10d", "10n"}, // rain
		{"11d", "11n"}, // thunderstorm
		{"13d", "13n"}, // snow
		{"50d", "50n"}, // mist
	}

	for _, pair := range dayNightPairs {
		t.Run(pair.day+"_"+pair.night, func(t *testing.T) {
			dayIcon := OpenWeatherToIcon[pair.day]
			nightIcon := OpenWeatherToIcon[pair.night]
			assert.Equal(t, dayIcon, nightIcon, "Day (%s) and night (%s) codes should map to same icon", pair.day, pair.night)
		})
	}
}
