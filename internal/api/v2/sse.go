// internal/api/v2/sse.go
package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// SSE connection configuration
const (
	// Connection timeouts
	maxSSEStreamDuration = 30 * time.Minute // Maximum stream duration to prevent resource leaks

	// Endpoints
	detectionStreamEndpoint  = "/api/v2/detections/stream"
	soundLevelStreamEndpoint = "/api/v2/soundlevels/stream"

	// Buffer sizes
	sseDetectionBufferSize  = 100 // Buffer size for detection channels (high volume)
	sseSoundLevelBufferSize = 100 // Buffer size for sound level channels
	ssePendingBufferSize    = 10  // Buffer size for pending detection channels
	sseMinimalBufferSize    = 1   // Minimal buffer for unused channels
	sseDoneChannelBuffer    = 1   // Buffer for Done channels to prevent blocking

	// Rate limits
	sseRateLimitRequests = 10              // SSE rate limit requests per window
	sseRateLimitWindow   = 1 * time.Minute // SSE rate limit time window
)

// SSEEvent represents a generic SSE event that can contain different data types
type SSEEvent struct {
	Type      string    `json:"type"`
	Data      any       `json:"data"`
	Timestamp time.Time `json:"timestamp"`
}

// initSSERoutes registers SSE-related API endpoints
func (c *Controller) initSSERoutes() {
	// Initialize SSE manager if not already done
	if c.SSEManager == nil {
		c.SSEManager = apicore.NewSSEManager()
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
	return c.SendSSEMessage(ctx, SSEStatusConnected, data)
}

// logSSEConnection logs SSE client connection/disconnection events
func (c *Controller) logSSEConnection(clientID, ip, userAgent, streamType string, connected bool) {
	action := SSEStatusConnected
	if !connected {
		action = SSEStatusDisconnected
	}

	c.LogInfoIfEnabled(fmt.Sprintf("SSE %s client %s", streamType, action),
		logger.String("client_id", clientID),
		logger.String("ip", ip),
		logger.String("user_agent", userAgent),
	)
}

// sendSSEHeartbeat sends a heartbeat message to keep the connection alive
func (c *Controller) sendSSEHeartbeat(ctx echo.Context, clientID, streamType string) error {
	data := map[string]any{
		"timestamp": time.Now().Unix(),
		"clients":   c.SSEManager.GetClientCount(),
	}
	if streamType != "" {
		data["type"] = streamType
	}

	if err := c.SendSSEMessage(ctx, "heartbeat", data); err != nil {
		c.LogDebugIfEnabled("SSE heartbeat failed, client likely disconnected",
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

	if c.Metrics != nil && c.Metrics.HTTP != nil && endpoint != "" {
		c.Metrics.HTTP.SSEConnectionStarted(endpoint)
		defer func() {
			duration := time.Since(connectionStartTime).Seconds()
			closeReason := metrics.SSECloseReasonClosed
			if ctx.Request().Context().Err() == context.DeadlineExceeded {
				closeReason = metrics.SSECloseReasonTimeout
			} else if ctx.Request().Context().Err() == context.Canceled {
				closeReason = metrics.SSECloseReasonCanceled
			}
			c.Metrics.HTTP.SSEConnectionClosed(endpoint, duration, closeReason)
		}()
	}

	// Create a context with timeout for maximum connection duration
	timeoutCtx, cancel := context.WithTimeout(ctx.Request().Context(), maxSSEStreamDuration)
	defer cancel()

	// Override the request context with timeout context
	originalReq := ctx.Request()
	ctx.SetRequest(originalReq.WithContext(timeoutCtx))

	// Set SSE headers
	apicore.SetSSEHeaders(ctx)

	// Generate client ID and create client
	clientID := apicore.GenerateCorrelationID()
	client := createSSEClient(clientID, ctx, streamType)

	// Allow custom setup
	if setupFunc != nil {
		setupFunc(client)
	}

	// Add client to manager (rejected during shutdown)
	if !c.SSEManager.AddClient(client) {
		return ctx.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "Server is shutting down",
		})
	}

	// Send initial connection message
	if err := c.sendConnectionMessage(ctx, clientID, message, streamType); err != nil {
		c.SSEManager.RemoveClient(clientID)
		return err
	}

	// Log the connection
	c.logSSEConnection(clientID, ctx.RealIP(), ctx.Request().UserAgent(), logPrefix, true)

	// Handle the SSE connection
	defer func() {
		c.SSEManager.RemoveClient(clientID)
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
			client.PendingChan = make(chan any, ssePendingBufferSize)            // Buffer for pending detection snapshots
		},
		func(ctx echo.Context, client *SSEClient, clientID string) error {
			return c.runSSEEventLoopMulti(ctx, client, clientID, detectionStreamEndpoint)
		})
}

