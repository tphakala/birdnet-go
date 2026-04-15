// dashboard_public_endpoints_test.go: Tests the dashboard-public carve-outs
// for /api/v2/notifications/*, /api/v2/streams/*, and
// /api/v2/streams/quiet-hours/status.
//
// Context: when BasicAuth/OAuth is enabled, the dashboard SPA still needs to
// render for unauthenticated guests (detections, analytics, and status panels
// are advertised as public read-only). PR #2763 carved out
// /api/v2/settings/dashboard; this file locks in the remaining carve-outs
// for the NotificationBell, "Currently Hearing" card, and stream status
// panels. Mutations and SSRF-prone endpoints must remain auth-protected.
//
// These tests reuse newSettingsAuthTestEnv (settings_public_dashboard_test.go)
// which registers the full /api/v2 route tree with a non-LAN remote IP, so
// IsAuthRequired() returns true regardless of SubnetBypass.

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// ---- helpers -----------------------------------------------------------

// assertNotUnauthorized fails the test if the response is HTTP 401. Public
// endpoints may legitimately return non-200 status (503 when a subsystem is
// not initialized in tests, 404 for unknown URLs) but MUST NOT reject a
// guest with 401.
func assertNotUnauthorized(t *testing.T, rec *httptest.ResponseRecorder, path string) {
	t.Helper()
	require.NotEqual(t, http.StatusUnauthorized, rec.Code,
		"guest must be able to read %s, got 401: %s", path, rec.Body.String())
}

// assertUnauthorized checks that a guest request returns HTTP 401.
func assertUnauthorized(t *testing.T, rec *httptest.ResponseRecorder, method, path string) {
	t.Helper()
	assert.Equal(t, http.StatusUnauthorized, rec.Code,
		"%s %s must remain auth-protected, got: %d %s",
		method, path, rec.Code, rec.Body.String())
}

// guestGet executes an unauthenticated GET request through the echo instance.
func guestGet(e *echo.Echo, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// ---- notifications -----------------------------------------------------

// ensureNotificationServiceInitialized brings up a notification service for
// tests that need to drive the notification path end-to-end. Callers get the
// active service back and are responsible for cleanup of any fixtures they
// add (the service itself is process-global and persists across tests).
func ensureNotificationServiceInitialized(t *testing.T) *notification.Service {
	t.Helper()
	if !notification.IsInitialized() {
		notification.Initialize(notification.DefaultServiceConfig())
	}
	svc := notification.GetService()
	require.NotNil(t, svc, "notification service must be initialized for guest-filter tests")
	return svc
}

// TestNotifications_PublicReadsAllowed verifies that the dashboard-facing
// read endpoints are reachable without auth. Including the SSE endpoint is
// important: NotificationBell opens a persistent /notifications/stream
// subscription as soon as the page loads, and a 401 there would be more
// visible than the list endpoint failing.
func TestNotifications_PublicReadsAllowed(t *testing.T) {
	e := newSettingsAuthTestEnv(t)

	for _, path := range []string{
		"/api/v2/notifications",
		"/api/v2/notifications/unread/count",
		"/api/v2/notifications/stream",
	} {
		t.Run(path, func(t *testing.T) {
			rec := guestGet(e, path)
			assertNotUnauthorized(t, rec, path)
		})
	}
}

// TestNotifications_MutationsRequireAuth guards that write/admin endpoints
// stay behind authMiddleware.
func TestNotifications_MutationsRequireAuth(t *testing.T) {
	e := newSettingsAuthTestEnv(t)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPut, "/api/v2/notifications/some-id/read"},
		{http.MethodPut, "/api/v2/notifications/some-id/acknowledge"},
		{http.MethodDelete, "/api/v2/notifications/some-id"},
		{http.MethodPost, "/api/v2/notifications/test/new-species"},
		// /:id read and /check-ntfy-server stay auth-protected
		{http.MethodGet, "/api/v2/notifications/some-id"},
		{http.MethodGet, "/api/v2/notifications/check-ntfy-server?host=example.com"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			var body *bytes.Buffer
			if tc.method == http.MethodPost || tc.method == http.MethodPut {
				body = bytes.NewBufferString(`{}`)
			} else {
				body = bytes.NewBuffer(nil)
			}
			req := httptest.NewRequest(tc.method, tc.path, body)
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			assertUnauthorized(t, rec, tc.method, tc.path)
		})
	}
}

