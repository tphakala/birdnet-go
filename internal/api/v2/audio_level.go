// internal/api/v2/audio_level.go
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
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
	// Rate is requests per second for Echo's rate limiter
	// 10 requests per minute = 10/60 ≈ 0.167 requests per second
	audioLevelRateLimitRate   = 10.0 / 60.0       // Rate in requests per second (10 req/min)
	audioLevelRateLimitWindow = 1 * time.Minute   // Window for rate limit expiration

	// Endpoints
	audioLevelStreamEndpoint = "/api/v2/streams/audio-level"
)

// AudioLevelSSEData represents the audio level data sent via SSE
// This matches the v1 format for frontend compatibility
type AudioLevelSSEData struct {
	Type   string                            `json:"type"`
	Levels map[string]myaudio.AudioLevelData `json:"levels"`
}

// audioLevelManager manages audio level SSE connections and broadcasts
type audioLevelManager struct {
	// Active connections tracked per client IP to prevent duplicates
	activeConnections sync.Map
	// Total connection counter for monitoring
	totalConnections int64
	// RTSP anonymization map for non-authenticated users (bounded to maxRTSPSources)
	rtspAnonymMap map[string]string
	rtspAnonymMu  sync.RWMutex

	// Fan-out broadcaster for audio level data
	subscribers   map[chan myaudio.AudioLevelData]struct{}
	subscribersMu sync.RWMutex

	// Broadcaster lifecycle
	broadcasterOnce   sync.Once
	broadcasterCancel context.CancelFunc
}

// Maximum number of RTSP sources to cache in anonymization map
const maxRTSPAnonymMapSize = 100

// Global audio level manager instance
// TODO: Move to Controller struct during httpcontroller refactoring
var audioLevelMgr = &audioLevelManager{
	rtspAnonymMap: make(map[string]string),
	subscribers:   make(map[chan myaudio.AudioLevelData]struct{}),
}

// SetAudioLevelChan sets the audio level channel for the controller and starts
// the broadcaster goroutine that fans out messages to all SSE subscribers.
//
// CONCURRENCY CONTRACT: This method MUST be called exactly once during
// single-threaded server startup, before any HTTP requests are processed.
// The channel is read by a single broadcaster goroutine which fans out to
// subscriber channels. Calling this method after the server starts accepting
// requests may result in data races.
//
// This connects the audio capture system to the SSE endpoint.
func (c *Controller) SetAudioLevelChan(ch chan myaudio.AudioLevelData) {
	c.audioLevelChan = ch
	if c.apiLogger != nil {
		c.apiLogger.Info("Audio level channel connected to API v2 controller")
	}

	// Start the broadcaster goroutine (only once across all controller instances)
	audioLevelMgr.broadcasterOnce.Do(func() {
		ctx, cancel := context.WithCancel(c.ctx)
		audioLevelMgr.broadcasterCancel = cancel
		go runAudioLevelBroadcaster(ctx, ch)
	})
}

// runAudioLevelBroadcaster reads from the source channel and broadcasts to all subscribers.
// This allows multiple SSE clients to receive the same audio level data.
func runAudioLevelBroadcaster(ctx context.Context, sourceChan chan myaudio.AudioLevelData) {
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-sourceChan:
			if !ok {
				// Source channel closed, close all subscriber channels
				audioLevelMgr.subscribersMu.Lock()
				for ch := range audioLevelMgr.subscribers {
					close(ch)
					delete(audioLevelMgr.subscribers, ch)
				}
				audioLevelMgr.subscribersMu.Unlock()
				return
			}

			// Fan out to all subscribers (non-blocking send)
			audioLevelMgr.subscribersMu.RLock()
			for ch := range audioLevelMgr.subscribers {
				select {
				case ch <- data:
					// Sent successfully
				default:
					// Subscriber channel full, skip this update
					// (slow clients will miss updates rather than blocking others)
				}
			}
			audioLevelMgr.subscribersMu.RUnlock()
		}
	}
}

// subscribeToAudioLevels creates a new subscriber channel and registers it.
// The returned channel will receive audio level data from the broadcaster.
// The caller MUST call unsubscribeFromAudioLevels when done.
func subscribeToAudioLevels() chan myaudio.AudioLevelData {
	ch := make(chan myaudio.AudioLevelData, audioLevelChannelBuffer)
	audioLevelMgr.subscribersMu.Lock()
	audioLevelMgr.subscribers[ch] = struct{}{}
	audioLevelMgr.subscribersMu.Unlock()
	return ch
}

