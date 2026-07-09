package specieslists

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/openfauna"
)

type Handler struct {
	*apicore.Core
	speciesListRepo       repository.SpeciesListRepository
	controlChan           chan<- string
	refreshEngineListsCB func(ctx context.Context) error
}

func New(core *apicore.Core, controlChan chan<- string, refreshCB func(ctx context.Context) error) *Handler {
	return &Handler{
		Core:                 core,
		controlChan:          controlChan,
		refreshEngineListsCB: refreshCB,
	}
}

func (c *Handler) requireV2(ctx echo.Context) error {
	return ctx.JSON(http.StatusConflict, map[string]any{
		"error":  "Enhanced database unavailable",
		"detail": "This operation requires the enhanced database schema, which is disabled or not yet initialized.",
	})
}

func (c *Handler) listsAvailable() bool {
	return c.V2Manager != nil && datastoreV2.IsEnhancedDatabase()
}

func (c *Handler) requireV2Middleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) error {
		if !c.listsAvailable() {
			return c.requireV2(ctx)
		}
		return next(ctx)
	}
}

func (c *Handler) RegisterRoutes(g *echo.Group) {
	if c.V2Manager != nil {
		// Initialize repository lazily from V2Manager
		c.speciesListRepo = repository.NewSpeciesListRepository(c.V2Manager.DB(), nil)
	}

	lists := g.Group("/species-lists", c.requireV2Middleware)

	// Public read endpoints
	lists.GET("", c.ListSpeciesLists)
	lists.GET("/:id", c.GetSpeciesList)

	// Protected endpoints
	protected := lists.Group("", c.AuthMiddleware)
	protected.POST("", c.CreateSpeciesList)
	protected.PUT("/:id", c.UpdateSpeciesList)
	protected.DELETE("/:id", c.DeleteSpeciesList)
}

func (c *Handler) triggerRebuildExtendedCapture() {
	if c.controlChan == nil {
		return
	}
	select {
	case c.controlChan <- "rebuild_extended_capture":
		apicore.GetLogger().Info("Control signal rebuild_extended_capture sent successfully")
	case <-c.Context().Done():
		apicore.GetLogger().Warn("Failed to send rebuild_extended_capture signal: server shutting down")
	default:
		apicore.GetLogger().Warn("Failed to send rebuild_extended_capture signal: channel full")
	}
}

func (c *Handler) refreshSpeciesLists(ctx echo.Context) {
	if c.refreshEngineListsCB != nil {
		if err := c.refreshEngineListsCB(ctx.Request().Context()); err != nil {
			c.LogErrorIfEnabled("failed to refresh alert engine species lists", logger.Error(err))
		}
	}
}

// validateSpeciesNames checks scientific names against the OpenFauna dataset and
// returns a list of names that were not recognized. The check is best-effort;
// unrecognized names are still stored (the user may know something OpenFauna
// does not). A single O(dataset) pass resolves all names at once.
func validateSpeciesNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	// LookupMeta does a single pass and returns metadata for names that exist.
	// Names absent from the result are unrecognized.
	var unrecognized []string
	for _, name := range names {
		if _, found := openfauna.LookupMeta(name); !found {
			unrecognized = append(unrecognized, name)
		}
	}
	return unrecognized
}

// speciesNames extracts the scientific names from a SpeciesList's members.
func speciesNames(list *entities.SpeciesList) []string {
	names := make([]string, 0, len(list.Members))
	for _, m := range list.Members {
		names = append(names, m.ScientificName)
	}
	return names
}

// ListSpeciesLists returns all species lists.
func (c *Handler) ListSpeciesLists(ctx echo.Context) error {
	lists, err := c.speciesListRepo.ListSpeciesLists(ctx.Request().Context())
	if err != nil {
		c.LogErrorIfEnabled("failed to list species lists", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to list species lists", http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, map[string]any{
		"lists": lists,
		"count": len(lists),
	})
}

// GetSpeciesList returns a single species list by ID.
func (c *Handler) GetSpeciesList(ctx echo.Context) error {
	idStr := ctx.Param("id")
	id64, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return c.HandleError(ctx, err, "Invalid species list ID", http.StatusBadRequest)
	}
	id := uint(id64)

	list, err := c.speciesListRepo.GetSpeciesList(ctx.Request().Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrSpeciesListNotFound) {
			return c.HandleError(ctx, err, "Species list not found", http.StatusNotFound)
		}
		c.LogErrorIfEnabled("failed to get species list", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to get species list", http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, list)
}

