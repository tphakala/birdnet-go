// internal/api/pprof_live_test.go
package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProfilingCPUProfileOutlivesWriteTimeout serves the routes over a real
// listener whose http.Server carries a WriteTimeout shorter than the requested
// sampling duration. That is the shape production runs in: the web server's
// WriteTimeout is 30s and `go tool pprof` asks for 30 seconds by default, so
// without the deadline being extended every default CPU profile would be cut
// off mid-response.
//
// net/http/pprof extends that deadline itself (configureWriteDeadline), but it
// reaches the connection through http.ResponseController, which walks the
// writer chain via Unwrap, and it discards SetWriteDeadline's error. So any
// middleware that wraps the ResponseWriter without implementing Unwrap breaks
// long profiles silently, with no log line and no failing unit test. This is
// the guard on that, and it has to use a real listener: an httptest recorder
// has no deadline to miss.
//
// The durations are scaled down so the test costs a few seconds rather than
// thirty; the relationship that matters (requested > WriteTimeout) is the same.
func TestProfilingCPUProfileOutlivesWriteTimeout(t *testing.T) {
	const (
		serverWriteTimeout = 2 * time.Second
		profileSeconds     = 3
	)
	require.Greater(t, profileSeconds*time.Second, serverWriteTimeout,
		"the test is meaningless unless the profile outlasts the write timeout")

	s := newPprofTestServer(t, profilingSettings(true, testProfilingToken))
	// The real middleware stack, which is the entire point: the Unwrap chain
	// this test guards runs through whatever setupMiddleware installs, and a
	// bare echo.New() would prove only that echo.Response implements Unwrap.
	s.setupMiddleware()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := &http.Server{
		Handler:           s.echo,
		ReadHeaderTimeout: serverWriteTimeout,
		WriteTimeout:      serverWriteTimeout,
	}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Close() })

	url := "http://" + listener.Addr().String() + PprofBasePath + "/profile" +
		"?token=" + testProfilingToken +
		"&seconds=" + strconv.Itoa(profileSeconds)

	// A generous client timeout: the server side is what is under test.
	client := &http.Client{Timeout: time.Duration(profileSeconds)*time.Second + 30*time.Second}
	resp, err := client.Get(url) //nolint:noctx // bounded by the client timeout above
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "the profile must not be cut short by the write deadline")

	require.Equal(t, http.StatusOK, resp.StatusCode,
		"pprof refused the request; body: %s", string(body))
	assert.NotEmpty(t, body)

	// Decompress rather than checking the gzip magic bytes. Magic bytes are the
	// FIRST two, so a response truncated at 99% still has them: the assertion
	// would hold for exactly the failure it claims to detect. gzip.Reader
	// verifies the trailing CRC32 and length, so a short read fails here.
	zr, err := gzip.NewReader(bytes.NewReader(body))
	require.NoError(t, err, "response is not a gzip-framed pprof profile")
	defer func() { _ = zr.Close() }()

	raw, err := io.ReadAll(zr)
	require.NoError(t, err,
		"the profile was truncated: the write deadline cut the response short")
	assert.NotEmpty(t, raw, "a complete profile must decompress to a non-empty payload")
}

// TestProfilingLiveTokenRefusal confirms the refusal survives the full
// middleware stack over a real transport. The in-process tests drive Echo
// directly with no middleware, so this is the one that would notice a
// middleware ordering change letting a request past the gate.
func TestProfilingLiveTokenRefusal(t *testing.T) {
	s := newPprofTestServer(t, profilingSettings(true, testProfilingToken))
	s.setupMiddleware()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := &http.Server{Handler: s.echo, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Close() })

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("http://" + listener.Addr().String() + heapPath) //nolint:noctx // bounded by the client timeout above
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}
