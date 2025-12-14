// internal/api/v2/audio_hls.go
package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
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
	hlsLogCooldown    = 60 * time.Second      // Only log client connections once per this duration
	hlsVerboseEnvVar  = "HLS_VERBOSE_LOGGING" // Environment variable to enable verbose logging
	hlsVerboseTimeout = 5 * time.Minute       // Verbose logging window at startup
)

// HLSStreamInfo contains information about an active HLS streaming session
type HLSStreamInfo struct {
	SourceID     string             // Original audio source identifier
	FFmpegCmd    *exec.Cmd          // FFmpeg process handle
	OutputDir    string             // Directory containing HLS files
	PlaylistPath string             // Path to the m3u8 playlist file
	FifoPipe     string             // Named pipe path (platform-specific)
	ctx          context.Context    // Stream lifecycle context
	cancel       context.CancelFunc // Cancel function for cleanup
}

// HLSStreamStatus represents the current status of an HLS stream (API response)
type HLSStreamStatus struct {
	Status        string `json:"status"` // "starting" or "ready"
	Source        string `json:"source"` // Source identifier
	PlaylistPath  string `json:"playlist_path,omitempty"`
	ActiveClients int    `json:"active_clients"`
	PlaylistReady bool   `json:"playlist_ready"`
}

// HLSHeartbeatRequest represents a client heartbeat message
type HLSHeartbeatRequest struct {
	SourceID string `json:"source_id"`
	ClientID string `json:"client_id,omitempty"` // Optional, server can identify from request
}

// hlsManager manages HLS streaming state
// TODO: Move to Controller struct during httpcontroller refactoring
type hlsManager struct {
	// Active streams indexed by sourceID
	streams   map[string]*HLSStreamInfo
	streamsMu sync.Mutex

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
}

// Global HLS manager instance
// TODO: Move to Controller struct during httpcontroller refactoring
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
	authMiddleware := c.getEffectiveAuthMiddleware()

	// HLS streaming routes - all require authentication
	hlsGroup := c.Group.Group("/streams/hls")
	hlsGroup.Use(authMiddleware)

	// Stream control endpoints
	hlsGroup.POST("/:sourceID/start", c.StartHLSStream)
	hlsGroup.POST("/:sourceID/stop", c.StopHLSStream)
	hlsGroup.POST("/heartbeat", c.HLSHeartbeat)

	// Stream content endpoints (playlist and segments)
	hlsGroup.GET("/:sourceID/playlist.m3u8", c.ServeHLSPlaylist)
	hlsGroup.GET("/:sourceID/*", c.ServeHLSContent)
}

// StartHLSStream initiates an HLS stream for a specific audio source
// POST /api/v2/streams/hls/:sourceID/start
func (c *Controller) StartHLSStream(ctx echo.Context) error {
	sourceID, err := c.validateAndDecodeSourceID(ctx)
	if err != nil {
		return err
	}

	clientID := c.generateClientID(ctx)

	c.logAPIRequest(ctx, slog.LevelInfo, "HLS stream start requested",
		"source_id", privacy.SanitizeRTSPUrl(sourceID),
		"client_id", clientID)

	// Verify source exists
	if !myaudio.HasCaptureBuffer(sourceID) {
		return c.HandleError(ctx, nil, "Audio source not found", http.StatusNotFound)
	}

	// Cleanup any existing stream for this source
	c.cleanupExistingHLSStream(sourceID)

	// Register client and update activity with grace period
	c.updateHLSActivity(sourceID, clientID, "stream_start", hlsNewStreamGracePeriod)

	// Create or get the HLS stream
	stream, err := c.getOrCreateHLSStream(ctx.Request().Context(), sourceID)
	if err != nil {
		c.logAPIRequest(ctx, slog.LevelError, "Failed to create HLS stream",
			"source_id", privacy.SanitizeRTSPUrl(sourceID),
			"error", err.Error())
		return c.HandleError(ctx, err, "Failed to start audio stream", http.StatusInternalServerError)
	}

	// Check if playlist is ready
	playlistReady := c.waitForHLSPlaylist(ctx, sourceID, stream)

	// Get client count
	clientCount := c.getHLSClientCount(sourceID)

	status := "starting"
	if playlistReady {
		status = "ready"
		c.logAPIRequest(ctx, slog.LevelInfo, "HLS stream ready",
			"source_id", privacy.SanitizeRTSPUrl(sourceID),
			"playlist_path", stream.PlaylistPath)
	}

	return ctx.JSON(http.StatusOK, HLSStreamStatus{
		Status:        status,
		Source:        sourceID,
		PlaylistPath:  stream.PlaylistPath,
		ActiveClients: clientCount,
		PlaylistReady: playlistReady,
	})
}

