// internal/api/v2/analytics.go
package api

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/analysis/species"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
)

const placeholderImageURL = "/assets/images/bird-placeholder.svg"
const maxSpeciesBatch = 10

// Analytics constants (file-local)
const (
	defaultConfidenceThreshold = 0.8 // Default confidence threshold for analytics
	defaultAnalyticsDays       = 30  // Default number of days for analytics queries
)

// SpeciesDailySummary represents a bird in the daily species summary API response
type SpeciesDailySummary struct {
	ScientificName     string `json:"scientific_name"`
	CommonName         string `json:"common_name"`
	SpeciesCode        string `json:"species_code,omitempty"`
	Count              int    `json:"count"`
	HourlyCounts       []int  `json:"hourly_counts"`
	HighConfidence     bool   `json:"high_confidence"`
	FirstHeard         string `json:"first_heard,omitempty"`
	LatestHeard        string `json:"latest_heard,omitempty"`
	ThumbnailURL       string `json:"thumbnail_url,omitempty"`
	IsNewSpecies       bool   `json:"is_new_species,omitempty"`        // First seen within tracking window
	DaysSinceFirstSeen int    `json:"days_since_first_seen,omitempty"` // Days since species was first detected
	// Multi-period tracking metadata
	IsNewThisYear   bool   `json:"is_new_this_year,omitempty"`   // First time this year
	IsNewThisSeason bool   `json:"is_new_this_season,omitempty"` // First time this season
	DaysThisYear    int    `json:"days_this_year,omitempty"`     // Days since first this year
	DaysThisSeason  int    `json:"days_this_season,omitempty"`   // Days since first this season
	CurrentSeason   string `json:"current_season,omitempty"`     // Current season name
}

// SpeciesSummary represents a bird in the overall species summary API response
type SpeciesSummary struct {
	ScientificName string  `json:"scientific_name"`
	CommonName     string  `json:"common_name"`
	SpeciesCode    string  `json:"species_code,omitempty"`
	Count          int     `json:"count"`
	FirstHeard     string  `json:"first_heard,omitempty"`
	LastHeard      string  `json:"last_heard,omitempty"`
	AvgConfidence  float64 `json:"avg_confidence,omitempty"`
	MaxConfidence  float64 `json:"max_confidence,omitempty"`
	ThumbnailURL   string  `json:"thumbnail_url,omitempty"`
}

// HourlyDistribution represents detections aggregated by hour
type HourlyDistribution struct {
	Hour  int `json:"hour"`
	Count int `json:"count"`
	// Date string `json:"date,omitempty"` // Removed as it's not populated or used
}

// NewSpeciesResponse represents a newly detected species in the API response
type NewSpeciesResponse struct {
	ScientificName string `json:"scientific_name"`
	CommonName     string `json:"common_name"`
	FirstHeardDate string `json:"first_heard_date"`
	ThumbnailURL   string `json:"thumbnail_url,omitempty"`
	CountInPeriod  int    `json:"count_in_period"` // How many times seen in the query period
}

// Define standard errors for date validation
var (
	ErrInvalidStartDate = errors.New("invalid start_date format. Use YYYY-MM-DD")
	ErrInvalidEndDate   = errors.New("invalid end_date format. Use YYYY-MM-DD")
	ErrDateOrder        = errors.New("start_date cannot be after end_date")
)

// dateRegex ensures YYYY-MM-DD format
var dateRegex = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// aggregatedBirdInfo holds intermediate aggregated data for a species on a specific day.
type aggregatedBirdInfo struct {
	CommonName     string
	ScientificName string
	SpeciesCode    string
	Count          int
	HourlyCounts   [24]int
	HighConfidence bool
	First          string
	Latest         string
}

// initAnalyticsRoutes registers all analytics-related API endpoints
func (c *Controller) initAnalyticsRoutes() {
	// Create analytics API group - publicly accessible
	analyticsGroup := c.Group.Group("/analytics")

	// Species analytics routes
	speciesGroup := analyticsGroup.Group("/species")
	speciesGroup.GET("/daily", c.GetDailySpeciesSummary)
	speciesGroup.GET("/daily/batch", c.GetBatchDailySpeciesSummary) // Batch daily summaries endpoint
	speciesGroup.GET("/summary", c.GetSpeciesSummary)
	speciesGroup.GET("/detections/new", c.GetNewSpeciesDetections) // Renamed endpoint
	speciesGroup.GET("/thumbnails", c.GetSpeciesThumbnails)        // Batch thumbnail endpoint

	// Time analytics routes (can be implemented later)
	timeGroup := analyticsGroup.Group("/time")
	timeGroup.GET("/hourly", c.GetHourlyAnalytics)
	timeGroup.GET("/hourly/batch", c.GetBatchHourlySpeciesData) // Batch hourly data for multiple species
	timeGroup.GET("/daily", c.GetDailyAnalytics)
	timeGroup.GET("/daily/batch", c.GetBatchDailySpeciesData)         // Batch daily trends for multiple species
	timeGroup.GET("/distribution/hourly", c.GetTimeOfDayDistribution) // Renamed endpoint for time-of-day distribution
}

// GetDailySpeciesSummary handles GET /api/v2/analytics/species/daily
func (c *Controller) GetDailySpeciesSummary(ctx echo.Context) error {
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path

	// 1. Parse Parameters
	selectedDate, minConfidence, limit, err := c.parseDailySpeciesSummaryParams(ctx)
	if err != nil {
		// Error already logged in helper
		return err // Return the HTTP error created by the helper
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieving daily species summary",
			"date", selectedDate,
			"min_confidence", minConfidence,
			"limit", limit,
			"ip", ip,
			"path", path,
		)
	}

	// 2. Get Initial Data
	notes, err := c.DS.GetTopBirdsData(selectedDate, minConfidence)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to get initial daily species data", "date", selectedDate, "min_confidence", minConfidence, "error", err.Error(), "ip", ip, "path", path)
		}
		return c.HandleError(ctx, err, "Failed to get daily species data", http.StatusInternalServerError)
	}

	// 3. Aggregate Data (including fetching hourly counts)
	aggregatedData, err := c.aggregateDailySpeciesData(notes, selectedDate, minConfidence)
	if err != nil {
		// Errors during hourly fetch are logged within the helper, but we need to handle the overall failure
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to aggregate daily species data", "date", selectedDate, "error", err.Error(), "ip", ip, "path", path)
		}
		// Decide if this is a user error (bad data?) or server error
		// For now, assume server error if aggregation fails overall
		return c.HandleError(ctx, err, "Failed to process daily species data", http.StatusInternalServerError)
	}

	// 4. Build Response (including fetching thumbnails)
	result, err := c.buildDailySpeciesSummaryResponse(aggregatedData, selectedDate)
	if err != nil {
		// Error logged in helper
		return c.HandleError(ctx, err, "Failed to build response", http.StatusInternalServerError)
	}

	// 5. Sort by count first, then by latest detection time for stable ordering
	sort.Slice(result, func(i, j int) bool {
		// Primary sort: by detection count (descending)
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		// Secondary sort: by latest detection time (descending - most recent first)
		// This ensures stable ordering when counts are equal
		return result[i].LatestHeard > result[j].LatestHeard
	})

	// 6. Apply Limit
	if limit > 0 && limit < len(result) {
		result = result[:limit]
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Daily species summary retrieved",
			"date", selectedDate,
			"count", len(result),
			"limit_applied", limit > 0,
			"ip", ip,
			"path", path,
		)
	}

	// 7. Return JSON
	return ctx.JSON(http.StatusOK, result)
}