// runSSEEventLoopMulti handles the SSE event loop for detection streams,
// which receive both detection events and pending detection snapshots.
func (c *Controller) runSSEEventLoopMulti(ctx echo.Context, client *SSEClient, clientID, endpoint string) error {
	ticker := time.NewTicker(apicore.SSEHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := c.sendSSEHeartbeat(ctx, clientID, ""); err != nil {
				c.RecordSSEError(endpoint, "heartbeat_failed")
				return err
			}
			c.RecordSSEMessage(endpoint, "heartbeat")

		case <-ctx.Request().Context().Done():
			return nil

		case <-client.Done:
			return nil

		default:
			sent := false

			// Check for detection data
			select {
			case detection, ok := <-client.Channel:
				if !ok {
					return nil
				}
				if err := c.SendSSEMessage(ctx, "detection", detection); err != nil {
					c.LogErrorIfEnabled("Failed to send SSE detection",
						logger.String("client_id", clientID),
						logger.String("endpoint", endpoint),
						logger.Error(err),
					)
					c.RecordSSEError(endpoint, "send_failed")
					return err
				}
				c.RecordSSEMessage(endpoint, "detection")
				sent = true
			default:
			}

			// Check for pending data
			if client.PendingChan != nil {
				select {
				case pending, ok := <-client.PendingChan:
					if !ok {
						return nil
					}
					if err := c.SendSSEMessage(ctx, "pending", pending); err != nil {
						c.LogErrorIfEnabled("Failed to send SSE pending",
							logger.String("client_id", clientID),
							logger.String("endpoint", endpoint),
							logger.Error(err),
						)
						c.RecordSSEError(endpoint, "send_failed")
						return err
					}
					c.RecordSSEMessage(endpoint, "pending")
					sent = true
				default:
				}
			}

			if !sent {
				time.Sleep(apicore.SSEEventLoopSleep)
			}
		}
	}
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

	ticker := time.NewTicker(apicore.SSEHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Send heartbeat
			if err := c.sendSSEHeartbeat(ctx, clientID, heartbeatType); err != nil {
				c.RecordSSEError(endpoint, "heartbeat_failed")
				return err
			}
			c.RecordSSEMessage(endpoint, "heartbeat")

		case <-ctx.Request().Context().Done():
			// Client disconnected
			return nil

		case <-client.Done:
			// Client marked for removal
			return nil

		default:
			// Check for data on the channel (non-blocking)
			if data, hasData := dataReceiver(); hasData {
				if err := c.SendSSEMessage(ctx, eventType, data); err != nil {
					c.LogErrorIfEnabled("Failed to send SSE message",
						logger.String("client_id", clientID),
						logger.String("endpoint", endpoint),
						logger.String("event_type", eventType),
						logger.Error(err),
					)
					c.RecordSSEError(endpoint, "send_failed")
					return err
				}
				c.RecordSSEMessage(endpoint, eventType)
			} else {
				// Small sleep to prevent busy-waiting when no data
				time.Sleep(apicore.SSEEventLoopSleep)
			}
		}
	}
}

// GetSSEStatus returns information about SSE connections
func (c *Controller) GetSSEStatus(ctx echo.Context) error {
	if c.SSEManager == nil {
		return ctx.JSON(http.StatusOK, map[string]any{
			"connected_clients": 0,
			"status":            "disabled",
		})
	}

	return ctx.JSON(http.StatusOK, map[string]any{
		"connected_clients": c.SSEManager.GetClientCount(),
		"status":            "active",
	})
}
