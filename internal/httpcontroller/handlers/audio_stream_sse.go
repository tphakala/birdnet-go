package handlers

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// audioStreamSSEConnections tracks active SSE connections per client IP and source
var (
	audioStreamSSEConnections sync.Map // map[string]map[string]context.CancelFunc // clientIP -> sourceID -> cancelFunc
	audioStreamSSEMutex       sync.Mutex
	streamConnectionTimeout   = 120 * time.Second // longer timeout for audio streaming
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
	if err := h.checkDuplicateStreamConnection(clientIP, sourceID); err != nil {
		return err
	}

	// Always log new connections regardless of debug mode, like WebSocket does
	log.Printf("🔌 New SSE audio stream connection for source: %s from %s", sourceID, clientIP)

	// Setup cancelable context for this connection
	ctx, cancel := context.WithCancel(c.Request().Context())

	// Store cancel function for cleanup
	h.storeStreamConnection(clientIP, sourceID, cancel)

	// Cleanup connection on exit
	defer func() {
		cancel()
		h.removeStreamConnection(clientIP, sourceID)
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

// checkDuplicateStreamConnection checks if there's already a connection from this client to this source
func (h *Handlers) checkDuplicateStreamConnection(clientIP, sourceID string) error {
	audioStreamSSEMutex.Lock()
	defer audioStreamSSEMutex.Unlock()

	val, exists := audioStreamSSEConnections.Load(clientIP)
	if exists {
		sources, ok := val.(map[string]context.CancelFunc)
		if ok {
			if cancel, hasSource := sources[sourceID]; hasSource {
				// Cancel existing connection before creating a new one
				cancel()
				delete(sources, sourceID)
				if h.debug {
					log.Printf("AudioStreamSSE: Replaced existing connection from %s to source %s", clientIP, sourceID)
				}
			}
		}
	}
	return nil
}

// storeStreamConnection stores the cancel function for a client connection
func (h *Handlers) storeStreamConnection(clientIP, sourceID string, cancel context.CancelFunc) {
	audioStreamSSEMutex.Lock()
	defer audioStreamSSEMutex.Unlock()

	val, exists := audioStreamSSEConnections.Load(clientIP)
	var sources map[string]context.CancelFunc
	if !exists {
		sources = make(map[string]context.CancelFunc)
		audioStreamSSEConnections.Store(clientIP, sources)
	} else {
		sources = val.(map[string]context.CancelFunc)
	}
	sources[sourceID] = cancel
}

// removeStreamConnection removes a client connection
func (h *Handlers) removeStreamConnection(clientIP, sourceID string) {
	audioStreamSSEMutex.Lock()
	defer audioStreamSSEMutex.Unlock()

	val, exists := audioStreamSSEConnections.Load(clientIP)
	if exists {
		sources, ok := val.(map[string]context.CancelFunc)
		if ok {
			delete(sources, sourceID)
			// If no more sources for this client, remove the client entry
			if len(sources) == 0 {
				audioStreamSSEConnections.Delete(clientIP)
			}
		}
	}
}

// setupSSEStreamConnection initializes the SSE connection for audio streaming
func (h *Handlers) setupSSEStreamConnection(c echo.Context, clientIP, sourceID string) error {
	if h.debug {
		log.Printf("AudioStreamSSE: New connection from %s to source %s", clientIP, sourceID)
	}

	// Set up SSE headers
	initializeSSEStreamHeaders(c)
	return nil
}

// runSSEStreamLoop handles the main event loop for SSE audio streaming
func (h *Handlers) runSSEStreamLoop(ctx context.Context, c echo.Context, clientIP, sourceID string) error {
	// Start connection timeout timer
	timeout := time.NewTimer(streamConnectionTimeout)
	defer timeout.Stop()

	// Create tickers for heartbeat to keep connection alive
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()

	// Status ticker for connection health monitoring
	statusTicker := time.NewTicker(30 * time.Second)
	defer statusTicker.Stop()

	// Buffer to hold audio data - use a size that's a power of two for compatibility
	const targetBufferSize = 6144 // Maintain 3072 samples (6144 bytes) of 16-bit audio per chunk
	audioBuffer := make([]byte, 0, targetBufferSize)
	lastBatchTime := time.Now()
	lastDataReceived := time.Now()

	// Track connection health
	dataReceived := false
	dataSent := false

	// Setup audio stream callback
	audioChan := make(chan []byte, 20) // Buffer multiple audio chunks
	var audioCallback func(sourceID string, data []byte)
	audioCallback = func(callbackSourceID string, data []byte) {
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
	if err := h.sendSSEAudioMessage(c, nil, "connected"); err != nil {
		log.Printf("AudioStreamSSE: Error sending initial message: %v", err)
		return err
	}

	// Helper function to flush buffer regardless of size
	flushBuffer := func() error {
		if len(audioBuffer) > 0 {
			if err := h.sendSSEAudioMessage(c, audioBuffer, "audio"); err != nil {
				return err
			}
			audioBuffer = make([]byte, 0, targetBufferSize)
			lastBatchTime = time.Now()
			dataSent = true
		}
		return nil
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
			// Periodically log data flow to help with debugging
			if h.debug && time.Now().Second()%10 == 0 {
				log.Printf("📤 SSE audio stream sending %d bytes to %s for source %s", len(data), clientIP, sourceID)
			}

			// Aim for consistent buffer sizes to match client-side processing
			audioBuffer = append(audioBuffer, data...)

			// If we have enough data or it's been long enough, send it
			if len(audioBuffer) >= targetBufferSize || time.Since(lastBatchTime) > 100*time.Millisecond {
				if err := flushBuffer(); err != nil {
					log.Printf("AudioStreamSSE: Error sending audio update: %v", err)
					return err
				}
			}

		case <-heartbeat.C:
			// Force flush any pending data before heartbeat
			if err := flushBuffer(); err != nil {
				log.Printf("AudioStreamSSE: Error flushing buffer before heartbeat: %v", err)
				return err
			}

			// Send heartbeat
			if err := h.sendSSEHeartbeat(c); err != nil {
				log.Printf("AudioStreamSSE: Heartbeat error: %v", err)
				return err
			}

		case <-statusTicker.C:
			// Log connection health status
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

			// Reset tracking for next period
			dataReceived = false
			dataSent = false
		}
	}
}

// sendSSEHeartbeat sends a heartbeat message to keep the connection alive
func (h *Handlers) sendSSEHeartbeat(c echo.Context) error {
	// Send a comment as heartbeat
	if _, err := fmt.Fprintf(c.Response(), ": heartbeat %d\n\n", time.Now().Unix()); err != nil {
		return err
	}
	c.Response().Flush()
	return nil
}

// sendSSEAudioMessage sends audio data as base64 encoded string in SSE format
func (h *Handlers) sendSSEAudioMessage(c echo.Context, data []byte, eventType string) error {
	var msg string

	if data != nil && len(data) > 0 {
		// Convert binary data to base64
		encodedData := base64.StdEncoding.EncodeToString(data)
		msg = fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, encodedData)
	} else {
		// Just send an event with empty data for control messages
		msg = fmt.Sprintf("event: %s\ndata: \n\n", eventType)
	}

	if _, err := fmt.Fprint(c.Response(), msg); err != nil {
		return fmt.Errorf("error writing to client: %w", err)
	}

	c.Response().Flush()
	return nil
}
