package apicore

import (
	"time"

	"github.com/labstack/echo/v4"
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
)

// SetSSEHeaders sets the required HTTP headers for a Server-Sent Events response.
func SetSSEHeaders(ctx echo.Context) {
	ctx.Response().Header().Set("Content-Type", "text/event-stream")
	ctx.Response().Header().Set("Cache-Control", "no-cache")
	ctx.Response().Header().Set("Connection", "keep-alive")
	ctx.Response().Header().Set("Access-Control-Allow-Origin", "*")
	ctx.Response().Header().Set("Access-Control-Allow-Headers", "Cache-Control")
}
