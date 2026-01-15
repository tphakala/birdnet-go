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
	"github.com/tphakala/birdnet-go/internal/logger"
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

// Test notification constants
const (
	testNotificationConfidence   = 0.99     // Test confidence value for new species notification
	testNotificationLatitude     = 42.3601  // Test latitude (Boston, MA) for new species notification
	testNotificationLongitude    = -71.0589 // Test longitude (Boston, MA) for new species notification
	newSpeciesNotificationExpiry = 24       // Hours until new species notification expires
)

// renderTemplateWithDefault renders a template, returning the default value if template is empty
// or rendering fails. Logs errors if logError is provided.
func renderTemplateWithDefault(name, template, defaultVal string, data *notification.TemplateData, logError func(string, ...any)) string {
	if template == "" {
		return "" // Empty template = user wants empty value
	}
	result, err := notification.RenderTemplate(name, template, data)
	if err != nil {
		if logError != nil {
			logError("failed to render "+name+" template, using default", "error", err)
		}
		return defaultVal
	}
	return result
}

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

// notificationAction represents a notification service operation
type notificationAction struct {
	operation      func(service *notification.Service, id string) error
	errorLogMsg    string
	errorRespMsg   string
	successRespMsg string
}

// executeNotificationAction handles the common pattern for notification operations:
// check service initialization, validate ID, execute operation, handle errors.
func (c *Controller) executeNotificationAction(ctx echo.Context, action notificationAction) error {
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
	if err := action.operation(service, id); err != nil {
		c.logErrorIfEnabled(action.errorLogMsg,
			logger.Error(err),
			logger.String("id", id))
		return ctx.JSON(http.StatusInternalServerError, map[string]string{
			"error": action.errorRespMsg,
		})
	}

	return ctx.JSON(http.StatusOK, map[string]string{
		"message": action.successRespMsg,
	})
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
	c.Group.GET("/notifications/stream", c.StreamNotifications, c.authMiddleware, middleware.RateLimiterWithConfig(rateLimiterConfig))

	// REST endpoints for notification management (authenticated)
	// All notification endpoints require authentication when security is enabled
	notificationsGroup := c.Group.Group("/notifications", c.authMiddleware)
	notificationsGroup.GET("", c.GetNotifications)
	notificationsGroup.GET("/:id", c.GetNotification)
	notificationsGroup.PUT("/:id/read", c.MarkNotificationRead)
	notificationsGroup.PUT("/:id/acknowledge", c.MarkNotificationAcknowledged)
	notificationsGroup.DELETE("/:id", c.DeleteNotification)
	notificationsGroup.GET("/unread/count", c.GetUnreadCount)

	// Test endpoints for notification system (authenticated)
	notificationsGroup.POST("/test/new-species", c.CreateTestNewSpeciesNotification)
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
	setSSEHeaders(ctx)

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
				c.recordSSEError(sseEndpoint, "heartbeat_failed")
				return err
			}
			c.recordSSEMessage(sseEndpoint, "heartbeat")

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
	// Clone notification to prevent concurrent access issues during JSON marshaling
	safeNotif := notif.Clone()
	toastEvent := c.createToastEventData(safeNotif)

	if err := c.sendSSEMessage(ctx, "toast", toastEvent); err != nil {
		c.logNotificationError("failed to send toast SSE", err, clientID)
		c.recordSSEError(sseEndpoint, "send_failed")
		return err
	}

	c.recordSSEMessage(sseEndpoint, "toast")
	c.logToastSent(clientID, notif)
	return nil
}

