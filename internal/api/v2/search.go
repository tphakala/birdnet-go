package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

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
	if c.apiLogger != nil {
		c.apiLogger.Info("Handling search request",
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	// Parse the request
	var req SearchRequest
	if err := ctx.Bind(&req); err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to bind search request",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, err, "Invalid request format", http.StatusBadRequest)
	}

	// Debug logging for the request parameters
	c.Debug("Search request: Species='%s', DateStart='%s', DateEnd='%s', ConfidenceMin=%f, ConfidenceMax=%f, VerifiedStatus='%s', LockedStatus='%s', TimeOfDay='%s', Page=%d, SortBy='%s'",
		req.Species, req.DateStart, req.DateEnd, req.ConfidenceMin, req.ConfidenceMax, req.VerifiedStatus,
		req.LockedStatus, req.TimeOfDay, req.Page, req.SortBy)

	if c.apiLogger != nil {
		c.apiLogger.Debug("Parsed search request parameters",
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
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	// Validate request
	if req.Page < 1 {
		if c.apiLogger != nil {
			c.apiLogger.Warn("Invalid page number requested, defaulting to 1",
				"requested_page", req.Page,
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		req.Page = 1
	}

	// Validate date strings
	if req.DateStart != "" {
		if _, err := time.Parse("2006-01-02", req.DateStart); err != nil {
			if c.apiLogger != nil {
				c.apiLogger.Error("Invalid start date format",
					"dateStart", req.DateStart,
					"error", err.Error(),
					"path", ctx.Request().URL.Path,
					"ip", ctx.RealIP(),
				)
			}
			return c.HandleError(ctx, err, "Invalid start date format, use YYYY-MM-DD", http.StatusBadRequest)
		}
	}
	if req.DateEnd != "" {
		if _, err := time.Parse("2006-01-02", req.DateEnd); err != nil {
			if c.apiLogger != nil {
				c.apiLogger.Error("Invalid end date format",
					"dateEnd", req.DateEnd,
					"error", err.Error(),
					"path", ctx.Request().URL.Path,
					"ip", ctx.RealIP(),
				)
			}
			return c.HandleError(ctx, err, "Invalid end date format, use YYYY-MM-DD", http.StatusBadRequest)
		}
	}

	// Ensure start ≤ end
	if req.DateStart != "" && req.DateEnd != "" {
		start, _ := time.Parse("2006-01-02", req.DateStart) // Errors already checked above
		end, _ := time.Parse("2006-01-02", req.DateEnd)     // Errors already checked above
		if start.After(end) {
			if c.apiLogger != nil {
				c.apiLogger.Error("Invalid date range: start date is after end date",
					"dateStart", req.DateStart,
					"dateEnd", req.DateEnd,
					"path", ctx.Request().URL.Path,
					"ip", ctx.RealIP(),
				)
			}
			return c.HandleError(ctx, nil,
				"'dateStart' must be earlier than or equal to 'dateEnd'",
				http.StatusBadRequest)
		}
	}

	// Validate status enums
	validVerifiedStatus := map[string]bool{"any": true, "verified": true, "unverified": true}
	if req.VerifiedStatus == "" {
		req.VerifiedStatus = "any"
	}
	if !validVerifiedStatus[req.VerifiedStatus] {
		if c.apiLogger != nil {
			c.apiLogger.Error("Invalid verified status parameter",
				"verifiedStatus", req.VerifiedStatus,
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, nil, "Invalid verified status. Use 'any', 'verified', or 'unverified'", http.StatusBadRequest)
	}
	validLockedStatus := map[string]bool{"any": true, "locked": true, "unlocked": true}
	if req.LockedStatus == "" {
		req.LockedStatus = "any"
	}
	if !validLockedStatus[req.LockedStatus] {
		if c.apiLogger != nil {
			c.apiLogger.Error("Invalid locked status parameter",
				"lockedStatus", req.LockedStatus,
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, nil, "Invalid locked status. Use 'any', 'locked', or 'unlocked'", http.StatusBadRequest)
	}

	// Validate time of day
	validTimeOfDay := map[string]bool{"any": true, "day": true, "night": true, "sunrise": true, "sunset": true}
	if req.TimeOfDay == "" {
		req.TimeOfDay = "any"
	}
	if !validTimeOfDay[req.TimeOfDay] {
		if c.apiLogger != nil {
			c.apiLogger.Error("Invalid time of day parameter",
				"timeOfDay", req.TimeOfDay,
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, nil, "Invalid time of day. Use 'any', 'day', 'night', 'sunrise', or 'sunset'", http.StatusBadRequest)
	}

	// Set default values
	if req.ConfidenceMin < 0 {
		// Log if confidence was adjusted
		if c.apiLogger != nil {
			c.apiLogger.Warn("Invalid confidenceMin, adjusted to 0",
				"originalConfidenceMin", req.ConfidenceMin,
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		req.ConfidenceMin = 0
	}
	switch {
	case req.ConfidenceMax > 1:
		if c.apiLogger != nil {
			c.apiLogger.Warn("Invalid confidenceMax, adjusted to 1",
				"originalConfidenceMax", req.ConfidenceMax,
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		req.ConfidenceMax = 1 // clamp to maximum
	case req.ConfidenceMax < 0:
		if c.apiLogger != nil {
			c.apiLogger.Warn("Invalid confidenceMax, adjusted to 0",
				"originalConfidenceMax", req.ConfidenceMax,
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		req.ConfidenceMax = 0 // handle negative values
	case req.ConfidenceMax == 0:
		// leave untouched – treat as "explicit 0"
	}

	// Ensure min ≤ max
	if req.ConfidenceMin > req.ConfidenceMax {
		if c.apiLogger != nil {
			c.apiLogger.Warn("ConfidenceMin > ConfidenceMax, swapping values",
				"originalConfidenceMin", req.ConfidenceMin,
				"originalConfidenceMax", req.ConfidenceMax,
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		req.ConfidenceMin, req.ConfidenceMax = req.ConfidenceMax, req.ConfidenceMin
	}

	// Validate SortBy parameter
	allowedSortBy := map[string]struct{}{
		"date_desc":       {},
		"date_asc":        {},
		"species_asc":     {},
		"confidence_desc": {},
	}
	if req.SortBy != "" { // Allow empty string for default sorting
		if _, ok := allowedSortBy[req.SortBy]; !ok {
			if c.apiLogger != nil {
				c.apiLogger.Error("Invalid sortBy parameter",
					"sortBy", req.SortBy,
					"path", ctx.Request().URL.Path,
					"ip", ctx.RealIP(),
				)
			}
			return c.HandleError(ctx, fmt.Errorf("unsupported sort option: %s", req.SortBy), "Invalid sortBy parameter", http.StatusBadRequest)
		}
	}

	// Create context with timeout for the database query
	ctxTimeout, cancel := context.WithTimeout(ctx.Request().Context(), 60*time.Second)
	defer cancel() // Ensure the context is cancelled

	// Default sort will be handled by the datastore layer
	// The datastore defaults to "notes.date DESC, notes.time DESC" when no sort is specified

	// Create search filters
	filters := datastore.SearchFilters{
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
		PerPage:        20, // Configure as needed
		SortBy:         req.SortBy,
		Ctx:            ctxTimeout, // Pass the context with timeout
	}

	if c.apiLogger != nil {
		c.apiLogger.Debug("Executing search with filters",
			"filters", fmt.Sprintf("%+v", filters), // Log the full filter struct
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	// Execute the search
	results, total, err := c.DS.SearchDetections(&filters)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Search query failed",
				"error", err.Error(),
				"filters", fmt.Sprintf("%+v", filters),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, err, "Search failed", http.StatusInternalServerError)
	}

	// Calculate total pages
	totalPages := (total + filters.PerPage - 1) / filters.PerPage
	if totalPages < 1 {
		totalPages = 1
	}

	// Return response
	resp := SearchResponse{
		Results:     results,
		Total:       total,
		Pages:       totalPages,
		CurrentPage: min(req.Page, totalPages), // Clamp current page
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Search completed successfully",
			"total_results", resp.Total,
			"results_returned", len(resp.Results),
			"total_pages", resp.Pages,
			"current_page", resp.CurrentPage,
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	return ctx.JSON(http.StatusOK, resp)
}
