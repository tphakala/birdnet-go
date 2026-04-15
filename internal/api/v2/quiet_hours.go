// internal/api/v2/quiet_hours.go
package api

import (
	"fmt"
	"maps"
	"net/http"
	"slices"

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

// GetQuietHoursStatus returns the current quiet hours suppression state for
// all sources. Unauthenticated (guest) requests receive opaque stream keys
// ("stream-1", "stream-2", ...) instead of the raw per-stream URL so that
// anonymous dashboard viewers cannot enumerate camera hostnames/ports. The
// preserved true/false values still let the dashboard "Currently Hearing"
// indicator count active suppressions. Authenticated requests (e.g. the
// StreamManager settings form) continue to receive the sanitized URL map.
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

	guest := c.authService != nil && !c.authService.IsAuthenticated(ctx)

	// Get sound card suppression state
	if scheduler != nil {
		response.SoundCardSuppressed = scheduler.IsSoundCardSuppressed()
		response.SuppressedStreams = buildSuppressedStreamsPayload(
			scheduler.GetSuppressedStreams(), guest)
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

// buildSuppressedStreamsPayload returns a map representing per-stream
// suppression state. For authenticated callers the keys are sanitized URLs
// (credentials stripped). For unauthenticated callers the keys are opaque
// placeholders like "stream-1" so that host/port information cannot be
// reconstructed from the response. URLs are sorted for stable opaque ordering.
func buildSuppressedStreamsPayload(raw map[string]bool, guest bool) map[string]bool {
	out := make(map[string]bool, len(raw))
	if !guest {
		for url, suppressed := range raw {
			out[privacy.SanitizeStreamUrl(url)] = suppressed
		}
		return out
	}
	// Deterministic order so that "stream-N" aliases are stable within a
	// single response and across repeated polls (raw keys live in the
	// scheduler, they do not change between calls).
	urls := slices.Sorted(maps.Keys(raw))
	for i, url := range urls {
		out[fmt.Sprintf("stream-%d", i+1)] = raw[url]
	}
	return out
}
