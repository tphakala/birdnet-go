// internal/api/v2/audio_level.go
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// Audio level SSE configuration constants
const (
	// Connection timeouts
	audioLevelMaxDuration      = 30 * time.Minute      // Maximum stream duration to prevent resource leaks
	audioLevelHeartbeatInterval = 10 * time.Second     // Heartbeat interval for keep-alive
	audioLevelActivityCheck    = 1 * time.Second       // Activity check interval for sources
	audioLevelInactivityThreshold = 15 * time.Second   // Threshold for marking sources as inactive
	audioLevelRateLimitUpdate  = 50 * time.Millisecond // Rate limit for sending updates

	// Buffer sizes
	audioLevelChannelBuffer = 100 // Buffer size for internal processing

	// Rate limits
	audioLevelRateLimitRequests = 10              // Rate limit requests per window
	audioLevelRateLimitWindow   = 1 * time.Minute // Rate limit time window

	// Endpoints
	audioLevelStreamEndpoint = "/api/v2/streams/audio-level"
)

// AudioLevelSSEData represents the audio level data sent via SSE
// This matches the v1 format for frontend compatibility
type AudioLevelSSEData struct {
	Type   string                            `json:"type"`
	Levels map[string]myaudio.AudioLevelData `json:"levels"`
}

// audioLevelManager manages audio level SSE connections
type audioLevelManager struct {
	// Active connections tracked per client IP to prevent duplicates
	activeConnections sync.Map
	// Total connection counter for monitoring
	totalConnections int64
	// RTSP anonymization map for non-authenticated users
	rtspAnonymMap map[string]string
	rtspAnonymMu  sync.RWMutex
}

// Global audio level manager instance
// TODO: Move to Controller struct during httpcontroller refactoring
var audioLevelMgr = &audioLevelManager{
	rtspAnonymMap: make(map[string]string),
}

// SetAudioLevelChan sets the audio level channel for the controller
// This should be called after controller initialization to connect
// the audio capture system to the SSE endpoint
func (c *Controller) SetAudioLevelChan(ch chan myaudio.AudioLevelData) {
	c.audioLevelChan = ch
	if c.apiLogger != nil {
		c.apiLogger.Info("Audio level channel connected to API v2 controller")
	}
}

// initAudioLevelRoutes registers audio level SSE endpoints
func (c *Controller) initAudioLevelRoutes() {
	// Create rate limiter for audio level SSE connections
	rateLimiterConfig := middleware.RateLimiterConfig{
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(
			middleware.RateLimiterMemoryStoreConfig{
				Rate:      audioLevelRateLimitRequests,
				ExpiresIn: audioLevelRateLimitWindow,
			},
		),
		IdentifierExtractor: middleware.DefaultRateLimiterConfig.IdentifierExtractor,
		ErrorHandler: func(context echo.Context, err error) error {
			return context.JSON(http.StatusTooManyRequests, map[string]string{
				"error": "Rate limit exceeded for audio level stream",
			})
		},
		DenyHandler: func(context echo.Context, identifier string, err error) error {
			return context.JSON(http.StatusTooManyRequests, map[string]string{
				"error": "Too many audio level stream connection attempts, please wait before trying again",
			})
		},
	}

	// Audio level SSE endpoint - public with rate limiting (matches v1 behavior)
	// Authentication is checked within the handler to control data anonymization
	c.Group.GET("/streams/audio-level", c.StreamAudioLevel, middleware.RateLimiterWithConfig(rateLimiterConfig))
}

