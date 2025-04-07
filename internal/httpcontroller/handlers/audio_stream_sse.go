package handlers

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// AudioStreamInfo provides information about the audio stream for client-side optimization
type AudioStreamInfo struct {
	SampleRate int    `json:"sampleRate"`
	BitDepth   int    `json:"bitDepth"`
	Channels   int    `json:"channels"`
	BufferSize int    `json:"bufferSize"`
	Transport  string `json:"transport"`
}

// AudioPacket contains audio data with metadata for packet ordering and timing
type AudioPacket struct {
	SequenceNumber uint32 `json:"seq"`
	Timestamp      int64  `json:"ts"` // Unix milliseconds
	Data           string `json:"data"`
}

// connectionManager manages active SSE connections with sharding to reduce contention
type connectionManager struct {
	// 16 shards to distribute connection management
	shards [16]struct {
		sync.Mutex
		connections map[string]map[string]context.CancelFunc // clientIP -> sourceID -> cancelFunc
	}
	// Global sequence counter for all audio packets
	sequenceCounter uint32
}

// getShard returns the appropriate shard for a client IP
func (m *connectionManager) getShard(clientIP string) *struct {
	sync.Mutex
	connections map[string]map[string]context.CancelFunc
} {
	sum := crc32.ChecksumIEEE([]byte(clientIP))
	return &m.shards[sum%16]
}

// storeConnection stores a client connection in the appropriate shard
func (m *connectionManager) storeConnection(clientIP, sourceID string, cancel context.CancelFunc) {
	shard := m.getShard(clientIP)
	shard.Lock()
	defer shard.Unlock()

	if shard.connections == nil {
		shard.connections = make(map[string]map[string]context.CancelFunc)
	}

	sources, exists := shard.connections[clientIP]
	if !exists {
		sources = make(map[string]context.CancelFunc)
		shard.connections[clientIP] = sources
	}

	sources[sourceID] = cancel
}

// removeConnection removes a client connection from the appropriate shard
func (m *connectionManager) removeConnection(clientIP, sourceID string) {
	shard := m.getShard(clientIP)
	shard.Lock()
	defer shard.Unlock()

	sources, exists := shard.connections[clientIP]
	if exists {
		delete(sources, sourceID)
		if len(sources) == 0 {
			delete(shard.connections, clientIP)
		}
	}
}

// checkDuplicateConnection checks if there's already a connection from this client to this source
// and cancels it if it exists
func (m *connectionManager) checkDuplicateConnection(clientIP, sourceID string, debug bool) {
	shard := m.getShard(clientIP)
	shard.Lock()
	defer shard.Unlock()

	if shard.connections == nil {
		shard.connections = make(map[string]map[string]context.CancelFunc)
		return
	}

	sources, exists := shard.connections[clientIP]
	if exists {
		if cancel, hasSource := sources[sourceID]; hasSource {
			// Cancel existing connection before creating a new one
			cancel()
			delete(sources, sourceID)
			if debug {
				log.Printf("AudioStreamSSE: Replaced existing connection from %s to source %s", clientIP, sourceID)
			}
		}
	}
}

// getNextSequence atomically increments and returns the next sequence number
func (m *connectionManager) getNextSequence() uint32 {
	return atomic.AddUint32(&m.sequenceCounter, 1)
}

var (
	// connectionMgr handles all SSE connections with reduced contention
	connectionMgr connectionManager

	// streamConnectionTimeout is the maximum time to keep a connection open
	streamConnectionTimeout = 120 * time.Second

	// Buffer pool for reusing audio buffers
	audioBufferPool = sync.Pool{
		New: func() interface{} {
			buf := make([]byte, 0, 6144) // 6KB buffer size (3072 samples of 16-bit audio)
			return &buf
		},
	}
)

