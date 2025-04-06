package handlers

import (
	"bytes"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/security"
)

// Websocket close codes
const (
	CloseNormalClosure       = 1000
	CloseGoingAway           = 1001
	CloseProtocolError       = 1002
	CloseUnsupportedData     = 1003
	CloseNoStatusReceived    = 1005
	CloseAbnormalClosure     = 1006
	CloseInvalidFramePayload = 1007
	ClosePolicyViolation     = 1008
	CloseMessageTooBig       = 1009
	CloseMandatoryExtension  = 1010
	CloseInternalServerErr   = 1011
	CloseServiceRestart      = 1012
	CloseTryAgainLater       = 1013
)

// AudioStreamManager manages WebSocket connections for audio streaming
type AudioStreamManager struct {
	clients      map[string]map[*websocket.Conn]bool
	clientsMutex sync.RWMutex
	upgrader     websocket.Upgrader
	debug        bool
	// Add buffer maps per source
	audioBuffers map[string]*bytes.Buffer
	bufferMutex  sync.Mutex
	// Configuration
	bufferSize    int  // Bytes to accumulate before sending
	forceInterval bool // Whether to force sending on interval
	// Connection quality tracking
	flushIntervals     map[string]time.Duration   // Flush interval per source
	connectionLatency  map[string]time.Duration   // Connection latency per source
	latencyHistory     map[string][]time.Duration // History of latency measurements
	flushIntervalMutex sync.RWMutex
	// Flush interval bounds
	minFlushInterval   time.Duration
	maxFlushInterval   time.Duration
	adaptiveFlush      bool // Whether to use adaptive flush intervals
	stabilityThreshold int  // Number of consistent readings required before adjustment
	// Connection mutex map to prevent concurrent writes to the same connection
	connectionMutexes      map[*websocket.Conn]*sync.Mutex
	connectionMutexesMutex sync.RWMutex
}

// NewAudioStreamManager creates a new audio stream manager
func NewAudioStreamManager(webServerDebug bool, securityHost string) *AudioStreamManager {
	return &AudioStreamManager{
		clients: make(map[string]map[*websocket.Conn]bool),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,  // Increased from 1024
			WriteBufferSize: 16384, // Increased from 1024
			// Set a longer handshake timeout for Cloudflare Tunnel
			HandshakeTimeout: 30 * time.Second,
			CheckOrigin: func(r *http.Request) bool {
				// For Cloudflare Tunnel, we need to be more permissive with origin checks
				origin := r.Header.Get("Origin")
				// Log all connection attempts with origin information
				if webServerDebug {
					log.Printf("🌐 WebSocket origin check: %s connecting from %s, CF-Connecting-IP: %s",
						origin, r.RemoteAddr, r.Header.Get("CF-Connecting-IP"))
				}

				// Always allow connections with Cloudflare headers
				if r.Header.Get("CF-Connecting-IP") != "" {
					if webServerDebug {
						log.Printf("🌐 WebSocket allowing Cloudflare proxied connection")
					}
					return true
				}

				// Allow same-site requests
				if origin == "" {
					return true // Same-site requests might not have Origin header
				}

				// If host is configured, validate origin against it
				if securityHost != "" {
					// Simple check to confirm the origin contains our host
					// More robust CORS checking should be applied via Echo middleware
					return strings.Contains(origin, securityHost)
				}

				// No host configured, allow all origins
				// This is only safe if Echo middleware is handling security
				return true
			},
			// Add custom response headers for Cloudflare Tunnel WebSocket
			EnableCompression: true,
		},
		debug:              webServerDebug, // Use the debug setting from web server config
		audioBuffers:       make(map[string]*bytes.Buffer),
		bufferSize:         12288, // Reduced from 32768 to 12288 (smaller chunks to prevent client buffer overflow)
		forceInterval:      true,  // Force sending buffers periodically even if not full
		flushIntervals:     make(map[string]time.Duration),
		connectionLatency:  make(map[string]time.Duration),
		latencyHistory:     make(map[string][]time.Duration),      // Track recent latency readings
		minFlushInterval:   10 * time.Millisecond,                 // Minimum flush interval
		maxFlushInterval:   50 * time.Millisecond,                 // Maximum flush interval
		adaptiveFlush:      true,                                  // Enable adaptive flush intervals
		stabilityThreshold: 3,                                     // Require 3 consistent readings before adjustment
		connectionMutexes:  make(map[*websocket.Conn]*sync.Mutex), // Initialize the connection mutex map
	}
}

