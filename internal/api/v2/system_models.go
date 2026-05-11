package api

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/classifier/inferencestats"
)

// ActiveModelResponse describes a single loaded model for the /system/models endpoint.
type ActiveModelResponse struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	MetricKey    string  `json:"metric_key"`
	ChunkSeconds float64 `json:"chunk_seconds"`
	SampleRate   int     `json:"sample_rate"`
}

// GetActiveModels returns metadata for all currently loaded models.
// GET /api/v2/system/models
func (c *Controller) GetActiveModels(ctx echo.Context) error {
	if c.ModelManager == nil {
		return ctx.JSON(http.StatusOK, []ActiveModelResponse{})
	}

	infos := c.ModelManager.ModelInfos()
	if infos == nil {
		return ctx.JSON(http.StatusOK, []ActiveModelResponse{})
	}

	models := make([]ActiveModelResponse, 0, len(infos))
	for i := range infos {
		models = append(models, ActiveModelResponse{
			ID:           infos[i].ID,
			Name:         infos[i].Name,
			MetricKey:    inferencestats.MetricKey(infos[i].ID),
			ChunkSeconds: infos[i].Spec.ClipLength.Seconds(),
			SampleRate:   infos[i].Spec.SampleRate,
		})
	}

	return ctx.JSON(http.StatusOK, models)
}
