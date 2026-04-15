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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestNotifications_PublicReadsAllowed verifies that the dashboard-facing
// read endpoints are reachable without auth.
func TestNotifications_PublicReadsAllowed(t *testing.T) {
	e := newSettingsAuthTestEnv(t)

	for _, path := range []string{
		"/api/v2/notifications",
		"/api/v2/notifications/unread/count",
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
// card's backing endpoint is reachable without auth.
func TestQuietHours_PublicReadsAllowed(t *testing.T) {
	e := newSettingsAuthTestEnv(t)

	rec := guestGet(e, "/api/v2/streams/quiet-hours/status")
	require.Equal(t, http.StatusOK, rec.Code,
		"guest must be able to read quiet-hours status, got: %s", rec.Body.String())

	body := rec.Body.String()
	// The handler sanitizes stream URLs via privacy.SanitizeStreamUrl, so
	// no raw "user:password@" userinfo should appear in the guest response.
	assert.NotContains(t, body, "@",
		"quiet-hours response must not include unredacted stream URLs: %s", body)
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
