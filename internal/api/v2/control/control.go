// Package control is the api/v2 control domain handler. It owns the
// /api/v2/control/* endpoints (restart analysis, reload model, rebuild range
// filter, restart server/container, restart a single audio source, and list
// available actions). The Handler embeds *apicore.Core by pointer so the shared
// dependencies and helpers (HandleError, the logging helpers, Debug, Context,
// GetShutdownRequester, and the audio Engine) promote onto it.
//
// Two domain members differ from the Core-only leaf domains:
//   - controlChan is the control-signal channel. It is SHARED: the settings
//     domain also sends on it and internal/analysis creates and closes it, so
//     the facade keeps the field and injects the same channel here as a
//     send-only view. The control handler never reads or closes it.
//   - sourceRestarter is referenced only by this domain (SetSourceRestarter and
//     the restart-source endpoint), so the Handler owns it outright. The facade
//     exposes a one-line SetSourceRestarter delegator for the external pipeline.
package control

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/restart"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
	"github.com/tphakala/birdnet-go/internal/telemetry"
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
	ActionRestartAnalysis    = "restart_analysis"
	ActionReloadModel        = "reload_model"
	ActionRebuildFilter      = "rebuild_filter"
	ActionRestartServer      = "restart_server"
	ActionRestartContainer   = "restart_container"
	ActionRestartAudioSource = "restart_audio_source"
)

// Control channel signals
const (
	SignalRestartAnalysis = "restart_analysis"
	SignalReloadModel     = "reload_birdnet"
	SignalRebuildFilter   = "rebuild_range_filter"
)

// SourceRestarterFunc restarts a single audio source identified by sourceID.
type SourceRestarterFunc func(sourceID string) error

// Handler serves the control domain endpoints. It embeds *apicore.Core BY
// POINTER so the shared Core members promote onto it without re-wiring; Core
// carries atomic/lock-bearing fields and must never be copied by value.
//
// controlChan is injected from the facade as a send-only view of the shared
// control-signal channel: the control handler only sends on it, while the
// settings domain (also a sender) and internal/analysis (the owner that creates
// and closes it) keep their bidirectional references. sourceRestarter is owned
// by this handler; it is set via SetSourceRestarter (called from the audio
// pipeline through the facade delegator) and read by the restart-source endpoint.
type Handler struct {
	*apicore.Core

	controlChan     chan<- string
	sourceRestarter atomic.Pointer[SourceRestarterFunc]
}

// New builds a control Handler around the shared core and the shared
// control-signal channel. controlChan MUST be the same channel the settings
// domain sends on and internal/analysis owns; it is held as a send-only view so
// the control handler can never read or close it.
func New(core *apicore.Core, controlChan chan<- string) *Handler {
	return &Handler{
		Core:        core,
		controlChan: controlChan,
	}
}

// RegisterRoutes registers all control-related API endpoints on the supplied API
// v2 group, preserving the exact routes, per-route middleware, and order the
// facade used before the control domain was extracted.
func (c *Handler) RegisterRoutes(g *echo.Group) {
	c.LogInfoIfEnabled("Initializing control routes")

	// Create control API group with auth middleware
	controlGroup := g.Group("/control", c.AuthMiddleware)

	// Control routes
	controlGroup.POST("/restart", c.RestartAnalysis)
	controlGroup.POST("/reload", c.ReloadModel)
	controlGroup.POST("/rebuild-filter", c.RebuildFilter)
	controlGroup.POST("/restart-server", c.RestartServer)
	controlGroup.POST("/restart-container", c.RestartContainer)
	controlGroup.POST("/restart-source/:id", c.RestartAudioSource)
	controlGroup.GET("/actions", c.GetAvailableActions)

	c.LogInfoIfEnabled("Control routes initialized successfully")
}

