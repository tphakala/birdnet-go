// internal/api/v2/quiet_hours.go
package api

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/audiocore/schedule"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// QuietHoursStatusResponse represents the current quiet hours suppression state.
type QuietHoursStatusResponse struct {
	// AnyActive is true if any source (sound card or stream) is currently suppressed.
	AnyActive bool `json:"anyActive"`
	// SoundCardSuppressed is true if the sound card is suppressed by quiet hours.
	SoundCardSuppressed bool `json:"soundCardSuppressed"`
	// SuppressedStreams maps stream URLs to their suppression state.
	SuppressedStreams map[string]bool `json:"suppressedStreams"`
}

// initQuietHoursRoutes registers quiet hours API routes.
func (c *Controller) initQuietHoursRoutes() {
	// Public read-only endpoint: the dashboard's "Currently Hearing" card
	// polls this to show whether any source is in a quiet-hours window.
	// Stream URLs are sanitized via privacy.SanitizeStreamUrl before the
	// response is serialized, so raw RTSP credentials are never leaked.
	// Mirrors the PR #2763 pattern for other dashboard read-only endpoints.
	c.Group.GET("/streams/quiet-hours/status", c.GetQuietHoursStatus)
}

// GetQuietHoursStatus returns the current quiet hours suppression state for all sources.
func (c *Controller) GetQuietHoursStatus(ctx echo.Context) error {
	settings := c.Settings
	var scheduler *schedule.QuietHoursScheduler
	if c.engine != nil {
		scheduler = c.engine.Scheduler()
	}

	response := QuietHoursStatusResponse{
		SuppressedStreams: make(map[string]bool),
	}

	// Check if quiet hours is configured at all
	if settings == nil {
		return ctx.JSON(http.StatusOK, response)
	}

	// Get sound card suppression state
	if scheduler != nil {
		response.SoundCardSuppressed = scheduler.IsSoundCardSuppressed()

		// Sanitize stream URLs to strip credentials before returning in API response
		rawStreams := scheduler.GetSuppressedStreams()
		for url, suppressed := range rawStreams {
			response.SuppressedStreams[privacy.SanitizeStreamUrl(url)] = suppressed
		}
	}

	// Determine if any source is currently suppressed
	if response.SoundCardSuppressed {
		response.AnyActive = true
	} else {
		for _, suppressed := range response.SuppressedStreams {
			if suppressed {
				response.AnyActive = true
				break
			}
		}
	}

	return ctx.JSON(http.StatusOK, response)
}
