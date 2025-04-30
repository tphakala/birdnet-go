// internal/api/v2/weather.go
package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/suncalc"
	"gorm.io/gorm"
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
		if c.apiLogger != nil {
			c.apiLogger.Error("Missing date parameter in daily weather request",
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, echo.NewHTTPError(http.StatusBadRequest), "Date parameter is required", http.StatusBadRequest)
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Getting daily weather",
			"date", date,
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	// Get daily weather data from datastore
	dailyEvents, err := c.DS.GetDailyEvents(date)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to get daily weather data",
				"date", date,
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, err, "Failed to get daily weather data", http.StatusInternalServerError)
	}

	// Convert to response format using the helper function
	response := c.buildDailyWeatherResponse(dailyEvents)

	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieved daily weather data",
			"date", date,
			"sunrise", response.Sunrise.Format(time.RFC3339),
			"sunset", response.Sunset.Format(time.RFC3339),
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	return ctx.JSON(http.StatusOK, response)
}

// GetHourlyWeatherForDay handles GET /api/v2/weather/hourly/:date
// Retrieves all hourly weather data for a specific date
func (c *Controller) GetHourlyWeatherForDay(ctx echo.Context) error {
	date := ctx.Param("date")
	if date == "" {
		if c.apiLogger != nil {
			c.apiLogger.Error("Missing date parameter in hourly weather request",
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, echo.NewHTTPError(http.StatusBadRequest), "Date parameter is required", http.StatusBadRequest)
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Getting hourly weather for day",
			"date", date,
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	// Get hourly weather data from datastore
	hourlyWeather, err := c.DS.GetHourlyWeather(date)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to get hourly weather data",
				"date", date,
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
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

				if c.apiLogger != nil {
					c.apiLogger.Warn("No hourly weather data for future date",
						"date", date,
						"reason", reason,
						"path", ctx.Request().URL.Path,
						"ip", ctx.RealIP(),
					)
				}

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

			if c.apiLogger != nil {
				c.apiLogger.Error("Invalid date format in hourly weather request",
					"date", date,
					"error", parseErr.Error(),
					"path", ctx.Request().URL.Path,
					"ip", ctx.RealIP(),
				)
			}
		}

		// Log at warning level since missing data might indicate a system issue
		c.logger.Printf("WARN: [Weather API] %s (reason=%s, endpoint=GetHourlyWeatherForDay)",
			logInfo, reason)

		if c.apiLogger != nil {
			c.apiLogger.Warn("No hourly weather data found",
				"date", date,
				"reason", reason,
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}

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

	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieved hourly weather data",
			"date", date,
			"count", len(response),
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
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
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path
	id := ctx.Param("id")
	if id == "" {
		if c.apiLogger != nil {
			c.apiLogger.Error("Missing detection ID", "path", path, "ip", ip)
		}
		return c.HandleError(ctx, echo.NewHTTPError(http.StatusBadRequest), "Detection ID is required", http.StatusBadRequest)
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Getting weather for detection", "detection_id", id, "path", path, "ip", ip)
	}

	// 1. Get the detection
	note, err := c.DS.Get(id)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to get detection", "detection_id", id, "error", err.Error(), "path", path, "ip", ip)
		}
		// Determine if it's a not found error or other server error
		status := http.StatusInternalServerError
		msg := "Failed to get detection"
		if errors.Is(err, gorm.ErrRecordNotFound) { // Use errors.Is for robust check
			status = http.StatusNotFound
			msg = "Detection not found"
		}
		return c.HandleError(ctx, err, msg, status)
	}

	// 2. Get daily weather data (best effort)
	date := note.Date
	dailyEvents, err := c.DS.GetDailyEvents(date)
	if err != nil {
		c.logger.Printf("WARN: [Weather API] Failed to get daily weather data for detection %s, date %s: %v", id, date, err)
		if c.apiLogger != nil {
			c.apiLogger.Warn("Failed to get daily weather data for detection", "detection_id", id, "date", date, "error", err.Error(), "path", path, "ip", ip)
		}
		// Use zero-value struct if daily data fails
		dailyEvents = datastore.DailyEvents{Date: date} // Ensure date is still set
	}
	dailyResponse := c.buildDailyWeatherResponse(dailyEvents)

	// 3. Get hourly weather data for the day (best effort)
	hourlyWeatherList, err := c.DS.GetHourlyWeather(date)
	if err != nil {
		c.logger.Printf("WARN: [Weather API] Failed to get hourly weather data for detection %s, date %s: %v", id, date, err)
		if c.apiLogger != nil {
			c.apiLogger.Warn("Failed to get hourly weather data for detection", "detection_id", id, "date", date, "error", err.Error(), "path", path, "ip", ip)
		}
		// Continue with empty list if hourly data fails
		hourlyWeatherList = []datastore.HourlyWeather{}
	}

	// 4. Determine TimeOfDay
	detectionTime, timeOfDay, parseErr := c.determineTimeOfDayForDetection(&note, date, id)
	if parseErr != nil {
		// Error already logged in helper
		// Proceed with default timeOfDay ("Night") but maybe log the issue here too
		c.logger.Printf("WARN: [Weather API] Proceeding with default TimeOfDay for detection %s due to parsing/calculation error: %v", id, parseErr)
		if c.apiLogger != nil {
			c.apiLogger.Warn("Proceeding with default TimeOfDay due to parsing/calculation error", "detection_id", id, "error", parseErr.Error())
		}
	}

	// 5. Find Closest Hourly Weather
	closestHourlyData := HourlyWeatherResponse{}            // Default to empty struct
	if len(hourlyWeatherList) > 0 && detectionTime != nil { // Only search if we have data and a valid detection time
		closestHourlyData = c.findClosestHourlyWeather(*detectionTime, hourlyWeatherList)
	} else if len(hourlyWeatherList) > 0 {
		// Fallback if detectionTime parsing failed but we have hourly data: try matching by hour string
		hourStr := ""
		if len(note.Time) >= 2 {
			hourStr = note.Time[:2]
		}
		requestedHour, convErr := strconv.Atoi(hourStr)
		if convErr == nil {
			for i := range hourlyWeatherList {
				hw := &hourlyWeatherList[i]
				if hw.Time.Hour() == requestedHour { // Compare integer hours
					closestHourlyData = c.buildHourlyWeatherResponse(hw)
					break
				}
			}
		} else if c.apiLogger != nil {
			c.apiLogger.Error("Invalid hour derived from detection time during fallback", "detection_id", id, "hour", hourStr, "time", note.Time, "error", convErr.Error(), "path", path, "ip", ip)
		}
	}

	// 6. Build the combined response
	response := DetectionWeatherResponse{
		Daily:     dailyResponse,
		Hourly:    closestHourlyData,
		TimeOfDay: timeOfDay,
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieved weather for detection", "detection_id", id, "date", date, "time_of_day", timeOfDay, "path", path, "ip", ip)
	}

	// 7. Return JSON
	return ctx.JSON(http.StatusOK, response)
}

