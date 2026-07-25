// internal/api/pprof_live_test.go
package api

import (
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
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

	settings := profilingSettings(true, testProfilingToken)

	previous := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(previous) })
	conftest.SetTestSettings(settings)

	s := &Server{
		echo:     echo.New(),
		config:   DefaultConfig(),
		settings: settings,
		slogger:  GetLogger(),
	}
	s.echo.HideBanner = true
	s.registerPprofRoutes()

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
	// A CPU profile is a gzip stream; the magic bytes confirm a complete,
	// well-formed response rather than a truncated or error body.
	require.GreaterOrEqual(t, len(body), 2)
	assert.Equal(t, []byte{0x1f, 0x8b}, body[:2],
		"expected a gzip-framed pprof profile")
}

// TestProfilingLiveTokenRefusal confirms the refusal path over a real listener
// too, so the gate is not accidentally bypassed by anything in the real serving
// stack that the in-process tests skip.
func TestProfilingLiveTokenRefusal(t *testing.T) {
	settings := profilingSettings(true, testProfilingToken)

	previous := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(previous) })
	conftest.SetTestSettings(settings)

	s := &Server{
		echo:     echo.New(),
		config:   DefaultConfig(),
		settings: settings,
		slogger:  GetLogger(),
	}
	s.echo.HideBanner = true
	s.registerPprofRoutes()

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
