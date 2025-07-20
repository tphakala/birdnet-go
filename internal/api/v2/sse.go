// internal/api/v2/sse.go
package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// SSEDetectionData represents the detection data sent via SSE
type SSEDetectionData struct {
	datastore.Note
	BirdImage imageprovider.BirdImage `json:"birdImage"`
	Timestamp time.Time               `json:"timestamp"`
	EventType string                  `json:"eventType"`
}

// SSESoundLevelData represents sound level data sent via SSE
type SSESoundLevelData struct {
	myaudio.SoundLevelData
	EventType string `json:"eventType"`
}

// SSEEvent represents a generic SSE event that can contain different data types
type SSEEvent struct {
	Type      string      `json:"type"`
	Data      interface{} `json:"data"`
	Timestamp time.Time   `json:"timestamp"`
}

// SSEToastData represents toast message data sent via SSE
type SSEToastData struct {
	Message   string    `json:"message"`
	Type      string    `json:"type"` // "info", "success", "warning", "error"
	Duration  int       `json:"duration,omitempty"` // Duration in milliseconds
	EventType string    `json:"eventType"`
	Timestamp time.Time `json:"timestamp"`
}

// SSEClient represents a connected SSE client
type SSEClient struct {
	ID             string
	Channel        chan SSEDetectionData
	SoundLevelChan chan SSESoundLevelData
	ToastChan      chan SSEToastData
	Request        *http.Request
	Response       http.ResponseWriter
	Done           chan bool
	StreamType     string // "detections", "soundlevels", "toasts", or "all"
}

// SSEManager manages SSE connections and broadcasts
type SSEManager struct {
	clients map[string]*SSEClient
	mutex   sync.RWMutex
	logger  *log.Logger
}

// NewSSEManager creates a new SSE manager
func NewSSEManager(logger *log.Logger) *SSEManager {
	return &SSEManager{
		clients: make(map[string]*SSEClient),
		logger:  logger,
	}
}

// AddClient adds a new SSE client
func (m *SSEManager) AddClient(client *SSEClient) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.clients[client.ID] = client
	if m.logger != nil {
		m.logger.Printf("SSE client connected: %s (total: %d)", client.ID, len(m.clients))
	}
}

// RemoveClient removes an SSE client
func (m *SSEManager) RemoveClient(clientID string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if client, exists := m.clients[clientID]; exists {
		close(client.Channel)
		if client.SoundLevelChan != nil {
			close(client.SoundLevelChan)
		}
		if client.ToastChan != nil {
			close(client.ToastChan)
		}
		close(client.Done)
		delete(m.clients, clientID)
		if m.logger != nil {
			m.logger.Printf("SSE client disconnected: %s (total: %d)", clientID, len(m.clients))
		}
	}
}

// BroadcastDetection sends detection data to all connected clients
func (m *SSEManager) BroadcastDetection(detection *SSEDetectionData) {
	m.mutex.RLock()

	if len(m.clients) == 0 {
		m.mutex.RUnlock()
		return // No clients to broadcast to
	}

	// Collect blocked client IDs to remove them after releasing the lock
	var blockedClients []string

	for clientID, client := range m.clients {
		select {
		case client.Channel <- *detection:
			// Successfully sent to client
		case <-time.After(3 * time.Second): // Increased timeout for slow connections
			// Client channel is blocked, collect ID for removal
			if m.logger != nil {
				m.logger.Printf("SSE client %s appears blocked, will remove", clientID)
			}
			blockedClients = append(blockedClients, clientID)
		}
	}

	// Release the read lock before removing clients
	m.mutex.RUnlock()

	// Remove blocked clients without holding the lock to avoid deadlock
	for _, clientID := range blockedClients {
		go m.RemoveClient(clientID)
	}
}

// BroadcastSoundLevel sends sound level data to all connected clients
func (m *SSEManager) BroadcastSoundLevel(soundLevel *SSESoundLevelData) {
	m.mutex.RLock()

	if len(m.clients) == 0 {
		m.mutex.RUnlock()
		return // No clients to broadcast to
	}

	// Collect blocked client IDs to remove them after releasing the lock
	var blockedClients []string

	for clientID, client := range m.clients {
		// Only send to clients that want sound level data
		if client.StreamType == "soundlevels" || client.StreamType == "all" {
			if client.SoundLevelChan != nil {
				select {
				case client.SoundLevelChan <- *soundLevel:
					// Successfully sent to client
				case <-time.After(3 * time.Second): // Increased timeout for slow connections
					// Client channel is blocked, collect ID for removal
					if m.logger != nil {
						m.logger.Printf("SSE client %s appears blocked on sound level channel, will remove", clientID)
					}
					blockedClients = append(blockedClients, clientID)
				}
			}
		}
	}

	// Release the read lock before removing clients
	m.mutex.RUnlock()

	// Remove blocked clients without holding the lock to avoid deadlock
	for _, clientID := range blockedClients {
		go m.RemoveClient(clientID)
	}
}