// determineTimeOfDayForDetection calculates the time of day string ("Day", "Night", etc.)
// It returns the parsed detection time, the calculated timeOfDay string, and any error during parsing or calculation.
func (c *Controller) determineTimeOfDayForDetection(note *datastore.Note, date, detectionID string) (*time.Time, string, error) {
	timeOfDay := "Night" // Default

	detectionTimeStr := date + " " + note.Time
	loc := time.Local // Assuming detection times are stored relative to local server time
	detectionTime, parseErr := time.ParseInLocation("2006-01-02 15:04:05", detectionTimeStr, loc)

	if parseErr != nil {
		c.logger.Printf("WARN: [Weather API] Failed to parse detection time '%s' in location %s for detection %s: %v. Cannot determine TimeOfDay accurately.", detectionTimeStr, loc.String(), detectionID, parseErr)
		if c.apiLogger != nil {
			c.apiLogger.Warn("Failed to parse detection time for TimeOfDay calculation", "detection_id", detectionID, "time_str", detectionTimeStr, "error", parseErr.Error())
		}
		return nil, timeOfDay, parseErr // Return default timeOfDay and the error
	}

	if c.SunCalc == nil {
		c.logger.Printf("WARN: [Weather API] SunCalc not initialized. Cannot determine TimeOfDay for detection %s.", detectionID)
		if c.apiLogger != nil {
			c.apiLogger.Warn("SunCalc not initialized for TimeOfDay calculation", "detection_id", detectionID)
		}
		return &detectionTime, timeOfDay, nil // Return parsed time and default timeOfDay
	}

	sunTimes, sunErr := c.SunCalc.GetSunEventTimes(detectionTime)
	if sunErr != nil {
		c.logger.Printf("WARN: [Weather API] Failed to get sun times for date %s for detection %s: %v. Cannot determine TimeOfDay.", date, detectionID, sunErr)
		if c.apiLogger != nil {
			c.apiLogger.Warn("Failed to get sun times for TimeOfDay calculation", "detection_id", detectionID, "date", date, "error", sunErr.Error())
		}
		return &detectionTime, timeOfDay, sunErr // Return parsed time, default timeOfDay, and suncalc error
	}

	// Successfully got sun times, calculate actual time of day
	timeOfDay = c.calculateTimeOfDay(detectionTime, &sunTimes)
	return &detectionTime, timeOfDay, nil // Return parsed time, calculated timeOfDay, and nil error
}

