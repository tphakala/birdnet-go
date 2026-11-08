// Package weather is the api/v2 weather domain handler. It owns the
// /api/v2/weather/* endpoints (daily/hourly/latest weather, weather for a
// detection, sun times, moon phase). The Handler embeds *apicore.Core by
// pointer so the shared dependencies and helpers (DS, SunCalc, HandleError, the
// logging helpers) promote onto it; the facade constructs one Handler and calls
// RegisterRoutes to wire the routes in their existing order.
package weather

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/suncalc"
	"gorm.io/gorm"
)

// Weather constants (package-local)
const (
	invalidDateMessage  = "Invalid date format. Use YYYY-MM-DD"
	noHourlyDataMessage = "No weather data found for the specified date"
	futureDateMessage   = "No weather data available for future date"

	// invalidDateParamKey is the error_params key the invalid-date translation interpolates.
	invalidDateParamKey = "paramName"
)

// Handler serves the weather domain endpoints. It embeds *apicore.Core BY
// POINTER so the shared Core members promote onto it without re-wiring; Core
// carries atomic/lock-bearing fields and must never be copied by value.
type Handler struct {
	*apicore.Core
}

// New builds a weather Handler around the shared core.
func New(core *apicore.Core) *Handler {
	return &Handler{core}
}

// RegisterRoutes registers all weather-related API endpoints on the supplied
// API v2 group, preserving the exact routes and order the facade used before
// the weather domain was extracted.
func (c *Handler) RegisterRoutes(g *echo.Group) {
	// Create weather API group
	weatherGroup := g.Group("/weather")

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

	// Moon phase endpoint
	weatherGroup.GET("/moon/:date", c.GetMoonPhase)
}

// dailyWeatherResponse represents the API response for daily weather data
type dailyWeatherResponse struct {
	Date     string    `json:"date"`
	Sunrise  time.Time `json:"sunrise"`
	Sunset   time.Time `json:"sunset"`
	Country  string    `json:"country,omitempty"`
	CityName string    `json:"city_name,omitempty"`
}

// hourlyWeatherResponse represents the API response for hourly weather data
type hourlyWeatherResponse struct {
	Time              string  `json:"time"`
	Temperature       float64 `json:"temperature"`
	FeelsLike         float64 `json:"feels_like"`
	TempMin           float64 `json:"temp_min,omitempty"`
	TempMax           float64 `json:"temp_max,omitempty"`
	Pressure          int     `json:"pressure,omitempty"`
	Humidity          int     `json:"humidity,omitempty"`
	Visibility        int     `json:"visibility,omitempty"`
	WindSpeed         float64 `json:"wind_speed,omitempty"`
	WindDeg           int     `json:"wind_deg,omitempty"`
	WindGust          float64 `json:"wind_gust,omitempty"`
	Clouds            int     `json:"clouds,omitempty"`
	Precipitation     float64 `json:"precipitation,omitempty"`
	PrecipitationType string  `json:"precipitation_type,omitempty"`
	WeatherMain       string  `json:"weather_main,omitempty"`
	WeatherDesc       string  `json:"weather_desc,omitempty"`
	WeatherIcon       string  `json:"weather_icon,omitempty"`
}

// detectionWeatherResponse represents weather data associated with a detection
type detectionWeatherResponse struct {
	Daily     dailyWeatherResponse  `json:"daily"`
	Hourly    hourlyWeatherResponse `json:"hourly"`
	TimeOfDay string                `json:"time_of_day"`
}

// moonResponse holds moon phase data for API responses.
type moonResponse struct {
	Phase        float64 `json:"phase"`        // 0-27.99, raw phase value
	PhaseName    string  `json:"phase_name"`   // e.g. "Full Moon"
	Illumination float64 `json:"illumination"` // 0-100 percentage
	IconName     string  `json:"icon_name"`    // Basmilius icon name e.g. "moon-full"
}

