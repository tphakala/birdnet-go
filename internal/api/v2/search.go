package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/datastore"
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
	c.logInfoIfEnabled("Handling search request", "path", path, "ip", ip)

	// Parse the request
	var req SearchRequest
	if err := ctx.Bind(&req); err != nil {
		c.logErrorIfEnabled("Failed to bind search request", "error", err.Error(), "path", path, "ip", ip)
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
	c.logDebugIfEnabled("Executing search with filters", "filters", filters, "path", path, "ip", ip)

	// Execute the search
	results, total, err := c.DS.SearchDetections(&filters)
	if err != nil {
		c.logErrorIfEnabled("Search query failed", "error", err.Error(), "filters", fmt.Sprintf("%+v", filters), "path", path, "ip", ip)
		return c.HandleError(ctx, err, "Search failed", http.StatusInternalServerError)
	}

	// Build and return response
	resp := c.buildSearchResponse(&req, results, total, filters.PerPage)
	c.logInfoIfEnabled("Search completed successfully",
		"total_results", resp.Total,
		"results_returned", len(resp.Results),
		"total_pages", resp.Pages,
		"current_page", resp.CurrentPage,
		"path", path, "ip", ip,
	)

	return ctx.JSON(http.StatusOK, resp)
}

// logValidatedRequest logs the validated parameters for debugging.
func (c *Controller) logValidatedRequest(path, ip string, req *SearchRequest) {
	c.Debug("Validated Search request: Species='%s', DateStart='%s', DateEnd='%s', ConfidenceMin=%f, ConfidenceMax=%f, VerifiedStatus='%s', LockedStatus='%s', TimeOfDay='%s', Page=%d, SortBy='%s'",
		req.Species, req.DateStart, req.DateEnd, req.ConfidenceMin, req.ConfidenceMax, req.VerifiedStatus,
		req.LockedStatus, req.TimeOfDay, req.Page, req.SortBy)
	c.logDebugIfEnabled("Validated search request parameters",
		"species", req.Species,
		"dateStart", req.DateStart,
		"dateEnd", req.DateEnd,
		"confidenceMin", req.ConfidenceMin,
		"confidenceMax", req.ConfidenceMax,
		"verifiedStatus", req.VerifiedStatus,
		"lockedStatus", req.LockedStatus,
		"deviceFilter", req.DeviceFilter,
		"timeOfDay", req.TimeOfDay,
		"page", req.Page,
		"sortBy", req.SortBy,
		"path", path, "ip", ip,
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
		c.logWarnIfEnabled("Invalid page number requested, defaulting to 1", "requested_page", req.Page, "path", path, "ip", ip)
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
		c.logErrorIfEnabled("Invalid start date format", "dateStart", req.DateStart, "path", path, "ip", ip)
		return err
	}
	if err := validateDateFormat(req.DateEnd, "end date"); err != nil {
		c.logErrorIfEnabled("Invalid end date format", "dateEnd", req.DateEnd, "path", path, "ip", ip)
		return err
	}
	if err := validateDateOrder(req.DateStart, req.DateEnd); err != nil {
		c.logErrorIfEnabled("Invalid date range", "dateStart", req.DateStart, "dateEnd", req.DateEnd, "path", path, "ip", ip)
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
		c.logErrorIfEnabled("Invalid verified status parameter", "verifiedStatus", req.VerifiedStatus, "path", path, "ip", ip)
		return fmt.Errorf("invalid verified status '%s'. Use 'any', 'verified', or 'unverified'", req.VerifiedStatus)
	}

	validLockedStatus := map[string]bool{QueryValueAny: true, "locked": true, "unlocked": true}
	if req.LockedStatus == "" {
		req.LockedStatus = QueryValueAny
	} else if !validLockedStatus[req.LockedStatus] {
		c.logErrorIfEnabled("Invalid locked status parameter", "lockedStatus", req.LockedStatus, "path", path, "ip", ip)
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
		c.logErrorIfEnabled("Invalid time of day parameter", "timeOfDay", req.TimeOfDay, "path", path, "ip", ip)
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
		c.logDebugIfEnabled("Confidence range is [0, 0], defaulting ConfidenceMax to 1", "path", path, "ip", ip)
		return 1
	default:
		return maxConf
	}
}

// logConfidenceAdjustment logs when a confidence value is adjusted
func (c *Controller) logConfidenceAdjustment(field string, original, adjusted float64, path, ip string) {
	c.logWarnIfEnabled("Invalid "+field+", adjusted", "original", original, "adjusted", adjusted, "path", path, "ip", ip)
}

// logConfidenceSwap logs when min/max values are swapped
func (c *Controller) logConfidenceSwap(minConf, maxConf float64, path, ip string) {
	c.logWarnIfEnabled("ConfidenceMin > ConfidenceMax after normalization, swapping values",
		"normalizedConfidenceMin", minConf, "normalizedConfidenceMax", maxConf, "path", path, "ip", ip)
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
			c.logErrorIfEnabled("Invalid sortBy parameter", "sortBy", req.SortBy, "path", path, "ip", ip)
			// Create a list of allowed sort options for the error message
			allowedKeys := make([]string, 0, len(allowedSortBy))
			for k := range allowedSortBy {
				allowedKeys = append(allowedKeys, k)
			}
			return fmt.Errorf("invalid sortBy parameter '%s'. Allowed values: %v", req.SortBy, allowedKeys)
		}
	}
	return nil
}
