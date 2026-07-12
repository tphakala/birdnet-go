package apicore

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Server-Sent Events streaming configuration shared by every SSE endpoint
// (detection/sound-level streams, notifications, metrics history, import
// progress, model install progress). Keeping these in apicore gives every
// domain a single source of truth so the streaming behavior cannot drift.
const (
	// SSEHeartbeatInterval is the keep-alive heartbeat interval. It must stay
	// below the server WriteTimeout so a quiet stream is not torn down.
	SSEHeartbeatInterval = 15 * time.Second

	// SSEEventLoopSleep is the sleep duration used to throttle an SSE poll loop
	// when there are no events to send.
	SSEEventLoopSleep = 10 * time.Millisecond

	// SSEWriteDeadline bounds how long a single SSE message write may block on a
	// slow or disconnected client before it is abandoned.
	SSEWriteDeadline = 10 * time.Second

	// MaxSSEStreamDuration is the maximum lifetime of a single SSE stream
	// connection before it is closed to prevent resource leaks. It is shared by
	// every SSE endpoint (detection/sound-level streams, stream health, import
	// progress) so the timeout cannot drift between domains.
	MaxSSEStreamDuration = 30 * time.Minute
)

// SSE connection status strings used in connection/heartbeat log lines and the
// initial connection event. Shared by every SSE endpoint.
const (
	// SSEStatusConnected marks an established SSE client connection.
	SSEStatusConnected = "connected"
	// SSEStatusDisconnected marks a torn-down SSE client connection.
	SSEStatusDisconnected = "disconnected"
)

// WriteDeadlineSetter is implemented by response writers that support a write
// deadline (e.g. the underlying TCP connection). SSE writers use it to avoid
// hanging on stalled clients.
type WriteDeadlineSetter interface {
	SetWriteDeadline(time.Time) error
}

// SetSSEHeaders sets the required HTTP headers for a Server-Sent Events response.
func SetSSEHeaders(ctx echo.Context) {
	ctx.Response().Header().Set("Content-Type", "text/event-stream")
	ctx.Response().Header().Set("Cache-Control", "no-cache")
	ctx.Response().Header().Set("Connection", "keep-alive")
	ctx.Response().Header().Set("Access-Control-Allow-Origin", "*")
	ctx.Response().Header().Set("Access-Control-Allow-Headers", "Cache-Control")
	DisableProxyBuffering(ctx)
}

// DisableProxyBuffering sets X-Accel-Buffering: no so nginx and compatible
// reverse proxies stream the response immediately instead of buffering it.
// Streaming endpoints that build their own headers (SSE as well as the
// NDJSON/JSON progress streams) call this so the buffering behavior has a single
// source of truth and cannot drift between endpoints.
func DisableProxyBuffering(ctx echo.Context) {
	ctx.Response().Header().Set("X-Accel-Buffering", "no")
}

// SetStreamWriteDeadline applies the shared SSE write deadline to the response
// writer when it supports one, so a stalled or disconnected client cannot block
// the writer indefinitely. It is best-effort: writers that do not implement
// WriteDeadlineSetter are left unchanged, and a SetWriteDeadline failure is logged
// at debug and otherwise ignored (the deadline is advisory). Streaming handlers
// call it before each streamed write, like SendSSEMessage does inline.
func SetStreamWriteDeadline(ctx echo.Context) {
	conn, ok := ctx.Response().Writer.(WriteDeadlineSetter)
	if !ok {
		return
	}
	if err := conn.SetWriteDeadline(time.Now().Add(SSEWriteDeadline)); err != nil {
		GetLogger().Debug("Failed to set stream write deadline", logger.Error(err))
	}
}

// SafeMarshalJSON marshals data to JSON with panic recovery.
// This protects against panics from concurrent map access or unmarshalable data.
func (c *Core) SafeMarshalJSON(event string, data any) (jsonData []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("JSON marshal panic: %v", r)
			c.LogErrorIfEnabled("SSE marshal panic recovered",
				logger.String("event", event),
				logger.Any("panic", r),
				logger.String("stack", string(debug.Stack())),
			)
			_ = errors.Newf("SSE JSON marshal panic: %v", r).
				Component("api").
				Category(errors.CategoryBroadcast).
				Context("operation", "sse_marshal_panic").
				Priority(errors.PriorityCritical).
				Build()
		}
	}()
	return json.Marshal(data)
}

// SendSSEMessage sends a Server-Sent Event message. It applies a write deadline
// so a stalled client cannot block the writer, and flushes so the client
// receives the event promptly.
func (c *Core) SendSSEMessage(ctx echo.Context, event string, data any) error {
	// Convert data to JSON with panic recovery
	jsonData, err := c.SafeMarshalJSON(event, data)
	if err != nil {
		return fmt.Errorf("failed to marshal SSE data: %w", err)
	}

	// Format SSE message
	message := fmt.Sprintf("event: %s\ndata: %s\n\n", event, string(jsonData))

	// Set write deadline to prevent hanging on slow/disconnected clients
	if conn, ok := ctx.Response().Writer.(WriteDeadlineSetter); ok {
		deadline := time.Now().Add(SSEWriteDeadline) // Write deadline timeout
		if err := conn.SetWriteDeadline(deadline); err != nil {
			// If we can't set deadline, log but continue - not all response writers support this
			c.LogDebugIfEnabled("Failed to set write deadline for SSE message", logger.Error(err))
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

// RecordSSEError records an SSE error metric if metrics are available.
func (c *Core) RecordSSEError(endpoint, errorType string) {
	if c.Metrics != nil && c.Metrics.HTTP != nil {
		c.Metrics.HTTP.RecordSSEError(endpoint, errorType)
	}
}

// RecordSSEMessage records an SSE message-sent metric if metrics are available.
func (c *Core) RecordSSEMessage(endpoint, messageType string) {
	if c.Metrics != nil && c.Metrics.HTTP != nil {
		c.Metrics.HTTP.RecordSSEMessageSent(endpoint, messageType)
	}
}

// SendConnectionMessage sends the initial connection message to an SSE client.
// Shared by every SSE endpoint (detection/sound-level streams, stream health).
func (c *Core) SendConnectionMessage(ctx echo.Context, clientID, message, streamType string) error {
	data := map[string]string{
		"clientId": clientID,
		"message":  message,
	}
	if streamType != "" {
		data["type"] = streamType
	}
	return c.SendSSEMessage(ctx, SSEStatusConnected, data)
}

// LogSSEConnection logs SSE client connection/disconnection events. Shared by
// every SSE endpoint.
func (c *Core) LogSSEConnection(clientID, ip, userAgent, streamType string, connected bool) {
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

// SendSSEHeartbeat sends a heartbeat message to keep an SSE connection alive.
// Shared by every SSE endpoint.
func (c *Core) SendSSEHeartbeat(ctx echo.Context, clientID, streamType string) error {
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
