// internal/api/v2/audio_level.go
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Audio level SSE configuration constants
const (
	// Connection timeouts
	audioLevelMaxDuration         = 30 * time.Minute      // Maximum stream duration to prevent resource leaks
	audioLevelHeartbeatInterval   = 10 * time.Second      // Heartbeat interval for keep-alive
	audioLevelActivityCheck       = 1 * time.Second       // Activity check interval for sources
	audioLevelInactivityThreshold = 15 * time.Second      // Threshold for marking sources as inactive
	audioLevelRateLimitUpdate     = 50 * time.Millisecond // Rate limit for sending updates
	audioLevelWriteDeadline       = 10 * time.Second      // Write deadline for SSE messages to prevent WriteTimeout

	// Buffer sizes
	audioLevelChannelBuffer = 100 // Buffer size for internal processing

	// Maximum connections per IP (allows multiple browser tabs)
	audioLevelMaxConnectionsPerIP = 5

	// Endpoints
	audioLevelStreamEndpoint = "/api/v2/streams/audio-level"

	// Audio source display name for privacy (unauthenticated users)
	audioSourceDefaultName = "audio-source-1"

	// Anonymization settings
	anonymizedIDPrefixLen = 8 // Length of ID prefix for anonymized camera names
)

// AudioLevelSSEData represents the audio level data sent via SSE
// This matches the v1 format for frontend compatibility
type AudioLevelSSEData struct {
	Type   string                              `json:"type"`
	Levels map[string]audiocore.AudioLevelData `json:"levels"`
}

// audioLevelManager manages audio level SSE connections and broadcasts
type audioLevelManager struct {
	// Active connections tracked per client IP (stores connection count as *int32)
	activeConnections sync.Map
	// Mutex for connection count operations
	connectionMu sync.Mutex
	// Total connection counter for monitoring
	totalConnections int64
	// Stream anonymization map for non-authenticated users (bounded to maxStreamSources)
	streamAnonymMap map[string]string
	streamAnonymMu  sync.RWMutex

	// Fan-out broadcaster for audio level data
	subscribers   map[chan audiocore.AudioLevelData]struct{}
	subscribersMu sync.RWMutex

	// Broadcaster lifecycle
	broadcasterOnce   sync.Once
	broadcasterCancel context.CancelFunc
}

// Maximum number of stream sources to cache in anonymization map
const maxStreamAnonymMapSize = 100

