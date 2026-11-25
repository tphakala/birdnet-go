// internal/api/v2/detections/enhanced_database.go
package detections

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// respondEnhancedDatabaseRequired answers 409 with message for a route that needs the
// enhanced (v2) database on an install that does not use it. It writes one Debug log
// line with logMsg and no Error line, so normal use of a legacy install does not fill
// the health error buffer.
func (c *Handler) respondEnhancedDatabaseRequired(ctx echo.Context, message, logMsg string) error {
	resp := c.NewErrorResponse(nil, message, http.StatusConflict)
	c.LogDebugIfEnabled(logMsg,
		logger.String("correlation_id", resp.CorrelationID),
		logger.String("path", apicore.RoutePattern(ctx)),
	)
	return ctx.JSON(http.StatusConflict, resp)
}