// GetBatchDailySpeciesSummary handles GET /api/v2/analytics/species/daily/batch
// Returns daily species summaries for multiple dates in a single request
func (c *Controller) GetBatchDailySpeciesSummary(ctx echo.Context) error {
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path

	// Parse and validate batch parameters
	dates, minConfidence, limit, err := c.parseBatchDailySummaryParams(ctx)
	if err != nil {
		return err
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieving batch daily species summary",
			"date_count", len(dates),
			"min_confidence", minConfidence,
			"limit", limit,
			"ip", ip,
			"path", path,
		)
	}

	// Process each date and collect results
	batchResults, processingErrors := c.processBatchDates(dates, minConfidence, limit, ip, path)

	// Handle results and errors
	return c.handleBatchResults(ctx, batchResults, processingErrors, len(dates), ip, path)
}

// parseBatchDailySummaryParams parses and validates parameters for batch daily summary requests
func (c *Controller) parseBatchDailySummaryParams(ctx echo.Context) (dates []string, minConfidence float64, limit int, err error) {
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path

	// Parse dates parameter
	datesParam := ctx.QueryParam("dates")
	if datesParam == "" {
		if c.apiLogger != nil {
			c.apiLogger.Error("Missing required parameter in batch daily summary",
				"parameter", "dates", "ip", ip, "path", path)
		}
		return nil, 0, 0, echo.NewHTTPError(http.StatusBadRequest, "Missing required parameter: dates (comma-separated list)")
	}

	// Parse and validate dates
	dateStrings := make([]string, 0)
	for dateStr := range strings.SplitSeq(datesParam, ",") {
		trimmed := strings.TrimSpace(dateStr)
		if trimmed != "" {
			dateStrings = append(dateStrings, trimmed)
		}
	}

	if len(dateStrings) == 0 {
		return nil, 0, 0, echo.NewHTTPError(http.StatusBadRequest, "No valid dates provided")
	}

	// Rate limiting - maximum 7 dates per request
	const maxBatchSize = 7
	if len(dateStrings) > maxBatchSize {
		if c.apiLogger != nil {
			c.apiLogger.Error("Batch size exceeded limit",
				"requested", len(dateStrings), "max", maxBatchSize, "ip", ip, "path", path)
		}
		return nil, 0, 0, echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("Too many dates requested. Maximum: %d", maxBatchSize))
	}

	// Validate date formats
	for _, dateStr := range dateStrings {
		if _, err := time.Parse("2006-01-02", dateStr); err != nil {
			if c.apiLogger != nil {
				c.apiLogger.Error("Invalid date in batch request",
					"date", dateStr, "error", err.Error(), "ip", ip, "path", path)
			}
			return nil, 0, 0, echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("Invalid date format: %s. Use YYYY-MM-DD", dateStr))
		}
	}

	// Parse min_confidence
	minConfidence = 0.0
	if minConfidenceStr := ctx.QueryParam("min_confidence"); minConfidenceStr != "" {
		if parsedConfidence, parseErr := strconv.ParseFloat(minConfidenceStr, 64); parseErr == nil {
			minConfidence = parsedConfidence / PercentageMultiplier
		} else if c.apiLogger != nil {
			c.apiLogger.Warn("Invalid min_confidence parameter in batch request, using default 0",
				"value", minConfidenceStr, "error", parseErr.Error(), "ip", ip, "path", path)
		}
	}

	// Parse limit
	limit = 0
	if limitStr := ctx.QueryParam("limit"); limitStr != "" {
		if parsedLimit, parseErr := strconv.Atoi(limitStr); parseErr == nil && parsedLimit > 0 {
			limit = parsedLimit
		} else if parseErr != nil && c.apiLogger != nil {
			c.apiLogger.Warn("Invalid limit parameter in batch request, ignoring limit",
				"value", limitStr, "error", parseErr.Error(), "ip", ip, "path", path)
		}
	}

	return dateStrings, minConfidence, limit, nil
}

// processBatchDates processes multiple dates and returns results and errors
func (c *Controller) processBatchDates(dates []string, minConfidence float64, limit int, ip, path string) (batchResults map[string][]SpeciesDailySummary, processingErrors []string) {
	batchResults = make(map[string][]SpeciesDailySummary)
	processingErrors = make([]string, 0)

	for _, selectedDate := range dates {
		result, err := c.processSingleDateForBatch(selectedDate, minConfidence, limit, ip, path)
		if err != nil {
			errorMsg := fmt.Sprintf("Failed to process date %s: %v", selectedDate, err)
			processingErrors = append(processingErrors, errorMsg)
			continue
		}
		batchResults[selectedDate] = result
	}

	return batchResults, processingErrors
}

// processSingleDateForBatch processes a single date using the same logic as the regular endpoint
func (c *Controller) processSingleDateForBatch(selectedDate string, minConfidence float64, limit int, ip, path string) ([]SpeciesDailySummary, error) {
	// Get data for the date
	notes, err := c.DS.GetTopBirdsData(selectedDate, minConfidence)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to get data for date in batch request",
				"date", selectedDate, "error", err.Error(), "ip", ip, "path", path)
		}
		return nil, err
	}

	// Aggregate data
	aggregatedData, err := c.aggregateDailySpeciesData(notes, selectedDate, minConfidence)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to aggregate data for date in batch request",
				"date", selectedDate, "error", err.Error(), "ip", ip, "path", path)
		}
		return nil, err
	}

	// Build response
	result, err := c.buildDailySpeciesSummaryResponse(aggregatedData, selectedDate)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to build response for date in batch request",
				"date", selectedDate, "error", err.Error(), "ip", ip, "path", path)
		}
		return nil, err
	}

	// Sort by count first, then by latest detection time for stable ordering
	sort.Slice(result, func(i, j int) bool {
		// Primary sort: by detection count (descending)
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		// Secondary sort: by latest detection time (descending - most recent first)
		// This ensures stable ordering when counts are equal
		return result[i].LatestHeard > result[j].LatestHeard
	})

	// Apply limit
	if limit > 0 && limit < len(result) {
		result = result[:limit]
	}

	return result, nil
}

// handleBatchResults handles the final response and error cases for batch requests
func (c *Controller) handleBatchResults(ctx echo.Context, batchResults map[string][]SpeciesDailySummary, processingErrors []string, totalRequested int, ip, path string) error {
	// Log partial failures if any
	if len(processingErrors) > 0 && len(batchResults) > 0 {
		if c.apiLogger != nil {
			c.apiLogger.Warn("Batch request completed with partial failures",
				"successful", len(batchResults), "failed", len(processingErrors),
				"errors", processingErrors, "ip", ip, "path", path)
		}
	}

	// Return error if all dates failed
	if len(batchResults) == 0 {
		if c.apiLogger != nil {
			c.apiLogger.Error("All dates in batch request failed",
				"requested_dates", totalRequested, "errors", processingErrors, "ip", ip, "path", path)
		}
		return c.HandleError(ctx, fmt.Errorf("failed to process any requested dates"),
			"Failed to process batch request", http.StatusInternalServerError)
	}

	// Log successful completion
	if c.apiLogger != nil {
		c.apiLogger.Info("Batch daily species summary retrieved",
			"requested_dates", totalRequested, "successful_dates", len(batchResults),
			"failed_dates", len(processingErrors), "ip", ip, "path", path)
	}

	return ctx.JSON(http.StatusOK, batchResults)
}

