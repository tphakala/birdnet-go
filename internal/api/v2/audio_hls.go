// internal/api/v2/audio_hls.go
package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/privacy"
	"github.com/tphakala/birdnet-go/internal/securefs"
)

// HLS streaming configuration constants
const (
	// Timeouts
	hlsStreamInactivityTimeout = 5 * time.Minute  // Cleanup inactive streams after this duration
	hlsMaxStreamLifetime       = 6 * time.Hour    // Maximum stream lifetime regardless of activity
	hlsPlaylistWaitTimeout     = 20 * time.Second // How long to wait for playlist file
	hlsNewStreamGracePeriod    = 30 * time.Second // Grace period for new streams before cleanup

	// Logging
	hlsLogCooldown        = 60 * time.Second      // Only log client connections once per this duration
	hlsVerboseEnvVar      = "HLS_VERBOSE_LOGGING" // Environment variable to enable verbose logging
	hlsVerboseTimeout     = 5 * time.Minute       // Verbose logging window at startup
	hlsClientLogRetention = 24 * time.Hour        // Retention period for client log timestamps

	// Audio encoding
	hlsMinSegments       = 2     // Minimum HLS segments required
	hlsDefaultSegmentLen = 2     // Default HLS segment length in seconds
	hlsMinSegmentLen     = 1     // Minimum HLS segment length in seconds
	hlsMaxSegmentLen     = 30    // Maximum HLS segment length in seconds
	hlsAudioBitDepth     = 16    // Audio bit depth for encoding
	hlsMinBitrate        = 16    // Minimum audio bitrate in kbps
	hlsMaxBitrate        = 320   // Maximum audio bitrate in kbps
	hlsDefaultSampleRate = 48000 // Default audio sample rate in Hz
	hlsCleanupDelay      = 5     // Delay in seconds before cleanup
)

// HLSStreamInfo contains information about an active HLS streaming session
type HLSStreamInfo struct {
	SourceID     string             // Original audio source identifier
	FFmpegCmd    *exec.Cmd          // FFmpeg process handle
	OutputDir    string             // Directory containing HLS files
	PlaylistPath string             // Path to the m3u8 playlist file
	FifoPipe     string             // Named pipe path (platform-specific)
	logFile      *os.File           // FFmpeg log file (closed after process exits)
	ctx          context.Context    // Stream lifecycle context
	cancel       context.CancelFunc // Cancel function for cleanup
}

// HLSStreamStatus represents the current status of an HLS stream (API response)
type HLSStreamStatus struct {
	Status        string `json:"status"`                 // "starting" or "ready"
	Source        string `json:"source"`                 // Source identifier (URL-encoded)
	PlaylistURL   string `json:"playlist_url,omitempty"` // API URL for the playlist (not filesystem path)
	ActiveClients int    `json:"active_clients"`
	PlaylistReady bool   `json:"playlist_ready"`
}

// HLSHeartbeatRequest represents a client heartbeat message
type HLSHeartbeatRequest struct {
	SourceID string `json:"source_id"`
	ClientID string `json:"client_id,omitempty"` // Optional, server can identify from request
}

// hlsManager manages HLS streaming state
// TODO: Consider moving to Controller struct for better encapsulation
type hlsManager struct {
	// Active streams indexed by sourceID
	streams   map[string]*HLSStreamInfo
	streamsMu sync.RWMutex // RWMutex for read-heavy operations

	// Client tracking per stream
	clients   map[string]map[string]bool // sourceID -> clientID -> true
	clientsMu sync.Mutex

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

	// Activity sync lifecycle management
	activitySyncOnce   sync.Once
	activitySyncCancel context.CancelFunc
}

// Global HLS manager instance
// TODO: Consider moving to Controller struct for better encapsulation
var hlsMgr = &hlsManager{
	streams:        make(map[string]*HLSStreamInfo),
	clients:        make(map[string]map[string]bool),
	activity:       make(map[string]time.Time),
	clientActivity: make(map[string]time.Time),
	clientLogTime:  make(map[string]time.Time),
	verboseLogging: os.Getenv(hlsVerboseEnvVar) != "",
}

// initHLSRoutes registers HLS streaming endpoints
func (c *Controller) initHLSRoutes() {
	// Get authentication middleware
	authMiddleware := c.authMiddleware

	// HLS base group (no auth by default)
	hlsGroup := c.Group.Group("/streams/hls")

	// Stream control endpoints - require authentication
	hlsGroup.POST("/:sourceID/start", c.StartHLSStream, authMiddleware)
	hlsGroup.POST("/:sourceID/stop", c.StopHLSStream, authMiddleware)

	// Public endpoints - no authentication required
	hlsGroup.POST("/heartbeat", c.HLSHeartbeat)
	hlsGroup.GET("/status", c.GetHLSStatus)
	hlsGroup.GET("/:sourceID/playlist.m3u8", c.ServeHLSPlaylist)
	hlsGroup.GET("/:sourceID/*", c.ServeHLSContent)

	// Start the HLS activity sync goroutine (only once across all controller instances)
	hlsMgr.activitySyncOnce.Do(func() {
		ctx, cancel := context.WithCancel(c.ctx)
		hlsMgr.activitySyncCancel = cancel
		go runHLSActivitySync(ctx)
	})
}

