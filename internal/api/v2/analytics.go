// internal/api/v2/analytics.go
package api

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/analysis/species"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const placeholderImageURL = "/assets/images/bird-placeholder.svg"
const maxSpeciesBatch = 10

// Analytics constants (file-local)
const (
	defaultConfidenceThreshold = 0.8 // Default confidence threshold for analytics
	defaultAnalyticsDays       = 30  // Default number of days for analytics queries
	defaultNewSpeciesLimit     = 100 // Default pagination limit for new species queries
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
	ErrInvalidStartDate = errors.NewStd("invalid start_date format. Use YYYY-MM-DD")
	ErrInvalidEndDate   = errors.NewStd("invalid end_date format. Use YYYY-MM-DD")
	ErrDateOrder        = errors.NewStd("start_date cannot be after end_date")
)

// ErrResponseHandled is a sentinel error indicating the HTTP response has already been written.
// Callers receiving this error should return it immediately without further processing.
var ErrResponseHandled = errors.NewStd("response already handled")

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

	c.logInfoIfEnabled("Retrieving daily species summary",
		logger.String("date", selectedDate),
		logger.Float64("min_confidence", minConfidence),
		logger.Int("limit", limit),
		logger.String("ip", ip),
		logger.String("path", path),
	)

	// 2. Get Initial Data
	notes, err := c.DS.GetTopBirdsData(selectedDate, minConfidence)
	if err != nil {
		c.logErrorIfEnabled("Failed to get initial daily species data",
			logger.String("date", selectedDate),
			logger.Float64("min_confidence", minConfidence),
			logger.Error(err),
			logger.String("ip", ip),
			logger.String("path", path),
		)
		return c.HandleError(ctx, err, "Failed to get daily species data", http.StatusInternalServerError)
	}

	// 3. Aggregate Data (including fetching hourly counts)
	aggregatedData, err := c.aggregateDailySpeciesData(notes, selectedDate, minConfidence)
	if err != nil {
		// Errors during hourly fetch are logged within the helper, but we need to handle the overall failure
		c.logErrorIfEnabled("Failed to aggregate daily species data",
			logger.String("date", selectedDate),
			logger.Error(err),
			logger.String("ip", ip),
			logger.String("path", path),
		)
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

	// 5. Sort and apply limit
	result = sortAndLimitSpeciesSummary(result, limit)

	c.logInfoIfEnabled("Daily species summary retrieved",
		logger.String("date", selectedDate),
		logger.Int("count", len(result)),
		logger.Bool("limit_applied", limit > 0),
		logger.String("ip", ip),
		logger.String("path", path),
	)

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

	c.logInfoIfEnabled("Retrieving batch daily species summary",
		logger.Int("date_count", len(dates)),
		logger.Float64("min_confidence", minConfidence),
		logger.Int("limit", limit),
		logger.String("ip", ip),
		logger.String("path", path),
	)

	// Process each date and collect results
	batchResults, processingErrors := c.processBatchDates(dates, minConfidence, limit, ip, path)

	// Handle results and errors
	return c.handleBatchResults(ctx, batchResults, processingErrors, len(dates), ip, path)
}

// parseBatchDailySummaryParams parses and validates parameters for batch daily summary requests
func (c *Controller) parseBatchDailySummaryParams(ctx echo.Context) (dates []string, minConfidence float64, limit int, err error) {
	const maxBatchSize = 7

	// Parse and validate dates using shared helper
	dates, err = c.parseRequiredCommaSeparatedDates(ctx, "dates", "batch_daily_summary", maxBatchSize)
	if err != nil {
		return nil, 0, 0, err
	}

	// Parse min_confidence using shared helper
	minConfidence = c.parseOptionalFloat(ctx, "min_confidence", 0.0, PercentageMultiplier)

	// Parse limit using shared helper (0 means no limit)
	limit = c.parseOptionalPositiveInt(ctx, "limit", 0)

	return dates, minConfidence, limit, nil
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
		c.logErrorIfEnabled("Failed to get data for date in batch request",
			logger.String("date", selectedDate),
			logger.Error(err),
			logger.String("ip", ip),
			logger.String("path", path),
		)
		return nil, err
	}

	// Aggregate data
	aggregatedData, err := c.aggregateDailySpeciesData(notes, selectedDate, minConfidence)
	if err != nil {
		c.logErrorIfEnabled("Failed to aggregate data for date in batch request",
			logger.String("date", selectedDate),
			logger.Error(err),
			logger.String("ip", ip),
			logger.String("path", path),
		)
		return nil, err
	}

	// Build response
	result, err := c.buildDailySpeciesSummaryResponse(aggregatedData, selectedDate)
	if err != nil {
		c.logErrorIfEnabled("Failed to build response for date in batch request",
			logger.String("date", selectedDate),
			logger.Error(err),
			logger.String("ip", ip),
			logger.String("path", path),
		)
		return nil, err
	}

	// Sort and apply limit
	result = sortAndLimitSpeciesSummary(result, limit)

	return result, nil
}

