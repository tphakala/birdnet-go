// internal/api/v2/weather.go
package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// DailyWeatherResponse represents the API response for daily weather data
type DailyWeatherResponse struct {
	Date     string    `json:"date"`
	Sunrise  time.Time `json:"sunrise"`
	Sunset   time.Time `json:"sunset"`
	Country  string    `json:"country,omitempty"`
	CityName string    `json:"city_name,omitempty"`
}

// HourlyWeatherResponse represents the API response for hourly weather data
type HourlyWeatherResponse struct {
	Time        string  `json:"time"`
	Temperature float64 `json:"temperature"`
	FeelsLike   float64 `json:"feels_like"`
	TempMin     float64 `json:"temp_min,omitempty"`
	TempMax     float64 `json:"temp_max,omitempty"`
	Pressure    int     `json:"pressure,omitempty"`
	Humidity    int     `json:"humidity,omitempty"`
	Visibility  int     `json:"visibility,omitempty"`
	WindSpeed   float64 `json:"wind_speed,omitempty"`
	WindDeg     int     `json:"wind_deg,omitempty"`
	WindGust    float64 `json:"wind_gust,omitempty"`
	Clouds      int     `json:"clouds,omitempty"`
	WeatherMain string  `json:"weather_main,omitempty"`
	WeatherDesc string  `json:"weather_desc,omitempty"`
	WeatherIcon string  `json:"weather_icon,omitempty"`
}

// DetectionWeatherResponse represents weather data associated with a detection
type DetectionWeatherResponse struct {
	Daily     DailyWeatherResponse  `json:"daily"`
	Hourly    HourlyWeatherResponse `json:"hourly"`
	TimeOfDay string                `json:"time_of_day"`
}

// initWeatherRoutes registers all weather-related API endpoints
func (c *Controller) initWeatherRoutes() {
	// Create weather API group
	weatherGroup := c.Group.Group("/weather")

	// TODO: Consider adding authentication middleware to protect these endpoints
	// Example: weatherGroup.Use(middlewares.RequireAuth())

	// TODO: Consider implementing rate limiting for these endpoints to prevent abuse
	// Example: weatherGroup.Use(middlewares.RateLimit(100, time.Hour))

	// Daily weather routes
	weatherGroup.GET("/daily/:date", c.GetDailyWeather)

	// Hourly weather routes
	weatherGroup.GET("/hourly/:date", c.GetHourlyWeatherForDay)
	weatherGroup.GET("/hourly/:date/:hour", c.GetHourlyWeatherForHour)

	// Weather for a specific detection
	weatherGroup.GET("/detection/:id", c.GetWeatherForDetection)

	// Latest weather data
	weatherGroup.GET("/latest", c.GetLatestWeather)
}

// buildDailyWeatherResponse creates a DailyWeatherResponse from a DailyEvents struct
// This helper function reduces code duplication and simplifies maintenance
func (c *Controller) buildDailyWeatherResponse(dailyEvents datastore.DailyEvents) DailyWeatherResponse {
	return DailyWeatherResponse{
		Date:     dailyEvents.Date,
		Sunrise:  time.Unix(dailyEvents.Sunrise, 0).UTC(),
		Sunset:   time.Unix(dailyEvents.Sunset, 0).UTC(),
		Country:  dailyEvents.Country,
		CityName: dailyEvents.CityName,
	}
}

// GetDailyWeather handles GET /api/v2/weather/daily/:date
// Retrieves daily weather data for a specific date
func (c *Controller) GetDailyWeather(ctx echo.Context) error {
	date := ctx.Param("date")
	if date == "" {
		return c.HandleError(ctx, echo.NewHTTPError(http.StatusBadRequest), "Date parameter is required", http.StatusBadRequest)
	}

	// Get daily weather data from datastore
	dailyEvents, err := c.DS.GetDailyEvents(date)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get daily weather data", http.StatusInternalServerError)
	}

	// Convert to response format using the helper function
	response := c.buildDailyWeatherResponse(dailyEvents)

	return ctx.JSON(http.StatusOK, response)
}