// TestNotifications_GuestListFiltersNonDetectionAndToasts drives the full
// handler with an initialized notification service to prove that a guest
// caller only sees TypeDetection non-toast notifications. This guards
// against the toast-metadata leak path (a TypeDetection notification with
// MetadataKeyIsToast=true must not reach unauthenticated callers).
func TestNotifications_GuestListFiltersNonDetectionAndToasts(t *testing.T) {
	svc := ensureNotificationServiceInitialized(t)

	detection, err := svc.Create(notification.TypeDetection, notification.PriorityHigh,
		"Detection: Test Bird", "guest-visible detection body")
	require.NoError(t, err)

	adminErr, err := svc.Create(notification.TypeError, notification.PriorityHigh,
		"MQTT failed", "admin-only error body with host example.com:1883")
	require.NoError(t, err)

	detectionToast := notification.NewNotification(notification.TypeDetection,
		notification.PriorityLow, "Detection toast title", "detection-typed toast body").
		WithMetadata(notification.MetadataKeyIsToast, true)
	require.NoError(t, svc.CreateWithMetadata(detectionToast))

	// Best-effort cleanup so repeated runs do not accumulate fixtures in
	// the process-global service. Ignore not-found errors (Delete may
	// already have been issued by a parallel test).
	t.Cleanup(func() {
		for _, n := range []*notification.Notification{detection, adminErr, detectionToast} {
			if n == nil {
				continue
			}
			_ = svc.Delete(n.ID)
		}
	})

	e := newSettingsAuthTestEnv(t)

	rec := guestGet(e, "/api/v2/notifications?limit=50")
	require.Equal(t, http.StatusOK, rec.Code,
		"guest list must succeed, got: %s", rec.Body.String())

	var payload struct {
		Notifications []*notification.Notification `json:"notifications"`
		Count         int                          `json:"count"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))

	sawDetection := false
	for _, n := range payload.Notifications {
		require.NotNil(t, n)
		assert.Equal(t, notification.TypeDetection, n.Type,
			"guest list leaked non-detection notification %q (id=%s)", n.Type, n.ID)
		isToast, _ := n.Metadata[notification.MetadataKeyIsToast].(bool)
		assert.False(t, isToast,
			"guest list leaked toast notification %q (id=%s)", n.Title, n.ID)
		if n.ID == detection.ID {
			sawDetection = true
		}
		assert.NotEqual(t, adminErr.ID, n.ID,
			"guest list must not include admin error notification")
		assert.NotEqual(t, detectionToast.ID, n.ID,
			"guest list must not include detection-typed toast")
	}
	assert.True(t, sawDetection,
		"expected test detection notification to be visible to guest")
}

// TestNotifications_GuestUnreadCountIgnoresNonDetectionAndToasts verifies
// that the guest /unread/count matches what the guest list actually shows.
func TestNotifications_GuestUnreadCountIgnoresNonDetectionAndToasts(t *testing.T) {
	svc := ensureNotificationServiceInitialized(t)

	det, err := svc.Create(notification.TypeDetection, notification.PriorityHigh,
		"Detection A", "body")
	require.NoError(t, err)
	errNotif, err := svc.Create(notification.TypeError, notification.PriorityHigh,
		"Error A", "body")
	require.NoError(t, err)
	detToast := notification.NewNotification(notification.TypeDetection,
		notification.PriorityLow, "Detection toast", "body").
		WithMetadata(notification.MetadataKeyIsToast, true)
	require.NoError(t, svc.CreateWithMetadata(detToast))

	t.Cleanup(func() {
		for _, n := range []*notification.Notification{det, errNotif, detToast} {
			if n == nil {
				continue
			}
			_ = svc.Delete(n.ID)
		}
	})

	e := newSettingsAuthTestEnv(t)

	rec := guestGet(e, "/api/v2/notifications/unread/count")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var payload struct {
		UnreadCount int `json:"unreadCount"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))

	// The store may contain fixtures from other tests, so instead of an
	// absolute equality we assert the count strictly matches the list the
	// same guest would see.
	listRec := guestGet(e, "/api/v2/notifications?status=unread&limit=1000")
	require.Equal(t, http.StatusOK, listRec.Code)
	var listPayload struct {
		Notifications []*notification.Notification `json:"notifications"`
	}
	require.NoError(t, json.Unmarshal(listRec.Body.Bytes(), &listPayload))

	assert.Equal(t, len(listPayload.Notifications), payload.UnreadCount,
		"guest unread count must equal guest-visible unread list length")
}