// buildDailyWeatherResponse creates a dailyWeatherResponse from a DailyEvents struct
// This helper function reduces code duplication and simplifies maintenance
func (c *Handler) buildDailyWeatherResponse(dailyEvents *datastore.DailyEvents) dailyWeatherResponse {
	return dailyWeatherResponse{
		Date:     dailyEvents.Date,
		Sunrise:  time.Unix(dailyEvents.Sunrise, 0).In(time.Local),
		Sunset:   time.Unix(dailyEvents.Sunset, 0).In(time.Local),
		Country:  dailyEvents.Country,
		CityName: dailyEvents.CityName,
	}
}

// parseDateParam parses a :date route parameter as a calendar date in YYYY-MM-DD form
// (time.DateOnly), returning midnight of that date in the server's local zone. Any other
// shape and any impossible date, such as 2026-02-30, is an error.
func parseDateParam(date string) (time.Time, error) {
	return time.ParseInLocation(time.DateOnly, date, time.Local)
}

// respondInvalidDate writes the 400 response for a :date parameter that parseDateParam
// rejected, with the shared invalid-date translation key.
func (c *Handler) respondInvalidDate(ctx echo.Context, err error) error {
	return c.HandleErrorWithKey(ctx, err, invalidDateMessage, http.StatusBadRequest,
		notification.MsgErrDetectionInvalidDate, map[string]any{invalidDateParamKey: "date"})
}

// isFutureDate reports whether requested, a midnight returned by parseDateParam, falls on a
// calendar day after the day now falls on in requested's zone.
func isFutureDate(requested, now time.Time) bool {
	loc := requested.Location()
	y, m, d := now.In(loc).Date()
	return requested.After(time.Date(y, m, d, 0, 0, 0, 0, loc))
}

