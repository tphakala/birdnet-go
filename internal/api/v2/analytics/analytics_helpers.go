// internal/api/v2/analytics/analytics_helpers.go
//
// Parameter-validation, pagination, hourly-distribution, and batch-response
// helpers used across the analytics domain handlers. They moved here verbatim
// from the facade utils.go when the analytics domain was extracted: the
// analytics package is their sole consumer (the system domain keeps its own copy
// of the date-format validators it needs, and detections does not use these).
package analytics

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// =============================================================================
// Parameter Validation Helpers
// =============================================================================

// requireQueryParam validates that a required query parameter is not empty.
// Returns ErrResponseHandled after sending error response, or nil if valid.
func (c *Handler) requireQueryParam(ctx echo.Context, paramName, operation string) error {
	value := ctx.QueryParam(paramName)
	if value == "" {
		c.LogErrorIfEnabled("Missing required parameter",
			logger.String("parameter", paramName),
			logger.String("operation", operation),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
		_ = c.HandleError(ctx, nil, "Missing required parameter: "+paramName, http.StatusBadRequest)
		return ErrResponseHandled
	}
	return nil
}

// requireQueryArrayParam validates that a required array query parameter is not empty.
// Returns ErrResponseHandled after sending error response, or nil if valid.
func (c *Handler) requireQueryArrayParam(ctx echo.Context, paramName, operation string) error {
	values := ctx.QueryParams()[paramName]
	if len(values) == 0 {
		c.LogErrorIfEnabled("Missing required parameter",
			logger.String("parameter", paramName),
			logger.String("operation", operation),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
		_ = c.HandleError(ctx, nil, "Missing required parameter: "+paramName+" (array)", http.StatusBadRequest)
		return ErrResponseHandled
	}
	return nil
}

// validateBatchSize validates that the number of items does not exceed the maximum.
// Returns ErrResponseHandled after sending error response, or nil if valid.
func (c *Handler) validateBatchSize(ctx echo.Context, count, maxSize int, operation string) error {
	if count > maxSize {
		c.LogErrorIfEnabled("Batch size exceeded limit",
			logger.Int("requested", count),
			logger.Int("max", maxSize),
			logger.String("operation", operation),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
		_ = c.HandleError(ctx, nil, fmt.Sprintf("Too many items requested. Maximum: %d", maxSize), http.StatusBadRequest)
		return ErrResponseHandled
	}
	return nil
}

// validateDateFormatWithResponse validates a date string is in YYYY-MM-DD format.
// Returns ErrResponseHandled after sending error response, or nil if valid.
func (c *Handler) validateDateFormatWithResponse(ctx echo.Context, dateStr, paramName, operation string) error {
	if dateStr == "" {
		return nil // Empty is valid (optional parameter)
	}
	if _, err := time.Parse(time.DateOnly, dateStr); err != nil {
		c.LogErrorIfEnabled("Invalid date format",
			logger.String("parameter", paramName),
			logger.String("value", dateStr),
			logger.String("operation", operation),
			logger.Error(err),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
		_ = c.HandleError(ctx, nil, "Invalid date format. Use YYYY-MM-DD", http.StatusBadRequest)
		return ErrResponseHandled
	}
	return nil
}

// validateDateFormatStrictWithResponse validates a date string using regex for strict format checking.
// Returns ErrResponseHandled after sending error response, or nil if valid.
func (c *Handler) validateDateFormatStrictWithResponse(ctx echo.Context, dateStr, paramName, operation string) error {
	if dateStr == "" {
		return nil // Empty is valid (optional parameter)
	}
	if !dateRegex.MatchString(dateStr) {
		c.LogErrorIfEnabled("Invalid date format",
			logger.String("parameter", paramName),
			logger.String("value", dateStr),
			logger.String("operation", operation),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
		_ = c.HandleError(ctx, nil, "Invalid "+paramName+" format or contains invalid characters. Use YYYY-MM-DD", http.StatusBadRequest)
		return ErrResponseHandled
	}
	return nil
}

// parseOptionalPositiveInt parses an optional integer query parameter.
// Returns the parsed value, defaultVal if empty, or defaultVal with warning if invalid.
func (c *Handler) parseOptionalPositiveInt(ctx echo.Context, paramName string, defaultVal int) int {
	str := ctx.QueryParam(paramName)
	if str == "" {
		return defaultVal
	}
	val, err := strconv.Atoi(str)
	if err != nil || val <= 0 {
		c.LogWarnIfEnabled("Invalid parameter, using default",
			logger.String("parameter", paramName),
			logger.String("value", str),
			logger.Int("default", defaultVal),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
		return defaultVal
	}
	return val
}

// parseOptionalFloat parses an optional float query parameter with optional divisor.
// Returns the parsed value (divided by divisor if > 0), defaultVal if empty, or defaultVal with warning if invalid.
func (c *Handler) parseOptionalFloat(ctx echo.Context, paramName string, defaultVal, divisor float64) float64 {
	str := ctx.QueryParam(paramName)
	if str == "" {
		return defaultVal
	}
	val, err := strconv.ParseFloat(str, 64)
	if err != nil {
		c.LogWarnIfEnabled("Invalid parameter, using default",
			logger.String("parameter", paramName),
			logger.String("value", str),
			logger.Float64("default", defaultVal),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
		return defaultVal
	}
	if divisor > 0 {
		return val / divisor
	}
	return val
}

// =============================================================================
// Data Processing Helpers
// =============================================================================

// deduplicateSpeciesList removes duplicates and empty entries from a species list.
// Returns a slice of unique, trimmed species names.
func deduplicateSpeciesList(speciesParams []string) []string {
	uniqueSpecies := make([]string, 0, len(speciesParams))
	seen := make(map[string]bool)

	for _, speciesItem := range speciesParams {
		trimmedSpecies := strings.TrimSpace(speciesItem)
		if trimmedSpecies == "" || seen[trimmedSpecies] {
			continue
		}
		seen[trimmedSpecies] = true
		uniqueSpecies = append(uniqueSpecies, trimmedSpecies)
	}
	return uniqueSpecies
}

// initEmptyHourlyDistribution creates an array of 24 HourlyDistribution entries initialized to zero.
func initEmptyHourlyDistribution() []HourlyDistribution {
	data := make([]HourlyDistribution, apicore.HoursPerDay)
	for hour := range apicore.HoursPerDay {
		data[hour] = HourlyDistribution{Hour: hour, Count: 0}
	}
	return data
}

// hourlyDataItem is an interface for types that have Hour and Count fields.
type hourlyDataItem interface {
	GetHour() int
	GetCount() int
}

// hourlyDistributionAdapter adapts HourlyDistributionData to hourlyDataItem.
type hourlyDistributionAdapter struct {
	d datastore.HourlyDistributionData
}

func (a hourlyDistributionAdapter) GetHour() int  { return a.d.Hour }
func (a hourlyDistributionAdapter) GetCount() int { return a.d.Count }

// hourlyAnalyticsAdapter adapts HourlyAnalyticsData to hourlyDataItem.
type hourlyAnalyticsAdapter struct{ d datastore.HourlyAnalyticsData }

func (a hourlyAnalyticsAdapter) GetHour() int  { return a.d.Hour }
func (a hourlyAnalyticsAdapter) GetCount() int { return a.d.Count }

// fillHourlyFromItems fills in actual counts from hourly data items and returns the total count.
func fillHourlyFromItems(dest []HourlyDistribution, items []hourlyDataItem) int {
	totalCount := 0
	for _, item := range items {
		hour := item.GetHour()
		if hour >= 0 && hour < apicore.HoursPerDay {
			dest[hour].Count = item.GetCount()
			totalCount += item.GetCount()
		}
	}
	return totalCount
}

// fillHourlyDistribution fills in actual counts from datastore data and returns the total count.
func fillHourlyDistribution(dest []HourlyDistribution, source []datastore.HourlyDistributionData) int {
	items := make([]hourlyDataItem, len(source))
	for i, d := range source {
		items[i] = hourlyDistributionAdapter{d}
	}
	return fillHourlyFromItems(dest, items)
}

// fillHourlyDistributionFromAnalytics fills in actual counts from analytics data and returns the total count.
func fillHourlyDistributionFromAnalytics(dest []HourlyDistribution, source []datastore.HourlyAnalyticsData) int {
	items := make([]hourlyDataItem, len(source))
	for i, d := range source {
		items[i] = hourlyAnalyticsAdapter{d}
	}
	return fillHourlyFromItems(dest, items)
}

// =============================================================================
// Internal Validation Helpers (return errors, not HTTP responses)
// =============================================================================

// validateDateOrderWithResponse validates that start date is not after end date.
// Returns ErrResponseHandled after sending error response, or nil if valid.
func (c *Handler) validateDateOrderWithResponse(ctx echo.Context, startDate, endDate, operation string) error {
	if startDate == "" || endDate == "" {
		return nil // Empty dates are valid (handled elsewhere)
	}
	start, _ := time.Parse(time.DateOnly, startDate)
	end, _ := time.Parse(time.DateOnly, endDate)
	if start.After(end) {
		c.LogErrorIfEnabled("Invalid date range",
			logger.String("start_date", startDate),
			logger.String("end_date", endDate),
			logger.String("error", "start_date cannot be after end_date"),
			logger.String("operation", operation),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
		_ = c.HandleError(ctx, nil, "`start_date` cannot be after `end_date`", http.StatusBadRequest)
		return ErrResponseHandled
	}
	return nil
}

// =============================================================================
// Pagination Helpers
// =============================================================================

// parseNonNegativeInt parses a query parameter as a non-negative integer.
// Returns (value, error). Returns defaultVal if the parameter is empty.
// Returns -1 if parsing fails or value is negative.
func parseNonNegativeInt(str string, defaultVal int) (int, error) {
	if str == "" {
		return defaultVal, nil
	}
	val, err := strconv.Atoi(str)
	if err != nil || val < 0 {
		return -1, fmt.Errorf("must be a non-negative integer")
	}
	return val, nil
}

// parsePaginationParams parses limit and offset query parameters with validation.
// Returns (limit, offset, error). Uses defaults if params are empty.
// Returns ErrResponseHandled if validation fails (error response already sent).
func (c *Handler) parsePaginationParams(ctx echo.Context, defaultLimit, maxLimit int) (limit, offset int, err error) {
	// Parse limit
	limit, err = parseNonNegativeInt(ctx.QueryParam("limit"), defaultLimit)
	if err != nil {
		_ = c.HandleError(ctx, nil, "Invalid limit parameter. Must be a non-negative integer.", http.StatusBadRequest)
		return 0, 0, ErrResponseHandled
	}
	if limit > 0 && maxLimit > 0 && limit > maxLimit {
		limit = maxLimit
	}

	// Parse offset
	offset, err = parseNonNegativeInt(ctx.QueryParam("offset"), 0)
	if err != nil {
		_ = c.HandleError(ctx, nil, "Invalid offset parameter. Must be a non-negative integer.", http.StatusBadRequest)
		return 0, 0, ErrResponseHandled
	}

	return limit, offset, nil
}

// getDefaultDateRange returns start and end dates with defaults if empty.
// startDefault and endDefault are days relative to today (negative for past).
func getDefaultDateRange(startDate, endDate string, startDefault, endDefault int) (start, end string) {
	start = startDate
	end = endDate
	if start == "" {
		start = time.Now().AddDate(0, 0, startDefault).Format(time.DateOnly)
	}
	if end == "" {
		end = time.Now().AddDate(0, 0, endDefault).Format(time.DateOnly)
	}
	return start, end
}

// =============================================================================
// Batch Parameter Helpers
// =============================================================================

// parseCommaSeparatedDates parses a comma-separated list of dates from a query parameter.
// Returns a slice of validated dates or an error if validation fails.
// Validates each date is in YYYY-MM-DD format.
func (c *Handler) parseCommaSeparatedDates(ctx echo.Context, paramName string) ([]string, error) {
	datesParam := ctx.QueryParam(paramName)
	if datesParam == "" {
		return nil, nil // Empty is valid for optional parameters
	}

	dates := make([]string, 0)
	for dateStr := range strings.SplitSeq(datesParam, ",") {
		trimmed := strings.TrimSpace(dateStr)
		if trimmed == "" {
			continue
		}
		// Validate date format
		if _, err := time.Parse(time.DateOnly, trimmed); err != nil {
			return nil, fmt.Errorf("invalid date format: %s. Use YYYY-MM-DD", trimmed)
		}
		dates = append(dates, trimmed)
	}
	return dates, nil
}

// parseRequiredCommaSeparatedDates parses a required comma-separated list of dates.
// Returns ErrResponseHandled after sending error response if validation fails.
func (c *Handler) parseRequiredCommaSeparatedDates(ctx echo.Context, paramName, operation string, maxDates int) ([]string, error) {
	// Check parameter exists
	if err := c.requireQueryParam(ctx, paramName, operation); err != nil {
		return nil, err
	}

	// Parse dates
	dates, err := c.parseCommaSeparatedDates(ctx, paramName)
	if err != nil {
		_ = c.HandleError(ctx, nil, "Invalid date format", http.StatusBadRequest)
		return nil, ErrResponseHandled
	}

	if len(dates) == 0 {
		_ = c.HandleError(ctx, nil, "No valid dates provided", http.StatusBadRequest)
		return nil, ErrResponseHandled
	}

	// Validate batch size
	if maxDates > 0 {
		if err := c.validateBatchSize(ctx, len(dates), maxDates, operation); err != nil {
			return nil, err
		}
	}

	return dates, nil
}

// =============================================================================
// Batch Response Helpers
// =============================================================================

// handleBatchResponse handles the common pattern for batch API responses with partial failure support.
// It logs warnings for partial failures, returns an error if all items failed, and returns JSON on success.
func (c *Handler) handleBatchResponse(ctx echo.Context, result any, successCount, requestedCount int, processingErrors []string, operationName, ip, urlPath string) error {
	// Log partial failures if any
	if len(processingErrors) > 0 && successCount > 0 {
		c.LogWarnIfEnabled(operationName+" completed with partial failures",
			logger.Int("successful", successCount),
			logger.Int("failed", len(processingErrors)),
			logger.Any("errors", processingErrors),
			logger.String("ip", ip),
			logger.String("path", urlPath))
	}

	// Return error if all items failed
	if successCount == 0 {
		c.LogErrorIfEnabled("All items in "+operationName+" failed",
			logger.Int("requested", requestedCount),
			logger.Any("errors", processingErrors),
			logger.String("ip", ip),
			logger.String("path", urlPath))
		return c.HandleError(ctx, fmt.Errorf("failed to process any requested items"),
			"Failed to process "+operationName, http.StatusInternalServerError)
	}

	// Log successful completion
	c.LogInfoIfEnabled(operationName+" completed",
		logger.Int("requested", requestedCount),
		logger.Int("successful", successCount),
		logger.Int("failed", len(processingErrors)),
		logger.String("ip", ip),
		logger.String("path", urlPath))

	return ctx.JSON(http.StatusOK, result)
}
