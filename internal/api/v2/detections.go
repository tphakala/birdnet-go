// internal/api/v2/detections.go
package api

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/patrickmn/go-cache"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// Detection constants (file-local)
const (
	detectionCacheExpiry  = 5 * time.Minute  // Default cache expiration
	detectionCacheCleanup = 10 * time.Minute // Cache cleanup interval
	defaultNumResults     = 100              // Default number of results
	maxNumResults         = 1000             // Maximum number of results
	sunEventWindowMinutes = 30               // Minutes before/after sunrise/sunset
	minHourRangeParts     = 2                // Minimum parts for hour range parsing
)

// Regex to validate YYYY-MM-DD format and check for unwanted characters
var validDateRegex = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)

// Function to validate date string format and content
// Returns nil for empty strings (treating them as optional parameters)
// Callers should implement additional checks if the parameter is required.
func validateDateParam(dateStr, paramName string) error {
	if dateStr == "" {
		return nil // Optional parameter, no error if empty
	}

	// Basic format check first
	if !validDateRegex.MatchString(dateStr) {
		return fmt.Errorf("invalid %s format, use YYYY-MM-DD", paramName)
	}

	// The regex above already ensures only digits and hyphens in correct positions
	// so the strings.ContainsAny check below is redundant and has been removed

	// Try parsing the date to catch invalid dates like 2023-02-30
	// This also catches dates with timezone annotations like 2024-04-05Z
	_, err := time.Parse(time.DateOnly, dateStr)
	if err != nil {
		return fmt.Errorf("invalid date value for %s: %w", paramName, err)
	}

	return nil
}

// verificationStatus represents the result of parsing a verification string.
type verificationStatus struct {
	IsSet    bool // whether verification was requested
	Verified bool // the verification value (true=correct, false=false_positive)
}

// parseVerificationStatus converts a verification string to a structured result.
// Returns (status, nil) for valid inputs, or (empty, error) for invalid status.
func parseVerificationStatus(status string) (verificationStatus, error) {
	if status == "" {
		return verificationStatus{IsSet: false}, nil
	}
	switch status {
	case "correct":
		return verificationStatus{IsSet: true, Verified: true}, nil
	case "false_positive":
		return verificationStatus{IsSet: true, Verified: false}, nil
	default:
		return verificationStatus{}, fmt.Errorf("invalid verification status: %s", status)
	}
}

// checkDetectionNotLocked verifies a detection is not locked, checking both in-memory and database.
// Returns true if locked (error response already sent), false if unlocked.
func (c *Controller) checkDetectionNotLocked(ctx echo.Context, idStr string, inMemoryLocked bool) bool {
	// Check in-memory lock state first
	if inMemoryLocked {
		_ = c.HandleError(ctx, fmt.Errorf("detection is locked"),
			"Detection is locked and status cannot be changed", http.StatusConflict)
		return true
	}

	// Check database for race condition (another process may have locked it)
	isLocked, err := c.DS.IsNoteLocked(idStr)
	if err != nil {
		_ = c.HandleError(ctx, err, "Failed to check lock status", http.StatusInternalServerError)
		return true
	}
	if isLocked {
		_ = c.HandleError(ctx, fmt.Errorf("detection is locked"),
			"Detection is locked and status cannot be changed", http.StatusConflict)
		return true
	}

	return false
}

// initDetectionRoutes registers all detection-related API endpoints
func (c *Controller) initDetectionRoutes() {
	// Initialize the cache with a 5-minute default expiration and 10-minute cleanup interval
	c.detectionCache = cache.New(detectionCacheExpiry, detectionCacheCleanup)

	// Detection endpoints - publicly accessible
	//
	// Note: Detection data is decoupled from weather data by design.
	// To get weather information for a specific detection, use the
	// /api/v2/weather/detection/:id endpoint after fetching the detection.
	c.Group.GET("/detections", c.GetDetections)
	c.Group.GET("/detections/:id", c.GetDetection)
	c.Group.GET("/detections/recent", c.GetRecentDetections)
	c.Group.GET("/detections/:id/time-of-day", c.GetDetectionTimeOfDay)

	// Protected detection management endpoints
	detectionGroup := c.Group.Group("/detections", c.authMiddleware)
	detectionGroup.DELETE("/:id", c.DeleteDetection)
	detectionGroup.POST("/:id/review", c.ReviewDetection)
	detectionGroup.POST("/:id/lock", c.LockDetection)
	detectionGroup.POST("/ignore", c.IgnoreSpecies)
	detectionGroup.GET("/ignored", c.GetExcludedSpecies)
}

// CommentResponse represents a comment on a detection in the API response
type CommentResponse struct {
	ID        uint   `json:"id"`
	Entry     string `json:"entry"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// DetectionResponse represents a detection in the API response
type DetectionResponse struct {
	ID                 uint              `json:"id"`
	Date               string            `json:"date"`
	Time               string            `json:"time"`
	Source             string            `json:"source"`
	BeginTime          string            `json:"beginTime"`
	EndTime            string            `json:"endTime"`
	SpeciesCode        string            `json:"speciesCode"`
	ScientificName     string            `json:"scientificName"`
	CommonName         string            `json:"commonName"`
	Confidence         float64           `json:"confidence"`
	Verified           string            `json:"verified"`
	Locked             bool              `json:"locked"`
	Comments           []CommentResponse `json:"comments,omitempty"`
	Weather            *WeatherInfo      `json:"weather,omitempty"`
	TimeOfDay          string            `json:"timeOfDay,omitempty"`
	IsNewSpecies       bool              `json:"isNewSpecies,omitempty"`       // First seen within tracking window
	DaysSinceFirstSeen int               `json:"daysSinceFirstSeen,omitempty"` // Days since species was first detected

	// Multi-period tracking metadata
	IsNewThisYear   bool   `json:"isNewThisYear,omitempty"`   // First time this year
	IsNewThisSeason bool   `json:"isNewThisSeason,omitempty"` // First time this season
	DaysThisYear    int    `json:"daysThisYear,omitempty"`    // Days since first this year
	DaysThisSeason  int    `json:"daysThisSeason,omitempty"`  // Days since first this season
	CurrentSeason   string `json:"currentSeason,omitempty"`   // Current season name
}

// WeatherInfo represents weather data for a detection
type WeatherInfo struct {
	WeatherIcon string  `json:"weatherIcon"`
	WeatherMain string  `json:"weatherMain,omitempty"`
	Description string  `json:"description,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	WindSpeed   float64 `json:"windSpeed,omitempty"`
	WindGust    float64 `json:"windGust,omitempty"`
	Humidity    int     `json:"humidity,omitempty"`
	Units       string  `json:"units,omitempty"`
}

