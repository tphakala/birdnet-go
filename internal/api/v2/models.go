package api

import (
	"net/http"
	"sort"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/classifier"
)

// ModelListItem represents a model in the API response.
type ModelListItem struct {
	ID   string `json:"id"`   // Config alias (e.g., "birdnet", "perch_v2")
	Name string `json:"name"` // Display name (e.g., "BirdNET GLOBAL 6K V2.4")
}

// initModelRoutes registers model-related API routes.
func (c *Controller) initModelRoutes() {
	c.Group.GET("/models", c.ListModels)
}

// ListModels returns available classifier models from the registry.
func (c *Controller) ListModels(ctx echo.Context) error {
	// Pre-count total aliases across all registry entries for preallocation.
	totalAliases := 0
	for id := range classifier.ModelRegistry {
		totalAliases += len(classifier.ModelRegistry[id].ConfigAliases)
	}

	models := make([]ModelListItem, 0, totalAliases)
	for id := range classifier.ModelRegistry {
		info := classifier.ModelRegistry[id]
		for _, alias := range info.ConfigAliases {
			models = append(models, ModelListItem{
				ID:   alias,
				Name: info.Name,
			})
		}
	}

	// Sort by ID for stable output.
	sort.Slice(models, func(i, j int) bool {
		return models[i].ID < models[j].ID
	})

	return ctx.JSON(http.StatusOK, models)
}
