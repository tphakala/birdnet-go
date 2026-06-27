// Package sse implements the Server-Sent Events ENDPOINT handlers for the v2
// API (the real-time detection and sound-level streams plus the SSE status
// endpoint). The SSE hub itself (SSEManager, broadcasters, SSEClient, the wire
// structs and the low-level write primitives) lives in apicore and is shared by
// every SSE producer; this package only owns the stream endpoints and their
// per-request event loops.
package sse

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// SSE connection configuration
const (
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

// Handler serves the SSE stream endpoints. It embeds *apicore.Core by pointer so
// the shared SSE hub (SSEManager), write primitives (SendSSEMessage,
// RecordSSE*), stream scaffolding (SendSSEHeartbeat, LogSSEConnection,
// SendConnectionMessage) and logging helpers promote directly onto it.
//
// isClientAuthenticated is injected from the facade (the auth-service check that
// lives on facade-owned state, matching the detections/analytics domains). The
// detection stream evaluates it ONCE at connect time so unauthenticated
// subscribers receive a sanitized payload that omits the raw audio source
// DisplayName (which can embed internal host details), exposing only the stable
// Source.ID.
type Handler struct {
	*apicore.Core

	isClientAuthenticated func(ctx echo.Context) bool
}

// New constructs the SSE endpoint handler from the shared core and the
// facade-injected auth check. isClientAuthenticated is read once per connection
// (at connect time) to decide whether the detection stream exposes the raw source
// DisplayName to that subscriber.
func New(core *apicore.Core, isClientAuthenticated func(ctx echo.Context) bool) *Handler {
	return &Handler{
		Core:                  core,
		isClientAuthenticated: isClientAuthenticated,
	}
}

// RegisterRoutes registers the SSE-related API endpoints on the given group,
// preserving the exact routes, order and per-route middleware (the SSE rate
// limiter on the two stream endpoints) from the original initSSERoutes.
func (c *Handler) RegisterRoutes(g *echo.Group) {
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
	g.GET("/detections/stream", c.StreamDetections, middleware.RateLimiterWithConfig(rateLimiterConfig))

	// SSE endpoint for sound level stream with rate limiting
	g.GET("/soundlevels/stream", c.StreamSoundLevels, middleware.RateLimiterWithConfig(rateLimiterConfig))

	// SSE status endpoint - shows connected client count
	g.GET("/sse/status", c.GetSSEStatus)
}

// createSSEClient creates a new SSE client with common settings
func createSSEClient(clientID string, ctx echo.Context, streamType string) *apicore.SSEClient {
	return &apicore.SSEClient{
		ID:         clientID,
		Request:    ctx.Request(),
		Response:   ctx.Response(),
		Done:       make(chan struct{}, sseDoneChannelBuffer), // Signal-only buffered channel to prevent blocking on cleanup
		StreamType: streamType,
	}
}

// handleSSEStream handles the common SSE stream setup and teardown with timeout protection
func (c *Handler) handleSSEStream(ctx echo.Context, streamType, message, logPrefix string, setupFunc func(*apicore.SSEClient), eventLoop func(echo.Context, *apicore.SSEClient, string) error) error {
	// Track connection start time for metrics
	connectionStartTime := time.Now()

	// Track metrics if available
	endpoint := ""
	switch streamType {
	case apicore.StreamTypeDetections:
		endpoint = detectionStreamEndpoint
	case apicore.StreamTypeSoundLevels:
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
	timeoutCtx, cancel := context.WithTimeout(ctx.Request().Context(), apicore.MaxSSEStreamDuration)
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
	if err := c.SendConnectionMessage(ctx, clientID, message, streamType); err != nil {
		c.SSEManager.RemoveClient(clientID)
		return err
	}

	// Log the connection
	c.LogSSEConnection(clientID, ctx.RealIP(), ctx.Request().UserAgent(), logPrefix, true)

	// Handle the SSE connection
	defer func() {
		c.SSEManager.RemoveClient(clientID)
		c.LogSSEConnection(clientID, ctx.RealIP(), "", logPrefix, false)
	}()

	// Run the event loop
	return eventLoop(ctx, client, clientID)
}

// StreamDetections handles the SSE connection for real-time detection streaming
func (c *Handler) StreamDetections(ctx echo.Context) error {
	return c.handleSSEStream(ctx, apicore.StreamTypeDetections, "Connected to detection stream", "detection",
		func(client *apicore.SSEClient) {
			client.Channel = make(chan apicore.SSEDetectionData, sseDetectionBufferSize) // Buffer for high detection periods
			client.PendingChan = make(chan any, ssePendingBufferSize)                    // Buffer for pending detection snapshots
			// Capture the authentication state ONCE at connect time (mirroring
			// StreamAudioLevel). setupFunc runs synchronously before the client is
			// registered and before the event loop starts, so this snapshot is
			// race-free and is read by the per-client send path to decide whether to
			// expose the raw source DisplayName.
			client.Authenticated = c.clientAuthenticated(ctx)
		},
		func(ctx echo.Context, client *apicore.SSEClient, clientID string) error {
			return c.runSSEEventLoopMulti(ctx, client, clientID, detectionStreamEndpoint)
		})
}

// clientAuthenticated reports whether the current request is authenticated,
// delegating to the facade-injected auth check. The facade always injects a
// non-nil bound method (api.go), so the nil branch is defense-in-depth for this
// privacy-sensitive public stream: a missing check fails closed (the client is
// treated as unauthenticated and the source is anonymized) instead of panicking.
// The injected facade method itself returns false when no auth service is
// configured. The detections/analytics/audio siblings call their injected check
// directly without this extra guard; keeping it here is deliberate.
func (c *Handler) clientAuthenticated(ctx echo.Context) bool {
	if c.isClientAuthenticated == nil {
		return false
	}
	return c.isClientAuthenticated(ctx)
}

// sanitizeDetectionForUnauthenticated returns a COPY of a detection event with the
// audio source DisplayName stripped, exposing only the stable Source.ID to
// unauthenticated subscribers. The source DisplayName can embed internal host
// details (for stream sources without a user-configured name), so it must never
// reach unauthenticated clients.
//
// Deliberate divergence from the detections REST/search endpoints: those strip the
// entire Source (id included) for unauthenticated clients, whereas this stream
// keeps Source.ID. The stream follows the StreamAudioLevel precedent (which exposes
// the raw source id and only anonymizes the display name) and the issue's explicit
// guidance ("only expose the stable Source.ID"). The id is an opaque, stable
// identifier the live UI needs to distinguish/correlate sources, and DisplayName is
// the only field that can carry sensitive host details.
//
// The returned event rebuilds Source as a FRESH pointer. The broadcast struct is
// fanned out to every client as a shared *apicore.SSESourceInfo (BroadcastDetection
// does `client.Channel <- *detection`, which copies the struct VALUE per client but
// shares the Source pointer), so mutating Source in place would race across clients
// under -race and corrupt authenticated clients' payloads. The function copies the
// dereferenced value first and rebinds Source only on that local copy, so the
// caller's struct and shared pointer are left untouched. The input is taken by
// pointer to avoid copying the (large) value twice.
func sanitizeDetectionForUnauthenticated(event *apicore.SSEDetectionData) apicore.SSEDetectionData {
	out := *event
	if out.Source != nil {
		out.Source = &apicore.SSESourceInfo{ID: out.Source.ID}
	}
	return out
}

// detectionPayloadForClient returns the detection payload to serialize for a single
// subscriber. Authenticated subscribers receive the event unchanged (full
// DisplayName, via the shared pointer, which is never mutated); unauthenticated
// subscribers receive a sanitized copy exposing only the stable Source.ID.
func detectionPayloadForClient(event *apicore.SSEDetectionData, authenticated bool) apicore.SSEDetectionData {
	if authenticated {
		return *event
	}
	return sanitizeDetectionForUnauthenticated(event)
}

// sanitizePendingForUnauthenticated returns a sanitized COPY of a pending detection
// snapshot for unauthenticated subscribers: each item's Source (the audio source
// display name) is stripped, leaving only the stable SourceID.
//
// PendingChan is typed `any`; the only payload the broadcaster sends today is
// []processor.SSEPendingDetection (verified at the BroadcastPending call site). An
// unexpected concrete type fails CLOSED, returning an empty pending list rather
// than forwarding an unknown (potentially display-name-bearing) payload to an
// unauthenticated client. This matches clientAuthenticated's fail-closed posture.
//
// The pending snapshot is broadcast as a single shared slice to every detection
// client (BroadcastPending sends the same []processor.SSEPendingDetection to all
// PendingChan channels), so all clients alias the same backing array. A fresh slice
// of copied, sanitized elements is allocated; the shared array is never mutated,
// which would race across clients and corrupt authenticated clients' payloads.
func sanitizePendingForUnauthenticated(pending any) any {
	items, ok := pending.([]processor.SSEPendingDetection)
	if !ok {
		return []processor.SSEPendingDetection{}
	}
	sanitized := make([]processor.SSEPendingDetection, len(items))
	for i := range items {
		item := items[i] // copy the struct value (does not touch the shared array)
		item.Source = "" // strip the display name; SourceID is retained for filtering
		sanitized[i] = item
	}
	return sanitized
}

// pendingPayloadForClient returns the pending snapshot to serialize for a single
// subscriber: authenticated subscribers receive it unchanged; unauthenticated
// subscribers receive a sanitized copy with the per-item display name removed.
func pendingPayloadForClient(pending any, authenticated bool) any {
	if authenticated {
		return pending
	}
	return sanitizePendingForUnauthenticated(pending)
}

// runSSEEventLoopMulti handles the SSE event loop for detection streams,
// which receive both detection events and pending detection snapshots.
func (c *Handler) runSSEEventLoopMulti(ctx echo.Context, client *apicore.SSEClient, clientID, endpoint string) error {
	ticker := time.NewTicker(apicore.SSEHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := c.SendSSEHeartbeat(ctx, clientID, ""); err != nil {
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
				// Per-client send path: unauthenticated subscribers receive a copy
				// with the source DisplayName stripped (only Source.ID exposed). The
				// shared broadcast struct is never mutated; see
				// sanitizeDetectionForUnauthenticated.
				payload := detectionPayloadForClient(&detection, client.Authenticated)
				if err := c.SendSSEMessage(ctx, "detection", payload); err != nil {
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
					// Same per-client anonymization as the detection path: the
					// pending snapshot also carries the source display name, so
					// unauthenticated subscribers receive a sanitized copy.
					pending = pendingPayloadForClient(pending, client.Authenticated)
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
func (c *Handler) StreamSoundLevels(ctx echo.Context) error {
	return c.handleSSEStream(ctx, apicore.StreamTypeSoundLevels, "Connected to sound level stream", "sound level",
		func(client *apicore.SSEClient) {
			client.Channel = make(chan apicore.SSEDetectionData, sseMinimalBufferSize)            // Minimal buffer, not used for sound levels
			client.SoundLevelChan = make(chan apicore.SSESoundLevelData, sseSoundLevelBufferSize) // Buffer for sound level data
		},
		func(ctx echo.Context, client *apicore.SSEClient, clientID string) error {
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
				apicore.StreamTypeSoundLevels,
			)
		})
}

// runSSEEventLoop handles the common SSE event loop pattern for all stream types
func (c *Handler) runSSEEventLoop(ctx echo.Context, client *apicore.SSEClient, clientID string, endpoint string,
	dataReceiver func() (any, bool), eventType string, heartbeatType string) error {

	ticker := time.NewTicker(apicore.SSEHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Send heartbeat
			if err := c.SendSSEHeartbeat(ctx, clientID, heartbeatType); err != nil {
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
func (c *Handler) GetSSEStatus(ctx echo.Context) error {
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