// GetAvailableActions handles GET /api/v2/control/actions
// Returns a list of available control actions
func (c *Handler) GetAvailableActions(ctx echo.Context) error {
	c.LogInfoIfEnabled("Getting available control actions",
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

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
		{
			Action:      ActionRestartServer,
			Description: "Restart the server binary",
		},
		{
			Action:      ActionRestartAudioSource,
			Description: "Restart a single audio source by ID",
		},
	}

	if sysinfo.IsContainer() {
		actions = append(actions, ControlAction{
			Action:      ActionRestartContainer,
			Description: "Restart the container",
		})
	}

	c.LogInfoIfEnabled("Retrieved available control actions successfully",
		logger.Int("action_count", len(actions)),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	return ctx.JSON(http.StatusOK, actions)
}

// handleControlSignal is a common handler for control operations that sends signals through the control channel.
// Parameters:
//   - ctx: The Echo context for the HTTP request
//   - signal: The control signal to send (e.g., SignalRestartAnalysis, SignalReloadModel)
//   - action: The action name for logging and response (e.g., ActionRestartAnalysis)
//   - logMessage: Initial log message when request is received
//   - successMessage: Message to return in the response when successful
//
// Returns an error if the control channel is nil or if the request times out
func (c *Handler) handleControlSignal(ctx echo.Context, signal, action, logMessage, successMessage string) error {
	c.LogInfoIfEnabled(logMessage,
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	if c.controlChan == nil {
		err := fmt.Errorf("control channel not initialized")
		c.LogErrorIfEnabled("Control channel not available",
			logger.Error(err),
			logger.String("action", action),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err,
			"System control interface not available - server may need to be restarted", http.StatusInternalServerError)
	}

	c.Debug("API requested %s", action)

	// Get request context
	reqCtx := ctx.Request().Context()

	// Send signal with context timeout and shutdown awareness.
	// The c.Context().Done() case prevents a send-on-closed-channel panic if shutdown
	// closes controlChan while an HTTP request is still in-flight.
	select {
	case c.controlChan <- signal:
		c.LogInfoIfEnabled("Control signal sent successfully",
			logger.String("action", action),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
	case <-c.Context().Done():
		return c.HandleError(ctx, c.Context().Err(),
			"Server is shutting down", http.StatusServiceUnavailable)
	case <-reqCtx.Done():
		err := reqCtx.Err()
		c.LogErrorIfEnabled("Request timeout/cancel while sending control signal",
			logger.Error(err),
			logger.String("action", action),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
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
func (c *Handler) RestartAnalysis(ctx echo.Context) error {
	return c.handleControlSignal(ctx, SignalRestartAnalysis, ActionRestartAnalysis,
		"Received request to restart analysis", "Analysis restart signal sent")
}

// ReloadModel handles POST /api/v2/control/reload
// Reloads the BirdNET model
func (c *Handler) ReloadModel(ctx echo.Context) error {
	return c.handleControlSignal(ctx, SignalReloadModel, ActionReloadModel,
		"Received request to reload model", "Model reload signal sent")
}

// RebuildFilter handles POST /api/v2/control/rebuild-filter
// Rebuilds the species filter based on current location
func (c *Handler) RebuildFilter(ctx echo.Context) error {
	return c.handleControlSignal(ctx, SignalRebuildFilter, ActionRebuildFilter,
		"Received request to rebuild species filter", "Filter rebuild signal sent")
}

// handleRestartRequest is the shared logic for restart endpoints.
// It checks the shutdown requester, applies the CAS flag via setFlag,
// and schedules an async shutdown after the HTTP response is sent.
func (c *Handler) handleRestartRequest(ctx echo.Context, action string, setFlag func() bool, successMessage string) error {
	sr := c.GetShutdownRequester()
	if sr == nil {
		err := fmt.Errorf("shutdown requester not initialized")
		return c.HandleError(ctx, err,
			"Restart not available - server may not support programmatic restart",
			http.StatusInternalServerError)
	}

	if !setFlag() {
		return ctx.JSON(http.StatusConflict, ControlResult{
			Success:   false,
			Message:   "A restart is already in progress",
			Action:    action,
			Timestamp: time.Now(),
		})
	}

	c.LogInfoIfEnabled(successMessage,
		logger.String("action", action),
		logger.String("ip", ctx.RealIP()),
	)

	// Record restart event as Sentry breadcrumb for diagnostics
	telemetry.AddBreadcrumb("restart", successMessage, sentry.LevelInfo, map[string]any{
		"action": action,
	})

	// Schedule shutdown after response is sent (500ms to ensure HTTP flush).
	// Capture sr locally so the goroutine uses a stable reference.
	go func() {
		time.Sleep(500 * time.Millisecond)
		sr.RequestShutdown()
	}()

	return ctx.JSON(http.StatusOK, ControlResult{
		Success:   true,
		Message:   successMessage,
		Action:    action,
		Timestamp: time.Now(),
	})
}

// RestartServer handles POST /api/v2/control/restart-server
// Triggers a graceful binary restart.
func (c *Handler) RestartServer(ctx echo.Context) error {
	c.LogInfoIfEnabled("Received request to restart server",
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	return c.handleRestartRequest(ctx, ActionRestartServer, restart.SetBinaryRestart, "Server restart initiated")
}

// RestartContainer handles POST /api/v2/control/restart-container
// Triggers a container restart by exiting the process.
func (c *Handler) RestartContainer(ctx echo.Context) error {
	c.LogInfoIfEnabled("Received request to restart container",
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	// Check if running in a container
	if !sysinfo.IsContainer() {
		envType, _ := sysinfo.GetEnvironment()
		return c.HandleError(ctx, fmt.Errorf("not running in a container (environment: %s)", envType),
			"Container restart is only available when running inside a container",
			http.StatusBadRequest)
	}

	return c.handleRestartRequest(ctx, ActionRestartContainer, restart.SetContainerRestart, "Container restart initiated")
}

// SetSourceRestarter injects the function used by the restart-source endpoint.
func (c *Handler) SetSourceRestarter(fn SourceRestarterFunc) {
	if fn == nil {
		c.sourceRestarter.Store(nil)
		return
	}
	c.sourceRestarter.Store(&fn)
}

// RestartAudioSource handles POST /api/v2/control/restart-source/:id
// Restarts a single audio source without affecting the rest of the pipeline.
func (c *Handler) RestartAudioSource(ctx echo.Context) error {
	sourceID := ctx.Param("id")
	if sourceID == "" {
		return c.HandleError(ctx, nil, "Source ID is required", http.StatusBadRequest)
	}

	c.LogInfoIfEnabled("Received request to restart audio source",
		logger.String("source_id", sourceID),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	eng := c.Engine.Load()
	if eng == nil {
		return c.HandleError(ctx, fmt.Errorf("audio engine not initialized"),
			"Audio engine not available", http.StatusInternalServerError)
	}
	if _, ok := eng.Registry().Get(sourceID); !ok {
		return c.HandleError(ctx, fmt.Errorf("source %s not found", sourceID),
			"Audio source not found", http.StatusNotFound)
	}

	fn := c.sourceRestarter.Load()
	if fn == nil {
		return c.HandleError(ctx, fmt.Errorf("source restarter not initialized"),
			"Audio pipeline not started", http.StatusServiceUnavailable)
	}

	if err := (*fn)(sourceID); err != nil {
		c.LogErrorIfEnabled("Failed to restart audio source",
			logger.String("source_id", sourceID),
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, "Failed to restart audio source", http.StatusInternalServerError)
	}

	c.LogInfoIfEnabled("Audio source restarted successfully",
		logger.String("source_id", sourceID),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	return ctx.JSON(http.StatusOK, ControlResult{
		Success:   true,
		Message:   fmt.Sprintf("Audio source %s restarted", sourceID),
		Action:    ActionRestartAudioSource,
		Timestamp: time.Now(),
	})
}