// StartHLSStream initiates an HLS stream for a specific audio source
// POST /api/v2/streams/hls/:sourceID/start
func (c *Controller) StartHLSStream(ctx echo.Context) error {
	sourceID, err := c.validateAndDecodeSourceID(ctx)
	if err != nil {
		return err
	}

	clientID := c.generateClientID(ctx)

	// Check for force restart query param
	forceRestart := ctx.QueryParam("force") == QueryValueTrue

	c.logAPIRequest(ctx, logger.LogLevelInfo, "HLS stream start requested",
		logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
		logger.String("client_id", clientID),
		logger.Bool("force_restart", forceRestart))

	// Verify source exists
	if !myaudio.HasCaptureBuffer(sourceID) {
		return c.HandleError(ctx, nil, "Audio source not found", http.StatusNotFound)
	}

	// Check for existing healthy stream first (reuse if possible)
	if existingStream := c.getHLSStream(sourceID); existingStream != nil && !forceRestart {
		// Existing stream found - register client and reuse it
		c.updateHLSActivity(sourceID, clientID, "stream_join", hlsNewStreamGracePeriod)
		c.logAPIRequest(ctx, logger.LogLevelInfo, "Reusing existing HLS stream",
			logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
			logger.String("client_id", clientID))
		return c.buildHLSStreamResponse(ctx, sourceID, existingStream)
	}

	// Cleanup existing stream if force restart requested
	if forceRestart {
		c.cleanupExistingHLSStream(sourceID)
	}

	// Register client and update activity with grace period
	c.updateHLSActivity(sourceID, clientID, "stream_start", hlsNewStreamGracePeriod)

	// Create or get the HLS stream
	stream, err := c.getOrCreateHLSStream(ctx.Request().Context(), sourceID)
	if err != nil {
		c.logAPIRequest(ctx, logger.LogLevelError, "Failed to create HLS stream",
			logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
			logger.Error(err))
		return c.HandleError(ctx, err, "Failed to start audio stream", http.StatusInternalServerError)
	}

	// Check if playlist is ready
	playlistReady := c.waitForHLSPlaylist(ctx, sourceID, stream)

	return c.buildHLSStreamResponse(ctx, sourceID, stream, playlistReady)
}

// buildHLSStreamResponse constructs the HLS stream status response
func (c *Controller) buildHLSStreamResponse(ctx echo.Context, sourceID string, stream *HLSStreamInfo, playlistReady ...bool) error {
	// Get client count
	clientCount := getStreamClientCount(sourceID)

	// Build the API URL for the playlist (not the filesystem path)
	encodedSourceID := url.PathEscape(sourceID)
	playlistURL := fmt.Sprintf("/api/v2/streams/hls/%s/playlist.m3u8", encodedSourceID)

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
		c.logAPIRequest(ctx, logger.LogLevelInfo, "HLS stream ready",
			logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
			logger.String("playlist_url", playlistURL))
	}

	return ctx.JSON(http.StatusOK, HLSStreamStatus{
		Status:        status,
		Source:        encodedSourceID,
		PlaylistURL:   playlistURL,
		ActiveClients: clientCount,
		PlaylistReady: isReady,
	})
}

// checkHLSPlaylistReady checks if the playlist file exists and is valid
func (c *Controller) checkHLSPlaylistReady(stream *HLSStreamInfo) bool {
	if stream == nil || stream.PlaylistPath == "" {
		return false
	}

	hlsBaseDir, err := conf.GetHLSDirectory()
	if err != nil {
		return false
	}

	secFS, err := securefs.New(hlsBaseDir)
	if err != nil {
		return false
	}
	defer func() {
		if err := secFS.Close(); err != nil {
			GetLogger().Error("Failed to close secure filesystem", logger.Error(err))
		}
	}()

	if !secFS.ExistsNoErr(stream.PlaylistPath) {
		return false
	}

	data, err := secFS.ReadFile(stream.PlaylistPath)
	if err != nil {
		return false
	}

	return len(data) > 0 && strings.Contains(string(data), "#EXTM3U")
}