// BroadcastToast sends toast message data to all connected clients
func (m *SSEManager) BroadcastToast(toast *SSEToastData) {
	m.mutex.RLock()

	if len(m.clients) == 0 {
		m.mutex.RUnlock()
		return // No clients to broadcast to
	}

	// Collect blocked client IDs to remove them after releasing the lock
	var blockedClients []string

	for clientID, client := range m.clients {
		// Only send to clients that want toast data
		if client.StreamType == "toasts" || client.StreamType == "all" {
			if client.ToastChan != nil {
				select {
				case client.ToastChan <- *toast:
					// Successfully sent to client
				case <-time.After(100 * time.Millisecond): // Short timeout for toast messages
					// Client channel is blocked, collect ID for removal
					if m.logger != nil {
						m.logger.Printf("SSE client %s appears blocked on toast channel, will remove", clientID)
					}
					blockedClients = append(blockedClients, clientID)
				}
			}
		}
	}

	// Release the read lock before removing clients
	m.mutex.RUnlock()

	// Remove blocked clients without holding the lock to avoid deadlock
	for _, clientID := range blockedClients {
		go m.RemoveClient(clientID)
	}
}

// GetClientCount returns the number of connected clients
func (m *SSEManager) GetClientCount() int {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	return len(m.clients)
}

// initSSERoutes registers SSE-related API endpoints
func (c *Controller) initSSERoutes() {
	// Initialize SSE manager if not already done
	if c.sseManager == nil {
		c.sseManager = NewSSEManager(c.logger)
	}

	// Create rate limiter for SSE connections (10 requests per minute per IP)
	rateLimiterConfig := middleware.RateLimiterConfig{
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(
			middleware.RateLimiterMemoryStoreConfig{
				Rate:      10,              // 10 requests
				ExpiresIn: 1 * time.Minute, // per minute
			},
		),
		IdentifierExtractor: middleware.DefaultRateLimiterConfig.IdentifierExtractor,
		ErrorHandler: func(context echo.Context, err error) error {
			return context.JSON(http.StatusTooManyRequests, map[string]string{
				"error": "Rate limit exceeded for SSE connections",
			})
		},
		DenyHandler: func(context echo.Context, identifier string, err error) error {
			return context.JSON(http.StatusTooManyRequests, map[string]string{
				"error": "Too many SSE connection attempts, please wait before trying again",
			})
		},
	}

	// SSE endpoint for detection stream with rate limiting
	c.Group.GET("/detections/stream", c.StreamDetections, middleware.RateLimiterWithConfig(rateLimiterConfig))

	// SSE endpoint for sound level stream with rate limiting
	c.Group.GET("/soundlevels/stream", c.StreamSoundLevels, middleware.RateLimiterWithConfig(rateLimiterConfig))

	// SSE endpoint for toast messages stream with rate limiting
	c.Group.GET("/toasts/stream", c.StreamToasts, middleware.RateLimiterWithConfig(rateLimiterConfig))

	// SSE status endpoint - shows connected client count
	c.Group.GET("/sse/status", c.GetSSEStatus)
}

// setSSEHeaders sets the required headers for Server-Sent Events
func setSSEHeaders(ctx echo.Context) {
	ctx.Response().Header().Set("Content-Type", "text/event-stream")
	ctx.Response().Header().Set("Cache-Control", "no-cache")
	ctx.Response().Header().Set("Connection", "keep-alive")
	ctx.Response().Header().Set("Access-Control-Allow-Origin", "*")
	ctx.Response().Header().Set("Access-Control-Allow-Headers", "Cache-Control")
}

// createSSEClient creates a new SSE client with common settings
func createSSEClient(clientID string, ctx echo.Context, streamType string) *SSEClient {
	return &SSEClient{
		ID:         clientID,
		Request:    ctx.Request(),
		Response:   ctx.Response(),
		Done:       make(chan bool),
		StreamType: streamType,
	}
}

// sendConnectionMessage sends the initial connection message to the client
func (c *Controller) sendConnectionMessage(ctx echo.Context, clientID, message, streamType string) error {
	data := map[string]string{
		"clientId": clientID,
		"message":  message,
	}
	if streamType != "" {
		data["type"] = streamType
	}
	return c.sendSSEMessage(ctx, "connected", data)
}

