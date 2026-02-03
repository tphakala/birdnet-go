package api

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const defaultSearchTimeout = 60 * time.Second
const defaultPerPage = 20

// initSearchRoutes registers the search-related routes
func (c *Controller) initSearchRoutes() {
	c.logInfoIfEnabled("Initializing search routes")

	// Search endpoints - publicly accessible
	c.Group.POST("/search", c.HandleSearch)

	c.logInfoIfEnabled("Search routes initialized successfully")
}

// SearchRequest defines the structure of the search API request
type SearchRequest struct {
	Species        string  `json:"species"`
	DateStart      string  `json:"dateStart"`
	DateEnd        string  `json:"dateEnd"`
	ConfidenceMin  float64 `json:"confidenceMin"`
	ConfidenceMax  float64 `json:"confidenceMax"`
	VerifiedStatus string  `json:"verifiedStatus"`
	LockedStatus   string  `json:"lockedStatus"`
	DeviceFilter   string  `json:"deviceFilter"`
	TimeOfDay      string  `json:"timeOfDay"`
	Page           int     `json:"page"`
	SortBy         string  `json:"sortBy"`
}

// SearchResponse defines the structure of the search API response
type SearchResponse struct {
	Results     []datastore.DetectionRecord `json:"results"`
	Total       int                         `json:"total"`
	Pages       int                         `json:"pages"`
	CurrentPage int                         `json:"currentPage"`
}

// HandleSearch processes search requests
func (c *Controller) HandleSearch(ctx echo.Context) error {
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path
	c.logInfoIfEnabled("Handling search request", logger.String("path", path), logger.String("ip", ip))

	// Parse the request
	var req SearchRequest
	if err := ctx.Bind(&req); err != nil {
		c.logErrorIfEnabled("Failed to bind search request", logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, err, "Invalid request format", http.StatusBadRequest)
	}

	// Validate and normalize the request
	if err := c.validateAndNormalizeSearchRequest(ctx, &req); err != nil {
		return c.HandleError(ctx, err, err.Error(), http.StatusBadRequest)
	}

	// Log validated request parameters
	c.logValidatedRequest(path, ip, &req)

	// Create context with timeout
	ctxTimeout, cancel := context.WithTimeout(ctx.Request().Context(), defaultSearchTimeout)
	defer cancel()

	// Build filters
	filters := c.buildSearchFilters(&req, ctxTimeout)
	c.logDebugIfEnabled("Executing search with filters", logger.Any("filters", filters), logger.String("path", path), logger.String("ip", ip))

	// Execute the search
	results, total, err := c.DS.SearchDetections(&filters)
	if err != nil {
		c.logErrorIfEnabled("Search query failed", logger.Error(err), logger.String("filters", fmt.Sprintf("%+v", filters)), logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, err, "Search failed", http.StatusInternalServerError)
	}

	// Build and return response
	resp := c.buildSearchResponse(&req, results, total, filters.PerPage)
	c.logInfoIfEnabled("Search completed successfully",
		logger.Int("total_results", resp.Total),
		logger.Int("results_returned", len(resp.Results)),
		logger.Int("total_pages", resp.Pages),
		logger.Int("current_page", resp.CurrentPage),
		logger.String("path", path),
		logger.String("ip", ip),
	)

	return ctx.JSON(http.StatusOK, resp)
}

// logValidatedRequest logs the validated parameters for debugging.
func (c *Controller) logValidatedRequest(path, ip string, req *SearchRequest) {
	c.Debug("Validated Search request: Species='%s', DateStart='%s', DateEnd='%s', ConfidenceMin=%f, ConfidenceMax=%f, VerifiedStatus='%s', LockedStatus='%s', TimeOfDay='%s', Page=%d, SortBy='%s'",
		req.Species, req.DateStart, req.DateEnd, req.ConfidenceMin, req.ConfidenceMax, req.VerifiedStatus,
		req.LockedStatus, req.TimeOfDay, req.Page, req.SortBy)
	c.logDebugIfEnabled("Validated search request parameters",
		logger.String("species", req.Species),
		logger.String("dateStart", req.DateStart),
		logger.String("dateEnd", req.DateEnd),
		logger.Float64("confidenceMin", req.ConfidenceMin),
		logger.Float64("confidenceMax", req.ConfidenceMax),
		logger.String("verifiedStatus", req.VerifiedStatus),
		logger.String("lockedStatus", req.LockedStatus),
		logger.String("deviceFilter", req.DeviceFilter),
		logger.String("timeOfDay", req.TimeOfDay),
		logger.Int("page", req.Page),
		logger.String("sortBy", req.SortBy),
		logger.String("path", path),
		logger.String("ip", ip),
	)
}