// StopHLSStream stops an HLS stream for a specific client
// POST /api/v2/streams/hls/:sourceID/stop
func (c *Controller) StopHLSStream(ctx echo.Context) error {
	sourceID, err := c.validateAndDecodeSourceID(ctx)
	if err != nil {
		return err
	}

	clientID := c.generateClientID(ctx)

	c.logAPIRequest(ctx, logger.LogLevelInfo, "HLS stream stop requested",
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
func (c *Controller) HLSHeartbeat(ctx echo.Context) error {
	var heartbeat HLSHeartbeatRequest
	if err := ctx.Bind(&heartbeat); err != nil {
		return c.HandleError(ctx, err, "Invalid heartbeat format", http.StatusBadRequest)
	}

	clientID := c.generateClientID(ctx)

	// Handle disconnection announcements
	if ctx.QueryParam("disconnect") == QueryValueTrue || ctx.QueryParam("status") == "disconnect" {
		return c.handleHLSDisconnect(ctx, heartbeat.SourceID, clientID)
	}

	// Validate stream exists
	if !c.hlsStreamExists(heartbeat.SourceID) {
		if hlsMgr.verboseLogging {
			c.logAPIRequest(ctx, logger.LogLevelWarn, "Heartbeat for non-existent stream",
				logger.String("source_id", privacy.SanitizeRTSPUrl(heartbeat.SourceID)))
		}
		return ctx.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}

	// Update activity
	c.updateHLSActivity(heartbeat.SourceID, clientID, "heartbeat")

	return ctx.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// GetHLSStatus returns the status of all active HLS streams
// GET /api/v2/streams/hls/status
func (c *Controller) GetHLSStatus(ctx echo.Context) error {
	hlsMgr.streamsMu.RLock()
	// Copy stream references under lock to minimize lock duration
	streamsCopy := make(map[string]*HLSStreamInfo, len(hlsMgr.streams))
	maps.Copy(streamsCopy, hlsMgr.streams)
	hlsMgr.streamsMu.RUnlock()

	streams := make([]HLSStreamStatus, 0, len(streamsCopy))
	for sourceID, stream := range streamsCopy {
		encodedSourceID := url.PathEscape(sourceID)
		playlistURL := fmt.Sprintf("/api/v2/streams/hls/%s/playlist.m3u8", encodedSourceID)

		// Check actual playlist readiness instead of hardcoding true
		playlistReady := c.checkHLSPlaylistReady(stream)

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
// GET /api/v2/streams/hls/:sourceID/playlist.m3u8
func (c *Controller) ServeHLSPlaylist(ctx echo.Context) error {
	sourceID, err := c.validateAndDecodeSourceID(ctx)
	if err != nil {
		return err
	}

	clientID := c.generateClientID(ctx)

	// Get stream info
	stream := c.getHLSStream(sourceID)
	if stream == nil {
		return c.HandleError(ctx, nil, "Stream not found", http.StatusNotFound)
	}

	// Update activity
	c.updateHLSActivity(sourceID, clientID, "playlist_request")

	// Get HLS base directory
	hlsBaseDir, err := conf.GetHLSDirectory()
	if err != nil {
		return c.HandleError(ctx, err, "Server configuration error", http.StatusInternalServerError)
	}

	// Create secure filesystem
	secFS, err := securefs.New(hlsBaseDir)
	if err != nil {
		return c.HandleError(ctx, err, "Server error", http.StatusInternalServerError)
	}
	defer func() {
		if err := secFS.Close(); err != nil {
			GetLogger().Error("Failed to close secure filesystem", logger.Error(err))
		}
	}()

	// Set headers
	c.setHLSHeaders(ctx)
	ctx.Response().Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	ctx.Response().Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	// Check if playlist exists
	if !secFS.ExistsNoErr(stream.PlaylistPath) {
		// Stream exists but playlist not ready yet
		if !c.hlsStreamExists(sourceID) {
			return c.HandleError(ctx, nil, "Stream no longer exists", http.StatusNotFound)
		}

		// Return temporary empty playlist with configured segment length
		segmentLength := c.getEffectiveSegmentLength()
		emptyPlaylist := fmt.Sprintf(`#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:%d
#EXT-X-MEDIA-SEQUENCE:0
#EXT-X-PLAYLIST-TYPE:EVENT
`, segmentLength)
		ctx.Response().Header().Set("Retry-After", fmt.Sprintf("%d", segmentLength))
		return ctx.String(http.StatusOK, emptyPlaylist)
	}

	return secFS.ServeFile(ctx, stream.PlaylistPath)
}

// ServeHLSContent serves HLS segment files
// GET /api/v2/streams/hls/:sourceID/*
func (c *Controller) ServeHLSContent(ctx echo.Context) error {
	sourceID, err := c.validateAndDecodeSourceID(ctx)
	if err != nil {
		return err
	}

	clientID := c.generateClientID(ctx)
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

	// Update activity for segment requests
	c.updateHLSActivity(sourceID, clientID, "segment_request")

	// Log client connection (rate-limited)
	c.logHLSClientConnection(sourceID, ctx.RealIP(), decodedPath)

	// Get HLS base directory
	hlsBaseDir, err := conf.GetHLSDirectory()
	if err != nil {
		return c.HandleError(ctx, err, "Server configuration error", http.StatusInternalServerError)
	}

	// Create secure filesystem
	secFS, err := securefs.New(hlsBaseDir)
	if err != nil {
		return c.HandleError(ctx, err, "Server error", http.StatusInternalServerError)
	}
	defer func() {
		if err := secFS.Close(); err != nil {
			GetLogger().Error("Failed to close secure filesystem", logger.Error(err))
		}
	}()

	// Validate and build segment path
	cleanPath := filepath.Clean("/" + decodedPath)
	// Use filepath.IsLocal for comprehensive path validation (prevents CVE-2023-45284, CVE-2023-45283)
	if !filepath.IsLocal(cleanPath[1:]) || cleanPath == "/" {
		return c.HandleError(ctx, nil, "Invalid segment path", http.StatusBadRequest)
	}

	safeRequestPath := cleanPath[1:] // Remove leading slash
	segmentPath := filepath.Join(stream.OutputDir, safeRequestPath)

	// Security check: ensure path is within stream directory
	isWithin, err := securefs.IsPathWithinBase(stream.OutputDir, segmentPath)
	if err != nil || !isWithin {
		return c.HandleError(ctx, nil, "Invalid segment path", http.StatusBadRequest)
	}

	// Check if segment exists
	if !secFS.ExistsNoErr(segmentPath) {
		return c.HandleError(ctx, nil, "Segment file not found", http.StatusNotFound)
	}

	// Set headers and content type
	c.setHLSHeaders(ctx)
	c.setHLSContentType(ctx, safeRequestPath)

	return secFS.ServeFile(ctx, segmentPath)
}

// Helper methods

// validateAndDecodeSourceID extracts and validates the sourceID parameter
func (c *Controller) validateAndDecodeSourceID(ctx echo.Context) (string, error) {
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
func (c *Controller) generateClientID(ctx echo.Context) string {
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

// generateFilesystemSafeName creates a filesystem-safe identifier from source ID
func generateFilesystemSafeName(input string) string {
	sum := sha256.Sum256([]byte(input))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// setHLSHeaders sets common HLS response headers
// Note: CORS is handled by middleware at the v2 group level
func (c *Controller) setHLSHeaders(ctx echo.Context) {
	// HLS-specific headers only; CORS is handled by middleware
}

// getEffectiveSegmentLength returns the configured segment length with defaults and limits applied
func (c *Controller) getEffectiveSegmentLength() int {
	segmentLength := c.Settings.WebServer.LiveStream.SegmentLength
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
func (c *Controller) setHLSContentType(ctx echo.Context, path string) {
	switch filepath.Ext(path) {
	case ".ts":
		ctx.Response().Header().Set("Content-Type", "audio/mp2t")
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

// Stream management methods

// getOrCreateHLSStream gets existing stream or creates a new one
func (c *Controller) getOrCreateHLSStream(_ context.Context, sourceID string) (*HLSStreamInfo, error) {
	// Check for existing stream
	if stream := c.getHLSStream(sourceID); stream != nil {
		return stream, nil
	}

	GetLogger().Info("Creating new HLS stream", logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)))

	// Generate filesystem-safe name
	filesystemSafeID := generateFilesystemSafeName(sourceID)

	// Create stream context from controller's lifecycle context, NOT from HTTP request context.
	// Using request context would cause the stream to be cleaned up when the /start request completes.
	// The stream must persist beyond the initial request lifetime.
	streamCtx, streamCancel := context.WithCancel(c.ctx)

	// Get HLS directory
	hlsBaseDir, err := conf.GetHLSDirectory()
	if err != nil {
		streamCancel()
		return nil, fmt.Errorf("failed to get HLS directory: %w", err)
	}

	// Create secure filesystem
	secFS, err := securefs.New(hlsBaseDir)
	if err != nil {
		streamCancel()
		return nil, fmt.Errorf("failed to initialize secure filesystem: %w", err)
	}
	defer func() {
		if err := secFS.Close(); err != nil {
			GetLogger().Error("Failed to close secure filesystem", logger.Error(err))
		}
	}()

	// Prepare output directory
	outputDir, playlistPath, err := c.prepareHLSDirectory(secFS, hlsBaseDir, filesystemSafeID)
	if err != nil {
		streamCancel()
		return nil, err
	}

	// Get FFmpeg path
	ffmpegPath := c.Settings.Realtime.Audio.FfmpegPath
	if ffmpegPath == "" {
		streamCancel()
		return nil, fmt.Errorf("ffmpeg not configured")
	}

	// Setup FIFO for audio streaming
	fifoPath, pipeName, err := c.setupHLSFifo(secFS, hlsBaseDir, outputDir)
	if err != nil {
		if removeErr := secFS.RemoveAll(outputDir); removeErr != nil {
			GetLogger().Error("Failed to remove output directory", logger.Error(removeErr))
		}
		streamCancel()
		return nil, err
	}

	// Determine reader path based on platform
	readerPath := fifoPath
	if runtime.GOOS == OSWindows {
		readerPath = pipeName
	}

	// Setup and start FFmpeg
	cmd, err := c.setupHLSFFmpeg(streamCtx, ffmpegPath, readerPath, outputDir, playlistPath)
	if err != nil {
		streamCancel()
		return nil, fmt.Errorf("failed to setup FFmpeg: %w", err)
	}

	// Setup Windows-specific stdin pipe handling
	if runtime.GOOS == OSWindows {
		if err := c.setupWindowsAudioFeed(streamCtx, sourceID, cmd); err != nil {
			streamCancel()
			return nil, err
		}
	}

	// Setup FFmpeg logging and start the process
	logFile, err := c.setupFFmpegLogging(secFS, cmd, hlsBaseDir, outputDir)
	if err != nil {
		streamCancel()
		return nil, err
	}

	// Create stream info
	stream := &HLSStreamInfo{
		SourceID:     sourceID,
		FFmpegCmd:    cmd,
		OutputDir:    outputDir,
		PlaylistPath: playlistPath,
		FifoPipe:     pipeName,
		logFile:      logFile,
		ctx:          streamCtx,
		cancel:       streamCancel,
	}

	// Handle race condition - check if another goroutine created the stream
	hlsMgr.streamsMu.Lock()
	if existingStream, exists := hlsMgr.streams[sourceID]; exists {
		hlsMgr.streamsMu.Unlock()

		// Cleanup our stream
		stream.cancel()
		if stream.FFmpegCmd != nil && stream.FFmpegCmd.Process != nil {
			if killErr := stream.FFmpegCmd.Process.Kill(); killErr != nil {
				GetLogger().Error("Failed to kill FFmpeg process", logger.Error(killErr))
			}
			if _, waitErr := stream.FFmpegCmd.Process.Wait(); waitErr != nil {
				GetLogger().Error("Failed to wait for FFmpeg process", logger.Error(waitErr))
			}
		}
		// Close log file after process exits
		if stream.logFile != nil {
			if closeErr := stream.logFile.Close(); closeErr != nil {
				GetLogger().Error("Failed to close log file", logger.Error(closeErr))
			}
		}
		if removeErr := secFS.RemoveAll(outputDir); removeErr != nil {
			GetLogger().Error("Failed to remove output directory", logger.Error(removeErr))
		}

		GetLogger().Debug("Race condition detected, using existing stream", logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)))
		return existingStream, nil
	}

	hlsMgr.streams[sourceID] = stream
	hlsMgr.streamsMu.Unlock()

	// Initialize activity
	c.updateHLSActivity(sourceID, "", "stream_creation")

	// Start audio feed (non-Windows platforms)
	if runtime.GOOS != OSWindows {
		go c.feedAudioToFFmpeg(sourceID, stream.FifoPipe, stream.ctx)
	}

	// Start context cleanup goroutine
	go func() {
		<-streamCtx.Done()
		c.cleanupHLSStream(sourceID)
	}()

	return stream, nil
}

// prepareHLSDirectory creates and validates the output directory
func (c *Controller) prepareHLSDirectory(secFS *securefs.SecureFS, hlsBaseDir, filesystemSafeID string) (outputDir, playlistPath string, err error) {
	outputDir = filepath.Join(hlsBaseDir, fmt.Sprintf("stream_%s", filesystemSafeID))

	// Verify output directory is within HLS base
	isWithin, err := securefs.IsPathWithinBase(hlsBaseDir, outputDir)
	if err != nil {
		return "", "", fmt.Errorf("failed to validate output directory: %w", err)
	}
	if !isWithin {
		return "", "", fmt.Errorf("security error: output directory outside HLS base")
	}

	// Clean existing directory
	if secFS.ExistsNoErr(outputDir) {
		if err := secFS.RemoveAll(outputDir); err != nil {
			return "", "", fmt.Errorf("failed to clean HLS directory: %w", err)
		}
	}

	// Create directory
	if err := secFS.MkdirAll(outputDir, FilePermExecutable); err != nil {
		return "", "", fmt.Errorf("failed to create HLS directory: %w", err)
	}

	playlistPath = filepath.Join(outputDir, "playlist.m3u8")

	// Verify playlist path
	isWithin, err = securefs.IsPathWithinBase(hlsBaseDir, playlistPath)
	if err != nil || !isWithin {
		return "", "", fmt.Errorf("security error: playlist path outside HLS base")
	}

	return outputDir, playlistPath, nil
}

// setupHLSFifo creates the FIFO pipe for audio streaming
func (c *Controller) setupHLSFifo(secFS *securefs.SecureFS, hlsBaseDir, outputDir string) (fifoPath, pipeName string, err error) {
	fifoPath = filepath.Join(outputDir, "audio.pcm")

	isWithin, pathErr := securefs.IsPathWithinBase(hlsBaseDir, fifoPath)
	if pathErr != nil || !isWithin {
		return "", "", fmt.Errorf("security error: FIFO path outside HLS base")
	}

	if err = secFS.CreateFIFO(fifoPath); err != nil {
		return "", "", fmt.Errorf("failed to create FIFO: %w", err)
	}

	pipeName = secFS.GetPipeName()
	return fifoPath, pipeName, nil
}

// setupHLSFFmpeg creates the FFmpeg command
func (c *Controller) setupHLSFFmpeg(ctx context.Context, ffmpegPath, inputSource, outputDir, playlistPath string) (*exec.Cmd, error) {
	args := c.buildFFmpegArgs(inputSource, outputDir, playlistPath)
	//nolint:gosec // G204: ffmpegPath is from admin config (Settings.Realtime.Audio.FfmpegPath), not user input
	return exec.CommandContext(ctx, ffmpegPath, args...), nil
}

// buildFFmpegArgs constructs FFmpeg command line arguments
func (c *Controller) buildFFmpegArgs(inputSource, outputDir, playlistPath string) []string {
	settings := c.Settings.WebServer.LiveStream

	// Apply defaults and limits
	bitrate := 128
	if settings.BitRate > 0 {
		switch {
		case settings.BitRate < hlsMinBitrate:
			bitrate = hlsMinBitrate
		case settings.BitRate > hlsMaxBitrate:
			bitrate = hlsMaxBitrate
		default:
			bitrate = settings.BitRate
		}
	}

	sampleRate := hlsDefaultSampleRate
	if settings.SampleRate > 0 {
		sampleRate = settings.SampleRate
	}

	segmentLength := hlsDefaultSegmentLen
	if settings.SegmentLength > 0 {
		switch {
		case settings.SegmentLength < hlsMinSegmentLen:
			segmentLength = hlsMinSegmentLen
		case settings.SegmentLength > hlsMaxSegmentLen:
			segmentLength = hlsMaxSegmentLen
		default:
			segmentLength = settings.SegmentLength
		}
	}

	logLevel := LogLevelWarning
	if settings.FfmpegLogLevel != "" {
		logLevel = settings.FfmpegLogLevel
	}

	args := []string{
		"-f", "s16le",
		"-ar", fmt.Sprintf("%d", sampleRate),
		"-ac", "1",
		"-i", inputSource,
		"-y",
		"-c:a", "aac",
		"-b:a", fmt.Sprintf("%dk", bitrate),
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", segmentLength),
		"-hls_list_size", "3",
		"-hls_flags", "delete_segments+temp_file",
		"-hls_segment_type", "fmp4",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_init_time", "3",
		"-hls_allow_cache", "1",
		"-movflags", "faststart+empty_moov+separate_moof",
		"-start_number", "0",
		"-loglevel", logLevel,
		"-hls_segment_filename", filepath.ToSlash(filepath.Join(outputDir, "segment%03d.m4s")),
		playlistPath,
	}

	return args
}

// setupWindowsAudioFeed sets up audio feeding via stdin for Windows
func (c *Controller) setupWindowsAudioFeed(ctx context.Context, sourceID string, cmd *exec.Cmd) error {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	go func() {
		defer func() {
			if err := stdin.Close(); err != nil {
				GetLogger().Error("Failed to close stdin", logger.Error(err))
			}
		}()
		GetLogger().Debug("Starting audio feed via stdin", logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)))

		audioChan, cleanup, err := c.setupAudioCallback(sourceID)
		if err != nil {
			GetLogger().Error("Error setting up audio callback", logger.Error(err))
			return
		}
		defer cleanup()

		for {
			select {
			case <-ctx.Done():
				GetLogger().Debug("Audio feed terminated due to context cancellation", logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)))
				return
			case data, ok := <-audioChan:
				if !ok {
					GetLogger().Debug("Audio channel closed", logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)))
					return
				}

				written := 0
				for written < len(data) {
					n, err := stdin.Write(data[written:])
					if err != nil {
						GetLogger().Error("Error writing to FFmpeg stdin", logger.Error(err))
						return
					}
					written += n
				}
			}
		}
	}()

	return nil
}

// setupFFmpegLogging configures FFmpeg output logging and starts the FFmpeg process.
// Returns the log file which must be closed by the caller after the process exits.
// The caller is responsible for calling cmd.Wait() and then closing the returned file.
func (c *Controller) setupFFmpegLogging(secFS *securefs.SecureFS, cmd *exec.Cmd, hlsBaseDir, outputDir string) (*os.File, error) {
	logFilePath := filepath.Join(outputDir, "ffmpeg.log")

	isWithin, err := securefs.IsPathWithinBase(hlsBaseDir, logFilePath)
	if err != nil || !isWithin {
		return nil, fmt.Errorf("security error: log file path outside HLS base")
	}

	logFile, err := secFS.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, FilePermReadWrite)
	if err != nil {
		return nil, fmt.Errorf("failed to create ffmpeg log file: %w", err)
	}

	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		if closeErr := logFile.Close(); closeErr != nil {
			GetLogger().Error("Failed to close log file", logger.Error(closeErr))
		}
		return nil, fmt.Errorf("failed to start FFmpeg: %w", err)
	}

	GetLogger().Debug("FFmpeg process started", logger.String("output_dir", outputDir))
	return logFile, nil
}

