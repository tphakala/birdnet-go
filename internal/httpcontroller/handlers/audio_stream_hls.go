package handlers

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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

	return hlsDir, nil
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
	sourceID := c.Param("sourceID")
	clientIP := c.RealIP()

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

	// Get or create HLS stream
	stream, err := getOrCreateHLSStream(sourceID)
	if err != nil {
		log.Printf("Error creating HLS stream: %v - source: %s, IP: %s", err, sourceID, clientIP)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to create audio stream")
	}

	// Update access time
	hlsStreamMutex.Lock()
	stream.LastAccess = time.Now()
	hlsStreamMutex.Unlock()

	// Determine what file is being requested
	requestPath := c.Param("*")

	// Record client activity when they request a segment
	// This gives us a more accurate view of active clients
	if strings.HasSuffix(requestPath, ".ts") {
		clientID := clientIP + "-" + c.Request().Header.Get("User-Agent")

		hlsClientActivityMutex.Lock()
		if _, exists := hlsClientActivity[sourceID]; !exists {
			hlsClientActivity[sourceID] = make(map[string]time.Time)
		}
		hlsClientActivity[sourceID][clientID] = time.Now()
		hlsClientActivityMutex.Unlock()
	}

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

	// Only log errors for missing files
	if (requestPath == "" || requestPath == "playlist.m3u8") && !checkFileExists(stream.PlaylistPath) {
		log.Printf("Error: HLS playlist file does not exist at %s", stream.PlaylistPath)
		return echo.NewHTTPError(http.StatusNotFound, "Playlist file not found")
	}

	// Check if segment file exists
	if requestPath != "" && requestPath != "playlist.m3u8" {
		segmentPath := filepath.Join(stream.OutputDir, requestPath)
		if !checkFileExists(segmentPath) {
			log.Printf("Error: HLS segment file does not exist at %s", segmentPath)
			return echo.NewHTTPError(http.StatusNotFound, "Segment file not found")
		}
	}

	if requestPath == "" || requestPath == "playlist.m3u8" {
		// Set proper content type for m3u8 playlist
		c.Response().Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		// Add cache control headers to prevent caching
		c.Response().Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		c.Response().Header().Set("Pragma", "no-cache")
		c.Response().Header().Set("Expires", "0")
		// Serve the main playlist
		return c.File(stream.PlaylistPath)
	}

	// For .ts segment files
	if strings.HasSuffix(requestPath, ".ts") {
		c.Response().Header().Set("Content-Type", "video/mp2t")
		// Allow caching of segments for a short time
		c.Response().Header().Set("Cache-Control", "public, max-age=60")
	}

	// Serve segment file
	return c.File(filepath.Join(stream.OutputDir, requestPath))
}

// Helper function to check if a file exists
func checkFileExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

// getOrCreateHLSStream gets an existing stream or creates a new one
func getOrCreateHLSStream(sourceID string) (*HLSStreamInfo, error) {
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

	// Get HLS directory
	hlsBaseDir, err := getHLSDirectory()
	if err != nil {
		return nil, fmt.Errorf("failed to get HLS directory: %w", err)
	}

	// Create stream-specific directory
	// Use a sanitized version of the sourceID for the directory name
	safeSourceID := strings.ReplaceAll(sourceID, ":", "_")
	safeSourceID = strings.ReplaceAll(safeSourceID, "/", "_")
	safeSourceID = strings.ReplaceAll(safeSourceID, "\\", "_")
	outputDir := filepath.Join(hlsBaseDir, fmt.Sprintf("stream_%s", safeSourceID))

	// Ensure the directory exists and is empty
	if checkPathExists(outputDir) {
		log.Printf("Removing existing output directory: %s", outputDir)
		if err := os.RemoveAll(outputDir); err != nil {
			return nil, fmt.Errorf("failed to clean HLS directory: %w", err)
		}
	}

	log.Printf("Creating new output directory: %s", outputDir)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create HLS directory: %w", err)
	}

	// Create playlist file path
	playlistPath := filepath.Join(outputDir, "playlist.m3u8")

	// Get FFmpeg path from settings
	ffmpegPath := conf.Setting().Realtime.Audio.FfmpegPath
	if ffmpegPath == "" {
		return nil, fmt.Errorf("ffmpeg not configured")
	}

	// Start FFmpeg process to read from FIFO and create HLS stream
	fifoPath := filepath.Join(outputDir, "audio.pcm")
	log.Printf("Creating FIFO for HLS stream: %s", fifoPath)
	if err := createFIFO(fifoPath); err != nil {
		os.RemoveAll(outputDir)
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
	}

	// Store stream in map
	hlsStreams[sourceID] = stream

	// Start goroutine to feed audio data to FFmpeg
	go feedAudioToFFmpeg(sourceID, fifoPath)

	return stream, nil
}