// logSSEConnection logs SSE client connection/disconnection events
func (c *Controller) logSSEConnection(clientID, ip, userAgent, streamType string, connected bool) {
	if c.apiLogger == nil {
		return
	}
	
	action := "connected"
	if !connected {
		action = "disconnected"
	}
	
	c.apiLogger.Info(fmt.Sprintf("SSE %s client %s", streamType, action),
		"client_id", clientID,
		"ip", ip,
		"user_agent", userAgent,
	)
}

// sendSSEHeartbeat sends a heartbeat message to keep the connection alive
func (c *Controller) sendSSEHeartbeat(ctx echo.Context, clientID, streamType string) error {
	data := map[string]interface{}{
		"timestamp": time.Now().Unix(),
		"clients":   c.sseManager.GetClientCount(),
	}
	if streamType != "" {
		data["type"] = streamType
	}
	
	if err := c.sendSSEMessage(ctx, "heartbeat", data); err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Debug("SSE heartbeat failed, client likely disconnected",
				"client_id", clientID,
				"error", err.Error(),
			)
		}
		return err
	}
	return nil
}

// handleSSEStream handles the common SSE stream setup and teardown
func (c *Controller) handleSSEStream(ctx echo.Context, streamType, message, logPrefix string, setupFunc func(*SSEClient), eventLoop func(echo.Context, *SSEClient, string) error) error {
	// Set SSE headers
	setSSEHeaders(ctx)

	// Generate client ID and create client
	clientID := generateCorrelationID()
	client := createSSEClient(clientID, ctx, streamType)
	
	// Allow custom setup
	if setupFunc != nil {
		setupFunc(client)
	}

	// Add client to manager
	c.sseManager.AddClient(client)

	// Send initial connection message
	if err := c.sendConnectionMessage(ctx, clientID, message, streamType); err != nil {
		c.sseManager.RemoveClient(clientID)
		return err
	}

	// Log the connection
	c.logSSEConnection(clientID, ctx.RealIP(), ctx.Request().UserAgent(), logPrefix, true)

	// Handle the SSE connection
	defer func() {
		c.sseManager.RemoveClient(clientID)
		c.logSSEConnection(clientID, ctx.RealIP(), "", logPrefix, false)
	}()

	// Run the event loop
	return eventLoop(ctx, client, clientID)
}

// StreamDetections handles the SSE connection for real-time detection streaming
func (c *Controller) StreamDetections(ctx echo.Context) error {
	return c.handleSSEStream(ctx, "detections", "Connected to detection stream", "detection",
		func(client *SSEClient) {
			client.Channel = make(chan SSEDetectionData, 100) // Increased buffer for high detection periods
		},
		func(ctx echo.Context, client *SSEClient, clientID string) error {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case detection := <-client.Channel:
					// Send detection data
					if err := c.sendSSEMessage(ctx, "detection", detection); err != nil {
						if c.apiLogger != nil {
							c.apiLogger.Error("Failed to send SSE detection",
								"client_id", clientID,
								"error", err.Error(),
							)
						}
						return err
					}

				case <-ticker.C:
					// Send heartbeat
					if err := c.sendSSEHeartbeat(ctx, clientID, ""); err != nil {
						return err
					}

				case <-ctx.Request().Context().Done():
					// Client disconnected
					return nil

				case <-client.Done:
					// Client marked for removal
					return nil
				}
			}
		})
}

// StreamSoundLevels handles the SSE connection for real-time sound level streaming
func (c *Controller) StreamSoundLevels(ctx echo.Context) error {
	return c.handleSSEStream(ctx, "soundlevels", "Connected to sound level stream", "sound level",
		func(client *SSEClient) {
			client.Channel = make(chan SSEDetectionData, 1)    // Small buffer, not used for sound levels
			client.SoundLevelChan = make(chan SSESoundLevelData, 100) // Buffer for sound level data
		},
		func(ctx echo.Context, client *SSEClient, clientID string) error {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case soundLevel := <-client.SoundLevelChan:
					// Send sound level data
					if err := c.sendSSEMessage(ctx, "soundlevel", soundLevel); err != nil {
						if c.apiLogger != nil {
							c.apiLogger.Error("Failed to send SSE sound level",
								"client_id", clientID,
								"error", err.Error(),
							)
						}
						return err
					}

				case <-ticker.C:
					// Send heartbeat
					if err := c.sendSSEHeartbeat(ctx, clientID, "soundlevels"); err != nil {
						return err
					}

				case <-ctx.Request().Context().Done():
					// Client disconnected
					return nil

				case <-client.Done:
					// Client marked for removal
					return nil
				}
			}
		})
}

