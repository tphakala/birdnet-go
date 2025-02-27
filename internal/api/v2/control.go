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
	// Create control API group with auth middleware
	controlGroup := c.Group.Group("/control", c.AuthMiddleware)

	// Control routes
	controlGroup.POST("/restart", c.RestartAnalysis)
	controlGroup.POST("/reload", c.ReloadModel)
	controlGroup.POST("/rebuild-filter", c.RebuildFilter)
	controlGroup.GET("/actions", c.GetAvailableActions)
}

// GetAvailableActions handles GET /api/v2/control/actions
// Returns a list of available control actions
func (c *Controller) GetAvailableActions(ctx echo.Context) error {
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

	return ctx.JSON(http.StatusOK, actions)
}

// RestartAnalysis handles POST /api/v2/control/restart
// Restarts the audio analysis process
func (c *Controller) RestartAnalysis(ctx echo.Context) error {
	if c.controlChan == nil {
		return c.HandleError(ctx, fmt.Errorf("control channel not initialized"),
			"System control interface not available - server may need to be restarted", http.StatusInternalServerError)
	}

	c.Debug("API requested analysis restart")

	// Send restart signal
	c.controlChan <- SignalRestartAnalysis

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
	if c.controlChan == nil {
		return c.HandleError(ctx, fmt.Errorf("control channel not initialized"),
			"System control interface not available - server may need to be restarted", http.StatusInternalServerError)
	}

	c.Debug("API requested model reload")

	// Send reload signal
	c.controlChan <- SignalReloadModel

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
	if c.controlChan == nil {
		return c.HandleError(ctx, fmt.Errorf("control channel not initialized"),
			"System control interface not available - server may need to be restarted", http.StatusInternalServerError)
	}

	c.Debug("API requested species filter rebuild")

	// Send rebuild filter signal
	c.controlChan <- SignalRebuildFilter

	return ctx.JSON(http.StatusOK, ControlResult{
		Success:   true,
		Message:   "Filter rebuild signal sent",
		Action:    ActionRebuildFilter,
		Timestamp: time.Now(),
	})
}
