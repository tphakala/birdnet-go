// internal/api/v2/sse.go
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// SSE connection configuration
const (
	// Connection timeouts
	maxSSEStreamDuration = 30 * time.Minute      // Maximum stream duration to prevent resource leaks
	sseHeartbeatInterval = 30 * time.Second      // Heartbeat interval for keep-alive
	sseEventLoopSleep    = 10 * time.Millisecond // Sleep duration when no events
	sseWriteDeadline     = 10 * time.Second      // Write deadline for SSE messages

	// Endpoints
	detectionStreamEndpoint  = "/api/v2/detections/stream"
	soundLevelStreamEndpoint = "/api/v2/soundlevels/stream"

	// Buffer sizes
	sseDetectionBufferSize  = 100 // Buffer size for detection channels (high volume)
	sseSoundLevelBufferSize = 100 // Buffer size for sound level channels
	sseMinimalBufferSize    = 1   // Minimal buffer for unused channels
	sseDoneChannelBuffer    = 1   // Buffer for Done channels to prevent blocking

	// Rate limits
	sseRateLimitRequests = 10              // SSE rate limit requests per window
	sseRateLimitWindow   = 1 * time.Minute // SSE rate limit time window

	// Client health monitoring
	maxConsecutiveDrops = 3 // Auto-disconnect clients after this many consecutive dropped messages

	// Stream types - used to identify what data a client wants to receive
	// Note: StreamType="all" shares a single consecutiveDrops counter across both streams,
	// meaning drops on one stream affect health tracking for both
	streamTypeDetections  = "detections"
	streamTypeSoundLevels = "soundlevels"
	streamTypeAll         = "all"
)

// WriteDeadlineSetter interface for response writers that support write deadlines
type WriteDeadlineSetter interface {
	SetWriteDeadline(time.Time) error
}

// SSEDetectionData represents the detection data sent via SSE
type SSEDetectionData struct {
	datastore.Note
	BirdImage          imageprovider.BirdImage `json:"birdImage"`
	Timestamp          time.Time               `json:"timestamp"`
	EventType          string                  `json:"eventType"`
	IsNewSpecies       bool                    `json:"isNewSpecies,omitempty"`       // First seen within tracking window
	DaysSinceFirstSeen int                     `json:"daysSinceFirstSeen,omitempty"` // Days since species was first detected
}

// SSESoundLevelData represents sound level data sent via SSE
type SSESoundLevelData struct {
	myaudio.SoundLevelData
	EventType string `json:"eventType"`
}

// SSEEvent represents a generic SSE event that can contain different data types
type SSEEvent struct {
	Type      string    `json:"type"`
	Data      any       `json:"data"`
	Timestamp time.Time `json:"timestamp"`
}

// SSEClient represents a connected SSE client
type SSEClient struct {
	ID             string
	Channel        chan SSEDetectionData
	SoundLevelChan chan SSESoundLevelData
	Request        *http.Request
	Response       http.ResponseWriter
	Done           chan struct{} // Signal-only buffered channel to prevent blocking
	StreamType     string        // streamTypeDetections, streamTypeSoundLevels, or streamTypeAll

	// Health tracking for auto-disconnect of slow/blocked clients
	// Uses atomic operations for thread-safe access during concurrent broadcasts
	consecutiveDrops atomic.Int32 // Count of consecutive failed message sends
}

// SSEManager manages SSE connections and broadcasts
type SSEManager struct {
	clients map[string]*SSEClient
	mutex   sync.RWMutex
}

// NewSSEManager creates a new SSE manager
func NewSSEManager() *SSEManager {
	return &SSEManager{
		clients: make(map[string]*SSEClient),
	}
}

// AddClient adds a new SSE client
func (m *SSEManager) AddClient(client *SSEClient) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.clients[client.ID] = client
	GetLogger().Debug("SSE client connected",
		logger.String("client_id", client.ID),
		logger.Int("total_clients", len(m.clients)),
	)
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
		close(client.Done)
		delete(m.clients, clientID)
		GetLogger().Debug("SSE client disconnected",
			logger.String("client_id", clientID),
			logger.Int("total_clients", len(m.clients)),
		)
	}
}

