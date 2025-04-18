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

	// Consider a client inactive after 10 seconds of no segment requests
	clientInactivityTimeout = 10 * time.Second

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

// isPathWithinBase checks if a path is contained within a base directory
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

	// Ensure paths are cleaned to remove any ".." components
	absBase = filepath.Clean(absBase)
	absTarget = filepath.Clean(absTarget)

	// Check if target path starts with base path
	return strings.HasPrefix(absTarget, absBase+string(filepath.Separator)) || absTarget == absBase, nil
}

// getHLSDirectory returns the directory where HLS files should be stored
func getHLSDirectory() (string, error) {
	// Get config directory paths
	configPaths, err := conf.GetDefaultConfigPaths()
	if err != nil {
		return "", fmt.Errorf("failed to get config paths: %w", err)
	}

	if len(configPaths) == 0 {
		return "", fmt.Errorf("no config paths found")
	}

	// Use the first config path as the base
	baseDir := configPaths[0]

	// Create HLS directory
	hlsDir := filepath.Join(baseDir, "hls")
	if err := os.MkdirAll(hlsDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create HLS directory: %w", err)
	}

	// Return the absolute path to ensure all path checks are consistent
	absPath, err := filepath.Abs(hlsDir)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path for HLS directory: %w", err)
	}

	return absPath, nil
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
	hlsDir, err := getHLSDirectory()
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

	sourceID := c.Param("sourceID")
	clientIP := c.RealIP()
	clientID := clientIP + "-" + c.Request().Header.Get("User-Agent")

	if sourceID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Source ID is required")
	}

	// Check authentication if the server requires it
	// The server object should be available in the context from the route handler
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
				return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
			}
		}
	}

	// Check if source exists and has a valid capture buffer
	if !myaudio.HasCaptureBuffer(sourceID) {
		log.Printf("Audio source not found for HLS stream - source: %s, IP: %s", sourceID, clientIP)
		return echo.NewHTTPError(http.StatusNotFound, "Audio source not found")
	}

	// Register client activity at the start of the request
	registerClientActivity(sourceID, clientID)

	// Get or create HLS stream
	stream, err := getOrCreateHLSStream(ctx, sourceID)
	if err != nil {
		log.Printf("Error creating HLS stream: %v - source: %s, IP: %s", err, sourceID, clientIP)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to create audio stream")
	}

	// Update access time
	hlsStreamMutex.Lock()
	stream.LastAccess = time.Now()
	hlsStreamMutex.Unlock()

	// Get HLS base directory for security validation
	hlsBaseDir, err := getHLSDirectory()
	if err != nil {
		log.Printf("Error getting HLS directory: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Server configuration error")
	}

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
	go func() {
		<-ctx.Done()
		// Request completed or canceled, update last activity
		updateClientDisconnection(sourceID, clientID)
	}()

	// Extremely minimal logging - only log initial connection and with cooldown period
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

	// Add CORS headers to allow cross-origin requests
	c.Response().Header().Set("Access-Control-Allow-Origin", "*")
	c.Response().Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	c.Response().Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Accept")

	// Validate playlist path request for path traversal prevention
	if requestPath == "" || requestPath == "playlist.m3u8" {
		// Sanitize the path
		cleanPath := filepath.Clean("/" + requestPath)
		if strings.Contains(cleanPath, "..") || cleanPath == "/" {
			log.Printf("Warning: Suspicious playlist path requested: %s", requestPath)
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid playlist path")
		}

		// Verify playlist path is within HLS base directory
		isWithin, err := isPathWithinBase(hlsBaseDir, stream.PlaylistPath)
		if err != nil {
			log.Printf("Error validating playlist path: %v", err)
			return echo.NewHTTPError(http.StatusInternalServerError, "Failed to validate file path")
		}

		if !isWithin {
			log.Printf("Security warning: Attempted access to playlist outside HLS directory: %s", stream.PlaylistPath)
			return echo.NewHTTPError(http.StatusForbidden, "Access denied")
		}

		// Only log errors for missing files
		if !checkFileExists(stream.PlaylistPath) {
			log.Printf("Error: HLS playlist file does not exist at %s", stream.PlaylistPath)
			return echo.NewHTTPError(http.StatusNotFound, "Playlist file not found")
		}

		// Set proper content type for m3u8 playlist
		c.Response().Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		// Add cache control headers to prevent caching
		c.Response().Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		c.Response().Header().Set("Pragma", "no-cache")
		c.Response().Header().Set("Expires", "0")
		// Serve the main playlist
		return c.File(stream.PlaylistPath)
	}

	// Validate segment path for path traversal prevention
	cleanPath := filepath.Clean("/" + requestPath)
	if strings.Contains(cleanPath, "..") || cleanPath == "/" {
		log.Printf("Warning: Suspicious segment path requested: %s", requestPath)
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid segment path")
	}

	// Remove leading slash for concatenation
	safeRequestPath := cleanPath[1:]

	// Build and validate the full segment path
	segmentPath := filepath.Join(stream.OutputDir, safeRequestPath)

	// Verify segment path is within the stream output directory
	isWithin, err := isPathWithinBase(hlsBaseDir, segmentPath)
	if err != nil {
		log.Printf("Error validating segment path: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to validate file path")
	}

	if !isWithin {
		log.Printf("Security warning: Attempted access to segment file outside HLS directory: %s", segmentPath)
		return echo.NewHTTPError(http.StatusForbidden, "Access denied")
	}

	// Check if segment file exists
	if !checkFileExists(segmentPath) {
		log.Printf("Error: HLS segment file does not exist at %s", segmentPath)
		return echo.NewHTTPError(http.StatusNotFound, "Segment file not found")
	}

	// For .ts segment files
	if strings.HasSuffix(safeRequestPath, ".ts") {
		c.Response().Header().Set("Content-Type", "video/mp2t")
		// Allow caching of segments for a short time
		c.Response().Header().Set("Cache-Control", "public, max-age=60")
	}

	// Serve segment file
	return c.File(segmentPath)
}

