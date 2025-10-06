package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// SSE Connection configuration
const (
	// Connection timeouts
	maxSSEConnectionDuration = 30 * time.Minute       // Maximum connection duration to prevent resource leaks
	rateLimitWindow          = 1 * time.Minute        // Rate limiter time window
	heartbeatInterval        = 30 * time.Second       // Heartbeat interval for keep-alive
	eventLoopCheckInterval   = 100 * time.Millisecond // Event loop check interval

	// Endpoints
	sseEndpoint = "/api/v2/notifications/stream"

	// Buffer sizes
	notificationChannelBuffer = 10 // Buffer size for notification channels

	// Rate limits
	rateLimitRequestsPerWindow = 10 // Maximum requests per rate limit window for notifications (increased from 1 to match other SSE endpoints)
	rateLimitBurst             = 15 // Rate limit burst allowance (increased to handle quick navigation)
)

// SSENotificationData represents notification data sent via SSE
type SSENotificationData struct {
	*notification.Notification
	EventType string `json:"eventType"`
}

// UnifiedSSEEvent represents a unified event structure for notifications and toasts
type UnifiedSSEEvent struct {
	Type      string         `json:"type"`      // "notification" or "toast"
	EventName string         `json:"eventName"` // Specific event name
	Data      any            `json:"data"`      // The actual event data
	Timestamp time.Time      `json:"timestamp"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// NotificationClient represents a connected notification SSE client
type NotificationClient struct {
	ID           string
	Channel      chan *notification.Notification
	Request      *http.Request
	Response     http.ResponseWriter
	Done         chan struct{} // Signal-only channel for shutdown notification
	SubscriberCh <-chan *notification.Notification
	Context      context.Context
}

// initNotificationRoutes registers notification-related routes
func (c *Controller) initNotificationRoutes() {
	c.SetupNotificationRoutes()
}

// SetupNotificationRoutes configures notification-related routes
func (c *Controller) SetupNotificationRoutes() {
	// Rate limiter configuration for SSE connections
	rateLimiterConfig := middleware.RateLimiterConfig{
		Skipper: middleware.DefaultSkipper,
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(
			middleware.RateLimiterMemoryStoreConfig{
				Rate:      rateLimitRequestsPerWindow, // Rate limit per window
				Burst:     rateLimitBurst,             // Rate limit burst
				ExpiresIn: rateLimitWindow,            // Rate limit window
			},
		),
		IdentifierExtractor: func(ctx echo.Context) (string, error) {
			// Use client IP as identifier
			return ctx.RealIP(), nil
		},
		ErrorHandler: func(context echo.Context, err error) error {
			return context.JSON(http.StatusTooManyRequests, map[string]string{
				"error": "Too many notification stream connection attempts, please wait before trying again",
			})
		},
	}

	// SSE endpoint for notification stream (authenticated - includes both notifications and toasts)
	c.Group.GET("/notifications/stream", c.StreamNotifications, c.getEffectiveAuthMiddleware(), middleware.RateLimiterWithConfig(rateLimiterConfig))

	// REST endpoints for notification management
	c.Group.GET("/notifications", c.GetNotifications)
	c.Group.GET("/notifications/:id", c.GetNotification)
	c.Group.PUT("/notifications/:id/read", c.MarkNotificationRead)
	c.Group.PUT("/notifications/:id/acknowledge", c.MarkNotificationAcknowledged)
	c.Group.DELETE("/notifications/:id", c.DeleteNotification)
	c.Group.GET("/notifications/unread/count", c.GetUnreadCount)

	// Test endpoints for notification system
	c.Group.POST("/notifications/test/new-species", c.CreateTestNewSpeciesNotification, c.getEffectiveAuthMiddleware())
}

// StreamNotifications handles the SSE connection for real-time notification streaming
func (c *Controller) StreamNotifications(ctx echo.Context) error {
	// Check if notification service is initialized
	if !notification.IsInitialized() {
		return ctx.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "Notification service not available",
		})
	}

	// Track connection start time for metrics
	connectionStartTime := time.Now()

	// Track active connections using metrics
	if c.metrics != nil && c.metrics.HTTP != nil {
		c.metrics.HTTP.SSEConnectionStarted(sseEndpoint)
		defer func() {
			duration := time.Since(connectionStartTime).Seconds()
			// Determine close reason based on context
			closeReason := metrics.SSECloseReasonClosed
			if ctx.Request().Context().Err() == context.DeadlineExceeded {
				closeReason = metrics.SSECloseReasonTimeout
			} else if ctx.Request().Context().Err() == context.Canceled {
				closeReason = metrics.SSECloseReasonCanceled
			}
			c.metrics.HTTP.SSEConnectionClosed(sseEndpoint, duration, closeReason)
		}()
	}

	// Create a context with timeout for maximum connection duration
	timeoutCtx, cancel := context.WithTimeout(ctx.Request().Context(), maxSSEConnectionDuration)
	defer cancel()

	// Override the request context with timeout context
	originalReq := ctx.Request()
	ctx.SetRequest(originalReq.WithContext(timeoutCtx))

	client, service, err := c.setupNotificationSSEClient(ctx)
	if err != nil {
		return err
	}

	// Ensure cleanup happens regardless of how we exit
	defer func() {
		service.Unsubscribe(client.SubscriberCh)
		// Note: We don't close client.Done to avoid race conditions with senders
		// The buffered channel will signal shutdown and be reclaimed by GC
	}()

	// Setup disconnect handler with proper cleanup
	c.setupNotificationDisconnectHandler(ctx, client)

	// Run the main event loop
	return c.runNotificationEventLoop(ctx, client)
}

// setupNotificationSSEClient initializes the SSE client and establishes connection
func (c *Controller) setupNotificationSSEClient(ctx echo.Context) (*NotificationClient, *notification.Service, error) {
	// Set SSE headers
	c.setNotificationSSEHeaders(ctx)

	// Generate client ID
	clientID := uuid.New().String()

	// Subscribe to notifications
	service := notification.GetService()
	notificationCh, notificationCtx := service.Subscribe()

	// Create notification client
	client := &NotificationClient{
		ID:           clientID,
		Channel:      make(chan *notification.Notification, notificationChannelBuffer),
		Request:      ctx.Request(),
		Response:     ctx.Response(),
		Done:         make(chan struct{}, 1), // Buffered signal channel to prevent deadlock during disconnect
		SubscriberCh: notificationCh,
		Context:      notificationCtx,
	}

	// Send initial connection message
	if err := c.sendSSEMessage(ctx, "connected", map[string]string{
		"clientId": clientID,
		"message":  "Connected to notification stream",
	}); err != nil {
		service.Unsubscribe(notificationCh)
		return nil, nil, err
	}

	if c.metrics != nil && c.metrics.HTTP != nil {
		c.metrics.HTTP.RecordSSEMessageSent(sseEndpoint, "connected")
	}

	// Log the connection
	c.logNotificationConnection(clientID, ctx.RealIP(), ctx.Request().UserAgent(), true)

	return client, service, nil
}

// setNotificationSSEHeaders sets the required headers for notification SSE
func (c *Controller) setNotificationSSEHeaders(ctx echo.Context) {
	ctx.Response().Header().Set("Content-Type", "text/event-stream")
	ctx.Response().Header().Set("Cache-Control", "no-cache")
	ctx.Response().Header().Set("Connection", "keep-alive")
	ctx.Response().Header().Set("Access-Control-Allow-Origin", "*")
	ctx.Response().Header().Set("Access-Control-Allow-Headers", "Cache-Control")
}

// setupNotificationDisconnectHandler sets up client disconnect handling with timeout
func (c *Controller) setupNotificationDisconnectHandler(ctx echo.Context, client *NotificationClient) {
	go func() {
		// Wait for client disconnect or timeout
		<-ctx.Request().Context().Done()

		// Client disconnected or timeout reached
		select {
		case client.Done <- struct{}{}:
			// Successfully notified
		case <-time.After(eventLoopCheckInterval):
			// Done channel might be blocked, continue
		}
		c.logNotificationConnection(client.ID, ctx.RealIP(), "", false)
	}()
}

// runNotificationEventLoop runs the main SSE event loop
func (c *Controller) runNotificationEventLoop(ctx echo.Context, client *NotificationClient) error {
	// Send heartbeat every 30 seconds
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	// Track connection start time for monitoring
	connectionStart := time.Now()

	// Main event loop
	for {
		select {
		case notif := <-client.SubscriberCh:
			if notif == nil {
				// Channel closed, service is shutting down
				return nil
			}

			if err := c.processNotificationEvent(ctx, client.ID, notif); err != nil {
				return err
			}

		case <-ticker.C:
			// Check if connection has exceeded maximum duration
			if time.Since(connectionStart) > maxSSEConnectionDuration {
				c.logNotificationConnection(client.ID, "", "max_duration_exceeded", false)
				return nil
			}
			// Send heartbeat
			if err := c.sendNotificationHeartbeat(ctx); err != nil {
				if c.metrics != nil && c.metrics.HTTP != nil {
					c.metrics.HTTP.RecordSSEError(sseEndpoint, "heartbeat_failed")
				}
				return err
			}
			if c.metrics != nil && c.metrics.HTTP != nil {
				c.metrics.HTTP.RecordSSEMessageSent(sseEndpoint, "heartbeat")
			}

		case <-client.Done:
			// Client disconnected
			return nil

		case <-client.Context.Done():
			// Subscription cancelled
			return nil
		}
	}
}

// processNotificationEvent processes a single notification event
func (c *Controller) processNotificationEvent(ctx echo.Context, clientID string, notif *notification.Notification) error {
	// Check if this is a toast notification
	isToast, _ := notif.Metadata[notification.MetadataKeyIsToast].(bool)

	if isToast {
		return c.sendToastEvent(ctx, clientID, notif)
	}

	return c.sendNotificationEvent(ctx, clientID, notif)
}

// sendToastEvent sends a toast event via SSE
func (c *Controller) sendToastEvent(ctx echo.Context, clientID string, notif *notification.Notification) error {
	toastEvent := c.createToastEventData(notif)

	if err := c.sendSSEMessage(ctx, "toast", toastEvent); err != nil {
		c.logNotificationError("failed to send toast SSE", err, clientID)
		if c.metrics != nil && c.metrics.HTTP != nil {
			c.metrics.HTTP.RecordSSEError(sseEndpoint, "send_failed")
		}
		return err
	}

	if c.metrics != nil && c.metrics.HTTP != nil {
		c.metrics.HTTP.RecordSSEMessageSent(sseEndpoint, "toast")
	}
	c.logToastSent(clientID, notif)
	return nil
}

// sendNotificationEvent sends a notification event via SSE
func (c *Controller) sendNotificationEvent(ctx echo.Context, clientID string, notif *notification.Notification) error {
	event := SSENotificationData{
		Notification: notif,
		EventType:    "notification",
	}

	if err := c.sendSSEMessage(ctx, "notification", event); err != nil {
		c.logNotificationError("failed to send notification SSE", err, clientID)
		if c.metrics != nil && c.metrics.HTTP != nil {
			c.metrics.HTTP.RecordSSEError(sseEndpoint, "send_failed")
		}
		return err
	}

	if c.metrics != nil && c.metrics.HTTP != nil {
		c.metrics.HTTP.RecordSSEMessageSent(sseEndpoint, "notification")
	}
	c.logNotificationSent(clientID, notif)
	return nil
}

// createToastEventData creates toast event data from notification
func (c *Controller) createToastEventData(notif *notification.Notification) map[string]any {
	toastType, _ := notif.Metadata["toastType"].(string)
	duration, _ := notif.Metadata["duration"].(int)
	action, _ := notif.Metadata["action"].(*notification.ToastAction)

	toastEvent := map[string]any{
		"id":        notif.Metadata["toastId"],
		"message":   notif.Message,
		"type":      toastType,
		"timestamp": notif.Timestamp,
		"component": notif.Component,
	}

	if duration > 0 {
		toastEvent["duration"] = duration
	}
	if action != nil {
		toastEvent["action"] = action
	}

	return toastEvent
}

// sendNotificationHeartbeat sends a heartbeat message
func (c *Controller) sendNotificationHeartbeat(ctx echo.Context) error {
	return c.sendSSEMessage(ctx, "heartbeat", map[string]string{
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// logNotificationConnection logs SSE client connection/disconnection events
func (c *Controller) logNotificationConnection(clientID, ip, userAgent string, connected bool) {
	if c.apiLogger == nil {
		return
	}

	action := "connected"
	if !connected {
		action = "disconnected"
	}

	if c.Settings != nil && c.Settings.WebServer.Debug && connected {
		c.apiLogger.Debug("notification SSE client "+action,
			"clientId", clientID,
			"ip", privacy.AnonymizeIP(ip),
			"user_agent", privacy.RedactUserAgent(userAgent))
	} else {
		c.apiLogger.Info("notification SSE client "+action,
			"clientId", clientID,
			"ip", privacy.AnonymizeIP(ip))
	}
}

// logNotificationError logs SSE errors
func (c *Controller) logNotificationError(message string, err error, clientID string) {
	if c.apiLogger != nil {
		c.apiLogger.Error(message, "error", err, "clientId", clientID)
	}
}

// logToastSent logs successful toast sending
func (c *Controller) logToastSent(clientID string, notif *notification.Notification) {
	if c.apiLogger != nil && c.Settings != nil && c.Settings.WebServer.Debug {
		toastType, _ := notif.Metadata["toastType"].(string)
		c.apiLogger.Debug("toast sent via SSE",
			"clientId", clientID,
			"toast_id", notif.Metadata["toastId"],
			"type", toastType,
			"component", notif.Component)
	}
}

// logNotificationSent logs successful notification sending
func (c *Controller) logNotificationSent(clientID string, notif *notification.Notification) {
	if c.apiLogger != nil && c.Settings != nil && c.Settings.WebServer.Debug {
		c.apiLogger.Debug("notification sent via SSE",
			"clientId", clientID,
			"notification_id", notif.ID,
			"type", notif.Type,
			"priority", notif.Priority)
	}
}

// GetNotifications returns a list of notifications with optional filtering
func (c *Controller) GetNotifications(ctx echo.Context) error {
	if !notification.IsInitialized() {
		return ctx.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "Notification service not available",
		})
	}

	service := notification.GetService()

	// Build filter options from query parameters
	filter := &notification.FilterOptions{}

	// Parse status filter
	if statusParam := ctx.QueryParam("status"); statusParam != "" {
		filter.Status = []notification.Status{notification.Status(statusParam)}
	}

	// Parse type filter
	if typeParam := ctx.QueryParam("type"); typeParam != "" {
		filter.Types = []notification.Type{notification.Type(typeParam)}
	}

	// Parse priority filter
	if priorityParam := ctx.QueryParam("priority"); priorityParam != "" {
		filter.Priorities = []notification.Priority{notification.Priority(priorityParam)}
	}

	// Parse limit
	if limitParam := ctx.QueryParam("limit"); limitParam != "" {
		if limit, err := strconv.Atoi(limitParam); err == nil && limit > 0 {
			filter.Limit = limit
		}
	} else {
		filter.Limit = 50 // Default limit
	}

	// Parse offset
	if offsetParam := ctx.QueryParam("offset"); offsetParam != "" {
		if offset, err := strconv.Atoi(offsetParam); err == nil && offset >= 0 {
			filter.Offset = offset
		}
	}

	if c.apiLogger != nil && c.Settings != nil && c.Settings.WebServer.Debug {
		c.apiLogger.Debug("listing notifications",
			"status", filter.Status,
			"types", filter.Types,
			"priorities", filter.Priorities,
			"limit", filter.Limit,
			"offset", filter.Offset)
	}

	// Get notifications
	notifications, err := service.List(filter)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("failed to list notifications", "error", err)
		}
		return ctx.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to retrieve notifications",
		})
	}

	if c.apiLogger != nil && c.Settings != nil && c.Settings.WebServer.Debug {
		unreadCount, err := service.GetUnreadCount()
		if err != nil {
			c.apiLogger.Error("failed to get unread count", "error", err)
			unreadCount = -1 // Indicate error in debug log
		}
		c.apiLogger.Debug("notifications retrieved",
			"count", len(notifications),
			"total_unread", unreadCount)
	}

	return ctx.JSON(http.StatusOK, map[string]any{
		"notifications": notifications,
		"count":         len(notifications),
		"limit":         filter.Limit,
		"offset":        filter.Offset,
	})
}

// GetNotification returns a single notification by ID
func (c *Controller) GetNotification(ctx echo.Context) error {
	if !notification.IsInitialized() {
		return ctx.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "Notification service not available",
		})
	}

	id := ctx.Param("id")
	if id == "" {
		return ctx.JSON(http.StatusBadRequest, map[string]string{
			"error": "Notification ID is required",
		})
	}

	service := notification.GetService()
	notif, err := service.Get(id)
	if err != nil {
		if errors.Is(err, notification.ErrNotificationNotFound) {
			return ctx.JSON(http.StatusNotFound, map[string]string{
				"error": "Notification not found",
			})
		}
		if c.apiLogger != nil {
			c.apiLogger.Error("failed to get notification", "error", err, "id", id)
		}
		return ctx.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to retrieve notification",
		})
	}

	return ctx.JSON(http.StatusOK, notif)
}

// MarkNotificationRead marks a notification as read
func (c *Controller) MarkNotificationRead(ctx echo.Context) error {
	if !notification.IsInitialized() {
		return ctx.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "Notification service not available",
		})
	}

	id := ctx.Param("id")
	if id == "" {
		return ctx.JSON(http.StatusBadRequest, map[string]string{
			"error": "Notification ID is required",
		})
	}

	service := notification.GetService()

	if err := service.MarkAsRead(id); err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("failed to mark notification as read", "error", err, "id", id)
		}
		return ctx.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to mark notification as read",
		})
	}

	if c.apiLogger != nil && c.Settings != nil && c.Settings.WebServer.Debug {
		c.apiLogger.Debug("notification marked as read", "id", id)
	}

	return ctx.JSON(http.StatusOK, map[string]string{
		"message": "Notification marked as read",
	})
}

// MarkNotificationAcknowledged marks a notification as acknowledged
func (c *Controller) MarkNotificationAcknowledged(ctx echo.Context) error {
	if !notification.IsInitialized() {
		return ctx.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "Notification service not available",
		})
	}

	id := ctx.Param("id")
	if id == "" {
		return ctx.JSON(http.StatusBadRequest, map[string]string{
			"error": "Notification ID is required",
		})
	}

	service := notification.GetService()
	if err := service.MarkAsAcknowledged(id); err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("failed to mark notification as acknowledged", "error", err, "id", id)
		}
		return ctx.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to mark notification as acknowledged",
		})
	}

	return ctx.JSON(http.StatusOK, map[string]string{
		"message": "Notification marked as acknowledged",
	})
}

// DeleteNotification deletes a notification
func (c *Controller) DeleteNotification(ctx echo.Context) error {
	if !notification.IsInitialized() {
		return ctx.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "Notification service not available",
		})
	}

	id := ctx.Param("id")
	if id == "" {
		return ctx.JSON(http.StatusBadRequest, map[string]string{
			"error": "Notification ID is required",
		})
	}

	service := notification.GetService()
	if err := service.Delete(id); err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("failed to delete notification", "error", err, "id", id)
		}
		return ctx.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to delete notification",
		})
	}

	return ctx.JSON(http.StatusOK, map[string]string{
		"message": "Notification deleted",
	})
}

// GetUnreadCount returns the count of unread notifications
func (c *Controller) GetUnreadCount(ctx echo.Context) error {
	if !notification.IsInitialized() {
		return ctx.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "Notification service not available",
		})
	}

	service := notification.GetService()
	count, err := service.GetUnreadCount()
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("failed to get unread count", "error", err)
		}
		return ctx.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to get unread count",
		})
	}

	return ctx.JSON(http.StatusOK, map[string]any{
		"unreadCount": count,
	})
}

// CreateTestNewSpeciesNotification creates a test new species detection notification
func (c *Controller) CreateTestNewSpeciesNotification(ctx echo.Context) error {
	if !notification.IsInitialized() {
		return ctx.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "Notification service not available",
		})
	}

	service := notification.GetService()

	// Build base URL for links
	baseURL := notification.BuildBaseURL(c.Settings.Security.Host, c.Settings.WebServer.Port, c.Settings.Security.AutoTLS)

	// Create test template data with realistic values
	testTemplateData := &notification.TemplateData{
		CommonName:         "Test Bird Species",
		ScientificName:     "Testus birdicus",
		Confidence:         0.99,
		ConfidencePercent:  "99",
		DetectionTime:      time.Now().Format("15:04:05"),
		DetectionDate:      time.Now().Format("2006-01-02"),
		Latitude:           42.3601,
		Longitude:          -71.0589,
		Location:           "Fake Test Location",
		DetectionURL:       baseURL + "/ui/detections/test",
		ImageURL:           "https://static.avicommons.org/houfin-DzFZcHoKwyx9JOmg-320.jpg",
		DaysSinceFirstSeen: 0,
	}

	// Get templates from settings
	titleTemplate := c.Settings.Notification.Templates.NewSpecies.Title
	messageTemplate := c.Settings.Notification.Templates.NewSpecies.Message

	// Render notification using templates (same pattern as detection_consumer.go)
	var title, message string
	var titleSet, messageSet bool

	if titleTemplate != "" {
		var err error
		title, err = notification.RenderTemplate("title", titleTemplate, testTemplateData)
		if err != nil {
			if c.apiLogger != nil {
				c.apiLogger.Error("failed to render title template, using default", "error", err)
			}
		} else {
			titleSet = true
		}
	} else {
		// Empty template means user wants no title
		titleSet = true
	}

	if messageTemplate != "" {
		var err error
		message, err = notification.RenderTemplate("message", messageTemplate, testTemplateData)
		if err != nil {
			if c.apiLogger != nil {
				c.apiLogger.Error("failed to render message template, using default", "error", err)
			}
		} else {
			messageSet = true
		}
	} else {
		// Empty template means user wants no message
		messageSet = true
	}

	// Use defaults only if template rendering failed
	if !titleSet {
		title = "New Species Detected: Test Bird Species"
	}
	if !messageSet {
		message = "First detection of Test Bird Species (Testus birdicus) at Fake Test Location"
	}

	testNotification := notification.NewNotification(notification.TypeDetection, notification.PriorityHigh, title, message).
		WithComponent("detection").
		WithMetadata("species", testTemplateData.CommonName).
		WithMetadata("scientific_name", testTemplateData.ScientificName).
		WithMetadata("confidence", testTemplateData.Confidence).
		WithMetadata("location", testTemplateData.Location).
		WithMetadata("is_new_species", true).
		WithMetadata("days_since_first_seen", testTemplateData.DaysSinceFirstSeen).
		WithExpiry(24 * time.Hour) // New species notifications expire after 24 hours

	// Use CreateWithMetadata to persist and broadcast
	if err := service.CreateWithMetadata(testNotification); err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("failed to create test notification", "error", err)
		}
		return ctx.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to create test notification",
		})
	}

	if c.apiLogger != nil && c.Settings != nil && c.Settings.WebServer.Debug {
		c.apiLogger.Debug("test new species notification created",
			"notification_id", testNotification.ID,
			"species", testTemplateData.CommonName,
			"rendered_title", title,
			"rendered_message", message)
	}

	return ctx.JSON(http.StatusOK, testNotification)
}
