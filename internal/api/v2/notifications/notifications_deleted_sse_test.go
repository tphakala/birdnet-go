package notifications

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/notification"
)

// runLoop runs a notification SSE event loop for client until drained reports
// that every queued event was received, then disconnects it and returns what
// the loop wrote to the wire.
func runLoop(t *testing.T, client *NotificationClient, drained func() bool) string {
	t.Helper()
	c := mockHandler()
	c.SetTestContext(t.Context(), nil)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, sseEndpoint, http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	errCh := make(chan error, 1)
	go func() { errCh <- c.runNotificationEventLoop(ctx, client) }()

	// The loop writes each received event before it selects again, so once the
	// queue is drained the Done signal is seen only after the last write.
	require.Eventually(t, drained, 2*time.Second, time.Millisecond)
	client.Done <- struct{}{}
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		require.Fail(t, "event loop did not return after disconnect")
	}
	return rec.Body.String()
}

// runLoopWithDeletions feeds events into a notification SSE event loop and
// returns what it wrote to the wire.
func runLoopWithDeletions(t *testing.T, guest bool, events ...notification.DeletedEvent) string {
	t.Helper()
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
	return runLoop(t, client, func() bool { return len(deletions) == 0 })
}

// runLoopWithNotifications feeds created notifications into a notification SSE
// event loop and returns what it wrote to the wire.
func runLoopWithNotifications(t *testing.T, guest bool, notifs ...*notification.Notification) string {
	t.Helper()
	created := make(chan *notification.Notification, len(notifs))
	for _, n := range notifs {
		created <- n
	}
	client := &NotificationClient{
		ID:           "created-sse-client",
		Done:         make(chan struct{}, 1),
		SubscriberCh: created,
		DeletionCh:   make(chan notification.DeletedEvent),
		Context:      t.Context(),
		Guest:        guest,
	}
	return runLoop(t, client, func() bool { return len(created) == 0 })
}

// TestNotificationEventLoop_SkipsPushOnlyNotifications pins that a notification
// routed only to push providers never reaches a browser, for authenticated and
// guest clients alike, while notifications for the bell or for all channels
// still arrive.
func TestNotificationEventLoop_SkipsPushOnlyNotifications(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		guest bool
	}{
		{name: "authenticated client", guest: false},
		{name: "guest client", guest: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Detection notifications pass the guest filter, so only the
			// delivery target decides whether each one arrives.
			pushOnly := notification.NewNotification(notification.TypeDetection, notification.PriorityHigh, "push-only", "m").
				WithDeliveryTarget(notification.DeliveryTargetPush)
			forAll := notification.NewNotification(notification.TypeDetection, notification.PriorityMedium, "for-all", "m")
			bellOnly := notification.NewNotification(notification.TypeDetection, notification.PriorityMedium, "bell-only", "m").
				WithDeliveryTarget(notification.DeliveryTargetBell)

			body := runLoopWithNotifications(t, tt.guest, pushOnly, forAll, bellOnly)

			assert.NotContains(t, body, pushOnly.ID, "a push-only notification must not reach the client")
			assert.Contains(t, body, forAll.ID, "a notification for all channels still arrives")
			assert.Contains(t, body, bellOnly.ID, "a bell-only notification still arrives")
		})
	}
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
		notification.DeletedEvent{ID: "toast-1", Type: notification.TypeDetection, Toast: true},
		notification.DeletedEvent{ID: "unknown-1"},
		notification.DeletedEvent{ID: "detection-1", Type: notification.TypeDetection},
	)
	assert.NotContains(t, body, "operational-1", "guests never saw operational notices, so they get no deletion for them")
	assert.NotContains(t, body, "toast-1", "nor toasts")
	assert.NotContains(t, body, "unknown-1", "nor a deletion whose type is unknown")
	assert.Contains(t, body, `"id":"detection-1"`)
}

// syncRecorder is an httptest.ResponseRecorder whose writes can be read while
// the SSE handler is still streaming.
type syncRecorder struct {
	*httptest.ResponseRecorder
	mu sync.Mutex
}

func (r *syncRecorder) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ResponseRecorder.Write(b)
}

func (r *syncRecorder) body() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Body.String()
}

// TestStreamNotifications_PushesServerDeletes drives the real stream handler:
// it subscribes to deletions, so deleting a notification on the service reaches
// the client as a notification_deleted event.
func TestStreamNotifications_PushesServerDeletes(t *testing.T) {
	t.Parallel()
	h, svc := newNotificationTestHandler(t)
	h.SetTestContext(t.Context(), nil)

	e := echo.New()
	reqCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, sseEndpoint, http.NoBody).WithContext(reqCtx)
	rec := &syncRecorder{ResponseRecorder: httptest.NewRecorder()}
	ctx := e.NewContext(req, rec)

	errCh := make(chan error, 1)
	go func() { errCh <- h.StreamNotifications(ctx) }()
	require.Eventually(t, func() bool { return strings.Contains(rec.body(), "event: connected") },
		2*time.Second, time.Millisecond)

	// A detection notice, so the event also passes the guest filter.
	n := notification.NewNotification(notification.TypeDetection, notification.PriorityMedium, "t", "m")
	require.NoError(t, svc.CreateWithMetadata(n))
	require.NoError(t, svc.Delete(n.ID))
	require.Eventually(t, func() bool {
		body := rec.body()
		return strings.Contains(body, "event: "+sseEventNotificationDeleted) && strings.Contains(body, n.ID)
	}, 2*time.Second, time.Millisecond)

	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err, "stream handler should end cleanly after the client left")
	case <-time.After(3 * time.Second):
		require.Fail(t, "stream handler did not return after the client left")
	}
}
