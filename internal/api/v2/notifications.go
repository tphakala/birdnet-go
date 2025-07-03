package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// SSENotificationData represents notification data sent via SSE
type SSENotificationData struct {
	*notification.Notification
	EventType string `json:"eventType"`
}

// NotificationClient represents a connected notification SSE client
type NotificationClient struct {
	ID           string
	Channel      chan *notification.Notification
	Request      *http.Request
	Response     http.ResponseWriter
	Done         chan bool
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
				Rate:      1,               // 1 request
				Burst:     5,               // Allow burst of 5
				ExpiresIn: 1 * time.Minute, // Per minute
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

	// SSE endpoint for notification stream
	c.Group.GET("/notifications/stream", c.StreamNotifications, middleware.RateLimiterWithConfig(rateLimiterConfig))

	// REST endpoints for notification management
	c.Group.GET("/notifications", c.GetNotifications)
	c.Group.GET("/notifications/:id", c.GetNotification)
	c.Group.PUT("/notifications/:id/read", c.MarkNotificationRead)
	c.Group.PUT("/notifications/:id/acknowledge", c.MarkNotificationAcknowledged)
	c.Group.DELETE("/notifications/:id", c.DeleteNotification)
	c.Group.GET("/notifications/unread/count", c.GetUnreadCount)
}

// StreamNotifications handles the SSE connection for real-time notification streaming
func (c *Controller) StreamNotifications(ctx echo.Context) error {
	// Check if notification service is initialized
	if !notification.IsInitialized() {
		return ctx.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "Notification service not available",
		})
	}

	// Set SSE headers
	ctx.Response().Header().Set("Content-Type", "text/event-stream")
	ctx.Response().Header().Set("Cache-Control", "no-cache")
	ctx.Response().Header().Set("Connection", "keep-alive")
	ctx.Response().Header().Set("Access-Control-Allow-Origin", "*")
	ctx.Response().Header().Set("Access-Control-Allow-Headers", "Cache-Control")

	// Generate client ID
	clientID := uuid.New().String()

	// Subscribe to notifications
	service := notification.GetService()
	notificationCh, notificationCtx := service.Subscribe()

	// Create notification client
	client := &NotificationClient{
		ID:           clientID,
		Channel:      make(chan *notification.Notification, 10),
		Request:      ctx.Request(),
		Response:     ctx.Response(),
		Done:         make(chan bool),
		SubscriberCh: notificationCh,
		Context:      notificationCtx,
	}

	// Send initial connection message
	if err := c.sendSSEMessage(ctx, "connected", map[string]string{
		"clientId": clientID,
		"message":  "Connected to notification stream",
	}); err != nil {
		service.Unsubscribe(notificationCh)
		return err
	}

	// Log the connection
	if c.apiLogger != nil {
		if c.Settings != nil && c.Settings.WebServer.Debug {
			c.apiLogger.Debug("notification SSE client connected",
				"clientId", clientID,
				"ip", privacy.AnonymizeIP(ctx.RealIP()),
				"user_agent", ctx.Request().UserAgent())
		} else {
			c.apiLogger.Info("notification SSE client connected",
				"clientId", clientID,
				"ip", privacy.AnonymizeIP(ctx.RealIP()))
		}
	}

	// Handle client disconnect
	go func() {
		<-ctx.Request().Context().Done()
		client.Done <- true
		service.Unsubscribe(notificationCh)
		if c.apiLogger != nil {
			if c.Settings != nil && c.Settings.WebServer.Debug {
				c.apiLogger.Debug("notification SSE client disconnected",
					"clientId", clientID,
					"reason", "context_done")
			} else {
				c.apiLogger.Info("notification SSE client disconnected",
					"clientId", clientID)
			}
		}
	}()

	// Send heartbeat every 30 seconds
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Main event loop
	for {
		select {
		case notif := <-notificationCh:
			if notif == nil {
				// Channel closed, service is shutting down
				return nil
			}

			// Send notification event
			event := SSENotificationData{
				Notification: notif,
				EventType:    "notification",
			}

			if err := c.sendSSEMessage(ctx, "notification", event); err != nil {
				if c.apiLogger != nil {
					c.apiLogger.Error("failed to send notification SSE",
						"error", err,
						"clientId", clientID,
					)
				}
				return err
			}
			
			if c.apiLogger != nil && c.Settings != nil && c.Settings.WebServer.Debug {
				c.apiLogger.Debug("notification sent via SSE",
					"clientId", clientID,
					"notification_id", notif.ID,
					"type", notif.Type,
					"priority", notif.Priority)
			}

		case <-ticker.C:
			// Send heartbeat
			if err := c.sendSSEMessage(ctx, "heartbeat", map[string]string{
				"timestamp": time.Now().Format(time.RFC3339),
			}); err != nil {
				return err
			}

		case <-client.Done:
			// Client disconnected
			return nil

		case <-notificationCtx.Done():
			// Subscription cancelled
			return nil
		}
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
		unreadCount, _ := service.GetUnreadCount()
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
		if c.apiLogger != nil {
			c.apiLogger.Error("failed to get notification", "error", err, "id", id)
		}
		return ctx.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to retrieve notification",
		})
	}

	if notif == nil {
		return ctx.JSON(http.StatusNotFound, map[string]string{
			"error": "Notification not found",
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
	
	if c.apiLogger != nil && c.Settings != nil && c.Settings.WebServer.Debug {
		c.apiLogger.Debug("marking notification as read", "id", id)
	}
	
	if err := service.MarkAsRead(id); err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("failed to mark notification as read", "error", err, "id", id)
		}
		return ctx.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to mark notification as read",
		})
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