// initializeSSEStreamHeaders sets up the necessary headers for SSE audio stream connection
func initializeSSEStreamHeaders(c echo.Context) {
	c.Response().Header().Set(echo.HeaderContentType, "text/event-stream")
	c.Response().Header().Set(echo.HeaderCacheControl, "no-cache")
	c.Response().Header().Set(echo.HeaderConnection, "keep-alive")
	c.Response().WriteHeader(http.StatusOK)
}

// AudioStreamSSE handles Server-Sent Events for audio streaming
// API: GET /api/v1/audio-stream/:sourceID
func (h *Handlers) AudioStreamSSE(c echo.Context) error {
	sourceID := c.Param("sourceID")
	clientIP := c.RealIP()

	if sourceID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Source ID is required")
	}

	// Check if source exists and has a valid capture buffer
	if !myaudio.HasCaptureBuffer(sourceID) {
		return echo.NewHTTPError(http.StatusNotFound, "Audio source not found")
	}

	// Check if the user is authorized
	if !h.Server.IsAccessAllowed(c) {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required for audio streaming")
	}

	// Check for existing connection from this client for this source
	connectionMgr.checkDuplicateConnection(clientIP, sourceID, h.debug)

	// Always log new connections regardless of debug mode, like WebSocket does
	log.Printf("🔌 New SSE audio stream connection for source: %s from %s", sourceID, clientIP)

	// Setup cancelable context for this connection
	ctx, cancel := context.WithCancel(c.Request().Context())

	// Store cancel function for cleanup
	connectionMgr.storeConnection(clientIP, sourceID, cancel)

	// Cleanup connection on exit
	defer func() {
		cancel()
		connectionMgr.removeConnection(clientIP, sourceID)
		// Always log disconnections regardless of debug mode
		log.Printf("🔌 SSE audio stream client %s disconnected from source %s", clientIP, sourceID)
	}()

	// Set up connection
	if err := h.setupSSEStreamConnection(c, clientIP, sourceID); err != nil {
		return err
	}

	// Run the SSE stream loop
	return h.runSSEStreamLoop(ctx, c, clientIP, sourceID)
}

// setupSSEStreamConnection initializes the SSE connection for audio streaming
func (h *Handlers) setupSSEStreamConnection(c echo.Context, clientIP, sourceID string) error {
	if h.debug {
		log.Printf("AudioStreamSSE: New connection from %s to source %s", clientIP, sourceID)
	}

	// Set up SSE headers
	initializeSSEStreamHeaders(c)

	// Send initial audio info message for adaptive streaming
	audioInfo := AudioStreamInfo{
		SampleRate: 48000, // Default sample rate, could be fetched from audio source
		BitDepth:   16,    // 16-bit audio
		Channels:   1,     // Mono
		BufferSize: 3072,  // Target buffer size in samples (matches server's incomingChunkSize)
		Transport:  "sse", // Transport type
	}

	// Send the audio info as the first message
	infoJSON, _ := json.Marshal(audioInfo)
	initMessage := fmt.Sprintf("event: info\ndata: %s\n\n", infoJSON)
	if _, err := c.Response().Write([]byte(initMessage)); err != nil {
		return fmt.Errorf("error sending initial stream info: %w", err)
	}

	// Flush to ensure headers and initial data are sent immediately
	c.Response().Flush()

	return nil
}

// processAudioData handles incoming audio data, buffering and sending it
func (h *Handlers) processAudioData(c echo.Context, data []byte, audioBuffer []byte, lastBatchTime time.Time,
	clientIP, sourceID string, targetBufferSize int, writer *bufio.Writer) ([]byte, time.Time, error) {

	// Periodically log data flow to help with debugging
	if h.debug && time.Now().Second()%10 == 0 {
		log.Printf("📤 SSE audio stream sending %d bytes to %s for source %s", len(data), clientIP, sourceID)
	}

	// Aim for consistent buffer sizes to match client-side processing
	audioBuffer = append(audioBuffer, data...)

	// If we have enough data or it's been long enough, send it
	if len(audioBuffer) >= targetBufferSize || time.Since(lastBatchTime) > 100*time.Millisecond {
		if err := h.sendSSEAudioData(c, audioBuffer, writer); err != nil {
			return audioBuffer, lastBatchTime, err
		}

		// Return current buffer to pool
		poolBuf := audioBuffer
		audioBufferPool.Put(&poolBuf)

		// Get fresh buffer from pool
		newBuf := *(audioBufferPool.Get().(*[]byte))
		audioBuffer = newBuf[:0] // Reset length but preserve capacity

		lastBatchTime = time.Now()
	}

	return audioBuffer, lastBatchTime, nil
}