// DetectionRequest represents the query parameters for listing detections
type DetectionRequest struct {
	Comment       string `json:"comment,omitempty"`
	Verified      string `json:"verified,omitempty"`
	IgnoreSpecies string `json:"ignoreSpecies,omitempty"`
	Locked        bool   `json:"locked,omitempty"`
	LockDetection bool   `json:"lock_detection,omitempty"`
}

// PaginatedResponse represents a paginated API response
type PaginatedResponse struct {
	Data        any   `json:"data"`
	Total       int64 `json:"total"`
	Limit       int   `json:"limit"`
	Offset      int   `json:"offset"`
	CurrentPage int   `json:"current_page"`
	TotalPages  int   `json:"total_pages"`
}

// TimeOfDayResponse represents the time of day response for a detection
type TimeOfDayResponse struct {
	TimeOfDay string `json:"timeOfDay"`
}

// detectionQueryParams holds all query parameters for detection requests
type detectionQueryParams struct {
	Date       string
	Hour       string
	Duration   int
	Species    string
	Search     string
	StartDate  string
	EndDate    string
	NumResults int
	Offset     int
	QueryType  string
	// Advanced filter parameters
	Confidence string
	TimeOfDay  string
	HourRange  string
	Verified   string
	Location   string
	Locked     string
	// Include additional data
	IncludeWeather bool
}

// advancedSearchCacheKey generates a deterministic cache key for advanced search queries.
// Includes all filter parameters to avoid cache collisions.
func (p *detectionQueryParams) advancedSearchCacheKey() string {
	return fmt.Sprintf("adv_search:%s:%d:%d:%s:%s:%s:%s:%s:%s:%s:%s:%s",
		p.Search, p.NumResults, p.Offset,
		p.Confidence, p.TimeOfDay, p.HourRange,
		p.Verified, p.Location, p.Locked,
		p.Species, p.Date, p.StartDate+":"+p.EndDate)
}

// parseDetectionQueryParams extracts and validates query parameters from the request
func (c *Controller) parseDetectionQueryParams(ctx echo.Context) (*detectionQueryParams, error) {
	params := &detectionQueryParams{
		Date:      ctx.QueryParam("date"),
		Hour:      ctx.QueryParam("hour"),
		Species:   ctx.QueryParam("species"),
		Search:    ctx.QueryParam("search"),
		StartDate: ctx.QueryParam("start_date"),
		EndDate:   ctx.QueryParam("end_date"),
		QueryType: ctx.QueryParam("queryType"),
		// Advanced filter parameters
		Confidence: ctx.QueryParam("confidence"),
		TimeOfDay:  ctx.QueryParam("timeOfDay"),
		HourRange:  ctx.QueryParam("hourRange"),
		Verified:   ctx.QueryParam("verified"),
		Location:   ctx.QueryParam("location"),
		Locked:     ctx.QueryParam("locked"),
		// Include weather data
		IncludeWeather: ctx.QueryParam("includeWeather") == QueryValueTrue,
	}

	// Parse duration
	duration, _ := strconv.Atoi(ctx.QueryParam("duration"))
	if duration <= 0 {
		duration = 1
	}
	params.Duration = duration

	// Validate dates
	if err := c.validateDateParameters(params.StartDate, params.EndDate, ctx); err != nil {
		return nil, err
	}

	// Parse and validate numResults
	numResults, err := c.parseNumResults(ctx.QueryParam("numResults"))
	if err != nil {
		return nil, err
	}
	params.NumResults = numResults

	// Parse and validate offset
	offset, err := c.parseOffset(ctx.QueryParam("offset"))
	if err != nil {
		return nil, err
	}
	params.Offset = offset

	// Validate hour parameter based on query type
	if params.QueryType == "hourly" {
		// Hourly queries require a single valid integer hour (0-23), not a range
		if params.Hour == "" {
			return nil, echo.NewHTTPError(http.StatusBadRequest, "hour parameter is required for hourly query type")
		}
		if h, err := strconv.Atoi(params.Hour); err != nil || h < 0 || h > 23 {
			return nil, echo.NewHTTPError(http.StatusBadRequest, "invalid hour parameter: must be a single integer between 0 and 23")
		}
	} else if params.Hour != "" {
		// Other query types: hour is optional, but if present must be valid (ranges OK)
		if parseHourFilter(params.Hour) == nil {
			return nil, echo.NewHTTPError(http.StatusBadRequest, "invalid hour parameter: must be a valid hour (0-23) or hour range (e.g. 6-9)")
		}
	}

	return params, nil
}

// validateDateParameters validates start_date and end_date parameters
func (c *Controller) validateDateParameters(startDateStr, endDateStr string, ctx echo.Context) error {
	// Validate individual date formats
	for _, dp := range []struct{ value, name string }{{startDateStr, "start_date"}, {endDateStr, "end_date"}} {
		if err := validateDateParam(dp.value, dp.name); err != nil {
			c.logErrorIfEnabled("Invalid date parameter",
				logger.String("parameter", dp.name),
				logger.String("value", dp.value),
				logger.String("path", ctx.Request().URL.Path),
				logger.String("ip", ctx.RealIP()))
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
	}

	// Check date order
	if err := validateDateOrder(startDateStr, endDateStr); err != nil {
		c.logErrorIfEnabled("Invalid date range",
			logger.String("start_date", startDateStr),
			logger.String("end_date", endDateStr),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()))
		return echo.NewHTTPError(http.StatusBadRequest, "start_date cannot be after end_date")
	}

	return nil
}