// setupAudioCallback sets up the audio callback channel
func (c *Controller) setupAudioCallback(sourceID string) (audioChan chan []byte, cleanup func(), err error) {
	audioChan = make(chan []byte, DefaultReadBufferSize)

	callback := func(callbackSourceID string, data []byte) {
		if callbackSourceID == sourceID {
			select {
			case audioChan <- data:
			default:
				// Channel full, drop oldest
				select {
				case <-audioChan:
					audioChan <- data
				default:
				}
			}
		}
	}

	myaudio.RegisterBroadcastCallback(sourceID, callback)
	GetLogger().Debug("Registered audio callback", logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)))

	cleanup = func() {
		myaudio.UnregisterBroadcastCallback(sourceID)
		GetLogger().Debug("Unregistered audio callback", logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)))
	}

	return audioChan, cleanup, nil
}

// feedAudioToFFmpeg feeds audio data to FFmpeg via FIFO (Unix platforms)
func (c *Controller) feedAudioToFFmpeg(sourceID, pipePath string, ctx context.Context) {
	sanitizedID := privacy.SanitizeRTSPUrl(sourceID)
	GetLogger().Debug("Starting audio feed", logger.String("source_id", sanitizedID), logger.String("pipe_path", pipePath))

	hlsBaseDir, err := conf.GetHLSDirectory()
	if err != nil {
		GetLogger().Error("Error getting HLS directory", logger.Error(err))
		return
	}

	secFS, err := securefs.New(hlsBaseDir)
	if err != nil {
		GetLogger().Error("Error creating secure filesystem", logger.Error(err))
		return
	}
	defer func() {
		if err := secFS.Close(); err != nil {
			GetLogger().Error("Failed to close secure filesystem", logger.Error(err))
		}
	}()

	// Open FIFO
	fifo, err := secFS.OpenFile(pipePath, os.O_WRONLY, 0)
	if err != nil {
		GetLogger().Error("Error opening pipe", logger.Error(err))
		return
	}
	defer func() {
		if err := fifo.Close(); err != nil {
			GetLogger().Error("Failed to close FIFO", logger.Error(err))
		}
	}()

	// Setup audio callback
	audioChan, cleanup, err := c.setupAudioCallback(sourceID)
	if err != nil {
		GetLogger().Error("Error setting up audio callback", logger.Error(err))
		return
	}
	defer cleanup()

	GetLogger().Debug("Audio feed ready", logger.String("source_id", sanitizedID))

	dataWritten := false
	for {
		select {
		case <-ctx.Done():
			GetLogger().Debug("Audio feed stopped due to context cancellation", logger.String("source_id", sanitizedID))
			return
		case data, ok := <-audioChan:
			if !ok {
				GetLogger().Debug("Audio channel closed", logger.String("source_id", sanitizedID))
				return
			}

			if _, err := fifo.Write(data); err != nil {
				GetLogger().Error("Error writing to FIFO", logger.Error(err))
				return
			}

			if !dataWritten {
				GetLogger().Debug("First audio data written", logger.String("source_id", sanitizedID))
				dataWritten = true
			}
		}
	}
}

