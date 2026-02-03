// internal/api/v2/weather.go
package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/datastore"
	errors_pkg "github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/suncalc"
	"gorm.io/gorm"
)

// Weather constants (file-local)
const (
	timePeriodNight        = "Night"
	minTimeStringLength    = 2  // Minimum length for parsing hour from time string
	weatherSunWindowMinute = 30 // Minutes before/after sunrise/sunset for weather
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

	// Sun times endpoint using SunCalc
	weatherGroup.GET("/sun/:date", c.GetSunTimes)
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
		c.logErrorIfEnabled("Missing date parameter in daily weather request",
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, echo.NewHTTPError(http.StatusBadRequest), "Date parameter is required", http.StatusBadRequest)
	}

	c.logInfoIfEnabled("Getting daily weather",
		logger.String("date", date),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	// Get daily weather data from datastore
	dailyEvents, err := c.DS.GetDailyEvents(date)
	if err != nil {
		c.logErrorIfEnabled("Failed to get daily weather data",
			logger.String("date", date),
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, "Failed to get daily weather data", http.StatusInternalServerError)
	}

	// Convert to response format using the helper function
	response := c.buildDailyWeatherResponse(dailyEvents)

	c.logInfoIfEnabled("Retrieved daily weather data",
		logger.String("date", date),
		logger.String("sunrise", response.Sunrise.Format(time.RFC3339)),
		logger.String("sunset", response.Sunset.Format(time.RFC3339)),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	return ctx.JSON(http.StatusOK, response)
}

// GetHourlyWeatherForDay handles GET /api/v2/weather/hourly/:date
// Retrieves all hourly weather data for a specific date
func (c *Controller) GetHourlyWeatherForDay(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	date := ctx.Param("date")
	if date == "" {
		c.logErrorIfEnabled("Missing date parameter in hourly weather request", logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, echo.NewHTTPError(http.StatusBadRequest), "Date parameter is required", http.StatusBadRequest)
	}

	c.logInfoIfEnabled("Getting hourly weather for day", logger.String("date", date), logger.String("path", path), logger.String("ip", ip))

	hourlyWeather, err := c.DS.GetHourlyWeather(date)
	if err != nil {
		c.logErrorIfEnabled("Failed to get hourly weather data", logger.String("date", date), logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, err, "Failed to get hourly weather data", http.StatusInternalServerError)
	}

	if len(hourlyWeather) == 0 {
		return c.handleEmptyHourlyWeather(ctx, date, ip, path)
	}

	response := c.buildHourlyWeatherResponseList(hourlyWeather)
	c.logInfoIfEnabled("Retrieved hourly weather data", logger.String("date", date), logger.Int("count", len(response)), logger.String("path", path), logger.String("ip", ip))

	return ctx.JSON(http.StatusOK, struct {
		Data []HourlyWeatherResponse `json:"data"`
	}{
		Data: response,
	})
}

// handleEmptyHourlyWeather handles the case when no hourly weather data is found
func (c *Controller) handleEmptyHourlyWeather(ctx echo.Context, date, ip, path string) error {
	emptyResponse := struct {
		Message string                  `json:"message"`
		Data    []HourlyWeatherResponse `json:"data"`
	}{
		Data: []HourlyWeatherResponse{},
	}

	// Check if it's a future date
	requestedDate, parseErr := time.Parse(time.DateOnly, date)
	if parseErr != nil {
		c.logErrorIfEnabled("Invalid date format in hourly weather request", logger.String("date", date), logger.Error(parseErr), logger.String("path", path), logger.String("ip", ip))
		emptyResponse.Message = "No weather data found for the specified date"
		return ctx.JSON(http.StatusOK, emptyResponse)
	}

	if requestedDate.After(time.Now()) {
		c.logWarnIfEnabled("No hourly weather data for future date", logger.String("date", date), logger.String("reason", "future_date"), logger.String("path", path), logger.String("ip", ip))
		emptyResponse.Message = "No weather data available for future date"
		return ctx.JSON(http.StatusOK, emptyResponse)
	}

	c.logWarnIfEnabled("No hourly weather data found", logger.String("date", date), logger.String("reason", "missing_data"), logger.String("path", path), logger.String("ip", ip))
	emptyResponse.Message = "No weather data found for the specified date"
	return ctx.JSON(http.StatusOK, emptyResponse)
}

// buildHourlyWeatherResponseList converts hourly weather data to response format
func (c *Controller) buildHourlyWeatherResponseList(hourlyWeather []datastore.HourlyWeather) []HourlyWeatherResponse {
	response := make([]HourlyWeatherResponse, 0, len(hourlyWeather))
	for i := range hourlyWeather {
		response = append(response, c.buildHourlyWeatherResponse(&hourlyWeather[i]))
	}
	return response
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
		storedHourStr := hw.Time.In(time.Local).Format("15")
		storedHour, err := strconv.Atoi(storedHourStr)
		if err != nil {
			return c.HandleError(ctx, echo.NewHTTPError(http.StatusInternalServerError),
				"Invalid stored hour format", http.StatusInternalServerError)
		}

		if storedHour == requestedHour {
			response := c.buildHourlyWeatherResponse(hw)
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
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	id := ctx.Param("id")
	if id == "" {
		c.logErrorIfEnabled("Missing detection ID", logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, echo.NewHTTPError(http.StatusBadRequest), "Detection ID is required", http.StatusBadRequest)
	}

	c.logInfoIfEnabled("Getting weather for detection", logger.String("detection_id", id), logger.String("path", path), logger.String("ip", ip))

	// Get the detection
	note, err := c.DS.Get(id)
	if err != nil {
		return c.handleDetectionFetchError(ctx, err, id, ip, path)
	}

	// Fetch weather data and build response
	date := note.Date
	dailyResponse := c.fetchDailyWeatherForDetection(date, id, ip, path)
	hourlyWeatherList := c.fetchHourlyWeatherForDetection(date, id, ip, path)
	detectionTime, timeOfDay := c.getDetectionTimeOfDay(&note, date, id)
	closestHourlyData := c.findClosestHourlyWeatherWithFallback(detectionTime, hourlyWeatherList, &note, id, ip, path)

	response := DetectionWeatherResponse{
		Daily:     dailyResponse,
		Hourly:    closestHourlyData,
		TimeOfDay: timeOfDay,
	}

	c.logInfoIfEnabled("Retrieved weather for detection", logger.String("detection_id", id), logger.String("date", date), logger.String("time_of_day", timeOfDay), logger.String("path", path), logger.String("ip", ip))
	return ctx.JSON(http.StatusOK, response)
}

// handleDetectionFetchError handles errors when fetching a detection
func (c *Controller) handleDetectionFetchError(ctx echo.Context, err error, id, ip, path string) error {
	c.logErrorIfEnabled("Failed to get detection", logger.String("detection_id", id), logger.Error(err), logger.String("path", path), logger.String("ip", ip))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.HandleError(ctx, err, "Detection not found", http.StatusNotFound)
	}
	return c.HandleError(ctx, err, "Failed to get detection", http.StatusInternalServerError)
}

// fetchDailyWeatherForDetection fetches daily weather data with error handling
func (c *Controller) fetchDailyWeatherForDetection(date, id, ip, path string) DailyWeatherResponse {
	dailyEvents, err := c.DS.GetDailyEvents(date)
	if err != nil {
		c.logWarnIfEnabled("Failed to get daily weather data for detection", logger.String("detection_id", id), logger.String("date", date), logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		dailyEvents = datastore.DailyEvents{Date: date}
	}
	return c.buildDailyWeatherResponse(dailyEvents)
}

// fetchHourlyWeatherForDetection fetches hourly weather data with error handling
func (c *Controller) fetchHourlyWeatherForDetection(date, id, ip, path string) []datastore.HourlyWeather {
	hourlyWeatherList, err := c.DS.GetHourlyWeather(date)
	if err != nil {
		c.logWarnIfEnabled("Failed to get hourly weather data for detection", logger.String("detection_id", id), logger.String("date", date), logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return []datastore.HourlyWeather{}
	}
	return hourlyWeatherList
}

// getDetectionTimeOfDay determines time of day for a detection
func (c *Controller) getDetectionTimeOfDay(note *datastore.Note, date, id string) (detectionTime *time.Time, timeOfDay string) {
	detectionTime, timeOfDay, parseErr := c.determineTimeOfDayForDetection(note, date, id)
	if parseErr != nil {
		c.logWarnIfEnabled("Proceeding with default TimeOfDay due to parsing/calculation error", logger.String("detection_id", id), logger.Error(parseErr))
	}
	return detectionTime, timeOfDay
}

// findClosestHourlyWeatherWithFallback finds closest hourly weather with fallback logic
func (c *Controller) findClosestHourlyWeatherWithFallback(detectionTime *time.Time, hourlyWeatherList []datastore.HourlyWeather, note *datastore.Note, id, ip, path string) HourlyWeatherResponse {
	if len(hourlyWeatherList) == 0 {
		return HourlyWeatherResponse{}
	}

	// Primary: use detection time if available
	if detectionTime != nil {
		return c.findClosestHourlyWeather(*detectionTime, hourlyWeatherList)
	}

	// Fallback: match by hour string
	return c.findHourlyWeatherByHourString(hourlyWeatherList, note.Time, id, ip, path)
}

// findHourlyWeatherByHourString finds hourly weather by matching hour from time string
func (c *Controller) findHourlyWeatherByHourString(hourlyWeatherList []datastore.HourlyWeather, timeStr, id, ip, path string) HourlyWeatherResponse {
	if len(timeStr) < minTimeStringLength {
		return HourlyWeatherResponse{}
	}

	hourStr := timeStr[:2]
	requestedHour, convErr := strconv.Atoi(hourStr)
	if convErr != nil {
		c.logErrorIfEnabled("Invalid hour derived from detection time during fallback", logger.String("detection_id", id), logger.String("hour", hourStr), logger.String("time", timeStr), logger.Error(convErr), logger.String("path", path), logger.String("ip", ip))
		return HourlyWeatherResponse{}
	}

	for i := range hourlyWeatherList {
		if hourlyWeatherList[i].Time.Hour() == requestedHour {
			return c.buildHourlyWeatherResponse(&hourlyWeatherList[i])
		}
	}
	return HourlyWeatherResponse{}
}

// determineTimeOfDayForDetection calculates the time of day string ("Day", "Night", etc.)
// It returns the parsed detection time, the calculated timeOfDay string, and any error during parsing or calculation.
func (c *Controller) determineTimeOfDayForDetection(note *datastore.Note, date, detectionID string) (*time.Time, string, error) {
	timeOfDay := timePeriodNight // Default

	detectionTimeStr := date + " " + note.Time
	detectionTime, parseErr := time.ParseInLocation("2006-01-02 15:04:05", detectionTimeStr, time.Local)
	if parseErr != nil {
		c.logWarnIfEnabled("Failed to parse detection time for TimeOfDay calculation", logger.String("detection_id", detectionID), logger.String("time_str", detectionTimeStr), logger.Error(parseErr))
		return nil, timeOfDay, parseErr
	}

	if c.SunCalc == nil {
		c.logWarnIfEnabled("SunCalc not initialized for TimeOfDay calculation", logger.String("detection_id", detectionID))
		return &detectionTime, timeOfDay, nil
	}

	sunTimes, sunErr := c.SunCalc.GetSunEventTimes(detectionTime)
	if sunErr != nil {
		c.logWarnIfEnabled("Failed to get sun times for TimeOfDay calculation", logger.String("detection_id", detectionID), logger.String("date", date), logger.Error(sunErr))
		return &detectionTime, timeOfDay, sunErr
	}

	timeOfDay = c.calculateTimeOfDay(detectionTime, &sunTimes)
	return &detectionTime, timeOfDay, nil
}

// findClosestHourlyWeather finds the hourly weather record closest to the detection time.
func (c *Controller) findClosestHourlyWeather(detectionTime time.Time, hourlyWeatherList []datastore.HourlyWeather) HourlyWeatherResponse {
	closestHourlyData := HourlyWeatherResponse{}
	if len(hourlyWeatherList) == 0 {
		return closestHourlyData // Return empty if no hourly data provided
	}

	loc := detectionTime.Location()  // Use the location from the detection time
	var closestDiff = 24 * time.Hour // Initialize with a large duration
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
		// This case should ideally not happen if hourlyWeatherList is not empty
		c.logWarnIfEnabled("No closest hourly weather record found despite having data", logger.Int("count", len(hourlyWeatherList)))
	}

	return closestHourlyData
}

// buildHourlyWeatherResponse creates an HourlyWeatherResponse from an HourlyWeather struct
func (c *Controller) buildHourlyWeatherResponse(hw *datastore.HourlyWeather) HourlyWeatherResponse {
	return HourlyWeatherResponse{
		Time:        hw.Time.In(time.Local).Format(time.TimeOnly),
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
	c.logInfoIfEnabled("Getting latest weather data",
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	// Get the latest hourly weather data
	latestWeather, err := c.DS.LatestHourlyWeather()
	if err != nil {
		c.logErrorIfEnabled("Failed to get latest weather data",
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, "Failed to get latest weather data", http.StatusInternalServerError)
	}

	// Get the date from the latest weather (convert to local time for correct date)
	date := latestWeather.Time.In(time.Local).Format(time.DateOnly)

	// Build response with hourly data
	response := struct {
		Daily  *DailyWeatherResponse `json:"daily"`
		Hourly HourlyWeatherResponse `json:"hourly"`
		Time   string                `json:"timestamp"`
	}{
		Daily:  nil, // Will be populated if available
		Hourly: c.buildHourlyWeatherResponse(latestWeather),
		Time:   time.Now().Format(time.RFC3339),
	}

	// Try to get daily weather data for this date
	dailyEvents, err := c.DS.GetDailyEvents(date)
	if err != nil {
		// Log the error but continue with partial response
		c.logWarnIfEnabled("Failed to get daily weather data for latest weather",
			logger.String("date", date),
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
	} else {
		// Add daily data to response if available using the helper function
		dailyResponse := c.buildDailyWeatherResponse(dailyEvents)
		response.Daily = &dailyResponse
	}

	c.logInfoIfEnabled("Retrieved latest weather data",
		logger.String("date", date),
		logger.Float64("temperature", latestWeather.Temperature),
		logger.Bool("has_daily", response.Daily != nil),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	return ctx.JSON(http.StatusOK, response)
}

// calculateTimeOfDay determines the time of day based on the detection time and sun events
func (c *Controller) calculateTimeOfDay(detectionTime time.Time, sunEvents *suncalc.SunEventTimes) string {
	// Convert all times to the same format for comparison
	detTime := detectionTime.Format(time.TimeOnly)
	sunriseTime := sunEvents.Sunrise.Format(time.TimeOnly)
	sunsetTime := sunEvents.Sunset.Format(time.TimeOnly)

	// Define sunrise/sunset window (30 minutes before and after)
	sunriseStart := sunEvents.Sunrise.Add(-weatherSunWindowMinute * time.Minute).Format(time.TimeOnly)
	sunriseEnd := sunEvents.Sunrise.Add(weatherSunWindowMinute * time.Minute).Format(time.TimeOnly)
	sunsetStart := sunEvents.Sunset.Add(-weatherSunWindowMinute * time.Minute).Format(time.TimeOnly)
	sunsetEnd := sunEvents.Sunset.Add(weatherSunWindowMinute * time.Minute).Format(time.TimeOnly)

	switch {
	case detTime >= sunriseStart && detTime <= sunriseEnd:
		return "Sunrise"
	case detTime >= sunsetStart && detTime <= sunsetEnd:
		return "Sunset"
	case detTime >= sunriseTime && detTime < sunsetTime:
		return "Day"
	default:
		return timePeriodNight
	}
}

// SunTimesResponse represents the API response for sun times
type SunTimesResponse struct {
	Date      string    `json:"date"`
	Sunrise   time.Time `json:"sunrise"`
	Sunset    time.Time `json:"sunset"`
	CivilDawn time.Time `json:"civil_dawn"`
	CivilDusk time.Time `json:"civil_dusk"`
}

// GetSunTimes handles GET /api/v2/weather/sun/:date
// Calculates sunrise and sunset times for a specific date using SunCalc
func (c *Controller) GetSunTimes(ctx echo.Context) error {
	date := ctx.Param("date")
	if date == "" {
		c.logErrorIfEnabled("Missing date parameter in sun times request",
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, echo.NewHTTPError(http.StatusBadRequest), "Date parameter is required", http.StatusBadRequest)
	}

	// Validate date format
	parsedDate, err := time.Parse(time.DateOnly, date)
	if err != nil {
		c.logErrorIfEnabled("Invalid date format in sun times request",
			logger.String("date", date),
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, "Invalid date format. Use YYYY-MM-DD", http.StatusBadRequest)
	}

	c.logInfoIfEnabled("Getting sun times",
		logger.String("date", date),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	// Check if SunCalc is available
	if c.SunCalc == nil {
		c.logErrorIfEnabled("SunCalc not initialized",
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, errors_pkg.New(errors.New("sun calculator not available")).
			Component("weather_api").
			Category(errors_pkg.CategoryConfiguration).
			Build(), "Sun calculator not initialized", http.StatusInternalServerError)
	}

	// Calculate sun times using SunCalc
	sunTimes, err := c.SunCalc.GetSunEventTimes(parsedDate)
	if err != nil {
		c.logErrorIfEnabled("Failed to calculate sun times",
			logger.String("date", date),
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, "Failed to calculate sun times", http.StatusInternalServerError)
	}

	// Build response
	response := SunTimesResponse{
		Date:      date,
		Sunrise:   sunTimes.Sunrise,
		Sunset:    sunTimes.Sunset,
		CivilDawn: sunTimes.CivilDawn,
		CivilDusk: sunTimes.CivilDusk,
	}

	c.logInfoIfEnabled("Calculated sun times",
		logger.String("date", date),
		logger.String("sunrise", response.Sunrise.Format(time.RFC3339)),
		logger.String("sunset", response.Sunset.Format(time.RFC3339)),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	return ctx.JSON(http.StatusOK, response)
}
