// internal/api/v2/debug.go
package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// DebugErrorRequest represents the request for triggering a test error
type DebugErrorRequest struct {
	Component string         `json:"component"`
	Category  string         `json:"category"`
	Message   string         `json:"message"`
	Context   map[string]any `json:"context,omitempty"`
}

// DebugNotificationRequest represents the request for triggering a test notification
type DebugNotificationRequest struct {
	Type    string         `json:"type"`
	Title   string         `json:"title"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

// DebugResponse represents the response for debug operations
type DebugResponse struct {
	Success bool           `json:"success"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// DebugSystemStatus represents the current system status for debugging
type DebugSystemStatus struct {
	Timestamp     string         `json:"timestamp"`
	Debug         bool           `json:"debug"`
	Telemetry     map[string]any `json:"telemetry,omitempty"`
	Notifications map[string]any `json:"notifications,omitempty"`
}

// initDebugRoutes registers debug-related routes
func (c *Controller) initDebugRoutes() {
	// Only register debug routes if debug mode is enabled
	if !c.Settings.Debug {
		GetLogger().Debug("Debug mode not enabled, skipping debug routes")
		return
	}

	// Debug endpoints require authentication
	debugGroup := c.Group.Group("/debug", c.authMiddleware)

	debugGroup.POST("/trigger-error", c.DebugTriggerError)
	debugGroup.POST("/trigger-notification", c.DebugTriggerNotification)
	debugGroup.GET("/status", c.DebugSystemStatus)

	GetLogger().Info("Debug routes initialized")
}

// DebugTriggerError triggers a test error for telemetry testing
func (c *Controller) DebugTriggerError(ctx echo.Context) error {
	// Double-check debug mode using controller's settings
	if c.Settings == nil || !c.Settings.Debug {
		return ctx.JSON(http.StatusForbidden, map[string]string{
			"error": "Debug mode not enabled",
		})
	}

	var req DebugErrorRequest
	if err := ctx.Bind(&req); err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	// Default values
	if req.Component == "" {
		req.Component = "debug"
	}
	if req.Category == "" {
		req.Category = "test"
	}
	if req.Message == "" {
		req.Message = "Test error triggered via debug endpoint"
	}

	// Map category string to error category
	category := mapErrorCategory(req.Category)

	// Create and report the error
	testErr := errors.Newf("%s", req.Message).
		Component(req.Component).
		Category(category).
		Context("triggered_at", time.Now().Format(time.RFC3339)).
		Context("debug_endpoint", true)

	// Add any additional context
	for k, v := range req.Context {
		testErr = testErr.Context(k, v)
	}

	// Build and report the error
	err := testErr.Build()

	// Log the error which will trigger telemetry if enabled
	c.logErrorIfEnabled("Debug error triggered",
		logger.Error(err),
		logger.String("component", req.Component),
		logger.String("category", req.Category))

	response := DebugResponse{
		Success: true,
		Message: "Test error triggered successfully",
		Details: map[string]any{
			"component": req.Component,
			"category":  req.Category,
			"message":   req.Message,
			"timestamp": time.Now().Format(time.RFC3339),
		},
	}

	return ctx.JSON(http.StatusOK, response)
}

// DebugTriggerNotification triggers a test notification
func (c *Controller) DebugTriggerNotification(ctx echo.Context) error {
	// Double-check debug mode using controller's settings
	if c.Settings == nil || !c.Settings.Debug {
		return ctx.JSON(http.StatusForbidden, map[string]string{
			"error": "Debug mode not enabled",
		})
	}

	var req DebugNotificationRequest
	if err := ctx.Bind(&req); err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	// Default values
	if req.Type == "" {
		req.Type = "test"
	}
	if req.Title == "" {
		req.Title = "Test Notification"
	}
	if req.Message == "" {
		req.Message = "This is a test notification triggered via debug endpoint"
	}

	// Get notification service
	notificationService := notification.GetService()
	if notificationService == nil {
		return ctx.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "Notification service not available",
		})
	}

	// Map string type to notification.Type
	notifType := mapNotificationType(req.Type)
	
	// Create notification using the service
	_, err := notificationService.CreateWithComponent(
		notifType,
		notification.PriorityMedium,
		req.Title,
		req.Message,
		"debug",
	)
	
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("Failed to create notification: %v", err),
		})
	}

	response := DebugResponse{
		Success: true,
		Message: "Test notification sent successfully",
		Details: map[string]any{
			"type":      req.Type,
			"title":     req.Title,
			"message":   req.Message,
			"timestamp": time.Now().Format(time.RFC3339),
		},
	}

	return ctx.JSON(http.StatusOK, response)
}