// logConnectionHealth logs the health status of the SSE connection
func (h *Handlers) logConnectionHealth(dataReceived, dataSent bool, lastDataReceived time.Time, clientIP, sourceID string) {
	switch {
	case !dataReceived:
		log.Printf("⚠️ SSE audio stream for %s to %s: No data received in last 30s", clientIP, sourceID)
	case !dataSent:
		log.Printf("⚠️ SSE audio stream for %s to %s: No data sent in last 30s", clientIP, sourceID)
	default:
		dataAge := time.Since(lastDataReceived)
		log.Printf("ℹ️ SSE audio stream for %s to %s: healthy, last data %.1fs ago",
			clientIP, sourceID, dataAge.Seconds())
	}
}

// runSSEStreamLoop handles the main event loop for SSE audio streaming
func (h *Handlers) runSSEStreamLoop(ctx context.Context, c echo.Context, clientIP, sourceID string) error {
	// Start connection timeout timer
	timeout := time.NewTimer(streamConnectionTimeout)
	defer timeout.Stop()

	// Create tickers for heartbeat to keep connection alive
	heartbeat := time.NewTicker(15 * time.Second) // Increased from 10s to 15s
	defer heartbeat.Stop()

	// Status ticker for connection health monitoring
	statusTicker := time.NewTicker(30 * time.Second)
	defer statusTicker.Stop()

	// Buffer to hold audio data - use a size that's a power of two for compatibility
	const targetBufferSize = 6144 // Maintain 3072 samples (6144 bytes) of 16-bit audio per chunk

	// Get buffer from pool
	poolBuf := *(audioBufferPool.Get().(*[]byte))
	audioBuffer := poolBuf[:0]              // Reset length but preserve capacity
	defer audioBufferPool.Put(&audioBuffer) // Ensure buffer is returned to pool on exit

	lastBatchTime := time.Now()
	lastDataReceived := time.Now()

	// Create a buffered writer for more efficient writes
	writer := bufio.NewWriterSize(c.Response().Writer, 16384) // 16KB buffer for writes
	defer writer.Flush()                                      // Ensure all data is written on exit

	// Track connection health
	dataReceived := false
	dataSent := false

	// Setup audio stream callback
	audioChan := make(chan []byte, 50) // Increased from 20 to 50 for high sample rate audio
	audioCallback := func(callbackSourceID string, data []byte) {
		if callbackSourceID == sourceID {
			lastDataReceived = time.Now()
			dataReceived = true

			select {
			case audioChan <- data:
				// Data sent successfully
			default:
				// Channel is full, clear the channel
				select {
				case <-audioChan:
					// Removed oldest data to make room
					audioChan <- data
				default:
					// Channel emptied concurrently, try again
					select {
					case audioChan <- data:
						// Data sent on second attempt
					default:
						// Still can't send, drop the data
					}
				}
			}
		}
	}

	// Register callback to receive audio data
	myaudio.RegisterBroadcastCallback(sourceID, audioCallback)
	defer myaudio.UnregisterBroadcastCallback(sourceID)

	if h.debug {
		log.Printf("💾 SSE audio stream registered callback for source %s", sourceID)
	}

	// Send initial message to establish connection
	if err := h.sendSSEConnectedMessage(c, writer); err != nil {
		log.Printf("AudioStreamSSE: Error sending initial message: %v", err)
		return err
	}

	// Helper function to flush buffer regardless of size
	flushBuffer := func() error {
		if len(audioBuffer) > 0 {
			if err := h.sendSSEAudioData(c, audioBuffer, writer); err != nil {
				return err
			}

			// Clear audioBuffer without allocating new memory
			audioBuffer = audioBuffer[:0]
			lastBatchTime = time.Now()
			dataSent = true
		}

		// Flush the writer to ensure data is sent
		return writer.Flush()
	}

	for {
		select {
		case <-timeout.C:
			if h.debug {
				log.Printf("AudioStreamSSE: Connection timeout for %s to source %s", clientIP, sourceID)
			}
			return nil

		case <-ctx.Done():
			if h.debug {
				log.Printf("AudioStreamSSE: Client disconnected: %s from source %s", clientIP, sourceID)
			}
			return nil

		case data := <-audioChan:
			var err error
			audioBuffer, lastBatchTime, err = h.processAudioData(c, data, audioBuffer, lastBatchTime, clientIP, sourceID, targetBufferSize, writer)
			if err != nil {
				log.Printf("AudioStreamSSE: Error sending audio update: %v", err)
				return err
			}
			dataSent = lastBatchTime.After(time.Now().Add(-30 * time.Second))

		case <-heartbeat.C:
			// Force flush any pending data before heartbeat
			if err := flushBuffer(); err != nil {
				log.Printf("AudioStreamSSE: Error flushing buffer before heartbeat: %v", err)
				return err
			}

			// Send heartbeat
			if err := h.sendSSEHeartbeat(c, writer); err != nil {
				log.Printf("AudioStreamSSE: Heartbeat error: %v", err)
				return err
			}

		case <-statusTicker.C:
			// Log connection health status
			h.logConnectionHealth(dataReceived, dataSent, lastDataReceived, clientIP, sourceID)

			// Reset tracking for next period
			dataReceived = false
			dataSent = false
		}
	}
}

