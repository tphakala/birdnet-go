package handlers

import (
	"bytes"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/myaudio"
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
}

// NewAudioStreamManager creates a new audio stream manager
func NewAudioStreamManager() *AudioStreamManager {
	return &AudioStreamManager{
		clients: make(map[string]map[*websocket.Conn]bool),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,  // Increased from 1024
			WriteBufferSize: 16384, // Increased from 1024
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all connections (security is handled by Echo middleware)
			},
		},
		debug:         true, // Enable debug for troubleshooting
		audioBuffers:  make(map[string]*bytes.Buffer),
		bufferSize:    12288, // Reduced from 32768 to 12288 (smaller chunks to prevent client buffer overflow)
		forceInterval: true,  // Force sending buffers periodically even if not full
	}
}

// HandleWebSocket upgrades an HTTP connection to WebSocket and handles the connection
func (asm *AudioStreamManager) HandleWebSocket(c echo.Context) error {
	// Check if user is authenticated or connecting from local network
	server := c.Get("server")
	if server == nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Server context not available")
	}

	// Check if user is authenticated
	var isAuthenticated bool
	if s, ok := server.(interface {
		IsAccessAllowed(c echo.Context) bool
		isAuthenticationEnabled(c echo.Context) bool
	}); ok {
		// Allow access if authentication is disabled globally or user is authenticated
		isAuthenticated = !s.isAuthenticationEnabled(c) || s.IsAccessAllowed(c)
	}

	// Check if request is from local network
	clientIP := c.RealIP()
	isLocalNetwork := isLocalIPAddress(clientIP)

	// Deny access if not authenticated and not from local network
	if !isAuthenticated && !isLocalNetwork {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required for audio streaming")
	}

	sourceID := c.Param("sourceID")
	if sourceID == "" {
		return echo.NewHTTPError(400, "Source ID is required")
	}

	// Check if the source exists and has a valid capture buffer
	exists := myaudio.HasCaptureBuffer(sourceID)
	if !exists {
		return echo.NewHTTPError(404, "Audio source not found")
	}

	// Upgrade the connection to WebSocket
	ws, err := asm.upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		log.Printf("❌ WebSocket upgrade failed: %v", err)
		return err
	}

	// Set write deadline to ensure timely delivery
	if err := ws.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		log.Printf("❌ Failed to set write deadline: %v", err)
		ws.Close()
		return err
	}

	// Log connection info
	if asm.debug {
		log.Printf("🔌 New WebSocket connection for source: %s from %s", sourceID, c.RealIP())
	}

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
		if asm.debug {
			log.Printf("🔄 Registering audio broadcast callback for source: %s", sourceID)
		}
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
	ticker := time.NewTicker(20 * time.Millisecond) // Reduced from 30ms to 20ms for smoother delivery
	defer ticker.Stop()

	for range ticker.C {
		// Stop if no clients are connected
		if !asm.HasActiveClients(sourceID) {
			break
		}

		asm.flushBufferIfNeeded(sourceID, true)
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
			continue
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
	}
}

// handleClient manages a single WebSocket client connection
func (asm *AudioStreamManager) handleClient(ws *websocket.Conn, sourceID string) {
	defer func() {
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

		// If this was the last client, unregister the callback and clean up buffer
		if lastClient {
			if asm.debug {
				log.Printf("🔄 Unregistering audio broadcast callback for source: %s (no more clients)", sourceID)
			}
			myaudio.UnregisterBroadcastCallback(sourceID)

			// Clean up the buffer
			asm.bufferMutex.Lock()
			delete(asm.audioBuffers, sourceID)
			asm.bufferMutex.Unlock()
		}

		if asm.debug {
			log.Printf("🔌 WebSocket client %s disconnected from source %s, remaining clients: %d",
				ws.RemoteAddr().String(), sourceID, clientCount)
		}

		ws.Close()
	}()

	// Set larger ping/pong timeout
	if err := ws.SetReadDeadline(time.Time{}); err != nil {
		log.Printf("❌ Failed to set read deadline: %v", err)
		return
	}

	ws.SetPongHandler(func(string) error {
		if err := ws.SetReadDeadline(time.Time{}); err != nil {
			log.Printf("❌ Failed to reset read deadline on pong: %v", err)
			return err
		}
		return nil
	})

	// Send an initial ping to check connection
	if err := ws.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
		log.Printf("❌ Failed to send initial ping: %v", err)
		return
	}

	// Handle incoming messages (including pings and control messages)
	for {
		msgType, msg, err := ws.ReadMessage()
		if err != nil {
			// Client disconnected or error occurred
			if asm.debug {
				log.Printf("📥 WebSocket read error: %v", err)
			}
			break
		}

		// Handle ping messages
		if msgType == websocket.PingMessage {
			if err := ws.WriteMessage(websocket.PongMessage, nil); err != nil {
				break
			}
			continue
		}

		// Process other messages
		if asm.debug && msgType == websocket.TextMessage {
			log.Printf("📥 Received WebSocket message from client: %s", string(msg))
		}
	}
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

// isLocalIPAddress checks if the given IP address is from a local network
func isLocalIPAddress(ip string) bool {
	// Check for localhost
	if ip == "127.0.0.1" || ip == "::1" || ip == "localhost" {
		return true
	}

	// Check for private network ranges
	// 10.0.0.0/8
	if strings.HasPrefix(ip, "10.") {
		return true
	}

	// 172.16.0.0/12
	if strings.HasPrefix(ip, "172.") {
		parts := strings.Split(ip, ".")
		if len(parts) >= 2 {
			if second, err := strconv.Atoi(parts[1]); err == nil {
				if second >= 16 && second <= 31 {
					return true
				}
			}
		}
	}

	// 192.168.0.0/16
	if strings.HasPrefix(ip, "192.168.") {
		return true
	}

	// fc00::/7 (Unique Local Addresses)
	if strings.HasPrefix(strings.ToLower(ip), "fc") || strings.HasPrefix(strings.ToLower(ip), "fd") {
		return true
	}

	return false
}
