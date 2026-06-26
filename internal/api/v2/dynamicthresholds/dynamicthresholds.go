// Package dynamicthresholds is the api/v2 dynamic-thresholds domain handler. It
// owns the /api/v2/dynamic-thresholds* endpoints (BG-59: reading the merged
// runtime threshold data from the database and processor memory, aggregate
// stats, per-species lookups and event history, plus the protected single and
// bulk reset controls). The Handler embeds *apicore.Core by pointer so the
// shared dependencies and helpers (DS, Processor, AuthMiddleware, HandleError,
// HandleErrorWithNotFound, CurrentSettings, Debug, and the v2 route group)
// promote onto it. The domain needs only the shared core; it injects no
// facade-owned dependencies.
package dynamicthresholds

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Pagination defaults for dynamic threshold endpoints
const (
	defaultThresholdLimit = 50
	maxThresholdLimit     = 250
	defaultEventLimit     = 10
	maxEventLimit         = 100
)

// Handler serves the api/v2 dynamic-thresholds endpoints. It embeds the shared
// *apicore.Core by pointer (never copied by value) so the shared state and
// helpers promote onto the handler methods.
type Handler struct {
	*apicore.Core
}

// New builds a dynamic-thresholds Handler around the shared core. The domain has
// no facade-owned dependencies; everything it needs promotes from the core.
func New(core *apicore.Core) *Handler {
	return &Handler{Core: core}
}

// RegisterRoutes wires the dynamic-threshold endpoints onto the v2 group,
// preserving the exact routes, order, and per-route middleware of the former
// initDynamicThresholdRoutes.
func (c *Handler) RegisterRoutes(g *echo.Group) {
	// Public endpoints for reading threshold data
	g.GET("/dynamic-thresholds", c.GetDynamicThresholds)
	g.GET("/dynamic-thresholds/stats", c.GetDynamicThresholdStats)
	g.GET("/dynamic-thresholds/:species", c.GetDynamicThreshold)
	g.GET("/dynamic-thresholds/:species/events", c.GetThresholdEvents)

	// Protected endpoints for modifying thresholds (require authentication)
	g.DELETE("/dynamic-thresholds/:species", c.ResetDynamicThreshold, c.AuthMiddleware)
	g.DELETE("/dynamic-thresholds", c.ResetAllDynamicThresholds, c.AuthMiddleware)
}

// DynamicThresholdResponse represents a single dynamic threshold for API responses
type DynamicThresholdResponse struct {
	SpeciesName    string    `json:"speciesName"`
	ScientificName string    `json:"scientificName"`
	ModelName      string    `json:"modelName,omitempty"`
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
	ModelName     string    `json:"modelName,omitempty"`
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
	ValidHours        int             `json:"validHours"`   // Configured threshold validity period in hours
	MinThreshold      float64         `json:"minThreshold"` // Configured minimum threshold value
}

// LevelStatItem represents count for a specific level
type LevelStatItem struct {
	Level int   `json:"level"`
	Count int64 `json:"count"`
}

// parseSpeciesParam extracts and validates the species parameter from the request.
// Returns the species name and nil on success, or empty string and error on failure.
func (c *Handler) parseSpeciesParam(ctx echo.Context) (string, error) {
	species, err := url.PathUnescape(ctx.Param("species"))
	if err != nil {
		return "", c.HandleError(ctx, errors.Newf("invalid species parameter").
			Category(errors.CategoryValidation).
			Component("api-dynamic-thresholds").
			Build(), "Invalid species parameter", http.StatusBadRequest)
	}
	if species == "" {
		return "", c.HandleError(ctx, errors.Newf("species parameter is required").
			Category(errors.CategoryValidation).
			Component("api-dynamic-thresholds").
			Build(), "Missing species parameter", http.StatusBadRequest)
	}
	return species, nil
}

// requireProcessor snapshots c.Processor and returns it if non-nil.
// Callers must use the returned pointer for all subsequent accesses
// to avoid a TOCTOU race between the nil check and usage.
func (c *Handler) requireProcessor(ctx echo.Context) (*processor.Processor, error) {
	proc := c.Processor
	if proc == nil {
		err := errors.Newf("processor not available").
			Category(errors.CategorySystem).
			Component("api-dynamic-thresholds").
			Build()
		_ = c.HandleError(ctx, err, "Processor not available", http.StatusServiceUnavailable)
		return nil, err
	}
	return proc, nil
}

