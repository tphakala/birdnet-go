package api

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

const (
	connectionTestPeer   = "172.18.0.1"
	connectionTestClient = "198.51.100.9"
)

// connectionResponseKeys are the exact JSON keys of the connection check.
var connectionResponseKeys = []string{
	"status", "peerAddress", "hasForwardedHeaders", "peerIsTrustedProxy",
	"verifiedClientAddress", "peerIsCloudflareEdge", "cloudflarePresetWouldVerify",
}

// getConnectionCheck drives GetConnectionCheck with a request from remoteAddr
// carrying the given headers and returns the recorder.
func getConnectionCheck(t *testing.T, e *echo.Echo, c *Controller, remoteAddr string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/settings/connection", http.NoBody)
	req.RemoteAddr = remoteAddr
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	require.NoError(t, c.GetConnectionCheck(e.NewContext(req, rec)))
	return rec
}

// decodeConnectionCheck decodes the response and asserts its exact key set.
func decodeConnectionCheck(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.ElementsMatch(t, connectionResponseKeys, slices.Collect(maps.Keys(body)), "response must carry exactly the documented keys")
	return body
}

// TestGetConnectionCheck_Statuses drives one request per status through the
// handler and pins the exact response values.
func TestGetConnectionCheck_Statuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		trusted    []string
		remoteAddr string
		headers    map[string]string
		want       map[string]any
	}{
		{
			name: "direct", remoteAddr: "192.168.1.20:5000",
			want: map[string]any{"status": "direct", "peerAddress": "192.168.1.20", "hasForwardedHeaders": false, "peerIsTrustedProxy": false, "verifiedClientAddress": "192.168.1.20", "peerIsCloudflareEdge": false, "cloudflarePresetWouldVerify": false},
		},
		{
			name: "trusted proxy", trusted: []string{connectionTestPeer}, remoteAddr: connectionTestPeer + ":5000",
			headers: map[string]string{"X-Forwarded-For": connectionTestClient},
			want:    map[string]any{"status": "trusted_proxy", "peerAddress": connectionTestPeer, "hasForwardedHeaders": true, "peerIsTrustedProxy": true, "verifiedClientAddress": connectionTestClient, "peerIsCloudflareEdge": false, "cloudflarePresetWouldVerify": false},
		},
		{
			name: "trusted proxy unverified", trusted: []string{connectionTestPeer}, remoteAddr: connectionTestPeer + ":5000",
			headers: map[string]string{"Forwarded": "for=" + connectionTestClient},
			want:    map[string]any{"status": "trusted_proxy_unverified", "peerAddress": connectionTestPeer, "hasForwardedHeaders": true, "peerIsTrustedProxy": true, "verifiedClientAddress": "", "peerIsCloudflareEdge": false, "cloudflarePresetWouldVerify": false},
		},
		{
			name: "untrusted proxy", remoteAddr: connectionTestPeer + ":5000",
			headers: map[string]string{"X-Forwarded-For": connectionTestClient},
			want:    map[string]any{"status": "untrusted_proxy", "peerAddress": connectionTestPeer, "hasForwardedHeaders": true, "peerIsTrustedProxy": false, "verifiedClientAddress": "", "peerIsCloudflareEdge": false, "cloudflarePresetWouldVerify": false},
		},
		{
			name: "untrusted public proxy", remoteAddr: "203.0.113.50:5000",
			headers: map[string]string{"X-Forwarded-For": connectionTestClient},
			want:    map[string]any{"status": "untrusted_public_proxy", "peerAddress": "203.0.113.50", "hasForwardedHeaders": true, "peerIsTrustedProxy": false, "verifiedClientAddress": "", "peerIsCloudflareEdge": false, "cloudflarePresetWouldVerify": false},
		},
		{
			name: "untrusted proxy unverifiable", remoteAddr: connectionTestPeer + ":5000",
			headers: map[string]string{"Forwarded": "for=" + connectionTestClient},
			want:    map[string]any{"status": "untrusted_proxy_unverifiable", "peerAddress": connectionTestPeer, "hasForwardedHeaders": true, "peerIsTrustedProxy": false, "verifiedClientAddress": "", "peerIsCloudflareEdge": false, "cloudflarePresetWouldVerify": false},
		},
		{
			name: "Cloudflare edge without the preset", remoteAddr: "173.245.48.10:443",
			headers: map[string]string{"CF-Connecting-IP": connectionTestClient},
			want:    map[string]any{"status": "untrusted_public_proxy", "peerAddress": "173.245.48.10", "hasForwardedHeaders": true, "peerIsTrustedProxy": false, "verifiedClientAddress": "", "peerIsCloudflareEdge": true, "cloudflarePresetWouldVerify": true},
		},
		{
			name: "unparseable peer without headers", remoteAddr: "@",
			want: map[string]any{"status": "direct", "peerAddress": "", "hasForwardedHeaders": false, "peerIsTrustedProxy": false, "verifiedClientAddress": "", "peerIsCloudflareEdge": false, "cloudflarePresetWouldVerify": false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := echo.New()
			c := getTestController(t, e)
			settings := getTestSettings(t)
			settings.Security.TrustedProxies = tt.trusted
			c.Settings.Store(settings)

			rec := getConnectionCheck(t, e, c, tt.remoteAddr, tt.headers)
			body := decodeConnectionCheck(t, rec)
			assert.Equal(t, tt.want, body)
			assert.Equal(t, "no-store", rec.Header().Get(echo.HeaderCacheControl))
		})
	}
}

// TestGetConnectionCheck_HotReload verifies the check follows the saved list
// per request, without a restart (invariant I4).
func TestGetConnectionCheck_HotReload(t *testing.T) {
	t.Parallel()

	e := echo.New()
	c := getTestController(t, e)
	headers := map[string]string{"X-Forwarded-For": connectionTestClient}
	statusOf := func() string {
		body := decodeConnectionCheck(t, getConnectionCheck(t, e, c, connectionTestPeer+":5000", headers))
		status, ok := body["status"].(string)
		require.True(t, ok, "status must be a string, got %v", body["status"])
		return status
	}

	assert.Equal(t, "untrusted_proxy", statusOf())

	listed := getTestSettings(t)
	listed.Security.TrustedProxies = []string{connectionTestPeer}
	c.Settings.Store(listed)
	assert.Equal(t, "trusted_proxy", statusOf(), "listing the peer takes effect on the next request")

	unlisted := getTestSettings(t)
	c.Settings.Store(unlisted)
	assert.Equal(t, "untrusted_proxy", statusOf(), "removing the peer takes effect on the next request")
}

// TestGetConnectionCheck_NoSettings verifies the handler answers with an empty
// list instead of failing when the controller has no settings snapshot.
func TestGetConnectionCheck_NoSettings(t *testing.T) {
	t.Parallel()

	e := echo.New()
	c := getTestController(t, e)
	c.Settings.Store((*conf.Settings)(nil))
	rec := getConnectionCheck(t, e, c, "192.168.1.20:5000", nil)
	body := decodeConnectionCheck(t, rec)
	assert.Equal(t, "direct", body["status"])
}

// TestGetConnectionCheck_RequiresAuth verifies the route sits behind the
// settings group's authentication.
func TestGetConnectionCheck_RequiresAuth(t *testing.T) {
	e := newSettingsAuthTestEnv(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/settings/connection", http.NoBody)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code, "the connection check must be auth-protected")
}