// Activity and client management

// updateHLSActivity records activity for a stream
func (c *Controller) updateHLSActivity(sourceID, clientID, activityType string, gracePeriod ...time.Duration) {
	// Track client
	if clientID != "" {
		hlsMgr.clientsMu.Lock()
		if hlsMgr.clients[sourceID] == nil {
			hlsMgr.clients[sourceID] = make(map[string]bool)
		}
		hlsMgr.clients[sourceID][clientID] = true
		hlsMgr.clientsMu.Unlock()
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

// getHLSStream returns the stream info if it exists
func (c *Controller) getHLSStream(sourceID string) *HLSStreamInfo {
	hlsMgr.streamsMu.RLock()
	defer hlsMgr.streamsMu.RUnlock()
	return hlsMgr.streams[sourceID]
}

// hlsStreamExists checks if a stream exists
func (c *Controller) hlsStreamExists(sourceID string) bool {
	hlsMgr.streamsMu.RLock()
	defer hlsMgr.streamsMu.RUnlock()
	_, exists := hlsMgr.streams[sourceID]
	return exists
}

// removeHLSClient removes a client from tracking, returns true if last client
func (c *Controller) removeHLSClient(sourceID, clientID string) bool {
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
func (c *Controller) handleHLSDisconnect(ctx echo.Context, sourceID, clientID string) error {
	// Check for premature disconnect
	hlsMgr.activityMu.Lock()
	if lastTime, exists := hlsMgr.clientActivity[sourceID+":"+clientID]; exists {
		if time.Since(lastTime) < 10*time.Second {
			hlsMgr.activityMu.Unlock()
			c.logAPIRequest(ctx, logger.LogLevelWarn, "Ignoring premature disconnect",
				logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)))
			c.updateHLSActivity(sourceID, clientID, "continued-connection")
			return ctx.JSON(http.StatusOK, map[string]string{"status": "ok"})
		}
	}
	hlsMgr.activityMu.Unlock()

	c.logAPIRequest(ctx, logger.LogLevelInfo, "Client announced disconnection",
		logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
		logger.String("client_id", clientID))

	c.removeHLSClient(sourceID, clientID)
	return ctx.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// Cleanup methods

// cleanupExistingHLSStream cleans up an existing stream before restart
func (c *Controller) cleanupExistingHLSStream(sourceID string) {
	hlsMgr.streamsMu.Lock()
	stream, exists := hlsMgr.streams[sourceID]
	if !exists {
		hlsMgr.streamsMu.Unlock()
		return
	}

	GetLogger().Debug("Cleaning up existing stream", logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)))

	if stream.cancel != nil {
		stream.cancel()
	}

	var cmd *exec.Cmd
	if stream.FFmpegCmd != nil && stream.FFmpegCmd.Process != nil {
		cmd = stream.FFmpegCmd
	}

	outputDir := stream.OutputDir
	logFile := stream.logFile
	delete(hlsMgr.streams, sourceID)
	hlsMgr.streamsMu.Unlock()

	// Wait for process termination
	if cmd != nil && cmd.Process != nil {
		if _, err := cmd.Process.Wait(); err != nil {
			GetLogger().Error("Failed to wait for FFmpeg process", logger.Error(err))
		}
	}

	// Close log file after process exits (must be done after Wait())
	if logFile != nil {
		if closeErr := logFile.Close(); closeErr != nil {
			GetLogger().Error("Failed to close log file", logger.Error(closeErr))
		}
	}

	// Clean up directory
	if outputDir != "" {
		hlsBaseDir, err := conf.GetHLSDirectory()
		if err == nil {
			if secFS, err := securefs.New(hlsBaseDir); err == nil {
				defer func() {
					if closeErr := secFS.Close(); closeErr != nil {
						GetLogger().Error("Failed to close secure filesystem", logger.Error(closeErr))
					}
				}()
				if secFS.ExistsNoErr(outputDir) {
					if removeErr := secFS.RemoveAll(outputDir); removeErr != nil {
						GetLogger().Error("Failed to remove output directory", logger.Error(removeErr))
					}
				}
			}
		}
	}
}