// handleBatchResults handles the final response and error cases for batch requests
func (c *Controller) handleBatchResults(ctx echo.Context, batchResults map[string][]SpeciesDailySummary, processingErrors []string, totalRequested int, ip, path string) error {
	return c.handleBatchResponse(ctx, batchResults, len(batchResults), totalRequested, processingErrors, "batch daily species summary", ip, path)
}

// parseDailySpeciesSummaryParams parses and validates query parameters for the daily summary.
func (c *Controller) parseDailySpeciesSummaryParams(ctx echo.Context) (selectedDate string, minConfidence float64, limit int, err error) {
	// Parse and validate date (defaults to today if not provided)
	selectedDate = ctx.QueryParam("date")
	if selectedDate == "" {
		selectedDate = time.Now().Format("2006-01-02")
	} else if validErr := c.validateDateFormatWithResponse(ctx, selectedDate, "date", "daily species summary"); validErr != nil {
		err = validErr
		return
	}

	// Parse optional parameters with defaults
	minConfidence = c.parseOptionalFloat(ctx, "min_confidence", 0.0, PercentageMultiplier)
	limit = c.parseOptionalPositiveInt(ctx, "limit", 0)

	return
}

// aggregateDailySpeciesData processes raw notes, fetches hourly counts, and aggregates results.
// hourlyCountsCache manages caching of hourly counts per species
type hourlyCountsCache struct {
	fetched map[string]struct{}
	counts  map[string][24]int
}

func newHourlyCountsCache() *hourlyCountsCache {
	return &hourlyCountsCache{
		fetched: make(map[string]struct{}),
		counts:  make(map[string][24]int),
	}
}

func (c *Controller) aggregateDailySpeciesData(notes []datastore.Note, selectedDate string, minConfidence float64) (map[string]aggregatedBirdInfo, error) {
	aggregatedData := make(map[string]aggregatedBirdInfo)
	cache := newHourlyCountsCache()

	for i := range notes {
		note := &notes[i]
		if note.Confidence < minConfidence {
			continue
		}

		hourlyCounts, ok := c.fetchHourlyCounts(cache, note, selectedDate, minConfidence)
		if !ok {
			continue
		}

		c.updateAggregatedData(aggregatedData, note, &hourlyCounts)
	}

	return aggregatedData, nil
}

// fetchHourlyCounts retrieves hourly counts with caching
func (c *Controller) fetchHourlyCounts(cache *hourlyCountsCache, note *datastore.Note, selectedDate string, minConfidence float64) ([24]int, bool) {
	// Use CommonName as cache key since GetHourlyOccurrences queries by CommonName
	birdKey := note.CommonName

	if _, fetched := cache.fetched[birdKey]; fetched {
		return cache.counts[birdKey], true
	}

	hourlyCounts, err := c.DS.GetHourlyOccurrences(selectedDate, note.CommonName, minConfidence)
	if err != nil {
		c.Debug("Error getting hourly counts for %s: %v", note.CommonName, err)
		return [24]int{}, false
	}

	cache.fetched[birdKey] = struct{}{}
	cache.counts[birdKey] = hourlyCounts
	return hourlyCounts, true
}

// updateAggregatedData updates the aggregated map with note data
func (c *Controller) updateAggregatedData(aggregatedData map[string]aggregatedBirdInfo, note *datastore.Note, hourlyCounts *[24]int) {
	birdKey := note.ScientificName

	totalCount := 0
	for _, count := range hourlyCounts {
		totalCount += count
	}

	data, exists := aggregatedData[birdKey]
	if !exists {
		data = aggregatedBirdInfo{
			CommonName:     note.CommonName,
			ScientificName: note.ScientificName,
			SpeciesCode:    note.SpeciesCode,
			First:          note.Time,
			Latest:         note.Time,
		}
	}

	data.Count = totalCount
	data.HourlyCounts = *hourlyCounts
	data.HighConfidence = data.HighConfidence || note.Confidence >= defaultConfidenceThreshold

	if note.Time < data.First {
		data.First = note.Time
	}
	if note.Time > data.Latest {
		data.Latest = note.Time
	}

	aggregatedData[birdKey] = data
}

// buildDailySpeciesSummaryResponse converts aggregated data into the final API response slice.
func (c *Controller) buildDailySpeciesSummaryResponse(aggregatedData map[string]aggregatedBirdInfo, selectedDate string) ([]SpeciesDailySummary, error) {
	// Collect species names with detections
	scientificNames := collectSpeciesWithDetections(aggregatedData)

	// Batch fetch thumbnail URLs (cached only for fast response)
	thumbnailURLs := c.batchFetchCachedThumbnails(scientificNames)

	// Parse selected date for status computation
	statusTime := parseStatusTimeFromDate(selectedDate)

	// Batch fetch species tracking status
	batchSpeciesStatus := c.batchFetchSpeciesStatus(scientificNames, statusTime)

	// Build the final result slice
	result := make([]SpeciesDailySummary, 0, len(scientificNames))
	for _, scientificName := range scientificNames {
		data := aggregatedData[scientificName]
		thumbnailURL := getThumbnailWithFallback(thumbnailURLs, scientificName)
		summary := buildSpeciesSummaryFromData(&data, thumbnailURL)
		if status, exists := batchSpeciesStatus[scientificName]; exists {
			applySpeciesStatusToSummary(&summary, &status)
		}
		result = append(result, summary)
	}

	return result, nil
}