// sendSSEHeartbeat sends a heartbeat message to keep the connection alive
func (h *Handlers) sendSSEHeartbeat(c echo.Context, writer *bufio.Writer) error {
	// Send a comment as heartbeat with current timestamp
	if _, err := fmt.Fprintf(writer, ": heartbeat %d\n\n", time.Now().Unix()); err != nil {
		return err
	}
	return writer.Flush()
}

// sendSSEConnectedMessage sends a connected event to establish the connection
func (h *Handlers) sendSSEConnectedMessage(c echo.Context, writer *bufio.Writer) error {
	msg := "event: connected\ndata: \n\n"
	if _, err := fmt.Fprint(writer, msg); err != nil {
		return fmt.Errorf("error writing connected message: %w", err)
	}
	return writer.Flush()
}

// sendSSEAudioData sends audio data as part of an audio packet with sequence and timestamp
func (h *Handlers) sendSSEAudioData(c echo.Context, data []byte, writer *bufio.Writer) error {
	if len(data) == 0 {
		return nil
	}

	// Convert binary data to base64 using more efficient RawURLEncoding
	encodedData := base64.RawURLEncoding.EncodeToString(data)

	// Create packet with metadata
	packet := AudioPacket{
		SequenceNumber: connectionMgr.getNextSequence(),
		Timestamp:      time.Now().UnixNano() / 1000000, // Convert to milliseconds
		Data:           encodedData,
	}

	// Marshal to JSON
	packetJSON, err := json.Marshal(packet)
	if err != nil {
		return fmt.Errorf("error marshaling audio packet: %w", err)
	}

	// Format as SSE message
	msg := fmt.Sprintf("event: audio\ndata: %s\n\n", packetJSON)

	if _, err := fmt.Fprint(writer, msg); err != nil {
		return fmt.Errorf("error writing to client: %w", err)
	}

	return nil
}
