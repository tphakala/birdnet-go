// Package notifications provides tests for the NTFY server connectivity check endpoint.
package notifications

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
)

// newNtfyCheckRequest builds a request for the check-ntfy-server endpoint with
// the given host in the JSON body. The endpoint is POST (not GET) so Echo's CSRF
// middleware protects the side-effecting probe; the handler reads the host via
// ctx.Bind, which requires the application/json content type.
func newNtfyCheckRequest(t *testing.T, host string) *http.Request {
	t.Helper()
	body, err := json.Marshal(map[string]string{"host": host})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/notifications/check-ntfy-server", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	return req
}

func TestCheckNtfyServer_HTTPSuccess(t *testing.T) {
	// Spin up a fake HTTP server that mimics the ntfy /v1/health response
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"healthy":true}`))
	}))
	defer ts.Close()

	e := echo.New()
	ctrl := New(&apicore.Core{}, nil, nil)
	ctrl.Settings.Store(apitest.NewValidTestSettings())
	// ts.Listener.Addr().String() returns "127.0.0.1:PORT"; the SSRF guard allows
	// loopback, so the guarded probe still reaches the test server.
	req := newNtfyCheckRequest(t, ts.Listener.Addr().String())
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
	ctrl := New(&apicore.Core{}, nil, nil)
	ctrl.Settings.Store(apitest.NewValidTestSettings())
	req := newNtfyCheckRequest(t, "")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := ctrl.CheckNtfyServer(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestCheckNtfyServer_ClosedPortUnreachable pins the dial-failure path: a host
// that refuses the connection is answered with 200 and "unreachable", not an
// error status. The port belongs to a listener that was just closed, so nothing
// leaves the machine.
func TestCheckNtfyServer_ClosedPortUnreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closedAddr := ln.Addr().String()
	require.NoError(t, ln.Close())

	e := echo.New()
	ctrl := New(&apicore.Core{}, nil, nil)
	ctrl.Settings.Store(apitest.NewValidTestSettings())
	ctrl.ntfyCheckTimeoutOverride = testFailFastTimeout
	req := newNtfyCheckRequest(t, closedAddr)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = ctrl.CheckNtfyServer(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp NtfyServerCheckResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "unreachable", resp.Recommended)
	assert.False(t, resp.HTTP)
	assert.False(t, resp.HTTPS)
}

func TestCheckNtfyServer_NonNtfyServerNotFalsePositive(t *testing.T) {
	// A plain HTTP server (e.g. nginx) that returns 200 with non-ntfy body
	// must NOT be reported as reachable ntfy.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<html><body>Welcome</body></html>`))
	}))
	defer ts.Close()

	e := echo.New()
	ctrl := New(&apicore.Core{}, nil, nil)
	ctrl.Settings.Store(apitest.NewValidTestSettings())
	req := newNtfyCheckRequest(t, ts.Listener.Addr().String())
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := ctrl.CheckNtfyServer(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "unreachable", resp["recommended"], "non-ntfy HTTP server should not be reported as reachable")
}

func TestCheckNtfyServer_InjectionRejected(t *testing.T) {
	e := echo.New()
	ctrl := New(&apicore.Core{}, nil, nil)
	ctrl.Settings.Store(apitest.NewValidTestSettings())
	// Slash/@ injection attempt: rejected by isValidNtfyHost as a malformed host.
	req := newNtfyCheckRequest(t, "evil.com/@good.com")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := ctrl.CheckNtfyServer(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestCheckNtfyServer_ProbesThroughGuardedClient checks that the ntfy
// connectivity check uses the guarded outbound client. The server listens on
// loopback, and the probe addresses it as 0.0.0.0:<port>. A plain HTTP client
// reaches the server through that address (the precondition below), while the
// guarded client refuses it at dial time, so the answer is "unreachable". If
// probeNtfyServer switched to a plain or default client the server would be
// found and this test would fail. Nothing outside the machine is contacted
// either way.
func TestCheckNtfyServer_ProbesThroughGuardedClient(t *testing.T) {
	// ntfyGuardPreconditionTimeout bounds the plain-client precondition probe and
	// the handler's own probe. Both are wide so a loaded host cannot turn a plain
	// client that is slow to answer into "unreachable".
	const ntfyGuardPreconditionTimeout = 10 * time.Second

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"healthy":true}`))
	}))
	t.Cleanup(ts.Close)

	_, port, err := net.SplitHostPort(ts.Listener.Addr().String())
	require.NoError(t, err)
	unspecifiedHost := net.JoinHostPort("0.0.0.0", port)

	// Precondition: a proxy-less plain client must reach the server through the
	// unspecified address, otherwise the assertion below proves nothing. A
	// transport without a proxy keeps an HTTP_PROXY environment from hijacking it.
	plain := &http.Client{
		Transport: &http.Transport{Proxy: nil},
		Timeout:   ntfyGuardPreconditionTimeout,
	}
	t.Cleanup(plain.CloseIdleConnections)
	preReq, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+unspecifiedHost+"/v1/health", http.NoBody)
	require.NoError(t, err)
	pre, err := plain.Do(preReq)
	if err != nil {
		t.Skipf("plain dial of %s failed, so the guarded-client check cannot be made: %v", unspecifiedHost, err)
	}
	require.NoError(t, pre.Body.Close())

	e := echo.New()
	ctrl := New(&apicore.Core{}, nil, nil)
	ctrl.Settings.Store(apitest.NewValidTestSettings())
	ctrl.ntfyCheckTimeoutOverride = ntfyGuardPreconditionTimeout
	req := newNtfyCheckRequest(t, unspecifiedHost)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = ctrl.CheckNtfyServer(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp NtfyServerCheckResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "unreachable", resp.Recommended)
	assert.False(t, resp.HTTP)
	assert.False(t, resp.HTTPS)
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
		// Metadata IPs are syntactically valid hosts; they are refused at dial time
		// by the SSRF-guarded client, not by this syntactic validator.
		{"169.254.169.254", true},
		{"[169.254.169.254]", true},
		{"fd00:ec2::254", true},
		{"[fd00:ec2::254]", true},
		{"192.168.1.100:0", false},            // port 0 out of range
		{"192.168.1.100:99999", false},        // port > 65535
		{"192.168.1.100:-1", false},           // negative port
		{"192.168.1.100:notaport", false},     // non-numeric port
		{"http://ntfy.sh", false},             // scheme not allowed
		{"https://192.168.1.100:8080", false}, // scheme not allowed
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.valid, isValidNtfyHost(tt.host), "host: %q", tt.host)
		})
	}
}
