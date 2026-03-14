package v2

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// GetNotifications handles GET /api/v2/notifications
func (h *Handler) GetNotifications(c echo.Context) error {
	notifications, err := h.DS.GetNotifications()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch notifications")
	}

	return c.JSON(http.StatusOK, notifications)
}

// MarkNotificationAsRead handles PUT /api/v2/notifications/:id/read
func (h *Handler) MarkNotificationAsRead(c echo.Context) error {
	notificationID := c.Param("id")

	err := h.DS.MarkNotificationAsRead(notificationID)
	if err != nil {
		if err == datastore.ErrNotificationNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Notification not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to mark notification as read")
	}

	return c.NoContent(http.StatusOK)
}

// DeleteNotification handles DELETE /api/v2/notifications/:id
func (h *Handler) DeleteNotification(c echo.Context) error {
	notificationID := c.Param("id")

	err := h.DS.DeleteNotification(notificationID)
	if err != nil {
		if err == datastore.ErrNotificationNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Notification not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to delete notification")
	}

	return c.NoContent(http.StatusOK)
}

// ClearAllNotifications handles DELETE /api/v2/notifications
func (h *Handler) ClearAllNotifications(c echo.Context) error {
	err := h.DS.ClearAllNotifications()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to clear notifications")
	}

	return c.NoContent(http.StatusOK)
}
