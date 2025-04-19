package handlers

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// HLSStreamInfo contains information about a streaming session
type HLSStreamInfo struct {
	SourceID     string
	FFmpegCmd    *exec.Cmd
	OutputDir    string
	PlaylistPath string
	LastAccess   time.Time
	// Add context and cancellation for managing stream lifecycle
	ctx    context.Context
	cancel context.CancelFunc
}

var (
	// Manage active HLS streams
	hlsStreams     = make(map[string]*HLSStreamInfo)
	hlsStreamMutex sync.Mutex

	// Clean up inactive streams every 5 minutes
	cleanupInterval = 5 * time.Minute

	// Consider a stream inactive after 2 minutes without access
	inactivityTimeout = 2 * time.Minute

	// Track active clients per stream
	hlsStreamClients     = make(map[string]map[string]bool) // sourceID -> clientID -> true
	hlsStreamClientMutex sync.Mutex

	// Track client activity with timestamps
	hlsClientActivity      = make(map[string]map[string]time.Time) // sourceID -> clientID -> lastActiveTime
	hlsClientActivityMutex sync.Mutex

	// Consider a client inactive after 30 seconds of no segment requests (increased from 10)
	clientInactivityTimeout = 30 * time.Second

	// Control logging verbosity
	hlsVerboseLogging = false

	// Store the last log time per client+source to reduce log spam
	hlsClientLogTime      = make(map[string]time.Time)
	hlsClientLogTimeMutex sync.Mutex

	// Log cooldown - only log once per client per this duration
	hlsLogCooldown = 60 * time.Second
)

// StreamStatus represents the current status of an HLS stream
type StreamStatus struct {
	Status        string `json:"status"`
	Source        string `json:"source"`
	PlaylistPath  string `json:"playlist_path,omitempty"`
	ActiveClients int    `json:"active_clients"`
	PlaylistReady bool   `json:"playlist_ready"`
}

// isPathWithinBase checks if targetPath is within or equal to basePath
func isPathWithinBase(basePath, targetPath string) (bool, error) {
	// Resolve both paths to absolute, clean paths
	absBase, err := filepath.Abs(basePath)
	if err != nil {
		return false, fmt.Errorf("failed to resolve base path: %w", err)
	}

	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return false, fmt.Errorf("failed to resolve target path: %w", err)
	}

	// Resolve symlinks in base path (which should always exist)
	absBase, err = filepath.EvalSymlinks(absBase)
	if err != nil {
		return false, fmt.Errorf("failed to eval base symlinks: %w", err)
	}

	// Try to resolve all symlinks in the target path, including intermediate components
	absTargetResolved, err := filepath.EvalSymlinks(absTarget)
	if err == nil {
		// If successful, use the fully resolved path
		absTarget = absTargetResolved
	} else {
		// If the target doesn't exist or there's another issue, we should still
		// handle the case where intermediate components might be symlinks
		// This is a fallback that at least checks what we can
		dir := filepath.Dir(absTarget)
		for dir != "/" && dir != "." {
			// Try to resolve symlinks in parent directories
			resolvedDir, err := filepath.EvalSymlinks(dir)
			if err == nil && resolvedDir != dir {
				// Found a parent directory that's a symlink
				// Reconstruct the target with the resolved parent
				base := filepath.Base(absTarget)
				absTarget = filepath.Join(resolvedDir, base)
				break
			}
			dir = filepath.Dir(dir)
		}
	}

	// Ensure paths are cleaned to remove any ".." components
	absBase = filepath.Clean(absBase)
	absTarget = filepath.Clean(absTarget)

	// Check if target path starts with base path
	return strings.HasPrefix(absTarget, absBase+string(filepath.Separator)) || absTarget == absBase, nil
}

// Initialize HLS streaming service
func init() {
	// Start cleanup goroutine
	go func() {
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()

		for range ticker.C {
			cleanupInactiveStreams()
		}
	}()

	// Start client activity sync
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
		log.Printf("Warning: Failed to create HLS directory: %v", err)
	} else {
		log.Printf("HLS streaming directory initialized at: %s", hlsDir)
	}
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

	// Register client activity at the start of the request
	registerClientActivity(sourceID, clientID)

	// Get or create HLS stream
	stream, err := getOrCreateHLSStream(ctx, sourceID)
	if err != nil {
		log.Printf("Error creating HLS stream: %v - source: %s", err, sourceID)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to create audio stream")
	}

	// Update access time
	hlsStreamMutex.Lock()
	stream.LastAccess = time.Now()
	hlsStreamMutex.Unlock()

	// Determine what file is being requested
	rawPath := c.Param("*")
	// Decode %XX sequences first, then continue with validation
	requestPath, err := url.PathUnescape(rawPath)
	if err != nil {
		log.Printf("Invalid URL encoding in path: %s", rawPath)
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid URL encoding")
	}

	// Record client activity when they request a segment
	// This gives us a more accurate view of active clients
	if strings.HasSuffix(requestPath, ".ts") {
		registerClientActivity(sourceID, clientID)
	}

	// Monitor context cancellation for client disconnection
	go h.monitorClientDisconnection(ctx, sourceID, clientID)

	// Log client connection if needed
	h.logClientConnection(sourceID, c.RealIP(), requestPath)

	// Add CORS headers to allow cross-origin requests
	h.addCorsHeaders(c)

	// Serve the appropriate file based on request
	if requestPath == "" || requestPath == "playlist.m3u8" {
		return h.servePlaylistFile(c, stream, hlsBaseDir)
	}

	return h.serveSegmentFile(c, stream, requestPath, hlsBaseDir)
}

// validateHLSRequest validates the request parameters and permissions
func (h *Handlers) validateHLSRequest(c echo.Context) (sourceID, clientID, hlsBaseDir string, err error) {
	sourceID = c.Param("sourceID")
	clientIP := c.RealIP()
	clientID = clientIP + "-" + c.Request().Header.Get("User-Agent")

	if sourceID == "" {
		return "", "", "", echo.NewHTTPError(http.StatusBadRequest, "Source ID is required")
	}

	// Validate sourceID for security - ensure it only contains safe characters
	safeSourceIDRegex := regexp.MustCompile(`^[A-Za-z0-9_\-]+$`)
	if !safeSourceIDRegex.MatchString(sourceID) {
		log.Printf("Security warning: Invalid source ID format detected: %s", sourceID)
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
				log.Printf("Authentication failed for HLS stream - source: %s, IP: %s", sourceID, clientIP)
				return "", "", "", echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
			}
		}
	}

	// Check if source exists and has a valid capture buffer
	if !myaudio.HasCaptureBuffer(sourceID) {
		log.Printf("Audio source not found for HLS stream - source: %s, IP: %s", sourceID, clientIP)
		return "", "", "", echo.NewHTTPError(http.StatusNotFound, "Audio source not found")
	}

	// Get HLS base directory for security validation
	var baseDir string
	baseDir, err = conf.GetHLSDirectory()
	if err != nil {
		log.Printf("Error getting HLS directory: %v", err)
		return "", "", "", echo.NewHTTPError(http.StatusInternalServerError, "Server configuration error")
	}

	return sourceID, clientID, baseDir, nil
}

