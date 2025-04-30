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

// RestartAnalysis handles POST /api/v2/control/restart
// Restarts the audio analysis process
func (c *Controller) RestartAnalysis(ctx echo.Context) error {
	if c.apiLogger != nil {
		c.apiLogger.Info("Received request to restart analysis",
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	if c.controlChan == nil {
		err := fmt.Errorf("control channel not initialized")
		if c.apiLogger != nil {
			c.apiLogger.Error("Control channel not available for restart analysis",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, err,
			"System control interface not available - server may need to be restarted", http.StatusInternalServerError)
	}

	c.Debug("API requested analysis restart")

	// Get request context
	reqCtx := ctx.Request().Context()

	// Send restart signal with context timeout awareness
	select {
	case c.controlChan <- SignalRestartAnalysis:
		if c.apiLogger != nil {
			c.apiLogger.Info("Analysis restart signal sent successfully",
				"action", ActionRestartAnalysis,
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		// Signal sent successfully
	case <-reqCtx.Done():
		err := reqCtx.Err()
		if c.apiLogger != nil {
			c.apiLogger.Error("Request timeout/cancel while sending restart analysis signal",
				"error", err.Error(),
				"action", ActionRestartAnalysis,
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
		Message:   "Analysis restart signal sent",
		Action:    ActionRestartAnalysis,
		Timestamp: time.Now(),
	})
}

// ReloadModel handles POST /api/v2/control/reload
// Reloads the BirdNET model
func (c *Controller) ReloadModel(ctx echo.Context) error {
	if c.apiLogger != nil {
		c.apiLogger.Info("Received request to reload model",
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	if c.controlChan == nil {
		err := fmt.Errorf("control channel not initialized")
		if c.apiLogger != nil {
			c.apiLogger.Error("Control channel not available for reload model",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, err,
			"System control interface not available - server may need to be restarted", http.StatusInternalServerError)
	}

	c.Debug("API requested model reload")

	// Get request context
	reqCtx := ctx.Request().Context()

	// Send reload signal with context timeout awareness
	select {
	case c.controlChan <- SignalReloadModel:
		if c.apiLogger != nil {
			c.apiLogger.Info("Model reload signal sent successfully",
				"action", ActionReloadModel,
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		// Signal sent successfully
	case <-reqCtx.Done():
		err := reqCtx.Err()
		if c.apiLogger != nil {
			c.apiLogger.Error("Request timeout/cancel while sending reload model signal",
				"error", err.Error(),
				"action", ActionReloadModel,
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
		Message:   "Model reload signal sent",
		Action:    ActionReloadModel,
		Timestamp: time.Now(),
	})
}

// RebuildFilter handles POST /api/v2/control/rebuild-filter
// Rebuilds the species filter based on current location
func (c *Controller) RebuildFilter(ctx echo.Context) error {
	if c.apiLogger != nil {
		c.apiLogger.Info("Received request to rebuild species filter",
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	if c.controlChan == nil {
		err := fmt.Errorf("control channel not initialized")
		if c.apiLogger != nil {
			c.apiLogger.Error("Control channel not available for rebuild filter",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, err,
			"System control interface not available - server may need to be restarted", http.StatusInternalServerError)
	}

	c.Debug("API requested species filter rebuild")

	// Get request context
	reqCtx := ctx.Request().Context()

	// Send rebuild filter signal with context timeout awareness
	select {
	case c.controlChan <- SignalRebuildFilter:
		if c.apiLogger != nil {
			c.apiLogger.Info("Filter rebuild signal sent successfully",
				"action", ActionRebuildFilter,
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		// Signal sent successfully
	case <-reqCtx.Done():
		err := reqCtx.Err()
		if c.apiLogger != nil {
			c.apiLogger.Error("Request timeout/cancel while sending rebuild filter signal",
				"error", err.Error(),
				"action", ActionRebuildFilter,
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
		Message:   "Filter rebuild signal sent",
		Action:    ActionRebuildFilter,
		Timestamp: time.Now(),
	})
}
