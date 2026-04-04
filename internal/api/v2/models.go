package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/classifier"
)

// ModelListItem represents a model in the API response.
type ModelListItem struct {
	ID   string `json:"id"`   // Config alias (e.g., "birdnet", "perch_v2")
	Name string `json:"name"` // Display name (e.g., "BirdNET v2.4 (TFLite)")
}

// initModelRoutes registers model-related API routes.
func (c *Controller) initModelRoutes() {
	c.Group.GET("/models", c.ListModels)
}

// ListModels returns classifier models that are enabled in the configuration.
func (c *Controller) ListModels(ctx echo.Context) error {
	// Build a set of enabled model config IDs for fast lookup.
	enabled := make(map[string]bool, len(c.Settings.Models.Enabled))
	for _, id := range c.Settings.Models.Enabled {
		enabled[strings.ToLower(id)] = true
	}

	models := make([]ModelListItem, 0, len(enabled))
	for id := range classifier.ModelRegistry {
		info := classifier.ModelRegistry[id]
		for _, alias := range info.ConfigAliases {
			if enabled[strings.ToLower(alias)] {
				models = append(models, ModelListItem{
					ID:   alias,
					Name: info.DisplayName(),
				})
				break // one entry per model
			}
		}
	}

	// Sort by ID for stable output.
	sort.Slice(models, func(i, j int) bool {
		return models[i].ID < models[j].ID
	})

	return ctx.JSON(http.StatusOK, models)
}