// isAudioSourceValid checks if the source exists and has a valid capture buffer
func (asm *AudioStreamManager) isAudioSourceValid(sourceID, clientIP string) bool {
	exists := myaudio.HasCaptureBuffer(sourceID)
	if !exists && asm.debug {
		log.Printf("⚠️ WebSocket connection attempt for non-existent source %s from %s", sourceID, clientIP)
	}
	return exists
}

// isLocalNetworkConnection checks if the request is from a local network
func isLocalNetworkConnection(clientIP net.IP, debugLog bool) bool {
	isLocal := security.IsInLocalSubnet(clientIP)
	if debugLog {
		log.Printf("🔐 WebSocket local network check: IP=%s, isLocal=%v", clientIP, isLocal)
	}
	return isLocal
}

// HandleWebSocket upgrades an HTTP connection to WebSocket and handles the connection
func (asm *AudioStreamManager) HandleWebSocket(c echo.Context) error {
	sourceID := c.Param("sourceID")
	if sourceID == "" {
		if asm.debug {
			log.Printf("⚠️ WebSocket connection attempt without source ID from %s", c.RealIP())
		}
		return echo.NewHTTPError(http.StatusBadRequest, "Source ID is required")
	}

	// Handle Cloudflare specific headers
	clientIPString := c.RealIP()
	cfIP := c.Request().Header.Get("CF-Connecting-IP")
	if cfIP != "" {
		clientIPString = cfIP
		if asm.debug {
			log.Printf("🌐 Using Cloudflare-provided IP: %s", clientIPString)
		}
	}

	// Check if the source exists and has a valid capture buffer
	if !asm.isAudioSourceValid(sourceID, clientIPString) {
		return echo.NewHTTPError(http.StatusNotFound, "Audio source not found")
	}

	// Initialize authentication variables
	var isAuthenticated bool = false
	var authEnabled bool = false

	// Check if request is from local network using the security package
	clientIP := net.ParseIP(clientIPString)
	isLocalNetwork := isLocalNetworkConnection(clientIP, asm.debug)

	// Try to get server from context for authentication check
	server := c.Get("server")
	if server == nil {
		// Always log this error since it's critical for authentication
		log.Printf("⚠️ Server context not available for WebSocket authentication from %s for source %s", c.RealIP(), sourceID)

		// If server context is missing but client is from local network, allow the connection
		if isLocalNetwork {
			if asm.debug {
				log.Printf("🔐 WebSocket connection allowed from local network without server context: %s", c.RealIP())
			}
			isAuthenticated = true
		} else {
			// For non-local connections, fall back to a safe default: require authentication
			authEnabled = true
			isAuthenticated = false
		}
	} else {
		// Server context is available, try multiple ways to perform authentication check
		authenticated := false
		authEnabled := true

		// Try to use the interface directly
		if s, ok := server.(interface {
			IsAccessAllowed(c echo.Context) bool
			isAuthenticationEnabled(c echo.Context) bool
		}); ok {
			authEnabled = s.isAuthenticationEnabled(c)
			isAuthenticated = !authEnabled || s.IsAccessAllowed(c)
			authenticated = true
		}

		// If the interface assertion failed, try with reflection or specific type assertion
		if !authenticated {
			// Try to use the specific Server type if available
			switch s := server.(type) {
			case interface{ IsAccessAllowed(c echo.Context) bool }:
				isAuthenticated = s.IsAccessAllowed(c)
				authenticated = true
				log.Printf("🔐 WebSocket auth: using IsAccessAllowed method only for %s", c.RealIP())
			default:
				// Unable to use the interface
				log.Printf("⚠️ WebSocket auth check: unable to use server interface from %s for source %s - server type: %T",
					c.RealIP(), sourceID, server)
			}
		}

		// Always log authentication status for debugging
		log.Printf("🔐 WebSocket auth check: enabled=%v, authenticated=%v, IP=%s, source=%s",
			authEnabled, isAuthenticated, c.RealIP(), sourceID)
	}

	// Always allow local network connections
	if isLocalNetwork {
		if asm.debug {
			log.Printf("🔐 WebSocket connection allowed from local network: %s", c.RealIP())
		}
		isAuthenticated = true
	}

	// Check authorization before attempting to upgrade
	if authEnabled && !isAuthenticated {
		// Always log unauthorized attempts regardless of debug mode
		log.Printf("⛔ Unauthorized WebSocket attempt from %s for source %s", c.RealIP(), sourceID)
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required for audio streaming")
	}

	// Upgrade the connection to WebSocket
	ws, err := asm.upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		// Always log upgrade failures regardless of debug mode
		log.Printf("❌ WebSocket upgrade failed from %s for source %s: %v", c.RealIP(), sourceID, err)
		return err
	}

	// Set write deadline to ensure timely delivery
	if err := ws.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		log.Printf("❌ Failed to set write deadline: %v", err)
		ws.Close()
		return err
	}

	// Always log new connections regardless of debug mode
	log.Printf("🔌 New WebSocket connection for source: %s from %s", sourceID, c.RealIP())

	// Register the client
	asm.clientsMutex.Lock()
	firstClient := false
	if _, exists := asm.clients[sourceID]; !exists {
		asm.clients[sourceID] = make(map[*websocket.Conn]bool)
		firstClient = true
	}
	asm.clients[sourceID][ws] = true
	clientCount := len(asm.clients[sourceID])
	asm.clientsMutex.Unlock()

	// Initialize buffer for this source if needed
	asm.bufferMutex.Lock()
	if _, exists := asm.audioBuffers[sourceID]; !exists {
		asm.audioBuffers[sourceID] = bytes.NewBuffer(make([]byte, 0, asm.bufferSize*2))
	}
	asm.bufferMutex.Unlock()

	// Register the callback for this source if this is the first client
	if firstClient {
		// Always log callback registration regardless of debug mode
		log.Printf("🔄 Registering audio broadcast callback for source: %s", sourceID)
		myaudio.RegisterBroadcastCallback(sourceID, func(callbackSourceID string, data []byte) {
			asm.BroadcastAudio(callbackSourceID, data)
		})

		// Start a goroutine to flush buffers periodically if enabled
		if asm.forceInterval {
			go asm.startPeriodicFlush(sourceID)
		}
	}

	if asm.debug {
		log.Printf("👥 Client count for source %s: %d", sourceID, clientCount)
	}

	// Start the client handler
	go asm.handleClient(ws, sourceID)

	return nil
}

