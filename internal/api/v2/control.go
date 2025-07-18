// internal/api/v2/control.go
package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
)

// ControlAction represents a control action request
type ControlAction struct {
	Action      string `json:"action"`
	Description string `json:"description"`
}

// ControlResult represents the result of a control action
type ControlResult struct {
	Success   bool      `json:"success"`
	Message   string    `json:"message"`
	Action    string    `json:"action"`
	Timestamp time.Time `json:"timestamp"`
}

// Available control actions
const (
	ActionRestartAnalysis = "restart_analysis"
	ActionReloadModel     = "reload_model"
	ActionRebuildFilter   = "rebuild_filter"
)

// Control channel signals
const (
	SignalRestartAnalysis = "restart_analysis"
	SignalReloadModel     = "reload_birdnet"
	SignalRebuildFilter   = "rebuild_range_filter"
)

// initControlRoutes registers all control-related API endpoints
func (c *Controller) initControlRoutes() {
	if c.apiLogger != nil {
		c.apiLogger.Info("Initializing control routes")
	}

	// Create control API group with auth middleware
	controlGroup := c.Group.Group("/control", c.AuthMiddleware)

	// Control routes
	controlGroup.POST("/restart", c.RestartAnalysis)
	controlGroup.POST("/reload", c.ReloadModel)
	controlGroup.POST("/rebuild-filter", c.RebuildFilter)
	controlGroup.GET("/actions", c.GetAvailableActions)

	if c.apiLogger != nil {
		c.apiLogger.Info("Control routes initialized successfully")
	}
}

// GetAvailableActions handles GET /api/v2/control/actions
// Returns a list of available control actions
func (c *Controller) GetAvailableActions(ctx echo.Context) error {
	if c.apiLogger != nil {
		c.apiLogger.Info("Getting available control actions",
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	actions := []ControlAction{
		{
			Action:      ActionRestartAnalysis,
			Description: "Restart the audio analysis process",
		},
		{
			Action:      ActionReloadModel,
			Description: "Reload the BirdNET model",
		},
		{
			Action:      ActionRebuildFilter,
			Description: "Rebuild the species filter based on current location",
		},
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieved available control actions successfully",
			"action_count", len(actions),
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	return ctx.JSON(http.StatusOK, actions)
}

// handleControlSignal is a common handler for control operations
func (c *Controller) handleControlSignal(ctx echo.Context, signal, action, logMessage, successMessage string) error {
	if c.apiLogger != nil {
		c.apiLogger.Info(logMessage,
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	if c.controlChan == nil {
		err := fmt.Errorf("control channel not initialized")
		if c.apiLogger != nil {
			c.apiLogger.Error("Control channel not available",
				"error", err.Error(),
				"action", action,
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, err,
			"System control interface not available - server may need to be restarted", http.StatusInternalServerError)
	}

	c.Debug("API requested %s", action)

	// Get request context
	reqCtx := ctx.Request().Context()

	// Send signal with context timeout awareness
	select {
	case c.controlChan <- signal:
		if c.apiLogger != nil {
			c.apiLogger.Info("Control signal sent successfully",
				"action", action,
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		// Signal sent successfully
	case <-reqCtx.Done():
		err := reqCtx.Err()
		if c.apiLogger != nil {
			c.apiLogger.Error("Request timeout/cancel while sending control signal",
				"error", err.Error(),
				"action", action,
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		// Request context is done (timeout or cancelled)
		return c.HandleError(ctx, err,
			"Request timeout while sending control signal", http.StatusRequestTimeout)
	}

	return ctx.JSON(http.StatusOK, ControlResult{
		Success:   true,
		Message:   successMessage,
		Action:    action,
		Timestamp: time.Now(),
	})
}

// RestartAnalysis handles POST /api/v2/control/restart
// Restarts the audio analysis process
func (c *Controller) RestartAnalysis(ctx echo.Context) error {
	return c.handleControlSignal(ctx, SignalRestartAnalysis, ActionRestartAnalysis,
		"Received request to restart analysis", "Analysis restart signal sent")
}

// ReloadModel handles POST /api/v2/control/reload
// Reloads the BirdNET model
func (c *Controller) ReloadModel(ctx echo.Context) error {
	return c.handleControlSignal(ctx, SignalReloadModel, ActionReloadModel,
		"Received request to reload model", "Model reload signal sent")
}

// RebuildFilter handles POST /api/v2/control/rebuild-filter
// Rebuilds the species filter based on current location
func (c *Controller) RebuildFilter(ctx echo.Context) error {
	return c.handleControlSignal(ctx, SignalRebuildFilter, ActionRebuildFilter,
		"Received request to rebuild species filter", "Filter rebuild signal sent")
}
