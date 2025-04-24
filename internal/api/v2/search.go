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
	// Search endpoints - publicly accessible
	c.Group.POST("/search", c.HandleSearch)
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
	// Parse the request
	var req SearchRequest
	if err := ctx.Bind(&req); err != nil {
		return c.HandleError(ctx, err, "Invalid request format", http.StatusBadRequest)
	}

	// Validate request
	if req.Page < 1 {
		req.Page = 1
	}

	// Validate date strings
	if req.DateStart != "" {
		if _, err := time.Parse("2006-01-02", req.DateStart); err != nil {
			return c.HandleError(ctx, err, "Invalid start date format, use YYYY-MM-DD", http.StatusBadRequest)
		}
	}
	if req.DateEnd != "" {
		if _, err := time.Parse("2006-01-02", req.DateEnd); err != nil {
			return c.HandleError(ctx, err, "Invalid end date format, use YYYY-MM-DD", http.StatusBadRequest)
		}
	}

	// Ensure start ≤ end
	if req.DateStart != "" && req.DateEnd != "" {
		start, _ := time.Parse("2006-01-02", req.DateStart) // Errors already checked above
		end, _ := time.Parse("2006-01-02", req.DateEnd)     // Errors already checked above
		if start.After(end) {
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
		return c.HandleError(ctx, nil, "Invalid verified status. Use 'any', 'verified', or 'unverified'", http.StatusBadRequest)
	}
	validLockedStatus := map[string]bool{"any": true, "locked": true, "unlocked": true}
	if req.LockedStatus == "" {
		req.LockedStatus = "any"
	}
	if !validLockedStatus[req.LockedStatus] {
		return c.HandleError(ctx, nil, "Invalid locked status. Use 'any', 'locked', or 'unlocked'", http.StatusBadRequest)
	}

	// Set default values
	if req.ConfidenceMin < 0 {
		req.ConfidenceMin = 0
	}
	switch {
	case req.ConfidenceMax > 1:
		req.ConfidenceMax = 1 // clamp to maximum
	case req.ConfidenceMax < 0:
		req.ConfidenceMax = 0 // handle negative values
	case req.ConfidenceMax == 0:
		// leave untouched – treat as "explicit 0"
	}

	// Ensure min ≤ max
	if req.ConfidenceMin > req.ConfidenceMax {
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
		Page:           req.Page,
		PerPage:        20, // Configure as needed
		SortBy:         req.SortBy,
		Ctx:            ctxTimeout, // Pass the context with timeout
	}

	// Execute the search
	results, total, err := c.DS.SearchDetections(&filters)
	if err != nil {
		return c.HandleError(ctx, err, "Search failed", http.StatusInternalServerError)
	}

	// Calculate total pages
	totalPages := (total + filters.PerPage - 1) / filters.PerPage
	if totalPages < 1 {
		totalPages = 1
	}

	// Return response
	return ctx.JSON(http.StatusOK, SearchResponse{
		Results:     results,
		Total:       total,
		Pages:       totalPages,
		CurrentPage: min(req.Page, totalPages), // Clamp current page
	})
}