// startPeriodicFlush ensures audio buffers are flushed periodically to avoid latency
func (asm *AudioStreamManager) startPeriodicFlush(sourceID string) {
	// Set initial flush interval
	asm.flushIntervalMutex.Lock()
	if _, exists := asm.flushIntervals[sourceID]; !exists {
		asm.flushIntervals[sourceID] = 20 * time.Millisecond // Default starting interval
	}
	flushInterval := asm.flushIntervals[sourceID]
	asm.flushIntervalMutex.Unlock()

	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	adjustmentTicker := time.NewTicker(1 * time.Second) // Adjust interval every second
	defer adjustmentTicker.Stop()

	for {
		select {
		case <-ticker.C:
			// Stop if no clients are connected
			if !asm.HasActiveClients(sourceID) {
				return
			}

			asm.flushBufferIfNeeded(sourceID, true)

		case <-adjustmentTicker.C:
			// Skip adjustment if adaptive flush is disabled
			if !asm.adaptiveFlush {
				continue
			}

			// Stop if no clients are connected
			if !asm.HasActiveClients(sourceID) {
				return
			}

			// Adjust flush interval based on connection quality
			asm.adjustFlushInterval(sourceID)

			// Update the ticker with the new interval
			asm.flushIntervalMutex.RLock()
			newInterval := asm.flushIntervals[sourceID]
			asm.flushIntervalMutex.RUnlock()

			if newInterval != flushInterval {
				ticker.Stop()
				ticker = time.NewTicker(newInterval)
				flushInterval = newInterval

				if asm.debug {
					log.Printf("🔄 Adjusted flush interval for source %s: %v", sourceID, newInterval)
				}
			}
		}
	}
}