// StopHLSStream stops an HLS stream for a specific client
// POST /api/v2/streams/hls/:sourceID/stop
func (c *Controller) StopHLSStream(ctx echo.Context) error {
	sourceID, err := c.validateAndDecodeSourceID(ctx)
	if err != nil {
		return err
	}

	clientID := c.generateClientID(ctx)

	c.logAPIRequest(ctx, slog.LevelInfo, "HLS stream stop requested",
		"source_id", privacy.SanitizeRTSPUrl(sourceID),
		"client_id", clientID)

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
	if ctx.QueryParam("disconnect") == "true" || ctx.QueryParam("status") == "disconnect" {
		return c.handleHLSDisconnect(ctx, heartbeat.SourceID, clientID)
	}

	// Validate stream exists
	if !c.hlsStreamExists(heartbeat.SourceID) {
		if hlsMgr.verboseLogging {
			c.logAPIRequest(ctx, slog.LevelWarn, "Heartbeat for non-existent stream",
				"source_id", privacy.SanitizeRTSPUrl(heartbeat.SourceID))
		}
		return ctx.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}

	// Update activity
	c.updateHLSActivity(heartbeat.SourceID, clientID, "heartbeat")

	return ctx.JSON(http.StatusOK, map[string]string{"status": "ok"})
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
			log.Printf("Failed to close secure filesystem: %v", err)
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

		// Return temporary empty playlist
		emptyPlaylist := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:0
#EXT-X-PLAYLIST-TYPE:EVENT
`
		ctx.Response().Header().Set("Retry-After", "2")
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
			log.Printf("Failed to close secure filesystem: %v", err)
		}
	}()

	// Validate and build segment path
	cleanPath := filepath.Clean("/" + decodedPath)
	if strings.Contains(cleanPath, "..") || cleanPath == "/" {
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
func (c *Controller) generateClientID(ctx echo.Context) string {
	clientIP := ctx.RealIP()
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
func (c *Controller) setHLSHeaders(ctx echo.Context) {
	ctx.Response().Header().Set("Access-Control-Allow-Origin", "*")
	ctx.Response().Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	ctx.Response().Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization")
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
func (c *Controller) getOrCreateHLSStream(ctx context.Context, sourceID string) (*HLSStreamInfo, error) {
	// Check for existing stream
	if stream := c.getHLSStream(sourceID); stream != nil {
		return stream, nil
	}

	log.Printf("Creating new HLS stream for source: %s", privacy.SanitizeRTSPUrl(sourceID))

	// Generate filesystem-safe name
	filesystemSafeID := generateFilesystemSafeName(sourceID)

	// Create stream context
	streamCtx, streamCancel := context.WithCancel(ctx)

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
			log.Printf("Failed to close secure filesystem: %v", err)
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
			log.Printf("Failed to remove output directory: %v", removeErr)
		}
		streamCancel()
		return nil, err
	}

	// Determine reader path based on platform
	readerPath := fifoPath
	if runtime.GOOS == "windows" {
		readerPath = pipeName
	}

	// Setup and start FFmpeg
	cmd, err := c.setupHLSFFmpeg(streamCtx, ffmpegPath, readerPath, outputDir, playlistPath)
	if err != nil {
		streamCancel()
		return nil, fmt.Errorf("failed to setup FFmpeg: %w", err)
	}

	// Setup Windows-specific stdin pipe handling
	if runtime.GOOS == "windows" {
		if err := c.setupWindowsAudioFeed(streamCtx, sourceID, cmd); err != nil {
			streamCancel()
			return nil, err
		}
	}

	// Setup FFmpeg logging and start the process
	if err := c.setupFFmpegLogging(secFS, cmd, hlsBaseDir, outputDir); err != nil {
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
				log.Printf("Failed to kill FFmpeg process: %v", killErr)
			}
			if _, waitErr := stream.FFmpegCmd.Process.Wait(); waitErr != nil {
				log.Printf("Failed to wait for FFmpeg process: %v", waitErr)
			}
		}
		if removeErr := secFS.RemoveAll(outputDir); removeErr != nil {
			log.Printf("Failed to remove output directory: %v", removeErr)
		}

		log.Printf("Race condition detected, using existing stream for %s", privacy.SanitizeRTSPUrl(sourceID))
		return existingStream, nil
	}

	hlsMgr.streams[sourceID] = stream
	hlsMgr.streamsMu.Unlock()

	// Initialize activity
	c.updateHLSActivity(sourceID, "", "stream_creation")

	// Start audio feed (non-Windows platforms)
	if runtime.GOOS != "windows" {
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
	if err := secFS.MkdirAll(outputDir, 0o755); err != nil {
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
	return exec.CommandContext(ctx, ffmpegPath, args...), nil
}

// buildFFmpegArgs constructs FFmpeg command line arguments
func (c *Controller) buildFFmpegArgs(inputSource, outputDir, playlistPath string) []string {
	settings := c.Settings.WebServer.LiveStream

	// Apply defaults and limits
	bitrate := 128
	if settings.BitRate > 0 {
		switch {
		case settings.BitRate < 16:
			bitrate = 16
		case settings.BitRate > 320:
			bitrate = 320
		default:
			bitrate = settings.BitRate
		}
	}

	sampleRate := 48000
	if settings.SampleRate > 0 {
		sampleRate = settings.SampleRate
	}

	segmentLength := 2
	if settings.SegmentLength > 0 {
		switch {
		case settings.SegmentLength < 1:
			segmentLength = 1
		case settings.SegmentLength > 30:
			segmentLength = 30
		default:
			segmentLength = settings.SegmentLength
		}
	}

	logLevel := "warning"
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
				log.Printf("Failed to close stdin: %v", err)
			}
		}()
		log.Printf("Starting audio feed via stdin for source %s", privacy.SanitizeRTSPUrl(sourceID))

		audioChan, cleanup, err := c.setupAudioCallback(sourceID)
		if err != nil {
			log.Printf("Error setting up audio callback: %v", err)
			return
		}
		defer cleanup()

		for {
			select {
			case <-ctx.Done():
				log.Printf("Audio feed terminated: context cancelled for source %s", privacy.SanitizeRTSPUrl(sourceID))
				return
			case data, ok := <-audioChan:
				if !ok {
					log.Printf("Audio channel closed for source %s", privacy.SanitizeRTSPUrl(sourceID))
					return
				}

				written := 0
				for written < len(data) {
					n, err := stdin.Write(data[written:])
					if err != nil {
						log.Printf("Error writing to FFmpeg stdin: %v", err)
						return
					}
					written += n
				}
			}
		}
	}()

	return nil
}

// setupFFmpegLogging configures FFmpeg output logging
func (c *Controller) setupFFmpegLogging(secFS *securefs.SecureFS, cmd *exec.Cmd, hlsBaseDir, outputDir string) error {
	logFilePath := filepath.Join(outputDir, "ffmpeg.log")

	isWithin, err := securefs.IsPathWithinBase(hlsBaseDir, logFilePath)
	if err != nil || !isWithin {
		return fmt.Errorf("security error: log file path outside HLS base")
	}

	logFile, err := secFS.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("failed to create ffmpeg log file: %w", err)
	}

	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		if closeErr := logFile.Close(); closeErr != nil {
			log.Printf("Failed to close log file: %v", closeErr)
		}
		return fmt.Errorf("failed to start FFmpeg: %w", err)
	}

	// Close log file after FFmpeg exits
	go func(f *os.File, p *exec.Cmd) {
		_ = p.Wait()
		_ = f.Close()
	}(logFile, cmd)

	log.Printf("FFmpeg process started for output: %s", outputDir)
	return nil
}

// setupAudioCallback sets up the audio callback channel
func (c *Controller) setupAudioCallback(sourceID string) (audioChan chan []byte, cleanup func(), err error) {
	audioChan = make(chan []byte, 1024)

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
	log.Printf("Registered audio callback for source: %s", privacy.SanitizeRTSPUrl(sourceID))

	cleanup = func() {
		myaudio.UnregisterBroadcastCallback(sourceID)
		log.Printf("Unregistered audio callback for source: %s", privacy.SanitizeRTSPUrl(sourceID))
	}

	return audioChan, cleanup, nil
}

// feedAudioToFFmpeg feeds audio data to FFmpeg via FIFO (Unix platforms)
func (c *Controller) feedAudioToFFmpeg(sourceID, pipePath string, ctx context.Context) {
	sanitizedID := privacy.SanitizeRTSPUrl(sourceID)
	log.Printf("Starting audio feed for source %s to pipe %s", sanitizedID, pipePath)

	hlsBaseDir, err := conf.GetHLSDirectory()
	if err != nil {
		log.Printf("Error getting HLS directory: %v", err)
		return
	}

	secFS, err := securefs.New(hlsBaseDir)
	if err != nil {
		log.Printf("Error creating secure filesystem: %v", err)
		return
	}
	defer func() {
		if err := secFS.Close(); err != nil {
			log.Printf("Failed to close secure filesystem: %v", err)
		}
	}()

	// Open FIFO
	fifo, err := secFS.OpenFile(pipePath, os.O_WRONLY, 0)
	if err != nil {
		log.Printf("Error opening pipe: %v", err)
		return
	}
	defer func() {
		if err := fifo.Close(); err != nil {
			log.Printf("Failed to close FIFO: %v", err)
		}
	}()

	// Setup audio callback
	audioChan, cleanup, err := c.setupAudioCallback(sourceID)
	if err != nil {
		log.Printf("Error setting up audio callback: %v", err)
		return
	}
	defer cleanup()

	log.Printf("Audio feed ready for source %s", sanitizedID)

	dataWritten := false
	for {
		select {
		case <-ctx.Done():
			log.Printf("Audio feed stopped: context cancelled for source %s", sanitizedID)
			return
		case data, ok := <-audioChan:
			if !ok {
				log.Printf("Audio channel closed for source %s", sanitizedID)
				return
			}

			if _, err := fifo.Write(data); err != nil {
				log.Printf("Error writing to FIFO: %v", err)
				return
			}

			if !dataWritten {
				log.Printf("First audio data written for source %s", sanitizedID)
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
	hlsMgr.streamsMu.Lock()
	defer hlsMgr.streamsMu.Unlock()
	return hlsMgr.streams[sourceID]
}

// hlsStreamExists checks if a stream exists
func (c *Controller) hlsStreamExists(sourceID string) bool {
	hlsMgr.streamsMu.Lock()
	defer hlsMgr.streamsMu.Unlock()
	_, exists := hlsMgr.streams[sourceID]
	return exists
}

// getHLSClientCount returns the number of active clients for a stream
func (c *Controller) getHLSClientCount(sourceID string) int {
	hlsMgr.clientsMu.Lock()
	defer hlsMgr.clientsMu.Unlock()
	if clients, exists := hlsMgr.clients[sourceID]; exists {
		return len(clients)
	}
	return 0
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
			c.logAPIRequest(ctx, slog.LevelWarn, "Ignoring premature disconnect",
				"source_id", privacy.SanitizeRTSPUrl(sourceID))
			c.updateHLSActivity(sourceID, clientID, "continued-connection")
			return ctx.JSON(http.StatusOK, map[string]string{"status": "ok"})
		}
	}
	hlsMgr.activityMu.Unlock()

	c.logAPIRequest(ctx, slog.LevelInfo, "Client announced disconnection",
		"source_id", privacy.SanitizeRTSPUrl(sourceID),
		"client_id", clientID)

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

	log.Printf("Cleaning up existing stream for source %s", privacy.SanitizeRTSPUrl(sourceID))

	if stream.cancel != nil {
		stream.cancel()
	}

	var cmd *exec.Cmd
	if stream.FFmpegCmd != nil && stream.FFmpegCmd.Process != nil {
		cmd = stream.FFmpegCmd
	}

	outputDir := stream.OutputDir
	delete(hlsMgr.streams, sourceID)
	hlsMgr.streamsMu.Unlock()

	// Wait for process termination
	if cmd != nil && cmd.Process != nil {
		if _, err := cmd.Process.Wait(); err != nil {
			log.Printf("Failed to wait for FFmpeg process: %v", err)
		}
	}

	// Clean up directory
	if outputDir != "" {
		hlsBaseDir, err := conf.GetHLSDirectory()
		if err == nil {
			if secFS, err := securefs.New(hlsBaseDir); err == nil {
				defer func() {
					if closeErr := secFS.Close(); closeErr != nil {
						log.Printf("Failed to close secure filesystem: %v", closeErr)
					}
				}()
				if secFS.ExistsNoErr(outputDir) {
					if removeErr := secFS.RemoveAll(outputDir); removeErr != nil {
						log.Printf("Failed to remove output directory: %v", removeErr)
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

	log.Printf("Cleaning up HLS stream for source: %s", privacy.SanitizeRTSPUrl(sourceID))
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

	log.Printf("Stopping HLS stream for source %s: %s", privacy.SanitizeRTSPUrl(sourceID), reason)
	delete(hlsMgr.streams, sourceID)
	hlsMgr.streamsMu.Unlock()

	c.performHLSCleanup(sourceID, stream, reason)
}

// performHLSCleanup performs the actual cleanup of stream resources
func (c *Controller) performHLSCleanup(sourceID string, stream *HLSStreamInfo, reason string) {
	log.Printf("Performing HLS cleanup for source %s: %s", privacy.SanitizeRTSPUrl(sourceID), reason)

	// Cancel context
	if stream.cancel != nil {
		stream.cancel()
	}

	// Kill FFmpeg process
	if stream.FFmpegCmd != nil && stream.FFmpegCmd.Process != nil {
		go func(cmd *exec.Cmd) {
			if _, err := cmd.Process.Wait(); err != nil {
				log.Printf("FFmpeg process wait error: %v", err)
			}
			log.Printf("FFmpeg process terminated for source %s", privacy.SanitizeRTSPUrl(sourceID))
		}(stream.FFmpegCmd)
	}

	// Clean up output directory
	if stream.OutputDir != "" {
		hlsBaseDir, err := conf.GetHLSDirectory()
		if err == nil {
			if secFS, err := securefs.New(hlsBaseDir); err == nil {
				defer func() {
					if closeErr := secFS.Close(); closeErr != nil {
						log.Printf("Failed to close secure filesystem: %v", closeErr)
					}
				}()
				if secFS.ExistsNoErr(stream.OutputDir) {
					log.Printf("Removing stream directory: %s", stream.OutputDir)
					if removeErr := secFS.RemoveAll(stream.OutputDir); removeErr != nil {
						log.Printf("Failed to remove stream directory: %v", removeErr)
					}
				}
			}
		}
	}

	// Clean up client tracking
	hlsMgr.clientsMu.Lock()
	delete(hlsMgr.clients, sourceID)
	hlsMgr.clientsMu.Unlock()

	// Clean up activity tracking
	hlsMgr.activityMu.Lock()
	delete(hlsMgr.activity, sourceID)
	for key := range hlsMgr.clientActivity {
		if strings.HasPrefix(key, sourceID+":") {
			delete(hlsMgr.clientActivity, key)
		}
	}
	hlsMgr.activityMu.Unlock()

	log.Printf("HLS stream cleanup completed for source %s", privacy.SanitizeRTSPUrl(sourceID))
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
			log.Printf("Failed to close secure filesystem: %v", err)
		}
	}()

	playlistCtx, cancel := context.WithTimeout(ctx.Request().Context(), hlsPlaylistWaitTimeout)
	defer cancel()

	for range 30 {
		select {
		case <-playlistCtx.Done():
			return false
		default:
			if secFS.ExistsNoErr(stream.PlaylistPath) {
				data, err := secFS.ReadFile(stream.PlaylistPath)
				if err == nil && len(data) > 0 && strings.Contains(string(data), "#EXTM3U") {
					return true
				}
			}

			if !c.hlsStreamExists(sourceID) {
				return false
			}

			time.Sleep(1 * time.Second)
		}
	}

	return false
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
		log.Printf("HLS stream for source: %s from %s%s",
			privacy.SanitizeRTSPUrl(sourceID), clientIP, streamStartMsg)
	}
}

// CleanupAllHLSStreams removes all HLS streams (called on shutdown)
func (c *Controller) CleanupAllHLSStreams() error {
	hlsMgr.streamsMu.Lock()
	streamsToClean := make(map[string]*HLSStreamInfo)
	for sourceID, stream := range hlsMgr.streams {
		streamsToClean[sourceID] = stream
		delete(hlsMgr.streams, sourceID)
	}
	hlsMgr.streamsMu.Unlock()

	for sourceID, stream := range streamsToClean {
		c.performHLSCleanup(sourceID, stream, "server shutdown")
	}

	// Clean remaining directories
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
			log.Printf("Failed to close secure filesystem: %v", closeErr)
		}
	}()

	entries, err := secFS.ReadDir(hlsBaseDir)
	if err != nil {
		return fmt.Errorf("failed to read HLS directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "stream_") {
			streamDir := filepath.Join(hlsBaseDir, entry.Name())
			log.Printf("Removing HLS stream directory: %s", streamDir)
			if removeErr := secFS.RemoveAll(streamDir); removeErr != nil {
				log.Printf("Failed to remove stream directory: %v", removeErr)
			}
		}
	}

	if runtime.GOOS == "windows" {
		securefs.CleanupNamedPipes()
	}

	return nil
}

// init initializes the HLS cleanup background task
func init() {
	// Start background cleanup task
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			syncHLSActivity()
		}
	}()
}

// syncHLSActivity checks for inactive streams and cleans them up
func syncHLSActivity() {
	activeStreamIDs := getActiveStreamIDs()
	streamsToCleanup := findInactiveStreams(activeStreamIDs)
	cleanupInactiveStreams(streamsToCleanup)
}

// getActiveStreamIDs returns a snapshot of all active stream IDs
func getActiveStreamIDs() []string {
	hlsMgr.streamsMu.Lock()
	defer hlsMgr.streamsMu.Unlock()

	activeStreamIDs := make([]string, 0, len(hlsMgr.streams))
	for sourceID := range hlsMgr.streams {
		activeStreamIDs = append(activeStreamIDs, sourceID)
	}
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
	log.Printf("Stream %s inactive for %v (timeout %v), clients: %d - marking for cleanup",
		privacy.SanitizeRTSPUrl(sourceID), inactiveDuration, hlsStreamInactivityTimeout, clientCount)
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
// TODO: Refactor to use proper dependency injection during httpcontroller refactoring
func cleanupStream(s *HLSStreamInfo, sourceID string) {
	if s.cancel != nil {
		s.cancel()
	}

	if s.FFmpegCmd != nil && s.FFmpegCmd.Process != nil {
		if _, err := s.FFmpegCmd.Process.Wait(); err != nil {
			log.Printf("Failed to wait for FFmpeg process: %v", err)
		}
	}

	cleanupStreamDirectory(s.OutputDir)
	log.Printf("Cleaned up inactive stream: %s", privacy.SanitizeRTSPUrl(sourceID))
}

// cleanupStreamDirectory removes the stream's output directory
func cleanupStreamDirectory(outputDir string) {
	if outputDir == "" {
		return
	}

	hlsBaseDir, err := conf.GetHLSDirectory()
	if err != nil {
		log.Printf("Failed to get HLS directory: %v", err)
		return
	}

	secFS, err := securefs.New(hlsBaseDir)
	if err != nil {
		log.Printf("Failed to create secure filesystem: %v", err)
		return
	}
	defer func() {
		if closeErr := secFS.Close(); closeErr != nil {
			log.Printf("Failed to close secure filesystem: %v", closeErr)
		}
	}()

	if removeErr := secFS.RemoveAll(outputDir); removeErr != nil {
		log.Printf("Failed to remove output directory: %v", removeErr)
	}
}