// BroadcastDetection sends detection data to all connected clients
// Uses non-blocking send to prevent slow clients from blocking fast clients.
// Clients are automatically disconnected after maxConsecutiveDrops failed sends.
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
			// Successfully sent to client - reset health counter atomically
			client.consecutiveDrops.Store(0)

		default:
			// Channel full - drop this update, increment counter atomically
			drops := client.consecutiveDrops.Add(1)

			// Only log when reaching disconnect threshold to avoid log spam
			if drops >= maxConsecutiveDrops {
				GetLogger().Info("SSE client disconnected after consecutive drops",
					logger.String("client_id", clientID),
					logger.Int("consecutive_drops", int(drops)),
				)
				blockedClients = append(blockedClients, clientID)
			}
		}
	}

	// Release the read lock before removing clients
	m.mutex.RUnlock()

	// Remove blocked clients synchronously (we're outside the lock and RemoveClient is fast)
	// Note: Low probability race if client reconnects with same ID between unlock and removal
	for _, clientID := range blockedClients {
		m.RemoveClient(clientID)
	}
}

// BroadcastSoundLevel sends sound level data to all connected clients
// Uses non-blocking send to prevent slow clients from blocking fast clients.
// Clients are automatically disconnected after maxConsecutiveDrops failed sends.
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
		if client.StreamType == streamTypeSoundLevels || client.StreamType == streamTypeAll {
			if client.SoundLevelChan != nil {
				select {
				case client.SoundLevelChan <- *soundLevel:
					// Successfully sent to client - reset health counter atomically
					client.consecutiveDrops.Store(0)

				default:
					// Channel full - drop this update, increment counter atomically
					drops := client.consecutiveDrops.Add(1)

					// Only log when reaching disconnect threshold to avoid log spam
					if drops >= maxConsecutiveDrops {
						GetLogger().Info("SSE client disconnected after consecutive drops",
							logger.String("client_id", clientID),
							logger.Int("consecutive_drops", int(drops)),
						)
						blockedClients = append(blockedClients, clientID)
					}
				}
			}
		}
	}

	// Release the read lock before removing clients
	m.mutex.RUnlock()

	// Remove blocked clients synchronously (we're outside the lock and RemoveClient is fast)
	// Note: Low probability race if client reconnects with same ID between unlock and removal
	for _, clientID := range blockedClients {
		m.RemoveClient(clientID)
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
		c.sseManager = NewSSEManager()
	}

	// Create rate limiter for SSE connections (10 requests per minute per IP)
	rateLimiterConfig := middleware.RateLimiterConfig{
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(
			middleware.RateLimiterMemoryStoreConfig{
				Rate:      sseRateLimitRequests, // Requests per window
				ExpiresIn: sseRateLimitWindow,   // Rate limit window
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
		Done:       make(chan struct{}, sseDoneChannelBuffer), // Signal-only buffered channel to prevent blocking on cleanup
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
	return c.sendSSEMessage(ctx, SSEStatusConnected, data)
}

// logSSEConnection logs SSE client connection/disconnection events
func (c *Controller) logSSEConnection(clientID, ip, userAgent, streamType string, connected bool) {
	action := SSEStatusConnected
	if !connected {
		action = SSEStatusDisconnected
	}

	c.logInfoIfEnabled(fmt.Sprintf("SSE %s client %s", streamType, action),
		logger.String("client_id", clientID),
		logger.String("ip", ip),
		logger.String("user_agent", userAgent),
	)
}

// sendSSEHeartbeat sends a heartbeat message to keep the connection alive
func (c *Controller) sendSSEHeartbeat(ctx echo.Context, clientID, streamType string) error {
	data := map[string]any{
		"timestamp": time.Now().Unix(),
		"clients":   c.sseManager.GetClientCount(),
	}
	if streamType != "" {
		data["type"] = streamType
	}

	if err := c.sendSSEMessage(ctx, "heartbeat", data); err != nil {
		c.logDebugIfEnabled("SSE heartbeat failed, client likely disconnected",
			logger.String("client_id", clientID),
			logger.Error(err),
		)
		return err
	}
	return nil
}

// handleSSEStream handles the common SSE stream setup and teardown with timeout protection
func (c *Controller) handleSSEStream(ctx echo.Context, streamType, message, logPrefix string, setupFunc func(*SSEClient), eventLoop func(echo.Context, *SSEClient, string) error) error {
	// Track connection start time for metrics
	connectionStartTime := time.Now()

	// Track metrics if available
	endpoint := ""
	switch streamType {
	case streamTypeDetections:
		endpoint = detectionStreamEndpoint
	case streamTypeSoundLevels:
		endpoint = soundLevelStreamEndpoint
	}

	if c.metrics != nil && c.metrics.HTTP != nil && endpoint != "" {
		c.metrics.HTTP.SSEConnectionStarted(endpoint)
		defer func() {
			duration := time.Since(connectionStartTime).Seconds()
			closeReason := metrics.SSECloseReasonClosed
			if ctx.Request().Context().Err() == context.DeadlineExceeded {
				closeReason = metrics.SSECloseReasonTimeout
			} else if ctx.Request().Context().Err() == context.Canceled {
				closeReason = metrics.SSECloseReasonCanceled
			}
			c.metrics.HTTP.SSEConnectionClosed(endpoint, duration, closeReason)
		}()
	}

	// Create a context with timeout for maximum connection duration
	timeoutCtx, cancel := context.WithTimeout(ctx.Request().Context(), maxSSEStreamDuration)
	defer cancel()

	// Override the request context with timeout context
	originalReq := ctx.Request()
	ctx.SetRequest(originalReq.WithContext(timeoutCtx))

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
	return c.handleSSEStream(ctx, streamTypeDetections, "Connected to detection stream", "detection",
		func(client *SSEClient) {
			client.Channel = make(chan SSEDetectionData, sseDetectionBufferSize) // Buffer for high detection periods
		},
		func(ctx echo.Context, client *SSEClient, clientID string) error {
			return c.runSSEEventLoop(ctx, client, clientID, detectionStreamEndpoint,
				func() (any, bool) {
					select {
					case detection, ok := <-client.Channel:
						if !ok {
							return nil, false // Channel closed, no more data
						}
						return detection, true
					default:
						return nil, false
					}
				},
				"detection",
				"",
			)
		})
}

// StreamSoundLevels handles the SSE connection for real-time sound level streaming
func (c *Controller) StreamSoundLevels(ctx echo.Context) error {
	return c.handleSSEStream(ctx, streamTypeSoundLevels, "Connected to sound level stream", "sound level",
		func(client *SSEClient) {
			client.Channel = make(chan SSEDetectionData, sseMinimalBufferSize)            // Minimal buffer, not used for sound levels
			client.SoundLevelChan = make(chan SSESoundLevelData, sseSoundLevelBufferSize) // Buffer for sound level data
		},
		func(ctx echo.Context, client *SSEClient, clientID string) error {
			return c.runSSEEventLoop(ctx, client, clientID, soundLevelStreamEndpoint,
				func() (any, bool) {
					select {
					case soundLevel, ok := <-client.SoundLevelChan:
						if !ok {
							return nil, false // Channel closed, no more data
						}
						return soundLevel, true
					default:
						return nil, false
					}
				},
				"soundlevel",
				streamTypeSoundLevels,
			)
		})
}

// runSSEEventLoop handles the common SSE event loop pattern for all stream types
func (c *Controller) runSSEEventLoop(ctx echo.Context, client *SSEClient, clientID string, endpoint string,
	dataReceiver func() (any, bool), eventType string, heartbeatType string) error {

	ticker := time.NewTicker(sseHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Send heartbeat
			if err := c.sendSSEHeartbeat(ctx, clientID, heartbeatType); err != nil {
				c.recordSSEError(endpoint, "heartbeat_failed")
				return err
			}
			c.recordSSEMessage(endpoint, "heartbeat")

		case <-ctx.Request().Context().Done():
			// Client disconnected
			return nil

		case <-client.Done:
			// Client marked for removal
			return nil

		default:
			// Check for data on the channel (non-blocking)
			if data, hasData := dataReceiver(); hasData {
				if err := c.sendSSEMessage(ctx, eventType, data); err != nil {
					c.logErrorIfEnabled("Failed to send SSE message",
						logger.String("client_id", clientID),
						logger.String("endpoint", endpoint),
						logger.String("event_type", eventType),
						logger.Error(err),
					)
					c.recordSSEError(endpoint, "send_failed")
					return err
				}
				c.recordSSEMessage(endpoint, eventType)
			} else {
				// Small sleep to prevent busy-waiting when no data
				time.Sleep(sseEventLoopSleep)
			}
		}
	}
}

// sendSSEMessage sends a Server-Sent Event message
func (c *Controller) sendSSEMessage(ctx echo.Context, event string, data any) error {
	// Convert data to JSON with panic recovery
	jsonData, err := c.safeMarshalJSON(event, data)
	if err != nil {
		return fmt.Errorf("failed to marshal SSE data: %w", err)
	}

	// Format SSE message
	message := fmt.Sprintf("event: %s\ndata: %s\n\n", event, string(jsonData))

	// Set write deadline to prevent hanging on slow/disconnected clients
	if conn, ok := ctx.Response().Writer.(WriteDeadlineSetter); ok {
		deadline := time.Now().Add(sseWriteDeadline) // Write deadline timeout
		if err := conn.SetWriteDeadline(deadline); err != nil {
			// If we can't set deadline, log but continue - not all response writers support this
			c.logDebugIfEnabled("Failed to set write deadline for SSE message", logger.Error(err))
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

// safeMarshalJSON marshals data to JSON with panic recovery.
// This protects against panics from concurrent map access or unmarshalable data.
func (c *Controller) safeMarshalJSON(event string, data any) (jsonData []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("JSON marshal panic: %v", r)
			c.logErrorIfEnabled("SSE marshal panic recovered",
				logger.String("event", event),
				logger.Any("panic", r),
				logger.String("stack", string(debug.Stack())),
			)
		}
	}()
	return json.Marshal(data)
}

// GetSSEStatus returns information about SSE connections
func (c *Controller) GetSSEStatus(ctx echo.Context) error {
	if c.sseManager == nil {
		return ctx.JSON(http.StatusOK, map[string]any{
			"connected_clients": 0,
			"status":            "disabled",
		})
	}

	return ctx.JSON(http.StatusOK, map[string]any{
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
		c.logErrorIfEnabled("SSE broadcast skipped: note is nil")
		return fmt.Errorf("note is nil")
	}
	if birdImage == nil {
		c.logErrorIfEnabled("SSE broadcast skipped: birdImage is nil")
		return fmt.Errorf("birdImage is nil")
	}

	detection := SSEDetectionData{
		Note:      *note,
		BirdImage: *birdImage,
		Timestamp: time.Now(),
		EventType: "new_detection",
	}

	// Add species tracking metadata if processor has tracker
	if c.Processor != nil && c.Processor.NewSpeciesTracker != nil {
		status := c.Processor.NewSpeciesTracker.GetSpeciesStatus(note.ScientificName, time.Now())
		detection.IsNewSpecies = status.IsNew
		detection.DaysSinceFirstSeen = status.DaysSinceFirst
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
		c.logErrorIfEnabled("SSE broadcast skipped: soundLevel is nil")
		return fmt.Errorf("soundLevel is nil")
	}

	sseData := SSESoundLevelData{
		SoundLevelData: *soundLevel,
		EventType:      "sound_level_update",
	}

	c.sseManager.BroadcastSoundLevel(&sseData)
	return nil
}
