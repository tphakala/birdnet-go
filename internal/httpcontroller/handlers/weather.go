// internal/httpcontroller/weather_icons.go
package handlers

import (
	"html/template"
	"time"

	"github.com/tphakala/birdnet-go/internal/suncalc"
	"github.com/tphakala/birdnet-go/internal/weather"
)

// GetWeatherIconFunc returns a function that returns an SVG icon for a given weather main
func (h *Handlers) GetWeatherIconFunc() func(weatherCode string, timeOfDay weather.TimeOfDay) template.HTML {
	return func(weatherCode string, timeOfDay weather.TimeOfDay) template.HTML {
		// Strip 'd' or 'n' suffix if present
		if len(weatherCode) > 2 {
			weatherCode = weatherCode[:2]
		}
		iconCode := weather.IconCode(weatherCode)

		return weather.GetWeatherIcon(iconCode, timeOfDay)
	}
}

// GetSunPositionIconFunc returns a function that returns an SVG icon for a given time of day
func (h *Handlers) GetSunPositionIconFunc() func(timeOfDay weather.TimeOfDay) template.HTML {
	return func(timeOfDay weather.TimeOfDay) template.HTML {
		return weather.GetTimeOfDayIcon(timeOfDay)
	}
}

// CalculateTimeOfDay determines the time of day based on the note time and sun events
func (h *Handlers) CalculateTimeOfDay(noteTime time.Time, sunEvents suncalc.SunEventTimes) weather.TimeOfDay {
	return weather.CalculateTimeOfDay(noteTime, sunEvents)
}

// TimeOfDayToInt converts a string representation of a time of day to a TimeOfDay value
func (h *Handlers) TimeOfDayToInt(s string) weather.TimeOfDay {
	return weather.StringToTimeOfDay(s)
}

// GetWeatherDescriptionFunc returns a function that returns a description for a given weather code
func (h *Handlers) GetWeatherDescriptionFunc() func(weatherCode string) string {
	return func(weatherCode string) string {
		// Strip 'd' or 'n' suffix if present
		if len(weatherCode) > 2 {
			weatherCode = weatherCode[:2]
		}
		iconCode := weather.IconCode(weatherCode)
		return weather.GetIconDescription(iconCode)
	}
}
