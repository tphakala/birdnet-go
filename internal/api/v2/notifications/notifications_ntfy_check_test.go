// Package notifications provides tests for the NTFY server connectivity check endpoint.
package notifications

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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

func TestCheckNtfyServer_InvalidHost_Unreachable(t *testing.T) {
	e := echo.New()
	ctrl := New(&apicore.Core{}, nil, nil)
	ctrl.Settings.Store(apitest.NewValidTestSettings())
	// Inject a short per-scheme probe timeout so the unreachable-host path returns
	// quickly. 192.0.2.1 never responds, so the result is deterministically
	// "unreachable" regardless of the timeout; the override only bounds the wait
	// (default would be ntfyServerCheckTimeout for HTTPS plus HTTP).
	ctrl.ntfyCheckTimeoutOverride = testFailFastTimeout
	// Use a reserved/invalid IP that will not respond (TEST-NET-1, RFC 5737). The
	// SSRF guard permits it (not link-local/metadata), so the request is dialed
	// and times out rather than being refused.
	req := newNtfyCheckRequest(t, "192.0.2.1")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := ctrl.CheckNtfyServer(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "unreachable", resp["recommended"])
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

func TestCheckNtfyServer_CloudMetadataBlocked(t *testing.T) {
	e := echo.New()
	ctrl := New(&apicore.Core{}, nil, nil)
	ctrl.Settings.Store(apitest.NewValidTestSettings())
	// A metadata IP literal is now syntactically valid, so it reaches the probe.
	// This asserts the handler returns 200 + "unreachable" for it (no 400/500, no
	// panic, no relayed response). Note this alone does not prove the guard is what
	// refused it: a plain dial failure also yields "unreachable". The guard's actual
	// block is proven in httpclient/ssrf_test.go
	// (TestNewGuardedHTTPClient_BlocksMetadataLiteral) and TestIsBlockedTargetIP.
	ctrl.ntfyCheckTimeoutOverride = testFailFastTimeout
	req := newNtfyCheckRequest(t, "169.254.169.254")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := ctrl.CheckNtfyServer(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "unreachable", resp["recommended"], "cloud metadata IP must be refused by the SSRF guard")
}

func TestCheckNtfyServer_IPv6MetadataBlocked(t *testing.T) {
	e := echo.New()
	ctrl := New(&apicore.Core{}, nil, nil)
	ctrl.Settings.Store(apitest.NewValidTestSettings())
	// AWS IMDS over IPv6 (fd00:ec2::254) sits in an otherwise-allowed ULA range.
	// Same caveat as TestCheckNtfyServer_CloudMetadataBlocked: this asserts the
	// handler safely returns 200 + "unreachable" for it; the guard's actual block
	// is proven in httpclient/ssrf_test.go and TestIsBlockedTargetIP.
	ctrl.ntfyCheckTimeoutOverride = testFailFastTimeout
	req := newNtfyCheckRequest(t, "fd00:ec2::254")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := ctrl.CheckNtfyServer(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "unreachable", resp["recommended"], "IPv6 cloud metadata IP must be refused by the SSRF guard")
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