// collectSpeciesWithDetections returns scientific names of species with Count > 0
func collectSpeciesWithDetections(aggregatedData map[string]aggregatedBirdInfo) []string {
	names := make([]string, 0, len(aggregatedData))
	for key := range aggregatedData {
		if aggregatedData[key].Count > 0 {
			names = append(names, key)
		}
	}
	return names
}

// batchFetchCachedThumbnails fetches thumbnail URLs from cache only
func (c *Controller) batchFetchCachedThumbnails(scientificNames []string) map[string]string {
	thumbnailURLs := make(map[string]string)
	if c.BirdImageCache == nil || len(scientificNames) == 0 {
		return thumbnailURLs
	}

	batchResults := c.BirdImageCache.GetBatchCachedOnly(scientificNames)
	for name := range batchResults {
		if batchResults[name].URL != "" {
			thumbnailURLs[name] = batchResults[name].URL
		}
	}
	return thumbnailURLs
}

// parseStatusTimeFromDate parses selected date for species status computation
func parseStatusTimeFromDate(selectedDate string) time.Time {
	if selectedDate == "" {
		return time.Now()
	}
	parsedDate, err := time.Parse("2006-01-02", selectedDate)
	if err != nil {
		return time.Now()
	}
	// Use end of day for the selected date to include all detections from that day
	return time.Date(parsedDate.Year(), parsedDate.Month(), parsedDate.Day(), 23, 59, 59, 0, parsedDate.Location())
}

// batchFetchSpeciesStatus fetches species tracking status to avoid N+1 queries
func (c *Controller) batchFetchSpeciesStatus(scientificNames []string, statusTime time.Time) map[string]species.SpeciesStatus {
	if c.Processor == nil || c.Processor.NewSpeciesTracker == nil || len(scientificNames) == 0 {
		return nil
	}
	return c.Processor.NewSpeciesTracker.GetBatchSpeciesStatus(scientificNames, statusTime)
}

// getThumbnailWithFallback returns thumbnail URL or placeholder
func getThumbnailWithFallback(thumbnailURLs map[string]string, scientificName string) string {
	if url, ok := thumbnailURLs[scientificName]; ok && url != "" {
		return url
	}
	return placeholderImageURL
}