// monitorClientDisconnection watches for client disconnection
func (h *Handlers) monitorClientDisconnection(ctx context.Context, sourceID, clientID string) {
	<-ctx.Done()
	// Request completed or canceled, update last activity
	updateClientDisconnection(sourceID, clientID)
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
	// Sanitize the path
	cleanPath := filepath.Clean("/playlist.m3u8")
	if strings.Contains(cleanPath, "..") || cleanPath == "/" {
		log.Printf("Warning: Suspicious playlist path requested")
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid playlist path")
	}

	// Create a secure filesystem for operations
	secFS, err := newSecureFS(hlsBaseDir)
	if err != nil {
		log.Printf("Error creating secure filesystem: %v", err)
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
	if !secFS.Exists(stream.PlaylistPath) {
		// If the playlist doesn't exist yet, check if FFmpeg is still running
		hlsStreamMutex.Lock()
		_, streamExists := hlsStreams[stream.SourceID]
		hlsStreamMutex.Unlock()

		if !streamExists {
			log.Printf("Error: HLS stream no longer exists for source %s", stream.SourceID)
			return echo.NewHTTPError(http.StatusNotFound, "Stream no longer exists")
		}

		// Send a temporary empty playlist to avoid client errors
		// This will cause the client to retry after a short delay
		log.Printf("Sending temporary empty playlist for source %s (real playlist not ready yet)", stream.SourceID)

		// Create a basic empty HLS playlist
		emptyPlaylist := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:0
#EXT-X-PLAYLIST-TYPE:EVENT
#EXT-X-ENDLIST
`
		return c.String(http.StatusOK, emptyPlaylist)
	}

	// Serve the playlist file securely
	return secFS.ServeFile(c, stream.PlaylistPath)
}

// serveSegmentFile serves the HLS segment file
func (h *Handlers) serveSegmentFile(c echo.Context, stream *HLSStreamInfo, requestPath, hlsBaseDir string) error {
	// Validate segment path for path traversal prevention
	cleanPath := filepath.Clean("/" + requestPath)
	if strings.Contains(cleanPath, "..") || cleanPath == "/" {
		log.Printf("Warning: Suspicious segment path requested: %s", requestPath)
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid segment path")
	}

	// Remove leading slash for concatenation
	safeRequestPath := cleanPath[1:]

	// Build the full segment path
	segmentPath := filepath.Join(stream.OutputDir, safeRequestPath)

	// Create a secure filesystem for operations
	secFS, err := newSecureFS(hlsBaseDir)
	if err != nil {
		log.Printf("Error creating secure filesystem: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Server error")
	}
	defer secFS.Close()

	// Check if segment file exists using secureFS
	if !secFS.Exists(segmentPath) {
		log.Printf("Error: HLS segment file does not exist at %s", segmentPath)
		return echo.NewHTTPError(http.StatusNotFound, "Segment file not found")
	}

	// For .ts segment files
	if strings.HasSuffix(safeRequestPath, ".ts") {
		c.Response().Header().Set("Content-Type", "video/mp2t")
		// Allow caching of segments for a short time
		c.Response().Header().Set("Cache-Control", "public, max-age=60")
	}

	// Serve the segment file securely
	return secFS.ServeFile(c, segmentPath)
}

// registerClientActivity records client activity for a source
func registerClientActivity(sourceID, clientID string) {
	// Consistent lock order: first stream clients, then client activity
	hlsStreamClientMutex.Lock()
	if _, exists := hlsStreamClients[sourceID]; !exists {
		hlsStreamClients[sourceID] = make(map[string]bool)
	}
	hlsStreamClients[sourceID][clientID] = true
	hlsStreamClientMutex.Unlock()

	// Use a separate lock scope for client activity
	hlsClientActivityMutex.Lock()
	if _, exists := hlsClientActivity[sourceID]; !exists {
		hlsClientActivity[sourceID] = make(map[string]time.Time)
	}
	hlsClientActivity[sourceID][clientID] = time.Now()
	hlsClientActivityMutex.Unlock()
}

// updateClientDisconnection handles client disconnection events
func updateClientDisconnection(sourceID, clientID string) {
	// Just mark the last activity time, let the regular cleanup handle the rest
	// This avoids immediate cleanup which could interrupt other active requests
	registerClientActivity(sourceID, clientID)
}

// getOrCreateHLSStream gets an existing stream or creates a new one
func getOrCreateHLSStream(ctx context.Context, sourceID string) (*HLSStreamInfo, error) {
	// Validate sourceID for security - ensure it only contains safe characters
	// First apply strict validation to prevent any potential path manipulation
	safeSourceIDRegex := regexp.MustCompile(`^[A-Za-z0-9_\-]+$`)
	if !safeSourceIDRegex.MatchString(sourceID) {
		return nil, fmt.Errorf("invalid source ID format: contains unauthorized characters")
	}

	hlsStreamMutex.Lock()
	defer hlsStreamMutex.Unlock()

	// Check if stream already exists
	if stream, exists := hlsStreams[sourceID]; exists {
		if hlsVerboseLogging {
			log.Printf("Using existing HLS stream for source: %s", sourceID)
		}
		return stream, nil
	}

	log.Printf("Creating new HLS stream for source: %s", sourceID)

	// Create a context that can be canceled to terminate the stream
	streamCtx, streamCancel := context.WithCancel(ctx)

	// Get HLS directory
	hlsBaseDir, err := conf.GetHLSDirectory()
	if err != nil {
		streamCancel() // Clean up context
		return nil, fmt.Errorf("failed to get HLS directory: %w", err)
	}

	// Create a secureFS instance for filesystem operations
	secFS, err := newSecureFS(hlsBaseDir)
	if err != nil {
		streamCancel() // Clean up context
		return nil, fmt.Errorf("failed to initialize secure filesystem: %w", err)
	}
	defer secFS.Close()

	// Apply strict sanitization to prevent directory traversal and other issues
	// Even though we already validated sourceID, apply another layer of sanitization
	// for defense in depth
	reSafe := regexp.MustCompile(`[^A-Za-z0-9_\-]`)
	safeSourceID := reSafe.ReplaceAllString(sourceID, "_")

	// Ensure the sanitized ID is still valid
	if safeSourceID == "" {
		streamCancel() // Clean up context
		return nil, fmt.Errorf("invalid source ID after sanitization")
	}

	outputDir := filepath.Join(hlsBaseDir, fmt.Sprintf("stream_%s", safeSourceID))

	// Verify the output directory is within the HLS base directory for safety
	isWithin, err := isPathWithinBase(hlsBaseDir, outputDir)
	if err != nil {
		streamCancel() // Clean up context
		return nil, fmt.Errorf("failed to validate output directory: %w", err)
	}

	if !isWithin {
		streamCancel() // Clean up context
		return nil, fmt.Errorf("security error: output directory would be outside HLS base directory")
	}

	// Ensure the directory exists and is empty
	if secFS.Exists(outputDir) {
		log.Printf("Removing existing output directory: %s", outputDir)
		if err := secFS.RemoveAll(outputDir); err != nil {
			streamCancel() // Clean up context
			return nil, fmt.Errorf("failed to clean HLS directory: %w", err)
		}
	}

	log.Printf("Creating new output directory: %s", outputDir)
	if err := secFS.MkdirAll(outputDir, 0o755); err != nil {
		streamCancel() // Clean up context
		return nil, fmt.Errorf("failed to create HLS directory: %w", err)
	}

	// Verify the directory was created successfully
	if !secFS.Exists(outputDir) {
		streamCancel() // Clean up context
		return nil, fmt.Errorf("failed to create HLS directory: directory doesn't exist after creation")
	}

	// Create playlist file path
	playlistPath := filepath.Join(outputDir, "playlist.m3u8")

	// Verify the playlist file is within the HLS base directory
	isWithin, err = isPathWithinBase(hlsBaseDir, playlistPath)
	if err != nil {
		streamCancel() // Clean up context
		return nil, fmt.Errorf("failed to validate playlist path: %w", err)
	}

	if !isWithin {
		streamCancel() // Clean up context
		return nil, fmt.Errorf("security error: playlist path would be outside HLS base directory")
	}

	// Get FFmpeg path from settings
	ffmpegPath := conf.Setting().Realtime.Audio.FfmpegPath
	if ffmpegPath == "" {
		streamCancel() // Clean up context
		return nil, fmt.Errorf("ffmpeg not configured")
	}

	// Start FFmpeg process to read from FIFO and create HLS stream
	fifoPath := filepath.Join(outputDir, "audio.pcm")

	// Verify the FIFO is within the HLS base directory
	isWithin, err = isPathWithinBase(hlsBaseDir, fifoPath)
	if err != nil {
		streamCancel() // Clean up context
		return nil, fmt.Errorf("failed to validate FIFO path: %w", err)
	}

	if !isWithin {
		streamCancel() // Clean up context
		return nil, fmt.Errorf("security error: FIFO path would be outside HLS base directory")
	}

	log.Printf("Creating FIFO for HLS stream: %s", fifoPath)
	// Use the secure filesystem for FIFO creation
	if err := secFS.CreateFIFO(fifoPath); err != nil {
		// Use secureFS for cleanup
		if removeErr := secFS.RemoveAll(outputDir); removeErr != nil {
			log.Printf("Error removing output directory: %v", removeErr)
		}
		streamCancel() // Clean up context
		return nil, fmt.Errorf("failed to create FIFO: %w", err)
	}

	log.Printf("Starting FFmpeg HLS process for source: %s", sourceID)
	// Start FFmpeg HLS process with improved parameters for reliability
	cmd := exec.CommandContext(
		streamCtx,
		ffmpegPath,
		"-f", "s16le", // Input format: 16-bit PCM
		"-ar", "48000", // Sample rate: 48kHz
		"-ac", "1", // Channels: mono
		"-i", fifoPath, // Input from FIFO
		"-c:a", "aac", // Codec: AAC
		"-b:a", "96k", // Bitrate: 96kbps
		"-f", "hls", // Format: HLS
		"-hls_time", "2", // Segment duration: 2 seconds
		"-hls_list_size", "5", // Keep 5 segments in playlist
		"-hls_flags", "delete_segments+append_list", // Delete old segments and append to playlist
		"-hls_segment_type", "mpegts", // Use MPEGTS segments for better compatibility
		"-hls_init_time", "1", // Initial segment length: 1 second for faster startup
		"-hls_allow_cache", "0", // Disable caching
		"-start_number", "0", // Start with segment 0
		"-loglevel", "warning", // Reduce ffmpeg logging verbosity
		"-hls_segment_filename", filepath.Join(outputDir, "segment%03d.ts"),
		playlistPath, // Output playlist
	)

	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		log.Printf("Error starting FFmpeg: %v", err)
		// Use secureFS for cleanup
		if err := secFS.RemoveAll(outputDir); err != nil {
			log.Printf("Error removing output directory: %v", err)
		}
		streamCancel() // Clean up context
		return nil, fmt.Errorf("failed to start FFmpeg: %w", err)
	}

	log.Printf("FFmpeg process started successfully for source: %s", sourceID)

	// Create stream info
	stream := &HLSStreamInfo{
		SourceID:     sourceID,
		FFmpegCmd:    cmd,
		OutputDir:    outputDir,
		PlaylistPath: playlistPath,
		LastAccess:   time.Now(),
		ctx:          streamCtx,
		cancel:       streamCancel,
	}

	// Store stream in map
	hlsStreams[sourceID] = stream

	// Start goroutine to feed audio data to FFmpeg
	go feedAudioToFFmpeg(sourceID, fifoPath, streamCtx)

	// Start goroutine to handle context cancellation
	go func() {
		<-streamCtx.Done()
		cleanupStream(sourceID)
	}()

	return stream, nil
}

// cleanupStream handles stream cleanup when terminated
func cleanupStream(sourceID string) {
	hlsStreamMutex.Lock()
	stream, exists := hlsStreams[sourceID]
	if !exists {
		hlsStreamMutex.Unlock()
		return
	}

	log.Printf("Cleaning up HLS stream for source: %s", sourceID)

	// With exec.CommandContext, the process will be automatically terminated
	// when the context is canceled. We just need to wait for it to exit cleanly.
	if stream.FFmpegCmd != nil && stream.FFmpegCmd.Process != nil {
		log.Printf("Waiting for FFmpeg process to terminate for source: %s", sourceID)
		_, _ = stream.FFmpegCmd.Process.Wait()
	}

	// Remove from map
	delete(hlsStreams, sourceID)
	hlsStreamMutex.Unlock()

	// Clean up client tracking
	hlsStreamClientMutex.Lock()
	delete(hlsStreamClients, sourceID)
	hlsStreamClientMutex.Unlock()

	// Clean up activity tracking
	hlsClientActivityMutex.Lock()
	delete(hlsClientActivity, sourceID)
	hlsClientActivityMutex.Unlock()

	// Get HLS directory for secure path checks
	hlsBaseDir, err := conf.GetHLSDirectory()
	if err != nil {
		log.Printf("Error getting HLS directory: %v", err)
		return
	}

	// Clean up directory using secure filesystem operations
	if stream.OutputDir != "" {
		// Create a secure filesystem for cleanup operations
		secFS, err := newSecureFS(hlsBaseDir)
		if err != nil {
			log.Printf("Error creating secure filesystem: %v", err)
			return
		}
		defer secFS.Close()

		// Check if directory exists using secureFS
		if secFS.Exists(stream.OutputDir) {
			log.Printf("Removing stream directory: %s", stream.OutputDir)
			if err := secFS.RemoveAll(stream.OutputDir); err != nil {
				log.Printf("Error removing stream directory: %v", err)
			}
		}
	}

	log.Printf("HLS stream for source %s fully cleaned up", sourceID)
}

// secureFS provides filesystem operations with path validation
// using Go 1.24's os.Root for OS-level filesystem sandboxing
//
// The os.Root feature introduced in Go 1.24 provides directory-limited filesystem access,
// preventing path traversal vulnerabilities by enforcing access boundaries at the OS level.
// This implementation reliably prevents access to files outside the specified directory,
// including via symlinks, relative paths, or other traversal techniques.
//
// Security guarantees:
// - Prevents directory traversal attacks using "../" or other relative paths
// - Prevents access via symlinks that point outside the base directory
// - Prevents time-of-check/time-of-use (TOCTOU) race conditions
// - Prevents access to reserved Windows device names
//
// Example valid usage:
//
//	root, err := os.OpenRoot("/safe-directory")
//	if err != nil {
//	    return err
//	}
//	defer root.Close()
//
//	// Operations are safely contained within /safe-directory
//	file, err := root.Open("user-data.txt")
//	dir, err := root.OpenRoot("user/uploads")  // Sub-directory sandboxing
//
// More information: https://go.dev/blog/osroot
type secureFS struct {
	baseDir  string   // The base directory that all operations are restricted to
	root     *os.Root // The sandboxed filesystem root
	pipeName string   // Platform-specific pipe name for named pipes
}

// newSecureFS creates a new secure filesystem with the specified base directory
// using Go 1.24's os.Root for OS-level sandboxing. All operations through the
// returned secureFS will be restricted to the specified base directory.
func newSecureFS(baseDir string) (*secureFS, error) {
	// Ensure baseDir is an absolute path
	absPath, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve base path: %w", err)
	}

	// Create the directory if it doesn't exist
	if err := os.MkdirAll(absPath, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create base directory: %w", err)
	}

	// Create a sandboxed filesystem with os.Root
	// This is a Go 1.24 feature that provides OS-level filesystem isolation
	root, err := os.OpenRoot(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create filesystem sandbox: %w", err)
	}

	return &secureFS{
		baseDir: absPath,
		root:    root,
	}, nil
}

// relativePath converts an absolute path to a path relative to the base directory
func (sfs *secureFS) relativePath(path string) (string, error) {
	// Clean the path to handle any . or .. components safely
	path = filepath.Clean(path)

	// Get absolute paths for consistent comparison
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Verify the path is within the base directory for safety
	// Using the updated isPathWithinBase that handles non-existent paths
	isWithin, err := isPathWithinBase(sfs.baseDir, absPath)
	if err != nil {
		return "", fmt.Errorf("path validation error: %w", err)
	}

	if !isWithin {
		return "", fmt.Errorf("security error: path %s is outside allowed directory %s", path, sfs.baseDir)
	}

	// Make the path relative to the base directory for os.Root operations
	relPath, err := filepath.Rel(sfs.baseDir, absPath)
	if err != nil {
		return "", fmt.Errorf("failed to make path relative: %w", err)
	}

	// Ensure no leading slash which would make a relative path be treated as absolute
	relPath = strings.TrimPrefix(relPath, string(filepath.Separator))

	return relPath, nil
}

// MkdirAll creates a directory and all necessary parent directories with path validation
func (sfs *secureFS) MkdirAll(path string, perm os.FileMode) error {
	// Get relative path for os.Root operations
	relPath, err := sfs.relativePath(path)
	if err != nil {
		return err
	}

	// If the path is the root, it's already created
	if relPath == "" || relPath == "." {
		return nil
	}

	// Create directories recursively
	components := strings.Split(relPath, string(filepath.Separator))
	currentPath := ""

	// Create each directory component
	for i, component := range components {
		// Skip empty components that might result from path normalization
		if component == "" {
			continue
		}

		// Build the current path
		if currentPath == "" {
			currentPath = component
		} else {
			currentPath = filepath.Join(currentPath, component)
		}

		// Try to create the directory using os.Root.Mkdir
		// Ignore "already exists" errors
		err := sfs.root.Mkdir(currentPath, perm)
		if err != nil && !os.IsExist(err) {
			return fmt.Errorf("failed to create directory component %s: %w", currentPath, err)
		}

		// If this is the last component, we're done
		if i == len(components)-1 {
			return nil
		}
	}

	return nil
}

// walkRemove is a helper function that walks a directory tree and removes files and directories
// in a secure manner using os.Root operations where possible
func (sfs *secureFS) walkRemove(path string) error {
	// Validate the path is within the base directory
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}

	// Final security check that path is within base directory
	isWithin, err := isPathWithinBase(sfs.baseDir, absPath)
	if err != nil {
		// If the path doesn't exist, we don't need to remove it
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("path validation error: %w", err)
	}
	if !isWithin {
		return fmt.Errorf("security error: path %s is outside allowed directory %s", absPath, sfs.baseDir)
	}

	// Get file info to determine if it's a file or directory
	info, err := os.Stat(absPath)
	if os.IsNotExist(err) {
		return nil // Already gone, no error
	}
	if err != nil {
		return err
	}

	if !info.IsDir() {
		// For regular files, use os.Root.Remove if possible
		relPath, err := sfs.relativePath(absPath)
		if err != nil {
			return err
		}
		return sfs.root.Remove(relPath)
	}

	// For directories, we need to walk and remove contents first
	entries, err := os.ReadDir(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Directory already gone
		}
		return err
	}

	// Remove each entry in the directory
	for _, entry := range entries {
		childPath := filepath.Join(absPath, entry.Name())

		// Validate child path is within base directory
		isChildWithin, err := isPathWithinBase(sfs.baseDir, childPath)
		if err != nil {
			// Skip entries that don't exist
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("child path validation error: %w", err)
		}

		if !isChildWithin {
			return fmt.Errorf("security error: child path %s is outside allowed directory %s",
				childPath, sfs.baseDir)
		}

		if err := sfs.walkRemove(childPath); err != nil {
			return err
		}
	}

	// Now that the directory is empty, remove it using os.Root if possible
	relPath, err := sfs.relativePath(absPath)
	if err != nil {
		return err
	}
	return sfs.root.Remove(relPath)
}

// RemoveAll removes a directory and all its contents with path validation
// This implementation provides a more secure alternative to os.RemoveAll by using
// os.Root operations for each individual file/directory where possible
func (sfs *secureFS) RemoveAll(path string) error {
	// Validate the path is within the base directory
	if err := isPathValidWithinBase(sfs.baseDir, path); err != nil {
		return err
	}

	// Use our secure walkRemove implementation
	return sfs.walkRemove(path)
}

// Remove removes a file with path validation
func (sfs *secureFS) Remove(path string) error {
	// Get relative path for os.Root operations
	relPath, err := sfs.relativePath(path)
	if err != nil {
		return err
	}

	// Use os.Root.Remove to safely remove the file
	return sfs.root.Remove(relPath)
}

// OpenFile opens a file with path validation
func (sfs *secureFS) OpenFile(path string, flag int, perm os.FileMode) (*os.File, error) {
	// Get relative path for os.Root operations
	relPath, err := sfs.relativePath(path)
	if err != nil {
		return nil, err
	}

	// Use os.Root.OpenFile to safely open the file
	return sfs.root.OpenFile(relPath, flag, perm)
}

// Stat returns file info with path validation
func (sfs *secureFS) Stat(path string) (fs.FileInfo, error) {
	// Get relative path for os.Root operations
	relPath, err := sfs.relativePath(path)
	if err != nil {
		return nil, err
	}

	// Use os.Root.Stat to safely get file info
	return sfs.root.Stat(relPath)
}

// Exists checks if a path exists with validation
func (sfs *secureFS) Exists(path string) bool {
	// Get relative path for os.Root operations
	relPath, err := sfs.relativePath(path)
	if err != nil {
		return false
	}

	// Use os.Root.Stat to check if the file exists
	_, err = sfs.root.Stat(relPath)
	return err == nil
}

// ReadFile reads a file with path validation and returns its contents
func (sfs *secureFS) ReadFile(path string) ([]byte, error) {
	// Open the file using secure methods
	file, err := sfs.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Read the entire file
	return io.ReadAll(file)
}

// ServeFile serves a file through an HTTP response
// This provides a secure alternative to echo.Context.File()
func (sfs *secureFS) ServeFile(c echo.Context, path string) error {
	// Get relative path for os.Root operations
	relPath, err := sfs.relativePath(path)
	if err != nil {
		return err
	}

	// Open the file using os.Root for sandbox protection
	file, err := sfs.root.Open(relPath)
	if err != nil {
		if os.IsNotExist(err) {
			return echo.NewHTTPError(http.StatusNotFound, "File not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to open file")
	}
	defer file.Close()

	// Get file info for content-length
	stat, err := file.Stat()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to get file info")
	}

	// Only serve regular files
	if !stat.Mode().IsRegular() {
		return echo.NewHTTPError(http.StatusForbidden, "Not a regular file")
	}

	// Set content type based on file extension
	contentType := mime.TypeByExtension(filepath.Ext(path))
	if contentType != "" {
		c.Response().Header().Set(echo.HeaderContentType, contentType)
	}

	// Set content length
	c.Response().Header().Set(echo.HeaderContentLength, strconv.FormatInt(stat.Size(), 10))

	// Stream the file directly from within the sandbox
	_, err = io.Copy(c.Response(), file)
	return err
}

// CreateFIFO creates a named pipe with path validation
func (sfs *secureFS) CreateFIFO(path string) error {
	// Validate the path is within the base directory
	if err := isPathValidWithinBase(sfs.baseDir, path); err != nil {
		return err
	}

	// Call the platform-agnostic wrapper which now returns the pipe name
	pipeName, err := createFIFOWrapper(path)
	if err != nil {
		return err
	}

	// Store the pipeName in the secureFS instance to retrieve it later
	sfs.pipeName = pipeName
	return nil
}

// GetPipeName returns the platform-specific pipe name for the given path
// On Windows this returns the Windows named pipe path, on Unix systems
// this returns the original path
func (sfs *secureFS) GetPipeName(path string) (string, error) {
	// Validate the path is within the base directory
	if err := isPathValidWithinBase(sfs.baseDir, path); err != nil {
		return "", err
	}

	// If we have a stored pipe name from CreateFIFO, use that
	if sfs.pipeName != "" {
		return sfs.pipeName, nil
	}

	// Otherwise, determine the platform-specific pipe name based on OS
	if runtime.GOOS == "windows" {
		// Convert Unix-style path to Windows named pipe path
		// Format: \\.\pipe\[path]
		baseName := filepath.Base(path)
		ext := filepath.Ext(baseName)
		pipeName := strings.TrimSuffix(baseName, ext)
		return `\\.\pipe\` + pipeName, nil
	}

	// For Unix systems, return the original path
	return path, nil
}

// Close closes the underlying Root
func (sfs *secureFS) Close() error {
	if sfs.root != nil {
		return sfs.root.Close()
	}
	return nil
}

// isPathValidWithinBase is a helper that checks if a path is within a base directory
// and returns an error if not
func isPathValidWithinBase(baseDir, path string) error {
	isWithin, err := isPathWithinBase(baseDir, path)
	if err != nil {
		// If the error is because the target doesn't exist, don't treat it as a security error
		// This is common during cleanup operations when we're checking paths that might be gone
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("path validation error: %w", err)
	}

	if !isWithin {
		return fmt.Errorf("security error: path %s is outside allowed directory %s", path, baseDir)
	}

	return nil
}

// setupFIFO prepares and opens the FIFO for writing with platform-specific settings
func setupFIFO(ctx context.Context, sourceID, fifoPath string, secFS *secureFS) (*os.File, error) {
	// Get the platform-specific pipe name
	pipePath, err := secFS.GetPipeName(fifoPath)
	if err != nil {
		return nil, fmt.Errorf("error getting platform-specific pipe name: %w", err)
	}

	// Set platform-specific flags for opening FIFO
	openFlags := getPlatformOpenFlags()

	// Try to open the FIFO with retries
	return openFIFOWithRetries(ctx, sourceID, fifoPath, pipePath, openFlags, secFS)
}

// getPlatformOpenFlags returns OS-specific open flags for the FIFO
func getPlatformOpenFlags() int {
	if runtime.GOOS == "windows" {
		return os.O_WRONLY // Windows uses writeable flag without O_NONBLOCK
	}
	// Unix systems use non-blocking flag to prevent indefinite blocking if FFmpeg crashes
	return os.O_WRONLY | syscall.O_NONBLOCK
}

// openFIFOWithRetries attempts to open the FIFO with multiple retries
func openFIFOWithRetries(ctx context.Context, sourceID, fifoPath, pipePath string, openFlags int, secFS *secureFS) (*os.File, error) {
	maxRetries := 30
	retryInterval := 200 * time.Millisecond

	for i := 0; i < maxRetries; i++ {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context canceled while opening FIFO")
		default:
			// Attempt to open the FIFO with platform-specific approach
			fifo, openErr := openPlatformSpecificFIFO(pipePath, fifoPath, openFlags, secFS)
			if openErr == nil {
				log.Printf("FIFO opened successfully for source %s on attempt %d", sourceID, i+1)
				return fifo, nil
			}

			if i == 0 || (i+1)%5 == 0 {
				log.Printf("Waiting for FFmpeg to open FIFO (attempt %d): %v", i+1, openErr)
			}

			// Sleep before retrying
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("context canceled during retry delay")
			case <-time.After(retryInterval):
				// Continue to next attempt
			}
		}
	}

	return nil, fmt.Errorf("failed to open FIFO after %d attempts", maxRetries)
}

// openPlatformSpecificFIFO opens the FIFO using OS-specific approach
func openPlatformSpecificFIFO(pipePath, fifoPath string, openFlags int, secFS *secureFS) (*os.File, error) {
	if runtime.GOOS == "windows" {
		// For Windows, open the named pipe directly
		return os.OpenFile(pipePath, openFlags, 0o666)
	}
	// For Unix systems, use secFS to maintain security
	return secFS.OpenFile(fifoPath, openFlags, 0o666)
}

// setupAudioCallback sets up the audio callback and channel
func setupAudioCallback(sourceID string) (audioChan chan []byte, cleanup func(), err error) {
	audioChan = make(chan []byte, 50)

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
		log.Printf("Unregistered audio callback for source %s", sourceID)
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
				log.Printf("First audio data successfully written to FIFO for source %s", sourceID)
				dataWritten = true
			}
		}
	}
}

// feedAudioToFFmpeg pumps audio data to the FFmpeg process
func feedAudioToFFmpeg(sourceID, fifoPath string, ctx context.Context) {
	log.Printf("Starting audio feed for source %s to FIFO %s", sourceID, fifoPath)

	// Set up secure filesystem
	secFS, err := setupSecureFS(fifoPath)
	if err != nil {
		log.Printf("Error setting up secure filesystem: %v", err)
		return
	}
	defer secFS.Close()

	// Set up and open the FIFO
	fifo, err := setupFIFO(ctx, sourceID, fifoPath, secFS)
	if err != nil {
		log.Printf("Error opening FIFO: %v", err)
		return
	}
	defer func() {
		log.Printf("Closing FIFO for source %s", sourceID)
		fifo.Close()
	}()

	// Set up audio callback
	audioChan, cleanup, err := setupAudioCallback(sourceID)
	if err != nil {
		log.Printf("Error setting up audio callback: %v", err)
		return
	}
	defer cleanup()

	log.Printf("Audio feed ready for source %s", sourceID)

	// Process audio data
	err = processFIFOData(ctx, sourceID, fifo, audioChan)
	if err != nil {
		log.Printf("Audio processing stopped: %v for source %s", err, sourceID)
	}
}

// setupSecureFS prepares the secure filesystem and validates paths
func setupSecureFS(fifoPath string) (*secureFS, error) {
	// Get HLS directory for path validation
	hlsBaseDir, err := conf.GetHLSDirectory()
	if err != nil {
		return nil, fmt.Errorf("error getting HLS directory: %w", err)
	}

	// Create a secure filesystem for operations
	secFS, err := newSecureFS(hlsBaseDir)
	if err != nil {
		return nil, fmt.Errorf("error creating secure filesystem: %w", err)
	}

	// Validate fifoPath before opening
	isWithin, err := isPathWithinBase(hlsBaseDir, fifoPath)
	if err != nil {
		secFS.Close()
		return nil, fmt.Errorf("error validating FIFO path: %w", err)
	}
	if !isWithin {
		secFS.Close()
		return nil, fmt.Errorf("security error: FIFO path would be outside HLS directory: %s", fifoPath)
	}

	return secFS, nil
}

// cleanupInactiveStreams removes streams that haven't been accessed recently
func cleanupInactiveStreams() {
	hlsStreamMutex.Lock()
	defer hlsStreamMutex.Unlock()

	now := time.Now()
	for sourceID, stream := range hlsStreams {
		if now.Sub(stream.LastAccess) <= inactivityTimeout {
			continue
		}

		log.Printf("Cleaning up inactive HLS stream for source: %s (inactive for %v)",
			sourceID, now.Sub(stream.LastAccess))

		// Cancel the stream context to trigger cleanup
		if stream.cancel != nil {
			stream.cancel()
		}
		// All streams should have context now, so we don't need the fallback anymore
	}
}

// syncHLSClientActivity verifies true client activity by checking segment requests
func syncHLSClientActivity() {
	// Process inactive clients first
	inactiveClients, activeCount := processInactiveClients()

	// Get HLS directory for secure path checks
	hlsBaseDir, err := conf.GetHLSDirectory()
	if err != nil {
		log.Printf("Error getting HLS directory: %v", err)
		return
	}

	// Create a secureFS for safe filesystem operations
	secureFs, err := newSecureFS(hlsBaseDir)
	if err != nil {
		log.Printf("Error creating secure filesystem: %v", err)
		return
	}
	defer secureFs.Close()

	// Sync client tracking with activity data
	syncStreamClients(inactiveClients, activeCount, hlsBaseDir, secureFs)
}

// processInactiveClients cleans up inactive clients and returns active client counts and inactive client IDs
func processInactiveClients() (inactiveClients map[string][]string, activeCount map[string]int) {
	hlsClientActivityMutex.Lock()
	defer hlsClientActivityMutex.Unlock()

	now := time.Now()
	activeCount = make(map[string]int)
	inactiveClients = make(map[string][]string)

	// Check for inactive clients
	for sourceID, clients := range hlsClientActivity {
		activeCount[sourceID] = 0
		inactiveClients[sourceID] = []string{}

		for clientID, lastActive := range clients {
			// Calculate inactivity duration
			inactiveDuration := now.Sub(lastActive)

			if inactiveDuration > clientInactivityTimeout {
				// Client hasn't requested segments recently, consider inactive
				delete(clients, clientID)
				inactiveClients[sourceID] = append(inactiveClients[sourceID], clientID)
				log.Printf("Removing inactive HLS client %s for source %s (no segments requested for %v)",
					clientID, sourceID, inactiveDuration)
			} else {
				activeCount[sourceID]++
			}
		}

		// Remove source entry if no clients left
		if len(clients) == 0 {
			delete(hlsClientActivity, sourceID)
		}
	}

	return inactiveClients, activeCount
}

// syncStreamClients cleans up streams with no active clients
func syncStreamClients(inactiveClients map[string][]string, activeCount map[string]int, hlsBaseDir string, secureFs *secureFS) {
	hlsStreamMutex.Lock()

	// Get current time for grace period calculations
	now := time.Now()

	// Track streams to clean up, we'll do this outside the lock
	streamsToCleanup := []string{}

	// First, process inactive clients we found and collect streams to check
	hlsStreamClientMutex.Lock()

	// First, process inactive clients we found
	for sourceID, clientIDs := range inactiveClients {
		if clients, exists := hlsStreamClients[sourceID]; exists {
			for _, clientID := range clientIDs {
				delete(clients, clientID)
			}
		}
	}

	// Then check for sources with no active clients
	for sourceID, clients := range hlsStreamClients {
		// Look up the corresponding stream to check its creation time
		stream, streamExists := hlsStreams[sourceID]

		// If no active clients for this source, clean up after a grace period
		activityCount := activeCount[sourceID]
		trackedCount := len(clients)

		if activityCount == 0 && trackedCount > 0 {
			// Tracking says we have clients but no active clients detected
			// Only clean up if the stream has been around for at least a few seconds
			// to avoid race conditions during stream startup

			// Apply a 30-second grace period for newly created streams to avoid
			// cleaning up streams that are still initializing
			streamAge := time.Duration(0)
			if streamExists {
				streamAge = now.Sub(stream.LastAccess)
			}

			if !streamExists || streamAge >= 15*time.Second {
				log.Printf("Client tracking mismatch for source %s: tracked=%d, active=%d, age=%v. Resolving...",
					sourceID, trackedCount, activityCount, streamAge)

				// Force clean up all clients for this source
				delete(hlsStreamClients, sourceID)

				// Mark for stream cleanup
				streamsToCleanup = append(streamsToCleanup, sourceID)
			} else {
				// Stream is too new, give it more time before cleanup
				log.Printf("Delaying cleanup for new HLS stream %s: tracked=%d, active=%d, age=%v",
					sourceID, trackedCount, activityCount, streamAge)
			}
		}
	}

	hlsStreamClientMutex.Unlock()
	hlsStreamMutex.Unlock()

	// Clean up streams in a separate lock scope to prevent deadlocks
	for _, sourceID := range streamsToCleanup {
		cleanupInactiveStream(sourceID, hlsBaseDir, secureFs)
	}
}

// cleanupInactiveStream stops and cleans up an inactive stream
func cleanupInactiveStream(sourceID, hlsBaseDir string, secureFs *secureFS) {
	hlsStreamMutex.Lock()
	defer hlsStreamMutex.Unlock()

	if stream, exists := hlsStreams[sourceID]; exists {
		log.Printf("Stopping stale HLS stream for source %s (no active clients)", sourceID)

		// Cancel the context, which will terminate the FFmpeg process
		if stream.cancel != nil {
			stream.cancel()
		}

		// Wait for process termination if needed
		if stream.FFmpegCmd != nil && stream.FFmpegCmd.Process != nil {
			_, _ = stream.FFmpegCmd.Process.Wait()
		}

		// Clean up stream directory using secureFS
		if stream.OutputDir != "" && secureFs != nil {
			if secureFs.Exists(stream.OutputDir) {
				log.Printf("Cleaning up stream directory: %s", stream.OutputDir)
				if err := secureFs.RemoveAll(stream.OutputDir); err != nil {
					log.Printf("Error removing stream directory: %v", err)
				}
			}
		}

		delete(hlsStreams, sourceID)
		log.Printf("HLS stream for source %s fully cleaned up", sourceID)
	}
}

// StartHLSStream explicitly starts an HLS stream for a source
// This is called when a client wants to start playing a stream
func (h *Handlers) StartHLSStream(c echo.Context, sourceID string) (*StreamStatus, error) {
	clientIP := c.RealIP()
	clientID := fmt.Sprintf("%s-%d", clientIP, time.Now().UnixNano())

	log.Printf("Client %s requested to start HLS stream for source: %s", clientID, sourceID)

	// Check if source exists
	if !myaudio.HasCaptureBuffer(sourceID) {
		return nil, echo.NewHTTPError(http.StatusNotFound, "Audio source not found")
	}

	// First, ensure that any existing stream for this source is fully cleaned up
	// This is important if a previous cleanup didn't complete properly
	hlsStreamMutex.Lock()
	if stream, exists := hlsStreams[sourceID]; exists {
		log.Printf("Found existing stream for source %s, ensuring cleanup before restart", sourceID)

		// Cancel the context, which will terminate the FFmpeg process
		if stream.cancel != nil {
			stream.cancel()
		}

		// Wait for process termination if needed
		if stream.FFmpegCmd != nil && stream.FFmpegCmd.Process != nil {
			_, _ = stream.FFmpegCmd.Process.Wait()
		}

		// Get HLS directory for secure filesystem operations
		hlsBaseDir, err := conf.GetHLSDirectory()
		if err != nil {
			log.Printf("Error getting HLS directory: %v", err)
		} else if stream.OutputDir != "" {
			// Use secureFS to remove the directory
			secFS, err := newSecureFS(hlsBaseDir)
			if err != nil {
				log.Printf("Error creating secure filesystem: %v", err)
			} else {
				defer secFS.Close()

				if secFS.Exists(stream.OutputDir) {
					log.Printf("Removing stream directory: %s", stream.OutputDir)
					if err := secFS.RemoveAll(stream.OutputDir); err != nil {
						log.Printf("Error removing stream directory: %v", err)
					}
				}
			}
		}

		// Remove from map
		delete(hlsStreams, sourceID)
	}
	hlsStreamMutex.Unlock()

	// Register this client for the stream before starting FFmpeg
	// to avoid race condition where stream is terminated before client connects
	hlsStreamClientMutex.Lock()
	if _, exists := hlsStreamClients[sourceID]; !exists {
		hlsStreamClients[sourceID] = make(map[string]bool)
	}
	hlsStreamClients[sourceID][clientID] = true
	activeClients := len(hlsStreamClients[sourceID])
	hlsStreamClientMutex.Unlock()

	// Also update client activity timestamp
	hlsClientActivityMutex.Lock()
	if _, exists := hlsClientActivity[sourceID]; !exists {
		hlsClientActivity[sourceID] = make(map[string]time.Time)
	}
	hlsClientActivity[sourceID][clientID] = time.Now().Add(10 * time.Second) // Extend initial activity time
	hlsClientActivityMutex.Unlock()

	log.Printf("HLS stream for source %s now has %d active clients", sourceID, activeClients)

	// Start the FFmpeg process if it's not already running
	stream, err := getOrCreateHLSStream(context.Background(), sourceID)
	if err != nil {
		log.Printf("Error creating HLS stream: %v", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to start stream: %v", err))
	}

	// Get HLS directory for secure path checks
	hlsBaseDir, err := conf.GetHLSDirectory()
	if err != nil {
		log.Printf("Error getting HLS directory: %v", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError, "Server configuration error")
	}

	// Create a secure filesystem for checking playlist
	secFS, err := newSecureFS(hlsBaseDir)
	if err != nil {
		log.Printf("Error creating secure filesystem: %v", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError, "Server error")
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
				log.Printf("Playlist check cancelled or timed out for source: %s", sourceID)
				return
			default:
				// Check if playlist exists
				if secFS.Exists(stream.PlaylistPath) {
					// Check if it's a valid playlist with some content
					data, err := secFS.ReadFile(stream.PlaylistPath)
					if err == nil && len(data) > 0 && strings.Contains(string(data), "#EXTM3U") {
						playlistReady = true
						log.Printf("Playlist file is ready (attempt %d): %s", retryCount+1, stream.PlaylistPath)
						return
					}
				}

				// Check if stream is still active - don't wait if it's been terminated
				hlsStreamMutex.Lock()
				_, streamExists := hlsStreams[sourceID]
				hlsStreamMutex.Unlock()

				if !streamExists {
					log.Printf("Stream was terminated while waiting for playlist: %s", sourceID)
					return
				}

				log.Printf("Waiting for playlist file (attempt %d): %s", retryCount+1, stream.PlaylistPath)
				retryCount++
				time.Sleep(1000 * time.Millisecond)
			}
		}

		log.Printf("Warning: Playlist file not created after waiting: %s", stream.PlaylistPath)
	}()

	// Wait for the playlist checker to complete
	<-playlistCheckerDone

	status := "starting"
	if playlistReady {
		status = "ready"
		log.Printf("Playlist file is ready: %s", stream.PlaylistPath)
	} else {
		// For tighter UX, we still return a result even if playlist isn't ready
		// The client will retry loading the playlist
		log.Printf("Warning: Playlist file not immediately available: %s", stream.PlaylistPath)
	}

	// Return stream status information that the controller can use
	return &StreamStatus{
		Status:        status,
		Source:        sourceID,
		PlaylistPath:  stream.PlaylistPath,
		ActiveClients: activeClients,
		PlaylistReady: playlistReady,
	}, nil
}

// StopHLSClientStream registers that a client has stopped streaming
// When the last client disconnects, we'll stop the FFmpeg process
func (h *Handlers) StopHLSClientStream(c echo.Context, sourceID string) error {
	clientIP := c.RealIP()
	var lastClient bool
	var clientIDToRemove string
	var remainingClients int

	// First find the client to remove and check if it's the last one
	hlsStreamClientMutex.Lock()
	if clients, exists := hlsStreamClients[sourceID]; exists {
		// Find the client ID to remove
		for clientID := range clients {
			if strings.HasPrefix(clientID, clientIP+"-") {
				clientIDToRemove = clientID
				break
			}
		}

		// Remove the client if found
		if clientIDToRemove != "" {
			delete(clients, clientIDToRemove)

			// Check if no clients are left
			remainingClients = len(clients)
			lastClient = remainingClients == 0
			if lastClient {
				delete(hlsStreamClients, sourceID)
			}
		}
	}
	hlsStreamClientMutex.Unlock()

	// Log client disconnection - after releasing the mutex
	if clientIDToRemove != "" {
		log.Printf("Client %s disconnected from HLS stream for source: %s", clientIDToRemove, sourceID)

		if !lastClient {
			log.Printf("HLS stream for source %s still has %d active clients", sourceID, remainingClients)
		}
	}

	// If this was the last client, clean up the stream in a separate lock scope
	// Note: We've already released the client mutex, which prevents deadlock
	if lastClient {
		hlsStreamMutex.Lock()
		if stream, exists := hlsStreams[sourceID]; exists {
			log.Printf("Last client disconnected, stopping FFmpeg for source: %s", sourceID)

			// Cancel the context, which will terminate the FFmpeg process
			if stream.cancel != nil {
				stream.cancel()
			}

			// Wait for process termination if needed
			if stream.FFmpegCmd != nil && stream.FFmpegCmd.Process != nil {
				_, _ = stream.FFmpegCmd.Process.Wait()
			}

			// Clean up the stream
			delete(hlsStreams, sourceID)

			// Get HLS directory for secure path operations
			hlsBaseDir, err := conf.GetHLSDirectory()
			if err != nil {
				log.Printf("Error getting HLS directory: %v", err)
			} else if stream.OutputDir != "" {
				// Create a secure filesystem for cleanup
				secFS, err := newSecureFS(hlsBaseDir)
				if err != nil {
					log.Printf("Error creating secure filesystem: %v", err)
				} else {
					defer secFS.Close()

					// Clean up the directory using secureFS
					if secFS.Exists(stream.OutputDir) {
						log.Printf("Cleaning up stream directory: %s", stream.OutputDir)
						if err := secFS.RemoveAll(stream.OutputDir); err != nil {
							log.Printf("Error removing stream directory: %v", err)
						}
					}
				}
			}

			log.Printf("HLS stream stopped for source: %s", sourceID)
		}
		hlsStreamMutex.Unlock()
	}

	// Clean up client activity tracking in a separate lock scope
	if clientIDToRemove != "" {
		hlsClientActivityMutex.Lock()
		if clients, exists := hlsClientActivity[sourceID]; exists {
			delete(clients, clientIDToRemove)
			if len(clients) == 0 {
				delete(hlsClientActivity, sourceID)
			}
		}
		hlsClientActivityMutex.Unlock()
	}

	return nil
}

// CleanupAllStreams removes all HLS streams and their files
func CleanupAllStreams() error {
	hlsStreamMutex.Lock()
	defer hlsStreamMutex.Unlock()

	// Iterate through all streams and clean them up
	for sourceID, stream := range hlsStreams {
		log.Printf("Cleaning up HLS stream for source: %s", sourceID)

		// Cancel the context if it exists
		if stream.cancel != nil {
			stream.cancel()
		}

		// Wait for FFmpeg process to terminate if it exists
		if stream.FFmpegCmd != nil && stream.FFmpegCmd.Process != nil {
			_, _ = stream.FFmpegCmd.Process.Wait()
		}
	}

	// Clear the stream maps
	for sourceID := range hlsStreams {
		delete(hlsStreams, sourceID)
	}

	// Also clear the client tracking maps
	hlsStreamClientMutex.Lock()
	for sourceID := range hlsStreamClients {
		delete(hlsStreamClients, sourceID)
	}
	hlsStreamClientMutex.Unlock()

	hlsClientActivityMutex.Lock()
	for sourceID := range hlsClientActivity {
		delete(hlsClientActivity, sourceID)
	}
	hlsClientActivityMutex.Unlock()

	// Clean up any remaining stream directories
	hlsBaseDir, err := conf.GetHLSDirectory()
	if err != nil {
		return fmt.Errorf("failed to get HLS directory: %w", err)
	}

	// Check if directory exists
	if _, err := os.Stat(hlsBaseDir); os.IsNotExist(err) {
		return nil // Directory doesn't exist, nothing to clean up
	}

	// Read all entries in the HLS directory
	entries, err := os.ReadDir(hlsBaseDir)
	if err != nil {
		return fmt.Errorf("failed to read HLS directory: %w", err)
	}

	// Remove all stream directories
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "stream_") {
			streamDir := filepath.Join(hlsBaseDir, entry.Name())
			log.Printf("Removing HLS stream directory: %s", streamDir)

			if err := os.RemoveAll(streamDir); err != nil {
				log.Printf("Error removing stream directory %s: %v", streamDir, err)
				// Continue with other directories
			}
		}
	}

	return nil
}
