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
	if c.apiLogger != nil {
		c.apiLogger.Info("Initializing search routes")
	}

	// Search endpoints - publicly accessible
	c.Group.POST("/search", c.HandleSearch)

	if c.apiLogger != nil {
		c.apiLogger.Info("Search routes initialized successfully")
	}
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
	if c.apiLogger != nil {
		c.apiLogger.Info("Handling search request", "path", path, "ip", ip)
	}

	// Parse the request
	var req SearchRequest
	if err := ctx.Bind(&req); err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to bind search request", "error", err.Error(), "path", path, "ip", ip)
		}
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
	if c.apiLogger != nil {
		c.apiLogger.Debug("Executing search with filters", "filters", filters, "path", path, "ip", ip)
	}

	// Execute the search
	results, total, err := c.DS.SearchDetections(&filters)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Search query failed", "error", err.Error(), "filters", fmt.Sprintf("%+v", filters), "path", path, "ip", ip)
		}
		return c.HandleError(ctx, err, "Search failed", http.StatusInternalServerError)
	}

	// Build and return response
	resp := c.buildSearchResponse(&req, results, total, filters.PerPage)
	if c.apiLogger != nil {
		c.apiLogger.Info("Search completed successfully",
			"total_results", resp.Total,
			"results_returned", len(resp.Results),
			"total_pages", resp.Pages,
			"current_page", resp.CurrentPage,
			"path", path, "ip", ip,
		)
	}

	return ctx.JSON(http.StatusOK, resp)
}