// createFIFO creates a named pipe
func createFIFO(path string) error {
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

// feedAudioToFFmpeg pumps audio data to the FFmpeg process
func feedAudioToFFmpeg(sourceID, fifoPath string) {
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

	// Create a done channel to signal termination
	done := make(chan struct{})

	// Start a goroutine to monitor stream activity
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// Check if stream is still active
				hlsStreamMutex.Lock()
				stream, exists := hlsStreams[sourceID]
				active := exists && time.Since(stream.LastAccess) < inactivityTimeout
				hlsStreamMutex.Unlock()

				if !active {
					log.Printf("Stream inactive, stopping audio feed for source %s", sourceID)
					close(done)
					return
				}
			case <-done:
				return
			}
		}
	}()

	// Use for-range loop for processing audio data
	for data := range audioChan {
		select {
		case <-done:
			return
		default:
			// Write data to FIFO
			if _, err := fifo.Write(data); err != nil {
				log.Printf("Error writing to FIFO: %v", err)
				close(done)
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

		// Stop FFmpeg process with a more reliable approach
		if stream.FFmpegCmd != nil && stream.FFmpegCmd.Process != nil {
			// Try SIGTERM first for graceful shutdown
			log.Printf("Sending SIGTERM to FFmpeg process for source: %s", sourceID)
			err := stream.FFmpegCmd.Process.Signal(syscall.SIGTERM)
			if err != nil {
				log.Printf("Error sending SIGTERM to FFmpeg: %v, falling back to Kill", err)
				// Fall back to Kill if SIGTERM fails
				_ = stream.FFmpegCmd.Process.Kill()
			}

			// Wait for process to complete to avoid zombies
			_, _ = stream.FFmpegCmd.Process.Wait()
			log.Printf("FFmpeg process for source %s terminated", sourceID)
		}

		// Clean up client tracking
		hlsStreamClientMutex.Lock()
		if clients, exists := hlsStreamClients[sourceID]; exists {
			log.Printf("Cleaning up %d client references for source: %s", len(clients), sourceID)
			delete(hlsStreamClients, sourceID)
		}
		hlsStreamClientMutex.Unlock()

		// Clean up directory
		if stream.OutputDir != "" && checkPathExists(stream.OutputDir) {
			log.Printf("Removing stream directory: %s", stream.OutputDir)
			if err := os.RemoveAll(stream.OutputDir); err != nil {
				log.Printf("Error removing stream directory: %v", err)
			}
		}

		// Remove from map
		delete(hlsStreams, sourceID)
		log.Printf("HLS stream for source %s fully cleaned up", sourceID)
	}
}

// Helper function to check if a path exists
func checkPathExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

// StartHLSStream explicitly starts an HLS stream for a source
// This is called when a client wants to start playing a stream
func (h *Handlers) StartHLSStream(c echo.Context, sourceID string) error {
	clientIP := c.RealIP()
	clientID := fmt.Sprintf("%s-%d", clientIP, time.Now().UnixNano())

	log.Printf("Client %s requested to start HLS stream for source: %s", clientID, sourceID)

	// Check if source exists
	if !myaudio.HasCaptureBuffer(sourceID) {
		return echo.NewHTTPError(http.StatusNotFound, "Audio source not found")
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
	stream, err := getOrCreateHLSStream(sourceID)
	if err != nil {
		log.Printf("Error creating HLS stream: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to start stream: %v", err))
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

	if !playlistReady {
		log.Printf("Warning: Playlist file not created after waiting: %s", stream.PlaylistPath)
	} else {
		log.Printf("Playlist file is ready: %s", stream.PlaylistPath)
	}

	return nil
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
