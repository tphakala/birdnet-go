// Package api provides tests for the NTFY server connectivity check endpoint.
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckNtfyServer_HTTPSuccess(t *testing.T) {
	// Spin up a fake HTTP server (simulates a reachable ntfy instance)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	e := echo.New()
	ctrl := &Controller{}
	// ts.Listener.Addr().String() returns "127.0.0.1:PORT"
	req := httptest.NewRequest(http.MethodGet, "/api/v2/notifications/check-ntfy-server?host="+ts.Listener.Addr().String(), http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := ctrl.CheckNtfyServer(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	// Should be reachable on http (https will fail since test server is plain HTTP)
	assert.Equal(t, "http", resp["recommended"])
	assert.Equal(t, true, resp["http"])
}

func TestCheckNtfyServer_MissingHost(t *testing.T) {
	e := echo.New()
	ctrl := &Controller{}
	req := httptest.NewRequest(http.MethodGet, "/api/v2/notifications/check-ntfy-server", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := ctrl.CheckNtfyServer(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCheckNtfyServer_InvalidHost_Unreachable(t *testing.T) {
	e := echo.New()
	ctrl := &Controller{}
	// Use a reserved/invalid IP that will not respond (TEST-NET-1, RFC 5737)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/notifications/check-ntfy-server?host=192.0.2.1", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := ctrl.CheckNtfyServer(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "unreachable", resp["recommended"])
}

func TestCheckNtfyServer_InjectionRejected(t *testing.T) {
	e := echo.New()
	ctrl := &Controller{}
	// Slash injection attempt
	req := httptest.NewRequest(http.MethodGet, "/api/v2/notifications/check-ntfy-server?host=evil.com%2F%40good.com", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := ctrl.CheckNtfyServer(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCheckNtfyServer_CloudMetadataBlocked(t *testing.T) {
	e := echo.New()
	ctrl := &Controller{}
	req := httptest.NewRequest(http.MethodGet, "/api/v2/notifications/check-ntfy-server?host=169.254.169.254", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := ctrl.CheckNtfyServer(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestIsValidNtfyHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		host  string
		valid bool
	}{
		{"ntfy.sh", true},
		{"myserver.local", true},
		{"192.168.1.100", true},
		{"192.168.1.100:8080", true},
		{"[::1]", true},
		{"[::1]:8080", true},
		{"", false},
		{"evil.com/path", false},
		{"evil.com@other.com", false},
		{"169.254.169.254", false}, // cloud metadata
		{"fd00:ec2::254", false},   // cloud metadata IPv6
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.valid, isValidNtfyHost(tt.host), "host: %q", tt.host)
		})
	}
}