// adjustFlushInterval modifies the flush interval based on connection quality
func (asm *AudioStreamManager) adjustFlushInterval(sourceID string) {
	asm.flushIntervalMutex.Lock()
	defer asm.flushIntervalMutex.Unlock()

	// Get current latency and flush interval
	latency, hasLatency := asm.connectionLatency[sourceID]
	currentInterval := asm.flushIntervals[sourceID]
	if !hasLatency {
		// No latency data yet, use default
		return
	}

	// Add current latency to history
	if _, exists := asm.latencyHistory[sourceID]; !exists {
		asm.latencyHistory[sourceID] = make([]time.Duration, 0, asm.stabilityThreshold)
	}

	// Add latency to history, keeping only the last N measurements
	asm.latencyHistory[sourceID] = append(asm.latencyHistory[sourceID], latency)
	if len(asm.latencyHistory[sourceID]) > asm.stabilityThreshold {
		asm.latencyHistory[sourceID] = asm.latencyHistory[sourceID][1:] // Remove oldest measurement
	}

	// Only adjust if we have enough history
	if len(asm.latencyHistory[sourceID]) < asm.stabilityThreshold {
		return
	}

	// Check if all measurements are in the same range
	allLow := true
	allHigh := true

	for _, l := range asm.latencyHistory[sourceID] {
		if l >= 50*time.Millisecond {
			allLow = false
		}
		if l <= 100*time.Millisecond {
			allHigh = false
		}
	}

	// Use switch instead of if-else chain for better readability
	var newInterval time.Duration
	switch {
	case allLow:
		// Good connection consistently, decrease interval by 2ms (more frequent flushes)
		newInterval = currentInterval - 2*time.Millisecond
		if newInterval < asm.minFlushInterval {
			newInterval = asm.minFlushInterval
		}
	case allHigh:
		// Poor connection consistently, increase interval by 5ms (less frequent flushes)
		newInterval = currentInterval + 5*time.Millisecond
		if newInterval > asm.maxFlushInterval {
			newInterval = asm.maxFlushInterval
		}
	default:
		// Mixed/inconclusive measurements, keep current interval
		return
	}

	// Update the interval only if it changed
	if newInterval != currentInterval {
		asm.flushIntervals[sourceID] = newInterval
		// Reset history after adjustment to prevent immediate readjustment
		asm.latencyHistory[sourceID] = asm.latencyHistory[sourceID][:0]
	}
}

// flushBufferIfNeeded sends accumulated audio data to clients
func (asm *AudioStreamManager) flushBufferIfNeeded(sourceID string, force bool) {
	asm.bufferMutex.Lock()
	defer asm.bufferMutex.Unlock()

	buf, exists := asm.audioBuffers[sourceID]
	if !exists || buf.Len() == 0 {
		return
	}

	// Only send if buffer has reached target size or forced flush
	if force || buf.Len() >= asm.bufferSize {
		// Check if buffer has at least a minimum size before sending
		// to avoid sending tiny fragments that cause jitter
		if buf.Len() < 1024 && !force {
			return // Don't send too small chunks unless forced
		}

		// Ensure we don't send too much data at once
		// This helps prevent client buffer overflow
		sendSize := buf.Len()
		if sendSize > asm.bufferSize {
			sendSize = asm.bufferSize
		}

		// Extract the data
		data := make([]byte, sendSize)
		copy(data, buf.Bytes()[:sendSize])

		// Remove the sent data from buffer
		remainingData := buf.Bytes()[sendSize:]
		buf.Reset()
		if len(remainingData) > 0 {
			buf.Write(remainingData)
		}

		// Send the data in a goroutine to avoid blocking
		go asm.sendToClients(sourceID, data)
	}
}

