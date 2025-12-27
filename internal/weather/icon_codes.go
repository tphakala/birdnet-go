package weather

// IconCode represents a standardized weather icon code
type IconCode string

// Standardized Icon Codes
const (
	IconClearSky     IconCode = "01"
	IconFair         IconCode = "02"
	IconPartlyCloudy IconCode = "03"
	IconCloudy       IconCode = "04"
	IconRainShowers  IconCode = "09"
	IconRain         IconCode = "10"
	IconThunderstorm IconCode = "11"
	IconSleet        IconCode = "12"
	IconSnow         IconCode = "13"
	IconFog          IconCode = "50"
	IconUnknown      IconCode = "unknown" // Added unknown icon code
)

// YrNoSymbolToIcon maps Yr.no symbol codes to standardized icon codes
// Complete list from https://nrkno.github.io/yr-weather-symbols/
var YrNoSymbolToIcon = map[string]IconCode{
	// Clear sky
	"clearsky_day":           IconClearSky,
	"clearsky_night":         IconClearSky,
	"clearsky_polartwilight": IconClearSky,

	// Fair (few clouds)
	"fair_day":           IconFair,
	"fair_night":         IconFair,
	"fair_polartwilight": IconFair,

	// Partly cloudy
	"partlycloudy_day":           IconPartlyCloudy,
	"partlycloudy_night":         IconPartlyCloudy,
	"partlycloudy_polartwilight": IconPartlyCloudy,

	// Cloudy
	"cloudy": IconCloudy,

	// Fog
	"fog": IconFog,

	// Light rain showers
	"lightrainshowers_day":           IconRainShowers,
	"lightrainshowers_night":         IconRainShowers,
	"lightrainshowers_polartwilight": IconRainShowers,

	// Rain showers
	"rainshowers_day":           IconRainShowers,
	"rainshowers_night":         IconRainShowers,
	"rainshowers_polartwilight": IconRainShowers,

	// Heavy rain showers
	"heavyrainshowers_day":           IconRainShowers,
	"heavyrainshowers_night":         IconRainShowers,
	"heavyrainshowers_polartwilight": IconRainShowers,

	// Light rain
	"lightrain": IconRain,

	// Rain
	"rain": IconRain,

	// Heavy rain
	"heavyrain": IconRain,

	// Light rain showers and thunder
	"lightrainshowersandthunder_day":           IconThunderstorm,
	"lightrainshowersandthunder_night":         IconThunderstorm,
	"lightrainshowersandthunder_polartwilight": IconThunderstorm,

	// Rain showers and thunder
	"rainshowersandthunder_day":           IconThunderstorm,
	"rainshowersandthunder_night":         IconThunderstorm,
	"rainshowersandthunder_polartwilight": IconThunderstorm,

	// Heavy rain showers and thunder
	"heavyrainshowersandthunder_day":           IconThunderstorm,
	"heavyrainshowersandthunder_night":         IconThunderstorm,
	"heavyrainshowersandthunder_polartwilight": IconThunderstorm,

	// Light rain and thunder
	"lightrainandthunder": IconThunderstorm,

	// Rain and thunder
	"rainandthunder": IconThunderstorm,

	// Heavy rain and thunder
	"heavyrainandthunder": IconThunderstorm,

	// Light sleet showers
	"lightsleetshowers_day":           IconSleet,
	"lightsleetshowers_night":         IconSleet,
	"lightsleetshowers_polartwilight": IconSleet,

	// Sleet showers
	"sleetshowers_day":           IconSleet,
	"sleetshowers_night":         IconSleet,
	"sleetshowers_polartwilight": IconSleet,

	// Heavy sleet showers
	"heavysleetshowers_day":           IconSleet,
	"heavysleetshowers_night":         IconSleet,
	"heavysleetshowers_polartwilight": IconSleet,

	// Light sleet
	"lightsleet": IconSleet,

	// Sleet
	"sleet": IconSleet,

	// Heavy sleet
	"heavysleet": IconSleet,

	// Light sleet showers and thunder (note: yr.no has typo "lightssleet" with extra 's')
	"lightssleetshowersandthunder_day":           IconThunderstorm,
	"lightssleetshowersandthunder_night":         IconThunderstorm,
	"lightssleetshowersandthunder_polartwilight": IconThunderstorm,

	// Sleet showers and thunder
	"sleetshowersandthunder_day":           IconThunderstorm,
	"sleetshowersandthunder_night":         IconThunderstorm,
	"sleetshowersandthunder_polartwilight": IconThunderstorm,

	// Heavy sleet showers and thunder
	"heavysleetshowersandthunder_day":           IconThunderstorm,
	"heavysleetshowersandthunder_night":         IconThunderstorm,
	"heavysleetshowersandthunder_polartwilight": IconThunderstorm,

	// Light sleet and thunder
	"lightsleetandthunder": IconThunderstorm,

	// Sleet and thunder
	"sleetandthunder": IconThunderstorm,

	// Heavy sleet and thunder
	"heavysleetandthunder": IconThunderstorm,

	// Light snow showers
	"lightsnowshowers_day":           IconSnow,
	"lightsnowshowers_night":         IconSnow,
	"lightsnowshowers_polartwilight": IconSnow,

	// Snow showers
	"snowshowers_day":           IconSnow,
	"snowshowers_night":         IconSnow,
	"snowshowers_polartwilight": IconSnow,

	// Heavy snow showers
	"heavysnowshowers_day":           IconSnow,
	"heavysnowshowers_night":         IconSnow,
	"heavysnowshowers_polartwilight": IconSnow,

	// Light snow
	"lightsnow": IconSnow,

	// Snow
	"snow": IconSnow,

	// Heavy snow
	"heavysnow": IconSnow,

	// Light snow showers and thunder (note: yr.no has typo "lightssnow" with extra 's')
	"lightssnowshowersandthunder_day":           IconThunderstorm,
	"lightssnowshowersandthunder_night":         IconThunderstorm,
	"lightssnowshowersandthunder_polartwilight": IconThunderstorm,

	// Snow showers and thunder
	"snowshowersandthunder_day":           IconThunderstorm,
	"snowshowersandthunder_night":         IconThunderstorm,
	"snowshowersandthunder_polartwilight": IconThunderstorm,

	// Heavy snow showers and thunder
	"heavysnowshowersandthunder_day":           IconThunderstorm,
	"heavysnowshowersandthunder_night":         IconThunderstorm,
	"heavysnowshowersandthunder_polartwilight": IconThunderstorm,

	// Light snow and thunder
	"lightsnowandthunder": IconThunderstorm,

	// Snow and thunder
	"snowandthunder": IconThunderstorm,

	// Heavy snow and thunder
	"heavysnowandthunder": IconThunderstorm,
}

