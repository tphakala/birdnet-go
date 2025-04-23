package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/httpcontroller/securefs"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// HLSStreamInfo contains information about a streaming session
type HLSStreamInfo struct {
	SourceID     string
	FFmpegCmd    *exec.Cmd
	OutputDir    string
	PlaylistPath string
	FifoPipe     string // Windows named pipe path for platform compatibility
	// Add context and cancellation for managing stream lifecycle
	ctx    context.Context
	cancel context.CancelFunc
}

var (
	// Manage active HLS streams
	hlsStreams     = make(map[string]*HLSStreamInfo)
	hlsStreamMutex sync.Mutex

	// Track active clients per stream - each entry represents a distinct IP address
	hlsStreamClients     = make(map[string]map[string]bool) // sourceID -> clientID -> true
	hlsStreamClientMutex sync.Mutex

	// Master activity tracking system - this is the unified way to track activity
	// Each stream has a master activity timestamp that's updated when ANY client
	// requests EITHER a playlist or segment file or sends a heartbeat.
	// As long as any client is active, the stream stays alive.
	hlsStreamActivity      = make(map[string]time.Time) // sourceID -> lastActivityTime
	hlsStreamActivityMutex sync.Mutex

	// Track client-specific last activity time for false disconnect detection
	hlsStreamClientLastActivity = make(map[string]time.Time) // sourceID:clientID -> lastActivityTime

	// When no playlist, segment, or heartbeat has been received for this period,
	// a stream is considered inactive and will be cleaned up
	streamInactivityTimeout = 5 * time.Minute

	// Logging configuration
	hlsVerboseLogging        = false                 // Controlled by environment
	hlsVerboseLoggingTimeout = 5 * time.Minute       // How long to keep verbose logging enabled at startup
	hlsVerboseEnvVar         = "HLS_VERBOSE_LOGGING" // Environment variable to control logging

	// Store the last log time per client+source to reduce log spam
	hlsClientLogTime      = make(map[string]time.Time)
	hlsClientLogTimeMutex sync.Mutex

	// Log cooldown - only log once per client per this duration
	hlsLogCooldown = 60 * time.Second

	// Define global variables and constants for HLS streaming
	maxStreamLifetime = 6 * time.Hour // Maximum time a stream can live regardless of activity
)

// StreamStatus represents the current status of an HLS stream
type StreamStatus struct {
	Status        string `json:"status"`
	Source        string `json:"source"`
	PlaylistPath  string `json:"playlist_path,omitempty"`
	ActiveClients int    `json:"active_clients"`
	PlaylistReady bool   `json:"playlist_ready"`
}

// HLSHeartbeat represents a client heartbeat message
type HLSHeartbeat struct {
	SourceID string `json:"source_id"`
	ClientID string `json:"client_id,omitempty"` // Optional, server can identify client from request
}

// AudioStreamHLS handles HLS audio streaming
// API: GET /api/v1/audio-stream-hls/:sourceID/playlist.m3u8
func (h *Handlers) AudioStreamHLS(c echo.Context) error {
	// Create a context that's canceled when the request completes
	ctx, cancel := context.WithCancel(c.Request().Context())
	defer cancel() // Ensures cleanup when the function exits

	// Extract and validate basic parameters
	sourceID, clientID, hlsBaseDir, err := h.validateHLSRequest(c)
	if err != nil {
		return err
	}

	// Register client in client list and update stream activity
	updateStreamActivity(sourceID, clientID, "request")

	// Get or create HLS stream
	stream, err := getOrCreateHLSStream(ctx, sourceID)
	if err != nil {
		log.Printf("❌ Error creating HLS stream: %v - source: %s", err, sourceID)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to create audio stream")
	}

	// Determine what file is being requested
	rawPath := c.Param("*")
	// Decode %XX sequences first, then continue with validation
	requestPath, err := url.PathUnescape(rawPath)
	if err != nil {
		log.Printf("Invalid URL encoding in path: %s", rawPath)
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid URL encoding")
	}

	// No need to monitor context for this anymore - handled by heartbeats
	// NOTE: We're removing the call to monitorClientDisconnection

	// Log client connection if needed
	h.logClientConnection(sourceID, c.RealIP(), requestPath)

	// Add CORS headers to allow cross-origin requests
	h.addCorsHeaders(c)

	// Serve the appropriate file based on request
	if requestPath == "" || requestPath == "playlist.m3u8" {
		if hlsVerboseLogging {
			log.Printf("📄 Serving playlist for source %s requested by %s", sourceID, clientID)
		}
		return h.servePlaylistFile(c, stream, hlsBaseDir)
	}

	if hlsVerboseLogging && strings.HasSuffix(requestPath, ".ts") {
		log.Printf("📄 Serving segment %s for source %s requested by %s", requestPath, sourceID, clientID)
	}
	return h.serveSegmentFile(c, stream, requestPath, hlsBaseDir)
}

// validateHLSRequest validates the request parameters and permissions
func (h *Handlers) validateHLSRequest(c echo.Context) (sourceID, clientID, hlsBaseDir string, err error) {
	sourceID = c.Param("sourceID")
	clientIP := c.RealIP()

	// Create a standardized client ID using the helper function
	clientID = generateClientID(clientIP, c.Request().Header.Get("User-Agent"))

	if sourceID == "" {
		return "", "", "", echo.NewHTTPError(http.StatusBadRequest, "Source ID is required")
	}

	// Validate sourceID for security - ensure it only contains safe characters
	safeSourceIDRegex := regexp.MustCompile(`^[A-Za-z0-9_\-]+$`)
	if !safeSourceIDRegex.MatchString(sourceID) {
		log.Printf("🚨 Security warning: Invalid source ID format detected: %s", sourceID)
		return "", "", "", echo.NewHTTPError(http.StatusBadRequest, "Invalid source ID format")
	}

	// Check authentication if the server requires it
	server := c.Get("server")
	if server != nil {
		// Type assertion to access the server methods
		if s, ok := server.(interface {
			IsAccessAllowed(c echo.Context) bool
			isAuthenticationEnabled(c echo.Context) bool
		}); ok {
			// Check if authentication is required and access is allowed
			if s.isAuthenticationEnabled(c) && !s.IsAccessAllowed(c) {
				log.Printf("🔒 Authentication failed for HLS stream - source: %s, IP: %s", sourceID, clientIP)
				return "", "", "", echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
			}
		}
	}

	// Check if source exists and has a valid capture buffer
	if !myaudio.HasCaptureBuffer(sourceID) {
		log.Printf("❌ Audio source not found for HLS stream - source: %s, IP: %s", sourceID, clientIP)
		return "", "", "", echo.NewHTTPError(http.StatusNotFound, "Audio source not found")
	}

	// Get HLS base directory for security validation
	var baseDir string
	baseDir, err = conf.GetHLSDirectory()
	if err != nil {
		log.Printf("❌ Error getting HLS directory: %v", err)
		return "", "", "", echo.NewHTTPError(http.StatusInternalServerError, "Server configuration error")
	}

	return sourceID, clientID, baseDir, nil
}

// logClientConnection logs client connection information
func (h *Handlers) logClientConnection(sourceID, clientIP, requestPath string) {
	logKey := sourceID + "-" + clientIP
	shouldLog := false

	hlsClientLogTimeMutex.Lock()
	lastLogTime, exists := hlsClientLogTime[logKey]
	now := time.Now()

	// Only log if it's the first connection or if enough time has passed since last log
	if !exists || now.Sub(lastLogTime) > hlsLogCooldown {
		shouldLog = true
		hlsClientLogTime[logKey] = now
	}
	hlsClientLogTimeMutex.Unlock()

	// First segment request indicates actual stream start
	firstSegmentRequest := strings.HasPrefix(requestPath, "segment00")

	if shouldLog && (firstSegmentRequest || !exists) {
		streamStartMsg := ""
		if firstSegmentRequest {
			streamStartMsg = " (streaming started)"
		}
		log.Printf("🔌 HLS stream for source: %s from %s%s",
			sourceID, clientIP, streamStartMsg)
	}
}

