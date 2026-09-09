// internal/api/v2/audio/audio_hls.go
package audio

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	hls "github.com/tphakala/go-hls"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/engine"
	"github.com/tphakala/birdnet-go/internal/audiocore/equalizer"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
	"golang.org/x/sync/singleflight"
)

// HLS streaming configuration constants
const (
	// Timeouts
	hlsStreamInactivityTimeout = 5 * time.Minute  // Cleanup inactive streams after this duration
	hlsPlaylistWaitTimeout     = 20 * time.Second // How long to wait for playlist file
	hlsNewStreamGracePeriod    = 30 * time.Second // Grace period for new streams before cleanup

	// Logging
	hlsLogCooldown        = 60 * time.Second      // Only log client connections once per this duration
	hlsVerboseEnvVar      = "HLS_VERBOSE_LOGGING" // Environment variable to enable verbose logging
	hlsClientLogRetention = 24 * time.Hour        // Retention period for client log timestamps

	// Audio encoding
	hlsMinSegments                  = 2                // Minimum HLS segments required
	hlsDefaultSegmentLen            = 2                // Default HLS segment length in seconds
	hlsMinSegmentLen                = 1                // Minimum HLS segment length in seconds
	hlsMaxSegmentLen                = 30               // Maximum HLS segment length in seconds
	hlsMinBitrate                   = 16               // Minimum audio bitrate in kbps
	hlsMaxBitrate                   = 320              // Maximum audio bitrate in kbps
	hlsCleanupDelay                 = 5                // Delay in seconds before cleanup
	hlsPrematureDisconnectThreshold = 10 * time.Second // Ignore disconnects within this window

	// hlsDropLogInterval is the minimum interval between audio data drop log messages
	// to avoid flooding the log when the channel is consistently full.
	hlsDropLogInterval = 5 * time.Second

	// hlsSegmentFreshnessMultiplier is the multiplier applied to the configured
	// segment length to determine the staleness threshold. A segment older than
	// segmentLength * multiplier is considered stale. Using 3x allows for normal
	// jitter in segment production timing.
	hlsSegmentFreshnessMultiplier = 3

	// hlsFreshnessCheckInterval is the minimum interval between segment freshness
	// checks to avoid adding I/O load on every playlist poll.
	hlsFreshnessCheckInterval = 10 * time.Second

	// hlsPlaylistPollInterval is how often stream creation checks whether enough
	// segments exist to start playback. Set to catch the second segment promptly
	// after the first.
	hlsPlaylistPollInterval = 500 * time.Millisecond

	// HLS muxer settings
	hlsListSize = 6 // Number of HLS segments to keep in playlist (must exceed HLS.js liveSyncDurationCount)

	// hlsConsumerChannels is the channel count the HLS consumer declares to the
	// audio router. The capture pipeline is mono end to end and the muxer does
	// not downmix, so naming it gives the muxer configuration something to be
	// checked against rather than a second bare literal.
	hlsConsumerChannels = 1
)

// HLSStreamInfo contains information about an active HLS streaming session.
//
// Every live HLS stream is served by the in-process muxer (internal go-hls
// Stream): there is no FFmpeg process, no FIFO and no output directory, and the
// playlist and segments are served straight from memory.
type HLSStreamInfo struct {
	SourceID    string             // Original audio source identifier
	ctx         context.Context    // Stream lifecycle context
	cancel      context.CancelFunc // Cancel function for cleanup
	streamEpoch time.Time          // Wall-clock time corresponding to HLS stream position 0

	// mux is the in-process muxer serving this stream. It is written once before
	// the stream is published and never reassigned, so isNative (mux != nil) is a
	// stable discriminator for a fully constructed stream.
	mux *hls.Stream

	// feedDone is closed when the feed goroutine has stopped, so teardown can
	// close the muxer only once nothing can still write to it.
	feedDone chan struct{}

	// firstDataTime records the capture time (UnixNano) of the first audio frame
	// the muxer encoded, for start-up diagnostics. Written once by the feed loop.
	firstDataTime atomic.Int64
}

// HLSStreamStatus represents the current status of an HLS stream (API response)
type HLSStreamStatus struct {
	Status        string `json:"status"`                 // "starting" or "ready"
	Source        string `json:"source"`                 // Source identifier (URL-encoded)
	StreamToken   string `json:"stream_token,omitempty"` // Crypto-random token for secure URL access
	PlaylistURL   string `json:"playlist_url,omitempty"` // API URL for the playlist (not filesystem path)
	ActiveClients int    `json:"active_clients"`
	PlaylistReady bool   `json:"playlist_ready"`
	StreamEpoch   string `json:"stream_epoch,omitempty"` // ISO8601 wall-clock time of stream position 0
}

// HLSSessionRequest represents an optional request body for stream start
type HLSSessionRequest struct {
	SessionID string `json:"session_id,omitempty"` // Per-tab session UUID from frontend
}

// HLSHeartbeatRequest represents a client heartbeat message
type HLSHeartbeatRequest struct {
	StreamToken string `json:"stream_token"`
	SessionID   string `json:"session_id,omitempty"` // Per-tab session UUID from frontend
}

// hlsManager manages HLS streaming state
// TODO: Consider moving to Handler struct for better encapsulation
type hlsManager struct {
	// Active streams indexed by sourceID
	streams   map[string]*HLSStreamInfo
	streamsMu sync.RWMutex // RWMutex for read-heavy operations

	// Client tracking per stream
	clients   map[string]map[string]bool // sourceID -> clientID -> true
	clientsMu sync.RWMutex               // RWMutex for read-heavy client count operations

	// Activity tracking for cleanup
	activity   map[string]time.Time // sourceID -> lastActivityTime
	activityMu sync.Mutex

	// Client activity for false disconnect detection
	clientActivity map[string]time.Time // sourceID:clientID -> lastActivityTime

	// Logging configuration
	verboseLogging bool

	// Client log cooldown tracking
	clientLogTime   map[string]time.Time
	clientLogTimeMu sync.Mutex

	// Stream token mappings for secure URL access
	tokens       map[string]string // streamToken → sourceID
	sourceTokens map[string]string // sourceID → streamToken (reverse lookup)
	tokensMu     sync.RWMutex      // Protects both token maps

	// Singleflight for stream creation to prevent concurrent creation races
	streamCreate singleflight.Group

	// Activity sync lifecycle management
	activitySyncOnce   sync.Once
	activitySyncCancel context.CancelFunc
}

// Global HLS manager instance
// TODO: Consider moving to Handler struct for better encapsulation
var hlsMgr = &hlsManager{
	streams:        make(map[string]*HLSStreamInfo),
	clients:        make(map[string]map[string]bool),
	activity:       make(map[string]time.Time),
	clientActivity: make(map[string]time.Time),
	clientLogTime:  make(map[string]time.Time),
	tokens:         make(map[string]string),
	sourceTokens:   make(map[string]string),
	verboseLogging: os.Getenv(hlsVerboseEnvVar) != "",
}

// HLS route path fragments, registered relative to the v2 API group in
// RegisterHLSRoutes. They are exported so the facade's isPrivateModeExempt
// allow-list (which still lives in package api) cannot drift from the routes
// registered here.
const (
	HLSGroupPath      = "/streams/hls"
	HLSTokenGroupPath = "/t"
	HLSStartPath      = "/:sourceID/start"
	HLSStopPath       = "/:sourceID/stop"
	HLSHeartbeatPath  = "/heartbeat"
	HLSStatusPath     = "/status"
	HLSPlaylistPath   = "/:streamToken/playlist.m3u8"
	HLSContentPath    = "/:streamToken/*"
)