// StreamAudioLevel handles SSE connections for real-time audio level streaming
// This provides simple audio level data (0-100 with clipping detection) for UI indicators
func (c *Controller) StreamAudioLevel(ctx echo.Context) error {
	clientIP := ctx.RealIP()

	// Check for duplicate connection from same IP
	if _, exists := audioLevelMgr.activeConnections.LoadOrStore(clientIP, time.Now()); exists {
		c.logAPIRequest(ctx, slog.LevelWarn, "Rejected duplicate audio level SSE connection",
			"reason", "already_connected")
		return ctx.JSON(http.StatusTooManyRequests, map[string]string{
			"error": "Only one audio level stream connection per client is allowed",
		})
	}

	// Cleanup connection on exit
	defer func() {
		audioLevelMgr.activeConnections.Delete(clientIP)
		atomic.AddInt64(&audioLevelMgr.totalConnections, -1)
	}()

	// Track connection
	atomic.AddInt64(&audioLevelMgr.totalConnections, 1)

	// Track metrics if available
	if c.metrics != nil && c.metrics.HTTP != nil {
		connectionStartTime := time.Now()
		c.metrics.HTTP.SSEConnectionStarted(audioLevelStreamEndpoint)
		defer func() {
			duration := time.Since(connectionStartTime).Seconds()
			closeReason := "closed"
			if ctx.Request().Context().Err() == context.DeadlineExceeded {
				closeReason = "timeout"
			} else if ctx.Request().Context().Err() == context.Canceled {
				closeReason = "canceled"
			}
			c.metrics.HTTP.SSEConnectionClosed(audioLevelStreamEndpoint, duration, closeReason)
		}()
	}

	// Create timeout context for maximum connection duration
	timeoutCtx, cancel := context.WithTimeout(ctx.Request().Context(), audioLevelMaxDuration)
	defer cancel()

	// Override request context with timeout
	ctx.SetRequest(ctx.Request().WithContext(timeoutCtx))

	// Set SSE headers
	ctx.Response().Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	ctx.Response().Header().Set("Cache-Control", "no-cache")
	ctx.Response().Header().Set("Connection", "keep-alive")
	ctx.Response().Header().Set("Access-Control-Allow-Origin", "*")
	ctx.Response().WriteHeader(http.StatusOK)

	// Check authentication status for data anonymization
	isAuthenticated := c.isClientAuthenticated(ctx)

	// Initialize level tracking data
	levels := c.initializeAudioLevels(isAuthenticated)
	lastUpdateTime := make(map[string]time.Time)
	lastNonZeroTime := make(map[string]time.Time)
	now := time.Now()
	for source := range levels {
		lastUpdateTime[source] = now
		lastNonZeroTime[source] = now
	}

	// Log connection
	c.logAPIRequest(ctx, slog.LevelInfo, "Audio level SSE client connected",
		"authenticated", isAuthenticated,
		"total_connections", atomic.LoadInt64(&audioLevelMgr.totalConnections))

	// Send initial empty update to establish connection
	if err := c.sendAudioLevelUpdate(ctx, levels); err != nil {
		return err
	}

	// Create tickers
	heartbeat := time.NewTicker(audioLevelHeartbeatInterval)
	defer heartbeat.Stop()
	activityCheck := time.NewTicker(audioLevelActivityCheck)
	defer activityCheck.Stop()

	lastSentTime := time.Now()

	// Main event loop
	for {
		select {
		case <-timeoutCtx.Done():
			c.logAPIRequest(ctx, slog.LevelInfo, "Audio level SSE connection closed",
				"reason", "timeout_or_cancelled")
			return nil

		case audioData, ok := <-c.audioLevelChan:
			if !ok {
				c.logAPIRequest(ctx, slog.LevelWarn, "Audio level channel closed")
				return nil
			}

			// Update audio levels with proper name handling
			c.updateAudioLevel(audioData, levels, lastUpdateTime, lastNonZeroTime, isAuthenticated)

			// Rate limit updates
			if time.Since(lastSentTime) >= audioLevelRateLimitUpdate {
				if err := c.sendAudioLevelUpdate(ctx, levels); err != nil {
					return err
				}
				lastSentTime = time.Now()
			}

		case <-activityCheck.C:
			// Check for inactive sources and zero them out
			if updated := c.checkSourceActivity(levels, lastUpdateTime, lastNonZeroTime); updated {
				if err := c.sendAudioLevelUpdate(ctx, levels); err != nil {
					return err
				}
			}

		case <-heartbeat.C:
			// Send heartbeat comment to keep connection alive
			if _, err := fmt.Fprintf(ctx.Response(), ": heartbeat %d\n\n", time.Now().Unix()); err != nil {
				return err
			}
			ctx.Response().Flush()
		}
	}
}

// isClientAuthenticated checks if the current request is authenticated
func (c *Controller) isClientAuthenticated(ctx echo.Context) bool {
	if c.AuthService == nil {
		return false
	}

	// Check if auth is required for this client
	if !c.AuthService.IsAuthRequired(ctx) {
		return true // Bypassed auth is treated as authenticated for data access
	}

	// Try token auth
	if authenticated, _ := c.handleTokenAuth(ctx); authenticated {
		return true
	}

	// Try session auth
	return c.handleSessionAuth(ctx)
}

// initializeAudioLevels creates the initial levels map with configured sources
func (c *Controller) initializeAudioLevels(isAuthenticated bool) map[string]myaudio.AudioLevelData {
	levels := make(map[string]myaudio.AudioLevelData)
	registry := myaudio.GetRegistry()

	if registry == nil {
		return levels
	}

	// Add configured audio device if set
	if c.Settings.Realtime.Audio.Source != "" {
		source := registry.GetOrCreateSource(c.Settings.Realtime.Audio.Source, myaudio.SourceTypeAudioCard)
		if source != nil {
			displayName := source.DisplayName
			if !isAuthenticated {
				displayName = "audio-source-1"
			}
			levels[source.ID] = myaudio.AudioLevelData{
				Level:  0,
				Name:   displayName,
				Source: source.ID,
			}
		}
	}

	// Add configured RTSP sources
	for i, url := range c.Settings.Realtime.RTSP.URLs {
		source := registry.GetOrCreateSource(url, myaudio.SourceTypeRTSP)
		if source != nil {
			displayName := source.DisplayName
			if !isAuthenticated {
				displayName = fmt.Sprintf("camera-%d", i+1)
				// Cache anonymized name for O(1) lookup
				audioLevelMgr.rtspAnonymMu.Lock()
				audioLevelMgr.rtspAnonymMap[source.ID] = displayName
				audioLevelMgr.rtspAnonymMu.Unlock()
			}
			levels[source.ID] = myaudio.AudioLevelData{
				Level:  0,
				Name:   displayName,
				Source: source.ID,
			}
		}
	}

	return levels
}

