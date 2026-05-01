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

// ListAudioSources handles GET /api/v2/system/audio/sources.
// Returns all active audio sources from the engine registry (sound cards + streams).
func (c *Controller) ListAudioSources(ctx echo.Context) error {
	c.logInfoIfEnabled("Listing audio sources",
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	resp := AudioSourceListResponse{Sources: []AudioSourceInfo{}}

	if c.engine == nil {
		return ctx.JSON(http.StatusOK, resp)
	}

	registry := c.engine.Registry()
	if registry == nil {
		return ctx.JSON(http.StatusOK, resp)
	}

	for _, src := range registry.List() {
		resp.Sources = append(resp.Sources, AudioSourceInfo{
			ID:    src.ID,
			Name:  src.DisplayName,
			Type:  string(src.Type),
			State: src.State.String(),
		})
	}

	c.logInfoIfEnabled("Audio sources listed",
		logger.Int("count", len(resp.Sources)),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	return ctx.JSON(http.StatusOK, resp)
}

// isStreamSourceType returns true for source types that represent network streams.
func isStreamSourceType(t string) bool {
	switch audiocore.SourceType(t) {
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

// ListStreamSources handles GET /api/v2/streams/sources.
// Returns only stream-type audio sources (RTSP, HTTP, HLS, RTMP, UDP).
func (c *Controller) ListStreamSources(ctx echo.Context) error {
	c.logInfoIfEnabled("Listing stream sources",
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	resp := AudioSourceListResponse{Sources: []AudioSourceInfo{}}

	if c.engine == nil {
		return ctx.JSON(http.StatusOK, resp)
	}

	registry := c.engine.Registry()
	if registry == nil {
		return ctx.JSON(http.StatusOK, resp)
	}

	for _, src := range registry.List() {
		if !isStreamSourceType(string(src.Type)) {
			continue
		}
		resp.Sources = append(resp.Sources, AudioSourceInfo{
			ID:    src.ID,
			Name:  src.DisplayName,
			Type:  string(src.Type),
			State: src.State.String(),
		})
	}

	c.logInfoIfEnabled("Stream sources listed",
		logger.Int("count", len(resp.Sources)),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	return ctx.JSON(http.StatusOK, resp)
}