// hlsPlaylistURL builds the client-facing playlist URL for a stream token from
// the same route constants used to register the playlist route (see
// RegisterHLSRoutes), so the emitted URL cannot drift from the actual route on a
// prefix or fragment rename.
func hlsPlaylistURL(token string) string {
	tokenPath := strings.Replace(HLSPlaylistPath, ":streamToken", token, 1)
	return apiV2Prefix + HLSGroupPath + HLSTokenGroupPath + tokenPath
}

// RegisterHLSRoutes registers HLS streaming endpoints
func (c *Handler) RegisterHLSRoutes(g *echo.Group) {
	// Get authentication middleware
	authMiddleware := c.AuthMiddleware

	// HLS base group (no auth by default)
	hlsGroup := g.Group(HLSGroupPath)

	// Stream control endpoints
	// Start uses dynamic middleware that checks PublicAccess.LiveAudio per-request,
	// so changes take effect immediately without server restart.
	// Stop always requires authentication to prevent abuse.
	hlsGroup.POST(HLSStartPath, c.StartHLSStream, c.publicLiveAudioAuth)
	hlsGroup.POST(HLSStopPath, c.StopHLSStream, authMiddleware)

	// Auth-gated endpoints
	hlsGroup.POST(HLSHeartbeatPath, c.HLSHeartbeat, c.publicLiveAudioAuth)
	hlsGroup.GET(HLSStatusPath, c.GetHLSStatus, c.publicLiveAudioAuth)

	// Token-based content serving
	hlsTokenGroup := hlsGroup.Group(HLSTokenGroupPath)
	hlsTokenGroup.GET(HLSPlaylistPath, c.ServeHLSPlaylist)
	hlsTokenGroup.GET(HLSContentPath, c.ServeHLSContent)

	// Start the HLS activity sync goroutine (only once across all controller instances)
	hlsMgr.activitySyncOnce.Do(func() {
		ctx, cancel := context.WithCancel(c.Context())
		hlsMgr.activitySyncCancel = cancel
		go runHLSActivitySync(ctx)
	})
}

// publicLiveAudioAuth is a dynamic middleware that checks PublicAccess.LiveAudio
// on each request. When enabled, the request proceeds without authentication.
// When disabled, the standard auth middleware is applied. This allows the setting
// to take effect immediately without a server restart.
func (c *Handler) publicLiveAudioAuth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) error {
		isPublic := c.CurrentSettings().Security.PublicAccess.LiveAudio
		if isPublic {
			return next(ctx)
		}
		return c.AuthMiddleware(next)(ctx)
	}
}

// generateStreamToken creates a crypto-random 32-character hex token for stream URL access.
func generateStreamToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate stream token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// getOrCreateStreamToken returns the existing token for a sourceID, or creates a new one.
// Uses double-checked locking to avoid generating redundant tokens under contention.
func getOrCreateStreamToken(sourceID string) (string, error) {
	hlsMgr.tokensMu.RLock()
	if token, exists := hlsMgr.sourceTokens[sourceID]; exists {
		hlsMgr.tokensMu.RUnlock()
		return token, nil
	}
	hlsMgr.tokensMu.RUnlock()

	token, err := generateStreamToken()
	if err != nil {
		return "", err
	}

	hlsMgr.tokensMu.Lock()
	if existing, exists := hlsMgr.sourceTokens[sourceID]; exists {
		hlsMgr.tokensMu.Unlock()
		return existing, nil
	}
	hlsMgr.tokens[token] = sourceID
	hlsMgr.sourceTokens[sourceID] = token
	hlsMgr.tokensMu.Unlock()

	return token, nil
}

// resolveStreamToken looks up the sourceID for a given stream token.
// Returns empty string if the token is not found.
func resolveStreamToken(token string) string {
	hlsMgr.tokensMu.RLock()
	defer hlsMgr.tokensMu.RUnlock()
	return hlsMgr.tokens[token]
}

// removeStreamToken removes the token mappings for a sourceID.
func removeStreamToken(sourceID string) {
	hlsMgr.tokensMu.Lock()
	defer hlsMgr.tokensMu.Unlock()
	if token, exists := hlsMgr.sourceTokens[sourceID]; exists {
		delete(hlsMgr.tokens, token)
		delete(hlsMgr.sourceTokens, sourceID)
	}
}

// StartHLSStream initiates an HLS stream for a specific audio source
// POST /api/v2/streams/hls/:sourceID/start
func (c *Handler) StartHLSStream(ctx echo.Context) error {
	sourceID, err := c.validateAndDecodeSourceID(ctx)
	if err != nil {
		return err
	}

	// Bind optional request body for session_id
	var req HLSSessionRequest
	if err := ctx.Bind(&req); err != nil {
		apicore.GetLogger().Debug("Failed to bind start request body", logger.Error(err))
	}

	clientID := c.resolveClientID(ctx, req.SessionID)

	// Check for force restart query param
	forceRestart := ctx.QueryParam("force") == queryValueTrue

	// Only allow force restart for authenticated users to prevent DoS
	// (force tears down the muxer and rebuilds the stream from scratch)
	if forceRestart && !c.isClientAuthenticated(ctx) {
		forceRestart = false
	}

	c.LogAPIRequest(ctx, logger.LogLevelInfo, "HLS stream start requested",
		logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
		logger.String("client_id", clientID),
		logger.Bool("force_restart", forceRestart))

	// Verify source exists by checking for a capture buffer
	eng := c.Engine.Load()
	if eng == nil {
		return c.HandleError(ctx, nil, "Audio engine not initialized", http.StatusServiceUnavailable)
	}
	if _, bufErr := eng.BufferManager().CaptureBuffer(sourceID); bufErr != nil {
		return c.respondNoCaptureBuffer(ctx, eng, sourceID)
	}

	// Check for existing healthy stream first (reuse if possible)
	if existingStream := c.getHLSStream(sourceID); existingStream != nil && !forceRestart {
		// Existing stream found - register client and reuse it
		c.updateHLSActivity(sourceID, clientID, "stream_join", hlsNewStreamGracePeriod)
		c.LogAPIRequest(ctx, logger.LogLevelInfo, "Reusing existing HLS stream",
			logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
			logger.String("client_id", clientID))
		return c.buildHLSStreamResponse(ctx, sourceID, existingStream)
	}

	// Create or get the HLS stream (force-restart uses atomic cleanup+create)
	var stream *HLSStreamInfo
	if forceRestart {
		stream, err = c.forceCreateHLSStream(sourceID)
	} else {
		stream, err = c.getOrCreateHLSStream(sourceID)
	}
	if err != nil {
		c.LogAPIRequest(ctx, logger.LogLevelError, "Failed to create HLS stream",
			logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
			logger.Error(err))
		return c.HandleError(ctx, err, "Failed to start audio stream", http.StatusInternalServerError)
	}

	// Register client AFTER stream creation so force-restart gets clean tracking
	// (forceCreateHLSStream clears stale tracking before creating the new stream)
	c.updateHLSActivity(sourceID, clientID, "stream_start", hlsNewStreamGracePeriod)

	// Check if playlist is ready
	playlistReady := c.waitForHLSPlaylist(ctx, sourceID, stream)

	return c.buildHLSStreamResponse(ctx, sourceID, stream, playlistReady)
}