// findClosestHourlyWeather finds the hourly weather record closest to the detection time.
func (c *Controller) findClosestHourlyWeather(detectionTime time.Time, hourlyWeatherList []datastore.HourlyWeather) HourlyWeatherResponse {
	closestHourlyData := HourlyWeatherResponse{}
	if len(hourlyWeatherList) == 0 {
		return closestHourlyData // Return empty if no hourly data provided
	}

	loc := detectionTime.Location()                // Use the location from the detection time
	var closestDiff time.Duration = 24 * time.Hour // Initialize with a large duration
	found := false

	for i := range hourlyWeatherList {
		hw := &hourlyWeatherList[i]
		hwTime := hw.Time.In(loc) // Ensure comparison in the same location
		diff := hwTime.Sub(detectionTime)
		if diff < 0 {
			diff = -diff // Absolute difference
		}

		if diff < closestDiff {
			closestDiff = diff
			closestHourlyData = c.buildHourlyWeatherResponse(hw)
			found = true
		}
	}

	if !found {
		// This case should ideally not happen if hourlyWeatherList is not empty,
		// but good practice to handle. Log potentially?
		c.logger.Printf("WARN: [Weather API] No closest hourly weather found despite having %d records.", len(hourlyWeatherList))
		if c.apiLogger != nil {
			c.apiLogger.Warn("No closest hourly weather record found", "count", len(hourlyWeatherList))
		}
	}

	return closestHourlyData
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
	if c.apiLogger != nil {
		c.apiLogger.Info("Getting latest weather data",
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	// Get the latest hourly weather data
	latestWeather, err := c.DS.LatestHourlyWeather()
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to get latest weather data",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
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
		if c.apiLogger != nil {
			c.apiLogger.Warn("Failed to get daily weather data for latest weather",
				"date", date,
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
	} else {
		// Add daily data to response if available using the helper function
		dailyResponse := c.buildDailyWeatherResponse(dailyEvents)
		response.Daily = &dailyResponse
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieved latest weather data",
			"date", date,
			"temperature", latestWeather.Temperature,
			"has_daily", response.Daily != nil,
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
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