// parseNumResults parses and validates the numResults parameter
func (c *Controller) parseNumResults(numResultsStr string) (int, error) {
	if numResultsStr == "" {
		return defaultNumResults, nil // Default value
	}

	c.logDebugIfEnabled("GetDetections: Raw numResults string",
		logger.String("value", numResultsStr),
	)
	numResults, err := strconv.Atoi(numResultsStr)
	if err != nil {
		c.logDebugIfEnabled("GetDetections: Invalid numResults string",
			logger.String("value", numResultsStr),
			logger.Error(err),
		)
		// Log the enhanced error for telemetry while returning a simpler error for HTTP response
		// This pattern allows detailed internal tracking without exposing complex error structures to API clients
		_ = errors.Newf("invalid numeric value for numResults: %v", err).
			Component("api").
			Category(errors.CategoryValidation).
			Context("parameter", "numResults").
			Context("value", numResultsStr).
			Build()
		return 0, fmt.Errorf("Invalid numeric value for numResults: %w", err) //nolint:staticcheck // matches test expectations
	}

	c.logDebugIfEnabled("GetDetections: Parsed numResults value",
		logger.Int("value", numResults),
	)
	if numResults <= 0 {
		c.logDebugIfEnabled("GetDetections: Zero or negative numResults value",
			logger.Int("value", numResults),
		)
		// Log the enhanced error for telemetry while returning a simpler error for HTTP response
		// This pattern allows detailed internal tracking without exposing complex error structures to API clients
		_ = errors.New(errors.NewStd("numResults must be greater than zero")).
			Component("api").
			Category(errors.CategoryValidation).
			Context("parameter", "numResults").
			Context("value", numResults).
			Build()
		return 0, errors.NewStd("numResults must be greater than zero")
	}

	if numResults > maxNumResults {
		c.logDebugIfEnabled("GetDetections: Too large numResults value",
			logger.Int("value", numResults),
		)
		// Log the enhanced error for telemetry while returning a simpler error for HTTP response
		// This pattern allows detailed internal tracking without exposing complex error structures to API clients
		_ = errors.New(errors.NewStd("numResults exceeds maximum allowed value (1000)")).
			Component("api").
			Category(errors.CategoryValidation).
			Context("parameter", "numResults").
			Context("value", numResults).
			Build()
		return 0, errors.NewStd("numResults exceeds maximum allowed value (1000)")
	}

	return numResults, nil
}

// parseOffset parses and validates the offset parameter
func (c *Controller) parseOffset(offsetStr string) (int, error) {
	if offsetStr == "" {
		return 0, nil // Default value
	}

	c.logDebugIfEnabled("GetDetections: Raw offset string",
		logger.String("value", offsetStr),
	)
	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		c.logDebugIfEnabled("GetDetections: Invalid offset string",
			logger.String("value", offsetStr),
			logger.Error(err),
		)
		// Log the enhanced error for telemetry
		_ = errors.Newf("invalid numeric value for offset: %v", err).
			Component("api").
			Category(errors.CategoryValidation).
			Context("parameter", "offset").
			Context("value", offsetStr).
			Build()
		return 0, fmt.Errorf("Invalid numeric value for offset: %w", err) //nolint:staticcheck // matches test expectations
	}

	c.logDebugIfEnabled("GetDetections: Parsed offset value",
		logger.Int("value", offset),
	)
	if offset < 0 {
		c.logDebugIfEnabled("GetDetections: Negative offset value",
			logger.Int("value", offset),
		)
		// Log the enhanced error for telemetry
		_ = errors.New(errors.NewStd("offset cannot be negative")).
			Component("api").
			Category(errors.CategoryValidation).
			Context("parameter", "offset").
			Context("value", offset).
			Build()
		return 0, errors.NewStd("offset cannot be negative")
	}

	const maxOffset = 1000000
	if offset > maxOffset {
		c.logDebugIfEnabled("GetDetections: Too large offset value",
			logger.Int("value", offset),
		)
		// Log the enhanced error for telemetry
		_ = errors.Newf("offset exceeds maximum allowed value (%d)", maxOffset).
			Component("api").
			Category(errors.CategoryValidation).
			Context("parameter", "offset").
			Context("value", offset).
			Context("max_allowed", maxOffset).
			Build()
		return 0, fmt.Errorf("offset exceeds maximum allowed value (%d)", maxOffset)
	}

	return offset, nil
}