// respondNoCaptureBuffer builds a diagnostic 404 for a live-audio start request
// whose source has no capture buffer. Before this, StartHLSStream returned a bare
// 404 with a nil error and no diagnostics, so users saw an error popup with "no
// logs" (issue #3766). Here we gather the currently registered source IDs and the
// source IDs that actually have a capture buffer (the ones that can back a live
// stream right now), so the WARN log and the returned error explain what went
// wrong and what could be started instead.
//
// All logged identifiers are opaque source-ID hashes (e.g. "rtsp_d13dfe45"), which
// are safe to log. Raw connection strings are never logged; the requested source
// ID is additionally passed through privacy.SanitizeRTSPUrl as defense in depth.
func (c *Handler) respondNoCaptureBuffer(ctx echo.Context, eng *engine.AudioEngine, sourceID string) error {
	// Registered sources from the source registry (all configured sources).
	var registeredIDs []string
	if registry := eng.Registry(); registry != nil {
		sources := registry.List()
		registeredIDs = make([]string, 0, len(sources))
		for _, src := range sources {
			registeredIDs = append(registeredIDs, src.ID)
		}
	}

	// Source IDs that currently have a capture buffer allocated: these are the
	// ones that can actually back a live stream right now.
	health := eng.BufferManager().CaptureBufferHealthAll()
	captureBufferIDs := make([]string, 0, len(health))
	for _, h := range health {
		captureBufferIDs = append(captureBufferIDs, h.SourceID)
	}

	// registeredCount is the number of configured/registered sources, not the
	// number usable for streaming right now (that is capture_buffer_count).
	registeredCount := len(registeredIDs)

	diagErr := errors.Newf("live audio source not available: no capture buffer for requested source").
		Component("api").
		Category(errors.CategoryAudioSource).
		Context("requested_source", privacy.SanitizeRTSPUrl(sourceID)).
		Context("registered_source_count", registeredCount).
		Context("capture_buffer_count", len(captureBufferIDs)).
		Build()

	c.LogAPIRequest(ctx, logger.LogLevelWarn, "Live audio start failed: source has no capture buffer",
		logger.String("requested_source", privacy.SanitizeRTSPUrl(sourceID)),
		logger.Int("registered_sources", registeredCount),
		logger.Int("capture_buffers", len(captureBufferIDs)),
		logger.String("registered_source_ids", strings.Join(registeredIDs, ",")),
		logger.String("capture_buffer_ids", strings.Join(captureBufferIDs, ",")))

	return c.HandleError(ctx, diagErr,
		"Audio source not available for live streaming. If you recently changed audio settings, the stream may need a moment or a restart to become available.",
		http.StatusNotFound)
}

// buildHLSStreamResponse constructs the HLS stream status response
func (c *Handler) buildHLSStreamResponse(ctx echo.Context, sourceID string, stream *HLSStreamInfo, playlistReady ...bool) error {
	// Get client count
	clientCount := getStreamClientCount(sourceID)

	// Generate or retrieve stream token for secure URL access
	token, err := getOrCreateStreamToken(sourceID)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to generate stream token", http.StatusInternalServerError)
	}

	// Build the API URL using the stream token (not the sourceID)
	playlistURL := hlsPlaylistURL(token)

	// Determine playlist ready status
	var isReady bool
	if len(playlistReady) > 0 {
		isReady = playlistReady[0]
	} else {
		// Check playlist file existence if not explicitly provided
		isReady = c.checkHLSPlaylistReady(stream)
	}

	status := "starting"
	if isReady {
		status = "ready"
		c.LogAPIRequest(ctx, logger.LogLevelInfo, "HLS stream ready",
			logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
			logger.String("stream_token", token[:8]+"..."),
			logger.String("playlist_url", playlistURL))
	}

	// Format stream epoch as ISO8601 if set
	var epochStr string
	if !stream.streamEpoch.IsZero() {
		epochStr = stream.streamEpoch.UTC().Format(time.RFC3339Nano)
	}

	return ctx.JSON(http.StatusOK, HLSStreamStatus{
		Status:        status,
		Source:        url.PathEscape(sourceID),
		StreamToken:   token,
		PlaylistURL:   playlistURL,
		ActiveClients: clientCount,
		PlaylistReady: isReady,
		StreamEpoch:   epochStr,
	})
}

// checkHLSPlaylistReady reports whether the muxer has advertised enough segments
// for immediate playback.
func (c *Handler) checkHLSPlaylistReady(stream *HLSStreamInfo) bool {
	if stream == nil || !stream.isNative() {
		return false
	}
	return stream.mux.Ready(hlsMinSegments)
}

// StopHLSStream stops an HLS stream for a specific client
// POST /api/v2/streams/hls/:sourceID/stop
func (c *Handler) StopHLSStream(ctx echo.Context) error {
	sourceID, err := c.validateAndDecodeSourceID(ctx)
	if err != nil {
		return err
	}

	// Bind optional request body for session_id
	var req HLSSessionRequest
	if err := ctx.Bind(&req); err != nil {
		apicore.GetLogger().Debug("Failed to bind stop request body", logger.Error(err))
	}

	clientID := c.resolveClientID(ctx, req.SessionID)

	c.LogAPIRequest(ctx, logger.LogLevelInfo, "HLS stream stop requested",
		logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
		logger.String("client_id", clientID))

	// Remove client from tracking
	lastClient := c.removeHLSClient(sourceID, clientID)

	// If last client, stop the stream
	if lastClient {
		c.stopHLSStream(sourceID, "last client disconnected")
	}

	return ctx.JSON(http.StatusOK, map[string]string{
		"status": "stopped",
	})
}

// HLSHeartbeat processes client heartbeat to keep streams alive
// POST /api/v2/streams/hls/heartbeat
func (c *Handler) HLSHeartbeat(ctx echo.Context) error {
	var heartbeat HLSHeartbeatRequest
	if err := ctx.Bind(&heartbeat); err != nil {
		return c.HandleError(ctx, err, "Invalid heartbeat format", http.StatusBadRequest)
	}

	// Resolve stream token to sourceID
	sourceID := resolveStreamToken(heartbeat.StreamToken)
	if sourceID == "" {
		// Return OK silently to avoid revealing the token mechanism
		return ctx.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}

	clientID := c.resolveClientID(ctx, heartbeat.SessionID)

	// Handle disconnection announcements
	if ctx.QueryParam("disconnect") == queryValueTrue || ctx.QueryParam("status") == "disconnect" {
		return c.handleHLSDisconnect(ctx, sourceID, clientID)
	}

	// Validate stream exists
	if !c.hlsStreamExists(sourceID) {
		if hlsMgr.verboseLogging {
			c.LogAPIRequest(ctx, logger.LogLevelWarn, "Heartbeat for non-existent stream",
				logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)))
		}
		return ctx.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}

	// Update activity
	c.updateHLSActivity(sourceID, clientID, "heartbeat")

	return ctx.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// GetHLSStatus returns the status of all active HLS streams