// CreateSpeciesList creates a new species list.
func (c *Handler) CreateSpeciesList(ctx echo.Context) error {
	var req struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Species     []string `json:"species"`
	}
	if err := ctx.Bind(&req); err != nil {
		return c.HandleError(ctx, err, "Malformed request body", http.StatusBadRequest)
	}

	if strings.TrimSpace(req.Name) == "" {
		return c.HandleError(ctx, nil, "Name is required", http.StatusBadRequest)
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(req.Name)), "yaml:") {
		return c.HandleError(ctx, nil, "Name cannot start with 'YAML:' (reserved for system lists)", http.StatusBadRequest)
	}
	if len(req.Name) > 255 {
		return c.HandleError(ctx, nil, "Name cannot exceed 255 characters", http.StatusBadRequest)
	}
	if len(req.Description) > 1000 {
		return c.HandleError(ctx, nil, "Description cannot exceed 1000 characters", http.StatusBadRequest)
	}

	list := &entities.SpeciesList{
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
	}
	seen := make(map[string]struct{})
	for _, sp := range req.Species {
		// Normalize to canonical lowercase scientific name (OpenFauna convention).
		spNorm := strings.ToLower(strings.TrimSpace(sp))
		if spNorm == "" {
			continue
		}
		if _, dup := seen[spNorm]; dup {
			continue
		}
		seen[spNorm] = struct{}{}
		list.Members = append(list.Members, entities.SpeciesListMember{
			ScientificName: spNorm,
		})
	}

	if err := c.speciesListRepo.CreateSpeciesList(ctx.Request().Context(), list); err != nil {
		c.LogErrorIfEnabled("failed to create species list", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to create species list", http.StatusInternalServerError)
	}

	c.triggerRebuildExtendedCapture()
	c.refreshSpeciesLists(ctx)

	response := map[string]any{"list": list}
	if warnings := validateSpeciesNames(speciesNames(list)); len(warnings) > 0 {
		response["warnings"] = map[string]any{
			"unrecognized_species": warnings,
			"message":             "Some species were not found in the OpenFauna dataset. They have been saved but may not match detections.",
		}
	}
	return ctx.JSON(http.StatusCreated, response)
}

// UpdateSpeciesList updates an existing species list.
func (c *Handler) UpdateSpeciesList(ctx echo.Context) error {
	idStr := ctx.Param("id")
	id64, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return c.HandleError(ctx, err, "Invalid species list ID", http.StatusBadRequest)
	}
	id := uint(id64)

	var req struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Species     []string `json:"species"`
	}
	if err := ctx.Bind(&req); err != nil {
		return c.HandleError(ctx, err, "Malformed request body", http.StatusBadRequest)
	}

	if strings.TrimSpace(req.Name) == "" {
		return c.HandleError(ctx, nil, "Name is required", http.StatusBadRequest)
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(req.Name)), "yaml:") {
		return c.HandleError(ctx, nil, "Name cannot start with 'YAML:' (reserved for system lists)", http.StatusBadRequest)
	}
	if len(req.Name) > 255 {
		return c.HandleError(ctx, nil, "Name cannot exceed 255 characters", http.StatusBadRequest)
	}
	if len(req.Description) > 1000 {
		return c.HandleError(ctx, nil, "Description cannot exceed 1000 characters", http.StatusBadRequest)
	}

	// Verify it exists first
	existing, err := c.speciesListRepo.GetSpeciesList(ctx.Request().Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrSpeciesListNotFound) {
			return c.HandleError(ctx, err, "Species list not found", http.StatusNotFound)
		}
		c.LogErrorIfEnabled("failed to verify species list existence", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to update species list", http.StatusInternalServerError)
	}
	if existing.IsSystem {
		return c.HandleError(ctx, nil, "System-configured species lists cannot be modified via the API", http.StatusForbidden)
	}

	list := &entities.SpeciesList{
		ID:          id,
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
	}
	seen := make(map[string]struct{})
	for _, sp := range req.Species {
		// Normalize to canonical lowercase scientific name (OpenFauna convention).
		spNorm := strings.ToLower(strings.TrimSpace(sp))
		if spNorm == "" {
			continue
		}
		if _, dup := seen[spNorm]; dup {
			continue
		}
		seen[spNorm] = struct{}{}
		list.Members = append(list.Members, entities.SpeciesListMember{
			ListID:         id,
			ScientificName: spNorm,
		})
	}

	if err := c.speciesListRepo.UpdateSpeciesList(ctx.Request().Context(), list); err != nil {
		c.LogErrorIfEnabled("failed to update species list", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to update species list", http.StatusInternalServerError)
	}

	c.triggerRebuildExtendedCapture()
	c.refreshSpeciesLists(ctx)

	response := map[string]any{"list": list}
	if warnings := validateSpeciesNames(speciesNames(list)); len(warnings) > 0 {
		response["warnings"] = map[string]any{
			"unrecognized_species": warnings,
			"message":             "Some species were not found in the OpenFauna dataset. They have been saved but may not match detections.",
		}
	}
	return ctx.JSON(http.StatusOK, response)
}

// DeleteSpeciesList deletes a species list.
func (c *Handler) DeleteSpeciesList(ctx echo.Context) error {
	idStr := ctx.Param("id")
	id64, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return c.HandleError(ctx, err, "Invalid species list ID", http.StatusBadRequest)
	}
	id := uint(id64)

	// Verify it exists first and check if it is system list
	existing, err := c.speciesListRepo.GetSpeciesList(ctx.Request().Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrSpeciesListNotFound) {
			return c.HandleError(ctx, err, "Species list not found", http.StatusNotFound)
		}
		c.LogErrorIfEnabled("failed to verify species list existence", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to delete species list", http.StatusInternalServerError)
	}
	if existing.IsSystem {
		return c.HandleError(ctx, nil, "System-configured species lists cannot be deleted", http.StatusForbidden)
	}

	if err := c.speciesListRepo.DeleteSpeciesList(ctx.Request().Context(), id); err != nil {
		if errors.Is(err, repository.ErrSpeciesListNotFound) {
			return c.HandleError(ctx, err, "Species list not found", http.StatusNotFound)
		}
		c.LogErrorIfEnabled("failed to delete species list", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to delete species list", http.StatusInternalServerError)
	}

	c.triggerRebuildExtendedCapture()
	c.refreshSpeciesLists(ctx)

	return ctx.NoContent(http.StatusNoContent)
}
