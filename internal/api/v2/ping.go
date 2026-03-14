package api

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// Ping returns a minimal response for connectivity checks.
// Unlike HealthCheck, it performs no database or system checks.
func (c *Controller) Ping(ctx echo.Context) error {
	return ctx.JSON(http.StatusOK, map[string]string{"status": "ok"})
}