// GET /api/v2/streams/hls/status
func (c *Handler) GetHLSStatus(ctx echo.Context) error {
	hlsMgr.streamsMu.RLock()
	// Copy stream references under lock to minimize lock duration
	streamsCopy := make(map[string]*HLSStreamInfo, len(hlsMgr.streams))
	maps.Copy(streamsCopy, hlsMgr.streams)
	hlsMgr.streamsMu.RUnlock()

	streams := make([]HLSStreamStatus, 0, len(streamsCopy))
	for sourceID, stream := range streamsCopy {
		encodedSourceID := url.PathEscape(sourceID)

		// Use token-based playlist URL if token exists
		var playlistURL string
		hlsMgr.tokensMu.RLock()
		token, hasToken := hlsMgr.sourceTokens[sourceID]
		hlsMgr.tokensMu.RUnlock()
		if hasToken {
			playlistURL = hlsPlaylistURL(token)
		}

		// Check actual playlist readiness instead of hardcoding true
		playlistReady := c.checkHLSPlaylistReady(stream)

		// Intentionally omit StreamToken from status response to prevent token leakage
		streams = append(streams, HLSStreamStatus{
			Status:        "active",
			Source:        encodedSourceID,
			PlaylistURL:   playlistURL,
			ActiveClients: getStreamClientCount(sourceID),
			PlaylistReady: playlistReady,
		})
	}

	return ctx.JSON(http.StatusOK, map[string]any{
		"streams": streams,
		"count":   len(streams),
	})
}

// ServeHLSPlaylist serves the HLS playlist file
// GET /api/v2/streams/hls/t/:streamToken/playlist.m3u8
func (c *Handler) ServeHLSPlaylist(ctx echo.Context) error {
	streamToken := ctx.Param("streamToken")
	sourceID := resolveStreamToken(streamToken)
	if sourceID == "" {
		return c.HandleError(ctx, nil, "Stream not found", http.StatusNotFound)
	}

	// Get stream info
	stream := c.getHLSStream(sourceID)
	if stream == nil {
		return c.HandleError(ctx, nil, "Stream not found", http.StatusNotFound)
	}

	// Update stream-level activity (no client registration - lifecycle managed by start/stop/heartbeat)
	c.updateStreamActivity(sourceID)

	return c.serveNativePlaylist(ctx, sourceID, stream)
}

// ServeHLSContent serves HLS segment files
// GET /api/v2/streams/hls/t/:streamToken/*
func (c *Handler) ServeHLSContent(ctx echo.Context) error {
	streamToken := ctx.Param("streamToken")
	sourceID := resolveStreamToken(streamToken)
	if sourceID == "" {
		return c.HandleError(ctx, nil, "Stream not found", http.StatusNotFound)
	}

	requestPath := ctx.Param("*")

	// Decode URL path
	decodedPath, err := url.PathUnescape(requestPath)
	if err != nil {
		return c.HandleError(ctx, err, "Invalid URL encoding", http.StatusBadRequest)
	}

	// Get stream info
	stream := c.getHLSStream(sourceID)
	if stream == nil {
		return c.HandleError(ctx, nil, "Stream not found", http.StatusNotFound)
	}

	// Update stream-level activity (no client registration - lifecycle managed by start/stop/heartbeat)
	c.updateStreamActivity(sourceID)

	// Log client connection (rate-limited)
	c.logHLSClientConnection(sourceID, ctx.RealIP(), decodedPath)

	return c.serveNativeContent(ctx, stream, decodedPath)
}

// Helper methods

// validateAndDecodeSourceID extracts and validates the sourceID parameter
func (c *Handler) validateAndDecodeSourceID(ctx echo.Context) (string, error) {
	sourceIDParam := ctx.Param("sourceID")

	decodedSourceID, err := url.PathUnescape(sourceIDParam)
	if err != nil {
		return "", c.HandleError(ctx, err, "Invalid source ID encoding", http.StatusBadRequest)
	}

	if decodedSourceID == "" {
		return "", c.HandleError(ctx, nil, "Source ID is required", http.StatusBadRequest)
	}

	return decodedSourceID, nil
}

// generateClientID creates a standardized client identifier
// Uses RemoteAddr (not RealIP) for consistency with audio_level.go to prevent IP spoofing
func (c *Handler) generateClientID(ctx echo.Context) string {
	clientIP := c.extractRemoteAddr(ctx)
	userAgent := ctx.Request().Header.Get("User-Agent")

	clientType := "HLSPlayer"
	switch {
	case strings.Contains(userAgent, "Mozilla"):
		clientType = "Browser"
	case strings.Contains(userAgent, "VLC"):
		clientType = "VLC"
	case strings.Contains(userAgent, "FFmpeg"):
		clientType = "FFmpeg"
	}

	return clientIP + "-" + clientType
}

// hlsSessionIDMinLen and hlsSessionIDMaxLen bound the length of a client-supplied
// HLS session identifier that resolveClientID will accept. The lower bound keeps
// trivially short tokens out; the upper bound caps how much client-controlled data
// can enter the client-tracking map.
const (
	hlsSessionIDMinLen = 8
	hlsSessionIDMaxLen = 128
)

// hlsSessionClientPrefix namespaces client IDs derived from a client-supplied
// session ID. Without it a crafted session ID could equal the User-Agent type
// segment that generateClientID appends (for example "HLSPlayer"), letting a
// session-based client collide with a fallback client on the same IP. The prefix
// keeps the two identity spaces disjoint regardless of the fallback type strings.
const hlsSessionClientPrefix = "sid"

// isSafeSessionID reports whether a client-supplied session identifier is safe to
// embed in a client ID. It accepts bounded-length tokens containing only
// URL/filename-safe characters ([A-Za-z0-9._-]). This admits canonical UUIDs as
// well as other unique tokens a client may send (an alternate or older client may
// use a non-canonical but still unique token), while rejecting empty, oversized,
// or structurally unexpected input.
func isSafeSessionID(sessionID string) bool {
	if len(sessionID) < hlsSessionIDMinLen || len(sessionID) > hlsSessionIDMaxLen {
		return false
	}
	for _, ch := range sessionID {
		switch {
		case ch >= 'a' && ch <= 'z',
			ch >= 'A' && ch <= 'Z',
			ch >= '0' && ch <= '9',
			ch == '.', ch == '_', ch == '-':
			// allowed character
		default:
			return false
		}
	}
	return true
}

// resolveClientID returns a client identifier, preferring a client-supplied
// session ID when it is present and safe. The session ID is prefixed with the
// remote IP so a client can never impersonate another IP's clients, and it lets
// multiple live-audio consumers on the same host (for example the HLS player and
// the dashboard spectrogram) be tracked as distinct clients instead of collapsing
// onto a single IP+UA identity. Missing or unsafe session IDs fall back to
// IP+UA-based identification (used by non-browser clients such as VLC and FFmpeg).
func (c *Handler) resolveClientID(ctx echo.Context, sessionID string) string {
	if isSafeSessionID(sessionID) {
		// Namespace session-derived IDs so a crafted session ID can never collide
		// with a generateClientID fallback (see hlsSessionClientPrefix).
		return c.extractRemoteAddr(ctx) + "-" + hlsSessionClientPrefix + "-" + sessionID
	}
	// Fallback for non-browser clients (VLC, FFmpeg, etc.) or unsafe session IDs
	return c.generateClientID(ctx)
}

// setHLSHeaders sets common HLS response headers
// Note: CORS is handled by middleware at the v2 group level
func (c *Handler) setHLSHeaders(ctx echo.Context) {
	// HLS-specific headers only; CORS is handled by middleware
}