// GetHourlyWeatherForDay handles GET /api/v2/weather/hourly/:date
// Retrieves all hourly weather data for a specific date
func (c *Controller) GetHourlyWeatherForDay(ctx echo.Context) error {
	date := ctx.Param("date")
	if date == "" {
		return c.HandleError(ctx, echo.NewHTTPError(http.StatusBadRequest), "Date parameter is required", http.StatusBadRequest)
	}

	// Get hourly weather data from datastore
	hourlyWeather, err := c.DS.GetHourlyWeather(date)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get hourly weather data", http.StatusInternalServerError)
	}

	// Check if we got any data
	if len(hourlyWeather) == 0 {
		// Create structured log information as a formatted message
		logInfo := "No hourly weather data found for date: " + date
		reason := "missing_data"

		// Determine if this is a valid date but with no data, or potentially a future date
		requestedDate, parseErr := time.Parse("2006-01-02", date)
		if parseErr == nil {
			today := time.Now()

			if requestedDate.After(today) {
				// Future date
				reason = "future_date"
				logInfo = "No hourly weather data available for future date: " + date

				// Log at warning level since this might indicate a client issue
				c.logger.Printf("WARN: [Weather API] %s (reason=%s, endpoint=GetHourlyWeatherForDay)",
					logInfo, reason)

				return ctx.JSON(http.StatusOK, struct {
					Message string                  `json:"message"`
					Data    []HourlyWeatherResponse `json:"data"`
				}{
					Message: "No weather data available for future date",
					Data:    []HourlyWeatherResponse{},
				})
			}
		} else {
			logInfo += " (invalid date format, parse error: " + parseErr.Error() + ")"
		}

		// Log at warning level since missing data might indicate a system issue
		c.logger.Printf("WARN: [Weather API] %s (reason=%s, endpoint=GetHourlyWeatherForDay)",
			logInfo, reason)

		return ctx.JSON(http.StatusOK, struct {
			Message string                  `json:"message"`
			Data    []HourlyWeatherResponse `json:"data"`
		}{
			Message: "No weather data found for the specified date",
			Data:    []HourlyWeatherResponse{},
		})
	}

	// Convert to response format
	response := make([]HourlyWeatherResponse, 0, len(hourlyWeather))
	for i := range hourlyWeather {
		hw := &hourlyWeather[i]
		response = append(response, HourlyWeatherResponse{
			Time:        hw.Time.Format("15:04:05"),
			Temperature: hw.Temperature,
			FeelsLike:   hw.FeelsLike,
			TempMin:     hw.TempMin,
			TempMax:     hw.TempMax,
			Pressure:    hw.Pressure,
			Humidity:    hw.Humidity,
			Visibility:  hw.Visibility,
			WindSpeed:   hw.WindSpeed,
			WindDeg:     hw.WindDeg,
			WindGust:    hw.WindGust,
			Clouds:      hw.Clouds,
			WeatherMain: hw.WeatherMain,
			WeatherDesc: hw.WeatherDesc,
			WeatherIcon: hw.WeatherIcon,
		})
	}

	return ctx.JSON(http.StatusOK, struct {
		Data []HourlyWeatherResponse `json:"data"`
	}{
		Data: response,
	})
}

// GetHourlyWeatherForHour handles GET /api/v2/weather/hourly/:date/:hour
// Retrieves hourly weather data for a specific date and hour
func (c *Controller) GetHourlyWeatherForHour(ctx echo.Context) error {
	date := ctx.Param("date")
	hour := ctx.Param("hour")

	if date == "" || hour == "" {
		return c.HandleError(ctx, echo.NewHTTPError(http.StatusBadRequest), "Date and hour parameters are required", http.StatusBadRequest)
	}

	// Parse the requested hour to an integer
	requestedHour, err := strconv.Atoi(hour)
	if err != nil {
		return c.HandleError(ctx, echo.NewHTTPError(http.StatusBadRequest), "Invalid hour format", http.StatusBadRequest)
	}

	// Get hourly weather data for the day
	hourlyWeather, err := c.DS.GetHourlyWeather(date)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get hourly weather data", http.StatusInternalServerError)
	}

	// Find the weather data for the requested hour
	var targetHourData *HourlyWeatherResponse
	for i := range hourlyWeather {
		hw := &hourlyWeather[i]
		storedHourStr := hw.Time.Format("15")
		storedHour, err := strconv.Atoi(storedHourStr)
		if err != nil {
			return c.HandleError(ctx, echo.NewHTTPError(http.StatusInternalServerError),
				"Invalid stored hour format", http.StatusInternalServerError)
		}

		if storedHour == requestedHour {
			response := HourlyWeatherResponse{
				Time:        hw.Time.Format("15:04:05"),
				Temperature: hw.Temperature,
				FeelsLike:   hw.FeelsLike,
				TempMin:     hw.TempMin,
				TempMax:     hw.TempMax,
				Pressure:    hw.Pressure,
				Humidity:    hw.Humidity,
				Visibility:  hw.Visibility,
				WindSpeed:   hw.WindSpeed,
				WindDeg:     hw.WindDeg,
				WindGust:    hw.WindGust,
				Clouds:      hw.Clouds,
				WeatherMain: hw.WeatherMain,
				WeatherDesc: hw.WeatherDesc,
				WeatherIcon: hw.WeatherIcon,
			}
			targetHourData = &response
			break
		}
	}

	if targetHourData == nil {
		return c.HandleError(ctx, echo.NewHTTPError(http.StatusNotFound), "Weather data not found for specified hour", http.StatusNotFound)
	}

	return ctx.JSON(http.StatusOK, targetHourData)
}