// registerClientActivity records client activity for a source
func registerClientActivity(sourceID, clientID string) {
	hlsClientActivityMutex.Lock()
	defer hlsClientActivityMutex.Unlock()

	if _, exists := hlsClientActivity[sourceID]; !exists {
		hlsClientActivity[sourceID] = make(map[string]time.Time)
	}
	hlsClientActivity[sourceID][clientID] = time.Now()

	// Also register in clients map for consistency
	hlsStreamClientMutex.Lock()
	defer hlsStreamClientMutex.Unlock()

	if _, exists := hlsStreamClients[sourceID]; !exists {
		hlsStreamClients[sourceID] = make(map[string]bool)
	}
	hlsStreamClients[sourceID][clientID] = true
}

// updateClientDisconnection handles client disconnection events
func updateClientDisconnection(sourceID, clientID string) {
	// Just mark the last activity time, let the regular cleanup handle the rest
	// This avoids immediate cleanup which could interrupt other active requests
	registerClientActivity(sourceID, clientID)
}

// Helper function to check if a file exists
func checkFileExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

// getOrCreateHLSStream gets an existing stream or creates a new one
func getOrCreateHLSStream(ctx context.Context, sourceID string) (*HLSStreamInfo, error) {
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
	hlsBaseDir, err := getHLSDirectory()
	if err != nil {
		streamCancel() // Clean up context
		return nil, fmt.Errorf("failed to get HLS directory: %w", err)
	}

	// Create stream-specific directory
	// Use a sanitized version of the sourceID for the directory name
	// Apply strict sanitization to prevent directory traversal and other issues
	reSafe := regexp.MustCompile(`[^A-Za-z0-9_\-]`)
	safeSourceID := reSafe.ReplaceAllString(sourceID, "_")

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
	if checkPathExists(outputDir) {
		log.Printf("Removing existing output directory: %s", outputDir)
		if err := os.RemoveAll(outputDir); err != nil {
			streamCancel() // Clean up context
			return nil, fmt.Errorf("failed to clean HLS directory: %w", err)
		}
	}

	log.Printf("Creating new output directory: %s", outputDir)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		streamCancel() // Clean up context
		return nil, fmt.Errorf("failed to create HLS directory: %w", err)
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
	// Use the secure version with baseDir parameter
	if err := secureCreateFIFO(hlsBaseDir, fifoPath); err != nil {
		os.RemoveAll(outputDir)
		streamCancel() // Clean up context
		return nil, fmt.Errorf("failed to create FIFO: %w", err)
	}

	log.Printf("Starting FFmpeg HLS process for source: %s", sourceID)
	// Start FFmpeg HLS process
	cmd := exec.Command(
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
		"-hls_flags", "delete_segments", // Delete old segments
		"-hls_segment_type", "mpegts", // Use MPEGTS segments for better compatibility
		"-loglevel", "warning", // Reduce ffmpeg logging verbosity
		"-hls_segment_filename", filepath.Join(outputDir, "segment%03d.ts"),
		playlistPath, // Output playlist
	)

	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		log.Printf("Error starting FFmpeg: %v", err)
		os.RemoveAll(outputDir)
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

	// Stop FFmpeg process with a more reliable approach
	if stream.FFmpegCmd != nil && stream.FFmpegCmd.Process != nil {
		log.Printf("Stopping FFmpeg process for source: %s", sourceID)

		// Try SIGTERM first for graceful shutdown
		err := stream.FFmpegCmd.Process.Signal(syscall.SIGTERM)
		if err != nil {
			log.Printf("Error sending SIGTERM to FFmpeg: %v, falling back to Kill", err)
			// Fall back to Kill if SIGTERM fails
			_ = stream.FFmpegCmd.Process.Kill()
		}

		// Wait for process to complete to avoid zombies
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
	if _, exists := hlsClientActivity[sourceID]; exists {
		delete(hlsClientActivity, sourceID)
	}
	hlsClientActivityMutex.Unlock()

	// Clean up directory
	if stream.OutputDir != "" && checkPathExists(stream.OutputDir) {
		log.Printf("Removing stream directory: %s", stream.OutputDir)
		if err := os.RemoveAll(stream.OutputDir); err != nil {
			log.Printf("Error removing stream directory: %v", err)
		}
	}

	log.Printf("HLS stream for source %s fully cleaned up", sourceID)
}

// secureFS provides filesystem operations with path validation
type secureFS struct {
	baseDir string // The base directory that all operations are restricted to
}

// newSecureFS creates a new secure filesystem with the specified base directory
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

	return &secureFS{baseDir: absPath}, nil
}

