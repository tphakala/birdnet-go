// internal/api/v2/analytics.go
package analytics

import (
	"context"
	"encoding/csv"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/analysis/species"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const maxSpeciesBatch = 10

// Analytics constants (file-local)
const (
	defaultConfidenceThreshold = 0.8              // Default confidence threshold for analytics
	defaultAnalyticsDays       = 30               // Default number of days for analytics queries
	defaultNewSpeciesLimit     = 100              // Default pagination limit for new species queries
	analyticsQueryTimeout      = 30 * time.Second // Timeout for analytics database queries

	// Confidence distribution bin bounds (design spec section 6.5). The histogram bins span [0,1];
	// the default of 20 equal bins (width 0.05) aligns with the round confidence thresholds users
	// reason about (e.g. 0.80). The range is clamped so a malformed or extreme bins param can neither
	// break the binning nor produce an unreadably fine or coarse histogram.
	defaultConfidenceBins = 20
	minConfidenceBins     = 5
	maxConfidenceBins     = 50

	// Species ridgeline (who-sings-when) top-N bounds. The default is the readable top-N shown with no
	// selection; the max matches the control bar's species cap (MAX_SPECIES) so an explicit selection
	// of up to that many species is honored in full rather than silently truncated (a selection larger
	// than the default is the user's deliberate choice, crowding and all). Mirrors the succession max.
	defaultSpeciesRidgelineLimit = 5
	maxSpeciesRidgelineLimit     = 10

	// Arrival/departure phenology top-N bounds. The default matches the chart's maxSpecies cap; the
	// max keeps the Gantt's residency bars legible within the card's fixed height (one bar per species,
	// so beyond ~20 rows the bars and labels crowd).
	defaultSpeciesPhenologyLimit = 12
	maxSpeciesPhenologyLimit     = 20

	// Acoustic succession streamgraph top-N bounds. The default matches the chart's maxSpecies cap;
	// the max keeps the stacked streamgraph readable (beyond ~10 bands the wiggle layers crowd within
	// the card's fixed height).
	defaultSpeciesSuccessionLimit = 6
	maxSpeciesSuccessionLimit     = 10
)

// msgQueryTimeout is the user-facing message returned when an analytics query
// exceeds analyticsQueryTimeout (HTTP 408).
const msgQueryTimeout = "Query timeout - please try a smaller date range"

// withAnalyticsTimeout derives a deadline-bounded context for analytics datastore
// queries from the incoming request, using analyticsQueryTimeout. Callers MUST
// defer the returned cancel function (or call it before the next iteration in a
// loop). Centralizing the wrap keeps every analytics endpoint consistent: a query
// that exceeds the deadline surfaces as context.DeadlineExceeded, which handlers
// map to HTTP 408 rather than leaving the query unbounded.
func withAnalyticsTimeout(ctx echo.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx.Request().Context(), analyticsQueryTimeout)
}

// handleAnalyticsQueryError maps a single-query analytics datastore error to an
// HTTP response: a context deadline becomes 408 (msgQueryTimeout), any other error
// becomes 500 with genericMsg. opLabel names the operation in the structured log
// (" query timeout" / " query failed" is appended); the query error is logged
// alongside the supplied fields. The returned error is what the handler returns.
func (c *Handler) handleAnalyticsQueryError(ctx echo.Context, err error, opLabel, genericMsg string, fields ...logger.Field) error {
	fields = append(fields, logger.Error(err))
	switch {
	case errors.Is(err, context.Canceled):
		// Client disconnected (navigated away / closed the tab). An expected lifecycle
		// event, not a server error: log at info and return the non-standard
		// client-closed status, matching the convention in the media handler.
		c.LogInfoIfEnabled(opLabel+" query canceled by client", fields...)
		return c.HandleError(ctx, err, "Request canceled by client", apicore.StatusClientClosedRequest)
	case errors.Is(err, context.DeadlineExceeded):
		c.LogErrorIfEnabled(opLabel+" query timeout", fields...)
		return c.HandleError(ctx, err, msgQueryTimeout, http.StatusRequestTimeout)
	default:
		c.LogErrorIfEnabled(opLabel+" query failed", fields...)
		return c.HandleError(ctx, err, genericMsg, http.StatusInternalServerError)
	}
}

// logBatchQueryError logs a per-item failure inside a batch analytics request at the
// right level: a client cancellation (context.Canceled) is an expected disconnect
// logged at debug, any other error is a real failure logged at error. It returns
// true for a cancellation so the caller can stop the batch instead of continuing.
func (c *Handler) logBatchQueryError(msg string, err error, fields ...logger.Field) (canceled bool) {
	fields = append(fields, logger.Error(err))
	if errors.Is(err, context.Canceled) {
		c.LogDebugIfEnabled(msg+": client canceled request", fields...)
		return true
	}
	c.LogErrorIfEnabled(msg, fields...)
	return false
}

// SpeciesDailySummary represents a bird in the daily species summary API response
type SpeciesDailySummary struct {
	ScientificName     string  `json:"scientific_name"`
	CommonName         string  `json:"common_name"`
	SpeciesCode        string  `json:"species_code,omitempty"`
	Count              int     `json:"count"`
	HourlyCounts       []int   `json:"hourly_counts"`
	HighConfidence     bool    `json:"high_confidence"`
	MaxConfidence      float64 `json:"max_confidence,omitempty"` // Highest detection confidence on this day (0..1)
	FirstHeard         string  `json:"first_heard,omitempty"`
	LatestHeard        string  `json:"latest_heard,omitempty"`
	ThumbnailURL       string  `json:"thumbnail_url,omitempty"`
	IsNewSpecies       bool    `json:"is_new_species,omitempty"`        // First seen within tracking window
	DaysSinceFirstSeen int     `json:"days_since_first_seen,omitempty"` // Days since species was first detected
	DaysSinceLastSeen  int     `json:"days_since_last_seen,omitempty"`  // Days since the previous detection before this return; omitted unless > 0 (first-ever and same-day re-detections are not emitted)
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
	MaxConfidence  float64
	First          string
	Latest         string
}