// getConnectionMutex returns a mutex for the specified connection
func (asm *AudioStreamManager) getConnectionMutex(conn *websocket.Conn) *sync.Mutex {
	asm.connectionMutexesMutex.RLock()
	mutex, exists := asm.connectionMutexes[conn]
	asm.connectionMutexesMutex.RUnlock()

	if !exists {
		// Create a new mutex if one doesn't exist
		mutex = &sync.Mutex{}
		asm.connectionMutexesMutex.Lock()
		asm.connectionMutexes[conn] = mutex
		asm.connectionMutexesMutex.Unlock()
	}

	return mutex
}

// removeConnectionMutex removes the mutex for a closed connection
func (asm *AudioStreamManager) removeConnectionMutex(conn *websocket.Conn) {
	asm.connectionMutexesMutex.Lock()
	delete(asm.connectionMutexes, conn)
	asm.connectionMutexesMutex.Unlock()
}

// sendToClients sends data to all clients for a source
func (asm *AudioStreamManager) sendToClients(sourceID string, data []byte) {
	// Skip if no data or too small
	if len(data) < 32 {
		return
	}

	// Create a copy of the client map to avoid holding the lock during sends
	asm.clientsMutex.RLock()
	clients, exists := asm.clients[sourceID]
	if !exists || len(clients) == 0 {
		asm.clientsMutex.RUnlock()
		return
	}

	clientsCopy := make([]*websocket.Conn, 0, len(clients))
	for client := range clients {
		clientsCopy = append(clientsCopy, client)
	}
	asm.clientsMutex.RUnlock()

	// Send the audio data to each client
	for _, client := range clientsCopy {
		// Process each client in a separate function to correctly scope the defer
		func(client *websocket.Conn) {
			// Get the connection mutex for this client
			connMutex := asm.getConnectionMutex(client)

			// Lock the mutex to prevent concurrent writes to this connection
			connMutex.Lock()
			defer connMutex.Unlock()

			if err := client.SetWriteDeadline(time.Now().Add(1 * time.Second)); err != nil {
				if asm.debug {
					log.Printf("❌ Error setting write deadline for client %s: %v", client.RemoteAddr().String(), err)
				}
				// Remove the client if we can't set deadline
				asm.clientsMutex.Lock()
				sourceClients, exists := asm.clients[sourceID]
				if exists {
					delete(sourceClients, client)
				}
				asm.clientsMutex.Unlock()
				client.Close()
				return
			}

			err := client.WriteMessage(websocket.BinaryMessage, data)
			if err != nil {
				if asm.debug {
					log.Printf("❌ Error sending audio data to client %s: %v", client.RemoteAddr().String(), err)
				}
				// Remove the client if we can't send to it
				asm.clientsMutex.Lock()
				sourceClients, exists := asm.clients[sourceID]
				if exists {
					delete(sourceClients, client)
				}
				asm.clientsMutex.Unlock()
				client.Close()
			}
		}(client)
	}
}

// handleClient manages a single WebSocket client connection
func (asm *AudioStreamManager) handleClient(ws *websocket.Conn, sourceID string) {
	// Clean up on exit
	defer asm.cleanupClientConnection(ws, sourceID)

	// Set up connection
	if err := asm.setupClientConnection(ws, sourceID); err != nil {
		return
	}

	// Handle messages
	asm.handleClientMessages(ws, sourceID)
}

// setupClientConnection initializes the WebSocket connection for a client
func (asm *AudioStreamManager) setupClientConnection(ws *websocket.Conn, sourceID string) error {
	// Set a maximum message size limit to prevent memory exhaustion attacks
	const maxMessageSize = 64 * 1024 // 64KB, more than enough for control messages
	ws.SetReadLimit(maxMessageSize)

	// Set larger ping/pong timeout
	if err := ws.SetReadDeadline(time.Time{}); err != nil {
		log.Printf("❌ Failed to set read deadline: %v", err)
		return err
	}

	// Configure pong handler for latency tracking
	lastPingTime := time.Now()
	ws.SetPongHandler(func(string) error {
		if err := ws.SetReadDeadline(time.Time{}); err != nil {
			log.Printf("❌ Failed to reset read deadline on pong: %v", err)
			return err
		}

		// Calculate round-trip time and update connection latency
		if asm.adaptiveFlush {
			latency := time.Since(lastPingTime)
			asm.flushIntervalMutex.Lock()
			asm.connectionLatency[sourceID] = latency
			asm.flushIntervalMutex.Unlock()

			if asm.debug && time.Now().Second()%10 == 0 { // Log once every 10 seconds
				log.Printf("📊 WebSocket ping latency for source %s: %v", sourceID, latency)
			}
		}

		return nil
	})

	// Send an initial ping to check connection
	if err := ws.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
		log.Printf("❌ Failed to send initial ping: %v", err)
		return err
	}

	return nil
}

