// internal/api/v2/detections.go
package api

import (
	"fmt"
	"log"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/patrickmn/go-cache"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/suncalc"
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
	_, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return fmt.Errorf("invalid date value for %s: %w", paramName, err)
	}

	return nil
}

// initDetectionRoutes registers all detection-related API endpoints
func (c *Controller) initDetectionRoutes() {
	// Initialize the cache with a 5-minute default expiration and 10-minute cleanup interval
	c.detectionCache = cache.New(5*time.Minute, 10*time.Minute)

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
	detectionGroup := c.Group.Group("/detections", c.AuthMiddleware)
	detectionGroup.DELETE("/:id", c.DeleteDetection)
	detectionGroup.POST("/:id/review", c.ReviewDetection)
	detectionGroup.POST("/:id/lock", c.LockDetection)
	detectionGroup.POST("/ignore", c.IgnoreSpecies)
}

// DetectionResponse represents a detection in the API response
type DetectionResponse struct {
	ID             uint                `json:"id"`
	Date           string              `json:"date"`
	Time           string              `json:"time"`
	Source         string              `json:"source"`
	BeginTime      string              `json:"beginTime"`
	EndTime        string              `json:"endTime"`
	SpeciesCode    string              `json:"speciesCode"`
	ScientificName string              `json:"scientificName"`
	CommonName     string              `json:"commonName"`
	Confidence     float64             `json:"confidence"`
	Verified       string              `json:"verified"`
	Locked         bool                `json:"locked"`
	Comments       []string            `json:"comments,omitempty"`
	Weather        *WeatherInfo        `json:"weather,omitempty"`
	TimeOfDay      string              `json:"timeOfDay,omitempty"`
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
}

// PaginatedResponse represents a paginated API response
type PaginatedResponse struct {
	Data        any `json:"data"`
	Total       int64       `json:"total"`
	Limit       int         `json:"limit"`
	Offset      int         `json:"offset"`
	CurrentPage int         `json:"current_page"`
	TotalPages  int         `json:"total_pages"`
}

// TimeOfDayResponse represents the time of day response for a detection
type TimeOfDayResponse struct {
	TimeOfDay string `json:"timeOfDay"`
}

// detectionQueryParams holds all query parameters for detection requests
type detectionQueryParams struct {
	Date         string
	Hour         string
	Duration     int
	Species      string
	Search       string
	StartDate    string
	EndDate      string
	NumResults   int
	Offset       int
	QueryType    string
	// Advanced filter parameters
	Confidence   string
	TimeOfDay    string
	HourRange    string
	Verified     string
	Location     string
	Locked       string
	// Include additional data
	IncludeWeather bool
}