// ---- streams/health + streams/status (auth-protected) -----------------

// TestStreamsHealth_AllAuthProtected is a regression guard that the stream
// health/status endpoints stay auth-protected. They are consumed only by the
// StreamManager settings form (which sits behind auth), not by the dashboard,
// so they must not be widened to guest access where they would leak stream
// topology (target hosts, ports, FFmpeg diagnostics) to anonymous callers.
func TestStreamsHealth_AllAuthProtected(t *testing.T) {
	e := newSettingsAuthTestEnv(t)

	for _, path := range []string{
		"/api/v2/streams/health",
		"/api/v2/streams/status",
		"/api/v2/streams/health/some-source-id",
		"/api/v2/streams/health/stream",
	} {
		t.Run(path, func(t *testing.T) {
			rec := guestGet(e, path)
			assertUnauthorized(t, rec, http.MethodGet, path)
		})
	}
}

// ---- quiet-hours -------------------------------------------------------

// TestQuietHours_PublicReadsAllowed verifies that the "Currently Hearing"
// card's backing endpoint is reachable without auth and that the response
// shape is suitable for the frontend indicator.
func TestQuietHours_PublicReadsAllowed(t *testing.T) {
	e := newSettingsAuthTestEnv(t)

	rec := guestGet(e, "/api/v2/streams/quiet-hours/status")
	require.Equal(t, http.StatusOK, rec.Code,
		"guest must be able to read quiet-hours status, got: %s", rec.Body.String())

	var payload struct {
		AnyActive           bool            `json:"anyActive"`
		SoundCardSuppressed bool            `json:"soundCardSuppressed"`
		SuppressedStreams   map[string]bool `json:"suppressedStreams"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))

	// The handler must not leak userinfo via the stream URL keys. For guest
	// requests the keys are opaque placeholders ("stream-N"), so any "://"
	// or "@" in a key indicates a regression.
	for key := range payload.SuppressedStreams {
		assert.NotContains(t, key, "://",
			"guest quiet-hours response leaks stream URL in key %q", key)
		assert.NotContains(t, key, "@",
			"guest quiet-hours response leaks userinfo in key %q", key)
	}
}

// ---- regression guard --------------------------------------------------

// TestQuietHours_NoRawCredentialsInResponse is a defense-in-depth guard:
// the public quiet-hours endpoint must not leak raw RTSP credentials through
// the SuppressedStreams map keys. The handler passes each URL through
// privacy.SanitizeStreamUrl before encoding, but this test asserts the
// invariant directly on the JSON body.
func TestQuietHours_NoRawCredentialsInResponse(t *testing.T) {
	e := newSettingsAuthTestEnv(t)

	rec := guestGet(e, "/api/v2/streams/quiet-hours/status")
	body := rec.Body.String()

	// Walk every occurrence of a stream scheme and inspect the authority
	// segment (between "scheme://" and the next '/'). An "@" inside the
	// authority would indicate userinfo leakage (e.g. user:pass@host).
	for _, scheme := range []string{"rtsp://", "rtsps://", "rtmp://", "rtmps://", "http://", "https://"} {
		remaining := body
		for {
			idx := strings.Index(remaining, scheme)
			if idx < 0 {
				break
			}
			authority := remaining[idx+len(scheme):]
			if slash := strings.IndexByte(authority, '/'); slash >= 0 {
				authority = authority[:slash]
			}
			assert.NotContains(t, authority, "@",
				"quiet-hours response leaks raw credentials in %s authority %q (body: %s)",
				scheme, authority, body)
			remaining = remaining[idx+len(scheme):]
		}
	}
}