// parseDailySpeciesSummaryParams parses and validates query parameters for the daily summary.
func (c *Controller) parseDailySpeciesSummaryParams(ctx echo.Context) (selectedDate string, minConfidence float64, limit int, err error) {
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path

	// Parse and validate date
	selectedDate = ctx.QueryParam("date")
	if selectedDate == "" {
		selectedDate = time.Now().Format("2006-01-02")
	} else if _, parseErr := time.Parse("2006-01-02", selectedDate); parseErr != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Invalid date format parameter", "date", selectedDate, "error", parseErr.Error(), "ip", ip, "path", path)
		}
		err = echo.NewHTTPError(http.StatusBadRequest, "Invalid date format. Use YYYY-MM-DD")
		return
	}

	// Parse min confidence
	minConfidence = 0.0 // Default
	minConfidenceStr := ctx.QueryParam("min_confidence")
	if minConfidenceStr != "" {
		parsedConfidence, parseErr := strconv.ParseFloat(minConfidenceStr, 64)
		if parseErr == nil {
			minConfidence = parsedConfidence / PercentageMultiplier // Convert from percentage to decimal
		} else if c.apiLogger != nil {
			c.apiLogger.Warn("Invalid min_confidence parameter, using default 0", "value", minConfidenceStr, "error", parseErr.Error(), "ip", ip, "path", path)
		}
	}

	// Parse limit
	limit = 0 // Default (no limit)
	limitStr := ctx.QueryParam("limit")
	if limitStr != "" {
		parsedLimit, parseErr := strconv.Atoi(limitStr)
		if parseErr == nil && parsedLimit > 0 {
			limit = parsedLimit
		} else if parseErr != nil && c.apiLogger != nil {
			c.apiLogger.Warn("Invalid limit parameter, ignoring limit", "value", limitStr, "error", parseErr.Error(), "ip", ip, "path", path)
		}
	}

	return // Return parsed values (and potentially nil error)
}

// aggregateDailySpeciesData processes raw notes, fetches hourly counts, and aggregates results.
func (c *Controller) aggregateDailySpeciesData(notes []datastore.Note, selectedDate string, minConfidence float64) (map[string]aggregatedBirdInfo, error) {
	aggregatedData := make(map[string]aggregatedBirdInfo)

	// Use a map to track which species' hourly counts have already been fetched to avoid redundant DB calls
	hourlyFetched := make(map[string]struct{})
	// Store fetched hourly counts to reuse
	hourlyCache := make(map[string][24]int)

	for i := range notes {
		note := &notes[i]

		// Skip notes below confidence threshold
		if note.Confidence < minConfidence {
			continue
		}

		birdKey := note.ScientificName
		var hourlyCounts [24]int // Declare without initial assignment
		var fetchErr error

		// Fetch hourly counts only once per species per request
		if _, fetched := hourlyFetched[birdKey]; !fetched {
			hourlyCounts, fetchErr = c.DS.GetHourlyOccurrences(selectedDate, note.CommonName, minConfidence)
			if fetchErr != nil {
				c.Debug("Error getting hourly counts for %s: %v", note.CommonName, fetchErr)
				if c.apiLogger != nil {
					c.apiLogger.Error("Error getting hourly counts during aggregation", "species", note.CommonName, "date", selectedDate, "error", fetchErr.Error())
				}
				// Optionally continue to process other species, or return error immediately
				// For now, let's continue but log the error.
				// Set a flag or specific error state if needed.
			} else {
				hourlyFetched[birdKey] = struct{}{}
				hourlyCache[birdKey] = hourlyCounts
			}
		} else {
			// Reuse cached hourly counts
			hourlyCounts = hourlyCache[birdKey]
		}

		// If fetching hourly counts failed for this species, skip aggregating it
		if fetchErr != nil {
			continue
		}

		// Calculate total count for this species for the day
		totalCount := 0
		for _, count := range hourlyCounts {
			totalCount += count
		}

		// Get or create the entry in the aggregated map
		data, exists := aggregatedData[birdKey]
		if !exists {
			data = aggregatedBirdInfo{
				CommonName:     note.CommonName,
				ScientificName: note.ScientificName,
				SpeciesCode:    note.SpeciesCode,
				First:          note.Time, // Initialize first/latest with the current note's time
				Latest:         note.Time,
			}
		}

		// Update aggregated data
		data.Count = totalCount // Use the count derived from GetHourlyOccurrences
		data.HourlyCounts = hourlyCounts
		data.HighConfidence = data.HighConfidence || note.Confidence >= defaultConfidenceThreshold

		// Update first/latest times if the current note is earlier/later
		if note.Time < data.First {
			data.First = note.Time
		}
		if note.Time > data.Latest {
			data.Latest = note.Time
		}

		aggregatedData[birdKey] = data
	}

	// Check if any hourly fetch errors occurred if strict error handling is needed
	// For now, returning nil error assuming partial results are acceptable if some hourly fetches fail.
	return aggregatedData, nil
}

// buildDailySpeciesSummaryResponse converts aggregated data into the final API response slice.
func (c *Controller) buildDailySpeciesSummaryResponse(aggregatedData map[string]aggregatedBirdInfo, selectedDate string) ([]SpeciesDailySummary, error) {
	// Collect scientific names for batch thumbnail fetching
	scientificNames := make([]string, 0, len(aggregatedData))
	for key := range aggregatedData {
		// Only include species that actually had detections (Count > 0)
		if aggregatedData[key].Count > 0 {
			scientificNames = append(scientificNames, key)
		}
	}

	// Batch fetch thumbnail URLs (cached only for fast response)
	thumbnailURLs := make(map[string]string)
	if c.BirdImageCache != nil && len(scientificNames) > 0 {
		batchResults := c.BirdImageCache.GetBatchCachedOnly(scientificNames)
		if len(batchResults) > 0 {
			for name := range batchResults {
				imgData := batchResults[name] // Access value using key
				if imgData.URL != "" {
					thumbnailURLs[name] = imgData.URL
				}
			}
		}
	}

	// Parse selected date to use for species status computation
	statusTime := time.Now() // Default to now
	if selectedDate != "" {
		if parsedDate, err := time.Parse("2006-01-02", selectedDate); err == nil {
			// Use end of day for the selected date to include all detections from that day
			statusTime = time.Date(parsedDate.Year(), parsedDate.Month(), parsedDate.Day(), 23, 59, 59, 0, parsedDate.Location())
		}
	}

	// Batch fetch species tracking status to avoid N+1 queries
	var batchSpeciesStatus map[string]species.SpeciesStatus
	if c.Processor != nil && c.Processor.NewSpeciesTracker != nil && len(scientificNames) > 0 {
		batchSpeciesStatus = c.Processor.NewSpeciesTracker.GetBatchSpeciesStatus(scientificNames, statusTime)
	}

	// Build the final result slice
	result := make([]SpeciesDailySummary, 0, len(scientificNames))
	for _, scientificName := range scientificNames { // Iterate using the filtered list
		data := aggregatedData[scientificName]

		// Convert hourly counts array to slice
		hourlyCountsSlice := make([]int, HoursPerDay)
		copy(hourlyCountsSlice, data.HourlyCounts[:])

		// Get thumbnail URL with fallback
		thumbnailURL, ok := thumbnailURLs[scientificName]
		if !ok || thumbnailURL == "" {
			thumbnailURL = placeholderImageURL
		}

		// Initialize the species summary
		speciesSummary := SpeciesDailySummary{
			ScientificName: data.ScientificName,
			CommonName:     data.CommonName,
			SpeciesCode:    data.SpeciesCode,
			Count:          data.Count,
			HourlyCounts:   hourlyCountsSlice,
			HighConfidence: data.HighConfidence,
			FirstHeard:     data.First,
			LatestHeard:    data.Latest,
			ThumbnailURL:   thumbnailURL,
		}

		// Add species tracking metadata from batch results
		if status, exists := batchSpeciesStatus[scientificName]; exists {
			speciesSummary.IsNewSpecies = status.IsNew

			// Only set days fields if they have valid values (>= 0)
			if status.DaysSinceFirst >= 0 {
				speciesSummary.DaysSinceFirstSeen = status.DaysSinceFirst
			}

			// Multi-period tracking metadata
			speciesSummary.IsNewThisYear = status.IsNewThisYear
			speciesSummary.IsNewThisSeason = status.IsNewThisSeason

			if status.DaysThisYear >= 0 {
				speciesSummary.DaysThisYear = status.DaysThisYear
			}

			if status.DaysThisSeason >= 0 {
				speciesSummary.DaysThisSeason = status.DaysThisSeason
			}

			speciesSummary.CurrentSeason = status.CurrentSeason
		}

		result = append(result, speciesSummary)
	}

	return result, nil
}