// DebugSystemStatus returns current system status for debugging
func (c *Controller) DebugSystemStatus(ctx echo.Context) error {
	// Double-check debug mode using controller's settings
	if c.Settings == nil || !c.Settings.Debug {
		return ctx.JSON(http.StatusForbidden, map[string]string{
			"error": "Debug mode not enabled",
		})
	}

	status := DebugSystemStatus{
		Timestamp: time.Now().Format(time.RFC3339),
		Debug:     c.Settings.Debug,
	}

	// Get telemetry status
	if telemetryStatus := getTelemetryStatus(); telemetryStatus != nil {
		status.Telemetry = telemetryStatus
	}

	// Get notification status
	if notificationService := notification.GetService(); notificationService != nil {
		status.Notifications = map[string]any{
			"initialized": notification.IsInitialized(),
			"enabled":     true,
		}
	}

	return ctx.JSON(http.StatusOK, status)
}

// Helper functions

func mapErrorCategory(category string) errors.ErrorCategory {
	switch category {
	case "network":
		return errors.CategoryNetwork
	case "database":
		return errors.CategoryDatabase
	case "system":
		return errors.CategorySystem
	case "config":
		return errors.CategoryConfiguration
	case "audio":
		return errors.CategoryAudio
	default:
		return errors.CategorySystem
	}
}

func getTelemetryStatus() map[string]any {
	// Get more detailed telemetry status
	status := map[string]any{
		"enabled": false, // Default to false for safety
	}
	
	// Check if settings are available via global conf
	if settings := conf.GetSettings(); settings != nil {
		status["enabled"] = settings.Sentry.Enabled
	}
	
	// Add health check info if available
	if healthHandler := telemetry.NewHealthCheckHandler(); healthHandler != nil {
		// Get coordinator from global instance
		if coord := getGlobalInitCoordinator(); coord != nil {
			health := coord.HealthCheck()
			status["healthy"] = health.Healthy
			status["components"] = make(map[string]any)
			
			for name, compHealth := range health.Components {
				status["components"].(map[string]any)[name] = map[string]any{
					"state":   compHealth.State.String(),
					"healthy": compHealth.Healthy,
					"error":   compHealth.Error,
				}
			}
		}
	}
	
	// Add worker stats if available
	if worker := telemetry.GetTelemetryWorker(); worker != nil {
		stats := worker.GetStats()
		status["worker"] = map[string]any{
			"events_processed": stats.EventsProcessed,
			"events_dropped":   stats.EventsDropped,
			"events_failed":    stats.EventsFailed,
			"circuit_state":    stats.CircuitState,
		}
	}
	
	return status
}

func mapNotificationType(typeStr string) notification.Type {
	switch typeStr {
	case NotificationTypeError:
		return notification.TypeError
	case NotificationTypeWarning:
		return notification.TypeWarning
	case NotificationTypeInfo:
		return notification.TypeInfo
	case NotificationTypeDetection:
		return notification.TypeDetection
	case NotificationTypeSystem:
		return notification.TypeSystem
	default:
		return notification.TypeInfo
	}
}

func getGlobalInitCoordinator() *telemetry.InitCoordinator {
	// Use the exported getter function from the telemetry package
	return telemetry.GetGlobalInitCoordinator()
}