// cleanupHLSStream performs full stream cleanup
func (c *Controller) cleanupHLSStream(sourceID string) {
	hlsMgr.streamsMu.Lock()
	stream, exists := hlsMgr.streams[sourceID]
	if !exists {
		hlsMgr.streamsMu.Unlock()
		return
	}

	GetLogger().Debug("Cleaning up HLS stream", logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)))
	delete(hlsMgr.streams, sourceID)
	hlsMgr.streamsMu.Unlock()

	c.performHLSCleanup(sourceID, stream, "context cancelled")
}

// stopHLSStream stops a stream with a specific reason
func (c *Controller) stopHLSStream(sourceID, reason string) {
	hlsMgr.streamsMu.Lock()
	stream, exists := hlsMgr.streams[sourceID]
	if !exists {
		hlsMgr.streamsMu.Unlock()
		return
	}

	GetLogger().Info("Stopping HLS stream", logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)), logger.String("reason", reason))
	delete(hlsMgr.streams, sourceID)
	hlsMgr.streamsMu.Unlock()

	c.performHLSCleanup(sourceID, stream, reason)
}

// performHLSCleanup performs the actual cleanup of stream resources
func (c *Controller) performHLSCleanup(sourceID string, stream *HLSStreamInfo, reason string) {
	GetLogger().Debug("Performing HLS cleanup", logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)), logger.String("reason", reason))

	// Cancel context
	if stream.cancel != nil {
		stream.cancel()
	}

	// Clean up FFmpeg process and log file
	c.cleanupFFmpegProcess(sourceID, stream)

	// Clean up output directory
	c.cleanupStreamDirectory(stream.OutputDir)

	// Clean up tracking data
	c.cleanupStreamTracking(sourceID)

	GetLogger().Debug("HLS stream cleanup completed", logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)))
}