// addCorsHeaders adds CORS headers to the response
func (h *Handlers) addCorsHeaders(c echo.Context) {
	c.Response().Header().Set("Access-Control-Allow-Origin", "*")
	c.Response().Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	c.Response().Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Accept")
}

// servePlaylistFile serves the HLS playlist file
func (h *Handlers) servePlaylistFile(c echo.Context, stream *HLSStreamInfo, hlsBaseDir string) error {
	// Update stream activity - playlist requests indicate active client
	updateStreamActivity(stream.SourceID, "", "playlist_request")

	if hlsVerboseLogging {
		log.Printf("📄 Updated activity timestamp for stream %s due to playlist request", stream.SourceID)
	}

	// Sanitize the path
	cleanPath := filepath.Clean("/playlist.m3u8")
	if strings.Contains(cleanPath, "..") || cleanPath == "/" {
		log.Printf("⚠️ Suspicious playlist path requested")
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid playlist path")
	}

	// Create a secure filesystem for operations
	secFS, err := securefs.New(hlsBaseDir)
	if err != nil {
		log.Printf("❌ Error creating secure filesystem: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Server error")
	}
	defer secFS.Close()

	// Set proper content type for m3u8 playlist
	c.Response().Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	// Add cache control headers to prevent caching
	c.Response().Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Response().Header().Set("Pragma", "no-cache")
	c.Response().Header().Set("Expires", "0")

	// Check if the playlist file exists
	if !secFS.ExistsNoErr(stream.PlaylistPath) {
		// If the playlist doesn't exist yet, check if FFmpeg is still running
		hlsStreamMutex.Lock()
		_, streamExists := hlsStreams[stream.SourceID]
		hlsStreamMutex.Unlock()

		if !streamExists {
			log.Printf("❌ HLS stream no longer exists for source %s", stream.SourceID)
			return echo.NewHTTPError(http.StatusNotFound, "Stream no longer exists")
		}

		// Send a temporary empty playlist to avoid client errors
		// This will cause the client to retry after a short delay
		log.Printf("⏳ Sending temporary empty playlist for source %s (real playlist not ready yet)", stream.SourceID)

		// Create a basic empty HLS playlist
		// Important: DO NOT include EXT-X-ENDLIST tag which signals the end of the stream
		// and would cause clients to disconnect and retry, creating a busy loop
		emptyPlaylist := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:0
#EXT-X-PLAYLIST-TYPE:EVENT
`
		// Add response header to tell client to wait 2 seconds before retry
		c.Response().Header().Set("Retry-After", "2")
		return c.String(http.StatusOK, emptyPlaylist)
	}

	// Serve the playlist file securely
	return secFS.ServeFile(c, stream.PlaylistPath)
}

// serveSegmentFile serves the HLS segment file
func (h *Handlers) serveSegmentFile(c echo.Context, stream *HLSStreamInfo, requestPath, hlsBaseDir string) error {
	// Validate segment path for path traversal prevention
	cleanPath := filepath.Clean("/" + requestPath)

	// Remove leading slash for concatenation
	safeRequestPath := cleanPath[1:]

	// For all requests, update stream activity - this indicates active playback
	updateStreamActivity(stream.SourceID, "", "segment_request")

	// Build the full segment path
	segmentPath := filepath.Join(stream.OutputDir, safeRequestPath)

	// Create a secure filesystem for operations
	secFS, err := securefs.New(hlsBaseDir)
	if err != nil {
		log.Printf("❌ Error creating secure filesystem: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Server error")
	}
	defer secFS.Close()

	// Use securefs to validate the path is within the stream's output directory
	isWithin, err := securefs.IsPathWithinBase(stream.OutputDir, segmentPath)
	if err != nil || !isWithin || cleanPath == "/" {
		log.Printf("⚠️ Suspicious segment path requested: %s", requestPath)
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid segment path")
	}

	// Check if segment file exists using secureFS
	if !secFS.ExistsNoErr(segmentPath) {
		log.Printf("❌ HLS segment file does not exist at %s", segmentPath)
		return echo.NewHTTPError(http.StatusNotFound, "Segment file not found")
	}

	// Set appropriate Content-Type based on file extension
	switch filepath.Ext(safeRequestPath) {
	case ".ts":
		// For .ts segment files
		c.Response().Header().Set("Content-Type", "audio/mp2t")
		// Allow caching of segments for a short time
		c.Response().Header().Set("Cache-Control", "public, max-age=60")
	case ".m4s":
		// RFC 4337 / common practice for fMP4 HLS segments
		c.Response().Header().Set("Content-Type", "video/iso.segment")
		// Allow caching of segments for a short time
		c.Response().Header().Set("Cache-Control", "public, max-age=60")
	case ".mp4":
		c.Response().Header().Set("Content-Type", "audio/mp4")
		// Allow caching of initialization segments
		c.Response().Header().Set("Cache-Control", "public, max-age=3600")
	default:
		c.Response().Header().Set("Content-Type", "application/octet-stream")
	}

	// Serve the segment file securely
	return secFS.ServeFile(c, segmentPath)
}

// buildFFmpegArgs constructs the command line arguments for the FFmpeg HLS process
func buildFFmpegArgs(inputSource, outputDir, playlistPath string) []string {
	// Get live stream settings from config
	liveStreamSettings := conf.Setting().WebServer.LiveStream

	// Set default values if not configured
	bitrate := 128 // Default bitrate in kbps
	if liveStreamSettings.BitRate > 0 {
		// Enforce bitrate limits between 16 and 320 kbps
		switch {
		case liveStreamSettings.BitRate < 16:
			bitrate = 16
			log.Printf("⚠️ Configured bitrate %d kbps is too low, using minimum 16 kbps", liveStreamSettings.BitRate)
		case liveStreamSettings.BitRate > 320:
			bitrate = 320
			log.Printf("⚠️ Configured bitrate %d kbps is too high, using maximum 320 kbps", liveStreamSettings.BitRate)
		default:
			bitrate = liveStreamSettings.BitRate
		}
	}

	sampleRate := 48000
	if liveStreamSettings.SampleRate > 0 {
		sampleRate = liveStreamSettings.SampleRate
	}

	segmentLength := 2 // Default segment length in seconds
	if liveStreamSettings.SegmentLength > 0 {
		// Enforce segment length limits between 1 and 30 seconds
		switch {
		case liveStreamSettings.SegmentLength < 1:
			segmentLength = 1
			log.Printf("⚠️ Configured segment length %d seconds is too short, using minimum 1 second", liveStreamSettings.SegmentLength)
		case liveStreamSettings.SegmentLength > 30:
			segmentLength = 30
			log.Printf("⚠️ Configured segment length %d seconds is too long, using maximum 30 seconds", liveStreamSettings.SegmentLength)
		default:
			segmentLength = liveStreamSettings.SegmentLength
		}
	}

	logLevel := "warning"
	if liveStreamSettings.FfmpegLogLevel != "" {
		logLevel = liveStreamSettings.FfmpegLogLevel
	}

	// Base arguments, starting with input format if needed
	args := []string{}

	// Input format arguments common to both FIFO and pipe:0
	inputFormatArgs := []string{
		"-f", "s16le", // Input format: 16-bit PCM
		"-ar", fmt.Sprintf("%d", sampleRate), // Sample rate from config
		"-ac", "1", // Channels: mono
	}

	args = append(args, inputFormatArgs...)
	args = append(args, "-i", inputSource)

	// Common output arguments
	outputArgs := []string{
		"-y",          // overwrite existing files if they survived a previous run
		"-c:a", "aac", // Codec: AAC
		"-b:a", fmt.Sprintf("%dk", bitrate), // Bitrate from config with limits
		"-f", "hls", // Format: HLS
		"-hls_time", fmt.Sprintf("%d", segmentLength), // Segment duration from config with limits
		"-hls_list_size", "3", // Keep 3 segments in playlist
		"-hls_flags", "delete_segments+temp_file", // Delete old segments and use temp files
		"-hls_segment_type", "fmp4", // Use fmp4 segments
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_init_time", "3", // Initial segment length: 3 seconds for faster startup
		"-hls_allow_cache", "1", // Allow caching
		"-movflags", "faststart+empty_moov+separate_moof",
		"-start_number", "0", // Start with segment 0
		"-loglevel", logLevel, // Set ffmpeg logging level from config
		"-hls_segment_filename", filepath.ToSlash(filepath.Join(outputDir, "segment%03d.m4s")),
		playlistPath, // Output playlist
	}

	args = append(args, outputArgs...)

	return args
}

// validateSourceID validates the sourceID is safe for file paths
func validateSourceID(sourceID string) (string, error) {
	// Validate sourceID for security - ensure it only contains safe characters
	safeSourceIDRegex := regexp.MustCompile(`^[A-Za-z0-9_\-]+$`)
	if !safeSourceIDRegex.MatchString(sourceID) {
		return "", fmt.Errorf("invalid source ID format: contains unauthorized characters")
	}

	// Apply strict sanitization for defense in depth
	reSafe := regexp.MustCompile(`[^A-Za-z0-9_\-]`)
	safeSourceID := reSafe.ReplaceAllString(sourceID, "_")

	// Ensure the sanitized ID is still valid
	if safeSourceID == "" {
		return "", fmt.Errorf("invalid source ID after sanitization")
	}

	return safeSourceID, nil
}

// prepareStreamDirectory creates and validates the output directory
func prepareStreamDirectory(secFS *securefs.SecureFS, hlsBaseDir, safeSourceID string) (outputDir, playlistPath string, err error) {
	outputDir = filepath.Join(hlsBaseDir, fmt.Sprintf("stream_%s", safeSourceID))

	// Verify the output directory is within the HLS base directory for safety
	isWithin, err := securefs.IsPathWithinBase(hlsBaseDir, outputDir)
	if err != nil {
		return "", "", fmt.Errorf("failed to validate output directory: %w", err)
	}

	if !isWithin {
		return "", "", fmt.Errorf("security error: output directory would be outside HLS base directory")
	}

	// Ensure the directory exists and is empty
	if secFS.ExistsNoErr(outputDir) {
		log.Printf("🧹 Removing existing output directory: %s", outputDir)
		if err := secFS.RemoveAll(outputDir); err != nil {
			return "", "", fmt.Errorf("failed to clean HLS directory: %w", err)
		}
	}

	log.Printf("📁 Creating new output directory: %s", outputDir)
	if err := secFS.MkdirAll(outputDir, 0o755); err != nil {
		return "", "", fmt.Errorf("failed to create HLS directory: %w", err)
	}

	// Verify the directory was created successfully
	if !secFS.ExistsNoErr(outputDir) {
		return "", "", fmt.Errorf("failed to create HLS directory: directory doesn't exist after creation")
	}

	// Create playlist file path
	playlistPath = filepath.Join(outputDir, "playlist.m3u8")

	// Verify the playlist file is within the HLS base directory
	isWithin, err = securefs.IsPathWithinBase(hlsBaseDir, playlistPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to validate playlist path: %w", err)
	}

	if !isWithin {
		return "", "", fmt.Errorf("security error: playlist path would be outside HLS base directory")
	}

	return outputDir, playlistPath, nil
}

// setupHLSFifo prepares the FIFO pipe for audio streaming
func setupHLSFifo(secFS *securefs.SecureFS, hlsBaseDir, outputDir string) (fifoPath, pipeName string, err error) {
	fifoPath = filepath.Join(outputDir, "audio.pcm")

	// Verify the FIFO is within the HLS base directory
	isWithin, err := securefs.IsPathWithinBase(hlsBaseDir, fifoPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to validate FIFO path: %w", err)
	}

	if !isWithin {
		return "", "", fmt.Errorf("security error: FIFO path would be outside HLS base directory")
	}

	log.Printf("🔄 Creating FIFO for HLS stream: %s", fifoPath)
	// Use secure filesystem for FIFO creation
	if err := secFS.CreateFIFO(fifoPath); err != nil {
		return "", "", fmt.Errorf("failed to create FIFO: %w", err)
	}

	// Get the platform-specific pipe name for the FIFO
	pipeName = secFS.GetPipeName()
	return fifoPath, pipeName, nil
}

// setupFFmpeg creates and starts the FFmpeg process
func setupFFmpeg(ctx context.Context, sourceID, ffmpegPath, readerPath, outputDir, playlistPath string) (*exec.Cmd, error) {
	var cmd *exec.Cmd
	var ffmpegArgs []string

	if runtime.GOOS == "windows" {
		// For Windows, use the stdin pipe approach by calling buildFFmpegArgs
		ffmpegArgs = buildFFmpegArgs("pipe:0", outputDir, playlistPath)
	} else {
		// For Unix platforms, use the standard approach
		ffmpegArgs = buildFFmpegArgs(readerPath, outputDir, playlistPath)
	}

	cmd = exec.CommandContext(ctx, ffmpegPath, ffmpegArgs...)
	return cmd, nil
}

// getOrCreateHLSStream gets an existing stream or creates a new one
func getOrCreateHLSStream(ctx context.Context, sourceID string) (*HLSStreamInfo, error) {
	// Validate sourceID for security
	safeSourceID, err := validateSourceID(sourceID)
	if err != nil {
		return nil, err
	}

	// Check if stream exists
	if stream := getExistingStream(sourceID); stream != nil {
		return stream, nil
	}

	// Stream doesn't exist, we need to create it
	log.Printf("🎬 Creating new HLS stream for source: %s", sourceID)

	// Create a context that can be canceled to terminate the stream
	streamCtx, streamCancel := context.WithCancel(ctx)

	// Get HLS directory
	hlsBaseDir, err := conf.GetHLSDirectory()
	if err != nil {
		streamCancel() // Clean up context
		return nil, fmt.Errorf("failed to get HLS directory: %w", err)
	}

	// Create a securefs instance for filesystem operations
	secFS, err := securefs.New(hlsBaseDir)
	if err != nil {
		streamCancel() // Clean up context
		return nil, fmt.Errorf("failed to initialize secure filesystem: %w", err)
	}
	defer secFS.Close()

	// Prepare the output directory and playlist path
	outputDir, playlistPath, err := prepareStreamDirectory(secFS, hlsBaseDir, safeSourceID)
	if err != nil {
		streamCancel() // Clean up context
		return nil, err
	}

	// Get FFmpeg path from settings
	ffmpegPath := conf.Setting().Realtime.Audio.FfmpegPath
	if ffmpegPath == "" {
		streamCancel() // Clean up context
		return nil, fmt.Errorf("ffmpeg not configured")
	}

	// Setup FIFO for audio streaming
	fifoPath, pipeName, err := setupHLSFifo(secFS, hlsBaseDir, outputDir)
	if err != nil {
		// Cleanup on error
		if removeErr := secFS.RemoveAll(outputDir); removeErr != nil {
			log.Printf("Error removing output directory: %v", removeErr)
		}
		streamCancel() // Clean up context
		return nil, err
	}

	// Setup reader path based on platform
	readerPath := fifoPath
	if runtime.GOOS == "windows" {
		readerPath = pipeName // Use the Windows named pipe path
	}

	// Setup and start FFmpeg
	cmd, err := setupFFmpeg(streamCtx, sourceID, ffmpegPath, readerPath, outputDir, playlistPath)
	if err != nil {
		streamCancel() // Clean up context
		return nil, fmt.Errorf("failed to setup FFmpeg: %w", err)
	}

	// Set up Windows-specific stdin pipe handling
	if runtime.GOOS == "windows" {
		if err := setupWindowsAudioFeed(streamCtx, sourceID, cmd); err != nil {
			streamCancel() // Clean up context
			return nil, err
		}
	}

	// Setup FFmpeg logging and start the process
	if err := setupFFmpegLogging(secFS, cmd, hlsBaseDir, outputDir); err != nil {
		streamCancel() // Clean up context
		return nil, err
	}

	// Create stream info
	stream := &HLSStreamInfo{
		SourceID:     sourceID,
		FFmpegCmd:    cmd,
		OutputDir:    outputDir,
		PlaylistPath: playlistPath,
		FifoPipe:     pipeName, // Store the resolved pipe name
		ctx:          streamCtx,
		cancel:       streamCancel,
	}

	// Check for race condition
	existingStream := handleRaceCondition(sourceID, stream, secFS, outputDir)
	if existingStream != nil {
		return existingStream, nil
	}

	// Initialize the activity timestamp for the new stream
	updateStreamActivity(sourceID, "", "stream_creation")

	// Start goroutine to feed audio data to FFmpeg
	// Only for non-Windows platforms - Windows uses stdin approach
	if runtime.GOOS != "windows" {
		go feedAudioToFFmpeg(sourceID, stream.FifoPipe, stream.ctx)
	}

	// Start goroutine to handle context cancellation
	go func() {
		<-streamCtx.Done()
		cleanupStream(sourceID)
	}()

	return stream, nil
}

// getExistingStream checks if a stream already exists for the given source ID
func getExistingStream(sourceID string) *HLSStreamInfo {
	hlsStreamMutex.Lock()
	stream, exists := hlsStreams[sourceID]
	hlsStreamMutex.Unlock()

	if exists {
		if hlsVerboseLogging {
			log.Printf("🔄️ Using existing HLS stream for source: %s", sourceID)
		}
		return stream
	}
	return nil
}

// setupWindowsAudioFeed sets up audio feeding via stdin for Windows
func setupWindowsAudioFeed(ctx context.Context, sourceID string, cmd *exec.Cmd) error {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe for FFmpeg: %w", err)
	}

	// Start audio feeding in a goroutine
	go func() {
		defer stdin.Close()
		log.Printf("🎵 Starting audio feed via stdin for source %s", sourceID)

		// Set up audio callback
		audioChan, cleanup, err := setupAudioCallback(sourceID)
		if err != nil {
			log.Printf("❌ Error setting up audio callback: %v", err)
			return
		}
		defer cleanup()

		// Process audio data and write to stdin
		for {
			select {
			case <-ctx.Done():
				log.Printf("🛑 Audio feed terminated: context cancelled for source %s", sourceID)
				return
			case data, ok := <-audioChan:
				if !ok {
					log.Printf("🛑 Audio channel closed for source %s", sourceID)
					return
				}

				// Write data to FFmpeg's stdin
				if _, err := stdin.Write(data); err != nil {
					log.Printf("❌ Error writing to FFmpeg stdin: %v", err)
					return
				}
			}
		}
	}()

	return nil
}

// setupFFmpegLogging sets up the logging for FFmpeg and starts the process
func setupFFmpegLogging(secFS *securefs.SecureFS, cmd *exec.Cmd, hlsBaseDir, outputDir string) error {
	// Create a log file for ffmpeg output in the stream directory
	logFilePath := filepath.Join(outputDir, "ffmpeg.log")

	// Verify the log file path is within the HLS base directory
	isWithin, err := securefs.IsPathWithinBase(hlsBaseDir, logFilePath)
	if err != nil {
		return fmt.Errorf("failed to validate log file path: %w", err)
	}

	if !isWithin {
		return fmt.Errorf("security error: log file path would be outside HLS base directory")
	}

	// Open the log file using secureFS
	logFile, err := secFS.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("failed to create ffmpeg log file: %w", err)
	}

	// Set both stdout and stderr to the log file
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close() // Close the log file
		log.Printf("❌ Error starting FFmpeg: %v", err)
		// Use secureFS for cleanup
		if err := secFS.RemoveAll(outputDir); err != nil {
			log.Printf("Error removing output directory: %v", err)
		}
		return fmt.Errorf("failed to start FFmpeg: %w", err)
	}

	// Close the log file *after* FFmpeg exits to avoid FD leaks
	go func(f *os.File, p *exec.Cmd) {
		_ = p.Wait() // blocks until FFmpeg terminates
		_ = f.Close()
	}(logFile, cmd)

	log.Printf("✅ FFmpeg process started successfully for source: %s (logs at %s)", outputDir, logFilePath)
	return nil
}

// handleRaceCondition handles the case where another goroutine created the stream while we were working
func handleRaceCondition(sourceID string, stream *HLSStreamInfo, secFS *securefs.SecureFS, outputDir string) *HLSStreamInfo {
	hlsStreamMutex.Lock()
	existingStream, streamExists := hlsStreams[sourceID]
	if streamExists {
		// Another goroutine beat us to it, clean up our stream and use the existing one
		hlsStreamMutex.Unlock()

		log.Printf("⚠️ Another goroutine created the stream for %s while we were working, using that one", sourceID)

		// Clean up our stream resources
		if stream.cancel != nil {
			stream.cancel()
		}

		if stream.FFmpegCmd != nil && stream.FFmpegCmd.Process != nil {
			if err := stream.FFmpegCmd.Process.Kill(); err != nil {
				log.Printf("❌ Error killing duplicate FFmpeg process: %v", err)
			}
			_, _ = stream.FFmpegCmd.Process.Wait()
		}

		if err := secFS.RemoveAll(outputDir); err != nil {
			log.Printf("❌ Error removing duplicate output directory: %v", err)
		}

		return existingStream
	}

	// No race condition, store our new stream in the map
	hlsStreams[sourceID] = stream
	hlsStreamMutex.Unlock()

	return nil
}

// cleanupStream handles stream cleanup when terminated
func cleanupStream(sourceID string) {
	hlsStreamMutex.Lock()
	stream, exists := hlsStreams[sourceID]
	if !exists {
		hlsStreamMutex.Unlock()
		return
	}

	log.Printf("🧹 Cleaning up HLS stream for source: %s", sourceID)

	// Remove from map first, then release lock
	delete(hlsStreams, sourceID)
	hlsStreamMutex.Unlock()

	// Use the centralized cleanup function
	performStreamCleanup(sourceID, stream, "context cancelled")
}

// setupAudioCallback sets up the audio callback and channel
func setupAudioCallback(sourceID string) (audioChan chan []byte, cleanup func(), err error) {
	audioChan = make(chan []byte, 1024)

	// Create callback function to handle audio data
	callback := func(callbackSourceID string, data []byte) {
		if callbackSourceID == sourceID {
			select {
			case audioChan <- data:
				// Data sent successfully
			default:
				handleChannelFull(audioChan, data)
			}
		}
	}

	// Register callback
	myaudio.RegisterBroadcastCallback(sourceID, callback)

	// Create cleanup function
	cleanup = func() {
		myaudio.UnregisterBroadcastCallback(sourceID)
		log.Printf("🧹 Unregistered audio callback for source %s", sourceID)
	}

	return audioChan, cleanup, nil
}

// handleChannelFull handles the case when the audio channel is full
func handleChannelFull(audioChan chan []byte, data []byte) {
	// Channel full, clear oldest data
	select {
	case <-audioChan:
		audioChan <- data
	default:
		// Try again
		select {
		case audioChan <- data:
		default:
			// Drop data if still can't send
		}
	}
}

// processFIFOData processes audio data and writes it to the FIFO
func processFIFOData(ctx context.Context, sourceID string, fifo *os.File, audioChan chan []byte) error {
	dataWritten := false

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context canceled")
		case data, ok := <-audioChan:
			if !ok {
				return fmt.Errorf("audio channel closed")
			}

			// Write data to FIFO
			if _, err := fifo.Write(data); err != nil {
				return fmt.Errorf("error writing to FIFO: %w", err)
			}

			if !dataWritten {
				log.Printf("✅ First audio data successfully written to FIFO for source %s", sourceID)
				dataWritten = true
			}
		}
	}
}

// feedAudioToFFmpeg feeds audio data to FFmpeg via the FIFO
func feedAudioToFFmpeg(sourceID, pipePath string, ctx context.Context) {
	log.Printf("🎵 Starting audio feed for source %s to pipe %s", sourceID, pipePath)

	// Get HLS directory for path validation
	hlsBaseDir, err := conf.GetHLSDirectory()
	if err != nil {
		log.Printf("❌ Error getting HLS directory: %v", err)
		return
	}

	// Create a secure filesystem for operations
	secFS, err := securefs.New(hlsBaseDir)
	if err != nil {
		log.Printf("❌ Error creating secure filesystem: %v", err)
		return
	}
	defer secFS.Close()

	// Determine the filesystem path for callbacks
	// Derive paths from the *trusted* pipePath we already have
	fifoPath := pipePath

	// Open the pipe using the provided pipe path
	var fifo *os.File
	var openErr error

	if runtime.GOOS == "windows" {
		// On Windows, retry opening the named pipe with proper handling
		// The pipe might not be immediately available as FFmpeg needs time to connect
		var retryCount int
		const maxRetries = 10
		for retryCount < maxRetries {
			// Try to open the pipe
			fifo, openErr = secFS.OpenNamedPipe(pipePath)
			if openErr == nil {
				break // Successfully opened
			}

			// Check if cancelled before retry
			select {
			case <-ctx.Done():
				return
			default:
				// Exponential backoff with jitter (100ms, 200ms, 400ms, etc.)
				backoff := time.Duration(100*(1<<retryCount)) * time.Millisecond
				log.Printf("⏳ Retry opening named pipe for source %s (attempt %d/%d): %v",
					sourceID, retryCount+1, maxRetries, openErr)
				time.Sleep(backoff)
				retryCount++
			}
		}
	} else {
		// On Unix, we use secureFS and the original path
		fifo, openErr = secFS.OpenFile(fifoPath, os.O_WRONLY, 0)
	}

	if openErr != nil {
		log.Printf("❌ Error opening pipe: %v", openErr)
		return
	}
	defer func() {
		log.Printf("🧹 Closing pipe for source %s", sourceID)
		fifo.Close()
	}()

	// Set up audio callback
	audioChan, cleanup, err := setupAudioCallback(sourceID)
	if err != nil {
		log.Printf("❌ Error setting up audio callback: %v", err)
		return
	}
	defer cleanup()

	log.Printf("✅ Audio feed ready for source %s", sourceID)

	// Process audio data
	err = processFIFOData(ctx, sourceID, fifo, audioChan)
	if err != nil {
		log.Printf("❌ Audio processing stopped: %v for source %s", err, sourceID)
	}
}

// syncHLSClientActivity checks for inactive streams based on heartbeat timeouts
// This is the sole mechanism for cleaning up inactive streams - we rely on activity
// timestamps updated by client heartbeats and file requests
func syncHLSClientActivity() {
	// Check for inactive streams and store streams to clean up
	streamsToCleanup := []string{}

	// First, get a list of all current streams
	hlsStreamMutex.Lock()
	activeStreamIDs := make([]string, 0, len(hlsStreams))
	for sourceID := range hlsStreams {
		activeStreamIDs = append(activeStreamIDs, sourceID)
	}
	hlsStreamMutex.Unlock()

	// Check each stream's activity - we check for each known stream
	// rather than iterating hlsStreamActivity to catch any potential orphans
	for _, sourceID := range activeStreamIDs {
		if !isStreamActive(sourceID) {
			// Get duration for logging
			var inactiveDuration time.Duration
			hlsStreamActivityMutex.Lock()
			if lastActivity, exists := hlsStreamActivity[sourceID]; exists {
				inactiveDuration = time.Since(lastActivity)
				// Delete from activity map
				delete(hlsStreamActivity, sourceID)
			}
			hlsStreamActivityMutex.Unlock()

			// Count clients for logging
			clientCount := 0
			hlsStreamClientMutex.Lock()
			if clients, exists := hlsStreamClients[sourceID]; exists {
				clientCount = len(clients)
			}
			hlsStreamClientMutex.Unlock()

			// Log the cleanup
			log.Printf("⚠️ Stream %s inactive for %v (exceeds %v timeout), marking for cleanup. Client count: %d",
				sourceID, inactiveDuration, streamInactivityTimeout, clientCount)

			// Add to cleanup list
			streamsToCleanup = append(streamsToCleanup, sourceID)
		} else if hlsVerboseLogging {
			// Get last activity time for verbose logging
			var inactiveDuration time.Duration
			hlsStreamActivityMutex.Lock()
			if lastActivity, exists := hlsStreamActivity[sourceID]; exists {
				inactiveDuration = time.Since(lastActivity)
			}
			hlsStreamActivityMutex.Unlock()

			// Count clients for verbose logging
			clientCount := 0
			hlsStreamClientMutex.Lock()
			if clients, exists := hlsStreamClients[sourceID]; exists {
				clientCount = len(clients)
			}
			hlsStreamClientMutex.Unlock()

			// Log active stream status
			log.Printf("📊 Stream %s last activity: %0.0f seconds ago, active clients: %d",
				sourceID, inactiveDuration.Seconds(), clientCount)
		}
	}

	// Clean up the inactive streams
	for _, sourceID := range streamsToCleanup {
		cleanupInactiveStream(sourceID)
	}

	// Debug logging
	if hlsVerboseLogging {
		logActiveHLSStreams()
	}
}

// logActiveHLSStreams logs information about all active streams
func logActiveHLSStreams() {
	hlsStreamMutex.Lock()
	defer hlsStreamMutex.Unlock()

	hlsStreamClientMutex.Lock()
	defer hlsStreamClientMutex.Unlock()

	for sourceID := range hlsStreams {
		// Count clients for this stream
		clientCount := 0
		if clients, exists := hlsStreamClients[sourceID]; exists {
			clientCount = len(clients)
		}

		// Get activity info for this stream
		active := isStreamActive(sourceID)
		status := "ACTIVE"
		if !active {
			status = "INACTIVE"
		}

		// Get time since last activity
		var timeSinceActivity float64
		hlsStreamActivityMutex.Lock()
		if timestamp, exists := hlsStreamActivity[sourceID]; exists {
			timeSinceActivity = time.Since(timestamp).Seconds()
		}
		hlsStreamActivityMutex.Unlock()

		log.Printf("📊 Active Live Stream: %s | Status: %s | Clients: %d | Last Activity: %0.0f seconds ago",
			sourceID, status, clientCount, timeSinceActivity)
	}
}

// cleanupInactiveStream stops and cleans up an inactive stream
func cleanupInactiveStream(sourceID string) {
	hlsStreamMutex.Lock()

	stream, exists := hlsStreams[sourceID]
	if !exists {
		hlsStreamMutex.Unlock()
		return
	}

	// Check if this is a new stream that just started
	// Get activity timestamp from the activity map
	var streamAge time.Duration
	hlsStreamActivityMutex.Lock()
	if activityTime, hasActivity := hlsStreamActivity[sourceID]; hasActivity {
		streamAge = time.Since(activityTime)
	}
	hlsStreamActivityMutex.Unlock()

	if streamAge < 30*time.Second {
		// Don't clean up streams that are less than 30 seconds old
		// This gives FFmpeg time to initialize and generate the playlist
		log.Printf("⏳ Skipping cleanup of new HLS stream for source %s (age: %v)", sourceID, streamAge)
		hlsStreamMutex.Unlock()
		return
	}

	// Remove from map before cleanup to prevent new connections
	delete(hlsStreams, sourceID)
	hlsStreamMutex.Unlock()

	// Use the centralized cleanup function
	performStreamCleanup(sourceID, stream, fmt.Sprintf("inactive for %v", streamAge))
}

// cleanupExistingStream handles cleaning up an existing stream for a source
// Returns true if a stream was cleaned up
func (h *Handlers) cleanupExistingStream(sourceID string) bool {
	// Check if stream exists and get necessary info for cleanup
	hlsStreamMutex.Lock()
	stream, exists := hlsStreams[sourceID]
	if !exists {
		hlsStreamMutex.Unlock()
		return false
	}

	log.Printf("🧹 Found existing stream for source %s, ensuring cleanup before restart", sourceID)

	// Cancel the context, which will terminate the FFmpeg process
	if stream.cancel != nil {
		stream.cancel()
	}

	// Get process handle and release lock before waiting
	var cmd *exec.Cmd
	if stream.FFmpegCmd != nil && stream.FFmpegCmd.Process != nil {
		cmd = stream.FFmpegCmd
	}

	// Get output directory for cleanup
	outputDir := stream.OutputDir

	// Remove from map
	delete(hlsStreams, sourceID)
	hlsStreamMutex.Unlock()

	// Wait for process termination without holding the lock
	if cmd != nil && cmd.Process != nil {
		_, _ = cmd.Process.Wait()
	}

	// Clean up the output directory
	if outputDir != "" {
		// Get HLS directory for secure filesystem operations
		hlsBaseDir, err := conf.GetHLSDirectory()
		if err != nil {
			log.Printf("Error getting HLS directory: %v", err)
		} else {
			// Use secureFS to remove the directory
			secFS, err := securefs.New(hlsBaseDir)
			if err != nil {
				log.Printf("Error creating secure filesystem: %v", err)
			} else {
				defer secFS.Close()

				if secFS.ExistsNoErr(outputDir) {
					log.Printf("🧹 Removing stream directory: %s", outputDir)
					if err := secFS.RemoveAll(outputDir); err != nil {
						log.Printf("❌ Error removing stream directory: %v", err)
					}
				}
			}
		}
	}

	return true
}

// StartHLSStream explicitly starts an HLS stream for a source
// This is called when a client wants to start playing a stream
func (h *Handlers) StartHLSStream(c echo.Context, sourceID string) (*StreamStatus, error) {
	clientIP := c.RealIP()
	clientID := generateClientID(clientIP, c.Request().Header.Get("User-Agent"))

	log.Printf("🎬 Client %s requested to start HLS stream for source: %s", clientID, sourceID)

	// Check if source exists
	if !myaudio.HasCaptureBuffer(sourceID) {
		return nil, echo.NewHTTPError(http.StatusNotFound, "Audio source not found")
	}

	// Ensure any existing stream is cleaned up
	h.cleanupExistingStream(sourceID)

	// Add client to stream tracking with a longer initial timeout
	// to give FFmpeg time to start up and generate the playlist
	updateStreamActivity(sourceID, clientID, "stream_start", 30*time.Second)

	// Log the client count
	hlsStreamClientMutex.Lock()
	activeClients := 0
	if clients, exists := hlsStreamClients[sourceID]; exists {
		activeClients = len(clients)
	}
	hlsStreamClientMutex.Unlock()

	log.Printf("📊 HLS stream for source %s now has %d active clients", sourceID, activeClients)

	// Start the FFmpeg process if it's not already running
	stream, err := getOrCreateHLSStream(context.Background(), sourceID)
	if err != nil {
		log.Printf("❌ Error creating HLS stream: %v", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to start stream: %v", err))
	}

	// Get HLS directory for secure path checks
	hlsBaseDir, err := conf.GetHLSDirectory()
	if err != nil {
		log.Printf("❌ Error getting HLS directory: %v", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError, "Server configuration error")
	}

	// Check if playlist is ready
	playlistReady := h.checkPlaylistReady(c, sourceID, stream, hlsBaseDir)

	// Return stream status information
	status := "starting"
	if playlistReady {
		status = "ready"
		log.Printf("✅ Playlist file is ready: %s", stream.PlaylistPath)
	} else {
		log.Printf("⚠️ Playlist file not immediately available: %s", stream.PlaylistPath)
	}

	return &StreamStatus{
		Status:        status,
		Source:        sourceID,
		PlaylistPath:  stream.PlaylistPath,
		ActiveClients: activeClients,
		PlaylistReady: playlistReady,
	}, nil
}

// checkPlaylistReady checks if the playlist file exists and is valid
func (h *Handlers) checkPlaylistReady(c echo.Context, sourceID string, stream *HLSStreamInfo, hlsBaseDir string) bool {
	// Create a secure filesystem for checking playlist
	secFS, err := securefs.New(hlsBaseDir)
	if err != nil {
		log.Printf("❌ Error creating secure filesystem: %v", err)
		return false
	}
	defer secFS.Close()

	// Check if the playlist file exists, waiting a reasonable time if needed
	// Use a cancellable context to ensure we don't wait forever
	playlistCtx, cancelPlaylist := context.WithTimeout(c.Request().Context(), 20*time.Second)
	defer cancelPlaylist()

	playlistReady := false
	playlistCheckerDone := make(chan bool, 1)

	// Start a goroutine to check for the playlist file
	go func() {
		defer func() {
			playlistCheckerDone <- true
		}()

		retryCount := 0
		for retryCount < 30 { // Allow up to 30 seconds with 1 second intervals
			select {
			case <-playlistCtx.Done():
				log.Printf("⚠️ Playlist check cancelled or timed out for source: %s", sourceID)
				return
			default:
				// Check if playlist exists
				if secFS.ExistsNoErr(stream.PlaylistPath) {
					// Check if it's a valid playlist with some content
					data, err := secFS.ReadFile(stream.PlaylistPath)
					if err == nil && len(data) > 0 && strings.Contains(string(data), "#EXTM3U") {
						playlistReady = true
						log.Printf("✅ Playlist file is ready (attempt %d): %s", retryCount+1, stream.PlaylistPath)
						return
					}
				}

				// Check if stream is still active - don't wait if it's been terminated
				hlsStreamMutex.Lock()
				_, streamExists := hlsStreams[sourceID]
				hlsStreamMutex.Unlock()

				if !streamExists {
					log.Printf("❌ Stream was terminated while waiting for playlist: %s", sourceID)
					return
				}

				log.Printf("⏳ Waiting for playlist file (attempt %d): %s", retryCount+1, stream.PlaylistPath)
				retryCount++
				time.Sleep(1000 * time.Millisecond)
			}
		}

		log.Printf("⚠️ Playlist file not created after waiting: %s", stream.PlaylistPath)
	}()

	// Wait for the playlist checker to complete
	<-playlistCheckerDone

	return playlistReady
}

// StopHLSClientStream registers that a client has stopped streaming
// When the last client disconnects, we'll stop the FFmpeg process
func (h *Handlers) StopHLSClientStream(c echo.Context, sourceID string) error {
	clientIP := c.RealIP()
	clientID := generateClientID(clientIP, c.Request().Header.Get("User-Agent"))

	// Remove client from tracking
	hlsStreamClientMutex.Lock()
	lastClient := false
	if clients, exists := hlsStreamClients[sourceID]; exists {
		// Remove the client
		delete(clients, clientID)

		// Check if this was the last client
		lastClient = len(clients) == 0

		// If no clients left, remove the source entry
		if lastClient {
			delete(hlsStreamClients, sourceID)
		}

		// Log the disconnection
		remainingClients := len(clients)
		log.Printf("👋 Client %s disconnected from HLS stream for source: %s", clientID, sourceID)
		if !lastClient {
			log.Printf("📊 HLS stream for source %s still has %d active clients", sourceID, remainingClients)
		}
	}
	hlsStreamClientMutex.Unlock()

	// If this was the last client, stop the stream immediately
	if lastClient {
		hlsStreamMutex.Lock()
		stream, exists := hlsStreams[sourceID]
		if exists {
			log.Printf("🧹 Last client disconnected, stopping FFmpeg for source: %s", sourceID)

			// Remove from map immediately to prevent new clients
			delete(hlsStreams, sourceID)
			hlsStreamMutex.Unlock()

			// Use consolidated cleanup function
			performStreamCleanup(sourceID, stream, "last client disconnected")
		} else {
			hlsStreamMutex.Unlock()
		}
	}

	// Also remove from activity tracking
	hlsStreamActivityMutex.Lock()
	delete(hlsStreamActivity, sourceID)
	hlsStreamActivityMutex.Unlock()

	return nil
}

// CleanupAllStreams removes all HLS streams and their files
func CleanupAllStreams() error {
	hlsStreamMutex.Lock()

	// Make a copy of all streams to clean up
	streamsToClean := make(map[string]*HLSStreamInfo)
	for sourceID, stream := range hlsStreams {
		streamsToClean[sourceID] = stream
		// Also remove from streams map
		delete(hlsStreams, sourceID)
	}
	hlsStreamMutex.Unlock()

	// Clean up each stream individually
	for sourceID, stream := range streamsToClean {
		performStreamCleanup(sourceID, stream, "server shutdown")
	}

	// Clean up any remaining stream directories
	hlsBaseDir, err := conf.GetHLSDirectory()
	if err != nil {
		return fmt.Errorf("failed to get HLS directory: %w", err)
	}

	// Create a secure filesystem for cleanup
	secFS, err := securefs.New(hlsBaseDir)
	if err != nil {
		return fmt.Errorf("failed to create secure filesystem: %w", err)
	}
	defer secFS.Close()

	// Read all entries in the HLS directory using SecureFS
	entries, err := secFS.ReadDir(hlsBaseDir)
	if err != nil {
		return fmt.Errorf("failed to read HLS directory: %w", err)
	}

	// Remove all stream directories
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "stream_") {
			streamDir := filepath.Join(hlsBaseDir, entry.Name())
			log.Printf("🧹 Removing HLS stream directory: %s", streamDir)

			if err := secFS.RemoveAll(streamDir); err != nil {
				log.Printf("❌ Error removing stream directory %s: %v", streamDir, err)
				// Continue with other directories
			}
		}
	}

	// Cleanup named pipes if running on Windows
	if runtime.GOOS == "windows" {
		securefs.CleanupNamedPipes()
	}

	return nil
}

// generateClientID creates a standardized client ID from IP and user agent
// This ensures we consistently identify the same client across different parts of the code
func generateClientID(clientIP, userAgent string) string {
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

// ProcessHLSHeartbeat handles client heartbeat messages for HLS streams
// Returns success flag instead of writing directly to avoid duplicate responses
func (h *Handlers) ProcessHLSHeartbeat(c echo.Context) error {
	clientIP := c.RealIP()
	clientID := generateClientID(clientIP, c.Request().Header.Get("User-Agent"))

	// Decode the heartbeat message
	heartbeat := HLSHeartbeat{}
	if err := c.Bind(&heartbeat); err != nil {
		log.Printf("❌ Invalid heartbeat format from %s: %v", clientID, err)
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid heartbeat message")
	}

	// Handle disconnection announcements
	if disconnectFlag := c.QueryParam("disconnect"); disconnectFlag == "true" ||
		c.QueryParam("status") == "disconnect" {

		// Check if this client was recently connected to the stream (within 10 seconds)
		// This helps prevent false disconnects during stream initialization
		isRecentConnection := false
		hlsStreamActivityMutex.Lock()
		if lastTime, exists := hlsStreamClientLastActivity[heartbeat.SourceID+":"+clientID]; exists {
			if time.Since(lastTime) < 10*time.Second {
				isRecentConnection = true
				log.Printf("⚠️ Ignoring premature disconnect from client %s for stream %s (too soon after connect)",
					clientID, heartbeat.SourceID)
			}
		}
		hlsStreamActivityMutex.Unlock()

		// If this is a false disconnect during initialization, just update activity and don't disconnect
		if isRecentConnection {
			// Just update activity without disconnecting
			updateStreamActivity(heartbeat.SourceID, clientID, "continued-connection")
			return nil
		}

		log.Printf("👋 Client %s announced disconnection from stream %s",
			clientID, heartbeat.SourceID)

		// Remove client from tracking
		hlsStreamClientMutex.Lock()
		if clients, exists := hlsStreamClients[heartbeat.SourceID]; exists {
			// Remove the client
			delete(clients, clientID)

			// Log remaining clients
			remainingClients := len(clients)
			log.Printf("👋 Client %s disconnected from HLS stream %s (%d clients remaining)",
				clientID, heartbeat.SourceID, remainingClients)
		}
		hlsStreamClientMutex.Unlock()

		// Don't write the response - let the route handler do it
		return nil
	}

	// Validate that the stream exists
	hlsStreamMutex.Lock()
	_, streamExists := hlsStreams[heartbeat.SourceID]
	hlsStreamMutex.Unlock()

	if !streamExists {
		// Don't update activity for non-existent streams
		if hlsVerboseLogging {
			log.Printf("⚠️ Ignoring heartbeat from %s for non-existent stream %s",
				clientID, heartbeat.SourceID)
		}
		// Don't write the response - let the route handler do it
		return nil
	}

	// Store previous activity time for logging
	var previousActivity time.Time
	hlsStreamActivityMutex.Lock()
	if prevTime, exists := hlsStreamActivity[heartbeat.SourceID]; exists {
		previousActivity = prevTime
	}

	// Record the time of this client's activity for potential false disconnect detection
	if hlsStreamClientLastActivity == nil {
		hlsStreamClientLastActivity = make(map[string]time.Time)
	}
	hlsStreamClientLastActivity[heartbeat.SourceID+":"+clientID] = time.Now()
	hlsStreamActivityMutex.Unlock()

	// Update activity
	updateStreamActivity(heartbeat.SourceID, clientID, "heartbeat")

	// Log additional information about heartbeat if verbose logging is enabled
	if hlsVerboseLogging && !previousActivity.IsZero() {
		timeSinceLastActivity := time.Since(previousActivity)
		log.Printf("⏱️ Heartbeat extended stream lifetime by %0.0f seconds for %s",
			timeSinceLastActivity.Seconds(), heartbeat.SourceID)
	}

	// Don't write the response - let the route handler do it
	return nil
}

// Initialize HLS streaming service
func init() {
	// Set verbose logging to false by default
	hlsVerboseLogging = false

	// Start client activity sync - this is the real-time monitoring system that tracks
	// stream activity based on client heartbeats and file requests.
	// Note: This is separate from the longer-running cleanup task that is initialized
	// in Server.initHLSCleanupTask() which handles more comprehensive resource cleanup
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			syncHLSClientActivity()
		}
	}()

	// Create HLS directory if needed
	hlsDir, err := conf.GetHLSDirectory()
	if err != nil {
		log.Printf("⚠️ Warning: Failed to create HLS directory: %v", err)
	} else {
		log.Printf("✅ HLS streaming directory initialized at: %s", hlsDir)
	}
}

// performStreamCleanup provides a centralized cleanup function for HLS streams
// to reduce code duplication across different cleanup scenarios
func performStreamCleanup(sourceID string, stream *HLSStreamInfo, reason string) {
	log.Printf("🧹 Cleaning up HLS stream for source %s: %s", sourceID, reason)

	// Cancel the context to terminate the FFmpeg process
	if stream.cancel != nil {
		stream.cancel()
	}

	// Get FFmpeg command for waiting without blocking other operations
	var cmd *exec.Cmd
	if stream.FFmpegCmd != nil && stream.FFmpegCmd.Process != nil {
		cmd = stream.FFmpegCmd
	}

	// Wait for process termination without blocking other operations
	if cmd != nil && cmd.Process != nil {
		go func() {
			_, _ = cmd.Process.Wait()
			log.Printf("🛑 FFmpeg process for source %s has terminated", sourceID)
		}()
	}

	// Clean up the output directory
	if stream.OutputDir != "" {
		// Get HLS directory for secure filesystem operations
		hlsBaseDir, err := conf.GetHLSDirectory()
		if err != nil {
			log.Printf("Error getting HLS directory: %v", err)
		} else {
			// Use secureFS to remove the directory
			secFS, err := securefs.New(hlsBaseDir)
			if err != nil {
				log.Printf("Error creating secure filesystem: %v", err)
			} else {
				defer secFS.Close()

				if secFS.ExistsNoErr(stream.OutputDir) {
					log.Printf("🧹 Cleaning up stream directory: %s", stream.OutputDir)
					if err := secFS.RemoveAll(stream.OutputDir); err != nil {
						log.Printf("❌ Error removing stream directory: %v", err)
					}
				}
			}
		}
	}

	// Clean up client tracking
	hlsStreamClientMutex.Lock()
	delete(hlsStreamClients, sourceID)
	hlsStreamClientMutex.Unlock()

	// Clean up activity tracking
	hlsStreamActivityMutex.Lock()
	delete(hlsStreamActivity, sourceID)

	// Clean up client last activity records
	for key := range hlsStreamClientLastActivity {
		if strings.HasPrefix(key, sourceID+":") {
			delete(hlsStreamClientLastActivity, key)
		}
	}
	hlsStreamActivityMutex.Unlock()

	log.Printf("✅ HLS stream for source %s fully cleaned up", sourceID)
}

// updateStreamActivity records any activity for a stream and its clients
// This is the ONLY place where stream activity should be updated
func updateStreamActivity(sourceID, clientID, activityType string, gracePeriod ...time.Duration) {
	// Track the client if provided
	if clientID != "" {
		hlsStreamClientMutex.Lock()
		if _, exists := hlsStreamClients[sourceID]; !exists {
			hlsStreamClients[sourceID] = make(map[string]bool)
		}
		hlsStreamClients[sourceID][clientID] = true
		clientCount := len(hlsStreamClients[sourceID])
		hlsStreamClientMutex.Unlock()

		if hlsVerboseLogging {
			log.Printf("📊 Client %s activity (%s) recorded for stream %s (total clients: %d)",
				clientID, activityType, sourceID, clientCount)
		}
	}

	// Always update the activity timestamp
	hlsStreamActivityMutex.Lock()

	// Apply grace period if provided
	extraTime := time.Duration(0)
	if len(gracePeriod) > 0 {
		extraTime = gracePeriod[0]
		if hlsVerboseLogging && extraTime > 0 {
			log.Printf("⏱️ Adding grace period of %v to stream %s activity timestamp", extraTime, sourceID)
		}
	}

	hlsStreamActivity[sourceID] = time.Now().Add(extraTime)
	hlsStreamActivityMutex.Unlock()
}

// isStreamActive checks if a stream has had activity within the timeout period
func isStreamActive(sourceID string) bool {
	hlsStreamActivityMutex.Lock()
	defer hlsStreamActivityMutex.Unlock()

	lastActivity, exists := hlsStreamActivity[sourceID]
	if !exists {
		if hlsVerboseLogging {
			log.Printf("📊 Stream %s has no activity record", sourceID)
		}
		return false
	}

	timeSinceActivity := time.Since(lastActivity)
	isActive := timeSinceActivity <= streamInactivityTimeout

	if hlsVerboseLogging && !isActive {
		log.Printf("⚠️ Stream %s inactive: %0.0f seconds > %0.0f seconds timeout",
			sourceID, timeSinceActivity.Seconds(), streamInactivityTimeout.Seconds())
	}

	return isActive
}

// CleanupIdleHLSStreams performs a forced cleanup of all idle HLS streams
// This function can be called periodically from a background task to ensure
// resources are freed even when clients disconnect abnormally
func (h *Handlers) CleanupIdleHLSStreams() {
	log.Printf("🧹 Running HLS stream cleanup task")

	// Get a list of all current streams
	hlsStreamMutex.Lock()
	activeStreamIDs := make([]string, 0, len(hlsStreams))
	for sourceID := range hlsStreams {
		activeStreamIDs = append(activeStreamIDs, sourceID)
	}
	hlsStreamMutex.Unlock()

	// Track cleanup stats
	cleanupCount := 0
	maxStreamAge := time.Duration(0)
	var oldestStreamID string

	// Check each stream for idle time and other criteria
	for _, sourceID := range activeStreamIDs {
		// Get last activity time
		var lastActivity time.Time
		var inactiveDuration time.Duration
		hlsStreamActivityMutex.Lock()
		if activity, exists := hlsStreamActivity[sourceID]; exists {
			lastActivity = activity
			inactiveDuration = time.Since(lastActivity)
		}
		hlsStreamActivityMutex.Unlock()

		// Track oldest stream for monitoring
		if inactiveDuration > maxStreamAge {
			maxStreamAge = inactiveDuration
			oldestStreamID = sourceID
		}

		// Check client count
		clientCount := 0
		hlsStreamClientMutex.Lock()
		if clients, exists := hlsStreamClients[sourceID]; exists {
			clientCount = len(clients)
		}
		hlsStreamClientMutex.Unlock()

		// Determine if stream should be cleaned up based on multiple criteria:
		// 1. No activity within the inactivity timeout
		// 2. Zero clients and inactive for at least 30 seconds
		// 3. Stream has been active for more than the max lifetime
		shouldCleanup := false
		cleanupReason := ""

		switch {
		case inactiveDuration > streamInactivityTimeout:
			shouldCleanup = true
			cleanupReason = fmt.Sprintf("inactive for %v (exceeds %v timeout)",
				inactiveDuration, streamInactivityTimeout)
		case clientCount == 0 && inactiveDuration > 30*time.Second:
			shouldCleanup = true
			cleanupReason = fmt.Sprintf("no clients for %v", inactiveDuration)
		case inactiveDuration > maxStreamLifetime:
			shouldCleanup = true
			cleanupReason = fmt.Sprintf("reached maximum lifetime of %v", maxStreamLifetime)
		}

		if shouldCleanup {
			log.Printf("🧹 Cleaning up stream %s: %s", sourceID, cleanupReason)

			// Get stream for cleanup
			hlsStreamMutex.Lock()
			stream, exists := hlsStreams[sourceID]
			if exists {
				// Remove from map before cleanup
				delete(hlsStreams, sourceID)
				hlsStreamMutex.Unlock()

				// Perform actual cleanup
				performStreamCleanup(sourceID, stream, cleanupReason)
				cleanupCount++
			} else {
				hlsStreamMutex.Unlock()
			}
		}
	}

	// Log summary
	streamCount := len(activeStreamIDs)
	if streamCount > 0 {
		log.Printf("✅ HLS cleanup task completed: %d/%d streams cleaned up",
			cleanupCount, streamCount)

		if oldestStreamID != "" && maxStreamAge > 0 {
			log.Printf("📊 Oldest active stream: %s (inactive for %v)",
				oldestStreamID, maxStreamAge)
		}
	} else {
		log.Printf("✅ HLS cleanup task completed: no active streams")
	}
}