// unsubscribeFromAudioLevels removes a subscriber channel from the broadcaster.
// This should be called when an SSE client disconnects.
func unsubscribeFromAudioLevels(ch chan myaudio.AudioLevelData) {
	audioLevelMgr.subscribersMu.Lock()
	delete(audioLevelMgr.subscribers, ch)
	audioLevelMgr.subscribersMu.Unlock()
	// Note: We don't close the channel here as it may still have buffered data
	// that the handler is processing. The channel will be garbage collected.
}

// cacheRTSPAnonymName stores an anonymized name for an RTSP source with bounded map size.
// If the map exceeds maxRTSPAnonymMapSize, it clears the map to prevent unbounded growth.
// This is acceptable because the map is only a cache for performance; lookups will
// regenerate the name if not found.
func cacheRTSPAnonymName(sourceID, displayName string) {
	audioLevelMgr.rtspAnonymMu.Lock()
	defer audioLevelMgr.rtspAnonymMu.Unlock()

	// If map is at capacity and this is a new entry, clear the map
	// This is a simple strategy; in practice RTSP source count is typically small
	if len(audioLevelMgr.rtspAnonymMap) >= maxRTSPAnonymMapSize {
		if _, exists := audioLevelMgr.rtspAnonymMap[sourceID]; !exists {
			// Clear the map to prevent unbounded growth
			audioLevelMgr.rtspAnonymMap = make(map[string]string)
		}
	}

	audioLevelMgr.rtspAnonymMap[sourceID] = displayName
}

// initAudioLevelRoutes registers audio level SSE endpoints
func (c *Controller) initAudioLevelRoutes() {
	// Create rate limiter for audio level SSE connections
	rateLimiterConfig := middleware.RateLimiterConfig{
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(
			middleware.RateLimiterMemoryStoreConfig{
				Rate:      audioLevelRateLimitRate,
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
	// Early nil check for audio level channel
	if c.audioLevelChan == nil {
		c.logAPIRequest(ctx, slog.LevelWarn, "Audio level stream unavailable - channel not configured")
		return ctx.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "Audio level stream is not available",
		})
	}

	// Use RemoteAddr for duplicate connection check to prevent IP spoofing
	// via proxy headers for rate limiting purposes
	clientIP := c.extractRemoteAddr(ctx)

	// Check for duplicate connection from same IP
	if _, exists := audioLevelMgr.activeConnections.LoadOrStore(clientIP, time.Now()); exists {
		c.logAPIRequest(ctx, slog.LevelWarn, "Rejected duplicate audio level SSE connection",
			"reason", "already_connected")
		return ctx.JSON(http.StatusTooManyRequests, map[string]string{
			"error": "Only one audio level stream connection per client is allowed",
		})
	}

	// Subscribe to the broadcaster to receive audio level data
	// This allows multiple clients to receive the same data (fan-out pattern)
	subscriberChan := subscribeToAudioLevels()

	// Cleanup connection on exit
	defer func() {
		unsubscribeFromAudioLevels(subscriberChan)
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

	// Set SSE headers (CORS is handled by middleware at the v2 group level)
	ctx.Response().Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	ctx.Response().Header().Set("Cache-Control", "no-cache")
	ctx.Response().Header().Set("Connection", "keep-alive")
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

	// Main event loop - reads from subscriber channel instead of source channel
	for {
		select {
		case <-timeoutCtx.Done():
			c.logAPIRequest(ctx, slog.LevelInfo, "Audio level SSE connection closed",
				"reason", "timeout_or_cancelled")
			return nil

		case audioData, ok := <-subscriberChan:
			if !ok {
				c.logAPIRequest(ctx, slog.LevelWarn, "Audio level subscriber channel closed")
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
				// Cache anonymized name for O(1) lookup with bounded map size
				cacheRTSPAnonymName(source.ID, displayName)
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

// extractRemoteAddr extracts the IP address from RemoteAddr, stripping port if present.
// This is used for rate limiting and duplicate connection detection where we want
// the actual connection address rather than proxy-provided headers which can be spoofed.
func (c *Controller) extractRemoteAddr(ctx echo.Context) string {
	remoteAddr := ctx.Request().RemoteAddr
	// RemoteAddr is typically "IP:port", extract just the IP
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	// If no port (unlikely but possible), return as-is
	return remoteAddr
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