// validatePath checks if a path is within the allowed base directory
func (fs *secureFS) validatePath(path string) error {
	isWithin, err := isPathWithinBase(fs.baseDir, path)
	if err != nil {
		return fmt.Errorf("path validation error: %w", err)
	}

	if !isWithin {
		return fmt.Errorf("security error: path %s is outside allowed directory %s", path, fs.baseDir)
	}

	return nil
}

// MkdirAll creates a directory and all necessary parent directories with path validation
func (fs *secureFS) MkdirAll(path string, perm os.FileMode) error {
	if err := fs.validatePath(path); err != nil {
		return err
	}

	return os.MkdirAll(path, perm)
}

// RemoveAll removes a directory and all its contents with path validation
func (fs *secureFS) RemoveAll(path string) error {
	if err := fs.validatePath(path); err != nil {
		return err
	}

	return os.RemoveAll(path)
}

// Remove removes a file with path validation
func (fs *secureFS) Remove(path string) error {
	if err := fs.validatePath(path); err != nil {
		return err
	}

	return os.Remove(path)
}

// OpenFile opens a file with path validation
func (fs *secureFS) OpenFile(path string, flag int, perm os.FileMode) (*os.File, error) {
	if err := fs.validatePath(path); err != nil {
		return nil, err
	}

	return os.OpenFile(path, flag, perm)
}

