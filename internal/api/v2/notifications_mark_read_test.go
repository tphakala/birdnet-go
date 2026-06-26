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
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// newNotificationTestController builds a controller wired to an isolated, per-test
// notification service injected through the controller's DI seam. The service is
// stopped via t.Cleanup so its cleanupLoop goroutine does not leak (TestMain runs
// a goleak gate). Because no process-global state is touched, tests using this
// helper are safe to run with t.Parallel().
func newNotificationTestController(t *testing.T) (*Controller, *notification.Service) {
	t.Helper()

	config := &notification.ServiceConfig{
		Debug:              true,
		MaxNotifications:   100,
		CleanupInterval:    30 * time.Minute,
		RateLimitWindow:    1 * time.Minute,
		RateLimitMaxEvents: 10,
	}

	service := notification.NewService(config)
	t.Cleanup(service.Stop)

	controller := &Controller{Core: &apicore.Core{}, notificationService: service}
	controller.Settings.Store(&conf.Settings{})

	return controller, service
}

// runMarkNotificationNotFoundTest exercises a mark-state handler against a
// missing notification ID and asserts the idempotent 200 response with the
// handler's confirmation message. Shared by the read and acknowledge NotFound
// cases, which are identical apart from the path suffix, handler, and message.
func runMarkNotificationNotFoundTest(t *testing.T, pathSuffix, expectedMessage string, handler func(*Controller, echo.Context) error) {
	t.Helper()

	controller, _ := newNotificationTestController(t)

	e := echo.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v2/notifications/non-existent-id/"+pathSuffix, http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues("non-existent-id")

	err := handler(controller, ctx)
	require.NoError(t, err, "handler should return nil")

	assert.Equal(t, http.StatusOK, rec.Code, "missing notification returns 200 (idempotent)")

	var body map[string]any
	err = json.Unmarshal(rec.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Contains(t, body["message"], expectedMessage)
}

func TestMarkNotificationRead_NotFound(t *testing.T) {
	t.Parallel()
	runMarkNotificationNotFoundTest(t, "read", "Notification marked as read",
		(*Controller).MarkNotificationRead)
}

func TestMarkNotificationRead_EmptyID(t *testing.T) {
	t.Parallel()
	controller, _ := newNotificationTestController(t)

	e := echo.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v2/notifications//read", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues("")

	err := controller.MarkNotificationRead(ctx)
	require.NoError(t, err)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "expected 400 for empty ID")
}

func TestMarkNotificationRead_Success(t *testing.T) {
	t.Parallel()
	controller, service := newNotificationTestController(t)

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

	err = controller.MarkNotificationRead(ctx)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, rec.Code, "expected 200 for successful mark-as-read")
}

func TestMarkNotificationAcknowledged_NotFound(t *testing.T) {
	t.Parallel()
	runMarkNotificationNotFoundTest(t, "acknowledge", "Notification marked as acknowledged",
		(*Controller).MarkNotificationAcknowledged)
}