// buildSearchFilters creates the datastore search filters from the request.
func (c *Controller) buildSearchFilters(req *SearchRequest, ctxTimeout context.Context) datastore.SearchFilters {
	return datastore.SearchFilters{
		Species:        req.Species,
		DateStart:      req.DateStart,
		DateEnd:        req.DateEnd,
		ConfidenceMin:  req.ConfidenceMin,
		ConfidenceMax:  req.ConfidenceMax,
		VerifiedOnly:   req.VerifiedStatus == "verified",
		UnverifiedOnly: req.VerifiedStatus == "unverified",
		LockedOnly:     req.LockedStatus == "locked",
		UnlockedOnly:   req.LockedStatus == "unlocked",
		Device:         req.DeviceFilter,
		TimeOfDay:      req.TimeOfDay,
		Page:           req.Page,
		PerPage:        defaultPerPage,
		SortBy:         req.SortBy,
		Ctx:            ctxTimeout,
	}
}

// buildSearchResponse constructs the API response from search results.
func (c *Controller) buildSearchResponse(req *SearchRequest, results []datastore.DetectionRecord, total, perPage int) SearchResponse {
	totalPages := 1
	if total > 0 && perPage > 0 {
		totalPages = (total + perPage - 1) / perPage
	}

	currentPage := 1
	if req.Page > 0 {
		currentPage = min(req.Page, totalPages) // Clamp current page to valid range
	}

	return SearchResponse{
		Results:     results,
		Total:       total,
		Pages:       totalPages,
		CurrentPage: currentPage,
	}
}

// validateAndNormalizeSearchRequest checks the search request parameters for validity,
// applies default values, and normalizes ranges.
// It modifies the passed SearchRequest pointer directly.
// Returns an error suitable for user display if validation fails.
func (c *Controller) validateAndNormalizeSearchRequest(ctx echo.Context, req *SearchRequest) error {
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path

	// Validate Page
	if req.Page < 1 {
		c.logWarnIfEnabled("Invalid page number requested, defaulting to 1", logger.Int("requested_page", req.Page), logger.String("path", path), logger.String("ip", ip))
		req.Page = 1
	}

	var err error
	err = c.validateSearchDates(path, ip, req)
	if err != nil {
		return err
	}

	err = c.validateSearchStatusEnums(path, ip, req)
	if err != nil {
		return err
	}

	err = c.validateSearchTimeOfDay(path, ip, req)
	if err != nil {
		return err
	}

	err = c.validateSearchConfidenceRange(path, ip, req)
	if err != nil {
		return err
	}

	err = c.validateSearchSortBy(path, ip, req)
	if err != nil {
		return err
	}

	return nil // All validations passed
}

// validateSearchDates validates the DateStart and DateEnd parameters.
func (c *Controller) validateSearchDates(path, ip string, req *SearchRequest) error {
	if err := validateDateFormat(req.DateStart, "start date"); err != nil {
		c.logErrorIfEnabled("Invalid start date format", logger.String("dateStart", req.DateStart), logger.String("path", path), logger.String("ip", ip))
		return err
	}
	if err := validateDateFormat(req.DateEnd, "end date"); err != nil {
		c.logErrorIfEnabled("Invalid end date format", logger.String("dateEnd", req.DateEnd), logger.String("path", path), logger.String("ip", ip))
		return err
	}
	if err := validateDateOrder(req.DateStart, req.DateEnd); err != nil {
		c.logErrorIfEnabled("Invalid date range", logger.String("dateStart", req.DateStart), logger.String("dateEnd", req.DateEnd), logger.String("path", path), logger.String("ip", ip))
		return fmt.Errorf("'dateStart' (%s) must be earlier than or equal to 'dateEnd' (%s)", req.DateStart, req.DateEnd)
	}
	return nil
}

// validateSearchStatusEnums validates VerifiedStatus and LockedStatus.
func (c *Controller) validateSearchStatusEnums(path, ip string, req *SearchRequest) error {
	validVerifiedStatus := map[string]bool{QueryValueAny: true, "verified": true, "unverified": true}
	if req.VerifiedStatus == "" {
		req.VerifiedStatus = QueryValueAny
	} else if !validVerifiedStatus[req.VerifiedStatus] {
		c.logErrorIfEnabled("Invalid verified status parameter", logger.String("verifiedStatus", req.VerifiedStatus), logger.String("path", path), logger.String("ip", ip))
		return fmt.Errorf("invalid verified status '%s'. Use 'any', 'verified', or 'unverified'", req.VerifiedStatus)
	}

	validLockedStatus := map[string]bool{QueryValueAny: true, "locked": true, "unlocked": true}
	if req.LockedStatus == "" {
		req.LockedStatus = QueryValueAny
	} else if !validLockedStatus[req.LockedStatus] {
		c.logErrorIfEnabled("Invalid locked status parameter", logger.String("lockedStatus", req.LockedStatus), logger.String("path", path), logger.String("ip", ip))
		return fmt.Errorf("invalid locked status '%s'. Use 'any', 'locked', or 'unlocked'", req.LockedStatus)
	}
	return nil
}