// handleClientMessages processes incoming WebSocket messages
func (asm *AudioStreamManager) handleClientMessages(ws *websocket.Conn, sourceID string) {
	// Start periodic pings for latency measurement and keeping connection alive through Cloudflare
	// Cloudflare Tunnel has a 100-second timeout, so we ping more frequently to keep the connection alive
	pingTicker := time.NewTicker(25 * time.Second)
	defer pingTicker.Stop()

	// Add a keepalive ping that's more frequent for Cloudflare Tunnel
	keepAliveTicker := time.NewTicker(5 * time.Second)
	defer keepAliveTicker.Stop()

	// Message handling loop
	for {
		select {
		case <-pingTicker.C:
			// Send ping for latency measurement
			err := ws.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
			if err != nil {
				if asm.debug {
					log.Printf("❌ Failed to send measurement ping: %v", err)
				}
				return
			}

		case <-keepAliveTicker.C:
			// Send a small text message as keepalive (helps with Cloudflare Tunnel)
			err := ws.WriteMessage(websocket.TextMessage, []byte("keepalive"))
			if err != nil {
				if asm.debug {
					log.Printf("❌ Failed to send keepalive: %v", err)
				}
				return
			}

		default:
			// Process a single message with timeout handling for Cloudflare Tunnel
			if !asm.processNextMessage(ws, sourceID) {
				return // Connection closed or error
			}

			// Small sleep to avoid CPU spinning
			time.Sleep(time.Millisecond * 10)
		}
	}
}

// processNextMessage handles a single WebSocket message
// Returns false if the connection should be closed
func (asm *AudioStreamManager) processNextMessage(ws *websocket.Conn, sourceID string) bool {
	// Set deadline for reading the next message - longer timeout for Cloudflare Tunnel
	if err := ws.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		log.Printf("❌ Failed to set read deadline: %v", err)
		return false
	}

	// Read message
	msgType, msg, err := ws.ReadMessage()
	if err != nil {
		// Use a switch to categorize the error type instead of if-else chain
		switch {
		case websocket.IsCloseError(err,
			websocket.CloseNormalClosure,
			websocket.CloseGoingAway,
			websocket.CloseNoStatusReceived):
			// Normal closure - don't treat as error
			if asm.debug {
				log.Printf("📤 WebSocket closed normally: %v", err)
			}
		case websocket.IsUnexpectedCloseError(err):
			// Unexpected close
			log.Printf("📥 WebSocket closed unexpectedly: %v", err)
		case strings.Contains(err.Error(), "i/o timeout"):
			// Timeout is expected with Cloudflare Tunnel, just log at debug level
			if asm.debug {
				log.Printf("📥 WebSocket read timeout (expected with Cloudflare): %v", err)
			}
			return true // Continue connection despite timeout
		default:
			// Other errors - only log in debug mode
			if asm.debug {
				log.Printf("📥 WebSocket read error: %v", err)
			}
		}
		return false
	}

	// Clear deadline after successful read
	if err := ws.SetReadDeadline(time.Time{}); err != nil {
		log.Printf("❌ Failed to clear read deadline: %v", err)
		return false
	}

	// Handle ping messages
	if msgType == websocket.PingMessage {
		if err := ws.WriteMessage(websocket.PongMessage, nil); err != nil {
			return false
		}
		return true
	}

	// Process text messages
	if msgType == websocket.TextMessage {
		// Special handling for keepalive messages
		if string(msg) == "keepalive" && asm.debug {
			log.Printf("📥 Received keepalive from client")
		} else if asm.debug {
			log.Printf("📥 Received WebSocket message from client: %s", string(msg))
		}
	}

	return true
}

