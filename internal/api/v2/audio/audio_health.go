package audio

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/audiocore"
)

// AudioHealthResponse wraps the per-source health snapshots.
type AudioHealthResponse struct {
	Sources []audiocore.SourceHealthSnapshot `json:"sources"`
}

// RegisterAudioHealthRoutes registers the audio liveness health endpoints.
func (c *Handler) RegisterAudioHealthRoutes(g *echo.Group) {
	g.GET("/health/audio", c.GetAudioHealth, c.AuthMiddleware)
}

// SetAudioWatchdog injects the liveness watchdog for the health endpoint.
func (c *Handler) SetAudioWatchdog(w *audiocore.LivenessWatchdog) {
	c.AudioWatchdog.Store(w)
}

// GetAudioHealth returns the current liveness state for all monitored audio sources.
func (c *Handler) GetAudioHealth(ctx echo.Context) error {
	w := c.AudioWatchdog.Load()
	if w == nil {
		return ctx.JSON(http.StatusOK, AudioHealthResponse{
			Sources: []audiocore.SourceHealthSnapshot{},
		})
	}

	return ctx.JSON(http.StatusOK, AudioHealthResponse{
		Sources: w.Snapshot(),
	})
}
