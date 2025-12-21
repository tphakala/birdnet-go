// internal/api/v2/dynamic_thresholds.go
// BG-59: Dynamic threshold runtime data and reset controls
package api

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Pagination defaults for dynamic threshold endpoints
const (
	defaultThresholdLimit = 50
	maxThresholdLimit     = 250
	defaultEventLimit     = 10
	maxEventLimit         = 100
)

// DynamicThresholdResponse represents a single dynamic threshold for API responses
type DynamicThresholdResponse struct {
	SpeciesName    string    `json:"speciesName"`
	ScientificName string    `json:"scientificName"`
	Level          int       `json:"level"`
	CurrentValue   float64   `json:"currentValue"`
	BaseThreshold  float64   `json:"baseThreshold"`
	HighConfCount  int       `json:"highConfCount"`
	ExpiresAt      time.Time `json:"expiresAt"`
	LastTriggered  time.Time `json:"lastTriggered"`
	FirstCreated   time.Time `json:"firstCreated"`
	TriggerCount   int       `json:"triggerCount"`
	IsActive       bool      `json:"isActive"`
}

// ThresholdEventResponse represents a threshold change event for API responses
type ThresholdEventResponse struct {
	ID            uint      `json:"id"`
	SpeciesName   string    `json:"speciesName"`
	PreviousLevel int       `json:"previousLevel"`
	NewLevel      int       `json:"newLevel"`
	PreviousValue float64   `json:"previousValue"`
	NewValue      float64   `json:"newValue"`
	ChangeReason  string    `json:"changeReason"`
	Confidence    float64   `json:"confidence,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

// ThresholdStatsResponse represents aggregate statistics about dynamic thresholds
type ThresholdStatsResponse struct {
	TotalCount        int64           `json:"totalCount"`
	ActiveCount       int64           `json:"activeCount"`
	AtMinimumCount    int64           `json:"atMinimumCount"`
	LevelDistribution []LevelStatItem `json:"levelDistribution"`
	ValidHours        int             `json:"validHours"`    // Configured threshold validity period in hours
	MinThreshold      float64         `json:"minThreshold"`  // Configured minimum threshold value
}

// LevelStatItem represents count for a specific level
type LevelStatItem struct {
	Level int   `json:"level"`
	Count int64 `json:"count"`
}

// initDynamicThresholdRoutes registers all dynamic threshold API endpoints
func (c *Controller) initDynamicThresholdRoutes() {
	// Public endpoints for reading threshold data
	c.Group.GET("/dynamic-thresholds", c.GetDynamicThresholds)
	c.Group.GET("/dynamic-thresholds/stats", c.GetDynamicThresholdStats)
	c.Group.GET("/dynamic-thresholds/:species", c.GetDynamicThreshold)
	c.Group.GET("/dynamic-thresholds/:species/events", c.GetThresholdEvents)

	// Protected endpoints for modifying thresholds (require authentication)
	c.Group.DELETE("/dynamic-thresholds/:species", c.ResetDynamicThreshold, c.authMiddleware)
	c.Group.DELETE("/dynamic-thresholds", c.ResetAllDynamicThresholds, c.authMiddleware)
}

// GetDynamicThresholds returns all dynamic thresholds with optional pagination
// GET /api/v2/dynamic-thresholds?limit=50&offset=0
func (c *Controller) GetDynamicThresholds(ctx echo.Context) error {
	// Check if processor is available
	if c.Processor == nil {
		return c.HandleError(ctx, errors.Newf("processor not available").
			Category(errors.CategorySystem).
			Component("api-dynamic-thresholds").
			Build(), "Processor not available", http.StatusServiceUnavailable)
	}

	// Parse pagination parameters
	limit := c.parsePaginationLimit(ctx.QueryParam("limit"), defaultThresholdLimit, maxThresholdLimit)
	offset := c.parsePaginationOffset(ctx.QueryParam("offset"))

	// Get merged threshold data from memory and database
	thresholdMap := c.getMergedThresholdData()

	// Convert map to slice
	result := make([]DynamicThresholdResponse, 0, len(thresholdMap))
	for _, v := range thresholdMap {
		result = append(result, *v)
	}

	// Apply pagination
	total := len(result)
	result = c.paginateThresholds(result, offset, limit)

	return ctx.JSON(http.StatusOK, map[string]any{
		"data":   result,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// parsePaginationLimit parses and validates the limit parameter
func (c *Controller) parsePaginationLimit(value string, defaultVal, maxVal int) int {
	limit, _ := strconv.Atoi(value)
	if limit <= 0 || limit > maxVal {
		return defaultVal
	}
	return limit
}

// parsePaginationOffset parses and validates the offset parameter
func (c *Controller) parsePaginationOffset(value string) int {
	offset, _ := strconv.Atoi(value)
	if offset < 0 {
		return 0
	}
	return offset
}

// getMergedThresholdData merges database and in-memory threshold data
func (c *Controller) getMergedThresholdData() map[string]*DynamicThresholdResponse {
	thresholdMap := make(map[string]*DynamicThresholdResponse)

	// Add database thresholds first
	c.addDatabaseThresholds(thresholdMap)

	// Override/add with in-memory data (more current)
	c.addMemoryThresholds(thresholdMap)

	return thresholdMap
}

// addDatabaseThresholds adds thresholds from the database to the map
func (c *Controller) addDatabaseThresholds(thresholdMap map[string]*DynamicThresholdResponse) {
	dbThresholds, err := c.DS.GetAllDynamicThresholds()
	if err != nil {
		c.Debug("Failed to get dynamic thresholds from database: %v", err)
		return
	}

	now := time.Now()
	for i := range dbThresholds {
		dt := &dbThresholds[i]
		thresholdMap[dt.SpeciesName] = &DynamicThresholdResponse{
			SpeciesName:    dt.SpeciesName,
			ScientificName: dt.ScientificName,
			Level:          dt.Level,
			CurrentValue:   dt.CurrentValue,
			BaseThreshold:  dt.BaseThreshold,
			HighConfCount:  dt.HighConfCount,
			ExpiresAt:      dt.ExpiresAt,
			LastTriggered:  dt.LastTriggered,
			FirstCreated:   dt.FirstCreated,
			TriggerCount:   dt.TriggerCount,
			IsActive:       dt.ExpiresAt.After(now),
		}
	}
}

// addMemoryThresholds adds/updates thresholds from processor memory
func (c *Controller) addMemoryThresholds(thresholdMap map[string]*DynamicThresholdResponse) {
	memoryData := c.Processor.GetDynamicThresholdData()
	baseThreshold := c.Settings.BirdNET.Threshold

	for _, dt := range memoryData {
		if existing, exists := thresholdMap[dt.SpeciesName]; exists {
			// Update with in-memory values
			existing.Level = dt.Level
			existing.CurrentValue = dt.CurrentValue
			existing.HighConfCount = dt.HighConfCount
			existing.ExpiresAt = dt.ExpiresAt
			existing.IsActive = dt.IsActive
			// Update scientific name if memory has it and existing doesn't
			if existing.ScientificName == "" && dt.ScientificName != "" {
				existing.ScientificName = dt.ScientificName
			}
		} else {
			// Add new entry from memory
			thresholdMap[dt.SpeciesName] = &DynamicThresholdResponse{
				SpeciesName:    dt.SpeciesName,
				ScientificName: dt.ScientificName,
				Level:          dt.Level,
				CurrentValue:   dt.CurrentValue,
				BaseThreshold:  float64(baseThreshold),
				HighConfCount:  dt.HighConfCount,
				ExpiresAt:      dt.ExpiresAt,
				IsActive:       dt.IsActive,
			}
		}
	}
}

// paginateThresholds applies offset and limit to the threshold slice
func (c *Controller) paginateThresholds(result []DynamicThresholdResponse, offset, limit int) []DynamicThresholdResponse {
	total := len(result)
	if offset >= total {
		return []DynamicThresholdResponse{}
	}
	end := min(offset+limit, total)
	return result[offset:end]
}

// GetDynamicThresholdStats returns aggregate statistics about dynamic thresholds
// GET /api/v2/dynamic-thresholds/stats
func (c *Controller) GetDynamicThresholdStats(ctx echo.Context) error {
	totalCount, activeCount, atMinimumCount, levelDist, err := c.DS.GetDynamicThresholdStats()
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get threshold statistics", http.StatusInternalServerError)
	}

	// Convert level distribution map to slice
	distribution := make([]LevelStatItem, 0, len(levelDist))
	for level := 0; level <= 3; level++ {
		distribution = append(distribution, LevelStatItem{
			Level: level,
			Count: levelDist[level],
		})
	}

	return ctx.JSON(http.StatusOK, ThresholdStatsResponse{
		TotalCount:        totalCount,
		ActiveCount:       activeCount,
		AtMinimumCount:    atMinimumCount,
		LevelDistribution: distribution,
		ValidHours:        c.Settings.Realtime.DynamicThreshold.ValidHours,
		MinThreshold:      c.Settings.Realtime.DynamicThreshold.Min,
	})
}

// GetDynamicThreshold returns a single dynamic threshold by species name
// GET /api/v2/dynamic-thresholds/:species
func (c *Controller) GetDynamicThreshold(ctx echo.Context) error {
	species, err := url.PathUnescape(ctx.Param("species"))
	if err != nil {
		return c.HandleError(ctx, errors.Newf("invalid species parameter").
			Category(errors.CategoryValidation).
			Component("api-dynamic-thresholds").
			Build(), "Invalid species parameter", http.StatusBadRequest)
	}

	if species == "" {
		return c.HandleError(ctx, errors.Newf("species parameter is required").
			Category(errors.CategoryValidation).
			Component("api-dynamic-thresholds").
			Build(), "Missing species parameter", http.StatusBadRequest)
	}

	// Try to get from database
	dt, err := c.DS.GetDynamicThreshold(species)
	if err != nil {
		// Check if it's a not-found error
		var enhancedErr *errors.EnhancedError
		if errors.As(err, &enhancedErr) && enhancedErr.Category == errors.CategoryNotFound {
			return c.HandleError(ctx, err, "Threshold not found", http.StatusNotFound)
		}
		return c.HandleError(ctx, err, "Failed to get threshold", http.StatusInternalServerError)
	}

	response := DynamicThresholdResponse{
		SpeciesName:    dt.SpeciesName,
		ScientificName: dt.ScientificName,
		Level:          dt.Level,
		CurrentValue:   dt.CurrentValue,
		BaseThreshold:  dt.BaseThreshold,
		HighConfCount:  dt.HighConfCount,
		ExpiresAt:      dt.ExpiresAt,
		LastTriggered:  dt.LastTriggered,
		FirstCreated:   dt.FirstCreated,
		TriggerCount:   dt.TriggerCount,
		IsActive:       dt.ExpiresAt.After(time.Now()),
	}

	// Try to get more current data from processor memory
	if c.Processor != nil {
		memoryData := c.Processor.GetDynamicThresholdData()
		for _, md := range memoryData {
			if md.SpeciesName != species {
				continue
			}
			response.Level = md.Level
			response.CurrentValue = md.CurrentValue
			response.HighConfCount = md.HighConfCount
			response.ExpiresAt = md.ExpiresAt
			response.IsActive = md.IsActive
			// Update scientific name if memory has it and existing doesn't
			if response.ScientificName == "" && md.ScientificName != "" {
				response.ScientificName = md.ScientificName
			}
			break
		}
	}

	return ctx.JSON(http.StatusOK, response)
}

// GetThresholdEvents returns event history for a specific species
// GET /api/v2/dynamic-thresholds/:species/events?limit=10
func (c *Controller) GetThresholdEvents(ctx echo.Context) error {
	species, err := url.PathUnescape(ctx.Param("species"))
	if err != nil {
		return c.HandleError(ctx, errors.Newf("invalid species parameter").
			Category(errors.CategoryValidation).
			Component("api-dynamic-thresholds").
			Build(), "Invalid species parameter", http.StatusBadRequest)
	}

	if species == "" {
		return c.HandleError(ctx, errors.Newf("species parameter is required").
			Category(errors.CategoryValidation).
			Component("api-dynamic-thresholds").
			Build(), "Missing species parameter", http.StatusBadRequest)
	}

	// Parse limit parameter
	limit := c.parsePaginationLimit(ctx.QueryParam("limit"), defaultEventLimit, maxEventLimit)

	events, err := c.DS.GetThresholdEvents(species, limit)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get threshold events", http.StatusInternalServerError)
	}

	// Convert to response format
	response := make([]ThresholdEventResponse, len(events))
	for i, e := range events {
		response[i] = ThresholdEventResponse{
			ID:            e.ID,
			SpeciesName:   e.SpeciesName,
			PreviousLevel: e.PreviousLevel,
			NewLevel:      e.NewLevel,
			PreviousValue: e.PreviousValue,
			NewValue:      e.NewValue,
			ChangeReason:  e.ChangeReason,
			Confidence:    e.Confidence,
			CreatedAt:     e.CreatedAt,
		}
	}

	return ctx.JSON(http.StatusOK, map[string]any{
		"data":    response,
		"species": species,
		"limit":   limit,
	})
}

// ResetDynamicThreshold resets a single species threshold
// DELETE /api/v2/dynamic-thresholds/:species
func (c *Controller) ResetDynamicThreshold(ctx echo.Context) error {
	// Check if processor is available
	if c.Processor == nil {
		return c.HandleError(ctx, errors.Newf("processor not available").
			Category(errors.CategorySystem).
			Component("api-dynamic-thresholds").
			Build(), "Processor not available", http.StatusServiceUnavailable)
	}

	species, err := url.PathUnescape(ctx.Param("species"))
	if err != nil {
		return c.HandleError(ctx, errors.Newf("invalid species parameter").
			Category(errors.CategoryValidation).
			Component("api-dynamic-thresholds").
			Build(), "Invalid species parameter", http.StatusBadRequest)
	}

	if species == "" {
		return c.HandleError(ctx, errors.Newf("species parameter is required").
			Category(errors.CategoryValidation).
			Component("api-dynamic-thresholds").
			Build(), "Missing species parameter", http.StatusBadRequest)
	}

	// Use processor's reset method which handles both memory and database
	if err := c.Processor.ResetDynamicThreshold(species); err != nil {
		return c.HandleError(ctx, err, "Failed to reset threshold", http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, map[string]any{
		"success": true,
		"message": "Threshold reset successfully",
		"species": species,
	})
}

// ResetAllDynamicThresholds resets all dynamic thresholds
// DELETE /api/v2/dynamic-thresholds?confirm=true
func (c *Controller) ResetAllDynamicThresholds(ctx echo.Context) error {
	// Check if processor is available
	if c.Processor == nil {
		return c.HandleError(ctx, errors.Newf("processor not available").
			Category(errors.CategorySystem).
			Component("api-dynamic-thresholds").
			Build(), "Processor not available", http.StatusServiceUnavailable)
	}

	// Require confirmation query parameter for safety
	confirm := ctx.QueryParam("confirm")
	if confirm != "true" {
		return c.HandleError(ctx, errors.Newf("confirmation required").
			Category(errors.CategoryValidation).
			Component("api-dynamic-thresholds").
			Build(), "Must include ?confirm=true query parameter", http.StatusBadRequest)
	}

	// Use processor's reset all method which handles both memory and database
	count, err := c.Processor.ResetAllDynamicThresholds()
	if err != nil {
		return c.HandleError(ctx, err, "Failed to reset all thresholds", http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, map[string]any{
		"success": true,
		"message": "All thresholds reset successfully",
		"count":   count,
	})
}