// GetDetections handles GET requests for detections
func (c *Controller) GetDetections(ctx echo.Context) error {
	// Parse and validate query parameters
	params, err := c.parseDetectionQueryParams(ctx)
	if err != nil {
		c.logErrorIfEnabled("Failed to parse query parameters",
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Log the retrieval attempt
	c.logInfoIfEnabled("Retrieving detections",
		logger.String("queryType", params.QueryType),
		logger.String("date", params.Date),
		logger.String("hour", params.Hour),
		logger.Int("duration", params.Duration),
		logger.String("species", params.Species),
		logger.String("search", params.Search),
		logger.String("start_date", params.StartDate),
		logger.String("end_date", params.EndDate),
		logger.Int("limit", params.NumResults),
		logger.Int("offset", params.Offset),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	// Get notes based on query type
	notes, totalResults, err := c.getDetectionsByQueryType(params)
	if err != nil {
		c.logErrorIfEnabled("Failed to retrieve detections",
			logger.String("queryType", params.QueryType),
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	// Convert notes to response format
	detections := c.convertNotesToDetectionResponses(notes, params.IncludeWeather)

	// Create paginated response
	response := c.createPaginatedResponse(detections, totalResults, params.NumResults, params.Offset)

	// Log the successful response
	c.logInfoIfEnabled("Detections retrieved successfully",
		logger.String("queryType", params.QueryType),
		logger.Int("count", len(detections)),
		logger.Int64("total", response.Total),
		logger.Int("pages", response.TotalPages),
		logger.Int("currentPage", response.CurrentPage),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	return ctx.JSON(http.StatusOK, response)
}

// getDetectionsByQueryType retrieves detections based on the query type
func (c *Controller) getDetectionsByQueryType(params *detectionQueryParams) ([]datastore.Note, int64, error) {
	// Check if advanced filters are present
	hasAdvancedFilters := params.Confidence != "" || params.TimeOfDay != "" ||
		params.HourRange != "" || params.Verified != "" ||
		params.Location != "" || params.Locked != ""

	switch params.QueryType {
	case "hourly":
		return c.getHourlyDetections(params.Date, params.Hour, params.Duration, params.NumResults, params.Offset)
	case "species":
		return c.getSpeciesDetections(params.Species, params.Date, params.Hour, params.Duration, params.NumResults, params.Offset)
	case "search":
		// Use advanced search if filters are present
		if hasAdvancedFilters {
			return c.getSearchDetectionsAdvanced(params)
		}
		return c.getSearchDetections(params.Search, params.NumResults, params.Offset)
	default: // "all" or any other value
		// Check if there are filters even without explicit search text
		if hasAdvancedFilters {
			return c.getSearchDetectionsAdvanced(params)
		}
		return c.getAllDetections(params.NumResults, params.Offset)
	}
}

// convertNotesToDetectionResponses converts datastore notes to API detection responses
func (c *Controller) convertNotesToDetectionResponses(notes []datastore.Note, includeWeather bool) []DetectionResponse {
	detections := make([]DetectionResponse, 0, len(notes))

	// Create a map to cache weather data if needed
	weatherCache := make(map[string][]datastore.HourlyWeather)

	for i := range notes {
		note := &notes[i]
		detection := c.noteToDetectionResponse(note, includeWeather, weatherCache)
		detections = append(detections, detection)
	}
	return detections
}

// noteToDetectionResponse converts a single note to a detection response
func (c *Controller) noteToDetectionResponse(note *datastore.Note, includeWeather bool, weatherCache map[string][]datastore.HourlyWeather) DetectionResponse {
	detection := DetectionResponse{
		ID:             note.ID,
		Date:           note.Date,
		Time:           note.Time,
		Source:         note.Source.SafeString,
		BeginTime:      note.BeginTime.Format(time.RFC3339),
		EndTime:        note.EndTime.Format(time.RFC3339),
		SpeciesCode:    note.SpeciesCode,
		ScientificName: note.ScientificName,
		CommonName:     note.CommonName,
		Confidence:     note.Confidence,
		Locked:         note.Locked,
	}

	c.applySpeciesTrackingMetadata(&detection, note.ScientificName)
	detection.Verified = c.mapVerificationStatus(note.Verified)
	detection.Comments = extractNoteComments(note.Comments)

	if includeWeather {
		c.populateWeatherData(&detection, note, weatherCache)
	}

	return detection
}

// applySpeciesTrackingMetadata adds species tracking info to detection response
func (c *Controller) applySpeciesTrackingMetadata(detection *DetectionResponse, scientificName string) {
	if c.Processor == nil || c.Processor.NewSpeciesTracker == nil {
		return
	}
	status := c.Processor.NewSpeciesTracker.GetSpeciesStatus(scientificName, time.Now())
	detection.IsNewSpecies = status.IsNew
	detection.DaysSinceFirstSeen = status.DaysSinceFirst
	detection.IsNewThisYear = status.IsNewThisYear
	detection.IsNewThisSeason = status.IsNewThisSeason
	detection.DaysThisYear = status.DaysThisYear
	detection.DaysThisSeason = status.DaysThisSeason
	detection.CurrentSeason = status.CurrentSeason
}

// extractNoteComments converts datastore comments to API response format
func extractNoteComments(noteComments []datastore.NoteComment) []CommentResponse {
	if len(noteComments) == 0 {
		return nil
	}
	comments := make([]CommentResponse, 0, len(noteComments))
	for _, comment := range noteComments {
		comments = append(comments, CommentResponse{
			ID:        comment.ID,
			Entry:     comment.Entry,
			CreatedAt: comment.CreatedAt.Format(time.RFC3339),
			UpdatedAt: comment.UpdatedAt.Format(time.RFC3339),
		})
	}
	return comments
}

// populateWeatherData adds weather and time of day info to detection response
func (c *Controller) populateWeatherData(detection *DetectionResponse, note *datastore.Note, weatherCache map[string][]datastore.HourlyWeather) {
	// Parse detection time (database stores local time strings)
	detectionTimeStr := note.Date + " " + note.Time
	detectionTime, err := time.ParseInLocation("2006-01-02 15:04:05", detectionTimeStr, time.Local)
	if err != nil {
		return
	}

	detection.TimeOfDay = c.calculateDetectionTimeOfDay(detectionTime)
	detection.Weather = c.getWeatherForDetectionTime(detectionTime, note.Date, weatherCache)
}

// calculateDetectionTimeOfDay calculates time of day based on sun position
func (c *Controller) calculateDetectionTimeOfDay(detectionTime time.Time) string {
	if c.SunCalc == nil {
		return ""
	}
	sunTimes, err := c.SunCalc.GetSunEventTimes(detectionTime)
	if err != nil {
		return ""
	}
	return calculateTimeOfDay(detectionTime, &sunTimes)
}

// getWeatherForDetectionTime retrieves weather data for a detection time
func (c *Controller) getWeatherForDetectionTime(detectionTime time.Time, date string, weatherCache map[string][]datastore.HourlyWeather) *WeatherInfo {
	if weatherCache == nil {
		return nil
	}

	// Ensure weather data is cached for this date
	c.ensureWeatherCached(date, weatherCache)

	// Find and return closest weather data
	weatherData, exists := weatherCache[date]
	if !exists || len(weatherData) == 0 {
		return nil
	}

	closestWeather := c.findClosestHourlyWeather(detectionTime, weatherData)
	if closestWeather.WeatherIcon == "" {
		return nil
	}

	return &WeatherInfo{
		WeatherIcon: closestWeather.WeatherIcon,
		WeatherMain: closestWeather.WeatherMain,
		Description: closestWeather.WeatherDesc,
		Temperature: closestWeather.Temperature,
		WindSpeed:   closestWeather.WindSpeed,
		WindGust:    closestWeather.WindGust,
		Humidity:    closestWeather.Humidity,
		Units:       c.getWeatherUnits(),
	}
}

// ensureWeatherCached fetches weather data for a date if not already cached
func (c *Controller) ensureWeatherCached(date string, weatherCache map[string][]datastore.HourlyWeather) {
	if _, exists := weatherCache[date]; exists {
		return
	}
	hourlyWeather, err := c.DS.GetHourlyWeather(date)
	if err == nil {
		weatherCache[date] = hourlyWeather
	}
}

// mapVerificationStatus maps the database verification status to API response format
func (c *Controller) mapVerificationStatus(status string) string {
	switch status {
	case VerificationStatusCorrect:
		return VerificationStatusCorrect
	case VerificationStatusFalsePositive:
		return VerificationStatusFalsePositive
	default:
		return VerificationStatusUnverified
	}
}

// createPaginatedResponse creates a paginated response structure
func (c *Controller) createPaginatedResponse(detections []DetectionResponse, totalResults int64, numResults, offset int) PaginatedResponse {
	currentPage := (offset / numResults) + 1
	totalPages := int((totalResults + int64(numResults) - 1) / int64(numResults))

	return PaginatedResponse{
		Data:        detections,
		Total:       totalResults,
		Limit:       numResults,
		Offset:      offset,
		CurrentPage: currentPage,
		TotalPages:  totalPages,
	}
}

// getHourlyDetections handles hourly query type logic
func (c *Controller) getHourlyDetections(date, hour string, duration, numResults, offset int) ([]datastore.Note, int64, error) {
	// Generate a cache key based on parameters
	cacheKey := fmt.Sprintf("hourly:%s:%s:%d:%d:%d", date, hour, duration, numResults, offset)

	// Check if data is in cache
	if cachedData, found := c.detectionCache.Get(cacheKey); found {
		cachedResult := cachedData.(struct {
			Notes []datastore.Note
			Total int64
		})
		return cachedResult.Notes, cachedResult.Total, nil
	}

	// If not in cache, query the database
	notes, err := c.DS.GetHourlyDetections(date, hour, duration, numResults, offset)
	if err != nil {
		c.logErrorIfEnabled("Failed to get hourly detections",
			logger.String("date", date),
			logger.String("hour", hour),
			logger.Int("duration", duration),
			logger.Int("limit", numResults),
			logger.Int("offset", offset),
			logger.Error(err),
		)
		return nil, 0, err
	}

	totalCount, err := c.DS.CountHourlyDetections(date, hour, duration)
	if err != nil {
		c.logErrorIfEnabled("Failed to count hourly detections",
			logger.String("date", date),
			logger.String("hour", hour),
			logger.Int("duration", duration),
			logger.Error(err),
		)
		return nil, 0, err
	}

	// Cache the results
	c.detectionCache.Set(cacheKey, struct {
		Notes []datastore.Note
		Total int64
	}{notes, totalCount}, cache.DefaultExpiration)

	c.logInfoIfEnabled("Retrieved hourly detections",
		logger.String("date", date),
		logger.String("hour", hour),
		logger.Int("duration", duration),
		logger.Int("count", len(notes)),
		logger.Int64("total", totalCount),
	)

	return notes, totalCount, nil
}

// getSpeciesDetections handles species query type logic
func (c *Controller) getSpeciesDetections(species, date, hour string, duration, numResults, offset int) ([]datastore.Note, int64, error) {
	// Generate a cache key based on parameters
	cacheKey := fmt.Sprintf("species:%s:%s:%s:%d:%d:%d", species, date, hour, duration, numResults, offset)

	// Check if data is in cache
	if cachedData, found := c.detectionCache.Get(cacheKey); found {
		cachedResult := cachedData.(struct {
			Notes []datastore.Note
			Total int64
		})
		return cachedResult.Notes, cachedResult.Total, nil
	}

	// If not in cache, query the database
	notes, err := c.DS.SpeciesDetections(species, date, hour, duration, false, numResults, offset)
	if err != nil {
		c.logErrorIfEnabled("Failed to get species detections",
			logger.String("species", species),
			logger.String("date", date),
			logger.String("hour", hour),
			logger.Int("duration", duration),
			logger.Int("limit", numResults),
			logger.Int("offset", offset),
			logger.Error(err),
		)
		return nil, 0, err
	}

	totalCount, err := c.DS.CountSpeciesDetections(species, date, hour, duration)
	if err != nil {
		c.logErrorIfEnabled("Failed to count species detections",
			logger.String("species", species),
			logger.String("date", date),
			logger.String("hour", hour),
			logger.Int("duration", duration),
			logger.Error(err),
		)
		return nil, 0, err
	}

	// Cache the results
	c.detectionCache.Set(cacheKey, struct {
		Notes []datastore.Note
		Total int64
	}{notes, totalCount}, cache.DefaultExpiration)

	c.logInfoIfEnabled("Retrieved species detections",
		logger.String("species", species),
		logger.String("date", date),
		logger.String("hour", hour),
		logger.Int("duration", duration),
		logger.Int("count", len(notes)),
		logger.Int64("total", totalCount),
	)

	return notes, totalCount, nil
}

// getSearchDetectionsAdvanced handles advanced search with filters
func (c *Controller) getSearchDetectionsAdvanced(params *detectionQueryParams) ([]datastore.Note, int64, error) {
	filters := c.buildAdvancedSearchFilters(params)

	// Use the advanced search method
	notes, totalCount, err := c.DS.SearchNotesAdvanced(&filters)
	if err != nil {
		c.logErrorIfEnabled("Failed to perform advanced search",
			logger.String("filters", fmt.Sprintf("%+v", filters)),
			logger.Error(err),
		)
		return nil, 0, err
	}

	// Cache the results with key that includes all filter parameters
	c.detectionCache.Set(params.advancedSearchCacheKey(), struct {
		Notes []datastore.Note
		Total int64
	}{notes, totalCount}, cache.DefaultExpiration)

	return notes, totalCount, nil
}

// buildAdvancedSearchFilters constructs search filters from query parameters
func (c *Controller) buildAdvancedSearchFilters(params *detectionQueryParams) datastore.AdvancedSearchFilters {
	filters := datastore.AdvancedSearchFilters{
		TextQuery:     params.Search,
		Limit:         params.NumResults,
		Offset:        params.Offset,
		SortAscending: false,
	}

	// Apply confidence filter using shared helper
	if confFilter := parseConfidenceFilter(params.Confidence); confFilter != nil {
		filters.Confidence = &datastore.ConfidenceFilter{
			Operator: confFilter.Operator,
			Value:    confFilter.Value,
		}
	}

	// Apply time of day filter
	if params.TimeOfDay != "" {
		filters.TimeOfDay = []string{params.TimeOfDay}
	}

	// Apply hour filter using shared helper
	hourParam := params.HourRange
	if hourParam == "" {
		hourParam = params.Hour
	}
	if hourFilter := parseHourFilter(hourParam); hourFilter != nil {
		filters.Hour = &datastore.HourFilter{
			Start: hourFilter.Start,
			End:   hourFilter.End,
		}
	}

	// Apply date range filter using shared helper
	if dateRange := parseDateRangeFilter(params.Date, params.StartDate, params.EndDate); dateRange != nil {
		filters.DateRange = &datastore.DateRange{
			Start: dateRange.Start,
			End:   dateRange.End,
		}
	}

	// Apply simple string filters
	if params.Species != "" {
		filters.Species = []string{params.Species}
	}
	if params.Location != "" {
		filters.Location = []string{params.Location}
	}

	// Apply boolean filters
	if params.Verified != "" {
		verified := params.Verified == QueryValueTrue || params.Verified == "human"
		filters.Verified = &verified
	}
	if params.Locked != "" {
		locked := params.Locked == QueryValueTrue
		filters.Locked = &locked
	}

	return filters
}

// getSearchDetections handles search query type logic
func (c *Controller) getSearchDetections(search string, numResults, offset int) ([]datastore.Note, int64, error) {
	// Generate a cache key based on parameters
	cacheKey := fmt.Sprintf("search:%s:%d:%d", search, numResults, offset)

	// Check if data is in cache
	if cachedData, found := c.detectionCache.Get(cacheKey); found {
		cachedResult := cachedData.(struct {
			Notes []datastore.Note
			Total int64
		})
		return cachedResult.Notes, cachedResult.Total, nil
	}

	// If not in cache, query the database
	notes, err := c.DS.SearchNotes(search, false, numResults, offset)
	if err != nil {
		c.logErrorIfEnabled("Failed to search notes",
			logger.String("query", search),
			logger.Int("limit", numResults),
			logger.Int("offset", offset),
			logger.Error(err),
		)
		return nil, 0, err
	}

	totalCount, err := c.DS.CountSearchResults(search)
	if err != nil {
		c.logErrorIfEnabled("Failed to count search results",
			logger.String("query", search),
			logger.Error(err),
		)
		return nil, 0, err
	}

	// Cache the results
	c.detectionCache.Set(cacheKey, struct {
		Notes []datastore.Note
		Total int64
	}{notes, totalCount}, cache.DefaultExpiration)

	c.logInfoIfEnabled("Retrieved search results",
		logger.String("query", search),
		logger.Int("count", len(notes)),
		logger.Int64("total", totalCount),
	)

	return notes, totalCount, nil
}

// getAllDetections handles default/all query type logic
func (c *Controller) getAllDetections(numResults, offset int) ([]datastore.Note, int64, error) {
	// Generate a cache key based on parameters
	cacheKey := fmt.Sprintf("all:%d:%d", numResults, offset)

	// Check if data is in cache
	if cachedData, found := c.detectionCache.Get(cacheKey); found {
		cachedResult := cachedData.(struct {
			Notes []datastore.Note
			Total int64
		})
		return cachedResult.Notes, cachedResult.Total, nil
	}

	// Use the datastore.SearchNotes method with an empty query to get all notes
	notes, err := c.DS.SearchNotes("", false, numResults, offset)
	if err != nil {
		c.logErrorIfEnabled("Failed to get all detections",
			logger.Int("limit", numResults),
			logger.Int("offset", offset),
			logger.Error(err),
		)
		return nil, 0, err
	}

	// Estimate total by counting
	totalResults := int64(len(notes))
	if len(notes) == numResults {
		// If we got exactly the number requested, there may be more
		totalResults = int64(offset + numResults + 1) // This is an estimate
	}

	// Cache the results
	c.detectionCache.Set(cacheKey, struct {
		Notes []datastore.Note
		Total int64
	}{notes, totalResults}, cache.DefaultExpiration)

	c.logInfoIfEnabled("Retrieved all detections",
		logger.Int("count", len(notes)),
		logger.Int64("total", totalResults),
	)

	return notes, totalResults, nil
}

// GetDetection returns a single detection by ID
func (c *Controller) GetDetection(ctx echo.Context) error {
	id := ctx.Param("id")
	note, err := c.DS.Get(id)
	if err != nil {
		return ctx.JSON(http.StatusNotFound, map[string]string{"error": "Detection not found"})
	}

	// For single detection, include weather data by default
	weatherCache := make(map[string][]datastore.HourlyWeather)
	detection := c.noteToDetectionResponse(&note, true, weatherCache)
	return ctx.JSON(http.StatusOK, detection)
}

// GetRecentDetections returns the most recent detections
// Query parameters:
// - limit: number of detections to return (default: 10)
// - includeWeather: whether to include weather data (default: false)
func (c *Controller) GetRecentDetections(ctx echo.Context) error {
	limit, _ := strconv.Atoi(ctx.QueryParam("limit"))
	if limit <= 0 {
		limit = 10
	}

	// Check if weather data should be included
	includeWeather := ctx.QueryParam("includeWeather") == QueryValueTrue

	notes, err := c.DS.GetLastDetections(limit)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get recent detections", http.StatusInternalServerError)
	}

	detections := c.convertNotesToDetectionResponses(notes, includeWeather)
	return ctx.JSON(http.StatusOK, detections)
}

// DeleteDetection deletes a detection by ID
func (c *Controller) DeleteDetection(ctx echo.Context) error {
	idStr := ctx.Param("id")
	note, err := c.DS.Get(idStr)
	if err != nil {
		return c.HandleError(ctx, err, "Detection not found", http.StatusNotFound)
	}

	// Check if the note is locked
	if note.Locked {
		return c.HandleError(ctx, fmt.Errorf("detection is locked"), "Detection is locked", http.StatusForbidden)
	}

	err = c.DS.Delete(idStr)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to delete detection", http.StatusInternalServerError)
	}

	// Invalidate cache after deletion
	c.invalidateDetectionCache()

	return ctx.NoContent(http.StatusNoContent)
}

// invalidateDetectionCache clears the detection cache to ensure fresh data
// is fetched on subsequent requests. This should be called after any
// operation that modifies detection data.
func (c *Controller) invalidateDetectionCache() {
	// Clear all cached detection data to ensure fresh results
	c.detectionCache.Flush()
}

// checkAndHandleLock verifies if a detection is locked and manages lock state
// Returns the note and error if any
func (c *Controller) checkAndHandleLock(idStr string, shouldLock bool) (*datastore.Note, error) {
	// Get the note
	note, err := c.DS.Get(idStr)
	if err != nil {
		return nil, fmt.Errorf("detection not found: %w", err)
	}

	// Check if the note is already locked in memory
	if note.Locked {
		return nil, fmt.Errorf("detection is locked")
	}

	// Check if the note is locked in the database
	isLocked, err := c.DS.IsNoteLocked(idStr)
	if err != nil {
		return nil, fmt.Errorf("failed to check lock status: %w", err)
	}
	if isLocked {
		return nil, fmt.Errorf("detection is locked")
	}

	// If we should lock the note, try to acquire lock
	if shouldLock {
		if err := c.DS.LockNote(idStr); err != nil {
			return nil, fmt.Errorf("failed to acquire lock: %w", err)
		}
	}

	return &note, nil
}

// ReviewDetection updates a detection with verification status and optional comment
func (c *Controller) ReviewDetection(ctx echo.Context) error {
	idStr := ctx.Param("id")

	// Get the note directly - allow reviewing locked detections
	note, err := c.DS.Get(idStr)
	if err != nil {
		return c.HandleError(ctx, err, "Detection not found", http.StatusNotFound)
	}

	// Parse request
	req := &DetectionRequest{}
	if err := ctx.Bind(req); err != nil {
		return c.HandleError(ctx, err, "Invalid request format", http.StatusBadRequest)
	}

	// Check lock status (both in-memory and database for race condition)
	if c.checkDetectionNotLocked(ctx, idStr, note.Locked) {
		return nil // Response already handled by checkDetectionNotLocked
	}

	// Handle comment if provided
	if req.Comment != "" {
		// Save comment using the datastore method for adding comments
		err = c.AddComment(note.ID, req.Comment)
		if err != nil {
			return c.HandleError(ctx, err, fmt.Sprintf("Failed to add comment: %v", err), http.StatusInternalServerError)
		}
	}

	// Handle verification if provided
	verification, err := parseVerificationStatus(req.Verified)
	if err != nil {
		return c.HandleError(ctx, err, "Invalid verification status", http.StatusBadRequest)
	}

	if verification.IsSet {
		// Save review using the datastore method for reviews
		if err := c.AddReview(note.ID, verification.Verified); err != nil {
			return c.HandleError(ctx, err, fmt.Sprintf("Failed to update verification: %v", err), http.StatusInternalServerError)
		}

		// Handle ignored species
		if err := c.addToIgnoredSpecies(req.Verified, req.IgnoreSpecies); err != nil {
			return c.HandleError(ctx, err, err.Error(), http.StatusInternalServerError)
		}
	}

	// Handle lock/unlock request separately
	if req.LockDetection != note.Locked {
		c.logInfoIfEnabled("Updating lock status",
			logger.String("detection_id", idStr),
			logger.Bool("current_locked", note.Locked),
			logger.Bool("new_locked", req.LockDetection),
			logger.String("ip", ctx.RealIP()),
		)

		err = c.AddLock(note.ID, req.LockDetection)
		if err != nil {
			// Log the lock operation failure
			c.logErrorIfEnabled("Failed to update lock status",
				logger.String("detection_id", idStr),
				logger.Bool("attempted_lock_state", req.LockDetection),
				logger.Error(err),
				logger.String("ip", ctx.RealIP()),
			)
			return c.HandleError(ctx, err, fmt.Sprintf("Failed to update lock status: %v", err), http.StatusInternalServerError)
		}
	}

	// Invalidate cache after modification
	c.invalidateDetectionCache()

	// Return success response with 200 OK status
	return ctx.JSON(http.StatusOK, map[string]string{
		"status": "success",
	})
}

// LockDetection locks or unlocks a detection
func (c *Controller) LockDetection(ctx echo.Context) error {
	idStr := ctx.Param("id")

	// Parse request first to determine if we're locking or unlocking
	req := &DetectionRequest{}
	if err := ctx.Bind(req); err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request format"})
	}

	// Get the note to verify it exists
	note, err := c.DS.Get(idStr)
	if err != nil {
		return ctx.JSON(http.StatusNotFound, map[string]string{"error": "Detection not found"})
	}

	// Only check lock status when trying to LOCK (not unlock)
	// This allows unlocking a locked detection
	if req.Locked {
		if c.checkDetectionNotLocked(ctx, idStr, note.Locked) {
			return nil // Response already handled by checkDetectionNotLocked
		}
	}

	// Lock/unlock the detection
	err = c.AddLock(note.ID, req.Locked)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Failed to update lock status: %v", err)})
	}

	// Invalidate cache after changing lock status
	c.invalidateDetectionCache()

	return ctx.NoContent(http.StatusNoContent)
}

// IgnoreSpeciesRequest represents the request body for ignoring a species
type IgnoreSpeciesRequest struct {
	CommonName string `json:"common_name"`
}

// IgnoreSpeciesResponse represents the response for the ignore species endpoint
type IgnoreSpeciesResponse struct {
	CommonName string `json:"common_name"`
	Action     string `json:"action"` // "added" or "removed"
	IsExcluded bool   `json:"is_excluded"`
}

// ExcludedSpeciesResponse represents the response for the get excluded species endpoint
type ExcludedSpeciesResponse struct {
	Species []string `json:"species"`
	Count   int      `json:"count"`
}

// IgnoreSpecies toggles a species in the ignored list (adds if not present, removes if present)
func (c *Controller) IgnoreSpecies(ctx echo.Context) error {
	// Parse request body
	req := &IgnoreSpeciesRequest{}
	if err := ctx.Bind(req); err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request format"})
	}

	// Validate request
	if req.CommonName == "" {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Missing species name"})
	}

	// Toggle the species in ignored list
	action, isExcluded, err := c.toggleSpeciesInIgnoredList(req.CommonName)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	// Log the action
	c.logInfoIfEnabled("Species exclusion toggled",
		logger.String("species", req.CommonName),
		logger.String("action", action),
		logger.Bool("is_excluded", isExcluded),
		logger.String("ip", ctx.RealIP()),
	)

	return ctx.JSON(http.StatusOK, IgnoreSpeciesResponse{
		CommonName: req.CommonName,
		Action:     action,
		IsExcluded: isExcluded,
	})
}

// GetExcludedSpecies returns the list of excluded species
func (c *Controller) GetExcludedSpecies(ctx echo.Context) error {
	settings := conf.GetSettings()

	// Create a copy of the slice to avoid race conditions
	c.speciesExcludeMutex.Lock()
	species := make([]string, len(settings.Realtime.Species.Exclude))
	copy(species, settings.Realtime.Species.Exclude)
	c.speciesExcludeMutex.Unlock()

	return ctx.JSON(http.StatusOK, ExcludedSpeciesResponse{
		Species: species,
		Count:   len(species),
	})
}

// addToIgnoredSpecies handles the logic for adding species to the ignore list
func (c *Controller) addToIgnoredSpecies(verified, ignoreSpecies string) error {
	if verified == "false_positive" && ignoreSpecies != "" {
		return c.addSpeciesToIgnoredList(ignoreSpecies)
	}
	return nil
}

// toggleSpeciesInIgnoredList toggles a species in the ignore list with proper concurrency control.
// If the species is already excluded, it removes it. If not excluded, it adds it.
// Returns the action taken ("added" or "removed"), the new excluded state, and any error.
func (c *Controller) toggleSpeciesInIgnoredList(species string) (action string, isExcluded bool, err error) {
	if species == "" {
		return "", false, nil
	}

	// Use the controller's mutex to protect this operation
	c.speciesExcludeMutex.Lock()
	defer c.speciesExcludeMutex.Unlock()

	// Access the latest settings using the settings accessor function
	settings := conf.GetSettings()

	// Check if species is already in the excluded list
	wasExcluded := slices.Contains(settings.Realtime.Species.Exclude, species)

	if wasExcluded {
		// Remove from excluded list
		newExcludeList := make([]string, 0, len(settings.Realtime.Species.Exclude)-1)
		for _, s := range settings.Realtime.Species.Exclude {
			if s != species {
				newExcludeList = append(newExcludeList, s)
			}
		}
		settings.Realtime.Species.Exclude = newExcludeList
		action = "removed"
		isExcluded = false
	} else {
		// Add to excluded list
		newExcludeList := make([]string, len(settings.Realtime.Species.Exclude), len(settings.Realtime.Species.Exclude)+1)
		copy(newExcludeList, settings.Realtime.Species.Exclude)
		newExcludeList = append(newExcludeList, species)
		settings.Realtime.Species.Exclude = newExcludeList
		action = "added"
		isExcluded = true
	}

	// Save settings using the package function that handles concurrency
	if err := conf.SaveSettings(); err != nil {
		return "", wasExcluded, fmt.Errorf("failed to save settings: %w", err)
	}

	return action, isExcluded, nil
}

// addSpeciesToIgnoredList adds a species to the ignore list (used by review endpoint).
// This is a simplified version that only adds, used when marking as false positive.
func (c *Controller) addSpeciesToIgnoredList(species string) error {
	if species == "" {
		return nil
	}

	// Use the controller's mutex to protect this operation
	c.speciesExcludeMutex.Lock()
	defer c.speciesExcludeMutex.Unlock()

	// Access the latest settings using the settings accessor function
	settings := conf.GetSettings()

	// Check if species is already in the excluded list
	isExcluded := slices.Contains(settings.Realtime.Species.Exclude, species)

	// If not already excluded, add it
	if !isExcluded {
		// Create a copy of the current exclude list to avoid race conditions
		newExcludeList := make([]string, len(settings.Realtime.Species.Exclude), len(settings.Realtime.Species.Exclude)+1)
		copy(newExcludeList, settings.Realtime.Species.Exclude)

		// Add the new species to the list
		newExcludeList = append(newExcludeList, species)

		// Update the settings with the new list
		settings.Realtime.Species.Exclude = newExcludeList

		// Save settings using the package function that handles concurrency
		if err := conf.SaveSettings(); err != nil {
			return fmt.Errorf("failed to save settings: %w", err)
		}
	}

	return nil
}

// AddComment creates a comment for a note
func (c *Controller) AddComment(noteID uint, commentText string) error {
	if commentText == "" {
		return nil // No comment to add
	}

	comment := &datastore.NoteComment{
		NoteID:    noteID,
		Entry:     commentText,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	return c.DS.SaveNoteComment(comment)
}

// AddReview creates or updates a review for a note
func (c *Controller) AddReview(noteID uint, verified bool) error {
	// Convert bool to string value
	verifiedStr := map[bool]string{
		true:  "correct",
		false: "false_positive",
	}[verified]

	review := &datastore.NoteReview{
		NoteID:    noteID,
		Verified:  verifiedStr,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	return c.DS.SaveNoteReview(review)
}

// AddLock creates or removes a lock for a note
func (c *Controller) AddLock(noteID uint, locked bool) error {
	noteIDStr := strconv.FormatUint(uint64(noteID), 10)

	if locked {
		return c.DS.LockNote(noteIDStr)
	} else {
		return c.DS.UnlockNote(noteIDStr)
	}
}

// GetDetectionTimeOfDay calculates and returns the time of day for a detection
func (c *Controller) GetDetectionTimeOfDay(ctx echo.Context) error {
	id := ctx.Param("id")

	// Get the detection from the database
	note, err := c.DS.Get(id)
	if err != nil {
		return c.HandleError(ctx, err, "Detection not found", http.StatusNotFound)
	}

	// Parse the detection date and time
	dateTimeStr := fmt.Sprintf("%s %s", note.Date, note.Time)
	layout := "2006-01-02 15:04:05" // Adjust based on your actual date/time format

	detectionTime, err := time.Parse(layout, dateTimeStr)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to parse detection time", http.StatusInternalServerError)
	}

	// Check if SunCalc is initialized
	if c.SunCalc == nil {
		return c.HandleError(ctx, fmt.Errorf("sun calculator not initialized"), "Sun calculator not available", http.StatusInternalServerError)
	}

	// Calculate sun times for the detection date
	sunEvents, err := c.SunCalc.GetSunEventTimes(detectionTime)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to calculate sun times", http.StatusInternalServerError)
	}

	// Determine time of day based on the detection time and sun events
	timeOfDay := calculateTimeOfDay(detectionTime, &sunEvents)

	// Return the time of day
	return ctx.JSON(http.StatusOK, TimeOfDayResponse{
		TimeOfDay: timeOfDay,
	})
}

// calculateTimeOfDay determines the time of day based on the detection time and sun events
func calculateTimeOfDay(detectionTime time.Time, sunEvents *suncalc.SunEventTimes) string {
	// Convert all times to the same format for comparison
	detTime := detectionTime.Format(time.TimeOnly)
	sunriseTime := sunEvents.Sunrise.Format(time.TimeOnly)
	sunsetTime := sunEvents.Sunset.Format(time.TimeOnly)

	// Define sunrise/sunset window (30 minutes before and after)
	sunriseStart := sunEvents.Sunrise.Add(-sunEventWindowMinutes * time.Minute).Format(time.TimeOnly)
	sunriseEnd := sunEvents.Sunrise.Add(sunEventWindowMinutes * time.Minute).Format(time.TimeOnly)
	sunsetStart := sunEvents.Sunset.Add(-sunEventWindowMinutes * time.Minute).Format(time.TimeOnly)
	sunsetEnd := sunEvents.Sunset.Add(sunEventWindowMinutes * time.Minute).Format(time.TimeOnly)

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

// getWeatherUnits returns the temperature display unit based on user preference.
// All temperatures are stored in Celsius internally; this determines display format.
// Returns "imperial" for Fahrenheit or "metric" for Celsius to match frontend expectations.
func (c *Controller) getWeatherUnits() string {
	// Read settings with mutex
	c.settingsMutex.RLock()
	defer c.settingsMutex.RUnlock()

	// Use dashboard temperature unit preference for display
	// All temperatures are now stored in Celsius internally
	switch c.Settings.Realtime.Dashboard.TemperatureUnit {
	case conf.TemperatureUnitFahrenheit:
		return "imperial"
	case conf.TemperatureUnitCelsius:
		return WeatherUnitMetric
	default:
		// Default to Celsius if not set
		return WeatherUnitMetric
	}
}