// GetDailySpeciesSummary handles GET /api/v2/analytics/species/daily
func (c *Handler) GetDailySpeciesSummary(ctx echo.Context) error {
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path

	// 1. Parse Parameters
	selectedDate, minConfidence, limit, err := c.parseDailySpeciesSummaryParams(ctx)
	if err != nil {
		// Error already logged in helper
		return err // Return the HTTP error created by the helper
	}

	c.LogInfoIfEnabled("Retrieving daily species summary",
		logger.String("date", selectedDate),
		logger.Float64("min_confidence", minConfidence),
		logger.Int("limit", limit),
		logger.String("ip", ip),
		logger.String("path", path),
	)

	// Bound the per-request datastore work with a single deadline shared by the
	// initial query and the hourly aggregation below.
	queryCtx, cancel := withAnalyticsTimeout(ctx)
	defer cancel()

	// 2. Get Initial Data (limit applied at database level)
	notes, err := c.DS.GetTopBirdsData(queryCtx, selectedDate, minConfidence, limit)
	if err != nil {
		return c.handleAnalyticsQueryError(ctx, err, "Daily species summary", "Failed to get daily species data",
			logger.String("date", selectedDate),
			logger.Float64("min_confidence", minConfidence),
			logger.String("ip", ip),
			logger.String("path", path),
		)
	}

	// 3. Aggregate Data (including fetching hourly counts)
	aggregatedData, err := c.aggregateDailySpeciesData(queryCtx, notes, selectedDate, minConfidence)
	if err != nil {
		// Errors during hourly fetch are logged within the helper; map the overall failure.
		return c.handleAnalyticsQueryError(ctx, err, "Daily species aggregation", "Failed to process daily species data",
			logger.String("date", selectedDate),
			logger.String("ip", ip),
			logger.String("path", path),
		)
	}

	// 4. Build Response (including fetching thumbnails)
	result, err := c.buildDailySpeciesSummaryResponse(aggregatedData, selectedDate)
	if err != nil {
		// Error logged in helper
		return c.HandleError(ctx, err, "Failed to build response", http.StatusInternalServerError)
	}

	// 5. Sort (limit already applied at database level)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].LatestHeard > result[j].LatestHeard
	})

	c.LogInfoIfEnabled("Daily species summary retrieved",
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
func (c *Handler) GetBatchDailySpeciesSummary(ctx echo.Context) error {
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path

	// Parse and validate batch parameters
	dates, minConfidence, limit, err := c.parseBatchDailySummaryParams(ctx)
	if err != nil {
		return err
	}

	c.LogInfoIfEnabled("Retrieving batch daily species summary",
		logger.Int("date_count", len(dates)),
		logger.Float64("min_confidence", minConfidence),
		logger.Int("limit", limit),
		logger.String("ip", ip),
		logger.String("path", path),
	)

	// Process each date under a per-date query deadline (applied inside
	// processBatchDates); the request context still aborts the batch on disconnect.
	batchResults, processingErrors := c.processBatchDates(ctx.Request().Context(), dates, minConfidence, limit, ip, path)

	// Handle results and errors
	return c.handleBatchResults(ctx, batchResults, processingErrors, len(dates), ip, path)
}

// parseBatchDailySummaryParams parses and validates parameters for batch daily summary requests
func (c *Handler) parseBatchDailySummaryParams(ctx echo.Context) (dates []string, minConfidence float64, limit int, err error) {
	const maxBatchSize = 7

	// Parse and validate dates using shared helper
	dates, err = c.parseRequiredCommaSeparatedDates(ctx, "dates", "batch_daily_summary", maxBatchSize)
	if err != nil {
		return nil, 0, 0, err
	}

	// Parse min_confidence using shared helper
	minConfidence = c.parseOptionalFloat(ctx, "min_confidence", 0.0, apicore.PercentageMultiplier)

	// Parse limit using shared helper (0 means no limit)
	limit = c.parseOptionalPositiveInt(ctx, "limit", 0)

	return dates, minConfidence, limit, nil
}

// processBatchDates processes multiple dates and returns results and errors
func (c *Handler) processBatchDates(ctx context.Context, dates []string, minConfidence float64, limit int, ip, path string) (batchResults map[string][]SpeciesDailySummary, processingErrors []string) {
	batchResults = make(map[string][]SpeciesDailySummary)
	processingErrors = make([]string, 0)

	for _, selectedDate := range dates {
		// Stop cleanly if the client disconnected (request canceled): an expected
		// lifecycle event, not a processing failure worth recording.
		if err := ctx.Err(); err != nil {
			c.LogDebugIfEnabled("Batch daily species summary: client canceled request",
				logger.String("date", selectedDate),
				logger.Error(err),
				logger.String("ip", ip),
				logger.String("path", path),
			)
			break
		}
		// Bound each date's queries individually so one slow date cannot hang the batch.
		// Run in a closure with defer cancel() so the context timer is released even if
		// processSingleDateForBatch panics, instead of leaking until the timeout fires.
		result, err := func() ([]SpeciesDailySummary, error) {
			dateCtx, cancel := context.WithTimeout(ctx, analyticsQueryTimeout)
			defer cancel()
			return c.processSingleDateForBatch(dateCtx, selectedDate, minConfidence, limit, ip, path)
		}()
		if err != nil {
			if errors.Is(err, context.Canceled) {
				break // client disconnected; stop the batch without recording a failure
			}
			errorMsg := fmt.Sprintf("Failed to process date %s: %v", selectedDate, err)
			processingErrors = append(processingErrors, errorMsg)
			continue
		}
		batchResults[selectedDate] = result
	}

	return batchResults, processingErrors
}

// processSingleDateForBatch processes a single date using the same logic as the regular endpoint
func (c *Handler) processSingleDateForBatch(ctx context.Context, selectedDate string, minConfidence float64, limit int, ip, path string) ([]SpeciesDailySummary, error) {
	// Get data for the date (limit applied at database level)
	notes, err := c.DS.GetTopBirdsData(ctx, selectedDate, minConfidence, limit)
	if err != nil {
		c.logBatchQueryError("Failed to get data for date in batch request", err,
			logger.String("date", selectedDate),
			logger.String("ip", ip),
			logger.String("path", path),
		)
		return nil, err
	}

	// Aggregate data
	aggregatedData, err := c.aggregateDailySpeciesData(ctx, notes, selectedDate, minConfidence)
	if err != nil {
		c.logBatchQueryError("Failed to aggregate data for date in batch request", err,
			logger.String("date", selectedDate),
			logger.String("ip", ip),
			logger.String("path", path),
		)
		return nil, err
	}

	// Build response
	result, err := c.buildDailySpeciesSummaryResponse(aggregatedData, selectedDate)
	if err != nil {
		c.LogErrorIfEnabled("Failed to build response for date in batch request",
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
func (c *Handler) handleBatchResults(ctx echo.Context, batchResults map[string][]SpeciesDailySummary, processingErrors []string, totalRequested int, ip, path string) error {
	return c.handleBatchResponse(ctx, batchResults, len(batchResults), totalRequested, processingErrors, "batch daily species summary", ip, path)
}

// parseDailySpeciesSummaryParams parses and validates query parameters for the daily summary.
func (c *Handler) parseDailySpeciesSummaryParams(ctx echo.Context) (selectedDate string, minConfidence float64, limit int, err error) {
	// Parse and validate date (defaults to today if not provided)
	selectedDate = ctx.QueryParam("date")
	if selectedDate == "" {
		selectedDate = time.Now().Format(time.DateOnly)
	} else if validErr := c.validateDateFormatWithResponse(ctx, selectedDate, "date", "daily species summary"); validErr != nil {
		err = validErr
		return
	}

	// Parse optional parameters with defaults
	minConfidence = c.parseOptionalFloat(ctx, "min_confidence", 0.0, apicore.PercentageMultiplier)
	limit = c.parseOptionalPositiveInt(ctx, "limit", 0)

	return
}

// aggregateDailySpeciesData processes raw notes, fetches hourly counts, and aggregates results.
func (c *Handler) aggregateDailySpeciesData(ctx context.Context, notes []datastore.Note, selectedDate string, minConfidence float64) (map[string]aggregatedBirdInfo, error) {
	aggregatedData := make(map[string]aggregatedBirdInfo)

	if len(notes) == 0 {
		return aggregatedData, nil
	}

	// Collect all unique species that meet the confidence threshold, keyed by
	// scientific name. Keying on scientific name (not the localized common name)
	// keeps the hourly aggregation robust across models and locales: a localized
	// common name has no reliable reverse mapping back to a scientific name for
	// non-primary-model species (e.g. bats), which silently dropped them from the
	// summary. Scientific names resolve directly to label IDs for
	// every model, so no lossy round-trip is needed.
	uniqueSpecies := make(map[string]struct{})
	for i := range notes {
		if notes[i].Confidence >= minConfidence {
			uniqueSpecies[notes[i].ScientificName] = struct{}{}
		}
	}

	// Batch fetch hourly counts for all species in single query
	speciesList := slices.Collect(maps.Keys(uniqueSpecies))

	hourlyCounts, err := c.DS.GetBatchHourlyOccurrences(ctx, selectedDate, speciesList, minConfidence)
	if err != nil {
		return nil, fmt.Errorf("failed to get batch hourly occurrences: %w", err)
	}

	// Process notes with pre-fetched data
	for i := range notes {
		note := &notes[i]
		if note.Confidence < minConfidence {
			continue
		}

		counts, ok := hourlyCounts[note.ScientificName]
		if !ok {
			// Species not in batch result, skip
			continue
		}

		c.updateAggregatedData(aggregatedData, note, &counts)
	}

	return aggregatedData, nil
}

// updateAggregatedData updates the aggregated map with note data
func (c *Handler) updateAggregatedData(aggregatedData map[string]aggregatedBirdInfo, note *datastore.Note, hourlyCounts *[24]int) {
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
	// GetTopBirdsData already returns MAX(confidence) per species, but take the max
	// across notes defensively in case multiple notes for a species are aggregated.
	data.MaxConfidence = max(data.MaxConfidence, note.Confidence)

	if note.Time < data.First {
		data.First = note.Time
	}
	if note.Time > data.Latest {
		data.Latest = note.Time
	}

	aggregatedData[birdKey] = data
}

// buildDailySpeciesSummaryResponse converts aggregated data into the final API response slice.
func (c *Handler) buildDailySpeciesSummaryResponse(aggregatedData map[string]aggregatedBirdInfo, selectedDate string) ([]SpeciesDailySummary, error) {
	// Collect species names with detections
	scientificNames := collectSpeciesWithDetections(aggregatedData)

	// Parse selected date for status computation
	statusTime := parseStatusTimeFromDate(selectedDate)

	// Batch fetch species tracking status
	batchSpeciesStatus := c.batchFetchSpeciesStatus(scientificNames, statusTime)

	// Build the final result slice
	result := make([]SpeciesDailySummary, 0, len(scientificNames))
	for _, scientificName := range scientificNames {
		data := aggregatedData[scientificName]
		// Emit the media-proxy URL directly rather than pre-resolving from the
		// cache-only batch path. The proxy (ServeSpeciesImageProxy) resolves via
		// the single-item Get() fallback chain and the local file cache, so
		// dashboard thumbnails honor the configured fallback provider even when
		// the primary provider has a negative cache entry (issue #3806). The
		// frontend swaps to the placeholder on a 404 via handleBirdImageError.
		thumbnailURL := buildThumbnailURL(scientificName)
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

// parseStatusTimeFromDate parses selected date for species status computation
func parseStatusTimeFromDate(selectedDate string) time.Time {
	if selectedDate == "" {
		return time.Now()
	}
	parsedDate, err := time.Parse(time.DateOnly, selectedDate)
	if err != nil {
		return time.Now()
	}
	// Use end of day for the selected date to include all detections from that day
	return time.Date(parsedDate.Year(), parsedDate.Month(), parsedDate.Day(), 23, 59, 59, 0, parsedDate.Location())
}

// batchFetchSpeciesStatus fetches species tracking status to avoid N+1 queries
func (c *Handler) batchFetchSpeciesStatus(scientificNames []string, statusTime time.Time) map[string]species.SpeciesStatus {
	// Snapshot processor and tracker to avoid TOCTOU race
	proc := c.Processor
	if proc == nil || len(scientificNames) == 0 {
		return nil
	}
	tracker := proc.GetNewSpeciesTracker()
	if tracker == nil {
		return nil
	}
	return tracker.GetBatchSpeciesStatus(scientificNames, statusTime)
}

// buildSpeciesSummaryFromData creates a SpeciesDailySummary from aggregated data
func buildSpeciesSummaryFromData(data *aggregatedBirdInfo, thumbnailURL string) SpeciesDailySummary {
	hourlyCountsSlice := make([]int, apicore.HoursPerDay)
	copy(hourlyCountsSlice, data.HourlyCounts[:])

	return SpeciesDailySummary{
		ScientificName: data.ScientificName,
		CommonName:     data.CommonName,
		SpeciesCode:    data.SpeciesCode,
		Count:          data.Count,
		HourlyCounts:   hourlyCountsSlice,
		HighConfidence: data.HighConfidence,
		MaxConfidence:  data.MaxConfidence,
		FirstHeard:     data.First,
		LatestHeard:    data.Latest,
		ThumbnailURL:   thumbnailURL,
	}
}

// applySpeciesStatusToSummary applies tracking metadata to summary.
// Uses the tracker's window-based flags so that "new" indicators persist
// for the configured window period, not just the first-seen calendar day.
func applySpeciesStatusToSummary(summary *SpeciesDailySummary, status *species.SpeciesStatus) {
	summary.IsNewSpecies = status.IsNew

	if status.DaysSinceFirst >= 0 {
		summary.DaysSinceFirstSeen = status.DaysSinceFirst
	}

	// Only a genuine absence gap (>0) is meaningful; -1 (first-ever) and 0
	// (already seen the same day) are omitted.
	if status.DaysSinceLastSeen > 0 {
		summary.DaysSinceLastSeen = status.DaysSinceLastSeen
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
func (c *Handler) GetSpeciesSummary(ctx echo.Context) error {
	startDate := ctx.QueryParam("start_date")
	endDate := ctx.QueryParam("end_date")
	ip, path := ctx.RealIP(), ctx.Request().URL.Path

	c.LogInfoIfEnabled("Retrieving species summary",
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
	c.LogInfoIfEnabled("Database query completed",
		logger.Int64("duration_ms", dbDuration.Milliseconds()),
		logger.Int("record_count", len(summaryData)),
		logger.String("ip", ip),
		logger.String("path", path),
	)

	if err != nil {
		return c.handleAnalyticsQueryError(ctx, err, "Species summary", "Failed to get species summary data",
			logger.String("start_date", startDate),
			logger.String("end_date", endDate),
			logger.String("ip", ip),
			logger.String("path", path),
		)
	}

	// Build response. Emit the media-proxy URL for every species (defer-to-proxy,
	// matching the dashboard fix in #3806): ServeSpeciesImageProxy resolves images via
	// the single-item Get() fallback chain and the local file cache, so species the
	// primary provider lacks but a fallback has are honored at request time and a 404
	// is cached. The frontend swaps to a placeholder on error.
	response := c.convertSummaryDataToResponse(summaryData)

	// Apply limit
	response, limit := c.applyOptionalLimit(ctx, response, ip, path)

	c.LogInfoIfEnabled("Species summary retrieved",
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
func (c *Handler) fetchSpeciesSummaryData(ctx echo.Context, startDate, endDate string) ([]datastore.SpeciesSummaryData, time.Duration, error) {
	dbStart := time.Now()
	queryCtx, cancel := withAnalyticsTimeout(ctx)
	defer cancel()
	summaryData, err := c.DS.GetSpeciesSummaryData(queryCtx, startDate, endDate)
	return summaryData, time.Since(dbStart), err
}

// convertSummaryDataToResponse converts datastore models to API response
func (c *Handler) convertSummaryDataToResponse(summaryData []datastore.SpeciesSummaryData) []SpeciesSummary {
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
			ThumbnailURL:   buildThumbnailURL(data.ScientificName),
		})
	}

	return response
}

// formatTimeIfNotZero formats a timestamp as an ISO 8601 (RFC3339) string with a
// timezone offset, or "" if the time is zero. RFC3339 is the timestamp format used
// across the rest of the v2 API (detections, SSE, weather, notifications), so the
// species summary's first_heard/last_heard match it and carry an unambiguous offset
// for timezone-aware clients. See issue #3793.
func formatTimeIfNotZero(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// applyOptionalLimit parses and applies limit parameter
func (c *Handler) applyOptionalLimit(ctx echo.Context, response []SpeciesSummary, ip, path string) (result []SpeciesSummary, appliedLimit int) {
	limitStr := ctx.QueryParam("limit")
	if limitStr == "" {
		return response, 0
	}

	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		c.LogWarnIfEnabled("Invalid limit parameter",
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
func (c *Handler) GetHourlyAnalytics(ctx echo.Context) error {
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

	c.LogInfoIfEnabled("Retrieving hourly analytics",
		logger.String("date", date),
		logger.String("species", speciesParam),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	// Add timeout to prevent resource exhaustion
	ctxWithTimeout, cancel := withAnalyticsTimeout(ctx)
	defer cancel()

	// Resolve a localized common name (e.g. a Finnish bat name) to its scientific
	// name so analytics matches the detections/search path. A scientific name or an
	// unresolved term passes through unchanged. Only the datastore query uses the
	// resolved value; logs and the response keep the user-facing species string.
	querySpecies := speciesParam
	if resolved, hit := c.resolveSpeciesToScientific(speciesParam); hit {
		querySpecies = resolved
	}

	// Get hourly analytics data from the datastore
	hourlyData, err := c.DS.GetHourlyAnalyticsData(ctxWithTimeout, date, querySpecies)
	if err != nil {
		return c.handleAnalyticsQueryError(ctx, err, "Hourly analytics", "Failed to get hourly analytics data",
			logger.String("date", date),
			logger.String("species", speciesParam),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
	}

	// Create a 24-hour array filled with zeros
	hourlyCountsArray := make([]int, apicore.HoursPerDay)

	// Fill in the actual counts
	for i := range hourlyData {
		data := hourlyData[i]
		if data.Hour >= 0 && data.Hour < apicore.HoursPerDay {
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

	c.LogInfoIfEnabled("Hourly analytics retrieved",
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
func (c *Handler) GetDailyAnalytics(ctx echo.Context) error {
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
		startTime, _ := time.Parse(time.DateOnly, startDate) // Regex ensures this parse succeeds
		endDate = startTime.AddDate(0, 0, defaultAnalyticsDays).Format(time.DateOnly)
	}

	c.LogInfoIfEnabled("Retrieving daily analytics",
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.String("species", speciesParam),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	// Add timeout to prevent resource exhaustion
	ctxWithTimeout, cancel := withAnalyticsTimeout(ctx)
	defer cancel()

	// Resolve a localized common name to its scientific name so analytics matches the
	// detections/search path; a scientific name or unresolved term passes through.
	// Only the datastore query uses the resolved value.
	querySpecies := speciesParam
	if resolved, hit := c.resolveSpeciesToScientific(speciesParam); hit {
		querySpecies = resolved
	}

	// Get daily analytics data from the datastore
	dailyData, err := c.DS.GetDailyAnalyticsData(ctxWithTimeout, startDate, endDate, querySpecies)
	if err != nil {
		return c.handleAnalyticsQueryError(ctx, err, "Daily analytics", "Failed to get daily analytics data",
			logger.String("start_date", startDate),
			logger.String("end_date", endDate),
			logger.String("species", speciesParam),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
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

	c.LogInfoIfEnabled("Daily analytics retrieved",
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

// GetSpeciesDiversity handles GET /api/v2/analytics/species/diversity
// Returns the number of unique species detected per day over a date range
func (c *Handler) GetSpeciesDiversity(ctx echo.Context) error {
	const operation = "species diversity"

	// Validate required parameter
	if err := c.requireQueryParam(ctx, "start_date", operation); err != nil {
		return err
	}

	startDate := ctx.QueryParam("start_date")
	endDate := ctx.QueryParam("end_date")

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
		startTime, _ := time.Parse(time.DateOnly, startDate) // Regex ensures this parse succeeds
		endDate = startTime.AddDate(0, 0, defaultAnalyticsDays).Format(time.DateOnly)
	}

	c.LogInfoIfEnabled("Retrieving species diversity data",
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	// Add timeout to prevent resource exhaustion
	ctxWithTimeout, cancel := withAnalyticsTimeout(ctx)
	defer cancel()

	diversityData, err := c.DS.GetSpeciesDiversityData(ctxWithTimeout, startDate, endDate)
	if err != nil {
		return c.handleAnalyticsQueryError(ctx, err, "Species diversity", "Failed to get species diversity data",
			logger.String("start_date", startDate),
			logger.String("end_date", endDate),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
	}

	// Build the response
	type DiversityResponse struct {
		Date          string `json:"date"`
		UniqueSpecies int    `json:"unique_species"`
	}

	response := struct {
		StartDate    string              `json:"start_date"`
		EndDate      string              `json:"end_date"`
		Data         []DiversityResponse `json:"data"`
		MaxDiversity int                 `json:"max_diversity"`
	}{
		StartDate: startDate,
		EndDate:   endDate,
		Data:      make([]DiversityResponse, 0, len(diversityData)),
	}

	maxDiversity := 0
	for i := range diversityData {
		d := diversityData[i]
		response.Data = append(response.Data, DiversityResponse{
			Date:          d.Date,
			UniqueSpecies: d.Count,
		})
		if d.Count > maxDiversity {
			maxDiversity = d.Count
		}
	}
	response.MaxDiversity = maxDiversity

	c.LogInfoIfEnabled("Species diversity data retrieved",
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.Int("data_points", len(response.Data)),
		logger.Int("max_diversity", maxDiversity),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	return ctx.JSON(http.StatusOK, response)
}

// activityHeatmapCells holds the parallel cell arrays for the heatmap wire payload.
type activityHeatmapCells struct {
	DateIndex []int `json:"dateIndex"`
	Slot      []int `json:"slot"`
	Count     []int `json:"count"`
}

// activityHeatmapResponse is the columnar, sparse heatmap payload (design spec section 6.1).
type activityHeatmapResponse struct {
	Dates                 []string             `json:"dates"`
	SlotResolutionMinutes int                  `json:"slotResolutionMinutes"`
	Cells                 activityHeatmapCells `json:"cells"`
}

// newActivityHeatmapResponse maps the datastore aggregation onto the wire payload, coalescing
// nil slices to empty ones so the client always receives JSON arrays (never null).
func newActivityHeatmapResponse(data *datastore.ActivityHeatmapData) activityHeatmapResponse {
	dates := data.Dates
	if dates == nil {
		dates = []string{}
	}
	dateIndex := data.CellDateIndex
	if dateIndex == nil {
		dateIndex = []int{}
	}
	slot := data.CellSlot
	if slot == nil {
		slot = []int{}
	}
	count := data.CellCount
	if count == nil {
		count = []int{}
	}
	return activityHeatmapResponse{
		Dates:                 dates,
		SlotResolutionMinutes: data.SlotResolutionMinutes,
		Cells:                 activityHeatmapCells{DateIndex: dateIndex, Slot: slot, Count: count},
	}
}

// GetActivityHeatmap handles GET /api/v2/analytics/time/heatmap
// Returns detection counts bucketed by (station-local date, intra-day slot) over the date range
// as a columnar sparse payload. With ?format=csv it streams the non-zero cells as CSV instead.
func (c *Handler) GetActivityHeatmap(ctx echo.Context) error {
	const operation = "activity heatmap"

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

	// Default the end date to a 30-day window when omitted, matching the other range endpoints.
	if endDate == "" {
		startTime, _ := time.Parse(time.DateOnly, startDate) // Regex ensures this parse succeeds
		endDate = startTime.AddDate(0, 0, defaultAnalyticsDays).Format(time.DateOnly)
	}

	c.LogInfoIfEnabled("Retrieving activity heatmap",
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.String("species", speciesParam),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	// Resolve a localized common name to its scientific name so this endpoint matches the
	// detections/search path and the sibling time endpoints; a scientific name or unresolved
	// term passes through. Only the datastore query uses the resolved value.
	querySpecies := speciesParam
	if resolved, hit := c.resolveSpeciesToScientific(speciesParam); hit {
		querySpecies = resolved
	}

	// Add timeout to prevent resource exhaustion
	ctxWithTimeout, cancel := withAnalyticsTimeout(ctx)
	defer cancel()

	data, err := c.DS.GetActivityHeatmap(ctxWithTimeout, startDate, endDate, querySpecies)
	if err != nil {
		return c.handleAnalyticsQueryError(ctx, err, "Activity heatmap", "Failed to get activity heatmap data",
			logger.String("start_date", startDate),
			logger.String("end_date", endDate),
			logger.String("species", speciesParam),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
	}

	if strings.EqualFold(ctx.QueryParam("format"), "csv") {
		return c.writeActivityHeatmapCSV(ctx, &data)
	}

	c.LogInfoIfEnabled("Activity heatmap retrieved",
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.Int("slot_resolution_minutes", data.SlotResolutionMinutes),
		logger.Int("cell_count", len(data.CellCount)),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	return ctx.JSON(http.StatusOK, newActivityHeatmapResponse(&data))
}

// writeActivityHeatmapCSV streams the heatmap's non-zero cells as CSV. Each row is one cell:
// the calendar date, the slot index, the slot's wall-clock start time, and the detection count.
func (c *Handler) writeActivityHeatmapCSV(ctx echo.Context, data *datastore.ActivityHeatmapData) error {
	ctx.Response().Header().Set(echo.HeaderContentType, "text/csv; charset=utf-8")
	ctx.Response().Header().Set(echo.HeaderContentDisposition, `attachment; filename="activity-heatmap.csv"`)
	ctx.Response().WriteHeader(http.StatusOK)

	w := csv.NewWriter(ctx.Response())
	if err := w.Write([]string{"date", "slot", "slot_start", "count"}); err != nil {
		return err
	}

	resolution := data.SlotResolutionMinutes
	// Bound by the shortest parallel slice so a malformed payload can never panic on index access.
	n := min(len(data.CellDateIndex), len(data.CellSlot), len(data.CellCount))
	for i := range n {
		date := ""
		if di := data.CellDateIndex[i]; di >= 0 && di < len(data.Dates) {
			date = data.Dates[di]
		}
		slot := data.CellSlot[i]
		startMinutes := slot * resolution
		row := []string{
			date,
			strconv.Itoa(slot),
			fmt.Sprintf("%02d:%02d", startMinutes/60, startMinutes%60),
			strconv.Itoa(data.CellCount[i]),
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}

	w.Flush()
	return w.Error()
}

// speciesHourlyDistributionItem is one species' row in the ridgeline wire payload: the stable
// scientific-name key, its 24 normalized hour-of-day buckets (index = station-local hour 0..23,
// summing to 1.0), and the raw detection count. The localized common name is resolved client-side
// (the v2 label schema stores no common name), matching the sibling species charts.
type speciesHourlyDistributionItem struct {
	ScientificName string      `json:"scientificName"`
	Buckets        [24]float64 `json:"buckets"`
	Total          int         `json:"total"`
}

// newSpeciesHourlyDistributionResponse maps the datastore aggregation onto the wire payload as a
// JSON array (never null), preserving the descending-volume order.
func newSpeciesHourlyDistributionResponse(data []datastore.SpeciesHourlyDistribution) []speciesHourlyDistributionItem {
	items := make([]speciesHourlyDistributionItem, 0, len(data))
	for i := range data {
		items = append(items, speciesHourlyDistributionItem{
			ScientificName: data[i].ScientificName,
			Buckets:        data[i].Buckets,
			Total:          data[i].Total,
		})
	}
	return items
}

// dawnChorusOnsetItem is one calendar day's row in the dawn-chorus onset wire payload (design spec
// section 6.3). OnsetRelMinutes is the onset minute-of-day minus civil dawn's minute-of-day
// (negative = before civil dawn); it is null when the day had too few detections or civil dawn is
// undefined for the date, which the client renders as a gap (its trend line breaks over nulls
// rather than interpolating across them). DetectionCount is the day's detection count, shown in the
// tooltip.
type dawnChorusOnsetItem struct {
	Date            string `json:"date"`
	OnsetRelMinutes *int   `json:"onsetRelMinutes"`
	DetectionCount  int    `json:"detectionCount"`
}

// newDawnChorusOnsetResponse maps the datastore aggregation onto the wire payload as a JSON array
// (never null), preserving the ascending-date order.
func newDawnChorusOnsetResponse(data []datastore.DailyActivityOnset) []dawnChorusOnsetItem {
	items := make([]dawnChorusOnsetItem, 0, len(data))
	for i := range data {
		items = append(items, dawnChorusOnsetItem{
			Date:            data[i].Date,
			OnsetRelMinutes: data[i].OnsetRelMinutes,
			DetectionCount:  data[i].DetectionCount,
		})
	}
	return items
}

// GetDawnChorusOnset handles GET /api/v2/analytics/time/dawn-onset
// Returns, per calendar day in the range, the dawn-chorus onset relative to civil dawn (in minutes;
// negative = before civil dawn), powering the dawn-chorus onset tracker (design spec section 6.3).
func (c *Handler) GetDawnChorusOnset(ctx echo.Context) error {
	const operation = "dawn chorus onset"

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

	// Default the end date to a 30-day window when omitted, matching the other range endpoints.
	if endDate == "" {
		startTime, _ := time.Parse(time.DateOnly, startDate) // Regex ensures this parse succeeds
		endDate = startTime.AddDate(0, 0, defaultAnalyticsDays).Format(time.DateOnly)
	}

	// Resolve a localized common name to its scientific name so this endpoint matches the
	// detections/search path and the sibling time endpoints; a scientific name or unresolved
	// term passes through. Only the datastore query uses the resolved value.
	querySpecies := speciesParam
	if resolved, hit := c.resolveSpeciesToScientific(speciesParam); hit {
		querySpecies = resolved
	}

	c.LogInfoIfEnabled("Retrieving dawn chorus onset",
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.String("species", speciesParam),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	// Add timeout to prevent resource exhaustion
	ctxWithTimeout, cancel := withAnalyticsTimeout(ctx)
	defer cancel()

	data, err := c.DS.GetDailyActivityOnset(ctxWithTimeout, startDate, endDate, querySpecies)
	if err != nil {
		return c.handleAnalyticsQueryError(ctx, err, "Dawn chorus onset", "Failed to get dawn chorus onset",
			logger.String("start_date", startDate),
			logger.String("end_date", endDate),
			logger.String("species", speciesParam),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
	}

	c.LogInfoIfEnabled("Dawn chorus onset retrieved",
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.Int("day_count", len(data)),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	return ctx.JSON(http.StatusOK, newDawnChorusOnsetResponse(data))
}

// Nocturnal activity clock sun endpoint constants (design spec section 6.4).
const (
	sunNoonHour       = 12 // anchor a date at local noon before a SunCalc lookup (see GetAnalyticsSun)
	sunHoursPerDay    = 24 // for the range-midpoint day count
	sunMinutesPerHour = 60 // minute-of-day conversion
)

// analyticsSunResponse is the sun-times payload for the nocturnal activity clock (design spec
// section 6.4). Each event field is the minute-of-day (0..1439) in the server's local timezone,
// the same frame the hourly-distribution endpoint buckets detections in, so the chart's daytime
// arc aligns with its hourly bars. A nil field means the event does not occur for the date (polar
// day/night) or SunCalc is unavailable. CivilDawn/CivilDusk are nil unless a genuine civil
// twilight occurs (SunCalc substitutes sunrise/sunset when it cannot be computed at high
// latitudes). Available is false when no sun calculator is configured or the sun never rises/sets
// on the date, signalling the client to render the bars without day/night shading rather than
// erroring the card.
type analyticsSunResponse struct {
	Date      string `json:"date"`
	Sunrise   *int   `json:"sunrise"`
	Sunset    *int   `json:"sunset"`
	CivilDawn *int   `json:"civilDawn"`
	CivilDusk *int   `json:"civilDusk"`
	Available bool   `json:"available"`
}

// localMinuteOfDay expresses an absolute instant as its minute-of-day (0..1439) in the server's
// local timezone, matching the frame the hourly-distribution endpoint buckets detections in.
func localMinuteOfDay(t time.Time) int {
	lt := t.In(time.Local)
	return lt.Hour()*sunMinutesPerHour + lt.Minute()
}

// resolveSunRepresentativeDate picks the single date the nocturnal clock's sun times represent.
// A multi-day range collapses to its calendar midpoint (the chart notes this in its tooltip); a
// single `date` (or a lone `start_date`) is used directly; absent all three, today is used. The
// range bounds are parsed in UTC so every day is exactly 24h (DST-free), making the day count
// exact; the midpoint is then a pure calendar offset (AddDate), and only its Y-M-D is used
// downstream. Inputs are pre-validated by the handler.
func resolveSunRepresentativeDate(dateParam, startDate, endDate string) string {
	switch {
	case dateParam != "":
		return dateParam
	case startDate != "" && endDate != "":
		start, errS := time.Parse(time.DateOnly, startDate)
		end, errE := time.Parse(time.DateOnly, endDate)
		if errS != nil || errE != nil {
			return startDate // defensive: handler validated both already
		}
		days := int(end.Sub(start).Hours()) / sunHoursPerDay
		return start.AddDate(0, 0, days/2).Format(time.DateOnly)
	case startDate != "":
		return startDate
	default:
		return time.Now().In(time.Local).Format(time.DateOnly)
	}
}

// GetAnalyticsSun handles GET /api/v2/analytics/sun
// Returns the sunrise/sunset/civil-dawn/civil-dusk times (minute-of-day, server-local) for a
// representative date, powering the nocturnal activity clock's day/night shading (design spec
// section 6.4). Accepts ?date for a single day or ?start_date&end_date for a range (collapsed to
// its midpoint); defaults to today. Sun data is separate from the (unchanged) hourly-distribution
// endpoint so that endpoint's response shape stays backward compatible.
func (c *Handler) GetAnalyticsSun(ctx echo.Context) error {
	const operation = "analytics sun times"

	dateParam := ctx.QueryParam("date")
	startDate := ctx.QueryParam("start_date")
	endDate := ctx.QueryParam("end_date")

	// All date params are optional (default is today); validate any that are present.
	if err := c.validateDateFormatStrictWithResponse(ctx, dateParam, "date", operation); err != nil {
		return err
	}
	if err := c.validateDateFormatStrictWithResponse(ctx, startDate, "start_date", operation); err != nil {
		return err
	}
	if err := c.validateDateFormatStrictWithResponse(ctx, endDate, "end_date", operation); err != nil {
		return err
	}
	if err := c.validateDateRangeWithResponse(ctx, startDate, endDate, operation); err != nil {
		return err
	}
	// The strict format check is a regex, so a well-formed but impossible date (e.g. 2026-02-31)
	// slips through; reject it explicitly rather than letting it surface later as a misleading
	// available:false. (start_date/end_date already get this via validateDateRangeWithResponse.)
	if dateParam != "" {
		if _, err := time.Parse(time.DateOnly, dateParam); err != nil {
			_ = c.HandleError(ctx, err, "Invalid date parameters", http.StatusBadRequest)
			return ErrResponseHandled
		}
	}
	// A lone end_date has no start to pair with and would otherwise be silently ignored (the result
	// would default to today), which is misleading; require start_date alongside it.
	if dateParam == "" && startDate == "" && endDate != "" {
		_ = c.HandleError(ctx, nil, "start_date is required when end_date is provided", http.StatusBadRequest)
		return ErrResponseHandled
	}

	repDate := resolveSunRepresentativeDate(dateParam, startDate, endDate)

	c.LogInfoIfEnabled("Retrieving analytics sun times",
		logger.String("date", repDate),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	resp := analyticsSunResponse{Date: repDate}

	// SunCalc may be unconfigured (e.g. not started yet). Degrade gracefully so the clock still
	// renders its hourly bars without day/night shading rather than erroring the whole card.
	if c.SunCalc == nil {
		return ctx.JSON(http.StatusOK, resp)
	}

	// Anchor the date at local noon before the lookup: SunCalc re-derives the calendar day in its
	// own coordinate-derived zone, so passing midnight could land on the adjacent day. Noon keeps
	// the intended calendar day for any real timezone offset (same approach as the dawn-onset
	// tracker). repDate is validated/derived above, so a parse failure is treated as no sun data.
	repTime, err := time.ParseInLocation(time.DateOnly, repDate, time.Local)
	if err != nil {
		return ctx.JSON(http.StatusOK, resp)
	}
	// Construct local calendar noon directly (not midnight + 12h, which lands at 11:00/13:00 across a
	// DST transition) so the anchor is always mid-day on the intended calendar date.
	anchor := time.Date(repTime.Year(), repTime.Month(), repTime.Day(), sunNoonHour, 0, 0, 0, repTime.Location())

	times, err := c.SunCalc.GetSunEventTimes(anchor)
	if err != nil {
		// Polar day/night: the sun never rises/sets, so there is no daytime arc to shade. Return
		// available:false (not 500) so the client renders the bars without shading.
		c.LogInfoIfEnabled("Sun times unavailable for date (polar day/night)",
			logger.String("date", repDate),
			logger.String("ip", ctx.RealIP()),
		)
		return ctx.JSON(http.StatusOK, resp)
	}

	sunrise := localMinuteOfDay(times.Sunrise)
	sunset := localMinuteOfDay(times.Sunset)
	resp.Sunrise = &sunrise
	resp.Sunset = &sunset
	resp.Available = true

	// Civil dawn/dusk only when a genuine civil twilight occurs. SunCalc substitutes sunrise/sunset
	// (exact equality) when civil twilight cannot be computed at high latitudes (white nights), so a
	// genuine dawn is strictly before sunrise and a genuine dusk strictly after sunset; the equality
	// fallback is omitted. Both read the already-fetched times (no second SunCalc lookup), and the
	// dawn check mirrors suncalc.GetCivilDawn's own genuine-twilight test.
	if times.CivilDawn.Before(times.Sunrise) {
		civilDawn := localMinuteOfDay(times.CivilDawn)
		resp.CivilDawn = &civilDawn
	}
	if times.CivilDusk.After(times.Sunset) {
		civilDusk := localMinuteOfDay(times.CivilDusk)
		resp.CivilDusk = &civilDusk
	}

	c.LogInfoIfEnabled("Analytics sun times retrieved",
		logger.String("date", repDate),
		logger.Bool("available", resp.Available),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	return ctx.JSON(http.StatusOK, resp)
}

// parseSpeciesParams normalizes the repeated ?species query values into a scientific-name filter:
// each value is trimmed, empty entries are dropped (so a stray "?species=" does not turn into a
// filter that matches nothing), and case-insensitive duplicates are collapsed to their first
// occurrence (preserving its original casing) so a repeated selection does not inflate the IN clause.
// Returns nil when no usable value remains, which the datastore reads as "no filter" (top-N by volume).
func parseSpeciesParams(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(raw))
	speciesFilter := make([]string, 0, len(raw))
	for _, s := range raw {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		speciesFilter = append(speciesFilter, trimmed)
	}
	if len(speciesFilter) == 0 {
		return nil
	}
	return speciesFilter
}

// serveTopNHourlyChart is the shared request flow for the top-N-by-volume hour-of-day analytics
// endpoints (who-sings-when ridgeline, acoustic succession): require and strictly validate
// start_date/end_date, default the end to a 30-day window, parse and clamp the limit, read the
// optional repeated species filter, run the query under the analytics timeout (mapping a deadline to
// HTTP 408), and serialize the response as a JSON array. An empty species filter keeps the top-N
// default; a non-empty one narrows the chart to the selection (still volume-ordered, capped at the
// limit). The endpoints differ only in their limit bounds, the datastore query, and the response
// shape, which are passed in; operation names the endpoint in validation/error messages and logs.
func serveTopNHourlyChart[T any](
	c *Handler,
	ctx echo.Context,
	operation string,
	defaultLimit, maxLimit int,
	query func(context.Context, string, string, []string, int) ([]T, error),
	respond func([]T) any,
) error {
	// Validate required parameter
	if err := c.requireQueryParam(ctx, "start_date", operation); err != nil {
		return err
	}

	startDate := ctx.QueryParam("start_date")
	endDate := ctx.QueryParam("end_date")

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

	// Default the end date to a 30-day window when omitted, matching the other range endpoints.
	if endDate == "" {
		startTime, _ := time.Parse(time.DateOnly, startDate) // Regex ensures this parse succeeds
		endDate = startTime.AddDate(0, 0, defaultAnalyticsDays).Format(time.DateOnly)
	}

	limit := apicore.ParsePaginationLimit(ctx.QueryParam("limit"), defaultLimit, maxLimit)

	// Optional repeated species filter (?species=A&species=B): trimmed and empty-filtered. When empty
	// the query keeps its top-N-by-volume default; when non-empty it narrows to the selection (still
	// volume-ordered, capped at limit). Mirrors how the batch time-of-day endpoint reads species.
	speciesFilter := parseSpeciesParams(ctx.QueryParams()["species"])

	c.LogInfoIfEnabled("Retrieving "+operation,
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.Int("limit", limit),
		logger.Int("species_filter_count", len(speciesFilter)),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	// Add timeout to prevent resource exhaustion
	ctxWithTimeout, cancel := withAnalyticsTimeout(ctx)
	defer cancel()

	data, err := query(ctxWithTimeout, startDate, endDate, speciesFilter, limit)
	if err != nil {
		return c.handleAnalyticsQueryError(ctx, err, operation, "Failed to get "+operation,
			logger.String("start_date", startDate),
			logger.String("end_date", endDate),
			logger.Int("limit", limit),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
	}

	// Diagnostic: with an explicit selection the client sends limit == len(selection), so fewer rows
	// than selected means some selected species produced no chart data (no in-range detections, all
	// false positives, or a scientific-name mismatch between the selector and this ranking). Surfaces
	// the "N selected, fewer drawn" ridgeline symptom in logs with the exact names.
	if len(speciesFilter) > 0 && len(data) < len(speciesFilter) {
		c.LogInfoIfEnabled(operation+": some selected species returned no data",
			logger.Int("selected", len(speciesFilter)),
			logger.Int("returned", len(data)),
			logger.String("selected_species", strings.Join(speciesFilter, ", ")),
			logger.String("path", ctx.Request().URL.Path),
		)
	}

	c.LogInfoIfEnabled(operation+" retrieved",
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.Int("species_count", len(data)),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	return ctx.JSON(http.StatusOK, respond(data))
}

// GetSpeciesHourlyDistribution handles GET /api/v2/analytics/time/distribution/species
// Returns the normalized hour-of-day activity distribution powering the who-sings-when ridgeline
// (design spec section 6.2). With no species filter it covers the top N species by detection volume
// over the date range; with a repeated ?species filter it covers just the selected species (still
// volume-ordered, capped at the limit).
func (c *Handler) GetSpeciesHourlyDistribution(ctx echo.Context) error {
	return serveTopNHourlyChart(c, ctx, "species hourly distribution",
		defaultSpeciesRidgelineLimit, maxSpeciesRidgelineLimit,
		c.DS.GetHourlyDistributionBySpecies,
		func(data []datastore.SpeciesHourlyDistribution) any {
			return newSpeciesHourlyDistributionResponse(data)
		},
	)
}

// acousticSuccessionItem is one species' row in the acoustic-succession wire payload: the stable
// scientific-name key, its 24 raw hour-of-day detection counts (index = station-local hour 0..23),
// and the total detection count. The localized common name is resolved client-side (the v2 label
// schema stores no common name), matching the sibling species charts.
type acousticSuccessionItem struct {
	ScientificName string  `json:"scientificName"`
	Counts         [24]int `json:"counts"`
	Total          int     `json:"total"`
}

// newAcousticSuccessionResponse maps the datastore aggregation onto the wire payload as a JSON array
// (never null), preserving the descending-volume order.
func newAcousticSuccessionResponse(data []datastore.SpeciesHourlyCounts) []acousticSuccessionItem {
	items := make([]acousticSuccessionItem, 0, len(data))
	for i := range data {
		items = append(items, acousticSuccessionItem{
			ScientificName: data[i].ScientificName,
			Counts:         data[i].Counts,
			Total:          data[i].Total,
		})
	}
	return items
}

// GetAcousticSuccession handles GET /api/v2/analytics/time/succession
// Returns the per-species raw hour-of-day detection counts powering the acoustic succession
// streamgraph in the Activity Patterns tab (design spec #1155, Tier-2). With no species filter it
// covers the top-N species by volume; with a repeated ?species filter it covers just the selected
// species (still volume-ordered, capped at the limit). The counts are unnormalized so the frontend
// can stack them into a streamgraph whose band width is detection volume.
func (c *Handler) GetAcousticSuccession(ctx echo.Context) error {
	return serveTopNHourlyChart(c, ctx, "acoustic succession",
		defaultSpeciesSuccessionLimit, maxSpeciesSuccessionLimit,
		c.DS.GetAcousticSuccession,
		func(data []datastore.SpeciesHourlyCounts) any { return newAcousticSuccessionResponse(data) },
	)
}

// confidenceDistributionItem is one species' row in the confidence-distribution wire payload: its
// scientific-name key, its normalized confidence bins (each the fraction of the species' detections
// in that bin, summing to ~1.0), and the raw detection count. The localized common name is resolved
// client-side (the v2 label schema stores no common name), matching the sibling species charts.
type confidenceDistributionItem struct {
	ScientificName string    `json:"scientificName"`
	Bins           []float64 `json:"bins"`
	Total          int       `json:"total"`
}

// newConfidenceDistributionResponse maps the datastore aggregation onto the wire payload as a JSON
// array (never null), preserving the descending-volume order. Each species' Bins is emitted as a
// non-nil array so the client can read it without a null guard.
func newConfidenceDistributionResponse(data []datastore.SpeciesConfidenceHistogram) []confidenceDistributionItem {
	items := make([]confidenceDistributionItem, 0, len(data))
	for i := range data {
		bins := data[i].Bins
		if bins == nil {
			bins = []float64{}
		}
		items = append(items, confidenceDistributionItem{
			ScientificName: data[i].ScientificName,
			Bins:           bins,
			Total:          data[i].Total,
		})
	}
	return items
}

// clampConfidenceBins parses the optional bins query param, falling back to the default and clamping
// to [minConfidenceBins, maxConfidenceBins] so a malformed or extreme value can neither break the
// binning nor produce an unreadably fine or coarse histogram.
func clampConfidenceBins(value string) int {
	bins, err := strconv.Atoi(value)
	if err != nil {
		return defaultConfidenceBins
	}
	if bins < minConfidenceBins {
		return minConfidenceBins
	}
	if bins > maxConfidenceBins {
		return maxConfidenceBins
	}
	return bins
}

// GetConfidenceDistribution handles GET /api/v2/analytics/confidence/distribution
// Returns the per-species confidence-score distribution, powering the confidence distribution chart
// in the Review & Accuracy tab (design spec section 6.5). The datastore method is named
// GetConfidenceHistogram (it computes a per-species histogram); this endpoint and the chart present
// it as a distribution, hence the route/handler naming.
func (c *Handler) GetConfidenceDistribution(ctx echo.Context) error {
	const operation = "confidence distribution"

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

	// Default the end date to a 30-day window when omitted, matching the other range endpoints.
	if endDate == "" {
		startTime, _ := time.Parse(time.DateOnly, startDate) // Regex ensures this parse succeeds
		endDate = startTime.AddDate(0, 0, defaultAnalyticsDays).Format(time.DateOnly)
	}

	// Resolve a localized common name to its scientific name so the optional species filter matches
	// the detections/search path and the sibling time endpoints; a scientific name or unresolved term
	// passes through unchanged. Only the datastore query uses the resolved value.
	querySpecies := speciesParam
	if resolved, hit := c.resolveSpeciesToScientific(speciesParam); hit {
		querySpecies = resolved
	}

	bins := clampConfidenceBins(ctx.QueryParam("bins"))
	limit := apicore.ParsePaginationLimit(ctx.QueryParam("limit"), defaultSpeciesRidgelineLimit, maxSpeciesRidgelineLimit)

	c.LogInfoIfEnabled("Retrieving confidence distribution",
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.String("species", speciesParam),
		logger.Int("bins", bins),
		logger.Int("limit", limit),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	// Add timeout to prevent resource exhaustion
	ctxWithTimeout, cancel := withAnalyticsTimeout(ctx)
	defer cancel()

	data, err := c.DS.GetConfidenceHistogram(ctxWithTimeout, startDate, endDate, querySpecies, bins, limit)
	if err != nil {
		return c.handleAnalyticsQueryError(ctx, err, "Confidence distribution", "Failed to get confidence distribution",
			logger.String("start_date", startDate),
			logger.String("end_date", endDate),
			logger.String("species", speciesParam),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
	}

	c.LogInfoIfEnabled("Confidence distribution retrieved",
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.Int("species_count", len(data)),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	return ctx.JSON(http.StatusOK, newConfidenceDistributionResponse(data))
}

// speciesAccumulationItem is one day on the wire payload for the species accumulation curve.
// scientificName is intentionally absent: the curve is an all-species count, not a per-species series.
type speciesAccumulationItem struct {
	Date              string `json:"date"`
	CumulativeSpecies int    `json:"cumulativeSpecies"`
	NewSpecies        int    `json:"newSpecies"`
}

// newSpeciesAccumulationResponse maps the datastore aggregation onto the wire payload as a JSON array
// (never null), one entry per calendar day in ascending date order.
func newSpeciesAccumulationResponse(data []datastore.SpeciesAccumulationPoint) []speciesAccumulationItem {
	items := make([]speciesAccumulationItem, 0, len(data))
	for i := range data {
		items = append(items, speciesAccumulationItem{
			Date:              data[i].Date,
			CumulativeSpecies: data[i].CumulativeSpecies,
			NewSpecies:        data[i].NewSpecies,
		})
	}
	return items
}

// GetSpeciesAccumulation handles GET /api/v2/analytics/species/accumulation
// Returns the species accumulation curve (the biodiversity collector's curve): per calendar day, the
// cumulative count of distinct species first detected within the selected range, powering the
// accumulation chart in the Biodiversity tab. The metric is inherently all-species, so there is no
// species filter; "first seen" is bounded to the queried window, not lifetime.
func (c *Handler) GetSpeciesAccumulation(ctx echo.Context) error {
	const operation = "species accumulation"

	// Validate required parameter
	if err := c.requireQueryParam(ctx, "start_date", operation); err != nil {
		return err
	}

	startDate := ctx.QueryParam("start_date")
	endDate := ctx.QueryParam("end_date")

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

	// Default the end date to a 30-day window when omitted, matching the other range endpoints.
	if endDate == "" {
		startTime, _ := time.Parse(time.DateOnly, startDate) // Regex ensures this parse succeeds
		endDate = startTime.AddDate(0, 0, defaultAnalyticsDays).Format(time.DateOnly)
	}

	c.LogInfoIfEnabled("Retrieving species accumulation",
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	// Add timeout to prevent resource exhaustion
	ctxWithTimeout, cancel := withAnalyticsTimeout(ctx)
	defer cancel()

	data, err := c.DS.GetSpeciesAccumulation(ctxWithTimeout, startDate, endDate)
	if err != nil {
		return c.handleAnalyticsQueryError(ctx, err, "Species accumulation", "Failed to get species accumulation",
			logger.String("start_date", startDate),
			logger.String("end_date", endDate),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
	}

	c.LogInfoIfEnabled("Species accumulation retrieved",
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.Int("days", len(data)),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	return ctx.JSON(http.StatusOK, newSpeciesAccumulationResponse(data))
}

// analyticsSourceItem is one audio source on the analytics source/mic filter wire payload: a stable
// opaque id (string form of the numeric source id), a display label (anonymized for unauthenticated
// clients), and the source's in-range detection count.
type analyticsSourceItem struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// analyticsSourceListResponse is the analytics source/mic filter's wire payload: the audio sources that
// have detections in the range, most active first. Never null.
type analyticsSourceListResponse struct {
	Sources []analyticsSourceItem `json:"sources"`
}

// anonymizeHistoricalSourceName builds a non-identifying label for a historical audio source from its
// type and opaque id, mirroring the vocabulary of getAnonymizedSourceName / getAnonymizedSourceNameFallback
// used by the audio-level stream and /streams/sources: sound cards become "audio-source-N", network
// streams "camera-N", file inputs "file-source-N", and anything else "source-N". The id suffix keeps
// multiple sources of the same type distinguishable without revealing configured names, URIs, or node
// identity.
func anonymizeHistoricalSourceName(sourceType string, id uint) string {
	switch entities.SourceType(sourceType) {
	case entities.SourceTypeALSA, entities.SourceTypePulseAudio:
		return fmt.Sprintf("audio-source-%d", id)
	case entities.SourceTypeRTSP:
		return fmt.Sprintf("camera-%d", id)
	case entities.SourceTypeFile:
		return fmt.Sprintf("file-source-%d", id)
	default:
		// SourceTypeUnknown and any future/unrecognized type get a generic, non-identifying label.
		return fmt.Sprintf("source-%d", id)
	}
}

// analyticsSourceLabel returns the user-facing label for an audio source in the analytics source/mic
// filter. Authenticated clients see the configured display name (falling back to the node name, then a
// generic id-suffixed label). Unauthenticated clients get a type-based anonymized label so the public
// analytics page never leaks a source's configured name, URI, or node identity. The numeric id is
// exposed in both cases (it is opaque and carries no PII), matching the anonymization contract of the
// audio-level stream and /streams/sources.
func analyticsSourceLabel(src *datastore.AudioSourceSummary, authenticated bool) string {
	if authenticated {
		switch {
		case src.DisplayName != "":
			return src.DisplayName
		case src.NodeName != "":
			return src.NodeName
		default:
			return fmt.Sprintf("source-%d", src.ID)
		}
	}
	return anonymizeHistoricalSourceName(src.SourceType, src.ID)
}

// GetAnalyticsSources handles GET /api/v2/analytics/sources
// Returns the audio sources that have at least one (false-positive-excluded) detection in the date
// range, with per-source detection counts, most active first. When start_date/end_date are omitted it
// covers all history. Powers the analytics hub's source/mic filter option list. The metric is v2only
// (the legacy schema does not persist a detection's source); the legacy datastore returns an empty
// list. Source names are anonymized for unauthenticated clients (the analytics page is public); the
// opaque numeric id is safe to expose and is what the source filter round-trips in the URL.
func (c *Handler) GetAnalyticsSources(ctx echo.Context) error {
	const operation = "analytics sources"

	startDate := ctx.QueryParam("start_date")
	endDate := ctx.QueryParam("end_date")

	// Dates are optional (omitted = all history); validate only what is supplied.
	if startDate != "" {
		if err := c.validateDateFormatStrictWithResponse(ctx, startDate, "start_date", operation); err != nil {
			return err
		}
	}
	if endDate != "" {
		if err := c.validateDateFormatStrictWithResponse(ctx, endDate, "end_date", operation); err != nil {
			return err
		}
	}
	if startDate != "" && endDate != "" {
		if err := c.validateDateRangeWithResponse(ctx, startDate, endDate, operation); err != nil {
			return err
		}
	}

	c.LogInfoIfEnabled("Retrieving analytics audio sources",
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	// Add timeout to prevent resource exhaustion
	ctxWithTimeout, cancel := withAnalyticsTimeout(ctx)
	defer cancel()

	sources, err := c.DS.GetAudioSources(ctxWithTimeout, startDate, endDate)
	if err != nil {
		return c.handleAnalyticsQueryError(ctx, err, "Analytics sources", "Failed to get audio sources",
			logger.String("start_date", startDate),
			logger.String("end_date", endDate),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
	}

	authenticated := c.isClientAuthenticated(ctx)
	resp := analyticsSourceListResponse{Sources: make([]analyticsSourceItem, 0, len(sources))}
	for i := range sources {
		resp.Sources = append(resp.Sources, analyticsSourceItem{
			ID:    strconv.FormatUint(uint64(sources[i].ID), 10),
			Name:  analyticsSourceLabel(&sources[i], authenticated),
			Count: sources[i].Count,
		})
	}

	c.LogInfoIfEnabled("Analytics audio sources retrieved",
		logger.Int("count", len(resp.Sources)),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	return ctx.JSON(http.StatusOK, resp)
}

// yearOverYearPointItem is one calendar position on the year-over-year tracker wire payload: the
// current-year date (YYYY-MM-DD, for the x-axis), the year-independent MonthDay alignment key, the two
// cumulative detection counts, and their delta (thisYear - lastYear).
type yearOverYearPointItem struct {
	Date     string `json:"date"`
	MonthDay string `json:"monthDay"`
	ThisYear int    `json:"thisYear"`
	LastYear int    `json:"lastYear"`
	Delta    int    `json:"delta"`
}

// yearOverYearResponse is the year-over-year tracker wire payload: the two compared calendar years and
// one cumulative point per current-year day. currentYear/previousYear are at the root so the client can
// label the legend without parsing a date. points is always a JSON array (never null).
type yearOverYearResponse struct {
	CurrentYear  int                     `json:"currentYear"`
	PreviousYear int                     `json:"previousYear"`
	Points       []yearOverYearPointItem `json:"points"`
}

// newYearOverYearResponse maps the datastore aggregation onto the wire payload, one entry per
// current-year calendar day in ascending date order.
func newYearOverYearResponse(data datastore.YearOverYearResult) yearOverYearResponse {
	points := make([]yearOverYearPointItem, 0, len(data.Points))
	for i := range data.Points {
		points = append(points, yearOverYearPointItem{
			Date:     data.Points[i].Date,
			MonthDay: data.Points[i].MonthDay,
			ThisYear: data.Points[i].ThisYear,
			LastYear: data.Points[i].LastYear,
			Delta:    data.Points[i].Delta,
		})
	}
	return yearOverYearResponse{
		CurrentYear:  data.CurrentYear,
		PreviousYear: data.PreviousYear,
		Points:       points,
	}
}

// GetYearOverYear handles GET /api/v2/analytics/time/year-over-year
// Returns the current year-to-date cumulative detection counts versus the same calendar span one year
// earlier, with a per-day delta, powering the year-over-year tracker in the Trends tab. The single
// optional `date` query param (station-local YYYY-MM-DD, default today) sets the inclusive end of both
// windows; the metric is inherently all-species, so there is no species filter.
func (c *Handler) GetYearOverYear(ctx echo.Context) error {
	const operation = "year over year"

	// date is optional (defaults to today in the station timezone). Validate both the YYYY-MM-DD shape
	// and that it is a real calendar date: a regex-valid but non-existent date (e.g. 2026-13-45) would
	// otherwise reach the datastore and surface as a 500, so reject it here with a 400, matching the
	// sibling range endpoints. An empty value passes both checks (the datastore resolves the default).
	date := ctx.QueryParam("date")
	if err := c.validateDateFormatStrictWithResponse(ctx, date, "date", operation); err != nil {
		return err
	}
	if err := c.validateDateFormatWithResponse(ctx, date, "date", operation); err != nil {
		return err
	}

	c.LogInfoIfEnabled("Retrieving year-over-year tracker",
		logger.String("date", date),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	// Add timeout to prevent resource exhaustion
	ctxWithTimeout, cancel := withAnalyticsTimeout(ctx)
	defer cancel()

	data, err := c.DS.GetYearOverYear(ctxWithTimeout, date)
	if err != nil {
		return c.handleAnalyticsQueryError(ctx, err, "Year-over-year", "Failed to get year-over-year",
			logger.String("date", date),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
	}

	c.LogInfoIfEnabled("Year-over-year retrieved",
		logger.String("date", date),
		logger.Int("current_year", data.CurrentYear),
		logger.Int("days", len(data.Points)),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	return ctx.JSON(http.StatusOK, newYearOverYearResponse(data))
}

// speciesPhenologyItem is one species' residency row in the phenology wire payload: its
// scientific-name key, its first and last station-local detection dates (YYYY-MM-DD), and the
// in-range detection count. The localized common name is resolved client-side (the v2 label schema
// stores no common name), matching the sibling species charts.
type speciesPhenologyItem struct {
	ScientificName string `json:"scientificName"`
	FirstSeen      string `json:"firstSeen"`
	LastSeen       string `json:"lastSeen"`
	Count          int    `json:"count"`
}

// newSpeciesPhenologyResponse maps the datastore aggregation onto the wire payload as a JSON array
// (never null), one entry per species in arrival order.
func newSpeciesPhenologyResponse(data []datastore.SpeciesPhenologyPoint) []speciesPhenologyItem {
	items := make([]speciesPhenologyItem, 0, len(data))
	for i := range data {
		items = append(items, speciesPhenologyItem{
			ScientificName: data[i].ScientificName,
			FirstSeen:      data[i].FirstSeen,
			LastSeen:       data[i].LastSeen,
			Count:          data[i].Count,
		})
	}
	return items
}

// GetSpeciesPhenology handles GET /api/v2/analytics/species/phenology
// Returns the arrival/departure phenology (residency spans) for the top-N species by detection
// volume within the selected range: per species, the first and last detection date plus the in-range
// detection count, powering the residency-bar Gantt in the Biodiversity tab. The metric is inherently
// all-species top-N, so there is no species filter; spans are bounded to the queried window.
func (c *Handler) GetSpeciesPhenology(ctx echo.Context) error {
	const operation = "species phenology"

	// Validate required parameter
	if err := c.requireQueryParam(ctx, "start_date", operation); err != nil {
		return err
	}

	startDate := ctx.QueryParam("start_date")
	endDate := ctx.QueryParam("end_date")

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

	// Default the end date to a 30-day window when omitted, matching the other range endpoints.
	if endDate == "" {
		startTime, _ := time.Parse(time.DateOnly, startDate) // Regex ensures this parse succeeds
		endDate = startTime.AddDate(0, 0, defaultAnalyticsDays).Format(time.DateOnly)
	}

	limit := apicore.ParsePaginationLimit(ctx.QueryParam("limit"), defaultSpeciesPhenologyLimit, maxSpeciesPhenologyLimit)

	c.LogInfoIfEnabled("Retrieving species phenology",
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.Int("limit", limit),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	// Add timeout to prevent resource exhaustion
	ctxWithTimeout, cancel := withAnalyticsTimeout(ctx)
	defer cancel()

	data, err := c.DS.GetSpeciesPhenology(ctxWithTimeout, startDate, endDate, limit)
	if err != nil {
		return c.handleAnalyticsQueryError(ctx, err, "Species phenology", "Failed to get species phenology",
			logger.String("start_date", startDate),
			logger.String("end_date", endDate),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
	}

	c.LogInfoIfEnabled("Species phenology retrieved",
		logger.String("start_date", startDate),
		logger.String("end_date", endDate),
		logger.Int("species_count", len(data)),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	return ctx.JSON(http.StatusOK, newSpeciesPhenologyResponse(data))
}

// GetTimeOfDayDistribution handles GET /api/v2/analytics/time/distribution/hourly
// Returns an aggregated count of detections by hour of day across the given date range
func (c *Handler) GetTimeOfDayDistribution(ctx echo.Context) error {
	const operation = "time of day distribution"

	// Get query parameters with defaults
	startDate := ctx.QueryParam("start_date")
	endDate := ctx.QueryParam("end_date")
	speciesParam := ctx.QueryParam("species")

	if startDate == "" {
		startDate = time.Now().AddDate(0, 0, -30).Format(time.DateOnly)
	}
	if endDate == "" {
		endDate = time.Now().Format(time.DateOnly)
	}

	// Validate date formats and chronological order
	if err := c.validateDateRangeWithResponse(ctx, startDate, endDate, operation); err != nil {
		return err
	}

	// Resolve a localized common name to its scientific name so this endpoint matches
	// the detections/search path; a scientific name or unresolved term passes through.
	// Only the datastore query uses the resolved value.
	querySpecies := speciesParam
	if resolved, hit := c.resolveSpeciesToScientific(speciesParam); hit {
		querySpecies = resolved
	}

	// Get hourly distribution data from the datastore
	queryCtx, cancel := withAnalyticsTimeout(ctx)
	defer cancel()
	hourlyData, err := c.DS.GetHourlyDistribution(queryCtx, startDate, endDate, querySpecies)
	if err != nil {
		return c.handleAnalyticsQueryError(ctx, err, "Time of day distribution", "Failed to get hourly distribution data",
			logger.String("start_date", startDate),
			logger.String("end_date", endDate),
			logger.String("ip", ctx.RealIP()),
		)
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
func (c *Handler) GetNewSpeciesDetections(ctx echo.Context) error {
	const operation = "GetNewSpeciesDetections"
	ip, path := ctx.RealIP(), ctx.Request().URL.Path

	// Get query parameters with defaults (last 30 days)
	startDate, endDate := getDefaultDateRange(ctx.QueryParam("start_date"), ctx.QueryParam("end_date"), -30, 0)

	c.LogInfoIfEnabled("Retrieving new species detections",
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
	queryCtx, cancel := withAnalyticsTimeout(ctx)
	defer cancel()
	newSpeciesData, err := c.DS.GetNewSpeciesDetections(queryCtx, startDate, endDate, limit, offset)
	if err != nil {
		return c.handleAnalyticsQueryError(ctx, err, "New species detections", "Failed to get new species detections",
			logger.String("start_date", startDate),
			logger.String("end_date", endDate),
			logger.Int("limit", limit),
			logger.Int("offset", offset),
			logger.String("ip", ip),
			logger.String("path", path),
		)
	}

	// Build response with thumbnails
	response := c.convertNewSpeciesToResponse(newSpeciesData)

	c.LogInfoIfEnabled("New species detections retrieved",
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
func (c *Handler) validateNewSpeciesDateParams(ctx echo.Context, startDate, endDate, operation string) error {
	if err := c.validateDateFormatWithResponse(ctx, startDate, "start_date", operation); err != nil {
		return err
	}
	if err := c.validateDateFormatWithResponse(ctx, endDate, "end_date", operation); err != nil {
		return err
	}
	return c.validateDateOrderWithResponse(ctx, startDate, endDate, operation)
}

// convertNewSpeciesToResponse converts new species data to API response format.
// Thumbnails defer to the media proxy (see buildThumbnailURL / #3806): the proxy
// resolves via the single-item Get() fallback chain and local file cache, so a
// species the primary provider lacks but a fallback has is honored at request time.
func (c *Handler) convertNewSpeciesToResponse(newSpeciesData []datastore.NewSpeciesData) []NewSpeciesResponse {
	response := make([]NewSpeciesResponse, 0, len(newSpeciesData))
	for _, data := range newSpeciesData {
		response = append(response, NewSpeciesResponse{
			ScientificName: data.ScientificName,
			CommonName:     data.CommonName,
			FirstHeardDate: data.FirstSeenDate,
			ThumbnailURL:   buildThumbnailURL(data.ScientificName),
			CountInPeriod:  data.CountInPeriod,
		})
	}
	return response
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
		start, err = time.Parse(time.DateOnly, startDateStr)
		if err != nil {
			// Return standard error
			return fmt.Errorf("%w: %w", ErrInvalidStartDate, err)
		}
	}

	// Validate end date format if provided
	if endDateStr != "" {
		end, err = time.Parse(time.DateOnly, endDateStr)
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
func (c *Handler) validateDateRangeWithResponse(ctx echo.Context, startDate, endDate, operation string) error {
	if err := parseAndValidateDateRange(startDate, endDate); err != nil {
		// Check if it's a known date validation error
		if errors.Is(err, ErrInvalidStartDate) || errors.Is(err, ErrInvalidEndDate) || errors.Is(err, ErrDateOrder) {
			_ = c.HandleError(ctx, err, "Invalid date parameters", http.StatusBadRequest)
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
func (c *Handler) GetSpeciesThumbnails(ctx echo.Context) error {
	speciesParams := ctx.QueryParams()["species"]
	if len(speciesParams) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "No species provided")
	}

	result := c.buildThumbnailMap(speciesParams)
	return ctx.JSON(http.StatusOK, result)
}

// buildThumbnailMap maps each requested species to its media-proxy thumbnail URL.
// Defer-to-proxy (see buildThumbnailURL / #3806): ServeSpeciesImageProxy resolves via
// the single-item Get() fallback chain and the local file cache and caches a 404, so a
// species the primary provider lacks but a fallback has is honored at request time. The
// frontend swaps to a placeholder on error.
func (c *Handler) buildThumbnailMap(speciesParams []string) map[string]string {
	result := make(map[string]string, len(speciesParams))
	for _, name := range speciesParams {
		result[name] = buildThumbnailURL(name)
	}
	return result
}

// GetBatchHourlySpeciesData handles GET /api/v2/analytics/time/hourly/batch
// Returns hourly detection patterns for multiple species in a single request
func (c *Handler) GetBatchHourlySpeciesData(ctx echo.Context) error {
	const operation = "GetBatchHourlySpeciesData"
	ip, path := ctx.RealIP(), ctx.Request().URL.Path

	// Validate parameters
	speciesParams, date, err := c.validateBatchHourlyParams(ctx, operation)
	if err != nil {
		return err
	}

	minConfidence := c.parseOptionalFloat(ctx, "min_confidence", 0.0, apicore.PercentageMultiplier)
	c.LogInfoIfEnabled("Retrieving batch hourly species data",
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
func (c *Handler) validateBatchHourlyParams(ctx echo.Context, operation string) (speciesParams []string, date string, err error) {
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
func (c *Handler) processHourlyBatchSpecies(ctx echo.Context, speciesParams []string, date, ip, path string) (results map[string][]HourlyDistribution, processingErrors []string) {
	results = make(map[string][]HourlyDistribution)
	processingErrors = make([]string, 0)
	seen := make(map[string]bool)

	// Stop the batch when the client disconnects or the server shuts down. Each
	// per-species query is bounded individually by analyticsQueryTimeout (below) so
	// one slow query cannot hang the batch, without capping the total time a
	// legitimately large batch may take on slower hardware.
	reqCtx := ctx.Request().Context()

	for _, speciesItem := range speciesParams {
		if err := reqCtx.Err(); err != nil {
			c.LogDebugIfEnabled("Batch hourly species data: client canceled request",
				logger.String("species", speciesItem),
				logger.Error(err),
				logger.String("ip", ip),
				logger.String("path", path),
			)
			break
		}
		speciesItem = strings.TrimSpace(speciesItem)
		if speciesItem == "" || seen[speciesItem] {
			continue
		}
		seen[speciesItem] = true

		// Resolve a localized common name for the datastore query only; results stay
		// keyed by the user-facing species string.
		queryItem := speciesItem
		if resolved, hit := c.resolveSpeciesToScientific(speciesItem); hit {
			queryItem = resolved
		}

		queryCtx, cancel := withAnalyticsTimeout(ctx)
		hourlyData, err := c.DS.GetHourlyAnalyticsData(queryCtx, date, queryItem)
		cancel()
		if err != nil {
			if c.logBatchQueryError("Error getting hourly data for species in batch request", err,
				logger.String("species", speciesItem),
				logger.String("date", date),
				logger.String("ip", ip),
				logger.String("path", path),
			) {
				break // client disconnected; stop the batch
			}
			processingErrors = append(processingErrors, fmt.Sprintf("Failed to get hourly data for species %s: %v", speciesItem, err))
			continue
		}

		hourlyDistribution := initEmptyHourlyDistribution()
		fillHourlyDistributionFromAnalytics(hourlyDistribution, hourlyData)
		results[speciesItem] = hourlyDistribution
	}

	return results, processingErrors
}

// handleBatchHourlyResults logs and returns batch hourly results
func (c *Handler) handleBatchHourlyResults(ctx echo.Context, results map[string][]HourlyDistribution, processingErrors []string, requestedCount int, ip, path string) error {
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
func (c *Handler) GetBatchDailySpeciesData(ctx echo.Context) error {
	const operation = "GetBatchDailySpeciesData"
	ip, path := ctx.RealIP(), ctx.Request().URL.Path

	// Validate and parse parameters
	speciesParams, uniqueSpecies, startDate, endDate, err := c.validateBatchDailyParams(ctx, operation)
	if err != nil {
		return err
	}

	c.LogInfoIfEnabled("Retrieving batch daily species data",
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
func (c *Handler) validateBatchDailyParams(ctx echo.Context, operation string) (speciesParams, uniqueSpecies []string, startDate, endDate string, err error) {
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
		startTime, _ := time.Parse(time.DateOnly, startDate)
		endDate = startTime.AddDate(0, 0, defaultAnalyticsDays).Format(time.DateOnly)
	}

	uniqueSpecies = deduplicateSpeciesList(speciesParams)
	if err = c.validateBatchSize(ctx, len(uniqueSpecies), maxSpeciesBatch, operation); err != nil {
		return
	}

	return speciesParams, uniqueSpecies, startDate, endDate, nil
}

// processDailyBatchSpecies processes each species for batch daily data
func (c *Handler) processDailyBatchSpecies(ctx echo.Context, uniqueSpecies []string, startDate, endDate, ip, path string) (results map[string]SpeciesDailyData, processingErrors []string) {
	results = make(map[string]SpeciesDailyData)
	processingErrors = make([]string, 0)

	// Stop the batch when the client disconnects or the server shuts down. Each
	// per-species query is bounded individually by analyticsQueryTimeout (below) so
	// one slow query cannot hang the batch, without capping the total time a
	// legitimately large batch may take on slower hardware.
	reqCtx := ctx.Request().Context()

	for _, speciesItem := range uniqueSpecies {
		if err := reqCtx.Err(); err != nil {
			c.LogDebugIfEnabled("Batch daily species data: client canceled request",
				logger.String("species", speciesItem),
				logger.Error(err),
				logger.String("ip", ip),
				logger.String("path", path),
			)
			break
		}
		// Resolve a localized common name for the datastore query only; the response
		// stays keyed by and labeled with the user-facing species string.
		queryItem := speciesItem
		if resolved, hit := c.resolveSpeciesToScientific(speciesItem); hit {
			queryItem = resolved
		}

		queryCtx, cancel := withAnalyticsTimeout(ctx)
		dailyData, err := c.DS.GetDailyAnalyticsData(queryCtx, startDate, endDate, queryItem)
		cancel()
		if err != nil {
			if c.logBatchQueryError("Error getting daily data for species in batch request", err,
				logger.String("species", speciesItem),
				logger.String("start_date", startDate),
				logger.String("end_date", endDate),
				logger.String("ip", ip),
				logger.String("path", path),
			) {
				break // client disconnected; stop the batch
			}
			processingErrors = append(processingErrors, fmt.Sprintf("Failed to get daily data for species %s: %v", speciesItem, err))
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
func (c *Handler) handleBatchDailyResults(ctx echo.Context, results map[string]SpeciesDailyData, processingErrors []string, requestedCount, uniqueCount int, ip, path string) error {
	if len(processingErrors) > 0 && len(results) > 0 {
		c.LogWarnIfEnabled("Batch daily species data completed with partial failures",
			logger.Int("successful", len(results)),
			logger.Int("failed", len(processingErrors)),
			logger.Any("errors", processingErrors),
			logger.String("ip", ip),
			logger.String("path", path),
		)
	}

	if len(results) == 0 {
		c.LogErrorIfEnabled("All species in batch daily request failed",
			logger.Int("requested_species", requestedCount),
			logger.Int("unique_species", uniqueCount),
			logger.Any("errors", processingErrors),
			logger.String("ip", ip),
			logger.String("path", path),
		)
		return c.HandleError(ctx, fmt.Errorf("failed to process any requested species"), "Failed to process batch daily request", http.StatusInternalServerError)
	}

	c.LogInfoIfEnabled("Batch daily species data retrieved",
		logger.Int("requested_species", requestedCount),
		logger.Int("unique_species", uniqueCount),
		logger.Int("successful_species", len(results)),
		logger.Int("failed_species", len(processingErrors)),
		logger.String("ip", ip),
		logger.String("path", path),
	)
	return ctx.JSON(http.StatusOK, results)
}