// GetWeatherForDetection handles GET /api/v2/weather/detection/:id
// Retrieves weather data associated with a specific detection.
//
// This is the preferred endpoint for retrieving weather data for a detection.
// Frontend applications should first request detection data from the detections API,
// then use this endpoint to separately retrieve the associated weather data.
// This allows for more efficient data loading and keeps concerns separated.
func (c *Controller) GetWeatherForDetection(ctx echo.Context) error {
	id := ctx.Param("id")
	if id == "" {
		return c.HandleError(ctx, echo.NewHTTPError(http.StatusBadRequest), "Detection ID is required", http.StatusBadRequest)
	}

	// Get the detection
	note, err := c.DS.Get(id)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get detection", http.StatusInternalServerError)
	}

	// Get the date and hour from the detection
	date := note.Date
	hour := ""
	if len(note.Time) >= 2 {
		hour = note.Time[:2]
	}

	// Get daily weather data (best effort)
	dailyEvents, err := c.DS.GetDailyEvents(date)
	if err != nil {
		c.logger.Printf("WARN: [Weather API] Failed to get daily weather data for detection %s, date %s: %v", id, date, err)
		dailyEvents = datastore.DailyEvents{}
	}
	dailyResponse := c.buildDailyWeatherResponse(dailyEvents)

	// Get hourly weather data (best effort)
	hourlyWeather, err := c.DS.GetHourlyWeather(date)
	if err != nil {
		c.logger.Printf("WARN: [Weather API] Failed to get hourly weather data for detection %s, date %s: %v", id, date, err)
		hourlyWeather = []datastore.HourlyWeather{}
	}

	// --- Calculate TimeOfDay dynamically ---
	timeOfDay := "Night" // Default

	detectionTimeStr := date + " " + note.Time
	loc := time.Local
	detectionTime, err := time.ParseInLocation("2006-01-02 15:04:05", detectionTimeStr, loc)
	switch {
	case err != nil:
		c.logger.Printf("WARN: [Weather API] Failed to parse detection time '%s' in location %s for detection %s: %v. Cannot determine TimeOfDay accurately.", detectionTimeStr, loc.String(), id, err)
	case c.SunCalc == nil:
		c.logger.Printf("WARN: [Weather API] SunCalc not initialized. Cannot determine TimeOfDay for detection %s.", id)
	default:
		sunTimes, sunErr := c.SunCalc.GetSunEventTimes(detectionTime)
		if sunErr != nil {
			c.logger.Printf("WARN: [Weather API] Failed to get sun times for date %s for detection %s: %v. Cannot determine TimeOfDay.", date, id, sunErr)
		} else {
			timeOfDay = c.calculateTimeOfDay(detectionTime, &sunTimes)
		}
	}
	// --- End TimeOfDay Calculation ---

	// Find the closest hourly weather to the detection time
	var closestHourlyData HourlyWeatherResponse
	if len(hourlyWeather) > 0 {
		if err == nil {
			var closestDiff time.Duration = 24 * time.Hour
			for i := range hourlyWeather {
				hw := &hourlyWeather[i]
				hwTime := hw.Time.In(loc)
				diff := hwTime.Sub(detectionTime)
				if diff < 0 {
					diff = -diff
				}

				if diff < closestDiff {
					closestDiff = diff
					closestHourlyData = c.buildHourlyWeatherResponse(hw)
				}
			}
		} else {
			requestedHour, parseErr := strconv.Atoi(hour)
			if parseErr == nil {
				for i := range hourlyWeather {
					hw := &hourlyWeather[i]
					if hw.Time.In(loc).Hour() == requestedHour {
						closestHourlyData = c.buildHourlyWeatherResponse(hw)
						break
					}
				}
			} else {
				c.logger.Printf("ERROR: [Weather API] Invalid hour '%s' derived from detection time '%s' for detection %s", hour, note.Time, id)
			}
		}
	}

	// Build the combined response
	response := DetectionWeatherResponse{
		Daily:     dailyResponse,
		Hourly:    closestHourlyData,
		TimeOfDay: timeOfDay,
	}

	return ctx.JSON(http.StatusOK, response)
}