// BroadcastAudio accumulates audio data and sends in larger chunks for a specific source
func (asm *AudioStreamManager) BroadcastAudio(sourceID string, data []byte) {
	// First check if any clients exist for this source
	if !asm.HasActiveClients(sourceID) {
		return // No clients connected for this source
	}

	// Accumulate the data in the buffer
	asm.bufferMutex.Lock()
	buf, exists := asm.audioBuffers[sourceID]
	if !exists {
		buf = bytes.NewBuffer(make([]byte, 0, asm.bufferSize*2))
		asm.audioBuffers[sourceID] = buf
	}
	buf.Write(data)
	currentSize := buf.Len()
	asm.bufferMutex.Unlock()

	// Flush if buffer is full
	if currentSize >= asm.bufferSize {
		asm.flushBufferIfNeeded(sourceID, false)
	}
}

// GetActiveSourceIDs returns a list of sources that have active streaming clients
func (asm *AudioStreamManager) GetActiveSourceIDs() []string {
	asm.clientsMutex.RLock()
	defer asm.clientsMutex.RUnlock()

	activeSourceIDs := make([]string, 0, len(asm.clients))
	for sourceID, clients := range asm.clients {
		if len(clients) > 0 {
			activeSourceIDs = append(activeSourceIDs, sourceID)
		}
	}
	return activeSourceIDs
}

// HasActiveClients checks if there are any active clients for the given source ID
func (asm *AudioStreamManager) HasActiveClients(sourceID string) bool {
	asm.clientsMutex.RLock()
	defer asm.clientsMutex.RUnlock()

	clients, exists := asm.clients[sourceID]
	return exists && len(clients) > 0
}

// cleanupClientConnection performs the client disconnection cleanup
func (asm *AudioStreamManager) cleanupClientConnection(ws *websocket.Conn, sourceID string) {
	// Remove client on disconnect
	asm.clientsMutex.Lock()
	delete(asm.clients[sourceID], ws)

	// Check if this was the last client for the source
	lastClient := false
	if len(asm.clients[sourceID]) == 0 {
		delete(asm.clients, sourceID)
		lastClient = true
	}

	clientCount := 0
	if clients, exists := asm.clients[sourceID]; exists {
		clientCount = len(clients)
	}
	asm.clientsMutex.Unlock()

	// Remove the connection mutex
	asm.removeConnectionMutex(ws)

	// If this was the last client, unregister the callback and clean up buffer
	if lastClient {
		// Always log callback unregistration regardless of debug mode
		log.Printf("🔄 Unregistering audio broadcast callback for source: %s (no more clients)", sourceID)
		myaudio.UnregisterBroadcastCallback(sourceID)

		// Clean up the buffer
		asm.bufferMutex.Lock()
		delete(asm.audioBuffers, sourceID)
		delete(asm.latencyHistory, sourceID) // Clean up latency history
		asm.bufferMutex.Unlock()

		// Clean up latency and flush interval data
		asm.flushIntervalMutex.Lock()
		delete(asm.connectionLatency, sourceID)
		delete(asm.flushIntervals, sourceID)
		asm.flushIntervalMutex.Unlock()
	}

	// Always log disconnections regardless of debug mode
	log.Printf("🔌 WebSocket client %s disconnected from source %s, remaining clients: %d",
		ws.RemoteAddr().String(), sourceID, clientCount)

	// Try to send a clean close message only if not already closed
	closeMsg := websocket.FormatCloseMessage(CloseNormalClosure, "Connection closed normally")
	err := ws.WriteControl(websocket.CloseMessage, closeMsg, time.Now().Add(time.Second))
	if err != nil {
		// Only log as an error if it's not due to the connection already being closed
		if !strings.Contains(err.Error(), "websocket: close sent") {
			log.Printf("❌ Failed to send close message: %v", err)
		} else if asm.debug {
			// Log at debug level that we tried to close an already closed connection
			log.Printf("📥 Connection already closed: %s", ws.RemoteAddr().String())
		}
	}

	// Always ensure the connection is closed
	ws.Close()
}