// getEffectiveSegmentLength returns the configured segment length with defaults and limits applied
func (c *Handler) getEffectiveSegmentLength() int {
	segmentLength := c.CurrentSettings().WebServer.LiveStream.SegmentLength
	switch {
	case segmentLength < hlsMinSegmentLen:
		return hlsDefaultSegmentLen // Default
	case segmentLength > hlsMaxSegmentLen:
		return hlsMaxSegmentLen
	default:
		return segmentLength
	}
}

// setHLSContentType sets appropriate content type based on file extension
func (c *Handler) setHLSContentType(ctx echo.Context, path string) {
	switch filepath.Ext(path) {
	case ".ts":
		ctx.Response().Header().Set("Content-Type", "video/mp2t")
		ctx.Response().Header().Set("Cache-Control", "public, max-age=60")
	case ".m4s":
		ctx.Response().Header().Set("Content-Type", "video/iso.segment")
		ctx.Response().Header().Set("Cache-Control", "public, max-age=60")
	case ".mp4":
		ctx.Response().Header().Set("Content-Type", "audio/mp4")
		ctx.Response().Header().Set("Cache-Control", "public, max-age=3600")
	default:
		ctx.Response().Header().Set("Content-Type", "application/octet-stream")
	}
}

// lastFreshnessCheck tracks the last time segment freshness was checked per source
// to avoid adding I/O load on every playlist poll.
var lastFreshnessCheck sync.Map // sourceID → time.Time

// Stream management methods

// getOrCreateHLSStream gets existing stream or creates a new one.
// Uses singleflight to serialize creation per sourceID, preventing concurrent
// goroutines from racing on muxer construction and route registration.
func (c *Handler) getOrCreateHLSStream(sourceID string) (*HLSStreamInfo, error) {
	// Fast-path: existing stream (no singleflight overhead)
	if stream := c.getHLSStream(sourceID); stream != nil {
		return stream, nil
	}

	// Serialize creation per sourceID
	result, err, _ := hlsMgr.streamCreate.Do(sourceID, func() (any, error) {
		// Re-check: another goroutine may have created it while we waited
		if stream := c.getHLSStream(sourceID); stream != nil {
			return stream, nil
		}
		return c.createHLSStream(sourceID)
	})
	if err != nil {
		return nil, err
	}
	return result.(*HLSStreamInfo), nil
}

// forceCreateHLSStream cleans up any existing stream and creates a new one,
// atomically under the singleflight gate to prevent cleanup/creation races.
func (c *Handler) forceCreateHLSStream(sourceID string) (*HLSStreamInfo, error) {
	result, err, _ := hlsMgr.streamCreate.Do(sourceID, func() (any, error) {
		// Phase 1: Synchronous cleanup under singleflight serialization.
		// This prevents a concurrent request from creating a new stream
		// while the old one's directory is still being deleted.
		c.cleanupExistingHLSStream(sourceID)

		// Phase 2: Clear stale tracking data (clients, activity) from the
		// old stream so the replacement starts with a clean slate.
		c.cleanupStreamTracking(sourceID)

		// Phase 3: Create new stream (directory path is deterministic,
		// so cleanup must complete before creation).
		return c.createHLSStream(sourceID)
	})
	if err != nil {
		return nil, err
	}
	return result.(*HLSStreamInfo), nil
}

// createHLSStream creates a new HLS stream (called under singleflight serialization).
//
// Every live HLS stream is served by the in-process muxer; there is no FFmpeg
// alternative, so this delegates straight to the native constructor.
func (c *Handler) createHLSStream(sourceID string) (*HLSStreamInfo, error) {
	return c.createNativeHLSStream(sourceID)
}

// publishHLSStream registers the stream and seeds its activity tracking.
// Singleflight guarantees no concurrent creation for this sourceID.
func (c *Handler) publishHLSStream(sourceID string, stream *HLSStreamInfo) {
	hlsMgr.streamsMu.Lock()
	hlsMgr.streams[sourceID] = stream
	hlsMgr.streamsMu.Unlock()

	c.updateHLSActivity(sourceID, "", "stream_creation")
}

// watchHLSStreamContext starts the goroutine that cleans up once the stream's
// context is cancelled. It verifies stream identity before cleaning up, so a
// force-restarted stream is not killed by the old stream's cancellation.
func (c *Handler) watchHLSStreamContext(sourceID string, stream *HLSStreamInfo) {
	go func(s *HLSStreamInfo) {
		<-s.ctx.Done()
		hlsMgr.streamsMu.Lock()
		current, exists := hlsMgr.streams[sourceID]
		if exists && current == s {
			delete(hlsMgr.streams, sourceID)
			removeStreamToken(sourceID)
			hlsMgr.streamsMu.Unlock()
			c.performHLSCleanup(sourceID, s, "context cancelled")
		} else {
			hlsMgr.streamsMu.Unlock()
		}
	}(stream)
}

// audioChunk is one delivery of PCM from the router, carrying the capture time
// alongside the samples.
//
// The timestamp lets the muxer anchor EXT-X-PROGRAM-DATE-TIME to when audio was
// actually captured. Deriving that from the sample count instead would report
// wall-clock times that never happened whenever a source stalls and resumes,
// silently and permanently, because a stalled source produces no samples for the
// gap.
type audioChunk struct {
	data      []byte
	timestamp time.Time
}

// audioFeed is the per-stream PCM queue between the router, which produces
// chunks on its dispatch goroutine, and an HLS feed loop, which drains them.
//
// The queue is bounded by bytes of queued PCM rather than by chunk count,
// because chunk size is a property of the producer, not of HLS: the FFmpeg
// ingest path emits ffmpegBufferSize (32 KiB) per frame, a directly captured
// sound card emits miniaudio's default 10 ms period (under 1 KiB at 48 kHz
// mono), and a route whose source rate differs from the consumer rate has a
// resampler in between that changes the size again. A chunk-count bound
// therefore means a different memory ceiling and a different amount of buffered
// audio for every source; one small enough to bound RTSP frames leaves a sound
// card with a fraction of a second of slack. A byte budget gives every producer
// the same ceiling and the same wall-clock depth.
//
// Chunks leave the queue two ways, and each one has to be accounted for. The
// producer evicts the oldest to make room: in makeRoom when the byte budget is
// exceeded, and in Write's overflow arm when the channel's slots fill before the
// byte budget does (which is the only eviction path when there is no budget).
// The consumer receives. The invariant is simply that every
// path removing a chunk from ch calls release(len(chunk.data)) for it; skipping
// it leaves the producer believing the queue holds bytes that are already gone,
// and the queue eventually evicts on every write.
type audioFeed struct {
	ch chan audioChunk

	// maxBytes is the byte budget. hlsFeedQueueUnbounded disables it and leaves
	// the channel's slot count as the only bound.
	maxBytes int64

	// queued is the PCM currently sitting in ch. It is only ever advisory:
	// nothing serializes a producer's enqueue against a consumer's release, so
	// it can lag reality by a chunk in either direction. That is bounded and
	// harmless, since one chunk of slack on a multi-second budget changes
	// neither the memory ceiling nor the buffered depth in any way that matters.
	queued atomic.Int64
}

// release reports that a chunk of n bytes has been taken off the queue.
func (f *audioFeed) release(n int) {
	f.queued.Add(-int64(n))
}