// cleanupFFmpegProcess terminates the FFmpeg process and closes the log file.
func (c *Controller) cleanupFFmpegProcess(sourceID string, stream *HLSStreamInfo) {
	hasProcess := stream.FFmpegCmd != nil && stream.FFmpegCmd.Process != nil
	if hasProcess {
		go c.waitForFFmpegProcess(sourceID, stream.FFmpegCmd, stream.logFile)
		return
	}
	// No process, just close log file if present
	closeLogFile(stream.logFile)
}

// waitForFFmpegProcess waits for FFmpeg to exit and cleans up resources.
func (c *Controller) waitForFFmpegProcess(sourceID string, cmd *exec.Cmd, logFile *os.File) {
	if _, err := cmd.Process.Wait(); err != nil {
		GetLogger().Error("FFmpeg process wait error", logger.Error(err))
	}
	closeLogFile(logFile)
	GetLogger().Debug("FFmpeg process terminated", logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)))
}

// closeLogFile safely closes a log file if it's not nil.
func closeLogFile(f *os.File) {
	if f == nil {
		return
	}
	if err := f.Close(); err != nil {
		GetLogger().Error("Failed to close log file", logger.Error(err))
	}
}

// cleanupStreamDirectory removes a single stream's output directory.
func (c *Controller) cleanupStreamDirectory(outputDir string) {
	if outputDir == "" {
		return
	}

	hlsBaseDir, err := conf.GetHLSDirectory()
	if err != nil {
		return
	}

	secFS, err := securefs.New(hlsBaseDir)
	if err != nil {
		return
	}
	defer func() {
		if closeErr := secFS.Close(); closeErr != nil {
			GetLogger().Error("Failed to close secure filesystem", logger.Error(closeErr))
		}
	}()

	if secFS.ExistsNoErr(outputDir) {
		GetLogger().Debug("Removing stream directory", logger.String("output_dir", outputDir))
		if removeErr := secFS.RemoveAll(outputDir); removeErr != nil {
			GetLogger().Error("Failed to remove stream directory", logger.Error(removeErr))
		}
	}
}

// cleanupStreamTracking removes all tracking data for a stream.
func (c *Controller) cleanupStreamTracking(sourceID string) {
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
}

