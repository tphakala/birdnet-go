// audio_sources.go - Handlers for audio source listing endpoints.
package api

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// AudioSourceInfo represents a single audio source in API responses.
type AudioSourceInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	State string `json:"state"`
}

// AudioSourceListResponse is the response for the audio sources listing endpoints.
type AudioSourceListResponse struct {
	Sources []AudioSourceInfo `json:"sources"`
}

// toAudioSourceInfo converts an audiocore.AudioSource to the API response type.
func toAudioSourceInfo(src *audiocore.AudioSource) AudioSourceInfo {
	return AudioSourceInfo{
		ID:    src.ID,
		Name:  src.DisplayName,
		Type:  string(src.Type),
		State: src.State.String(),
	}
}

// isStreamSourceType returns true for source types that represent network streams.
func isStreamSourceType(t audiocore.SourceType) bool {
	switch t {
	case audiocore.SourceTypeRTSP,
		audiocore.SourceTypeHTTP,
		audiocore.SourceTypeHLS,
		audiocore.SourceTypeRTMP,
		audiocore.SourceTypeUDP:
		return true
	default:
		return false
	}
}

// listSources is the shared implementation for source listing endpoints.
// When filter is nil, all sources are included. When anonymize is true,
// source names are replaced with anonymized values for unauthenticated
// clients, matching the behavior of the audio-level SSE stream.
func (c *Controller) listSources(ctx echo.Context, label string, filter func(audiocore.SourceType) bool, anonymize bool) error {
	c.logInfoIfEnabled("Listing "+label,
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	resp := AudioSourceListResponse{Sources: []AudioSourceInfo{}}

	eng := c.engine.Load()
	if eng == nil {
		return ctx.JSON(http.StatusOK, resp)
	}

	registry := eng.Registry()
	if registry == nil {
		return ctx.JSON(http.StatusOK, resp)
	}

	sources := registry.List()
	isAuthenticated := !anonymize || c.isClientAuthenticated(ctx)
	resp.Sources = make([]AudioSourceInfo, 0, len(sources))
	for _, src := range sources {
		if filter != nil && !filter(src.Type) {
			continue
		}
		info := toAudioSourceInfo(src)
		if !isAuthenticated {
			info.Name = c.getAnonymizedSourceName(src)
		}
		resp.Sources = append(resp.Sources, info)
	}

	c.logInfoIfEnabled(label+" listed",
		logger.Int("count", len(resp.Sources)),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	return ctx.JSON(http.StatusOK, resp)
}

// ListAudioSources handles GET /api/v2/system/audio/sources.
// Returns all active audio sources from the engine registry (sound cards + streams).
func (c *Controller) ListAudioSources(ctx echo.Context) error {
	return c.listSources(ctx, "audio sources", nil, false)
}

// ListStreamSources handles GET /api/v2/streams/sources.
// Returns only stream-type audio sources (RTSP, HTTP, HLS, RTMP, UDP).
// Source names are anonymized for unauthenticated clients.
func (c *Controller) ListStreamSources(ctx echo.Context) error {
	return c.listSources(ctx, "stream sources", isStreamSourceType, true)
}