// evictOldest drops the single oldest queued chunk if there is one, releasing
// its bytes, and reports whether it evicted anything. It is the one place a
// chunk leaves the queue on the producer side, so the receive and its matching
// release stay welded together here rather than being repeated at each caller:
// makeRoom's byte-budget loop and Write's slot-exhaustion arm both go through it.
func (f *audioFeed) evictOldest() bool {
	select {
	case old := <-f.ch:
		f.release(len(old.data))
		return true
	default:
		return false
	}
}

// makeRoom evicts the oldest chunks until n more bytes fit within the byte
// budget, returning the number of chunks it dropped.
//
// Dropping the oldest is the right policy for a live stream: the queue only
// grows when the encoder falls behind, and audio that far behind the live edge
// is past the point where any player would still ask for it.
//
// It stops early when the queue runs empty, which happens when a single chunk
// is larger than the whole budget. Refusing that chunk would silence the stream
// permanently rather than briefly, so it is admitted and the budget is exceeded
// by that one chunk.
func (f *audioFeed) makeRoom(n int) int {
	if f.maxBytes <= 0 {
		return 0
	}
	dropped := 0
	for f.queued.Load()+int64(n) > f.maxBytes {
		if !f.evictOldest() {
			return dropped
		}
		dropped++
	}
	return dropped
}

// hlsConsumer implements audiocore.AudioConsumer for HLS streaming.
// It forwards audio frames to a bounded feed queue (see audioFeed) for encoding.
type hlsConsumer struct {
	id       string
	sourceID string
	feed     *audioFeed
	rate     int
	depth    int
	channels int
	closed   atomic.Bool

	// Drop tracking for diagnostics
	dropCount   int64
	lastDropLog time.Time
	dropMu      sync.Mutex
}

// ID returns the unique identifier for this consumer.
func (h *hlsConsumer) ID() string { return h.id }

// SampleRate returns the expected sample rate in Hz.
func (h *hlsConsumer) SampleRate() int { return h.rate }

// BitDepth returns the expected bit depth.
func (h *hlsConsumer) BitDepth() int { return h.depth }

// Channels returns the expected channel count.
func (h *hlsConsumer) Channels() int { return h.channels }

// Write delivers audio frame data to the HLS feed queue.
//
// The frame data is copied before being sent so the caller (the audio router)
// may safely reuse or recycle the underlying slice after Write returns. The
// feed loop reads asynchronously from the queue, so any retained slice header
// would race with the caller's buffer reuse.
//
// The send never blocks. A feed loop that has fallen behind costs the stream
// its oldest queued audio, not the router its dispatch goroutine.
func (h *hlsConsumer) Write(frame audiocore.AudioFrame) error { //nolint:gocritic // hugeParam: signature required by AudioConsumer interface
	if h.closed.Load() {
		return audiocore.ErrConsumerClosed
	}

	// Copy once up front. Every send arm below needs an owned slice.
	chunk := audioChunk{data: slices.Clone(frame.Data), timestamp: frame.Timestamp}
	chunkBytes := int64(len(chunk.data))

	// Evict down to the byte budget first, so the queue is bounded by the PCM
	// it holds rather than by the slot count of the channel behind it.
	dropped := h.feed.makeRoom(len(chunk.data))

	select {
	case h.feed.ch <- chunk:
		h.feed.queued.Add(chunkBytes)
	default:
		// Slots exhausted while still inside the byte budget, which is what a
		// stream of chunks far smaller than the budget looks like. Drop the
		// oldest to make room, on the same reasoning as makeRoom.
		if h.feed.evictOldest() {
			dropped++
		}
		select {
		case h.feed.ch <- chunk:
			h.feed.queued.Add(chunkBytes)
		default:
			// Unreachable under the single-producer model (the eviction above,
			// or the consumer draining, leaves a free slot before this retry),
			// but count the loss rather than trust that invariant: if a second
			// writer is ever added, the drop stays honest instead of silently
			// under-reporting.
			dropped++
		}
	}

	if dropped > 0 {
		h.dropMu.Lock()
		h.dropCount += int64(dropped)
		now := time.Now()
		if now.Sub(h.lastDropLog) >= hlsDropLogInterval {
			sanitizedID := privacy.SanitizeRTSPUrl(h.sourceID)
			apicore.GetLogger().Warn("HLS audio data dropped: feed queue full",
				logger.String("source_id", sanitizedID),
				logger.Int64("drops_since_last_log", h.dropCount),
				logger.Int64("queued_bytes", h.feed.queued.Load()),
				logger.Int64("max_queued_bytes", h.feed.maxBytes))
			h.dropCount = 0
			h.lastDropLog = now
		}
		h.dropMu.Unlock()
	}
	return nil
}

// Close marks the consumer as closed.
func (h *hlsConsumer) Close() error {
	h.closed.Store(true)
	return nil
}

// setupAudioCallback sets up the audio feed queue using the AudioRouter.
// sampleRate is the rate the consumer declares; the router inserts a resampler
// whenever the source differs from it. maxQueuedBytes is the queue's byte
// budget, or hlsFeedQueueUnbounded to leave the channel's slot count as the only
// bound; see audioFeed for why the bound is in bytes rather than chunks.
func (c *Handler) setupAudioCallback(sourceID string, sampleRate int, maxQueuedBytes int64) (feed *audioFeed, cleanup func(), err error) {
	feed = &audioFeed{
		ch:       make(chan audioChunk, defaultReadBufferSize),
		maxBytes: maxQueuedBytes,
	}

	// hlsConsumerChannels is what this consumer declares to the router, and the
	// native path derives its muxer sample-frame size from nativeHLSChannels.
	// They must be equal, and the failure when they are not is silent rather
	// than loud: with more consumer channels than muxer channels the interleaved
	// frames still divide evenly into the smaller sample frame, so every write is
	// ACCEPTED and the muxer reads L,R,L,R as consecutive mono samples. That
	// plays at double speed an octave up with no error anywhere.
	//
	// Both subtractions are needed. One alone only rejects the direction that
	// goes negative, which is the direction that would have failed loudly anyway.
	const _ = uint(hlsConsumerChannels-nativeHLSChannels) + uint(nativeHLSChannels-hlsConsumerChannels)

	consumerID := fmt.Sprintf("hls_%s_%s", privacy.SanitizeStreamUrl(sourceID), uuid.New().String()[:8])

	consumer := &hlsConsumer{
		id:       consumerID,
		sourceID: sourceID,
		feed:     feed,
		rate:     sampleRate,
		depth:    conf.BitDepth,
		channels: hlsConsumerChannels,
	}

	// Load the engine once for all registry/router operations in this section.
	eng := c.Engine.Load()
	if eng == nil {
		return nil, nil, fmt.Errorf("audio engine not initialized")
	}

	// Look up per-source gain from the registry so HLS listeners
	// hear the same gain-adjusted audio as the analysis pipeline.
	gainDB, _ := eng.Registry().GetGain(sourceID)

	// Resolve EQ filter chain for this source so HLS listeners
	// hear the same filtered audio as the analysis pipeline.
	settings := conf.Setting()
	src, _ := eng.Registry().Get(sourceID)
	sourceName := sourceID
	if src != nil {
		sourceName = src.DisplayName
	}
	eqChain := equalizer.ResolveAndBuildFilterChain(settings, sourceName, sampleRate)

	// Determine the actual sample rate of the capture source
	sourceSampleRate := sampleRate // Fallback to target rate
	if src != nil && src.SampleRate > 0 {
		sourceSampleRate = src.SampleRate
	} else {
		apicore.GetLogger().Warn("Audio source sample rate unavailable, falling back to HLS target rate", logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)), logger.Int("fallback_rate", sampleRate))
	}

	// Add route on the AudioRouter
	if routeErr := eng.Router().AddRoute(sourceID, consumer, sourceSampleRate, gainDB, eqChain); routeErr != nil {
		return nil, nil, fmt.Errorf("failed to add HLS route: %w", routeErr)
	}

	apicore.GetLogger().Debug("Registered HLS audio route", logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)), logger.String("consumer_id", consumerID))

	cleanup = func() {
		eng.Router().RemoveRoute(sourceID, consumerID)
		apicore.GetLogger().Debug("Removed HLS audio route", logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)), logger.String("consumer_id", consumerID))
	}

	return feed, cleanup, nil
}