// validateSearchTimeOfDay validates the TimeOfDay parameter.
func (c *Controller) validateSearchTimeOfDay(path, ip string, req *SearchRequest) error {
	validTimeOfDay := map[string]bool{QueryValueAny: true, "day": true, "night": true, "sunrise": true, "sunset": true}
	if req.TimeOfDay == "" {
		req.TimeOfDay = QueryValueAny
	} else if !validTimeOfDay[req.TimeOfDay] {
		c.logErrorIfEnabled("Invalid time of day parameter", logger.String("timeOfDay", req.TimeOfDay), logger.String("path", path), logger.String("ip", ip))
		return fmt.Errorf("invalid time of day '%s'. Use 'any', 'day', 'night', 'sunrise', or 'sunset'", req.TimeOfDay)
	}
	return nil
}

// validateSearchConfidenceRange validates and normalizes ConfidenceMin and ConfidenceMax.
func (c *Controller) validateSearchConfidenceRange(path, ip string, req *SearchRequest) error {
	// Clamp confidenceMin to [0, 1]
	if req.ConfidenceMin < 0 {
		c.logConfidenceAdjustment("confidenceMin", req.ConfidenceMin, 0, path, ip)
		req.ConfidenceMin = 0
	}

	// Clamp confidenceMax and handle defaults
	req.ConfidenceMax = c.normalizeConfidenceMax(req.ConfidenceMin, req.ConfidenceMax, path, ip)

	// Ensure min ≤ max
	if req.ConfidenceMin > req.ConfidenceMax {
		c.logConfidenceSwap(req.ConfidenceMin, req.ConfidenceMax, path, ip)
		req.ConfidenceMin, req.ConfidenceMax = req.ConfidenceMax, req.ConfidenceMin
	}
	return nil
}

// normalizeConfidenceMax clamps and normalizes the confidence max value
func (c *Controller) normalizeConfidenceMax(minConf, maxConf float64, path, ip string) float64 {
	switch {
	case maxConf > 1:
		c.logConfidenceAdjustment("confidenceMax", maxConf, 1, path, ip)
		return 1
	case maxConf < 0:
		c.logConfidenceAdjustment("confidenceMax", maxConf, 0, path, ip)
		return 0
	case maxConf == 0 && minConf == 0:
		c.logDebugIfEnabled("Confidence range is [0, 0], defaulting ConfidenceMax to 1", logger.String("path", path), logger.String("ip", ip))
		return 1
	default:
		return maxConf
	}
}

// logConfidenceAdjustment logs when a confidence value is adjusted
func (c *Controller) logConfidenceAdjustment(field string, original, adjusted float64, path, ip string) {
	c.logWarnIfEnabled("Invalid "+field+", adjusted", logger.Float64("original", original), logger.Float64("adjusted", adjusted), logger.String("path", path), logger.String("ip", ip))
}

// logConfidenceSwap logs when min/max values are swapped
func (c *Controller) logConfidenceSwap(minConf, maxConf float64, path, ip string) {
	c.logWarnIfEnabled("ConfidenceMin > ConfidenceMax after normalization, swapping values",
		logger.Float64("normalizedConfidenceMin", minConf), logger.Float64("normalizedConfidenceMax", maxConf), logger.String("path", path), logger.String("ip", ip))
}

// validateSearchSortBy validates the SortBy parameter.
func (c *Controller) validateSearchSortBy(path, ip string, req *SearchRequest) error {
	allowedSortBy := map[string]struct{}{ // Use struct{} for memory efficiency
		"date_desc":       {},
		"date_asc":        {},
		"species_asc":     {},
		"confidence_desc": {},
	}
	if req.SortBy != "" { // Allow empty string for default sorting (handled by datastore)
		if _, ok := allowedSortBy[req.SortBy]; !ok {
			c.logErrorIfEnabled("Invalid sortBy parameter", logger.String("sortBy", req.SortBy), logger.String("path", path), logger.String("ip", ip))
			// Create a list of allowed sort options for the error message
			allowedKeys := slices.Collect(maps.Keys(allowedSortBy))
			return fmt.Errorf("invalid sortBy parameter '%s'. Allowed values: %v", req.SortBy, allowedKeys)
		}
	}
	return nil
}