// applyMemoryOverlay updates a threshold response with in-memory data.
// This centralizes the logic for overlaying current processor state onto
// database records, ensuring API responses reflect the most current state.
func applyMemoryOverlay(response *DynamicThresholdResponse, level, highConfCount int, currentValue float64, expiresAt time.Time, isActive bool, scientificName string) {
	response.Level = level
	response.CurrentValue = currentValue
	response.HighConfCount = highConfCount
	response.ExpiresAt = expiresAt
	response.IsActive = isActive
	// Use scientific name from memory if it's missing in the database response.
	if response.ScientificName == "" && scientificName != "" {
		response.ScientificName = scientificName
	}
}

// GetDynamicThresholds returns all dynamic thresholds with optional pagination
// GET /api/v2/dynamic-thresholds?limit=50&offset=0
func (c *Handler) GetDynamicThresholds(ctx echo.Context) error {
	// Parse pagination parameters
	limit := apicore.ParsePaginationLimit(ctx.QueryParam("limit"), defaultThresholdLimit, maxThresholdLimit)
	offset := parsePaginationOffset(ctx.QueryParam("offset"))

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

// parsePaginationOffset parses and validates the offset parameter. The offset is
// dynamic-thresholds-specific (no other domain paginates by offset), so it stays
// local; the shared limit parser lives in apicore as ParsePaginationLimit. It is a
// package-level function (no receiver state needed), matching the apicore parser
// convention.
func parsePaginationOffset(value string) int {
	offset, _ := strconv.Atoi(value)
	if offset < 0 {
		return 0
	}
	return offset
}

// getMergedThresholdData merges database and in-memory threshold data
func (c *Handler) getMergedThresholdData() map[string]*DynamicThresholdResponse {
	thresholdMap := make(map[string]*DynamicThresholdResponse)

	// Add database thresholds first
	c.addDatabaseThresholds(thresholdMap)

	// Override/add with in-memory data (more current)
	c.addMemoryThresholds(thresholdMap)

	return thresholdMap
}

// addDatabaseThresholds adds thresholds from the database to the map.
// Map keys are normalized to lowercase to ensure case-insensitive merging
// with in-memory data (which uses lowercase species names).
func (c *Handler) addDatabaseThresholds(thresholdMap map[string]*DynamicThresholdResponse) {
	dbThresholds, err := c.DS.GetAllDynamicThresholds()
	if err != nil {
		c.Debug("Failed to get dynamic thresholds from database: %v", err)
		return
	}

	now := time.Now()
	for i := range dbThresholds {
		dt := &dbThresholds[i]
		// Use composite key to prevent overwrite when multiple models have thresholds for the same species.
		mapKey := strings.ToLower(dt.ModelName) + ":" + strings.ToLower(dt.SpeciesName)
		thresholdMap[mapKey] = &DynamicThresholdResponse{
			SpeciesName:    dt.SpeciesName,
			ScientificName: dt.ScientificName,
			ModelName:      dt.ModelName,
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

// addMemoryThresholds adds/updates thresholds from processor memory.
// If processor is unavailable, this function returns early without modification.
func (c *Handler) addMemoryThresholds(thresholdMap map[string]*DynamicThresholdResponse) {
	proc := c.Processor
	if proc == nil {
		return
	}
	memoryData := proc.GetDynamicThresholdData()
	baseThreshold := c.CurrentSettings().BirdNET.Threshold

	for _, dt := range memoryData {
		key := strings.ToLower(dt.ModelName) + ":" + strings.ToLower(dt.SpeciesName)
		if existing, exists := thresholdMap[key]; exists {
			// Update existing entry with in-memory values
			applyMemoryOverlay(existing, dt.Level, dt.HighConfCount, dt.CurrentValue, dt.ExpiresAt, dt.IsActive, dt.ScientificName)
		} else {
			// Add new entry from memory
			thresholdMap[key] = &DynamicThresholdResponse{
				SpeciesName:    dt.SpeciesName,
				ScientificName: dt.ScientificName,
				ModelName:      dt.ModelName,
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
func (c *Handler) paginateThresholds(result []DynamicThresholdResponse, offset, limit int) []DynamicThresholdResponse {
	total := len(result)
	if offset >= total {
		return []DynamicThresholdResponse{}
	}
	end := min(offset+limit, total)
	return result[offset:end]
}

// GetDynamicThresholdStats returns aggregate statistics about dynamic thresholds
// GET /api/v2/dynamic-thresholds/stats
func (c *Handler) GetDynamicThresholdStats(ctx echo.Context) error {
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

	settings := c.CurrentSettings()
	return ctx.JSON(http.StatusOK, ThresholdStatsResponse{
		TotalCount:        totalCount,
		ActiveCount:       activeCount,
		AtMinimumCount:    atMinimumCount,
		LevelDistribution: distribution,
		ValidHours:        settings.Realtime.DynamicThreshold.ValidHours,
		MinThreshold:      settings.Realtime.DynamicThreshold.Min,
	})
}

// GetDynamicThreshold returns a single dynamic threshold by species name
// GET /api/v2/dynamic-thresholds/:species
func (c *Handler) GetDynamicThreshold(ctx echo.Context) error {
	species, err := c.parseSpeciesParam(ctx)
	if err != nil {
		return err
	}

	// Try to get from database; optional model query param scopes the lookup
	model := ctx.QueryParam("model")
	dt, err := c.DS.GetDynamicThreshold(species, model)
	if err != nil {
		return c.HandleErrorWithNotFound(ctx, err, "Threshold not found", "Failed to get threshold")
	}

	response := DynamicThresholdResponse{
		SpeciesName:    dt.SpeciesName,
		ScientificName: dt.ScientificName,
		ModelName:      dt.ModelName,
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

	// Try to get more current data from processor memory.
	// Snapshot c.Processor to avoid a TOCTOU race between the nil check and usage.
	proc := c.Processor
	if proc != nil {
		for _, md := range proc.GetDynamicThresholdData() {
			if strings.EqualFold(md.SpeciesName, species) {
				applyMemoryOverlay(&response, md.Level, md.HighConfCount, md.CurrentValue, md.ExpiresAt, md.IsActive, md.ScientificName)
				break
			}
		}
	}

	return ctx.JSON(http.StatusOK, response)
}

// GetThresholdEvents returns event history for a specific species
// GET /api/v2/dynamic-thresholds/:species/events?limit=10
func (c *Handler) GetThresholdEvents(ctx echo.Context) error {
	species, err := c.parseSpeciesParam(ctx)
	if err != nil {
		return err
	}

	// Parse limit parameter
	limit := apicore.ParsePaginationLimit(ctx.QueryParam("limit"), defaultEventLimit, maxEventLimit)

	events, err := c.DS.GetThresholdEvents(species, limit)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get threshold events", http.StatusInternalServerError)
	}

	// Convert to response format
	response := make([]ThresholdEventResponse, len(events))
	for i := range events {
		response[i] = ThresholdEventResponse{
			ID:            events[i].ID,
			SpeciesName:   events[i].SpeciesName,
			ModelName:     events[i].ModelName,
			PreviousLevel: events[i].PreviousLevel,
			NewLevel:      events[i].NewLevel,
			PreviousValue: events[i].PreviousValue,
			NewValue:      events[i].NewValue,
			ChangeReason:  events[i].ChangeReason,
			Confidence:    events[i].Confidence,
			CreatedAt:     events[i].CreatedAt,
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
func (c *Handler) ResetDynamicThreshold(ctx echo.Context) error {
	proc, err := c.requireProcessor(ctx)
	if err != nil {
		return err
	}

	species, err := c.parseSpeciesParam(ctx)
	if err != nil {
		return err
	}

	// Use processor's reset method which handles both memory and database
	if err := proc.ResetDynamicThreshold(species); err != nil {
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
func (c *Handler) ResetAllDynamicThresholds(ctx echo.Context) error {
	proc, err := c.requireProcessor(ctx)
	if err != nil {
		return err
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
	count, err := proc.ResetAllDynamicThresholds()
	if err != nil {
		return c.HandleError(ctx, err, "Failed to reset all thresholds", http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, map[string]any{
		"success": true,
		"message": "All thresholds reset successfully",
		"count":   count,
	})
}