// Global audio level manager instance
// TODO: Consider moving to Controller struct for better encapsulation
var audioLevelMgr = &audioLevelManager{
	streamAnonymMap: make(map[string]string),
	subscribers:     make(map[chan audiocore.AudioLevelData]struct{}),
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
func (c *Controller) SetAudioLevelChan(ch chan audiocore.AudioLevelData) {
	c.audioLevelChan = ch
	c.logInfoIfEnabled("Audio level channel connected to API v2 controller")

	// Start the broadcaster goroutine (only once across all controller instances)
	audioLevelMgr.broadcasterOnce.Do(func() {
		ctx, cancel := context.WithCancel(c.ctx)
		audioLevelMgr.broadcasterCancel = cancel
		go runAudioLevelBroadcaster(ctx, ch)
	})
}

// runAudioLevelBroadcaster reads from the source channel and broadcasts to all subscribers.
// This allows multiple SSE clients to receive the same audio level data.
func runAudioLevelBroadcaster(ctx context.Context, sourceChan chan audiocore.AudioLevelData) {
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
func subscribeToAudioLevels() chan audiocore.AudioLevelData {
	ch := make(chan audiocore.AudioLevelData, audioLevelChannelBuffer)
	audioLevelMgr.subscribersMu.Lock()
	audioLevelMgr.subscribers[ch] = struct{}{}
	audioLevelMgr.subscribersMu.Unlock()
	return ch
}

// unsubscribeFromAudioLevels removes a subscriber channel from the broadcaster.
// This should be called when an SSE client disconnects.
func unsubscribeFromAudioLevels(ch chan audiocore.AudioLevelData) {
	audioLevelMgr.subscribersMu.Lock()
	delete(audioLevelMgr.subscribers, ch)
	audioLevelMgr.subscribersMu.Unlock()
	// Note: We don't close the channel here as it may still have buffered data
	// that the handler is processing. The channel will be garbage collected.
}

// cacheStreamAnonymName stores an anonymized name for a stream source with bounded map size.
// If the map exceeds maxStreamAnonymMapSize, it clears the map to prevent unbounded growth.
// This is acceptable because the map is only a cache for performance; lookups will
// regenerate the name if not found.
func cacheStreamAnonymName(sourceID, displayName string) {
	audioLevelMgr.streamAnonymMu.Lock()
	defer audioLevelMgr.streamAnonymMu.Unlock()

	// If map is at capacity and this is a new entry, clear the map
	// This is a simple strategy; in practice RTSP source count is typically small
	if len(audioLevelMgr.streamAnonymMap) >= maxStreamAnonymMapSize {
		if _, exists := audioLevelMgr.streamAnonymMap[sourceID]; !exists {
			// Clear the map to prevent unbounded growth
			audioLevelMgr.streamAnonymMap = make(map[string]string)
		}
	}

	audioLevelMgr.streamAnonymMap[sourceID] = displayName
}

// initAudioLevelRoutes registers audio level SSE endpoints
func (c *Controller) initAudioLevelRoutes() {
	// Audio level SSE endpoint - public, no rate limiting
	// The per-IP connection limit (audioLevelMaxConnectionsPerIP) still applies
	// Authentication is checked within the handler to control data anonymization
	c.Group.GET("/streams/audio-level", c.StreamAudioLevel)
}

// StreamAudioLevel handles SSE connections for real-time audio level streaming
// This provides simple audio level data (0-100 with clipping detection) for UI indicators
func (c *Controller) StreamAudioLevel(ctx echo.Context) error {
	// Early nil check for audio level channel
	if c.audioLevelChan == nil {
		c.logAPIRequest(ctx, logger.LogLevelWarn, "Audio level stream unavailable - channel not configured")
		return c.HandleError(ctx, nil, "Audio level stream is not available", http.StatusServiceUnavailable)
	}

	// Use RemoteAddr for connection tracking to prevent IP spoofing
	// via proxy headers for rate limiting purposes
	clientIP := c.extractRemoteAddr(ctx)

	// Check connection count per IP (allow multiple browser tabs up to limit)
	audioLevelMgr.connectionMu.Lock()
	countPtr, loaded := audioLevelMgr.activeConnections.Load(clientIP)
	var count int32
	if loaded {
		count = atomic.LoadInt32(countPtr.(*int32))
	}
	if count >= audioLevelMaxConnectionsPerIP {
		audioLevelMgr.connectionMu.Unlock()
		c.logAPIRequest(ctx, logger.LogLevelWarn, "Rejected audio level SSE connection - max per IP reached",
			logger.String("reason", "max_connections_per_ip"),
			logger.Int("current_count", int(count)),
			logger.Int("max_allowed", audioLevelMaxConnectionsPerIP))
		return c.HandleError(ctx, nil, fmt.Sprintf("Maximum %d audio level stream connections per client reached", audioLevelMaxConnectionsPerIP), http.StatusTooManyRequests)
	}
	// Increment connection count
	if !loaded {
		var newCount int32 = 1
		audioLevelMgr.activeConnections.Store(clientIP, &newCount)
	} else {
		atomic.AddInt32(countPtr.(*int32), 1)
	}
	audioLevelMgr.connectionMu.Unlock()

	// Subscribe to the broadcaster to receive audio level data
	// This allows multiple clients to receive the same data (fan-out pattern)
	subscriberChan := subscribeToAudioLevels()

	// Cleanup connection on exit
	defer func() {
		unsubscribeFromAudioLevels(subscriberChan)
		// Decrement connection count for this IP
		audioLevelMgr.connectionMu.Lock()
		if countPtr, ok := audioLevelMgr.activeConnections.Load(clientIP); ok {
			newCount := atomic.AddInt32(countPtr.(*int32), -1)
			if newCount <= 0 {
				audioLevelMgr.activeConnections.Delete(clientIP)
			}
		}
		audioLevelMgr.connectionMu.Unlock()
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
	c.logAPIRequest(ctx, logger.LogLevelInfo, "Audio level SSE client connected",
		logger.Bool("authenticated", isAuthenticated),
		logger.Int64("total_connections", atomic.LoadInt64(&audioLevelMgr.totalConnections)))

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
		case <-c.ctx.Done():
			// Controller shutting down
			c.logAPIRequest(ctx, logger.LogLevelInfo, "Audio level SSE connection closed",
				logger.String("reason", "shutdown"))
			return nil

		case <-timeoutCtx.Done():
			c.logAPIRequest(ctx, logger.LogLevelInfo, "Audio level SSE connection closed",
				logger.String("reason", "timeout_or_cancelled"))
			return nil

		case audioData, ok := <-subscriberChan:
			if !ok {
				c.logWarnIfEnabled("Audio level subscriber channel closed")
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
			// Prune sources that have been removed/disabled since the session started.
			registry := c.engine.Registry()
			pruned := registry != nil && c.pruneRemovedSources(registry, levels, lastUpdateTime, lastNonZeroTime)
			// Check for inactive sources and zero them out.
			if updated := c.checkSourceActivity(levels, lastUpdateTime, lastNonZeroTime); updated || pruned {
				if err := c.sendAudioLevelUpdate(ctx, levels); err != nil {
					return err
				}
			}

		case <-heartbeat.C:
			// Send heartbeat comment to keep connection alive
			if err := c.sendAudioLevelHeartbeat(ctx); err != nil {
				return err
			}
		}
	}
}

// isClientAuthenticated checks if the current request is authenticated
// by delegating to the auth service's centralized IsAuthenticated method.
func (c *Controller) isClientAuthenticated(ctx echo.Context) bool {
	if c.authService == nil {
		return false
	}
	return c.authService.IsAuthenticated(ctx)
}

// createAudioLevelEntry creates an AudioLevelData entry for a source with appropriate display name.
func createAudioLevelEntry(sourceID, displayName string) audiocore.AudioLevelData {
	return audiocore.AudioLevelData{
		Level:  0,
		Name:   displayName,
		Source: sourceID,
	}
}

// initializeAudioLevels creates the initial levels map with configured sources
func (c *Controller) initializeAudioLevels(isAuthenticated bool) map[string]audiocore.AudioLevelData {
	levels := make(map[string]audiocore.AudioLevelData)
	registry := c.engine.Registry()
	if registry == nil {
		return levels
	}

	// Add configured audio devices
	for _, source := range c.getAudioCardSources(registry) {
		displayName := source.DisplayName
		if !isAuthenticated {
			displayName = audioSourceDefaultName
		}
		levels[source.ID] = createAudioLevelEntry(source.ID, displayName)
	}

	// Add configured RTSP sources
	c.addStreamSourcesToLevels(registry, levels, isAuthenticated)

	return levels
}

// getAudioCardSources retrieves all configured audio card sources from the registry.
func (c *Controller) getAudioCardSources(registry *audiocore.SourceRegistry) []*audiocore.AudioSource {
	var sources []*audiocore.AudioSource
	for i := range c.Settings.Realtime.Audio.Sources {
		src := &c.Settings.Realtime.Audio.Sources[i]
		if src.Device == "" {
			continue
		}
		if audioSrc, ok := registry.GetByConnection(src.Device); ok {
			sources = append(sources, audioSrc)
		}
	}
	return sources
}

// addStreamSourcesToLevels adds all configured stream sources to the levels map.
func (c *Controller) addStreamSourcesToLevels(registry *audiocore.SourceRegistry, levels map[string]audiocore.AudioLevelData, isAuthenticated bool) {
	for i, stream := range c.Settings.Realtime.RTSP.EnabledStreams() {
		source, ok := registry.GetByConnection(stream.URL)
		if !ok {
			continue
		}
		displayName := source.DisplayName
		if !isAuthenticated {
			displayName = fmt.Sprintf("camera-%d", i+1)
			cacheStreamAnonymName(source.ID, displayName)
		}
		levels[source.ID] = createAudioLevelEntry(source.ID, displayName)
	}
}

// updateAudioLevel processes incoming audio data and updates the levels map
func (c *Controller) updateAudioLevel(
	audioData audiocore.AudioLevelData,
	levels map[string]audiocore.AudioLevelData,
	lastUpdateTime, lastNonZeroTime map[string]time.Time,
	isAuthenticated bool,
) {
	now := time.Now()
	registry := c.engine.Registry()

	// Determine display name based on authentication
	if registry != nil {
		if source, ok := registry.Get(audioData.Source); ok {
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
func (c *Controller) getAnonymizedSourceName(source *audiocore.AudioSource) string {
	switch source.Type {
	case audiocore.SourceTypeAudioCard:
		return audioSourceDefaultName
	case audiocore.SourceTypeRTSP, audiocore.SourceTypeHTTP, audiocore.SourceTypeHLS,
		audiocore.SourceTypeRTMP, audiocore.SourceTypeUDP:
		// All stream types use the same anonymization pattern
		audioLevelMgr.streamAnonymMu.RLock()
		if name, exists := audioLevelMgr.streamAnonymMap[source.ID]; exists {
			audioLevelMgr.streamAnonymMu.RUnlock()
			return name
		}
		audioLevelMgr.streamAnonymMu.RUnlock()
		// Fallback for unmapped stream sources
		idPrefix := source.ID
		if len(source.ID) > anonymizedIDPrefixLen {
			idPrefix = source.ID[:anonymizedIDPrefixLen]
		}
		return fmt.Sprintf("camera-%s", idPrefix)
	case audiocore.SourceTypeFile:
		return "file-source"
	default:
		return "unknown-source"
	}
}

// getAnonymizedSourceNameFallback returns anonymized name when registry is unavailable
func (c *Controller) getAnonymizedSourceNameFallback(sourceID string) string {
	// Check for audio card source
	if strings.HasPrefix(sourceID, "audio_card_") {
		return audioSourceDefaultName
	}

	// Check for file source
	if strings.HasPrefix(sourceID, "file_") {
		return "file-source"
	}

	// Check for any stream source type (rtsp_, http_, hls_, rtmp_, udp_)
	streamPrefixes := []string{"rtsp_", "http_", "hls_", "rtmp_", "udp_"}
	for _, prefix := range streamPrefixes {
		if !strings.HasPrefix(sourceID, prefix) {
			continue
		}
		audioLevelMgr.streamAnonymMu.RLock()
		if name, exists := audioLevelMgr.streamAnonymMap[sourceID]; exists {
			audioLevelMgr.streamAnonymMu.RUnlock()
			return name
		}
		audioLevelMgr.streamAnonymMu.RUnlock()
		idPrefix := sourceID
		if len(sourceID) > anonymizedIDPrefixLen {
			idPrefix = sourceID[:anonymizedIDPrefixLen]
		}
		return fmt.Sprintf("camera-%s", idPrefix)
	}

	return "unknown-source"
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

// pruneRemovedSources removes sources from the levels map that are no longer
// present in the registry (e.g. a stream was disabled and torn down mid-session).
// Returns true if any sources were pruned so the caller can send an update.
func (c *Controller) pruneRemovedSources(
	registry *audiocore.SourceRegistry,
	levels map[string]audiocore.AudioLevelData,
	lastUpdateTime, lastNonZeroTime map[string]time.Time,
) bool {
	pruned := false
	for sourceID := range levels {
		if _, ok := registry.Get(sourceID); !ok {
			delete(levels, sourceID)
			delete(lastUpdateTime, sourceID)
			delete(lastNonZeroTime, sourceID)
			pruned = true
		}
	}
	return pruned
}

// checkSourceActivity checks all sources for inactivity
func (c *Controller) checkSourceActivity(
	levels map[string]audiocore.AudioLevelData,
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

// resetAudioLevelWriteDeadline resets the write deadline for the SSE connection.
// This prevents the server's WriteTimeout from terminating long-lived SSE connections.
func (c *Controller) resetAudioLevelWriteDeadline(ctx echo.Context, operation string) {
	if conn, ok := ctx.Response().Writer.(WriteDeadlineSetter); ok {
		if err := conn.SetWriteDeadline(time.Now().Add(audioLevelWriteDeadline)); err != nil {
			c.logDebugIfEnabled("Failed to set write deadline for "+operation, logger.Error(err))
		}
	}
}

// sendAudioLevelUpdate sends the current levels to the client
func (c *Controller) sendAudioLevelUpdate(ctx echo.Context, levels map[string]audiocore.AudioLevelData) error {
	message := AudioLevelSSEData{
		Type:   "audio-level",
		Levels: levels,
	}

	jsonData, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal audio level data: %w", err)
	}

	// Reset write deadline to prevent server WriteTimeout from closing connection.
	c.resetAudioLevelWriteDeadline(ctx, "audio level update")

	if _, err := fmt.Fprintf(ctx.Response(), "data: %s\n\n", jsonData); err != nil {
		return fmt.Errorf("failed to write SSE message: %w", err)
	}

	ctx.Response().Flush()
	return nil
}

// sendAudioLevelHeartbeat sends a heartbeat comment to keep the SSE connection alive.
// It resets the write deadline before writing to prevent server WriteTimeout.
func (c *Controller) sendAudioLevelHeartbeat(ctx echo.Context) error {
	// Reset write deadline to prevent server WriteTimeout from closing connection.
	c.resetAudioLevelWriteDeadline(ctx, "heartbeat")

	if _, err := fmt.Fprintf(ctx.Response(), ": heartbeat %d\n\n", time.Now().Unix()); err != nil {
		return err
	}
	ctx.Response().Flush()
	return nil
}
