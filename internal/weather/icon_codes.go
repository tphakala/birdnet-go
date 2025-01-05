package weather

// IconCode represents a standardized weather icon code
type IconCode string

// YrNoSymbolToIcon maps Yr.no symbol codes to standardized icon codes
var YrNoSymbolToIcon = map[string]IconCode{
	"clearsky_day":     "01",
	"clearsky_night":   "01",
	"fair_day":         "02",
	"fair_night":       "02",
	"partlycloudy_day": "03",
	"cloudy":           "04",
	"rainshowers_day":  "09",
	"rain":             "10",
	"thunder":          "11",
	"sleet":            "12",
	"snow":             "13",
	"fog":              "50",
	// Add more mappings as needed
}

// OpenWeatherToIcon maps OpenWeather icon codes to standardized icon codes
var OpenWeatherToIcon = map[string]IconCode{
	"01d": "01", // clear sky
	"01n": "01",
	"02d": "02", // few clouds
	"02n": "02",
	"03d": "03", // scattered clouds
	"03n": "03",
	"04d": "04", // broken clouds
	"04n": "04",
	"09d": "09", // shower rain
	"09n": "09",
	"10d": "10", // rain
	"10n": "10",
	"11d": "11", // thunderstorm
	"11n": "11",
	"13d": "13", // snow
	"13n": "13",
	"50d": "50", // mist
	"50n": "50",
}

// IconDescription maps standardized icon codes to human-readable descriptions
var IconDescription = map[IconCode]string{
	"01": "Clear Sky",
	"02": "Fair",
	"03": "Partly Cloudy",
	"04": "Cloudy",
	"09": "Rain Showers",
	"10": "Rain",
	"11": "Thunderstorm",
	"12": "Sleet",
	"13": "Snow",
	"50": "Fog",
}

// GetStandardIconCode converts provider-specific weather codes to our standard icon codes
func GetStandardIconCode(code string, provider string) IconCode {
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
	return "01" // default to clear sky if no mapping found
}
