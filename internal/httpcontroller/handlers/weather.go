// internal/httpcontroller/weather_icons.go
package handlers

import (
	"html/template"
	"math"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
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
	return weather.GetTimeOfDayIcon
}

// CalculateTimeOfDay determines the time of day based on the note time and sun events
func (h *Handlers) CalculateTimeOfDay(noteTime time.Time, sunEvents *suncalc.SunEventTimes) weather.TimeOfDay {
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

// getSunEvents calculates sun events for a given date
func (h *Handlers) getSunEvents(date string, loc *time.Location) (suncalc.SunEventTimes, error) {
	// Parse the input date string into a time.Time object using the provided location
	dateTime, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		// If parsing fails, return an empty SunEventTimes and the error
		return suncalc.SunEventTimes{}, err
	}

	// Attempt to get sun event times using the SunCalc
	sunEvents, err := h.SunCalc.GetSunEventTimes(dateTime)
	if err != nil {
		// If sun events are not available, use default values
		return suncalc.SunEventTimes{
			CivilDawn: dateTime.Add(5 * time.Hour),  // Set civil dawn to 5:00 AM
			Sunrise:   dateTime.Add(6 * time.Hour),  // Set sunrise to 6:00 AM
			Sunset:    dateTime.Add(18 * time.Hour), // Set sunset to 6:00 PM
			CivilDusk: dateTime.Add(19 * time.Hour), // Set civil dusk to 7:00 PM
		}, nil
	}

	// Return the calculated sun events
	return sunEvents, nil
}

// findClosestWeather finds the closest hourly weather data to the given time
func findClosestWeather(noteTime time.Time, hourlyWeather []datastore.HourlyWeather) *datastore.HourlyWeather {
	// If there's no weather data, return nil
	if len(hourlyWeather) == 0 {
		return nil
	}

	// Initialize variables to track the closest weather data
	var closestWeather *datastore.HourlyWeather
	minDiff := time.Duration(math.MaxInt64)

	// Iterate through all hourly weather data
	for i := range hourlyWeather {
		// Calculate the absolute time difference between the note time and weather time
		diff := noteTime.Sub(hourlyWeather[i].Time).Abs()

		// If this difference is smaller than the current minimum, update the closest weather
		if diff < minDiff {
			minDiff = diff
			closestWeather = &hourlyWeather[i]
		}
	}

	// Return the weather data closest to the note time
	return closestWeather
}