// buildHourlyWeatherResponse creates an HourlyWeatherResponse from an HourlyWeather struct
func (c *Controller) buildHourlyWeatherResponse(hw *datastore.HourlyWeather) HourlyWeatherResponse {
	return HourlyWeatherResponse{
		Time:        hw.Time.Format("15:04:05"), // Consider if Timezone matters here
		Temperature: hw.Temperature,
		FeelsLike:   hw.FeelsLike,
		TempMin:     hw.TempMin,
		TempMax:     hw.TempMax,
		Pressure:    hw.Pressure,
		Humidity:    hw.Humidity,
		Visibility:  hw.Visibility,
		WindSpeed:   hw.WindSpeed,
		WindDeg:     hw.WindDeg,
		WindGust:    hw.WindGust,
		Clouds:      hw.Clouds,
		WeatherMain: hw.WeatherMain,
		WeatherDesc: hw.WeatherDesc,
		WeatherIcon: hw.WeatherIcon,
	}
}

// GetLatestWeather handles GET /api/v2/weather/latest
// Retrieves the latest available weather data
func (c *Controller) GetLatestWeather(ctx echo.Context) error {
	// Get the latest hourly weather data
	latestWeather, err := c.DS.LatestHourlyWeather()
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get latest weather data", http.StatusInternalServerError)
	}

	// Get the date from the latest weather
	date := latestWeather.Time.Format("2006-01-02")

	// Build response with hourly data
	response := struct {
		Daily  *DailyWeatherResponse `json:"daily"`
		Hourly HourlyWeatherResponse `json:"hourly"`
		Time   string                `json:"timestamp"`
	}{
		// Initialize with nil daily data, will be populated if available
		Daily: nil,
		// Always include hourly data since we have it
		Hourly: HourlyWeatherResponse{
			Time:        latestWeather.Time.Format("15:04:05"),
			Temperature: latestWeather.Temperature,
			FeelsLike:   latestWeather.FeelsLike,
			TempMin:     latestWeather.TempMin,
			TempMax:     latestWeather.TempMax,
			Pressure:    latestWeather.Pressure,
			Humidity:    latestWeather.Humidity,
			Visibility:  latestWeather.Visibility,
			WindSpeed:   latestWeather.WindSpeed,
			WindDeg:     latestWeather.WindDeg,
			WindGust:    latestWeather.WindGust,
			Clouds:      latestWeather.Clouds,
			WeatherMain: latestWeather.WeatherMain,
			WeatherDesc: latestWeather.WeatherDesc,
			WeatherIcon: latestWeather.WeatherIcon,
		},
		Time: time.Now().Format(time.RFC3339),
	}

	// Try to get daily weather data for this date
	dailyEvents, err := c.DS.GetDailyEvents(date)
	if err != nil {
		// Log the error but continue with partial response
		c.logger.Printf("WARN: [Weather API] Failed to get daily weather data for date %s: %v (endpoint=GetLatestWeather)",
			date, err)
	} else {
		// Add daily data to response if available using the helper function
		dailyResponse := c.buildDailyWeatherResponse(dailyEvents)
		response.Daily = &dailyResponse
	}

	return ctx.JSON(http.StatusOK, response)
}

// calculateTimeOfDay determines the time of day based on the detection time and sun events
func (c *Controller) calculateTimeOfDay(detectionTime time.Time, sunEvents *suncalc.SunEventTimes) string {
	// Convert all times to the same format for comparison
	detTime := detectionTime.Format("15:04:05")
	sunriseTime := sunEvents.Sunrise.Format("15:04:05")
	sunsetTime := sunEvents.Sunset.Format("15:04:05")

	// Define sunrise/sunset window (30 minutes before and after)
	sunriseStart := sunEvents.Sunrise.Add(-30 * time.Minute).Format("15:04:05")
	sunriseEnd := sunEvents.Sunrise.Add(30 * time.Minute).Format("15:04:05")
	sunsetStart := sunEvents.Sunset.Add(-30 * time.Minute).Format("15:04:05")
	sunsetEnd := sunEvents.Sunset.Add(30 * time.Minute).Format("15:04:05")

	switch {
	case detTime >= sunriseStart && detTime <= sunriseEnd:
		return "Sunrise"
	case detTime >= sunsetStart && detTime <= sunsetEnd:
		return "Sunset"
	case detTime >= sunriseTime && detTime < sunsetTime:
		return "Day"
	default:
		return "Night"
	}
}