// GetSpeciesSummary handles GET /api/v2/analytics/species/summary
// This provides an overall summary of species detections
func (c *Controller) GetSpeciesSummary(ctx echo.Context) error {
	// apiStart := time.Now()

	// Get query parameters
	startDate := ctx.QueryParam("start_date")
	endDate := ctx.QueryParam("end_date")

	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieving species summary",
			"start_date", startDate,
			"end_date", endDate,
			"ip", ctx.RealIP(),
			"path", ctx.Request().URL.Path,
		)
	}

	// Validate date range
	if err := parseAndValidateDateRange(startDate, endDate); err != nil {
		// Convert standard error to HTTP error
		if errors.Is(err, ErrInvalidStartDate) || errors.Is(err, ErrInvalidEndDate) || errors.Is(err, ErrDateOrder) {
			if c.apiLogger != nil {
				c.apiLogger.Error("Invalid date parameters",
					"start_date", startDate,
					"end_date", endDate,
					"error", err.Error(),
					"ip", ctx.RealIP(),
					"path", ctx.Request().URL.Path,
				)
			}
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		// Handle unexpected errors
		if c.apiLogger != nil {
			c.apiLogger.Error("Error validating date range",
				"start_date", startDate,
				"end_date", endDate,
				"error", err.Error(),
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Error validating date range")
	}

	// Retrieve species summary data from the datastore with date filtering
	// TODO(context-timeout): Add configurable timeout for complex analytics queries (e.g., 30s for summary data)
	// TODO(telemetry): Report slow queries (>5s), timeouts, and cancellations to internal/telemetry for monitoring
	dbStart := time.Now()
	summaryData, err := c.DS.GetSpeciesSummaryData(ctx.Request().Context(), startDate, endDate)
	dbDuration := time.Since(dbStart)

	// log.Printf("GetSpeciesSummary: Database query completed in %v, got %d records", dbDuration, len(summaryData))

	if c.apiLogger != nil {
		c.apiLogger.Info("Database query completed",
			"duration_ms", dbDuration.Milliseconds(),
			"record_count", len(summaryData),
			"ip", ctx.RealIP(),
			"path", ctx.Request().URL.Path,
		)
	}

	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to get species summary data",
				"start_date", startDate,
				"end_date", endDate,
				"error", err.Error(),
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return c.HandleError(ctx, err, "Failed to get species summary data", http.StatusInternalServerError)
	}

	// Convert datastore model to API response model
	response := make([]SpeciesSummary, 0, len(summaryData))

	// Collect scientific names for batch thumbnail fetching
	scientificNames := make([]string, 0, len(summaryData))
	for i := range summaryData {
		scientificNames = append(scientificNames, summaryData[i].ScientificName)
	}

	// Batch fetch thumbnail URLs (cached only for fast response)
	var thumbnailURLs map[string]imageprovider.BirdImage
	if c.BirdImageCache != nil && len(scientificNames) > 0 {
		thumbStart := time.Now()
		if c.apiLogger != nil {
			c.apiLogger.Debug("Fetching cached thumbnails only",
				"count", len(scientificNames),
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		// Use GetBatchCachedOnly for fast response - frontend will fetch missing thumbnails async
		thumbnailURLs = c.BirdImageCache.GetBatchCachedOnly(scientificNames)
		thumbDuration := time.Since(thumbStart)
		if c.apiLogger != nil {
			c.apiLogger.Info("Cached thumbnail fetch completed",
				"duration_ms", thumbDuration.Milliseconds(),
				"cached_count", len(thumbnailURLs),
				"requested_count", len(scientificNames),
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
	}

	for i := range summaryData {
		data := &summaryData[i]
		// Format the times as strings
		firstHeard := ""
		lastHeard := ""

		if !data.FirstSeen.IsZero() {
			firstHeard = data.FirstSeen.Format("2006-01-02 15:04:05")
		}

		if !data.LastSeen.IsZero() {
			lastHeard = data.LastSeen.Format("2006-01-02 15:04:05")
		}

		// Get bird thumbnail URL from batch results
		var thumbnailURL string
		if thumbnailURLs != nil {
			if birdImage, ok := thumbnailURLs[data.ScientificName]; ok {
				thumbnailURL = birdImage.URL
			}
		}

		// Add to response
		summary := SpeciesSummary{
			ScientificName: data.ScientificName,
			CommonName:     data.CommonName,
			SpeciesCode:    data.SpeciesCode,
			Count:          data.Count,
			FirstHeard:     firstHeard,
			LastHeard:      lastHeard,
			AvgConfidence:  data.AvgConfidence,
			MaxConfidence:  data.MaxConfidence,
			ThumbnailURL:   thumbnailURL,
		}

		response = append(response, summary)
	}

	// Limit results if requested
	limitStr := ctx.QueryParam("limit")
	var limit int
	if limitStr != "" {
		var err error
		limit, err = strconv.Atoi(limitStr)
		if err == nil && limit > 0 && limit < len(response) {
			response = response[:limit]
		} else if err != nil && c.apiLogger != nil {
			c.apiLogger.Warn("Invalid limit parameter",
				"value", limitStr,
				"error", err.Error(),
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Species summary retrieved",
			"start_date", startDate,
			"end_date", endDate,
			"count", len(response),
			"limit", limit,
			"ip", ctx.RealIP(),
			"path", ctx.Request().URL.Path,
		)
	}

	// totalAPITime := time.Since(apiStart)
	// log.Printf("GetSpeciesSummary: Total API execution time: %v", totalAPITime)

	return ctx.JSON(http.StatusOK, response)
}

// GetHourlyAnalytics handles GET /api/v2/analytics/time/hourly
// This provides hourly detection patterns
func (c *Controller) GetHourlyAnalytics(ctx echo.Context) error {
	// Get query parameters
	date := ctx.QueryParam("date")
	speciesParam := ctx.QueryParam("species")

	// Validate required parameters
	if date == "" {
		if c.apiLogger != nil {
			c.apiLogger.Error("Missing required parameter in hourly analytics",
				"parameter", "date",
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return echo.NewHTTPError(http.StatusBadRequest, "Missing required parameter: date")
	}

	if speciesParam == "" {
		if c.apiLogger != nil {
			c.apiLogger.Error("Missing required parameter in hourly analytics",
				"parameter", "species",
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return echo.NewHTTPError(http.StatusBadRequest, "Missing required parameter: species")
	}

	// Validate date format
	if _, err := time.Parse("2006-01-02", date); err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Invalid date format in hourly analytics",
				"date", date,
				"error", err.Error(),
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid date format. Use YYYY-MM-DD")
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieving hourly analytics",
			"date", date,
			"species", speciesParam,
			"ip", ctx.RealIP(),
			"path", ctx.Request().URL.Path,
		)
	}

	// Get hourly analytics data from the datastore
	// TODO(context-timeout): Add configurable timeout for analytics queries to prevent resource exhaustion
	// Example: ctx, cancel := context.WithTimeout(ctx.Request().Context(), 30*time.Second); defer cancel()
	// TODO(telemetry): Report query timeouts and context cancellations to Sentry via internal/telemetry
	hourlyData, err := c.DS.GetHourlyAnalyticsData(ctx.Request().Context(), date, speciesParam)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to get hourly analytics data",
				"date", date,
				"species", speciesParam,
				"error", err.Error(),
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return c.HandleError(ctx, err, "Failed to get hourly analytics data", http.StatusInternalServerError)
	}

	// Create a 24-hour array filled with zeros
	hourlyCountsArray := make([]int, HoursPerDay)

	// Fill in the actual counts
	for i := range hourlyData {
		data := hourlyData[i]
		if data.Hour >= 0 && data.Hour < HoursPerDay {
			hourlyCountsArray[data.Hour] = data.Count
		}
	}

	// Calculate total count
	total := sumCounts(hourlyCountsArray)

	// Build the response
	response := map[string]any{
		"date":    date,
		"species": speciesParam,
		"counts":  hourlyCountsArray,
		"total":   total,
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Hourly analytics retrieved",
			"date", date,
			"species", speciesParam,
			"total", total,
			"ip", ctx.RealIP(),
			"path", ctx.Request().URL.Path,
		)
	}

	return ctx.JSON(http.StatusOK, response)
}

// GetDailyAnalytics handles GET /api/v2/analytics/time/daily
// This provides daily detection patterns
func (c *Controller) GetDailyAnalytics(ctx echo.Context) error {
	// Get query parameters
	startDate := ctx.QueryParam("start_date")
	endDate := ctx.QueryParam("end_date")
	speciesParam := ctx.QueryParam("species")

	// --- Enhanced Validation ---
	// Check for empty required parameter first
	if startDate == "" {
		if c.apiLogger != nil {
			c.apiLogger.Error("Missing required parameter in daily analytics",
				"parameter", "start_date",
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return echo.NewHTTPError(http.StatusBadRequest, "Missing required parameter: start_date")
	}

	// Validate format strictly using regex to prevent any non-date characters
	if !dateRegex.MatchString(startDate) {
		if c.apiLogger != nil {
			c.apiLogger.Error("Invalid start_date format in daily analytics",
				"start_date", startDate,
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid start_date format or contains invalid characters. Use YYYY-MM-DD")
	}
	if endDate != "" && !dateRegex.MatchString(endDate) {
		if c.apiLogger != nil {
			c.apiLogger.Error("Invalid end_date format in daily analytics",
				"end_date", endDate,
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid end_date format or contains invalid characters. Use YYYY-MM-DD")
	}
	// --- End Enhanced Validation ---

	// Validate date values and chronological order (format is already checked by regex)
	if err := parseAndValidateDateRange(startDate, endDate); err != nil {
		// Convert standard error to HTTP error
		if errors.Is(err, ErrInvalidStartDate) || errors.Is(err, ErrInvalidEndDate) || errors.Is(err, ErrDateOrder) {
			if c.apiLogger != nil {
				c.apiLogger.Error("Invalid date range in daily analytics",
					"start_date", startDate,
					"end_date", endDate,
					"error", err.Error(),
					"ip", ctx.RealIP(),
					"path", ctx.Request().URL.Path,
				)
			}
			// Use the specific error message from parseAndValidateDateRange
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		// Handle unexpected errors from parseAndValidateDateRange
		log.Printf("Error validating date range: %v", err)
		if c.apiLogger != nil {
			c.apiLogger.Error("Unexpected error validating date range",
				"start_date", startDate,
				"end_date", endDate,
				"error", err.Error(),
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Error validating date range values")
	}

	// Default end date if not provided
	if endDate == "" {
		startTime, _ := time.Parse("2006-01-02", startDate) // Regex ensures this parse succeeds
		endDate = startTime.AddDate(0, 0, defaultAnalyticsDays).Format("2006-01-02")
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieving daily analytics",
			"start_date", startDate,
			"end_date", endDate,
			"species", speciesParam,
			"ip", ctx.RealIP(),
			"path", ctx.Request().URL.Path,
		)
	}

	// Get daily analytics data from the datastore
	// TODO(context-timeout): Add configurable timeout for analytics queries to prevent resource exhaustion
	// TODO(telemetry): Report query timeouts and context cancellations to Sentry via internal/telemetry
	dailyData, err := c.DS.GetDailyAnalyticsData(ctx.Request().Context(), startDate, endDate, speciesParam)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to get daily analytics data",
				"start_date", startDate,
				"end_date", endDate,
				"species", speciesParam,
				"error", err.Error(),
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return c.HandleError(ctx, err, "Failed to get daily analytics data", http.StatusInternalServerError)
	}

	// Build the response
	type DailyResponse struct {
		Date  string `json:"date"`
		Count int    `json:"count"`
	}

	response := struct {
		StartDate string          `json:"start_date"`
		EndDate   string          `json:"end_date"`
		Species   string          `json:"species,omitempty"`
		Data      []DailyResponse `json:"data"`
		Total     int             `json:"total"`
	}{
		StartDate: startDate,
		EndDate:   endDate,
		Species:   speciesParam,
		Data:      make([]DailyResponse, 0, len(dailyData)),
	}

	// Convert dailyData to response format and calculate total
	totalCount := 0
	for i := range dailyData {
		data := dailyData[i]
		response.Data = append(response.Data, DailyResponse{
			Date:  data.Date,
			Count: data.Count,
		})
		totalCount += data.Count
	}
	response.Total = totalCount

	if c.apiLogger != nil {
		c.apiLogger.Info("Daily analytics retrieved",
			"start_date", startDate,
			"end_date", endDate,
			"species", speciesParam,
			"data_points", len(response.Data),
			"total", totalCount,
			"ip", ctx.RealIP(),
			"path", ctx.Request().URL.Path,
		)
	}

	return ctx.JSON(http.StatusOK, response)
}

// GetTimeOfDayDistribution handles GET /api/v2/analytics/time/distribution/hourly
// Returns an aggregated count of detections by hour of day across the given date range
func (c *Controller) GetTimeOfDayDistribution(ctx echo.Context) error {
	// Get query parameters
	startDate := ctx.QueryParam("start_date")
	endDate := ctx.QueryParam("end_date")
	speciesParam := ctx.QueryParam("species") // Optional species filter

	// Set default date range if not provided (before validation)
	if startDate == "" {
		startDate = time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	}
	if endDate == "" {
		endDate = time.Now().Format("2006-01-02")
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieving time of day distribution",
			"start_date", startDate,
			"end_date", endDate,
			"species", speciesParam,
			"ip", ctx.RealIP(),
			"path", ctx.Request().URL.Path,
		)
	}

	// Validate date formats and chronological order
	if err := parseAndValidateDateRange(startDate, endDate); err != nil {
		// Convert standard error to HTTP error
		if errors.Is(err, ErrInvalidStartDate) || errors.Is(err, ErrInvalidEndDate) || errors.Is(err, ErrDateOrder) {
			if c.apiLogger != nil {
				c.apiLogger.Error("Invalid date range in time of day distribution",
					"start_date", startDate,
					"end_date", endDate,
					"error", err.Error(),
					"ip", ctx.RealIP(),
					"path", ctx.Request().URL.Path,
				)
			}
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		// Handle unexpected errors
		if c.apiLogger != nil {
			c.apiLogger.Error("Unexpected error validating date range",
				"start_date", startDate,
				"end_date", endDate,
				"error", err.Error(),
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Error validating date range")
	}

	// Get hourly distribution data from the datastore
	hourlyData, err := c.DS.GetHourlyDistribution(ctx.Request().Context(), startDate, endDate, speciesParam)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to get hourly distribution data",
				"start_date", startDate,
				"end_date", endDate,
				"species", speciesParam,
				"error", err.Error(),
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return c.HandleError(ctx, err, "Failed to get hourly distribution data", http.StatusInternalServerError)
	}

	// If no data is available yet, return an array with 24 empty hours
	if len(hourlyData) == 0 {
		emptyData := make([]HourlyDistribution, HoursPerDay)
		for hour := range HoursPerDay {
			emptyData[hour] = HourlyDistribution{Hour: hour, Count: 0}
		}

		if c.apiLogger != nil {
			c.apiLogger.Info("No hourly distribution data available",
				"start_date", startDate,
				"end_date", endDate,
				"species", speciesParam,
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}

		return ctx.JSON(http.StatusOK, emptyData)
	}

	// Ensure we have data for all 24 hours (fill in zeros for missing hours)
	completeHourlyData := make([]HourlyDistribution, HoursPerDay)
	for hour := range HoursPerDay {
		completeHourlyData[hour] = HourlyDistribution{Hour: hour, Count: 0}
	}

	// Fill in actual counts
	totalCount := 0
	for _, data := range hourlyData {
		if data.Hour >= 0 && data.Hour < 24 {
			completeHourlyData[data.Hour].Count = data.Count
			totalCount += data.Count
		}
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Hourly distribution retrieved",
			"start_date", startDate,
			"end_date", endDate,
			"species", speciesParam,
			"total", totalCount,
			"ip", ctx.RealIP(),
			"path", ctx.Request().URL.Path,
		)
	}

	return ctx.JSON(http.StatusOK, completeHourlyData)
}

// GetNewSpeciesDetections handles GET /api/v2/analytics/species/new
// Returns species whose absolute first detection occurred within the specified date range.
func (c *Controller) GetNewSpeciesDetections(ctx echo.Context) error {
	// Get query parameters
	startDate := ctx.QueryParam("start_date")
	endDate := ctx.QueryParam("end_date")

	// Set default date range if not provided (e.g., last 30 days)
	if startDate == "" {
		startDate = time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	}
	if endDate == "" {
		endDate = time.Now().Format("2006-01-02")
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieving new species detections",
			"start_date", startDate,
			"end_date", endDate,
			"ip", ctx.RealIP(),
			"path", ctx.Request().URL.Path,
		)
	}

	// Validate date formats
	if _, err := time.Parse("2006-01-02", startDate); err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Invalid start_date format in new species detections",
				"start_date", startDate,
				"error", err.Error(),
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid start_date format. Use YYYY-MM-DD")
	}
	if _, err := time.Parse("2006-01-02", endDate); err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Invalid end_date format in new species detections",
				"end_date", endDate,
				"error", err.Error(),
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid end_date format. Use YYYY-MM-DD")
	}

	// Ensure chronological order
	start, _ := time.Parse("2006-01-02", startDate)
	end, _ := time.Parse("2006-01-02", endDate)
	if start.After(end) {
		if c.apiLogger != nil {
			c.apiLogger.Error("Invalid date range in new species detections",
				"start_date", startDate,
				"end_date", endDate,
				"error", "start_date cannot be after end_date",
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return echo.NewHTTPError(http.StatusBadRequest, "`start_date` cannot be after `end_date`")
	}

	// Parse pagination parameters
	limit := 100 // Default limit
	offset := 0  // Default offset

	// Parse limit parameter if provided
	limitStr := ctx.QueryParam("limit")
	if limitStr != "" {
		parsedLimit, err := strconv.Atoi(limitStr)
		if err != nil {
			if c.apiLogger != nil {
				c.apiLogger.Error("Invalid limit parameter in new species detections",
					"limit", limitStr,
					"error", err.Error(),
					"ip", ctx.RealIP(),
					"path", ctx.Request().URL.Path,
				)
			}
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid limit parameter. Must be a positive integer.")
		}
		if parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	// Parse offset parameter if provided
	offsetStr := ctx.QueryParam("offset")
	if offsetStr != "" {
		parsedOffset, err := strconv.Atoi(offsetStr)
		if err != nil {
			if c.apiLogger != nil {
				c.apiLogger.Error("Invalid offset parameter in new species detections",
					"offset", offsetStr,
					"error", err.Error(),
					"ip", ctx.RealIP(),
					"path", ctx.Request().URL.Path,
				)
			}
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid offset parameter. Must be a non-negative integer.")
		}
		if parsedOffset >= 0 {
			offset = parsedOffset
		}
	}

	// Fetch data from datastore with pagination
	newSpeciesData, err := c.DS.GetNewSpeciesDetections(ctx.Request().Context(), startDate, endDate, limit, offset)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to get new species detections",
				"start_date", startDate,
				"end_date", endDate,
				"limit", limit,
				"offset", offset,
				"error", err.Error(),
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return c.HandleError(ctx, err, "Failed to get new species detections", http.StatusInternalServerError)
	}

	// Convert to response format and fetch thumbnails
	response := make([]NewSpeciesResponse, 0, len(newSpeciesData))
	scientificNames := make([]string, 0, len(newSpeciesData))
	for _, data := range newSpeciesData {
		scientificNames = append(scientificNames, data.ScientificName)
	}

	// Batch fetch thumbnail URLs if cache is available
	thumbnailURLs := make(map[string]string)
	if c.BirdImageCache != nil {
		batchResults := c.BirdImageCache.GetBatch(scientificNames)
		// Only populate map if results are not empty
		if len(batchResults) > 0 {
			for name := range batchResults {
				imgURL := batchResults[name].URL
				if imgURL != "" {
					thumbnailURLs[name] = imgURL
				}
			}
		}
	}

	for _, data := range newSpeciesData {
		// Get thumbnail URL from batch results, with fallback
		thumbnailURL, ok := thumbnailURLs[data.ScientificName]
		if !ok || thumbnailURL == "" {
			thumbnailURL = placeholderImageURL
		}

		response = append(response, NewSpeciesResponse{
			ScientificName: data.ScientificName,
			CommonName:     data.CommonName,
			FirstHeardDate: data.FirstSeenDate,
			ThumbnailURL:   thumbnailURL,
			CountInPeriod:  data.CountInPeriod,
		})
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("New species detections retrieved",
			"start_date", startDate,
			"end_date", endDate,
			"count", len(response),
			"limit", limit,
			"offset", offset,
			"ip", ctx.RealIP(),
			"path", ctx.Request().URL.Path,
		)
	}

	return ctx.JSON(http.StatusOK, response)
}

// Helper function to sum array values
func sumCounts(counts []int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}

// parseAndValidateDateRange checks if provided date strings are valid and in chronological order.
// It returns standard Go errors for validation failures.
func parseAndValidateDateRange(startDateStr, endDateStr string) error {
	var start, end time.Time
	var err error

	// Validate start date format if provided
	if startDateStr != "" {
		start, err = time.Parse("2006-01-02", startDateStr)
		if err != nil {
			// Return standard error
			return fmt.Errorf("%w: %w", ErrInvalidStartDate, err)
		}
	}

	// Validate end date format if provided
	if endDateStr != "" {
		end, err = time.Parse("2006-01-02", endDateStr)
		if err != nil {
			// Return standard error
			return fmt.Errorf("%w: %w", ErrInvalidEndDate, err)
		}
	}

	// Ensure chronological order only if both dates are provided and valid
	if startDateStr != "" && endDateStr != "" && !start.IsZero() && !end.IsZero() {
		if start.After(end) {
			// Return standard error
			return ErrDateOrder
		}
	}

	return nil // Dates are valid
}

// GetSpeciesThumbnails handles GET /api/v2/analytics/species/thumbnails
// Returns thumbnail URLs for multiple species in a single request
func (c *Controller) GetSpeciesThumbnails(ctx echo.Context) error {
	// Get species list from query parameters
	speciesParams := ctx.QueryParams()["species"]

	if len(speciesParams) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "No species provided")
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieving thumbnails for species",
			"count", len(speciesParams),
			"ip", ctx.RealIP(),
			"path", ctx.Request().URL.Path,
		)
	}

	// Create result map
	result := make(map[string]string)

	// Use the image cache if available
	if c.BirdImageCache != nil {
		// Get thumbnails in batch
		images := c.BirdImageCache.GetBatch(speciesParams)

		// Convert to simple map of scientific name -> URL
		for name := range images {
			if images[name].URL != "" {
				result[name] = images[name].URL
			} else {
				result[name] = placeholderImageURL
			}
		}

		// Add placeholder for any missing species
		for _, name := range speciesParams {
			if _, exists := result[name]; !exists {
				result[name] = placeholderImageURL
			}
		}
	} else {
		// No image cache, return placeholders for all
		for _, name := range speciesParams {
			result[name] = placeholderImageURL
		}
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Thumbnails retrieved",
			"requested", len(speciesParams),
			"found", len(result),
			"ip", ctx.RealIP(),
			"path", ctx.Request().URL.Path,
		)
	}

	return ctx.JSON(http.StatusOK, result)
}

// GetBatchHourlySpeciesData handles GET /api/v2/analytics/time/hourly/batch
// Returns hourly detection patterns for multiple species in a single request
func (c *Controller) GetBatchHourlySpeciesData(ctx echo.Context) error {
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path

	// Parse parameters
	speciesParams := ctx.QueryParams()["species"]
	date := ctx.QueryParam("date")

	// Validate required parameters
	if len(speciesParams) == 0 {
		if c.apiLogger != nil {
			c.apiLogger.Error("Missing required parameter in batch hourly species data",
				"parameter", "species",
				"ip", ip,
				"path", path,
			)
		}
		return echo.NewHTTPError(http.StatusBadRequest, "Missing required parameter: species (array)")
	}

	if date == "" {
		if c.apiLogger != nil {
			c.apiLogger.Error("Missing required parameter in batch hourly species data",
				"parameter", "date",
				"ip", ip,
				"path", path,
			)
		}
		return echo.NewHTTPError(http.StatusBadRequest, "Missing required parameter: date")
	}

	// Validate date format
	if _, err := time.Parse("2006-01-02", date); err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Invalid date format in batch hourly species data",
				"date", date,
				"error", err.Error(),
				"ip", ip,
				"path", path,
			)
		}
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid date format. Use YYYY-MM-DD")
	}

	// Rate limiting - maximum species per request
	if len(speciesParams) > maxSpeciesBatch {
		if c.apiLogger != nil {
			c.apiLogger.Error("Batch size exceeded limit in hourly species data",
				"requested", len(speciesParams), "max", maxSpeciesBatch, "ip", ip, "path", path)
		}
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("Too many species requested. Maximum: %d", maxSpeciesBatch))
	}

	// Parse optional min_confidence parameter
	minConfidence := 0.0
	if minConfidenceStr := ctx.QueryParam("min_confidence"); minConfidenceStr != "" {
		if parsedConfidence, parseErr := strconv.ParseFloat(minConfidenceStr, 64); parseErr == nil {
			minConfidence = parsedConfidence / PercentageMultiplier // Convert from percentage to decimal
		} else if c.apiLogger != nil {
			c.apiLogger.Warn("Invalid min_confidence parameter in batch hourly species data, using default 0",
				"value", minConfidenceStr, "error", parseErr.Error(), "ip", ip, "path", path)
		}
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieving batch hourly species data",
			"date", date,
			"species_count", len(speciesParams),
			"min_confidence", minConfidence,
			"ip", ip,
			"path", path,
		)
	}

	// Process each species
	results := make(map[string][]HourlyDistribution)
	processingErrors := make([]string, 0)
	seen := make(map[string]bool)

	for _, speciesItem := range speciesParams {
		// Trim whitespace from species name
		speciesItem = strings.TrimSpace(speciesItem)
		if speciesItem == "" {
			continue
		}

		// Skip if already processed
		if seen[speciesItem] {
			continue
		}
		seen[speciesItem] = true

		// Get hourly data for this species
		hourlyData, err := c.DS.GetHourlyAnalyticsData(ctx.Request().Context(), date, speciesItem)
		if err != nil {
			errorMsg := fmt.Sprintf("Failed to get hourly data for species %s: %v", speciesItem, err)
			processingErrors = append(processingErrors, errorMsg)
			if c.apiLogger != nil {
				c.apiLogger.Error("Error getting hourly data for species in batch request",
					"species", speciesItem, "date", date, "error", err.Error(), "ip", ip, "path", path)
			}
			continue
		}

		// Convert to HourlyDistribution format
		hourlyDistribution := make([]HourlyDistribution, HoursPerDay)
		for hour := range HoursPerDay {
			hourlyDistribution[hour] = HourlyDistribution{Hour: hour, Count: 0}
		}

		// Fill in actual data
		for _, data := range hourlyData {
			if data.Hour >= 0 && data.Hour < HoursPerDay {
				hourlyDistribution[data.Hour].Count = data.Count
			}
		}

		results[speciesItem] = hourlyDistribution
	}

	// Log partial failures if any
	if len(processingErrors) > 0 && len(results) > 0 {
		if c.apiLogger != nil {
			c.apiLogger.Warn("Batch hourly species data completed with partial failures",
				"successful", len(results), "failed", len(processingErrors),
				"errors", processingErrors, "ip", ip, "path", path)
		}
	}

	// Return error if all species failed
	if len(results) == 0 {
		if c.apiLogger != nil {
			c.apiLogger.Error("All species in batch hourly request failed",
				"requested_species", len(speciesParams), "errors", processingErrors, "ip", ip, "path", path)
		}
		return c.HandleError(ctx, fmt.Errorf("failed to process any requested species"),
			"Failed to process batch hourly request", http.StatusInternalServerError)
	}

	// Log successful completion
	if c.apiLogger != nil {
		c.apiLogger.Info("Batch hourly species data retrieved",
			"requested_species", len(speciesParams), "successful_species", len(results),
			"failed_species", len(processingErrors), "ip", ip, "path", path)
	}

	return ctx.JSON(http.StatusOK, results)
}

// GetBatchDailySpeciesData handles GET /api/v2/analytics/time/daily/batch
// Returns daily trend data for multiple species in a single request
func (c *Controller) GetBatchDailySpeciesData(ctx echo.Context) error {
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path

	// Parse parameters
	speciesParams := ctx.QueryParams()["species"]
	startDate := ctx.QueryParam("start_date")
	endDate := ctx.QueryParam("end_date")

	// Validate required parameters
	if len(speciesParams) == 0 {
		if c.apiLogger != nil {
			c.apiLogger.Error("Missing required parameter in batch daily species data",
				"parameter", "species",
				"ip", ip,
				"path", path,
			)
		}
		return echo.NewHTTPError(http.StatusBadRequest, "Missing required parameter: species (array)")
	}

	if startDate == "" {
		if c.apiLogger != nil {
			c.apiLogger.Error("Missing required parameter in batch daily species data",
				"parameter", "start_date",
				"ip", ip,
				"path", path,
			)
		}
		return echo.NewHTTPError(http.StatusBadRequest, "Missing required parameter: start_date")
	}

	// Validate date formats and chronological order
	if err := parseAndValidateDateRange(startDate, endDate); err != nil {
		if errors.Is(err, ErrInvalidStartDate) || errors.Is(err, ErrInvalidEndDate) || errors.Is(err, ErrDateOrder) {
			if c.apiLogger != nil {
				c.apiLogger.Error("Invalid date range in batch daily species data",
					"start_date", startDate,
					"end_date", endDate,
					"error", err.Error(),
					"ip", ip,
					"path", path,
				)
			}
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		// Handle unexpected errors
		if c.apiLogger != nil {
			c.apiLogger.Error("Unexpected error validating date range in batch daily species data",
				"start_date", startDate,
				"end_date", endDate,
				"error", err.Error(),
				"ip", ip,
				"path", path,
			)
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Error validating date range")
	}

	// Default end date if not provided (30 days from start)
	if endDate == "" {
		startTime, _ := time.Parse("2006-01-02", startDate) // parseAndValidateDateRange ensures this succeeds
		endDate = startTime.AddDate(0, 0, defaultAnalyticsDays).Format("2006-01-02")
	}

	// Deduplicate species list to avoid redundant DB calls and result overwrites
	uniqueSpecies := make([]string, 0, len(speciesParams))
	seen := make(map[string]bool)

	for _, speciesItem := range speciesParams {
		// Trim whitespace from species name
		trimmedSpecies := strings.TrimSpace(speciesItem)
		if trimmedSpecies == "" {
			continue // Skip empty entries
		}

		// Skip if already seen (case-sensitive deduplication)
		if seen[trimmedSpecies] {
			continue
		}

		// Add to unique list and mark as seen
		seen[trimmedSpecies] = true
		uniqueSpecies = append(uniqueSpecies, trimmedSpecies)
	}

	// Rate limiting - maximum unique species per request
	if len(uniqueSpecies) > maxSpeciesBatch {
		if c.apiLogger != nil {
			c.apiLogger.Error("Batch size exceeded limit in daily species data",
				"requested", len(speciesParams), "unique", len(uniqueSpecies), "max", maxSpeciesBatch, "ip", ip, "path", path)
		}
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("Too many unique species requested. Maximum: %d", maxSpeciesBatch))
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieving batch daily species data",
			"start_date", startDate,
			"end_date", endDate,
			"species_requested", len(speciesParams),
			"species_unique", len(uniqueSpecies),
			"ip", ip,
			"path", path,
		)
	}

	// Process each species
	type DailyResponse struct {
		Date  string `json:"date"`
		Count int    `json:"count"`
	}

	type SpeciesDailyData struct {
		StartDate string          `json:"start_date"`
		EndDate   string          `json:"end_date"`
		Species   string          `json:"species"`
		Data      []DailyResponse `json:"data"`
		Total     int             `json:"total"`
	}

	results := make(map[string]SpeciesDailyData)
	processingErrors := make([]string, 0)

	for _, speciesItem := range uniqueSpecies {
		// Species is already trimmed and validated in deduplication step

		// Get daily data for this species
		dailyData, err := c.DS.GetDailyAnalyticsData(ctx.Request().Context(), startDate, endDate, speciesItem)
		if err != nil {
			errorMsg := fmt.Sprintf("Failed to get daily data for species %s: %v", speciesItem, err)
			processingErrors = append(processingErrors, errorMsg)
			if c.apiLogger != nil {
				c.apiLogger.Error("Error getting daily data for species in batch request",
					"species", speciesItem, "start_date", startDate, "end_date", endDate,
					"error", err.Error(), "ip", ip, "path", path)
			}
			continue
		}

		// Convert to response format and calculate total
		responseData := make([]DailyResponse, 0, len(dailyData))
		totalCount := 0
		for _, data := range dailyData {
			responseData = append(responseData, DailyResponse{
				Date:  data.Date,
				Count: data.Count,
			})
			totalCount += data.Count
		}

		results[speciesItem] = SpeciesDailyData{
			StartDate: startDate,
			EndDate:   endDate,
			Species:   speciesItem,
			Data:      responseData,
			Total:     totalCount,
		}
	}

	// Log partial failures if any
	if len(processingErrors) > 0 && len(results) > 0 {
		if c.apiLogger != nil {
			c.apiLogger.Warn("Batch daily species data completed with partial failures",
				"successful", len(results), "failed", len(processingErrors),
				"errors", processingErrors, "ip", ip, "path", path)
		}
	}

	// Return error if all species failed
	if len(results) == 0 {
		if c.apiLogger != nil {
			c.apiLogger.Error("All species in batch daily request failed",
				"requested_species", len(speciesParams), "unique_species", len(uniqueSpecies), "errors", processingErrors, "ip", ip, "path", path)
		}
		return c.HandleError(ctx, fmt.Errorf("failed to process any requested species"),
			"Failed to process batch daily request", http.StatusInternalServerError)
	}

	// Log successful completion
	if c.apiLogger != nil {
		c.apiLogger.Info("Batch daily species data retrieved",
			"requested_species", len(speciesParams), "unique_species", len(uniqueSpecies), "successful_species", len(results),
			"failed_species", len(processingErrors), "ip", ip, "path", path)
	}

	return ctx.JSON(http.StatusOK, results)
}
