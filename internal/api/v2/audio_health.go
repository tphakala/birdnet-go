package api

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/audiocore"
)

// AudioHealthResponse wraps the per-source health snapshots.
type AudioHealthResponse struct {
	Sources []audiocore.SourceHealthSnapshot `json:"sources"`
}

// initAudioHealthRoutes registers the audio liveness health endpoints.
func (c *Controller) initAudioHealthRoutes() {
	c.Group.GET("/health/audio", c.GetAudioHealth, c.authMiddleware)
}

// SetAudioWatchdog injects the liveness watchdog for the health endpoint.
func (c *Controller) SetAudioWatchdog(w *audiocore.LivenessWatchdog) {
	c.audioWatchdog.Store(w)
}

// GetAudioHealth returns the current liveness state for all monitored audio sources.
func (c *Controller) GetAudioHealth(ctx echo.Context) error {
	w := c.audioWatchdog.Load()
	if w == nil {
		return ctx.JSON(http.StatusOK, AudioHealthResponse{
			Sources: []audiocore.SourceHealthSnapshot{},
		})
	}

	return ctx.JSON(http.StatusOK, AudioHealthResponse{
		Sources: w.Snapshot(),
	})
}