// updateAudioLevel processes incoming audio data and updates the levels map
func (c *Controller) updateAudioLevel(
	audioData myaudio.AudioLevelData,
	levels map[string]myaudio.AudioLevelData,
	lastUpdateTime, lastNonZeroTime map[string]time.Time,
	isAuthenticated bool,
) {
	now := time.Now()
	registry := myaudio.GetRegistry()

	// Determine display name based on authentication
	if registry != nil {
		if source, exists := registry.GetSourceByID(audioData.Source); exists {
			if isAuthenticated {
				audioData.Name = source.DisplayName
			} else {
				audioData.Name = c.getAnonymizedSourceName(source)
			}
		}
	} else if !isAuthenticated {
		// Fallback anonymization without registry
		audioData.Name = c.getAnonymizedSourceNameFallback(audioData.Source)
	}

	// Update activity timestamps
	lastUpdateTime[audioData.Source] = now
	if audioData.Level > 0 {
		lastNonZeroTime[audioData.Source] = now
	}

	// Update level unless source is inactive
	if !c.isSourceInactive(audioData.Source, now, lastUpdateTime, lastNonZeroTime) {
		levels[audioData.Source] = audioData
	} else {
		audioData.Level = 0
		levels[audioData.Source] = audioData
	}
}

// getAnonymizedSourceName returns an anonymized name for a source
func (c *Controller) getAnonymizedSourceName(source *myaudio.AudioSource) string {
	switch source.Type {
	case myaudio.SourceTypeAudioCard:
		return "audio-source-1"
	case myaudio.SourceTypeRTSP:
		audioLevelMgr.rtspAnonymMu.RLock()
		if name, exists := audioLevelMgr.rtspAnonymMap[source.ID]; exists {
			audioLevelMgr.rtspAnonymMu.RUnlock()
			return name
		}
		audioLevelMgr.rtspAnonymMu.RUnlock()
		// Fallback for unmapped RTSP sources
		idPrefix := source.ID
		if len(source.ID) > 8 {
			idPrefix = source.ID[:8]
		}
		return fmt.Sprintf("camera-%s", idPrefix)
	case myaudio.SourceTypeFile:
		return "file-source"
	default:
		return "unknown-source"
	}
}

// getAnonymizedSourceNameFallback returns anonymized name when registry is unavailable
func (c *Controller) getAnonymizedSourceNameFallback(sourceID string) string {
	switch {
	case strings.HasPrefix(sourceID, "audio_card_"):
		return "audio-source-1"
	case strings.HasPrefix(sourceID, "rtsp_"):
		audioLevelMgr.rtspAnonymMu.RLock()
		if name, exists := audioLevelMgr.rtspAnonymMap[sourceID]; exists {
			audioLevelMgr.rtspAnonymMu.RUnlock()
			return name
		}
		audioLevelMgr.rtspAnonymMu.RUnlock()
		idPrefix := sourceID
		if len(sourceID) > 8 {
			idPrefix = sourceID[:8]
		}
		return fmt.Sprintf("camera-%s", idPrefix)
	case strings.HasPrefix(sourceID, "file_"):
		return "file-source"
	default:
		return "unknown-source"
	}
}

// isSourceInactive checks if a source should be considered inactive
func (c *Controller) isSourceInactive(
	source string,
	now time.Time,
	lastUpdateTime, lastNonZeroTime map[string]time.Time,
) bool {
	lastUpdate, hasUpdate := lastUpdateTime[source]
	lastNonZero, hasNonZero := lastNonZeroTime[source]

	if !hasUpdate || !hasNonZero {
		return false // New sources are considered active
	}

	noUpdateTimeout := now.Sub(lastUpdate) > audioLevelInactivityThreshold
	noActivityTimeout := now.Sub(lastNonZero) > audioLevelInactivityThreshold

	return noUpdateTimeout || noActivityTimeout
}

// checkSourceActivity checks all sources for inactivity
func (c *Controller) checkSourceActivity(
	levels map[string]myaudio.AudioLevelData,
	lastUpdateTime, lastNonZeroTime map[string]time.Time,
) bool {
	now := time.Now()
	updated := false

	for source, data := range levels {
		if c.isSourceInactive(source, now, lastUpdateTime, lastNonZeroTime) && data.Level != 0 {
			data.Level = 0
			levels[source] = data
			updated = true
		}
	}

	return updated
}

// sendAudioLevelUpdate sends the current levels to the client
func (c *Controller) sendAudioLevelUpdate(ctx echo.Context, levels map[string]myaudio.AudioLevelData) error {
	message := AudioLevelSSEData{
		Type:   "audio-level",
		Levels: levels,
	}

	jsonData, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal audio level data: %w", err)
	}

	if _, err := fmt.Fprintf(ctx.Response(), "data: %s\n\n", jsonData); err != nil {
		return fmt.Errorf("failed to write SSE message: %w", err)
	}

	ctx.Response().Flush()
	return nil
}
