package api

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

// ActiveModelResponse describes a single loaded model for the /system/models endpoint.
type ActiveModelResponse struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	MetricKey    string  `json:"metric_key"`
	ChunkSeconds float64 `json:"chunk_seconds"`
	SampleRate   int     `json:"sample_rate"`
}

// sanitizeModelIDForMetric replaces any character that is not alphanumeric or
// underscore with an underscore so that the resulting string is safe to use as
// part of a Prometheus-style metric key.
func sanitizeModelIDForMetric(modelID string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, modelID)
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
			MetricKey:    "inference." + sanitizeModelIDForMetric(infos[i].ID) + ".avg_ms",
			ChunkSeconds: infos[i].Spec.ClipLength.Seconds(),
			SampleRate:   infos[i].Spec.SampleRate,
		})
	}

	return ctx.JSON(http.StatusOK, models)
}