// Stat returns file info with path validation
func (fs *secureFS) Stat(path string) (os.FileInfo, error) {
	if err := fs.validatePath(path); err != nil {
		return nil, err
	}

	return os.Stat(path)
}

// Exists checks if a path exists with validation
func (fs *secureFS) Exists(path string) bool {
	if err := fs.validatePath(path); err != nil {
		return false
	}

	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

// CreateFIFO creates a named pipe with path validation
func (fs *secureFS) CreateFIFO(path string) error {
	if err := fs.validatePath(path); err != nil {
		return err
	}

	// Remove if exists
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("Warning: Error removing existing FIFO: %v", err)
	}

	// Create FIFO with retry mechanism
	var err error
	for retry := 0; retry < 3; retry++ {
		err = syscall.Mkfifo(path, 0o666)
		if err == nil {
			log.Printf("Successfully created FIFO pipe: %s", path)
			return nil
		}

		log.Printf("Retry %d: Failed to create FIFO pipe: %v", retry+1, err)
		// If error is "file exists", try to remove again
		if os.IsExist(err) {
			os.Remove(path)
			time.Sleep(100 * time.Millisecond)
		}
	}

	return fmt.Errorf("failed to create FIFO after retries: %w", err)
}

// secureCreateFIFO creates a named pipe with path validation
// This is a transition function that will be replaced by secureFS.CreateFIFO in the future
func secureCreateFIFO(baseDir, path string) error {
	fs, err := newSecureFS(baseDir)
	if err != nil {
		return fmt.Errorf("failed to initialize secure filesystem: %w", err)
	}

	return fs.CreateFIFO(path)
}

// feedAudioToFFmpeg pumps audio data to the FFmpeg process
func feedAudioToFFmpeg(sourceID, fifoPath string, ctx context.Context) {
	log.Printf("Starting audio feed for source %s to FIFO %s", sourceID, fifoPath)

	// Open FIFO for writing (this will block until FFmpeg opens it for reading)
	log.Printf("Opening FIFO for writing: %s", fifoPath)
	fifo, err := os.OpenFile(fifoPath, os.O_WRONLY, 0o666)
	if err != nil {
		log.Printf("Error opening FIFO for writing: %v", err)
		return
	}
	defer func() {
		log.Printf("Closing FIFO for source %s", sourceID)
		fifo.Close()
	}()

	log.Printf("FIFO opened successfully for source %s", sourceID)

	// Register for audio callbacks
	audioChan := make(chan []byte, 50)

	// Create callback function
	callback := func(callbackSourceID string, data []byte) {
		if callbackSourceID == sourceID {
			select {
			case audioChan <- data:
				// Data sent successfully
			default:
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
		}
	}

	// Register callback
	myaudio.RegisterBroadcastCallback(sourceID, callback)
	defer func() {
		log.Printf("Unregistering audio callback for source %s", sourceID)
		myaudio.UnregisterBroadcastCallback(sourceID)
	}()

	log.Printf("Audio feed ready for source %s", sourceID)

	// Main loop to write audio data to FIFO
	dataWritten := false

	// Use the provided context for cancellation
	for {
		select {
		case <-ctx.Done():
			log.Printf("Context canceled, stopping audio feed for source %s", sourceID)
			return
		case data, ok := <-audioChan:
			if !ok {
				log.Printf("Audio channel closed for source %s", sourceID)
				return
			}

			// Write data to FIFO
			if _, err := fifo.Write(data); err != nil {
				log.Printf("Error writing to FIFO: %v", err)
				return
			}

			if !dataWritten {
				log.Printf("First audio data successfully written to FIFO for source %s", sourceID)
				dataWritten = true
			}
		}
	}
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
		} else {
			// For older streams without context, clean up manually
			cleanupStream(sourceID)
		}
	}
}

