package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// setupNotificationTestService initializes a notification service for testing
// and returns a cleanup function.
func setupNotificationTestService(t *testing.T) *notification.Service {
	t.Helper()

	config := &notification.ServiceConfig{
		Debug:              true,
		MaxNotifications:   100,
		CleanupInterval:    30 * time.Minute,
		RateLimitWindow:    1 * time.Minute,
		RateLimitMaxEvents: 10,
	}

	service := notification.NewService(config)
	if err := notification.SetServiceForTesting(service); err != nil {
		// An instance already exists; stop the service we just created so its
		// cleanupLoop goroutine does not leak (the gate in TestMain would flag
		// it), then use the existing singleton. We deliberately do NOT stop the
		// service on the success path: it becomes the global singleton, which is
		// stopped once after the whole suite in TestMain. Stopping it here would
		// leave later GetService() callers with a stopped instance.
		service.Stop()
		service = notification.GetService()
		require.NotNil(t, service, "Expected notification service to be available")
	}

	return service
}

func TestMarkNotificationRead_NotFound(t *testing.T) {
	service := setupNotificationTestService(t)
	require.NotNil(t, service)

	e := echo.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v2/notifications/non-existent-id/read", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues("non-existent-id")

	controller := &Controller{
		Settings: &conf.Settings{},
	}

	err := controller.MarkNotificationRead(ctx)
	require.NoError(t, err, "handler should return nil")

	assert.Equal(t, http.StatusOK, rec.Code, "mark-as-read is idempotent: missing notification returns 200")

	var body map[string]any
	err = json.Unmarshal(rec.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Contains(t, body["message"], "Notification marked as read")
}

func TestMarkNotificationRead_EmptyID(t *testing.T) {
	service := setupNotificationTestService(t)
	require.NotNil(t, service)

	e := echo.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v2/notifications//read", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues("")

	controller := &Controller{
		Settings: &conf.Settings{},
	}

	err := controller.MarkNotificationRead(ctx)
	require.NoError(t, err)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "expected 400 for empty ID")
}

func TestMarkNotificationRead_Success(t *testing.T) {
	service := setupNotificationTestService(t)
	require.NotNil(t, service)

	// Create a notification first
	notif := notification.NewNotification(notification.TypeInfo, notification.PriorityMedium, "Test", "Test message")
	err := service.CreateWithMetadata(notif)
	require.NoError(t, err)

	e := echo.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v2/notifications/"+notif.ID+"/read", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues(notif.ID)

	controller := &Controller{
		Settings: &conf.Settings{},
	}

	err = controller.MarkNotificationRead(ctx)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, rec.Code, "expected 200 for successful mark-as-read")
}

func TestMarkNotificationAcknowledged_NotFound(t *testing.T) {
	service := setupNotificationTestService(t)
	require.NotNil(t, service)

	e := echo.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v2/notifications/non-existent-id/acknowledge", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues("non-existent-id")

	controller := &Controller{
		Settings: &conf.Settings{},
	}

	err := controller.MarkNotificationAcknowledged(ctx)
	require.NoError(t, err, "handler should return nil")

	assert.Equal(t, http.StatusOK, rec.Code, "mark-as-acknowledged is idempotent: missing notification returns 200")

	var body map[string]any
	err = json.Unmarshal(rec.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Contains(t, body["message"], "Notification marked as acknowledged")
}