// StreamToasts handles the SSE connection for real-time toast message streaming
func (c *Controller) StreamToasts(ctx echo.Context) error {
	return c.handleSSEStream(ctx, "toasts", "Connected to toast stream", "toast",
		func(client *SSEClient) {
			client.Channel = make(chan SSEDetectionData, 1) // Small buffer, not used for toasts
			client.ToastChan = make(chan SSEToastData, 10)  // Buffer for toast messages
		},
		func(ctx echo.Context, client *SSEClient, clientID string) error {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case toast := <-client.ToastChan:
					// Send toast data
					if err := c.sendSSEMessage(ctx, "toast", toast); err != nil {
						if c.apiLogger != nil {
							c.apiLogger.Error("Failed to send SSE toast",
								"client_id", clientID,
								"error", err.Error(),
							)
						}
						return err
					}

				case <-ticker.C:
					// Send heartbeat
					if err := c.sendSSEHeartbeat(ctx, clientID, "toasts"); err != nil {
						return err
					}

				case <-ctx.Request().Context().Done():
					// Client disconnected
					return nil

				case <-client.Done:
					// Client marked for removal
					return nil
				}
			}
		})
}

// sendSSEMessage sends a Server-Sent Event message
func (c *Controller) sendSSEMessage(ctx echo.Context, event string, data interface{}) error {
	// Convert data to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal SSE data: %w", err)
	}

	// Format SSE message
	message := fmt.Sprintf("event: %s\ndata: %s\n\n", event, string(jsonData))

	// Set write deadline to prevent hanging on slow/disconnected clients
	if conn, ok := ctx.Response().Writer.(interface{ SetWriteDeadline(time.Time) error }); ok {
		deadline := time.Now().Add(10 * time.Second) // 10 second timeout
		if err := conn.SetWriteDeadline(deadline); err != nil {
			// If we can't set deadline, log but continue - not all response writers support this
			if c.apiLogger != nil {
				c.apiLogger.Debug("Failed to set write deadline for SSE message", "error", err.Error())
			}
		}
	}

	// Write to response
	if _, err := ctx.Response().Write([]byte(message)); err != nil {
		return fmt.Errorf("failed to write SSE message: %w", err)
	}

	// Flush the response
	if flusher, ok := ctx.Response().Writer.(http.Flusher); ok {
		flusher.Flush()
	}

	return nil
}

// GetSSEStatus returns information about SSE connections
func (c *Controller) GetSSEStatus(ctx echo.Context) error {
	if c.sseManager == nil {
		return ctx.JSON(http.StatusOK, map[string]interface{}{
			"connected_clients": 0,
			"status":            "disabled",
		})
	}

	return ctx.JSON(http.StatusOK, map[string]interface{}{
		"connected_clients": c.sseManager.GetClientCount(),
		"status":            "active",
	})
}

// BroadcastDetection is a helper method to broadcast detection from the controller
func (c *Controller) BroadcastDetection(note *datastore.Note, birdImage *imageprovider.BirdImage) error {
	if c.sseManager == nil {
		return fmt.Errorf("SSE manager not initialized")
	}

	// Add nil checks to prevent panic
	if note == nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("SSE broadcast skipped: note is nil")
		}
		return fmt.Errorf("note is nil")
	}
	if birdImage == nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("SSE broadcast skipped: birdImage is nil")
		}
		return fmt.Errorf("birdImage is nil")
	}

	detection := SSEDetectionData{
		Note:      *note,
		BirdImage: *birdImage,
		Timestamp: time.Now(),
		EventType: "new_detection",
	}

	c.sseManager.BroadcastDetection(&detection)
	return nil
}

// BroadcastSoundLevel is a helper method to broadcast sound level data from the controller
func (c *Controller) BroadcastSoundLevel(soundLevel *myaudio.SoundLevelData) error {
	if c.sseManager == nil {
		return fmt.Errorf("SSE manager not initialized")
	}

	// Add nil check to prevent panic
	if soundLevel == nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("SSE broadcast skipped: soundLevel is nil")
		}
		return fmt.Errorf("soundLevel is nil")
	}

	sseData := SSESoundLevelData{
		SoundLevelData: *soundLevel,
		EventType:      "sound_level_update",
	}

	c.sseManager.BroadcastSoundLevel(&sseData)
	return nil
}

// BroadcastToast is a helper method to broadcast toast messages from the controller
func (c *Controller) BroadcastToast(message, toastType string, duration int) error {
	if c.sseManager == nil {
		return fmt.Errorf("SSE manager not initialized")
	}

	toast := SSEToastData{
		Message:   message,
		Type:      toastType,
		Duration:  duration,
		EventType: "toast",
		Timestamp: time.Now(),
	}

	c.sseManager.BroadcastToast(&toast)
	return nil
}
