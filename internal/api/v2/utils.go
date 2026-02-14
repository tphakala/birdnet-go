package api

import (
	"fmt"
	"math"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// NormalizeClipPathStrict normalizes the audio clip path by removing the clips prefix if present.
// This is the strict variant that returns a boolean to distinguish between invalid paths and
// intentionally empty normalized paths.
//
// Returns:
//   - (normalizedPath, true) for valid results
//   - ("", false) for invalid/unsafe inputs (absolute paths, traversal, etc.)
//
// Examples:
//   - "clips/2024/01/bird.wav" → ("2024/01/bird.wav", true)
//   - "2024/01/bird.wav" → ("2024/01/bird.wav", true) (unchanged)
//   - "clips/" → ("", true) (intentionally empty)
//   - "../etc/passwd" → ("", false) (invalid traversal)
//   - "/absolute/path" → ("", false) (invalid absolute path)
func NormalizeClipPathStrict(p, clipsPrefix string) (string, bool) {
	clipsPrefix = normalizeClipsPrefix(clipsPrefix)
	p = strings.ReplaceAll(p, "\\", "/")
	p = stripClipsPrefix(p, clipsPrefix)
	p = path.Clean(p)

	// Handle the special case where Clean returns "."
	if p == "." {
		return "", true
	}

	// Reject paths that would escape the SecureFS root
	if isUnsafePath(p) {
		return "", false
	}

	return p, true
}

// normalizeClipsPrefix ensures the clips prefix is properly formatted
func normalizeClipsPrefix(prefix string) string {
	if prefix == "" {
		return "clips/"
	}
	if !strings.HasSuffix(prefix, "/") {
		return prefix + "/"
	}
	return prefix
}

// stripClipsPrefix attempts to remove the clips prefix using multiple strategies
func stripClipsPrefix(p, clipsPrefix string) string {
	// Strategy 1: Try the configured prefix as-is
	if trimmed, ok := strings.CutPrefix(p, clipsPrefix); ok {
		return trimmed
	}

	// Strategy 2: Try the basename of the configured path + "/"
	baseName := path.Base(strings.TrimSuffix(clipsPrefix, "/"))
	if baseName != "" && baseName != "." && baseName != "/" {
		basePrefix := baseName + "/"
		if trimmed, ok := strings.CutPrefix(p, basePrefix); ok {
			return trimmed
		}
	}

	// Strategy 3: Try literal "clips/" as fallback (case-insensitive)
	if strings.HasPrefix(strings.ToLower(p), "clips/") {
		return p[6:]
	}

	return p
}

// dangerousEncodedPatterns contains URL-encoded patterns that indicate path traversal attempts.
// These patterns are checked against the lowercased path.
var dangerousEncodedPatterns = []string{
	// Encoded null bytes
	"%00", "%2500",
	// URL-encoded path traversal: %2e = '.'
	"%2e%2e", "%2e.", ".%2e",
	// Double-encoded: %25 = '%', so %252e = '%2e' after one decode
	"%252e",
	// Triple-encoded (defense in depth)
	"%25252e",
	// Encoded slashes that could create traversal after decoding
	"%2f", "%252f",
	// Encoded backslashes for Windows-style traversal
	"%5c", "%255c",
}

// containsDangerousEncodedPattern checks if a lowercased path contains any dangerous URL-encoded patterns.
func containsDangerousEncodedPattern(lower string) bool {
	for _, pattern := range dangerousEncodedPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// minWindowsDrivePathLen is the minimum length for a Windows drive path (e.g., "C:").
const minWindowsDrivePathLen = 2

// isWindowsAbsolutePath checks for Windows-style absolute paths (e.g., C:, D:).
func isWindowsAbsolutePath(p string) bool {
	if len(p) < minWindowsDrivePathLen {
		return false
	}
	firstChar := p[0]
	return ((firstChar >= 'A' && firstChar <= 'Z') || (firstChar >= 'a' && firstChar <= 'z')) && p[1] == ':'
}

// isUnsafePath checks if a path would escape the SecureFS root.
// Uses explicit ".." check for URL/HTTP paths (which are not cleaned by the OS),
// plus filepath.IsLocal for platform-specific validation.
// Also checks for URL-encoded attacks and null bytes.
func isUnsafePath(p string) bool {
	// Check for null bytes - do this first as they can bypass other checks
	if strings.Contains(p, "\x00") {
		return true
	}

	// Check for Windows-style absolute paths on any platform
	if isWindowsAbsolutePath(p) {
		return true
	}

	// Check for dangerous URL-encoded patterns
	if containsDangerousEncodedPattern(strings.ToLower(p)) {
		return true
	}

	// Explicit ".." check needed for URL paths since filepath.IsLocal cleans paths internally.
	// For HTTP/URL contexts, "path/../etc" is dangerous even though it cleans to "etc".
	// This check must come BEFORE filepath.IsLocal to catch patterns like "..../file" which
	// contain ".." as a substring but aren't traversal after cleaning.
	//nolint:gocritic // URL path context requires explicit ".." check; filepath.IsLocal would clean "path/../etc" to "etc" (valid)
	if strings.Contains(p, "..") {
		return true
	}

	// filepath.IsLocal provides comprehensive platform-specific validation for:
	// - Absolute paths, empty paths
	// - Windows reserved names (NUL, COM1, LPT1, etc.)
	// - Windows \??\ prefix attacks (CVE-2023-45283)
	// - Space-padded reserved names (CVE-2023-45284)
	// Note: The ".." check is intentionally above, not relying on IsLocal's ".." detection
	return !filepath.IsLocal(p)
}

// NormalizeClipPath normalizes audio clip paths for use with SecureFS.
// The database stores paths with a configurable prefix (default "clips/"),
// but SecureFS is already rooted at the clips directory, so we need to strip this prefix.
//
// This is the backward-compatible version that returns only the string result.
// Returns empty string for both invalid paths and intentionally empty normalized paths.
//
// Examples:
//   - "clips/2024/01/bird.wav" → "2024/01/bird.wav"
//   - "2024/01/bird.wav" → "2024/01/bird.wav" (unchanged)
//   - "clips/" → "" (empty string)
//   - "../etc/passwd" → "" (invalid path)
func NormalizeClipPath(p, clipsPrefix string) string {
	normalized, _ := NormalizeClipPathStrict(p, clipsPrefix)
	return normalized
}

// =============================================================================
// Parameter Validation Helpers
// =============================================================================

// requireQueryParam validates that a required query parameter is not empty.
// Returns ErrResponseHandled after sending error response, or nil if valid.
func (c *Controller) requireQueryParam(ctx echo.Context, paramName, operation string) error {
	value := ctx.QueryParam(paramName)
	if value == "" {
		c.logErrorIfEnabled("Missing required parameter",
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
func (c *Controller) requireQueryArrayParam(ctx echo.Context, paramName, operation string) error {
	values := ctx.QueryParams()[paramName]
	if len(values) == 0 {
		c.logErrorIfEnabled("Missing required parameter",
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
func (c *Controller) validateBatchSize(ctx echo.Context, count, maxSize int, operation string) error {
	if count > maxSize {
		c.logErrorIfEnabled("Batch size exceeded limit",
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
func (c *Controller) validateDateFormatWithResponse(ctx echo.Context, dateStr, paramName, operation string) error {
	if dateStr == "" {
		return nil // Empty is valid (optional parameter)
	}
	if _, err := time.Parse(time.DateOnly, dateStr); err != nil {
		c.logErrorIfEnabled("Invalid date format",
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
func (c *Controller) validateDateFormatStrictWithResponse(ctx echo.Context, dateStr, paramName, operation string) error {
	if dateStr == "" {
		return nil // Empty is valid (optional parameter)
	}
	if !dateRegex.MatchString(dateStr) {
		c.logErrorIfEnabled("Invalid date format",
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
func (c *Controller) parseOptionalPositiveInt(ctx echo.Context, paramName string, defaultVal int) int {
	str := ctx.QueryParam(paramName)
	if str == "" {
		return defaultVal
	}
	val, err := strconv.Atoi(str)
	if err != nil || val <= 0 {
		c.logWarnIfEnabled("Invalid parameter, using default",
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
func (c *Controller) parseOptionalFloat(ctx echo.Context, paramName string, defaultVal, divisor float64) float64 {
	str := ctx.QueryParam(paramName)
	if str == "" {
		return defaultVal
	}
	val, err := strconv.ParseFloat(str, 64)
	if err != nil {
		c.logWarnIfEnabled("Invalid parameter, using default",
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
	data := make([]HourlyDistribution, HoursPerDay)
	for hour := range HoursPerDay {
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
		if hour >= 0 && hour < HoursPerDay {
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

// validateDateFormat validates a date string is in YYYY-MM-DD format.
// Returns a descriptive error if invalid, nil if valid or empty.
func validateDateFormat(dateStr, paramName string) error {
	if dateStr == "" {
		return nil
	}
	if _, err := time.Parse(time.DateOnly, dateStr); err != nil {
		return fmt.Errorf("invalid %s format '%s', use YYYY-MM-DD", paramName, dateStr)
	}
	return nil
}

// validateDateOrder validates that start date is not after end date.
// Returns a descriptive error if invalid, nil if valid or if either date is empty.
func validateDateOrder(startDate, endDate string) error {
	if startDate == "" || endDate == "" {
		return nil
	}
	start, _ := time.Parse(time.DateOnly, startDate)
	end, _ := time.Parse(time.DateOnly, endDate)
	if start.After(end) {
		return fmt.Errorf("start date (%s) must be earlier than or equal to end date (%s)", startDate, endDate)
	}
	return nil
}

// validateDateOrderWithResponse validates that start date is not after end date.
// Returns ErrResponseHandled after sending error response, or nil if valid.
func (c *Controller) validateDateOrderWithResponse(ctx echo.Context, startDate, endDate, operation string) error {
	if startDate == "" || endDate == "" {
		return nil // Empty dates are valid (handled elsewhere)
	}
	start, _ := time.Parse(time.DateOnly, startDate)
	end, _ := time.Parse(time.DateOnly, endDate)
	if start.After(end) {
		c.logErrorIfEnabled("Invalid date range",
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
func (c *Controller) parsePaginationParams(ctx echo.Context, defaultLimit, maxLimit int) (limit, offset int, err error) {
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
func (c *Controller) parseCommaSeparatedDates(ctx echo.Context, paramName string) ([]string, error) {
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
func (c *Controller) parseRequiredCommaSeparatedDates(ctx echo.Context, paramName, operation string, maxDates int) ([]string, error) {
	// Check parameter exists
	if err := c.requireQueryParam(ctx, paramName, operation); err != nil {
		return nil, err
	}

	// Parse dates
	dates, err := c.parseCommaSeparatedDates(ctx, paramName)
	if err != nil {
		_ = c.HandleError(ctx, nil, err.Error(), http.StatusBadRequest)
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
// Filter Parsing Helpers
// =============================================================================

// ConfidenceFilterResult holds the parsed confidence filter parameters.
type ConfidenceFilterResult struct {
	Operator string
	Value    float64
}

// parseConfidenceFilter parses a confidence filter parameter with operator support.
// Supports operators: >=, <=, >, <, = (default)
// Returns nil if the parameter is empty.
func parseConfidenceFilter(param string) *ConfidenceFilterResult {
	if param == "" {
		return nil
	}

	var operator string
	var value string

	switch {
	case strings.HasPrefix(param, ">="):
		operator = ">="
		value = param[2:]
	case strings.HasPrefix(param, "<="):
		operator = "<="
		value = param[2:]
	case strings.HasPrefix(param, ">"):
		operator = ">"
		value = param[1:]
	case strings.HasPrefix(param, "<"):
		operator = "<"
		value = param[1:]
	default:
		operator = "="
		value = param
	}

	confValue, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil
	}

	// Validate confidence is within 0-100 range and not NaN
	if math.IsNaN(confValue) || confValue < 0 || confValue > 100 {
		return nil
	}

	return &ConfidenceFilterResult{
		Operator: operator,
		Value:    confValue / PercentageMultiplier,
	}
}

// HourFilterResult holds the parsed hour filter parameters.
type HourFilterResult struct {
	Start int
	End   int
}

// parseHourFilter parses an hour filter parameter.
// Supports single hour ("6") or range format ("6-9").
// Returns nil if the parameter is empty, invalid, out of range (0-23), or has inverted range.
func parseHourFilter(param string) *HourFilterResult {
	if param == "" {
		return nil
	}

	if strings.Contains(param, "-") {
		// Range format: "6-9"
		parts := strings.Split(param, "-")
		if len(parts) != minHourRangeParts {
			return nil
		}
		start, err1 := strconv.Atoi(parts[0])
		end, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			return nil
		}
		// Validate hour range (0-23) and ensure start <= end
		if start < 0 || start > 23 || end < 0 || end > 23 || start > end {
			return nil
		}
		return &HourFilterResult{Start: start, End: end}
	}

	// Single hour
	hourVal, err := strconv.Atoi(param)
	if err != nil {
		return nil
	}
	// Validate hour is within 0-23
	if hourVal < 0 || hourVal > 23 {
		return nil
	}
	return &HourFilterResult{Start: hourVal, End: hourVal}
}

// DateRangeResult holds the parsed date range.
type DateRangeResult struct {
	Start time.Time
	End   time.Time
}

// parseDateRangeFilter parses date range from single date or start/end date parameters.
// If singleDate is provided, it's used as both start and end.
// Returns nil if no valid dates are provided.
func parseDateRangeFilter(singleDate, startDate, endDate string) *DateRangeResult {
	if singleDate != "" {
		// Try date shortcuts first (today, yesterday, etc.)
		if date, err := datastore.ParseDateShortcut(singleDate); err == nil {
			return &DateRangeResult{
				Start: date,
				End:   date.AddDate(0, 0, 1).Add(-time.Second),
			}
		}
	}

	if startDate != "" && endDate != "" {
		start, err1 := time.Parse(time.DateOnly, startDate)
		end, err2 := time.Parse(time.DateOnly, endDate)
		if err1 == nil && err2 == nil {
			// Reject inverted date ranges where start is after end
			if start.After(end) {
				return nil
			}
			return &DateRangeResult{
				Start: start,
				End:   end.AddDate(0, 0, 1).Add(-time.Second),
			}
		}
	}

	return nil
}

// =============================================================================
// SSE Metrics Helpers
// =============================================================================

// recordSSEError records an SSE error metric if metrics are available
func (c *Controller) recordSSEError(endpoint, errorType string) {
	if c.metrics != nil && c.metrics.HTTP != nil {
		c.metrics.HTTP.RecordSSEError(endpoint, errorType)
	}
}

// recordSSEMessage records an SSE message sent metric if metrics are available
func (c *Controller) recordSSEMessage(endpoint, messageType string) {
	if c.metrics != nil && c.metrics.HTTP != nil {
		c.metrics.HTTP.RecordSSEMessageSent(endpoint, messageType)
	}
}

// recordSSEConnectionStart records an SSE connection start if metrics are available
func (c *Controller) recordSSEConnectionStart(endpoint string) {
	if c.metrics != nil && c.metrics.HTTP != nil {
		c.metrics.HTTP.SSEConnectionStarted(endpoint)
	}
}

// recordSSEConnectionClose records an SSE connection close if metrics are available
func (c *Controller) recordSSEConnectionClose(endpoint string, duration float64, reason string) {
	if c.metrics != nil && c.metrics.HTTP != nil {
		c.metrics.HTTP.SSEConnectionClosed(endpoint, duration, reason)
	}
}

// =============================================================================
// Settings Validation Helpers
// =============================================================================

// validateFloatInRange validates a float64 field is within a range
func validateFloatInRange(m map[string]any, key string, minVal, maxVal float64, label string) error {
	val, exists := m[key]
	if !exists {
		return nil
	}
	floatVal, ok := val.(float64)
	if !ok {
		return nil // Type mismatch handled elsewhere
	}
	if floatVal < minVal || floatVal > maxVal {
		return fmt.Errorf("%s must be between %.0f and %.0f", label, minVal, maxVal)
	}
	return nil
}

// validateNonEmptyString validates a string field is non-empty with max length
func validateNonEmptyString(m map[string]any, key string, maxLen int, label string) error {
	val, exists := m[key]
	if !exists {
		return nil
	}
	str, ok := val.(string)
	if !ok {
		return fmt.Errorf("%s must be a string", label)
	}
	if str == "" {
		return fmt.Errorf("%s cannot be empty", label)
	}
	if maxLen > 0 && len(str) > maxLen {
		return fmt.Errorf("%s must not exceed %d characters", label, maxLen)
	}
	return nil
}

// validateBoolField validates a boolean field
func validateBoolField(m map[string]any, key, label string) error {
	val, exists := m[key]
	if !exists {
		return nil
	}
	if _, ok := val.(bool); !ok {
		return fmt.Errorf("%s must be a boolean value", label)
	}
	return nil
}

// validatePortField validates a port field (string or int, 1-65535)
func validatePortField(m map[string]any, key string) error {
	val, exists := m[key]
	if !exists {
		return nil
	}

	var port int
	switch v := val.(type) {
	case string:
		var err error
		port, err = strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("port must be a valid number")
		}
	case int:
		port = v
	case float64:
		port = int(v)
	default:
		return fmt.Errorf("port must be a number")
	}

	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}

// validateRequiredStringWhenEnabled validates required string fields when a provider is enabled
func validateRequiredStringWhenEnabled(providerMap map[string]any, fieldName, providerName string) error {
	val, exists := providerMap[fieldName]
	if !exists {
		return fmt.Errorf("%s.%s is required when enabled", providerName, fieldName)
	}
	str, ok := val.(string)
	if !ok || str == "" {
		return fmt.Errorf("%s.%s is required when enabled", providerName, fieldName)
	}
	return nil
}

// =============================================================================
// Logging Helpers
// =============================================================================

// GetLogger returns a logger instance for the API v2 package.
// This provides consistent logging with module identification.
func GetLogger() logger.Logger {
	return logger.Global().Module("api")
}

// log returns a logger for the Controller methods.
// This is a convenience helper for controller-level logging.
func (c *Controller) log() logger.Logger {
	return GetLogger()
}

// logInfoIfEnabled logs info message if apiLogger is enabled
func (c *Controller) logInfoIfEnabled(msg string, fields ...logger.Field) {
	if c.apiLogger != nil {
		c.apiLogger.Info(msg, fields...)
	}
}

// logErrorIfEnabled logs error message if apiLogger is enabled
func (c *Controller) logErrorIfEnabled(msg string, fields ...logger.Field) {
	if c.apiLogger != nil {
		c.apiLogger.Error(msg, fields...)
	}
}

// logWarnIfEnabled logs warning message if apiLogger is enabled
func (c *Controller) logWarnIfEnabled(msg string, fields ...logger.Field) {
	if c.apiLogger != nil {
		c.apiLogger.Warn(msg, fields...)
	}
}

// logDebugIfEnabled logs debug message if apiLogger is enabled
func (c *Controller) logDebugIfEnabled(msg string, fields ...logger.Field) {
	if c.apiLogger != nil {
		c.apiLogger.Debug(msg, fields...)
	}
}

// =============================================================================
// Batch Response Helpers
// =============================================================================

// handleBatchResponse handles the common pattern for batch API responses with partial failure support.
// It logs warnings for partial failures, returns an error if all items failed, and returns JSON on success.
func (c *Controller) handleBatchResponse(ctx echo.Context, result any, successCount, requestedCount int, processingErrors []string, operationName, ip, urlPath string) error {
	// Log partial failures if any
	if len(processingErrors) > 0 && successCount > 0 {
		c.logWarnIfEnabled(operationName+" completed with partial failures",
			logger.Int("successful", successCount),
			logger.Int("failed", len(processingErrors)),
			logger.Any("errors", processingErrors),
			logger.String("ip", ip),
			logger.String("path", urlPath))
	}

	// Return error if all items failed
	if successCount == 0 {
		c.logErrorIfEnabled("All items in "+operationName+" failed",
			logger.Int("requested", requestedCount),
			logger.Any("errors", processingErrors),
			logger.String("ip", ip),
			logger.String("path", urlPath))
		return c.HandleError(ctx, fmt.Errorf("failed to process any requested items"),
			"Failed to process "+operationName, http.StatusInternalServerError)
	}

	// Log successful completion
	c.logInfoIfEnabled(operationName+" completed",
		logger.Int("requested", requestedCount),
		logger.Int("successful", successCount),
		logger.Int("failed", len(processingErrors)),
		logger.String("ip", ip),
		logger.String("path", urlPath))

	return ctx.JSON(http.StatusOK, result)
}

// handleErrorWithNotFound handles errors that may be not-found errors.
// If the error is an EnhancedError with CategoryNotFound, returns a 404.
// Otherwise returns a 500 internal server error.
func (c *Controller) handleErrorWithNotFound(ctx echo.Context, err error, notFoundMsg, fallbackMsg string) error {
	var enhancedErr *errors.EnhancedError
	if errors.As(err, &enhancedErr) && enhancedErr.Category == errors.CategoryNotFound {
		return c.HandleError(ctx, err, notFoundMsg, http.StatusNotFound)
	}
	return c.HandleError(ctx, err, fallbackMsg, http.StatusInternalServerError)
}
