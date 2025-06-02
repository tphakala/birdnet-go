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
)

// SSEDetectionData represents the detection data sent via SSE
type SSEDetectionData struct {
	datastore.Note
	BirdImage imageprovider.BirdImage `json:"birdImage"`
	Timestamp time.Time               `json:"timestamp"`
	EventType string                  `json:"eventType"`
}

// SSEClient represents a connected SSE client
type SSEClient struct {
	ID       string
	Channel  chan SSEDetectionData
	Request  *http.Request
	Response http.ResponseWriter
	Done     chan bool
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

	// SSE status endpoint - shows connected client count
	c.Group.GET("/sse/status", c.GetSSEStatus)
}

// StreamDetections handles the SSE connection for real-time detection streaming
func (c *Controller) StreamDetections(ctx echo.Context) error {
	// Set SSE headers
	ctx.Response().Header().Set("Content-Type", "text/event-stream")
	ctx.Response().Header().Set("Cache-Control", "no-cache")
	ctx.Response().Header().Set("Connection", "keep-alive")
	ctx.Response().Header().Set("Access-Control-Allow-Origin", "*")
	ctx.Response().Header().Set("Access-Control-Allow-Headers", "Cache-Control")

	// Generate client ID
	clientID := generateCorrelationID()

	// Create client
	client := &SSEClient{
		ID:       clientID,
		Channel:  make(chan SSEDetectionData, 100), // Increased buffer for high detection periods
		Request:  ctx.Request(),
		Response: ctx.Response(),
		Done:     make(chan bool),
	}

	// Add client to manager
	c.sseManager.AddClient(client)

	// Send initial connection message
	if err := c.sendSSEMessage(ctx, "connected", map[string]string{
		"clientId": clientID,
		"message":  "Connected to detection stream",
	}); err != nil {
		c.sseManager.RemoveClient(clientID)
		return err
	}

	// Log the connection
	if c.apiLogger != nil {
		c.apiLogger.Info("SSE client connected",
			"client_id", clientID,
			"ip", ctx.RealIP(),
			"user_agent", ctx.Request().UserAgent(),
		)
	}

	// Handle the SSE connection
	defer func() {
		c.sseManager.RemoveClient(clientID)
		if c.apiLogger != nil {
			c.apiLogger.Info("SSE client disconnected",
				"client_id", clientID,
				"ip", ctx.RealIP(),
			)
		}
	}()

	// Keep connection alive and send detections
	ticker := time.NewTicker(30 * time.Second) // Heartbeat every 30 seconds
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
			if err := c.sendSSEMessage(ctx, "heartbeat", map[string]interface{}{
				"timestamp": time.Now().Unix(),
				"clients":   c.sseManager.GetClientCount(),
			}); err != nil {
				if c.apiLogger != nil {
					c.apiLogger.Debug("SSE heartbeat failed, client likely disconnected",
						"client_id", clientID,
						"error", err.Error(),
					)
				}
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