// sendNotificationEvent sends a notification event via SSE
func (c *Controller) sendNotificationEvent(ctx echo.Context, clientID string, notif *notification.Notification) error {
	// Clone notification to prevent concurrent access issues during JSON marshaling
	safeNotif := notif.Clone()

	event := SSENotificationData{
		Notification: safeNotif,
		EventType:    "notification",
	}

	if err := c.sendSSEMessage(ctx, "notification", event); err != nil {
		c.logNotificationError("failed to send notification SSE", err, clientID)
		c.recordSSEError(sseEndpoint, "send_failed")
		return err
	}

	c.recordSSEMessage(sseEndpoint, "notification")
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
	action := "connected"
	if !connected {
		action = "disconnected"
	}

	if c.Settings != nil && c.Settings.WebServer.Debug && connected {
		c.logDebugIfEnabled("notification SSE client "+action,
			logger.String("clientId", clientID),
			logger.String("ip", privacy.AnonymizeIP(ip)),
			logger.String("user_agent", privacy.RedactUserAgent(userAgent)))
	} else {
		c.logInfoIfEnabled("notification SSE client "+action,
			logger.String("clientId", clientID),
			logger.String("ip", privacy.AnonymizeIP(ip)))
	}
}

// logNotificationError logs SSE errors
func (c *Controller) logNotificationError(message string, err error, clientID string) {
	c.logErrorIfEnabled(message,
		logger.Error(err),
		logger.String("clientId", clientID))
}

// logToastSent logs successful toast sending
func (c *Controller) logToastSent(clientID string, notif *notification.Notification) {
	if c.Settings != nil && c.Settings.WebServer.Debug {
		toastType, _ := notif.Metadata["toastType"].(string)
		c.logDebugIfEnabled("toast sent via SSE",
			logger.String("clientId", clientID),
			logger.Any("toast_id", notif.Metadata["toastId"]),
			logger.String("type", toastType),
			logger.String("component", notif.Component))
	}
}

// logNotificationSent logs successful notification sending
func (c *Controller) logNotificationSent(clientID string, notif *notification.Notification) {
	if c.Settings != nil && c.Settings.WebServer.Debug {
		c.logDebugIfEnabled("notification sent via SSE",
			logger.String("clientId", clientID),
			logger.String("notification_id", notif.ID),
			logger.String("type", string(notif.Type)),
			logger.String("priority", string(notif.Priority)))
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

	if c.Settings != nil && c.Settings.WebServer.Debug {
		c.logDebugIfEnabled("listing notifications",
			logger.Any("status", filter.Status),
			logger.Any("types", filter.Types),
			logger.Any("priorities", filter.Priorities),
			logger.Int("limit", filter.Limit),
			logger.Int("offset", filter.Offset))
	}

	// Get notifications
	notifications, err := service.List(filter)
	if err != nil {
		c.logErrorIfEnabled("failed to list notifications", logger.Error(err))
		return ctx.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to retrieve notifications",
		})
	}

	if c.Settings != nil && c.Settings.WebServer.Debug {
		unreadCount, err := service.GetUnreadCount()
		if err != nil {
			c.logErrorIfEnabled("failed to get unread count", logger.Error(err))
			unreadCount = -1 // Indicate error in debug log
		}
		c.logDebugIfEnabled("notifications retrieved",
			logger.Int("count", len(notifications)),
			logger.Int("total_unread", unreadCount))
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
		c.logErrorIfEnabled("failed to get notification",
			logger.Error(err),
			logger.String("id", id))
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
		c.logErrorIfEnabled("failed to mark notification as read",
			logger.Error(err),
			logger.String("id", id))
		return ctx.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to mark notification as read",
		})
	}

	if c.Settings != nil && c.Settings.WebServer.Debug {
		c.logDebugIfEnabled("notification marked as read", logger.String("id", id))
	}

	return ctx.JSON(http.StatusOK, map[string]string{
		"message": "Notification marked as read",
	})
}

// MarkNotificationAcknowledged marks a notification as acknowledged
func (c *Controller) MarkNotificationAcknowledged(ctx echo.Context) error {
	return c.executeNotificationAction(ctx, notificationAction{
		operation:      func(s *notification.Service, id string) error { return s.MarkAsAcknowledged(id) },
		errorLogMsg:    "failed to mark notification as acknowledged",
		errorRespMsg:   "Failed to mark notification as acknowledged",
		successRespMsg: "Notification marked as acknowledged",
	})
}

