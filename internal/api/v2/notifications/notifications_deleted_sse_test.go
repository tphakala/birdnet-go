package notifications

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/notification"
)

// runLoopWithDeletions feeds events into a notification SSE event loop and
// returns what it wrote to the wire.
func runLoopWithDeletions(t *testing.T, guest bool, events ...notification.DeletedEvent) string {
	t.Helper()
	c := mockHandler()
	c.SetTestContext(t.Context(), nil)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, sseEndpoint, http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	deletions := make(chan notification.DeletedEvent, len(events))
	for _, ev := range events {
		deletions <- ev
	}
	client := &NotificationClient{
		ID:           "deleted-sse-client",
		Done:         make(chan struct{}, 1),
		SubscriberCh: make(chan *notification.Notification),
		DeletionCh:   deletions,
		Context:      t.Context(),
		Guest:        guest,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- c.runNotificationEventLoop(ctx, client) }()

	// Wait until the loop drained the queued events, then disconnect it.
	require.Eventually(t, func() bool { return len(deletions) == 0 }, 2*time.Second, time.Millisecond)
	time.Sleep(20 * time.Millisecond) // let the last drained event reach the writer
	client.Done <- struct{}{}
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		require.Fail(t, "event loop did not return after disconnect")
	}
	return rec.Body.String()
}

func TestNotificationEventLoop_SendsDeletedEvent(t *testing.T) {
	t.Parallel()
	body := runLoopWithDeletions(t, false, notification.DeletedEvent{ID: "abc-123", Type: notification.TypeError})
	assert.Contains(t, body, "event: "+sseEventNotificationDeleted)
	assert.Contains(t, body, `"id":"abc-123"`)
}

func TestNotificationEventLoop_GuestOnlyGetsDetectionDeletions(t *testing.T) {
	t.Parallel()
	body := runLoopWithDeletions(t, true,
		notification.DeletedEvent{ID: "operational-1", Type: notification.TypeError},
		notification.DeletedEvent{ID: "detection-1", Type: notification.TypeDetection},
	)
	assert.NotContains(t, body, "operational-1", "guests never saw operational notices, so they get no deletion for them")
	assert.Contains(t, body, `"id":"detection-1"`)
}