// audioFeedResources holds the resources the feed loop owns: the PCM queue
// from the router and the cleanup that unregisters the route.
type audioFeedResources struct {
	feed    *audioFeed // Feed queue from the router (byte-budgeted; see audioFeed)
	cleanup func()     // Releases resources (unregisters the router callback)
}

// Activity and client management

// updateHLSActivity records activity for a stream
func (c *Handler) updateHLSActivity(sourceID, clientID, activityType string, gracePeriod ...time.Duration) {
	// Track client
	if clientID != "" {
		hlsMgr.clientsMu.Lock()
		if hlsMgr.clients[sourceID] == nil {
			hlsMgr.clients[sourceID] = make(map[string]bool)
		}
		hlsMgr.clients[sourceID][clientID] = true
		hlsMgr.clientsMu.Unlock()
	}

	// Update per-client activity for premature disconnect detection
	if clientID != "" {
		hlsMgr.activityMu.Lock()
		hlsMgr.clientActivity[sourceID+":"+clientID] = time.Now()
		hlsMgr.activityMu.Unlock()
	}

	// Update activity timestamp
	hlsMgr.activityMu.Lock()
	extraTime := time.Duration(0)
	if len(gracePeriod) > 0 {
		extraTime = gracePeriod[0]
	}
	hlsMgr.activity[sourceID] = time.Now().Add(extraTime)
	hlsMgr.activityMu.Unlock()
}

// updateStreamActivity updates stream-level activity without registering a client.
// Used by playlist/segment handlers where session context is not available,
// preventing ghost client entries from non-session-aware traffic.
func (c *Handler) updateStreamActivity(sourceID string) {
	hlsMgr.activityMu.Lock()
	hlsMgr.activity[sourceID] = time.Now()
	hlsMgr.activityMu.Unlock()
}

// getHLSStream returns the stream info if it exists
func (c *Handler) getHLSStream(sourceID string) *HLSStreamInfo {
	hlsMgr.streamsMu.RLock()
	defer hlsMgr.streamsMu.RUnlock()
	return hlsMgr.streams[sourceID]
}

// hlsStreamExists checks if a stream exists
func (c *Handler) hlsStreamExists(sourceID string) bool {
	hlsMgr.streamsMu.RLock()
	defer hlsMgr.streamsMu.RUnlock()
	_, exists := hlsMgr.streams[sourceID]
	return exists
}

// removeHLSClient removes a client from tracking, returns true if last client
func (c *Handler) removeHLSClient(sourceID, clientID string) bool {
	hlsMgr.clientsMu.Lock()
	defer hlsMgr.clientsMu.Unlock()

	if clients, exists := hlsMgr.clients[sourceID]; exists {
		delete(clients, clientID)
		if len(clients) == 0 {
			delete(hlsMgr.clients, sourceID)
			return true
		}
	}
	return false
}

// handleHLSDisconnect handles client disconnect announcements
func (c *Handler) handleHLSDisconnect(ctx echo.Context, sourceID, clientID string) error {
	// Check for premature disconnect
	hlsMgr.activityMu.Lock()
	if lastTime, exists := hlsMgr.clientActivity[sourceID+":"+clientID]; exists {
		if time.Since(lastTime) < hlsPrematureDisconnectThreshold {
			hlsMgr.activityMu.Unlock()
			c.LogAPIRequest(ctx, logger.LogLevelWarn, "Ignoring premature disconnect",
				logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)))
			c.updateHLSActivity(sourceID, clientID, "continued-connection")
			return ctx.JSON(http.StatusOK, map[string]string{"status": "ok"})
		}
	}
	hlsMgr.activityMu.Unlock()

	c.LogAPIRequest(ctx, logger.LogLevelInfo, "Client announced disconnection",
		logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
		logger.String("client_id", clientID))

	c.removeHLSClient(sourceID, clientID)
	return ctx.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// Cleanup methods

// cleanupExistingHLSStream cleans up an existing stream before restart
func (c *Handler) cleanupExistingHLSStream(sourceID string) {
	hlsMgr.streamsMu.Lock()
	stream, exists := hlsMgr.streams[sourceID]
	if !exists {
		hlsMgr.streamsMu.Unlock()
		return
	}

	apicore.GetLogger().Debug("Cleaning up existing stream", logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)))

	if stream.cancel != nil {
		stream.cancel()
	}

	delete(hlsMgr.streams, sourceID)
	removeStreamToken(sourceID)
	hlsMgr.streamsMu.Unlock()

	// Deleting the stream above means the context watcher will find a different
	// (or missing) entry and skip performHLSCleanup, so the muxer has to be
	// closed here or it is never closed at all.
	closeNativeMux(sourceID, stream)
}

// RestartHLSStreams stops all active HLS streams so they restart with fresh settings.
func (c *Handler) RestartHLSStreams() {
	hlsMgr.streamsMu.Lock()
	sourceIDs := slices.Collect(maps.Keys(hlsMgr.streams))
	hlsMgr.streamsMu.Unlock()

	for _, id := range sourceIDs {
		c.stopHLSStream(id, "settings changed")
	}
}

// stopHLSStream stops a stream with a specific reason
func (c *Handler) stopHLSStream(sourceID, reason string) {
	hlsMgr.streamsMu.Lock()
	stream, exists := hlsMgr.streams[sourceID]
	if !exists {
		hlsMgr.streamsMu.Unlock()
		return
	}

	apicore.GetLogger().Info("Stopping HLS stream", logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)), logger.String("reason", reason))
	delete(hlsMgr.streams, sourceID)
	removeStreamToken(sourceID)
	hlsMgr.streamsMu.Unlock()

	c.performHLSCleanup(sourceID, stream, reason)
}

// performHLSCleanup performs the actual cleanup of stream resources
func (c *Handler) performHLSCleanup(sourceID string, stream *HLSStreamInfo, reason string) {
	apicore.GetLogger().Debug("Performing HLS cleanup", logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)), logger.String("reason", reason))

	// Cancel context
	if stream.cancel != nil {
		stream.cancel()
	}

	// There is no process, FIFO or output directory: closing the muxer (which
	// flushes the encoder tail) and dropping the tracking data is the whole of
	// cleanup.
	closeNativeMux(sourceID, stream)

	// Clean up tracking data
	c.cleanupStreamTracking(sourceID)

	apicore.GetLogger().Debug("HLS stream cleanup completed", logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)))
}

// cleanupStreamTracking removes all tracking data for a stream.
func (c *Handler) cleanupStreamTracking(sourceID string) {
	cleanupStreamTrackingData(sourceID)
}