// waitForHLSPlaylist waits for the playlist file to be ready
func (c *Controller) waitForHLSPlaylist(ctx echo.Context, sourceID string, stream *HLSStreamInfo) bool {
	hlsBaseDir, err := conf.GetHLSDirectory()
	if err != nil {
		return false
	}

	secFS, err := securefs.New(hlsBaseDir)
	if err != nil {
		return false
	}
	defer func() {
		if err := secFS.Close(); err != nil {
			GetLogger().Error("Failed to close secure filesystem", logger.Error(err))
		}
	}()

	playlistCtx, cancel := context.WithTimeout(ctx.Request().Context(), hlsPlaylistWaitTimeout)
	defer cancel()

	// Use ticker for polling, let context timeout control the overall duration
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// Poll until playlist is ready, stream is removed, or context times out
	for {
		if secFS.ExistsNoErr(stream.PlaylistPath) {
			data, err := secFS.ReadFile(stream.PlaylistPath)
			if err == nil && len(data) > 0 && strings.Contains(string(data), "#EXTM3U") {
				return true
			}
		}

		if !c.hlsStreamExists(sourceID) {
			return false
		}

		// Wait for next tick or context cancellation
		select {
		case <-playlistCtx.Done():
			return false
		case <-ticker.C:
			// Continue polling
		}
	}
}

// logHLSClientConnection logs client connections with rate limiting
func (c *Controller) logHLSClientConnection(sourceID, clientIP, requestPath string) {
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
		if strings.HasPrefix(requestPath, "segment00") {
			streamStartMsg = " (streaming started)"
		}
		GetLogger().Info("HLS stream request",
			logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
			logger.String("client_ip", clientIP),
			logger.String("status", streamStartMsg))
	}
}

// CleanupAllHLSStreams removes all HLS streams (called on shutdown)
func (c *Controller) CleanupAllHLSStreams() error {
	// Clone and clear streams atomically using Go 1.21+ maps package
	hlsMgr.streamsMu.Lock()
	streamsToClean := maps.Clone(hlsMgr.streams)
	clear(hlsMgr.streams)
	hlsMgr.streamsMu.Unlock()

	// Cleanup each stream
	for sourceID, stream := range streamsToClean {
		c.performHLSCleanup(sourceID, stream, "server shutdown")
	}

	// Clean remaining directories
	if err := c.cleanupHLSDirectories(); err != nil {
		return err
	}

	if runtime.GOOS == OSWindows {
		securefs.CleanupNamedPipes()
	}

	return nil
}

// cleanupHLSDirectories removes all stream directories from the HLS base directory.
func (c *Controller) cleanupHLSDirectories() error {
	hlsBaseDir, err := conf.GetHLSDirectory()
	if err != nil {
		return fmt.Errorf("failed to get HLS directory: %w", err)
	}

	secFS, err := securefs.New(hlsBaseDir)
	if err != nil {
		return fmt.Errorf("failed to create secure filesystem: %w", err)
	}
	defer func() {
		if closeErr := secFS.Close(); closeErr != nil {
			GetLogger().Error("Failed to close secure filesystem", logger.Error(closeErr))
		}
	}()

	entries, err := secFS.ReadDir(hlsBaseDir)
	if err != nil {
		return fmt.Errorf("failed to read HLS directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "stream_") {
			streamDir := filepath.Join(hlsBaseDir, entry.Name())
			GetLogger().Debug("Removing HLS stream directory", logger.String("stream_dir", streamDir))
			if removeErr := secFS.RemoveAll(streamDir); removeErr != nil {
				GetLogger().Error("Failed to remove stream directory", logger.Error(removeErr))
			}
		}
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
			GetLogger().Info("HLS activity sync stopped")
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
	GetLogger().Info("Stream inactive, marking for cleanup",
		logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
		logger.Duration("inactive_duration", inactiveDuration),
		logger.Duration("timeout", hlsStreamInactivityTimeout),
		logger.Int("client_count", clientCount))
	return true
}

// getStreamClientCount returns the number of clients for a stream
func getStreamClientCount(sourceID string) int {
	hlsMgr.clientsMu.Lock()
	defer hlsMgr.clientsMu.Unlock()

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
	defer hlsMgr.streamsMu.Unlock()

	stream, exists := hlsMgr.streams[sourceID]
	if exists {
		delete(hlsMgr.streams, sourceID)
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

	if s.FFmpegCmd != nil && s.FFmpegCmd.Process != nil {
		if _, err := s.FFmpegCmd.Process.Wait(); err != nil {
			GetLogger().Error("Failed to wait for FFmpeg process", logger.Error(err))
		}
	}

	// Close log file after process exits (must be done after Wait())
	if s.logFile != nil {
		if closeErr := s.logFile.Close(); closeErr != nil {
			GetLogger().Error("Failed to close log file", logger.Error(closeErr))
		}
	}

	cleanupStreamDirectory(s.OutputDir)
	GetLogger().Debug("Cleaned up inactive stream", logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)))
}

// cleanupStreamDirectory removes the stream's output directory
func cleanupStreamDirectory(outputDir string) {
	if outputDir == "" {
		return
	}

	hlsBaseDir, err := conf.GetHLSDirectory()
	if err != nil {
		GetLogger().Error("Failed to get HLS directory", logger.Error(err))
		return
	}

	secFS, err := securefs.New(hlsBaseDir)
	if err != nil {
		GetLogger().Error("Failed to create secure filesystem", logger.Error(err))
		return
	}
	defer func() {
		if closeErr := secFS.Close(); closeErr != nil {
			GetLogger().Error("Failed to close secure filesystem", logger.Error(closeErr))
		}
	}()

	if removeErr := secFS.RemoveAll(outputDir); removeErr != nil {
		GetLogger().Error("Failed to remove output directory", logger.Error(removeErr))
	}
}