// OpenWeatherToIcon maps OpenWeather icon codes to standardized icon codes
var OpenWeatherToIcon = map[string]IconCode{
	"01d": IconClearSky, // clear sky
	"01n": IconClearSky,
	"02d": IconFair, // few clouds
	"02n": IconFair,
	"03d": IconPartlyCloudy, // scattered clouds
	"03n": IconPartlyCloudy,
	"04d": IconCloudy, // broken clouds
	"04n": IconCloudy,
	"09d": IconRainShowers, // shower rain
	"09n": IconRainShowers,
	"10d": IconRain, // rain
	"10n": IconRain,
	"11d": IconThunderstorm, // thunderstorm
	"11n": IconThunderstorm,
	"13d": IconSnow, // snow
	"13n": IconSnow,
	"50d": IconFog, // mist
	"50n": IconFog,
}

// IconDescription maps standardized icon codes to human-readable descriptions
var IconDescription = map[IconCode]string{
	IconClearSky:     "Clear Sky",
	IconFair:         "Fair",
	IconPartlyCloudy: "Partly Cloudy",
	IconCloudy:       "Cloudy",
	IconRainShowers:  "Rain Showers",
	IconRain:         "Rain",
	IconThunderstorm: "Thunderstorm",
	IconSleet:        "Sleet",
	IconSnow:         "Snow",
	IconFog:          "Fog",
	IconUnknown:      "Unknown", // Added description for unknown
}

// GetStandardIconCode converts provider-specific weather codes to our standard icon codes
func GetStandardIconCode(code, provider string) IconCode {
	switch provider {
	case "yrno":
		if iconCode, ok := YrNoSymbolToIcon[code]; ok {
			return iconCode
		}
	case "openweather":
		if iconCode, ok := OpenWeatherToIcon[code]; ok {
			return iconCode
		}
	}
	// Return Unknown if no mapping found
	weatherLogger.Warn("No standard icon mapping found for provider code", "provider", provider, "code", code)
	return IconUnknown
}