// cleanupStreamTrackingData removes all tracking data for a stream from the global manager.
// Package-level function so it can be called from both Handler methods and standalone functions.
func cleanupStreamTrackingData(sourceID string) {
	// Clean up client tracking
	hlsMgr.clientsMu.Lock()
	delete(hlsMgr.clients, sourceID)
	hlsMgr.clientsMu.Unlock()

	// Clean up activity tracking using maps.DeleteFunc (Go 1.21+)
	hlsMgr.activityMu.Lock()
	delete(hlsMgr.activity, sourceID)
	prefix := sourceID + ":"
	maps.DeleteFunc(hlsMgr.clientActivity, func(key string, _ time.Time) bool {
		return strings.HasPrefix(key, prefix)
	})
	hlsMgr.activityMu.Unlock()

	// Clean up freshness check timestamp
	lastFreshnessCheck.Delete(sourceID)
}

// waitForHLSPlaylist waits until the muxer advertises enough segments for
// immediate playback.
func (c *Handler) waitForHLSPlaylist(ctx echo.Context, sourceID string, stream *HLSStreamInfo) bool {
	return stream.isNative() && c.waitForNativePlaylist(ctx, sourceID, stream)
}

// logHLSClientConnection logs client connections with rate limiting
func (c *Handler) logHLSClientConnection(sourceID, clientIP, requestPath string) {
	logKey := sourceID + "-" + clientIP

	hlsMgr.clientLogTimeMu.Lock()
	lastLogTime, exists := hlsMgr.clientLogTime[logKey]
	now := time.Now()

	shouldLog := !exists || now.Sub(lastLogTime) > hlsLogCooldown
	if shouldLog {
		hlsMgr.clientLogTime[logKey] = now
	}
	hlsMgr.clientLogTimeMu.Unlock()

	if shouldLog {
		streamStartMsg := ""
		// The muxer emits unpadded segment names; sequence 0 is the first segment
		// of the stream, so a request for it marks the start of streaming.
		if seq, ok := hls.ParseSegmentName(requestPath); ok && seq == 0 {
			streamStartMsg = " (streaming started)"
		}
		apicore.GetLogger().Info("HLS stream request",
			logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
			logger.String("client_ip", clientIP),
			logger.String("status", streamStartMsg))
	}
}

// CleanupAllHLSStreams removes all HLS streams (called on shutdown)
func (c *Handler) CleanupAllHLSStreams() error {
	// Clone and clear streams atomically using Go 1.21+ maps package
	hlsMgr.streamsMu.Lock()
	streamsToClean := maps.Clone(hlsMgr.streams)
	clear(hlsMgr.streams)
	hlsMgr.streamsMu.Unlock()

	// Clear token mappings
	hlsMgr.tokensMu.Lock()
	clear(hlsMgr.tokens)
	clear(hlsMgr.sourceTokens)
	hlsMgr.tokensMu.Unlock()

	// Cleanup each stream
	for sourceID, stream := range streamsToClean {
		c.performHLSCleanup(sourceID, stream, "server shutdown")
	}

	return nil
}

// runHLSActivitySync runs the HLS activity sync loop until context is cancelled
func runHLSActivitySync(ctx context.Context) {
	ticker := time.NewTicker(hlsCleanupDelay * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			apicore.GetLogger().Info("HLS activity sync stopped")
			return
		case <-ticker.C:
			syncHLSActivity()
		}
	}
}

// syncHLSActivity checks for inactive streams and cleans them up
func syncHLSActivity() {
	activeStreamIDs := getActiveStreamIDs()
	streamsToCleanup := findInactiveStreams(activeStreamIDs)
	cleanupInactiveStreams(streamsToCleanup)
	cleanupClientLogTime()
}

// cleanupClientLogTime removes stale entries from clientLogTime map
func cleanupClientLogTime() {
	now := time.Now()
	hlsMgr.clientLogTimeMu.Lock()
	defer hlsMgr.clientLogTimeMu.Unlock()

	for key, lastTime := range hlsMgr.clientLogTime {
		if now.Sub(lastTime) > hlsClientLogRetention {
			delete(hlsMgr.clientLogTime, key)
		}
	}
}

// getActiveStreamIDs returns a snapshot of all active stream IDs
func getActiveStreamIDs() []string {
	hlsMgr.streamsMu.RLock()
	defer hlsMgr.streamsMu.RUnlock()

	activeStreamIDs := slices.Collect(maps.Keys(hlsMgr.streams))
	return activeStreamIDs
}

// findInactiveStreams identifies streams that should be cleaned up
func findInactiveStreams(activeStreamIDs []string) []string {
	var streamsToCleanup []string

	for _, sourceID := range activeStreamIDs {
		if shouldCleanupStream(sourceID) {
			streamsToCleanup = append(streamsToCleanup, sourceID)
		}
	}
	return streamsToCleanup
}

// shouldCleanupStream checks if a stream should be marked for cleanup
func shouldCleanupStream(sourceID string) bool {
	hlsMgr.activityMu.Lock()
	lastActivity, exists := hlsMgr.activity[sourceID]
	hlsMgr.activityMu.Unlock()

	if !exists {
		return false
	}

	inactiveDuration := time.Since(lastActivity)

	// Check for new stream grace period
	if inactiveDuration < hlsNewStreamGracePeriod {
		return false
	}

	// Check for inactivity timeout
	if inactiveDuration <= hlsStreamInactivityTimeout {
		return false
	}

	clientCount := getStreamClientCount(sourceID)
	apicore.GetLogger().Info("Stream inactive, marking for cleanup",
		logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
		logger.Duration("inactive_duration", inactiveDuration),
		logger.Duration("timeout", hlsStreamInactivityTimeout),
		logger.Int("client_count", clientCount))
	return true
}

// getStreamClientCount returns the number of clients for a stream
func getStreamClientCount(sourceID string) int {
	hlsMgr.clientsMu.RLock()
	defer hlsMgr.clientsMu.RUnlock()

	if clients, exists := hlsMgr.clients[sourceID]; exists {
		return len(clients)
	}
	return 0
}

// cleanupInactiveStreams performs cleanup for marked streams
func cleanupInactiveStreams(streamsToCleanup []string) {
	for _, sourceID := range streamsToCleanup {
		stream := removeStreamFromManager(sourceID)
		if stream != nil {
			go cleanupStream(stream, sourceID)
		}
	}
}

// removeStreamFromManager removes a stream from the manager and returns it
func removeStreamFromManager(sourceID string) *HLSStreamInfo {
	hlsMgr.streamsMu.Lock()
	stream, exists := hlsMgr.streams[sourceID]
	if exists {
		delete(hlsMgr.streams, sourceID)
		// Remove token while still holding streamsMu to prevent race:
		// a new stream could create a token between unlock and removeStreamToken.
		removeStreamToken(sourceID)
	}
	hlsMgr.streamsMu.Unlock()

	if exists {
		return stream
	}
	return nil
}

// cleanupStream performs the actual cleanup of a stream
// TODO: Consider refactoring to use proper dependency injection
func cleanupStream(s *HLSStreamInfo, sourceID string) {
	if s.cancel != nil {
		s.cancel()
	}

	// The inactivity sweep removes the stream from the registry itself, so the
	// context watcher skips performHLSCleanup and this is the only chance to
	// release the muxer.
	closeNativeMux(sourceID, s)

	// Clean up tracking data (clients, activity) so stale entries don't persist
	cleanupStreamTrackingData(sourceID)

	apicore.GetLogger().Debug("Cleaned up inactive stream", logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)))
}
