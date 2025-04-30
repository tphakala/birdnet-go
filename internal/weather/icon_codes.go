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
var YrNoSymbolToIcon = map[string]IconCode{
	"clearsky_day":     IconClearSky,
	"clearsky_night":   IconClearSky,
	"fair_day":         IconFair,
	"fair_night":       IconFair,
	"partlycloudy_day": IconPartlyCloudy,
	"cloudy":           IconCloudy,
	"rainshowers_day":  IconRainShowers,
	"rain":             IconRain,
	"thunder":          IconThunderstorm,
	"sleet":            IconSleet,
	"snow":             IconSnow,
	"fog":              IconFog,
	// Add more mappings as needed
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
