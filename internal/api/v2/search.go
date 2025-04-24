package api

import (
	"net/http"

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

	// Set default values
	if req.ConfidenceMin < 0 {
		req.ConfidenceMin = 0
	}
	if req.ConfidenceMax > 1 || req.ConfidenceMax == 0 {
		req.ConfidenceMax = 1
	}

	// Ensure min ≤ max
	if req.ConfidenceMin > req.ConfidenceMax {
		req.ConfidenceMin, req.ConfidenceMax = req.ConfidenceMax, req.ConfidenceMin
	}

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
		CurrentPage: req.Page,
	})
}