// parseDetectionQueryParams extracts and validates query parameters from the request
func (c *Controller) parseDetectionQueryParams(ctx echo.Context) (*detectionQueryParams, error) {
	params := &detectionQueryParams{
		Date:       ctx.QueryParam("date"),
		Hour:       ctx.QueryParam("hour"),
		Species:    ctx.QueryParam("species"),
		Search:     ctx.QueryParam("search"),
		StartDate:  ctx.QueryParam("start_date"),
		EndDate:    ctx.QueryParam("end_date"),
		QueryType:  ctx.QueryParam("queryType"),
		// Advanced filter parameters
		Confidence: ctx.QueryParam("confidence"),
		TimeOfDay:  ctx.QueryParam("timeOfDay"),
		HourRange:  ctx.QueryParam("hourRange"),
		Verified:   ctx.QueryParam("verified"),
		Location:   ctx.QueryParam("location"),
		Locked:     ctx.QueryParam("locked"),
		// Include weather data
		IncludeWeather: ctx.QueryParam("includeWeather") == "true",
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

	return params, nil
}

// validateDateParameters validates start_date and end_date parameters
func (c *Controller) validateDateParameters(startDateStr, endDateStr string, ctx echo.Context) error {
	if err := validateDateParam(startDateStr, "start_date"); err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Invalid date parameter",
				"parameter", "start_date",
				"value", startDateStr,
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	if err := validateDateParam(endDateStr, "end_date"); err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Invalid date parameter",
				"parameter", "end_date",
				"value", endDateStr,
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Check if start_date is after end_date
	if startDateStr != "" && endDateStr != "" {
		startDate, _ := time.Parse("2006-01-02", startDateStr)
		endDate, _ := time.Parse("2006-01-02", endDateStr)
		if startDate.After(endDate) {
			if c.apiLogger != nil {
				c.apiLogger.Error("Invalid date range",
					"start_date", startDateStr,
					"end_date", endDateStr,
					"error", "start_date cannot be after end_date",
					"path", ctx.Request().URL.Path,
					"ip", ctx.RealIP(),
				)
			}
			return echo.NewHTTPError(http.StatusBadRequest, "start_date cannot be after end_date")
		}
	}

	return nil
}

// parseNumResults parses and validates the numResults parameter
func (c *Controller) parseNumResults(numResultsStr string) (int, error) {
	if numResultsStr == "" {
		return 100, nil // Default value
	}

	log.Printf("[DEBUG] GetDetections: Raw numResults string: '%s'", numResultsStr)
	numResults, err := strconv.Atoi(numResultsStr)
	if err != nil {
		log.Printf("[DEBUG] GetDetections: Invalid numResults string '%s', error: %v", numResultsStr, err)
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

	log.Printf("[DEBUG] GetDetections: Parsed numResults value: %d", numResults)
	if numResults <= 0 {
		log.Printf("[DEBUG] GetDetections: Zero or negative numResults value: %d", numResults)
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

	if numResults > 1000 {
		log.Printf("[DEBUG] GetDetections: Too large numResults value: %d", numResults)
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

	log.Printf("[DEBUG] GetDetections: Raw offset string: '%s'", offsetStr)
	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		log.Printf("[DEBUG] GetDetections: Invalid offset string '%s', error: %v", offsetStr, err)
		// Log the enhanced error for telemetry
		_ = errors.Newf("invalid numeric value for offset: %v", err).
			Component("api").
			Category(errors.CategoryValidation).
			Context("parameter", "offset").
			Context("value", offsetStr).
			Build()
		return 0, fmt.Errorf("Invalid numeric value for offset: %w", err) //nolint:staticcheck // matches test expectations
	}

	log.Printf("[DEBUG] GetDetections: Parsed offset value: %d", offset)
	if offset < 0 {
		log.Printf("[DEBUG] GetDetections: Negative offset value: %d", offset)
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
		log.Printf("[DEBUG] GetDetections: Too large offset value: %d", offset)
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
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to parse query parameters",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}


	// Log the retrieval attempt
	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieving detections",
			"queryType", params.QueryType,
			"date", params.Date,
			"hour", params.Hour,
			"duration", params.Duration,
			"species", params.Species,
			"search", params.Search,
			"start_date", params.StartDate,
			"end_date", params.EndDate,
			"limit", params.NumResults,
			"offset", params.Offset,
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	// Get notes based on query type
	notes, totalResults, err := c.getDetectionsByQueryType(params)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to retrieve detections",
				"queryType", params.QueryType,
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	// Convert notes to response format
	detections := c.convertNotesToDetectionResponses(notes, params.IncludeWeather)

	// Create paginated response
	response := c.createPaginatedResponse(detections, totalResults, params.NumResults, params.Offset)

	// Log the successful response
	if c.apiLogger != nil {
		c.apiLogger.Info("Detections retrieved successfully",
			"queryType", params.QueryType,
			"count", len(detections),
			"total", response.Total,
			"pages", response.TotalPages,
			"currentPage", response.CurrentPage,
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

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
		Source:         note.Source,
		BeginTime:      note.BeginTime.Format(time.RFC3339),
		EndTime:        note.EndTime.Format(time.RFC3339),
		SpeciesCode:    note.SpeciesCode,
		ScientificName: note.ScientificName,
		CommonName:     note.CommonName,
		Confidence:     note.Confidence,
		Locked:         note.Locked,
	}

	// Handle verification status
	detection.Verified = c.mapVerificationStatus(note.Verified)

	// Get comments if any
	if len(note.Comments) > 0 {
		comments := make([]string, 0, len(note.Comments))
		for _, comment := range note.Comments {
			comments = append(comments, comment.Entry)
		}
		detection.Comments = comments
	}

	
	// Add weather and time of day if requested
	if includeWeather {
		// Parse detection time
		detectionTimeStr := note.Date + " " + note.Time
		detectionTime, err := time.Parse("2006-01-02 15:04:05", detectionTimeStr)
		if err == nil {
			// Calculate time of day
			if c.SunCalc != nil {
				sunTimes, err := c.SunCalc.GetSunEventTimes(detectionTime)
				if err == nil {
					detection.TimeOfDay = calculateTimeOfDay(detectionTime, &sunTimes)
				}
			}
			
			// Get weather data
			if weatherCache != nil {
				// Check if we have weather data for this date in cache
				if _, exists := weatherCache[note.Date]; !exists {
					// Fetch weather data for this date
					hourlyWeather, err := c.DS.GetHourlyWeather(note.Date)
					if err == nil {
						weatherCache[note.Date] = hourlyWeather
					}
				}
				
				// Find closest weather data
				if weatherData, exists := weatherCache[note.Date]; exists && len(weatherData) > 0 {
					closestWeather := c.findClosestHourlyWeather(detectionTime, weatherData)
					if closestWeather.WeatherIcon != "" {
						detection.Weather = &WeatherInfo{
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
				}
			}
		}
	}
	
	return detection
}

// mapVerificationStatus maps the database verification status to API response format
func (c *Controller) mapVerificationStatus(status string) string {
	switch status {
	case "correct":
		return "correct"
	case "false_positive":
		return "false_positive"
	default:
		return "unverified"
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
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to get hourly detections",
				"date", date,
				"hour", hour,
				"duration", duration,
				"limit", numResults,
				"offset", offset,
				"error", err.Error(),
			)
		}
		return nil, 0, err
	}

	totalCount, err := c.DS.CountHourlyDetections(date, hour, duration)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to count hourly detections",
				"date", date,
				"hour", hour,
				"duration", duration,
				"error", err.Error(),
			)
		}
		return nil, 0, err
	}

	// Cache the results
	c.detectionCache.Set(cacheKey, struct {
		Notes []datastore.Note
		Total int64
	}{notes, totalCount}, cache.DefaultExpiration)

	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieved hourly detections",
			"date", date,
			"hour", hour,
			"duration", duration,
			"count", len(notes),
			"total", totalCount,
		)
	}

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
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to get species detections",
				"species", species,
				"date", date,
				"hour", hour,
				"duration", duration,
				"limit", numResults,
				"offset", offset,
				"error", err.Error(),
			)
		}
		return nil, 0, err
	}

	totalCount, err := c.DS.CountSpeciesDetections(species, date, hour, duration)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to count species detections",
				"species", species,
				"date", date,
				"hour", hour,
				"duration", duration,
				"error", err.Error(),
			)
		}
		return nil, 0, err
	}

	// Cache the results
	c.detectionCache.Set(cacheKey, struct {
		Notes []datastore.Note
		Total int64
	}{notes, totalCount}, cache.DefaultExpiration)

	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieved species detections",
			"species", species,
			"date", date,
			"hour", hour,
			"duration", duration,
			"count", len(notes),
			"total", totalCount,
		)
	}

	return notes, totalCount, nil
}

