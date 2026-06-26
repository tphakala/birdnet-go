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