// buildSpeciesSummaryFromData creates a SpeciesDailySummary from aggregated data
func buildSpeciesSummaryFromData(data *aggregatedBirdInfo, thumbnailURL string) SpeciesDailySummary {
	hourlyCountsSlice := make([]int, HoursPerDay)
	copy(hourlyCountsSlice, data.HourlyCounts[:])

	return SpeciesDailySummary{
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
}

// applySpeciesStatusToSummary applies tracking metadata to summary
func applySpeciesStatusToSummary(summary *SpeciesDailySummary, status *species.SpeciesStatus) {
	summary.IsNewSpecies = status.IsNew

	if status.DaysSinceFirst >= 0 {
		summary.DaysSinceFirstSeen = status.DaysSinceFirst
	}

	summary.IsNewThisYear = status.IsNewThisYear
	summary.IsNewThisSeason = status.IsNewThisSeason

	if status.DaysThisYear >= 0 {
		summary.DaysThisYear = status.DaysThisYear
	}

	if status.DaysThisSeason >= 0 {
		summary.DaysThisSeason = status.DaysThisSeason
	}

	summary.CurrentSeason = status.CurrentSeason
}

// GetSpeciesSummary handles GET /api/v2/analytics/species/summary
// This provides an overall summary of species detections
func (c *Controller) GetSpeciesSummary(ctx echo.Context) error {
	startDate := ctx.QueryParam("start_date")
	endDate := ctx.QueryParam("end_date")
	ip, path := ctx.RealIP(), ctx.Request().URL.Path

	c.logInfoIfEnabled("Retrieving species summary",
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.String("ip", ip),
		logger.String("path", path),
	)

	if err := c.validateDateRangeWithResponse(ctx, startDate, endDate, "species summary"); err != nil {
		return err
	}

	// Retrieve species summary data from the datastore
	summaryData, dbDuration, err := c.fetchSpeciesSummaryData(ctx, startDate, endDate)
	c.logInfoIfEnabled("Database query completed",
		logger.Int64("duration_ms", dbDuration.Milliseconds()),
		logger.Int("record_count", len(summaryData)),
		logger.String("ip", ip),
		logger.String("path", path),
	)

	if err != nil {
		c.logErrorIfEnabled("Failed to get species summary data",
			logger.String("start_date", startDate),
			logger.String("end_date", endDate),
			logger.Error(err),
			logger.String("ip", ip),
			logger.String("path", path),
		)
		return c.HandleError(ctx, err, "Failed to get species summary data", http.StatusInternalServerError)
	}

	// Build response with thumbnails
	scientificNames := extractScientificNames(summaryData)
	thumbnailURLs := c.batchFetchThumbnailsWithLogging(scientificNames, ip, path)
	response := c.convertSummaryDataToResponse(summaryData, thumbnailURLs)

	// Apply limit
	response, limit := c.applyOptionalLimit(ctx, response, ip, path)

	c.logInfoIfEnabled("Species summary retrieved",
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.Int("count", len(response)),
		logger.Int("limit", limit),
		logger.String("ip", ip),
		logger.String("path", path),
	)

	return ctx.JSON(http.StatusOK, response)
}

// fetchSpeciesSummaryData fetches species summary data with timing
func (c *Controller) fetchSpeciesSummaryData(ctx echo.Context, startDate, endDate string) ([]datastore.SpeciesSummaryData, time.Duration, error) {
	dbStart := time.Now()
	summaryData, err := c.DS.GetSpeciesSummaryData(ctx.Request().Context(), startDate, endDate)
	return summaryData, time.Since(dbStart), err
}

// extractScientificNames extracts scientific names from summary data
func extractScientificNames(summaryData []datastore.SpeciesSummaryData) []string {
	names := make([]string, 0, len(summaryData))
	for i := range summaryData {
		names = append(names, summaryData[i].ScientificName)
	}
	return names
}

// batchFetchThumbnailsWithLogging fetches thumbnails with debug logging
func (c *Controller) batchFetchThumbnailsWithLogging(scientificNames []string, ip, path string) map[string]imageprovider.BirdImage {
	if c.BirdImageCache == nil || len(scientificNames) == 0 {
		return nil
	}

	c.logDebugIfEnabled("Fetching cached thumbnails only",
		logger.Int("count", len(scientificNames)),
		logger.String("ip", ip),
		logger.String("path", path),
	)
	thumbStart := time.Now()
	thumbnailURLs := c.BirdImageCache.GetBatchCachedOnly(scientificNames)
	thumbDuration := time.Since(thumbStart)
	c.logInfoIfEnabled("Cached thumbnail fetch completed",
		logger.Int64("duration_ms", thumbDuration.Milliseconds()),
		logger.Int("cached_count", len(thumbnailURLs)),
		logger.Int("requested_count", len(scientificNames)),
		logger.String("ip", ip),
		logger.String("path", path),
	)

	return thumbnailURLs
}

// convertSummaryDataToResponse converts datastore models to API response
func (c *Controller) convertSummaryDataToResponse(summaryData []datastore.SpeciesSummaryData, thumbnailURLs map[string]imageprovider.BirdImage) []SpeciesSummary {
	response := make([]SpeciesSummary, 0, len(summaryData))

	for i := range summaryData {
		data := &summaryData[i]
		response = append(response, SpeciesSummary{
			ScientificName: data.ScientificName,
			CommonName:     data.CommonName,
			SpeciesCode:    data.SpeciesCode,
			Count:          data.Count,
			FirstHeard:     formatTimeIfNotZero(data.FirstSeen),
			LastHeard:      formatTimeIfNotZero(data.LastSeen),
			AvgConfidence:  data.AvgConfidence,
			MaxConfidence:  data.MaxConfidence,
			ThumbnailURL:   getThumbnailURLFromBirdImage(thumbnailURLs, data.ScientificName),
		})
	}

	return response
}

// formatTimeIfNotZero formats time as string if not zero
func formatTimeIfNotZero(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}

// getThumbnailURLFromBirdImage extracts URL from BirdImage map
func getThumbnailURLFromBirdImage(thumbnailURLs map[string]imageprovider.BirdImage, scientificName string) string {
	if thumbnailURLs == nil {
		return ""
	}
	if birdImage, ok := thumbnailURLs[scientificName]; ok {
		return birdImage.URL
	}
	return ""
}

// applyOptionalLimit parses and applies limit parameter
func (c *Controller) applyOptionalLimit(ctx echo.Context, response []SpeciesSummary, ip, path string) (result []SpeciesSummary, appliedLimit int) {
	limitStr := ctx.QueryParam("limit")
	if limitStr == "" {
		return response, 0
	}

	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		c.logWarnIfEnabled("Invalid limit parameter",
			logger.String("value", limitStr),
			logger.Error(err),
			logger.String("ip", ip),
			logger.String("path", path),
		)
		return response, 0
	}

	if limit > 0 && limit < len(response) {
		return response[:limit], limit
	}
	return response, limit
}