// DeleteNotification deletes a notification
func (c *Controller) DeleteNotification(ctx echo.Context) error {
	return c.executeNotificationAction(ctx, notificationAction{
		operation:      func(s *notification.Service, id string) error { return s.Delete(id) },
		errorLogMsg:    "failed to delete notification",
		errorRespMsg:   "Failed to delete notification",
		successRespMsg: "Notification deleted",
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
		c.logErrorIfEnabled("failed to get unread count", logger.Error(err))
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

	if c.Settings == nil {
		return ctx.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "Settings not initialized",
		})
	}

	service := notification.GetService()

	// Build base URL for links
	baseURL := c.Settings.Security.GetBaseURL(c.Settings.WebServer.Port)

	// Format detection time according to user's time format preference
	now := time.Now()
	var detectionTime string
	if c.Settings.Main.TimeAs24h {
		detectionTime = now.Format("15:04:05")
	} else {
		detectionTime = now.Format("3:04:05 PM")
	}

	// Create test template data with realistic values
	testTemplateData := &notification.TemplateData{
		CommonName:         "Test Bird Species",
		ScientificName:     "Testus birdicus",
		Confidence:         testNotificationConfidence,
		ConfidencePercent:  "99",
		DetectionTime:      detectionTime,
		DetectionDate:      now.Format("2006-01-02"),
		Latitude:           testNotificationLatitude,
		Longitude:          testNotificationLongitude,
		Location:           "Test Location (Sample Data)",
		DetectionID:        "test",
		DetectionPath:      "/ui/detections/test",
		DetectionURL:       baseURL + "/ui/detections/test",
		ImageURL:           "https://static.avicommons.org/houfin-DzFZcHoKwyx9JOmg-320.jpg",
		DaysSinceFirstSeen: 0,
	}

	// Render notification using templates with defaults
	title := renderTemplateWithDefault("title",
		c.Settings.Notification.Templates.NewSpecies.Title,
		"New Species: Test Bird Species",
		testTemplateData, nil)

	message := renderTemplateWithDefault("message",
		c.Settings.Notification.Templates.NewSpecies.Message,
		"First detection of Test Bird Species (Testus birdicus) at Fake Test Location",
		testTemplateData, nil)

	testNotification := notification.NewNotification(notification.TypeDetection, notification.PriorityHigh, title, message).
		WithComponent("detection").
		WithMetadata("species", testTemplateData.CommonName).
		WithMetadata("scientific_name", testTemplateData.ScientificName).
		WithMetadata("confidence", testTemplateData.Confidence).
		WithMetadata("location", testTemplateData.Location).
		WithMetadata("is_new_species", true).
		WithMetadata("days_since_first_seen", testTemplateData.DaysSinceFirstSeen).
		WithMetadata("note_id", 1).
		WithExpiry(newSpeciesNotificationExpiry * time.Hour) // New species notifications expire after 24 hours

	// Expose all TemplateData fields with bg_ prefix for use in provider templates
	// This ensures test notifications have the same metadata as real detections
	// See: https://github.com/tphakala/birdnet-go/issues/1457
	testNotification = notification.EnrichWithTemplateData(testNotification, testTemplateData)

	// Use CreateWithMetadata to persist and broadcast
	if err := service.CreateWithMetadata(testNotification); err != nil {
		c.logErrorIfEnabled("failed to create test notification", logger.Error(err))
		return ctx.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to create test notification",
		})
	}

	if c.Settings != nil && c.Settings.WebServer.Debug {
		c.logDebugIfEnabled("test new species notification created",
			logger.String("notification_id", testNotification.ID),
			logger.String("species", testTemplateData.CommonName),
			logger.String("rendered_title", title),
			logger.String("rendered_message", message))
	}

	return ctx.JSON(http.StatusOK, testNotification)
}