// logValidatedRequest logs the validated parameters for debugging.
func (c *Controller) logValidatedRequest(path, ip string, req *SearchRequest) {
	c.Debug("Validated Search request: Species='%s', DateStart='%s', DateEnd='%s', ConfidenceMin=%f, ConfidenceMax=%f, VerifiedStatus='%s', LockedStatus='%s', TimeOfDay='%s', Page=%d, SortBy='%s'",
		req.Species, req.DateStart, req.DateEnd, req.ConfidenceMin, req.ConfidenceMax, req.VerifiedStatus,
		req.LockedStatus, req.TimeOfDay, req.Page, req.SortBy)
	if c.apiLogger != nil {
		c.apiLogger.Debug("Validated search request parameters",
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
		if c.apiLogger != nil {
			c.apiLogger.Warn("Invalid page number requested, defaulting to 1", "requested_page", req.Page, "path", path, "ip", ip)
		}
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
	if req.DateStart != "" {
		if _, err := time.Parse("2006-01-02", req.DateStart); err != nil {
			if c.apiLogger != nil {
				c.apiLogger.Error("Invalid start date format", "dateStart", req.DateStart, "error", err.Error(), "path", path, "ip", ip)
			}
			return fmt.Errorf("invalid start date format '%s', use YYYY-MM-DD", req.DateStart)
		}
	}
	if req.DateEnd != "" {
		if _, err := time.Parse("2006-01-02", req.DateEnd); err != nil {
			if c.apiLogger != nil {
				c.apiLogger.Error("Invalid end date format", "dateEnd", req.DateEnd, "error", err.Error(), "path", path, "ip", ip)
			}
			return fmt.Errorf("invalid end date format '%s', use YYYY-MM-DD", req.DateEnd)
		}
	}
	if req.DateStart != "" && req.DateEnd != "" {
		start, _ := time.Parse("2006-01-02", req.DateStart)
		end, _ := time.Parse("2006-01-02", req.DateEnd)
		if start.After(end) {
			if c.apiLogger != nil {
				c.apiLogger.Error("Invalid date range: start date is after end date", "dateStart", req.DateStart, "dateEnd", req.DateEnd, "path", path, "ip", ip)
			}
			return fmt.Errorf("'dateStart' (%s) must be earlier than or equal to 'dateEnd' (%s)", req.DateStart, req.DateEnd)
		}
	}
	return nil
}

// validateSearchStatusEnums validates VerifiedStatus and LockedStatus.
func (c *Controller) validateSearchStatusEnums(path, ip string, req *SearchRequest) error {
	validVerifiedStatus := map[string]bool{"any": true, "verified": true, "unverified": true}
	if req.VerifiedStatus == "" {
		req.VerifiedStatus = "any"
	} else if !validVerifiedStatus[req.VerifiedStatus] {
		if c.apiLogger != nil {
			c.apiLogger.Error("Invalid verified status parameter", "verifiedStatus", req.VerifiedStatus, "path", path, "ip", ip)
		}
		return fmt.Errorf("invalid verified status '%s'. Use 'any', 'verified', or 'unverified'", req.VerifiedStatus)
	}

	validLockedStatus := map[string]bool{"any": true, "locked": true, "unlocked": true}
	if req.LockedStatus == "" {
		req.LockedStatus = "any"
	} else if !validLockedStatus[req.LockedStatus] {
		if c.apiLogger != nil {
			c.apiLogger.Error("Invalid locked status parameter", "lockedStatus", req.LockedStatus, "path", path, "ip", ip)
		}
		return fmt.Errorf("invalid locked status '%s'. Use 'any', 'locked', or 'unlocked'", req.LockedStatus)
	}
	return nil
}

// validateSearchTimeOfDay validates the TimeOfDay parameter.
func (c *Controller) validateSearchTimeOfDay(path, ip string, req *SearchRequest) error {
	validTimeOfDay := map[string]bool{"any": true, "day": true, "night": true, "sunrise": true, "sunset": true}
	if req.TimeOfDay == "" {
		req.TimeOfDay = "any"
	} else if !validTimeOfDay[req.TimeOfDay] {
		if c.apiLogger != nil {
			c.apiLogger.Error("Invalid time of day parameter", "timeOfDay", req.TimeOfDay, "path", path, "ip", ip)
		}
		return fmt.Errorf("invalid time of day '%s'. Use 'any', 'day', 'night', 'sunrise', or 'sunset'", req.TimeOfDay)
	}
	return nil
}

// validateSearchConfidenceRange validates and normalizes ConfidenceMin and ConfidenceMax.
func (c *Controller) validateSearchConfidenceRange(path, ip string, req *SearchRequest) error {
	if req.ConfidenceMin < 0 {
		if c.apiLogger != nil {
			c.apiLogger.Warn("Invalid confidenceMin, adjusted to 0", "originalConfidenceMin", req.ConfidenceMin, "path", path, "ip", ip)
		}
		req.ConfidenceMin = 0
	}
	switch {
	case req.ConfidenceMax > 1:
		if c.apiLogger != nil {
			c.apiLogger.Warn("Invalid confidenceMax, adjusted to 1", "originalConfidenceMax", req.ConfidenceMax, "path", path, "ip", ip)
		}
		req.ConfidenceMax = 1 // clamp to maximum
	case req.ConfidenceMax < 0:
		if c.apiLogger != nil {
			c.apiLogger.Warn("Invalid confidenceMax, adjusted to 0", "originalConfidenceMax", req.ConfidenceMax, "path", path, "ip", ip)
		}
		req.ConfidenceMax = 0 // handle negative values
	case req.ConfidenceMax == 0 && req.ConfidenceMin == 0:
		// Assume user wants full range [0, 1] if both are zero initially.
		if c.apiLogger != nil {
			c.apiLogger.Debug("Confidence range is [0, 0], defaulting ConfidenceMax to 1", "path", path, "ip", ip)
		}
		req.ConfidenceMax = 1
	}

	// Ensure min ≤ max after normalization
	if req.ConfidenceMin > req.ConfidenceMax {
		if c.apiLogger != nil {
			c.apiLogger.Warn("ConfidenceMin > ConfidenceMax after normalization, swapping values",
				"normalizedConfidenceMin", req.ConfidenceMin,
				"normalizedConfidenceMax", req.ConfidenceMax,
				"path", path, "ip", ip)
		}
		req.ConfidenceMin, req.ConfidenceMax = req.ConfidenceMax, req.ConfidenceMin
	}
	return nil
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
			if c.apiLogger != nil {
				c.apiLogger.Error("Invalid sortBy parameter", "sortBy", req.SortBy, "path", path, "ip", ip)
			}
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