// GetHourlyAnalytics handles GET /api/v2/analytics/time/hourly
// This provides hourly detection patterns
func (c *Controller) GetHourlyAnalytics(ctx echo.Context) error {
	const operation = "hourly analytics"

	// Validate required parameters
	if err := c.requireQueryParam(ctx, "date", operation); err != nil {
		return err
	}
	if err := c.requireQueryParam(ctx, "species", operation); err != nil {
		return err
	}

	date := ctx.QueryParam("date")
	speciesParam := ctx.QueryParam("species")

	// Validate date format
	if err := c.validateDateFormatWithResponse(ctx, date, "date", operation); err != nil {
		return err
	}

	c.logInfoIfEnabled("Retrieving hourly analytics",
		logger.String("date", date),
		logger.String("species", speciesParam),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	// Get hourly analytics data from the datastore
	// TODO(context-timeout): Add configurable timeout for analytics queries to prevent resource exhaustion
	// Example: ctx, cancel := context.WithTimeout(ctx.Request().Context(), 30*time.Second); defer cancel()
	// TODO(telemetry): Report query timeouts and context cancellations to Sentry via internal/telemetry
	hourlyData, err := c.DS.GetHourlyAnalyticsData(ctx.Request().Context(), date, speciesParam)
	if err != nil {
		c.logErrorIfEnabled("Failed to get hourly analytics data",
			logger.String("date", date),
			logger.String("species", speciesParam),
			logger.Error(err),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
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

	c.logInfoIfEnabled("Hourly analytics retrieved",
		logger.String("date", date),
		logger.String("species", speciesParam),
		logger.Int("total", total),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	return ctx.JSON(http.StatusOK, response)
}

// GetDailyAnalytics handles GET /api/v2/analytics/time/daily
// This provides daily detection patterns
func (c *Controller) GetDailyAnalytics(ctx echo.Context) error {
	const operation = "daily analytics"

	// Validate required parameter
	if err := c.requireQueryParam(ctx, "start_date", operation); err != nil {
		return err
	}

	startDate := ctx.QueryParam("start_date")
	endDate := ctx.QueryParam("end_date")
	speciesParam := ctx.QueryParam("species")

	// Validate date formats strictly using regex
	if err := c.validateDateFormatStrictWithResponse(ctx, startDate, "start_date", operation); err != nil {
		return err
	}
	if err := c.validateDateFormatStrictWithResponse(ctx, endDate, "end_date", operation); err != nil {
		return err
	}

	// Validate date values and chronological order
	if err := c.validateDateRangeWithResponse(ctx, startDate, endDate, operation); err != nil {
		return err
	}

	// Default end date if not provided
	if endDate == "" {
		startTime, _ := time.Parse("2006-01-02", startDate) // Regex ensures this parse succeeds
		endDate = startTime.AddDate(0, 0, defaultAnalyticsDays).Format("2006-01-02")
	}

	c.logInfoIfEnabled("Retrieving daily analytics",
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.String("species", speciesParam),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	// Get daily analytics data from the datastore
	// TODO(context-timeout): Add configurable timeout for analytics queries to prevent resource exhaustion
	// TODO(telemetry): Report query timeouts and context cancellations to Sentry via internal/telemetry
	dailyData, err := c.DS.GetDailyAnalyticsData(ctx.Request().Context(), startDate, endDate, speciesParam)
	if err != nil {
		c.logErrorIfEnabled("Failed to get daily analytics data",
			logger.String("start_date", startDate),
			logger.String("end_date", endDate),
			logger.String("species", speciesParam),
			logger.Error(err),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
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

	c.logInfoIfEnabled("Daily analytics retrieved",
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.String("species", speciesParam),
		logger.Int("data_points", len(response.Data)),
		logger.Int("total", totalCount),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	return ctx.JSON(http.StatusOK, response)
}

// GetTimeOfDayDistribution handles GET /api/v2/analytics/time/distribution/hourly
// Returns an aggregated count of detections by hour of day across the given date range
func (c *Controller) GetTimeOfDayDistribution(ctx echo.Context) error {
	const operation = "time of day distribution"

	// Get query parameters with defaults
	startDate := ctx.QueryParam("start_date")
	endDate := ctx.QueryParam("end_date")
	speciesParam := ctx.QueryParam("species")

	if startDate == "" {
		startDate = time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	}
	if endDate == "" {
		endDate = time.Now().Format("2006-01-02")
	}

	// Validate date formats and chronological order
	if err := c.validateDateRangeWithResponse(ctx, startDate, endDate, operation); err != nil {
		return err
	}

	// Get hourly distribution data from the datastore
	hourlyData, err := c.DS.GetHourlyDistribution(ctx.Request().Context(), startDate, endDate, speciesParam)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get hourly distribution data", http.StatusInternalServerError)
	}

	// Return empty data if nothing available
	if len(hourlyData) == 0 {
		return ctx.JSON(http.StatusOK, initEmptyHourlyDistribution())
	}

	// Fill in actual counts for all 24 hours
	completeHourlyData := initEmptyHourlyDistribution()
	fillHourlyDistribution(completeHourlyData, hourlyData)

	return ctx.JSON(http.StatusOK, completeHourlyData)
}

// GetNewSpeciesDetections handles GET /api/v2/analytics/species/new
// Returns species whose absolute first detection occurred within the specified date range.
func (c *Controller) GetNewSpeciesDetections(ctx echo.Context) error {
	const operation = "GetNewSpeciesDetections"
	ip, path := ctx.RealIP(), ctx.Request().URL.Path

	// Get query parameters with defaults (last 30 days)
	startDate, endDate := getDefaultDateRange(ctx.QueryParam("start_date"), ctx.QueryParam("end_date"), -30, 0)

	c.logInfoIfEnabled("Retrieving new species detections",
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.String("ip", ip),
		logger.String("path", path),
	)

	// Validate date formats and order
	if err := c.validateNewSpeciesDateParams(ctx, startDate, endDate, operation); err != nil {
		return err
	}

	// Parse pagination parameters
	limit, offset, err := c.parsePaginationParams(ctx, defaultNewSpeciesLimit, 0)
	if err != nil {
		return err
	}

	// Fetch data from datastore
	newSpeciesData, err := c.DS.GetNewSpeciesDetections(ctx.Request().Context(), startDate, endDate, limit, offset)
	if err != nil {
		c.logErrorIfEnabled("Failed to get new species detections",
			logger.String("start_date", startDate),
			logger.String("end_date", endDate),
			logger.Int("limit", limit),
			logger.Int("offset", offset),
			logger.Error(err),
			logger.String("ip", ip),
			logger.String("path", path),
		)
		return c.HandleError(ctx, err, "Failed to get new species detections", http.StatusInternalServerError)
	}

	// Build response with thumbnails
	response := c.convertNewSpeciesToResponse(newSpeciesData)

	c.logInfoIfEnabled("New species detections retrieved",
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.Int("count", len(response)),
		logger.Int("limit", limit),
		logger.Int("offset", offset),
		logger.String("ip", ip),
		logger.String("path", path),
	)

	return ctx.JSON(http.StatusOK, response)
}

// validateNewSpeciesDateParams validates date formats and chronological order
func (c *Controller) validateNewSpeciesDateParams(ctx echo.Context, startDate, endDate, operation string) error {
	if err := c.validateDateFormatWithResponse(ctx, startDate, "start_date", operation); err != nil {
		return err
	}
	if err := c.validateDateFormatWithResponse(ctx, endDate, "end_date", operation); err != nil {
		return err
	}
	return c.validateDateOrderWithResponse(ctx, startDate, endDate, operation)
}

// convertNewSpeciesToResponse converts new species data to API response format
func (c *Controller) convertNewSpeciesToResponse(newSpeciesData []datastore.NewSpeciesData) []NewSpeciesResponse {
	scientificNames := extractNewSpeciesNames(newSpeciesData)
	thumbnailURLs := c.batchFetchThumbnailURLs(scientificNames)

	response := make([]NewSpeciesResponse, 0, len(newSpeciesData))
	for _, data := range newSpeciesData {
		response = append(response, NewSpeciesResponse{
			ScientificName: data.ScientificName,
			CommonName:     data.CommonName,
			FirstHeardDate: data.FirstSeenDate,
			ThumbnailURL:   getThumbnailWithFallback(thumbnailURLs, data.ScientificName),
			CountInPeriod:  data.CountInPeriod,
		})
	}
	return response
}

// extractNewSpeciesNames extracts scientific names from new species data
func extractNewSpeciesNames(data []datastore.NewSpeciesData) []string {
	names := make([]string, 0, len(data))
	for _, d := range data {
		names = append(names, d.ScientificName)
	}
	return names
}

// batchFetchThumbnailURLs fetches thumbnail URLs from cache
func (c *Controller) batchFetchThumbnailURLs(scientificNames []string) map[string]string {
	thumbnailURLs := make(map[string]string)
	if c.BirdImageCache == nil {
		return thumbnailURLs
	}

	batchResults := c.BirdImageCache.GetBatch(scientificNames)
	for name := range batchResults {
		if batchResults[name].URL != "" {
			thumbnailURLs[name] = batchResults[name].URL
		}
	}
	return thumbnailURLs
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

// validateDateRangeWithResponse validates date range and returns an appropriate HTTP error if validation fails.
// This consolidates the common pattern of date validation + error logging + HTTP response.
// The operation parameter is used for log context (e.g., "species summary", "time of day distribution").
// Returns nil if validation passes, or ErrResponseHandled if an error response was sent to the client.
// Callers should check: if err := c.validateDateRangeWithResponse(...); err != nil { return err }
func (c *Controller) validateDateRangeWithResponse(ctx echo.Context, startDate, endDate, operation string) error {
	if err := parseAndValidateDateRange(startDate, endDate); err != nil {
		// Check if it's a known date validation error
		if errors.Is(err, ErrInvalidStartDate) || errors.Is(err, ErrInvalidEndDate) || errors.Is(err, ErrDateOrder) {
			_ = c.HandleError(ctx, err, err.Error(), http.StatusBadRequest)
			return ErrResponseHandled
		}

		// Handle unexpected errors
		_ = c.HandleError(ctx, err, "Error validating date range", http.StatusInternalServerError)
		return ErrResponseHandled
	}
	return nil
}

// sortAndLimitSpeciesSummary sorts species summaries by count (descending) then by latest detection time,
// and applies an optional limit. This is used by multiple analytics endpoints.
func sortAndLimitSpeciesSummary(result []SpeciesDailySummary, limit int) []SpeciesDailySummary {
	sort.Slice(result, func(i, j int) bool {
		// Primary sort: by detection count (descending)
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		// Secondary sort: by latest detection time (descending - most recent first)
		return result[i].LatestHeard > result[j].LatestHeard
	})

	// Apply limit if specified
	if limit > 0 && limit < len(result) {
		return result[:limit]
	}
	return result
}

// GetSpeciesThumbnails handles GET /api/v2/analytics/species/thumbnails
// Returns thumbnail URLs for multiple species in a single request
func (c *Controller) GetSpeciesThumbnails(ctx echo.Context) error {
	speciesParams := ctx.QueryParams()["species"]
	if len(speciesParams) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "No species provided")
	}

	result := c.buildThumbnailMap(speciesParams)
	return ctx.JSON(http.StatusOK, result)
}

// buildThumbnailMap creates a map of species names to thumbnail URLs.
func (c *Controller) buildThumbnailMap(speciesParams []string) map[string]string {
	result := make(map[string]string)

	if c.BirdImageCache == nil {
		// No image cache, return placeholders for all
		for _, name := range speciesParams {
			result[name] = placeholderImageURL
		}
		return result
	}

	// Get thumbnails in batch from cache
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

	return result
}

// GetBatchHourlySpeciesData handles GET /api/v2/analytics/time/hourly/batch
// Returns hourly detection patterns for multiple species in a single request
func (c *Controller) GetBatchHourlySpeciesData(ctx echo.Context) error {
	const operation = "GetBatchHourlySpeciesData"
	ip, path := ctx.RealIP(), ctx.Request().URL.Path

	// Validate parameters
	speciesParams, date, err := c.validateBatchHourlyParams(ctx, operation)
	if err != nil {
		return err
	}

	minConfidence := c.parseOptionalFloat(ctx, "min_confidence", 0.0, PercentageMultiplier)
	c.logInfoIfEnabled("Retrieving batch hourly species data",
		logger.String("date", date),
		logger.Int("species_count", len(speciesParams)),
		logger.Float64("min_confidence", minConfidence),
		logger.String("ip", ip),
		logger.String("path", path),
	)

	// Process all species
	results, processingErrors := c.processHourlyBatchSpecies(ctx, speciesParams, date, ip, path)

	// Handle results
	return c.handleBatchHourlyResults(ctx, results, processingErrors, len(speciesParams), ip, path)
}

// validateBatchHourlyParams validates and returns batch hourly parameters
func (c *Controller) validateBatchHourlyParams(ctx echo.Context, operation string) (speciesParams []string, date string, err error) {
	speciesParams = ctx.QueryParams()["species"]
	date = ctx.QueryParam("date")

	if err := c.requireQueryArrayParam(ctx, "species", operation); err != nil {
		return nil, "", err
	}
	if err := c.requireQueryParam(ctx, "date", operation); err != nil {
		return nil, "", err
	}
	if err := c.validateDateFormatWithResponse(ctx, date, "date", operation); err != nil {
		return nil, "", err
	}
	if err := c.validateBatchSize(ctx, len(speciesParams), maxSpeciesBatch, operation); err != nil {
		return nil, "", err
	}

	return speciesParams, date, nil
}

// processHourlyBatchSpecies processes each species for batch hourly data
func (c *Controller) processHourlyBatchSpecies(ctx echo.Context, speciesParams []string, date, ip, path string) (results map[string][]HourlyDistribution, processingErrors []string) {
	results = make(map[string][]HourlyDistribution)
	processingErrors = make([]string, 0)
	seen := make(map[string]bool)

	for _, speciesItem := range speciesParams {
		speciesItem = strings.TrimSpace(speciesItem)
		if speciesItem == "" || seen[speciesItem] {
			continue
		}
		seen[speciesItem] = true

		hourlyData, err := c.DS.GetHourlyAnalyticsData(ctx.Request().Context(), date, speciesItem)
		if err != nil {
			processingErrors = append(processingErrors, fmt.Sprintf("Failed to get hourly data for species %s: %v", speciesItem, err))
			c.logErrorIfEnabled("Error getting hourly data for species in batch request",
				logger.String("species", speciesItem),
				logger.String("date", date),
				logger.Error(err),
				logger.String("ip", ip),
				logger.String("path", path),
			)
			continue
		}

		hourlyDistribution := initEmptyHourlyDistribution()
		fillHourlyDistributionFromAnalytics(hourlyDistribution, hourlyData)
		results[speciesItem] = hourlyDistribution
	}

	return results, processingErrors
}

// handleBatchHourlyResults logs and returns batch hourly results
func (c *Controller) handleBatchHourlyResults(ctx echo.Context, results map[string][]HourlyDistribution, processingErrors []string, requestedCount int, ip, path string) error {
	return c.handleBatchResponse(ctx, results, len(results), requestedCount, processingErrors, "batch hourly species data", ip, path)
}

// BatchDailyResponse represents a single day's count for batch daily data
type BatchDailyResponse struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// SpeciesDailyData represents daily trend data for a single species
type SpeciesDailyData struct {
	StartDate string               `json:"start_date"`
	EndDate   string               `json:"end_date"`
	Species   string               `json:"species"`
	Data      []BatchDailyResponse `json:"data"`
	Total     int                  `json:"total"`
}

// GetBatchDailySpeciesData handles GET /api/v2/analytics/time/daily/batch
// Returns daily trend data for multiple species in a single request
func (c *Controller) GetBatchDailySpeciesData(ctx echo.Context) error {
	const operation = "GetBatchDailySpeciesData"
	ip, path := ctx.RealIP(), ctx.Request().URL.Path

	// Validate and parse parameters
	speciesParams, uniqueSpecies, startDate, endDate, err := c.validateBatchDailyParams(ctx, operation)
	if err != nil {
		return err
	}

	c.logInfoIfEnabled("Retrieving batch daily species data",
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.Int("species_requested", len(speciesParams)),
		logger.Int("species_unique", len(uniqueSpecies)),
		logger.String("ip", ip),
		logger.String("path", path),
	)

	// Process all species
	results, processingErrors := c.processDailyBatchSpecies(ctx, uniqueSpecies, startDate, endDate, ip, path)

	// Handle results
	return c.handleBatchDailyResults(ctx, results, processingErrors, len(speciesParams), len(uniqueSpecies), ip, path)
}

// validateBatchDailyParams validates and returns batch daily parameters
func (c *Controller) validateBatchDailyParams(ctx echo.Context, operation string) (speciesParams, uniqueSpecies []string, startDate, endDate string, err error) {
	speciesParams = ctx.QueryParams()["species"]
	startDate = ctx.QueryParam("start_date")
	endDate = ctx.QueryParam("end_date")

	if err = c.requireQueryArrayParam(ctx, "species", operation); err != nil {
		return
	}
	if err = c.requireQueryParam(ctx, "start_date", operation); err != nil {
		return
	}
	if err = c.validateDateRangeWithResponse(ctx, startDate, endDate, operation); err != nil {
		return
	}

	// Default end date if not provided
	if endDate == "" {
		startTime, _ := time.Parse("2006-01-02", startDate)
		endDate = startTime.AddDate(0, 0, defaultAnalyticsDays).Format("2006-01-02")
	}

	uniqueSpecies = deduplicateSpeciesList(speciesParams)
	if err = c.validateBatchSize(ctx, len(uniqueSpecies), maxSpeciesBatch, operation); err != nil {
		return
	}

	return speciesParams, uniqueSpecies, startDate, endDate, nil
}

// processDailyBatchSpecies processes each species for batch daily data
func (c *Controller) processDailyBatchSpecies(ctx echo.Context, uniqueSpecies []string, startDate, endDate, ip, path string) (results map[string]SpeciesDailyData, processingErrors []string) {
	results = make(map[string]SpeciesDailyData)
	processingErrors = make([]string, 0)

	for _, speciesItem := range uniqueSpecies {
		dailyData, err := c.DS.GetDailyAnalyticsData(ctx.Request().Context(), startDate, endDate, speciesItem)
		if err != nil {
			processingErrors = append(processingErrors, fmt.Sprintf("Failed to get daily data for species %s: %v", speciesItem, err))
			c.logErrorIfEnabled("Error getting daily data for species in batch request",
				logger.String("species", speciesItem),
				logger.String("start_date", startDate),
				logger.String("end_date", endDate),
				logger.Error(err),
				logger.String("ip", ip),
				logger.String("path", path),
			)
			continue
		}

		results[speciesItem] = buildSpeciesDailyData(speciesItem, startDate, endDate, dailyData)
	}

	return results, processingErrors
}

// buildSpeciesDailyData converts daily analytics data to response format
func buildSpeciesDailyData(speciesName, startDate, endDate string, dailyData []datastore.DailyAnalyticsData) SpeciesDailyData {
	responseData := make([]BatchDailyResponse, 0, len(dailyData))
	totalCount := 0
	for _, data := range dailyData {
		responseData = append(responseData, BatchDailyResponse{Date: data.Date, Count: data.Count})
		totalCount += data.Count
	}

	return SpeciesDailyData{
		StartDate: startDate,
		EndDate:   endDate,
		Species:   speciesName,
		Data:      responseData,
		Total:     totalCount,
	}
}

// handleBatchDailyResults logs and returns batch daily results
func (c *Controller) handleBatchDailyResults(ctx echo.Context, results map[string]SpeciesDailyData, processingErrors []string, requestedCount, uniqueCount int, ip, path string) error {
	if len(processingErrors) > 0 && len(results) > 0 {
		c.logWarnIfEnabled("Batch daily species data completed with partial failures",
			logger.Int("successful", len(results)),
			logger.Int("failed", len(processingErrors)),
			logger.Any("errors", processingErrors),
			logger.String("ip", ip),
			logger.String("path", path),
		)
	}

	if len(results) == 0 {
		c.logErrorIfEnabled("All species in batch daily request failed",
			logger.Int("requested_species", requestedCount),
			logger.Int("unique_species", uniqueCount),
			logger.Any("errors", processingErrors),
			logger.String("ip", ip),
			logger.String("path", path),
		)
		return c.HandleError(ctx, fmt.Errorf("failed to process any requested species"), "Failed to process batch daily request", http.StatusInternalServerError)
	}

	c.logInfoIfEnabled("Batch daily species data retrieved",
		logger.Int("requested_species", requestedCount),
		logger.Int("unique_species", uniqueCount),
		logger.Int("successful_species", len(results)),
		logger.Int("failed_species", len(processingErrors)),
		logger.String("ip", ip),
		logger.String("path", path),
	)
	return ctx.JSON(http.StatusOK, results)
}