// getSearchDetectionsAdvanced handles advanced search with filters
func (c *Controller) getSearchDetectionsAdvanced(params *detectionQueryParams) ([]datastore.Note, int64, error) {
	// Parse advanced filters from query parameters
	filters := datastore.AdvancedSearchFilters{
		TextQuery:     params.Search,
		Limit:         params.NumResults,
		Offset:        params.Offset,
		SortAscending: false, // Default to descending
	}

	// Parse confidence filter
	if confidenceParam := params.Confidence; confidenceParam != "" {
		// Parse operator and value (e.g., ">85", ">=90")
		var operator string
		var value string
		
		switch {
		case strings.HasPrefix(confidenceParam, ">="):
			operator = ">="
			value = confidenceParam[2:]
		case strings.HasPrefix(confidenceParam, "<="):
			operator = "<="
			value = confidenceParam[2:]
		case strings.HasPrefix(confidenceParam, ">"):
			operator = ">"
			value = confidenceParam[1:]
		case strings.HasPrefix(confidenceParam, "<"):
			operator = "<"
			value = confidenceParam[1:]
		default:
			operator = "="
			value = confidenceParam
		}
		
		if confValue, err := strconv.ParseFloat(value, 64); err == nil {
			filters.Confidence = &datastore.ConfidenceFilter{
				Operator: operator,
				Value:    confValue / 100.0, // Convert percentage to decimal
			}
		}
	}

	// Parse time of day filter
	if timeOfDay := params.TimeOfDay; timeOfDay != "" {
		filters.TimeOfDay = []string{timeOfDay}
	}

	// Parse hour filter (from hourRange parameter or hour parameter)
	hourParam := params.HourRange
	if hourParam == "" {
		hourParam = params.Hour
	}
	
	if hourParam != "" {
		if strings.Contains(hourParam, "-") {
			// Range format: "6-9"
			parts := strings.Split(hourParam, "-")
			if len(parts) == 2 {
				if start, err := strconv.Atoi(parts[0]); err == nil {
					if end, err := strconv.Atoi(parts[1]); err == nil {
						filters.Hour = &datastore.HourFilter{
							Start: start,
							End:   end,
						}
					}
				}
			}
		} else {
			// Single hour
			if hourVal, err := strconv.Atoi(hourParam); err == nil {
				filters.Hour = &datastore.HourFilter{
					Start: hourVal,
					End:   hourVal,
				}
			}
		}
	}

	// Parse date range
	if params.Date != "" {
		// Handle date shortcuts
		if date, err := datastore.ParseDateShortcut(params.Date); err == nil {
			filters.DateRange = &datastore.DateRange{
				Start: date,
				End:   date.AddDate(0, 0, 1).Add(-time.Second), // End of day
			}
		}
	} else if params.StartDate != "" && params.EndDate != "" {
		// Explicit date range
		if start, err := time.Parse("2006-01-02", params.StartDate); err == nil {
			if end, err := time.Parse("2006-01-02", params.EndDate); err == nil {
				filters.DateRange = &datastore.DateRange{
					Start: start,
					End:   end.AddDate(0, 0, 1).Add(-time.Second), // End of day
				}
			}
		}
	}

	// Parse species filter
	if params.Species != "" {
		filters.Species = []string{params.Species}
	}

	// Parse verified filter
	if params.Verified != "" {
		verified := params.Verified == "true" || params.Verified == "human"
		filters.Verified = &verified
	}

	// Parse locked filter
	if params.Locked != "" {
		locked := params.Locked == "true"
		filters.Locked = &locked
	}

	// Parse location filter
	if params.Location != "" {
		filters.Location = []string{params.Location}
	}

	// Use the advanced search method
	notes, totalCount, err := c.DS.SearchNotesAdvanced(&filters)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to perform advanced search",
				"filters", fmt.Sprintf("%+v", filters),
				"error", err.Error(),
			)
		}
		return nil, 0, err
	}

	// Cache the results
	cacheKey := fmt.Sprintf("adv_search:%s:%d:%d", params.Search, params.NumResults, params.Offset)
	c.detectionCache.Set(cacheKey, struct {
		Notes []datastore.Note
		Total int64
	}{notes, totalCount}, cache.DefaultExpiration)

	return notes, totalCount, nil
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
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to search notes",
				"query", search,
				"limit", numResults,
				"offset", offset,
				"error", err.Error(),
			)
		}
		return nil, 0, err
	}

	totalCount, err := c.DS.CountSearchResults(search)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to count search results",
				"query", search,
				"error", err.Error(),
			)
		}
		return nil, 0, err
	}

	// Cache the results
	c.detectionCache.Set(cacheKey, struct {
		Notes []datastore.Note
		Total int64
	}{notes, totalCount}, cache.DefaultExpiration)

	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieved search results",
			"query", search,
			"count", len(notes),
			"total", totalCount,
		)
	}

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
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to get all detections",
				"limit", numResults,
				"offset", offset,
				"error", err.Error(),
			)
		}
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

	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieved all detections",
			"count", len(notes),
			"total", totalResults,
		)
	}

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
	includeWeather := ctx.QueryParam("includeWeather") == "true"

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

	// Use the shared lock helper
	note, err := c.checkAndHandleLock(idStr, true)
	if err != nil {
		// Check error type to determine the appropriate status code
		if strings.Contains(err.Error(), "failed to check lock status") {
			// Database error during lock check should be 500
			return c.HandleError(ctx, err, err.Error(), http.StatusInternalServerError)
		} else {
			// Lock conflicts should be 409
			return c.HandleError(ctx, err, err.Error(), http.StatusConflict)
		}
	}

	// Parse request
	req := &DetectionRequest{}
	if err := ctx.Bind(req); err != nil {
		return c.HandleError(ctx, err, "Invalid request format", http.StatusBadRequest)
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
	if req.Verified != "" {
		var verified bool
		switch req.Verified {
		case "correct":
			verified = true
		case "false_positive":
			verified = false
		default:
			return c.HandleError(ctx, fmt.Errorf("invalid verification status"), "Invalid verification status", http.StatusBadRequest)
		}

		// Save review using the datastore method for reviews
		err = c.AddReview(note.ID, verified)
		if err != nil {
			return c.HandleError(ctx, err, fmt.Sprintf("Failed to update verification: %v", err), http.StatusInternalServerError)
		}

		// Handle ignored species
		if err := c.addToIgnoredSpecies(req.Verified, req.IgnoreSpecies); err != nil {
			return c.HandleError(ctx, err, err.Error(), http.StatusInternalServerError)
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

	// Use the shared lock helper without acquiring a lock
	note, err := c.checkAndHandleLock(idStr, false)
	if err != nil {
		return ctx.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
	}

	// Parse request
	req := &DetectionRequest{}
	if err := ctx.Bind(req); err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request format"})
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

// IgnoreSpecies adds a species to the ignored list
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

	// Add to ignored species list
	err := c.addSpeciesToIgnoredList(req.CommonName)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return ctx.NoContent(http.StatusNoContent)
}

// addToIgnoredSpecies handles the logic for adding species to the ignore list
func (c *Controller) addToIgnoredSpecies(verified, ignoreSpecies string) error {
	if verified == "false_positive" && ignoreSpecies != "" {
		return c.addSpeciesToIgnoredList(ignoreSpecies)
	}
	return nil
}

// addSpeciesToIgnoredList adds a species to the ignore list with proper concurrency control.
// It uses a mutex to ensure thread-safety when multiple requests try to modify the
// excluded species list simultaneously. The function:
// 1. Locks the controller's mutex to prevent concurrent modifications
// 2. Gets the latest settings from the settings package
// 3. Checks if the species is already in the excluded list
// 4. If not excluded, creates a copy of the exclude list to avoid race conditions
// 5. Adds the species to the new list and updates the settings
// 6. Saves the settings using the package's thread-safe function
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
		newExcludeList := make([]string, len(settings.Realtime.Species.Exclude))
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

// getWeatherUnits returns the weather units based on the provider and configuration
func (c *Controller) getWeatherUnits() string {
	// Read settings with mutex
	c.settingsMutex.RLock()
	defer c.settingsMutex.RUnlock()
	
	// Check the weather provider
	switch c.Settings.Realtime.Weather.Provider {
	case "openweather":
		// Return the configured units for OpenWeather
		return c.Settings.Realtime.Weather.OpenWeather.Units
	case "yrno":
		// yr.no always provides metric units
		return "metric"
	default:
		// Default to metric if provider is unknown
		return "metric"
	}
}