// Helper function to check if a path exists
func checkPathExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
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

		// Stop FFmpeg if running
		if stream.FFmpegCmd != nil && stream.FFmpegCmd.Process != nil {
			log.Printf("Stopping existing FFmpeg process for source %s", sourceID)

			// Try graceful termination first
			err := stream.FFmpegCmd.Process.Signal(syscall.SIGTERM)
			if err != nil {
				log.Printf("Error sending SIGTERM to FFmpeg: %v", err)
				// Try to kill forcefully
				_ = stream.FFmpegCmd.Process.Kill()
			}

			// Wait for process to exit
			_, _ = stream.FFmpegCmd.Process.Wait()
		}

		// Remove stream directory
		if stream.OutputDir != "" {
			log.Printf("Removing stream directory: %s", stream.OutputDir)
			_ = os.RemoveAll(stream.OutputDir)
		}

		// Remove from map
		delete(hlsStreams, sourceID)
	}
	hlsStreamMutex.Unlock()

	// Register this client for the stream
	hlsStreamClientMutex.Lock()
	if _, exists := hlsStreamClients[sourceID]; !exists {
		hlsStreamClients[sourceID] = make(map[string]bool)
	}
	hlsStreamClients[sourceID][clientID] = true
	activeClients := len(hlsStreamClients[sourceID])
	hlsStreamClientMutex.Unlock()

	log.Printf("HLS stream for source %s now has %d active clients", sourceID, activeClients)

	// Start the FFmpeg process if it's not already running
	stream, err := getOrCreateHLSStream(context.Background(), sourceID)
	if err != nil {
		log.Printf("Error creating HLS stream: %v", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to start stream: %v", err))
	}

	// Check if the playlist file exists, waiting a short time if needed
	playlistReady := false
	for retry := 0; retry < 15; retry++ {
		if checkFileExists(stream.PlaylistPath) {
			playlistReady = true
			log.Printf("Playlist file is ready on attempt %d: %s", retry+1, stream.PlaylistPath)
			break
		}
		log.Printf("Waiting for playlist file (attempt %d): %s", retry+1, stream.PlaylistPath)
		time.Sleep(300 * time.Millisecond)
	}

	status := "starting"
	if playlistReady {
		status = "ready"
		log.Printf("Playlist file is ready: %s", stream.PlaylistPath)
	} else {
		log.Printf("Warning: Playlist file not created after waiting: %s", stream.PlaylistPath)
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

	// Find and remove this client
	hlsStreamClientMutex.Lock()
	defer hlsStreamClientMutex.Unlock()

	if clients, exists := hlsStreamClients[sourceID]; exists {
		// Since we don't have the exact clientID from when it was registered,
		// we'll remove the first client entry from this IP address
		for clientID := range clients {
			if strings.HasPrefix(clientID, clientIP+"-") {
				delete(clients, clientID)
				log.Printf("Client %s disconnected from HLS stream for source: %s", clientID, sourceID)
				break
			}
		}

		// If no clients are left, stop the stream
		if len(clients) == 0 {
			delete(hlsStreamClients, sourceID)

			// Stop the FFmpeg process
			hlsStreamMutex.Lock()
			defer hlsStreamMutex.Unlock()

			if stream, exists := hlsStreams[sourceID]; exists {
				log.Printf("Last client disconnected, stopping FFmpeg for source: %s", sourceID)

				if stream.FFmpegCmd != nil && stream.FFmpegCmd.Process != nil {
					// Send a termination signal to FFmpeg
					err := stream.FFmpegCmd.Process.Signal(syscall.SIGTERM)
					if err != nil {
						log.Printf("Error sending SIGTERM to FFmpeg: %v", err)
						// Try to kill it forcefully if SIGTERM fails
						_ = stream.FFmpegCmd.Process.Kill()
					}

					// Wait for process to exit
					_, _ = stream.FFmpegCmd.Process.Wait()
				}

				// Clean up the stream
				delete(hlsStreams, sourceID)

				// Try to remove the stream directory if it exists
				if stream.OutputDir != "" {
					log.Printf("Cleaning up stream directory: %s", stream.OutputDir)
					_ = os.RemoveAll(stream.OutputDir)
				}
			}

			log.Printf("HLS stream stopped for source: %s", sourceID)
		} else {
			log.Printf("HLS stream for source %s still has %d active clients", sourceID, len(clients))
		}
	}

	return nil
}

// Add this new function to check for active clients
// syncHLSClientActivity verifies true client activity by checking segment requests
func syncHLSClientActivity() {
	hlsClientActivityMutex.Lock()
	defer hlsClientActivityMutex.Unlock()

	now := time.Now()
	activeCount := make(map[string]int)

	// Check for inactive clients first
	for sourceID, clients := range hlsClientActivity {
		activeCount[sourceID] = 0

		for clientID, lastActive := range clients {
			if now.Sub(lastActive) > clientInactivityTimeout {
				// Client hasn't requested segments recently, consider inactive
				delete(clients, clientID)
				log.Printf("Removing inactive HLS client %s for source %s (no segments requested for %v)",
					clientID, sourceID, now.Sub(lastActive))
			} else {
				activeCount[sourceID]++
			}
		}

		// Remove source entry if no clients left
		if len(clients) == 0 {
			delete(hlsClientActivity, sourceID)
		}
	}

	// Now sync client tracking with activity data
	hlsStreamClientMutex.Lock()
	defer hlsStreamClientMutex.Unlock()

	for sourceID, clients := range hlsStreamClients {
		// If no active clients for this source, clean up
		activityCount := activeCount[sourceID]
		trackedCount := len(clients)

		if activityCount == 0 && trackedCount > 0 {
			// Tracking says we have clients but no active clients detected
			log.Printf("Client tracking mismatch for source %s: tracked=%d, active=%d. Resolving...",
				sourceID, trackedCount, activityCount)

			// Force clean up all clients for this source
			delete(hlsStreamClients, sourceID)

			// Stop the stream
			go func(sid string) {
				hlsStreamMutex.Lock()
				defer hlsStreamMutex.Unlock()

				if stream, exists := hlsStreams[sid]; exists {
					log.Printf("Stopping stale HLS stream for source %s (no active clients)", sid)

					if stream.FFmpegCmd != nil && stream.FFmpegCmd.Process != nil {
						err := stream.FFmpegCmd.Process.Signal(syscall.SIGTERM)
						if err != nil {
							log.Printf("Error sending SIGTERM to FFmpeg: %v", err)
							_ = stream.FFmpegCmd.Process.Kill()
						}
						_, _ = stream.FFmpegCmd.Process.Wait()
					}

					if stream.OutputDir != "" && checkPathExists(stream.OutputDir) {
						_ = os.RemoveAll(stream.OutputDir)
					}

					delete(hlsStreams, sid)
					log.Printf("HLS stream for source %s fully cleaned up", sid)
				}
			}(sourceID)
		}
	}
}

// createFIFO creates a named pipe (DEPRECATED - use secureCreateFIFO instead)
// Kept for backward compatibility
func createFIFO(path string) error {
	// Log a warning about using the deprecated function
	log.Printf("Warning: Using deprecated createFIFO without path validation for %s", path)

	// Get HLS directory to validate path
	hlsBaseDir, err := getHLSDirectory()
	if err != nil {
		return fmt.Errorf("failed to get HLS directory for validation: %w", err)
	}

	// Use the secure version with validation
	return secureCreateFIFO(hlsBaseDir, path)
}