// GetDailyWeather handles GET /api/v2/weather/daily/:date
// Retrieves daily weather data for a specific date
func (c *Handler) GetDailyWeather(ctx echo.Context) error {
	date := ctx.Param("date")
	if date == "" {
		c.LogErrorIfEnabled("Missing date parameter in daily weather request",
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, echo.NewHTTPError(http.StatusBadRequest), "Date parameter is required", http.StatusBadRequest)
	}

	if _, err := parseDateParam(date); err != nil {
		return c.respondInvalidDate(ctx, err)
	}

	c.LogInfoIfEnabled("Getting daily weather",
		logger.String("date", date),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	// Get daily weather data from datastore
	dailyEvents, err := c.DS.GetDailyEvents(date)
	if err != nil {
		c.LogErrorIfEnabled("Failed to get daily weather data",
			logger.String("date", date),
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, "Failed to get daily weather data", http.StatusInternalServerError)
	}

	// Convert to response format using the helper function
	response := c.buildDailyWeatherResponse(&dailyEvents)

	c.LogInfoIfEnabled("Retrieved daily weather data",
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
func (c *Handler) GetHourlyWeatherForDay(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	date := ctx.Param("date")
	if date == "" {
		c.LogErrorIfEnabled("Missing date parameter in hourly weather request", logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, echo.NewHTTPError(http.StatusBadRequest), "Date parameter is required", http.StatusBadRequest)
	}

	requested, err := parseDateParam(date)
	if err != nil {
		return c.respondInvalidDate(ctx, err)
	}

	c.LogInfoIfEnabled("Getting hourly weather for day", logger.String("date", date), logger.String("path", path), logger.String("ip", ip))

	hourlyWeather, err := c.DS.GetHourlyWeather(date)
	if err != nil {
		c.LogErrorIfEnabled("Failed to get hourly weather data", logger.String("date", date), logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, err, "Failed to get hourly weather data", http.StatusInternalServerError)
	}

	if len(hourlyWeather) == 0 {
		return c.handleEmptyHourlyWeather(ctx, date, requested, ip, path)
	}

	response := c.buildHourlyWeatherResponseList(hourlyWeather)
	c.LogInfoIfEnabled("Retrieved hourly weather data", logger.String("date", date), logger.Int("count", len(response)), logger.String("path", path), logger.String("ip", ip))

	return ctx.JSON(http.StatusOK, struct {
		Data []hourlyWeatherResponse `json:"data"`
	}{
		Data: response,
	})
}

// handleEmptyHourlyWeather handles the case when no hourly weather data is found. The caller
// has validated date and passes its parsed local midnight as requested.
func (c *Handler) handleEmptyHourlyWeather(ctx echo.Context, date string, requested time.Time, ip, path string) error {
	emptyResponse := struct {
		Message string                  `json:"message"`
		Data    []hourlyWeatherResponse `json:"data"`
	}{
		Data: []hourlyWeatherResponse{},
	}

	// Check if it's a future date
	if isFutureDate(requested, time.Now()) {
		c.LogWarnIfEnabled("No hourly weather data for future date", logger.String("date", date), logger.String("reason", "future_date"), logger.String("path", path), logger.String("ip", ip))
		emptyResponse.Message = futureDateMessage
		return ctx.JSON(http.StatusOK, emptyResponse)
	}

	c.LogWarnIfEnabled("No hourly weather data found", logger.String("date", date), logger.String("reason", "missing_data"), logger.String("path", path), logger.String("ip", ip))
	emptyResponse.Message = noHourlyDataMessage
	return ctx.JSON(http.StatusOK, emptyResponse)
}

// buildHourlyWeatherResponseList converts hourly weather data to response format
func (c *Handler) buildHourlyWeatherResponseList(hourlyWeather []datastore.HourlyWeather) []hourlyWeatherResponse {
	response := make([]hourlyWeatherResponse, 0, len(hourlyWeather))
	for i := range hourlyWeather {
		response = append(response, c.buildHourlyWeatherResponse(&hourlyWeather[i]))
	}
	return response
}

// GetHourlyWeatherForHour handles GET /api/v2/weather/hourly/:date/:hour
// Retrieves hourly weather data for a specific date and hour
func (c *Handler) GetHourlyWeatherForHour(ctx echo.Context) error {
	date := ctx.Param("date")
	hour := ctx.Param("hour")

	if date == "" || hour == "" {
		return c.HandleError(ctx, echo.NewHTTPError(http.StatusBadRequest), "Date and hour parameters are required", http.StatusBadRequest)
	}

	if _, err := parseDateParam(date); err != nil {
		return c.respondInvalidDate(ctx, err)
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
	var targetHourData *hourlyWeatherResponse
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
func (c *Handler) GetWeatherForDetection(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	id := ctx.Param("id")
	if id == "" {
		c.LogErrorIfEnabled("Missing detection ID", logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, echo.NewHTTPError(http.StatusBadRequest), "Detection ID is required", http.StatusBadRequest)
	}

	c.LogInfoIfEnabled("Getting weather for detection", logger.String("detection_id", id), logger.String("path", path), logger.String("ip", ip))

	// Get the detection through the repository: its Timestamp is the stored
	// instant, so both occurrences of a repeated DST hour match their own weather.
	r, err := c.Repo.Get(ctx.Request().Context(), id)
	if err != nil {
		return c.handleDetectionFetchError(ctx, err, id, ip, path)
	}

	// Fetch weather data and build response
	date := r.Date()
	dailyResponse := c.fetchDailyWeatherForDetection(date, id, ip, path)
	hourlyWeatherList := c.fetchHourlyWeatherForDetection(date, id, ip, path)
	timeOfDay := c.getDetectionTimeOfDay(r.Timestamp, date, id)
	closestHourlyData := c.findClosestHourlyWeatherForDetection(r.Timestamp, hourlyWeatherList)

	response := detectionWeatherResponse{
		Daily:     dailyResponse,
		Hourly:    closestHourlyData,
		TimeOfDay: timeOfDay,
	}

	c.LogInfoIfEnabled("Retrieved weather for detection", logger.String("detection_id", id), logger.String("date", date), logger.String("time_of_day", timeOfDay), logger.String("path", path), logger.String("ip", ip))
	return ctx.JSON(http.StatusOK, response)
}

// handleDetectionFetchError handles errors when fetching a detection
func (c *Handler) handleDetectionFetchError(ctx echo.Context, err error, id, ip, path string) error {
	c.LogErrorIfEnabled("Failed to get detection", logger.String("detection_id", id), logger.Error(err), logger.String("path", path), logger.String("ip", ip))
	// The legacy bridge reports a missing row as gorm.ErrRecordNotFound, the v2 repository as
	// repository.ErrDetectionNotFound.
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, repository.ErrDetectionNotFound) {
		return c.HandleError(ctx, err, "Detection not found", http.StatusNotFound)
	}
	return c.HandleError(ctx, err, "Failed to get detection", http.StatusInternalServerError)
}

// fetchDailyWeatherForDetection fetches daily weather data with error handling
func (c *Handler) fetchDailyWeatherForDetection(date, id, ip, path string) dailyWeatherResponse {
	dailyEvents, err := c.DS.GetDailyEvents(date)
	if err != nil {
		c.LogWarnIfEnabled("Failed to get daily weather data for detection", logger.String("detection_id", id), logger.String("date", date), logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		dailyEvents = datastore.DailyEvents{Date: date}
	}
	return c.buildDailyWeatherResponse(&dailyEvents)
}

// fetchHourlyWeatherForDetection fetches hourly weather data with error handling
func (c *Handler) fetchHourlyWeatherForDetection(date, id, ip, path string) []datastore.HourlyWeather {
	hourlyWeatherList, err := c.DS.GetHourlyWeather(date)
	if err != nil {
		c.LogWarnIfEnabled("Failed to get hourly weather data for detection", logger.String("detection_id", id), logger.String("date", date), logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return []datastore.HourlyWeather{}
	}
	return hourlyWeatherList
}

// getDetectionTimeOfDay determines time of day for a detection
func (c *Handler) getDetectionTimeOfDay(detectionTime time.Time, date, id string) string {
	timeOfDay, calcErr := c.determineTimeOfDayForDetection(detectionTime, date, id)
	if calcErr != nil {
		c.LogWarnIfEnabled("Proceeding with default TimeOfDay due to calculation error", logger.String("detection_id", id), logger.Error(calcErr))
	}
	return timeOfDay
}

// findClosestHourlyWeatherForDetection returns the hourly weather closest to the
// detection's stored instant, or an empty response when there is no hourly data.
func (c *Handler) findClosestHourlyWeatherForDetection(detectionTime time.Time, hourlyWeatherList []datastore.HourlyWeather) hourlyWeatherResponse {
	if len(hourlyWeatherList) == 0 {
		return hourlyWeatherResponse{}
	}
	return c.findClosestHourlyWeather(detectionTime, hourlyWeatherList)
}

// determineTimeOfDayForDetection calculates the time of day string ("day", "night", etc.)
// for the detection's stored instant. It returns the default (night) together with
// an error when the sun times cannot be calculated.
func (c *Handler) determineTimeOfDayForDetection(detectionTime time.Time, date, detectionID string) (string, error) {
	timeOfDay := datastore.TimeOfDayNight // Default

	if c.SunCalc == nil {
		c.LogWarnIfEnabled("SunCalc not initialized for TimeOfDay calculation", logger.String("detection_id", detectionID))
		return timeOfDay, nil
	}

	sunTimes, sunErr := c.SunCalc.GetSunEventTimes(detectionTime)
	if sunErr != nil {
		c.LogWarnIfEnabled("Failed to get sun times for TimeOfDay calculation", logger.String("detection_id", detectionID), logger.String("date", date), logger.Error(sunErr))
		return timeOfDay, sunErr
	}

	return suncalc.ClassifyTimeOfDay(detectionTime, &sunTimes), nil
}

// ClosestHourlyWeather returns the hourly weather record closest in time to
// detectionTime, or nil when the list is empty or no record falls within a day
// of detectionTime. It is a free function (no Core state) so other api/v2
// domains (e.g. detections, which enriches its detection responses with
// weather) can reuse the weather domain's matching logic by importing this
// package, without depending on a constructed Handler instance.
//
// The returned pointer aliases an element of hourlyWeatherList; callers that
// keep it must not mutate or let the backing slice outlive their use of it.
func ClosestHourlyWeather(detectionTime time.Time, hourlyWeatherList []datastore.HourlyWeather) *datastore.HourlyWeather {
	loc := detectionTime.Location()  // Use the location from the detection time
	var closestDiff = 24 * time.Hour // Initialize with a large duration
	var closest *datastore.HourlyWeather

	for i := range hourlyWeatherList {
		hw := &hourlyWeatherList[i]
		hwTime := hw.Time.In(loc) // Ensure comparison in the same location
		diff := hwTime.Sub(detectionTime)
		if diff < 0 {
			diff = -diff // Absolute difference
		}

		if diff < closestDiff {
			closestDiff = diff
			closest = hw
		}
	}

	return closest
}

// findClosestHourlyWeather finds the hourly weather record closest to the
// detection time and converts it to the API response shape.
func (c *Handler) findClosestHourlyWeather(detectionTime time.Time, hourlyWeatherList []datastore.HourlyWeather) hourlyWeatherResponse {
	closest := ClosestHourlyWeather(detectionTime, hourlyWeatherList)
	if closest == nil {
		if len(hourlyWeatherList) > 0 {
			// This case should ideally not happen if hourlyWeatherList is not empty
			c.LogWarnIfEnabled("No closest hourly weather record found despite having data", logger.Int("count", len(hourlyWeatherList)))
		}
		return hourlyWeatherResponse{}
	}
	return c.buildHourlyWeatherResponse(closest)
}

// buildHourlyWeatherResponse creates an hourlyWeatherResponse from an HourlyWeather struct
func (c *Handler) buildHourlyWeatherResponse(hw *datastore.HourlyWeather) hourlyWeatherResponse {
	return hourlyWeatherResponse{
		Time:              hw.Time.In(time.Local).Format(time.TimeOnly),
		Temperature:       hw.Temperature,
		FeelsLike:         hw.FeelsLike,
		TempMin:           hw.TempMin,
		TempMax:           hw.TempMax,
		Pressure:          hw.Pressure,
		Humidity:          hw.Humidity,
		Visibility:        hw.Visibility,
		WindSpeed:         hw.WindSpeed,
		WindDeg:           hw.WindDeg,
		WindGust:          hw.WindGust,
		Clouds:            hw.Clouds,
		Precipitation:     hw.Precipitation,
		PrecipitationType: hw.PrecipitationType,
		WeatherMain:       hw.WeatherMain,
		WeatherDesc:       hw.WeatherDesc,
		WeatherIcon:       hw.WeatherIcon,
	}
}

// GetLatestWeather handles GET /api/v2/weather/latest
// Retrieves the latest available weather data
func (c *Handler) GetLatestWeather(ctx echo.Context) error {
	c.LogInfoIfEnabled("Getting latest weather data",
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	// Get the latest hourly weather data
	latestWeather, err := c.DS.LatestHourlyWeather()
	if err != nil {
		c.LogErrorIfEnabled("Failed to get latest weather data",
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
		Daily  *dailyWeatherResponse `json:"daily"`
		Hourly hourlyWeatherResponse `json:"hourly"`
		Moon   *moonResponse         `json:"moon,omitempty"`
		Time   string                `json:"timestamp"`
	}{
		Daily:  nil, // Will be populated if available
		Hourly: c.buildHourlyWeatherResponse(latestWeather),
		Time:   latestWeather.Time.Format(time.RFC3339),
	}

	// Try to get daily weather data for this date
	dailyEvents, err := c.DS.GetDailyEvents(date)
	if err != nil {
		// Log the error but continue with partial response
		c.LogWarnIfEnabled("Failed to get daily weather data for latest weather",
			logger.String("date", date),
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
	} else {
		// Add daily data to response if available using the helper function
		dailyResponse := c.buildDailyWeatherResponse(&dailyEvents)
		response.Daily = &dailyResponse
	}

	// Compute moon phase from the weather snapshot timestamp for consistency
	moonData := suncalc.GetMoonPhase(latestWeather.Time)
	response.Moon = &moonResponse{
		Phase:        moonData.Phase,
		PhaseName:    moonData.PhaseName,
		Illumination: moonData.Illumination,
		IconName:     moonData.IconName,
	}

	c.LogInfoIfEnabled("Retrieved latest weather data",
		logger.String("date", date),
		logger.Float64("temperature", latestWeather.Temperature),
		logger.Bool("has_daily", response.Daily != nil),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	return ctx.JSON(http.StatusOK, response)
}

// GetMoonPhase handles GET /api/v2/weather/moon/:date
// Returns the moon phase for a given date.
func (c *Handler) GetMoonPhase(ctx echo.Context) error {
	date := ctx.Param("date")

	parsedDate, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return c.HandleError(ctx, err, "Invalid date format, expected YYYY-MM-DD", http.StatusBadRequest)
	}

	moonData := suncalc.GetMoonPhase(parsedDate)

	return ctx.JSON(http.StatusOK, moonResponse{
		Phase:        moonData.Phase,
		PhaseName:    moonData.PhaseName,
		Illumination: moonData.Illumination,
		IconName:     moonData.IconName,
	})
}

// sunTimesResponse represents the API response for sun times
type sunTimesResponse struct {
	Date      string    `json:"date"`
	Sunrise   time.Time `json:"sunrise"`
	Sunset    time.Time `json:"sunset"`
	CivilDawn time.Time `json:"civil_dawn"`
	CivilDusk time.Time `json:"civil_dusk"`
	Timezone  string    `json:"timezone"` // IANA timezone name derived from observer coordinates
}

// GetSunTimes handles GET /api/v2/weather/sun/:date
// Calculates sunrise and sunset times for a specific date using SunCalc
func (c *Handler) GetSunTimes(ctx echo.Context) error {
	date := ctx.Param("date")
	if date == "" {
		c.LogErrorIfEnabled("Missing date parameter in sun times request",
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, echo.NewHTTPError(http.StatusBadRequest), "Date parameter is required", http.StatusBadRequest)
	}

	// Validate date format
	parsedDate, err := time.Parse(time.DateOnly, date)
	if err != nil {
		c.LogErrorIfEnabled("Invalid date format in sun times request",
			logger.String("date", date),
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, invalidDateMessage, http.StatusBadRequest)
	}

	c.LogInfoIfEnabled("Getting sun times",
		logger.String("date", date),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	// Check if SunCalc is available
	if c.SunCalc == nil {
		c.LogErrorIfEnabled("SunCalc not initialized",
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, errors.New(errors.NewStd("sun calculator not available")).
			Component("weather_api").
			Category(errors.CategoryConfiguration).
			Build(), "Sun calculator not initialized", http.StatusInternalServerError)
	}

	// Calculate sun times using SunCalc
	sunTimes, err := c.SunCalc.GetSunEventTimes(parsedDate)
	if err != nil {
		c.LogErrorIfEnabled("Failed to calculate sun times",
			logger.String("date", date),
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, "Failed to calculate sun times", http.StatusInternalServerError)
	}

	// Build response. The timezone is taken from the returned times, which carry the zone of the
	// state they were computed from: asking the calculator again could observe a location
	// changed in between and label these times with the new location's zone.
	response := sunTimesResponse{
		Date:      date,
		Sunrise:   sunTimes.Sunrise,
		Sunset:    sunTimes.Sunset,
		CivilDawn: sunTimes.CivilDawn,
		CivilDusk: sunTimes.CivilDusk,
		Timezone:  sunTimes.Sunrise.Location().String(),
	}

	c.LogInfoIfEnabled("Calculated sun times",
		logger.String("date", date),
		logger.String("sunrise", response.Sunrise.Format(time.RFC3339)),
		logger.String("sunset", response.Sunset.Format(time.RFC3339)),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	return ctx.JSON(http.StatusOK, response)
}
