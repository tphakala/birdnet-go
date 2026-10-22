// sse_connection_test.go: Tests for SSE connection lifecycle and goroutine cleanup
// This test suite prevents regression of the critical memory leak issues fixed in PR #1163
//
// Go 1.25 improvements:
// - Uses sync.WaitGroup.Go() for cleaner concurrent goroutine management
// - Uses T.Attr() for enhanced test metadata
// - Uses T.Output() for structured test logging
//
// LLM GUIDANCE: When updating concurrent tests in this file:
// 1. Use sync.WaitGroup.Go() instead of manual wg.Add(1) + go func + defer wg.Done()
// 2. Add T.Attr() metadata for test categorization
// 3. Use T.Output() for structured logging instead of t.Logf()
// 4. For benchmark loops, use b.Loop() instead of manual for i := 0; i < b.N; i++
// 5. Avoid testing/synctest.Test() if code creates background goroutines with time.Sleep

package sse

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
)

// SSETestConfig holds configuration for SSE connection tests
type SSETestConfig struct {
	endpoint       string
	maxConnections int
	testTimeout    time.Duration
}

// maxSSEReadLines is the number of lines attemptSSEConnection reads from an SSE
// stream before returning. The initial connected event is exactly this many
// lines ("event: ...", "data: ...", and the trailing blank line from the
// "\n\n" terminator), so reading this many confirms the connection was
// established without blocking on the next event, which only arrives at the
// heartbeat interval.
const maxSSEReadLines = 3

// attemptSSEConnection attempts to establish an SSE connection and read events.
// Returns true if connection was established and events were read.
func attemptSSEConnection(t *testing.T, serverURL, endpoint string, connID int, timeout time.Duration) bool {
	t.Helper()
	client := apitest.NewTestHTTPClient(timeout)
	defer client.CloseIdleConnections()

	req, err := http.NewRequest("GET", serverURL+"/api/v2"+endpoint, http.NoBody)
	if err != nil {
		assert.NoErrorf(t, err, "Connection %d: Failed to create request", connID)
		return false
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(req)
	if err != nil {
		return false // May fail due to rate limiting
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	// Read the initial connected event's lines, then stop. The line-count check
	// must come before scanner.Scan() so that once we have read the connected
	// event (exactly maxSSEReadLines lines) we never issue another Scan. A Scan
	// after the connected event would block until the next heartbeat or the
	// client timeout, burning the full timeout per connection for no benefit.
	scanner := bufio.NewScanner(resp.Body)
	eventCount := 0
	for eventCount < maxSSEReadLines && scanner.Scan() {
		eventCount++
	}
	// Report success only if the full connected event was read. Returning true
	// unconditionally would count a connection that got HTTP 200 but never
	// delivered the handshake (e.g. the stream closed early) as established.
	return eventCount == maxSSEReadLines
}

// Common test configurations for different SSE endpoints
var sseTestConfigs = []SSETestConfig{
	// Note: /notifications/stream endpoint requires authentication and lives in
	// the notifications domain; it is tested there separately.
	{
		endpoint:       "/detections/stream",
		maxConnections: 3,
		testTimeout:    5 * time.Second,
	},
	{
		endpoint:       "/soundlevels/stream",
		maxConnections: 3,
		testTimeout:    5 * time.Second,
	},
}

// TestSSEConnectionCleanup is the main test that verifies SSE connections
// are properly cleaned up without goroutine leaks
func TestSSEConnectionCleanup(t *testing.T) {
	// Goroutine leak checking is now handled centrally in TestMain
	// This prevents this test from detecting goroutines from other tests
	t.Attr("component", "sse")
	t.Attr("type", "connection-lifecycle")

	for _, config := range sseTestConfigs {
		// Loop variable capture no longer needed in Go 1.22+
		t.Run(fmt.Sprintf("endpoint_%s", strings.ReplaceAll(config.endpoint, "/", "_")), func(t *testing.T) {
			t.Parallel()
			testSSEEndpointCleanup(t, config)
		})
	}
}

// testSSEEndpointCleanup tests a specific SSE endpoint for proper cleanup
func testSSEEndpointCleanup(t *testing.T, config SSETestConfig) {
	t.Helper()
	// Create test server
	server, _ := setupSSETestServer(t)

	t.Run("single_connection_manual_disconnect", func(t *testing.T) {
		testSingleConnectionManualDisconnect(t, server, config)
	})

	t.Run("single_connection_context_cancellation", func(t *testing.T) {
		testSingleConnectionContextCancellation(t, server, config)
	})

	t.Run("multiple_concurrent_connections", func(t *testing.T) {
		testMultipleConcurrentConnections(t, server, config)
	})
}

// testSingleConnectionManualDisconnect verifies that manually closing an SSE connection
// properly cleans up goroutines
func testSingleConnectionManualDisconnect(t *testing.T, server *httptest.Server, config SSETestConfig) {
	t.Helper()
	// Create HTTP client optimized for tests
	client := apitest.NewTestHTTPClient(config.testTimeout)

	// Make SSE request
	req, err := http.NewRequest("GET", server.URL+"/api/v2"+config.endpoint, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify SSE headers (allow charset suffix in Content-Type)
	contentType := resp.Header.Get("Content-Type")
	require.True(t, strings.HasPrefix(contentType, "text/event-stream"),
		"Content-Type should start with text/event-stream, got: %s", contentType)
	require.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))
	require.Equal(t, "no", resp.Header.Get("X-Accel-Buffering"),
		"SSE responses must set X-Accel-Buffering: no so reverse proxies stream events without buffering")

	// Read first event (connection established)
	scanner := bufio.NewScanner(resp.Body)
	connected := false
	start := time.Now()
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "connected") || strings.Contains(line, "Connected") {
			connected = true
			break
		}
		// Don't wait too long for connection event
		if time.Since(start) > time.Second {
			break
		}
	}
	require.True(t, connected, "Should receive connection event")

	// Close connection manually
	_ = resp.Body.Close() // Ignore close error in test

	// Close idle connections - should be immediate with DisableKeepAlives
	client.CloseIdleConnections()

	// Connection cleanup is immediate with DisableKeepAlives=true
	// goleak will catch any leaked goroutines
}

// testSingleConnectionContextCancellation verifies that canceling the context
// properly cleans up the SSE connection
func testSingleConnectionContextCancellation(t *testing.T, server *httptest.Server, config SSETestConfig) {
	t.Helper()
	// Create context with cancellation
	ctx, cancel := context.WithCancel(t.Context())

	// Create HTTP client optimized for tests
	client := apitest.NewTestHTTPClient(config.testTimeout)

	// Make SSE request with context
	req, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v2"+config.endpoint, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() {
		if resp != nil {
			_ = resp.Body.Close() // Ignore close error in test
		}
	}()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Start reading in goroutine
	done := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			// Just read events until context cancellation
		}
		done <- true
	}()

	// Wait a bit to establish connection
	// TODO: Consider using testing/synctest.Wait() for deterministic timing
	time.Sleep(100 * time.Millisecond)

	// Cancel context
	cancel()

	// Wait for goroutine to finish
	select {
	case <-done:
		// Expected - connection should close due to context cancellation
	case <-time.After(500 * time.Millisecond):
		assert.Fail(t, "Connection did not close within expected time after context cancellation")
	}

	// Response body will be closed by defer

	// Close idle connections - should be immediate with DisableKeepAlives
	client.CloseIdleConnections()

	// Cleanup is immediate with DisableKeepAlives=true
}

// testMultipleConcurrentConnections verifies that multiple concurrent SSE connections
// are all properly cleaned up
func testMultipleConcurrentConnections(t *testing.T, server *httptest.Server, config SSETestConfig) {
	t.Helper()
	var wg sync.WaitGroup
	var connectionsEstablished atomic.Int32

	for i := range config.maxConnections {
		connID := i
		wg.Go(func() {
			if attemptSSEConnection(t, server.URL, config.endpoint, connID, config.testTimeout) {
				connectionsEstablished.Add(1)
			}
		})
	}

	wg.Wait()
	require.Positive(t, int(connectionsEstablished.Load()),
		"At least one connection should have been established")
}

// sseRapidDisconnectTimeout bounds each request of TestSSERapidDisconnectReleasesClients;
// the wait for client removal uses twice this. The bounds are generous so a
// loaded host does not fail the test, and a passing run does not wait for them.
const sseRapidDisconnectTimeout = 15 * time.Second

// TestSSERapidDisconnectReleasesClients verifies that connecting to the
// detections stream and closing at once, repeatedly, leaves no client registered
// afterwards. A handler that does not remove its client when the connection ends
// would leave the count above zero.
func TestSSERapidDisconnectReleasesClients(t *testing.T) {
	// Goroutine leak checking is now handled centrally in TestMain
	t.Attr("component", "sse")

	server, core := setupSSETestServer(t)

	client := apitest.NewTestHTTPClient(sseRapidDisconnectTimeout)

	const rapidConnections = 5
	for range rapidConnections {
		req, err := http.NewRequest("GET", server.URL+"/api/v2/detections/stream", http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Accept", "text/event-stream")

		resp, err := client.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		// Immediately close to trigger the disconnect handler
		_ = resp.Body.Close() // Ignore close error in test
	}

	// Close idle connections - should be immediate with DisableKeepAlives
	client.CloseIdleConnections()

	require.Eventually(t, func() bool {
		return core.SSEManager.GetClientCount() == 0
	}, sseRapidDisconnectTimeout*2, 10*time.Millisecond, "disconnected clients should be removed from the manager")
}

// TestSSERateLimiting verifies that SSE rate limiting works correctly
// and doesn't cause goroutine leaks
func TestSSERateLimiting(t *testing.T) {
	// Goroutine leak checking is now handled centrally in TestMain
	t.Attr("component", "sse")
	t.Attr("feature", "rate-limiting")

	server, _ := setupSSETestServer(t)

	client := apitest.NewTestHTTPClient(2 * time.Second)

	var successCount, rateLimitedCount int

	// Make many rapid requests to trigger rate limiting
	for range 15 { // More than the rate limit of 10
		req, err := http.NewRequest("GET", server.URL+"/api/v2/detections/stream", http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Accept", "text/event-stream")

		resp, err := client.Do(req)
		if err != nil {
			continue
		}

		switch resp.StatusCode {
		case http.StatusOK:
			successCount++
		case http.StatusTooManyRequests:
			rateLimitedCount++
		}

		_ = resp.Body.Close() // Ignore close error in test
	}

	// Should have some successful connections and some rate limited
	require.Positive(t, successCount, "Should have some successful connections")
	require.Positive(t, rateLimitedCount, "Should have some rate limited connections")

	// Use T.Output() for structured logging (Go 1.25)
	output := t.Output()
	_, _ = fmt.Fprintf(output, "Rate limiting test results: %d successful, %d rate limited\n", successCount, rateLimitedCount)

	// Close idle connections - should be immediate with DisableKeepAlives
	client.CloseIdleConnections()

	// Cleanup is immediate with DisableKeepAlives=true
}

// setupSSETestServer creates a test server with the SSE endpoints registered.
// It builds an apicore.Core via apitest (mock datastore, per-test t.TempDir
// media path, in-memory Echo), wires only this domain's handler, and serves it
// over httptest. The core's lifecycle (SSE client teardown, context cancel, SFS
// close) is torn down by apitest.NewCore's own t.Cleanup; this helper adds a
// cleanup that closes lingering clients and the httptest server first (LIFO) so
// a blocked streaming handler is released before the server is closed. Settings
// are not published to the process-global snapshot (the SSE endpoints never read
// it) so the parallel endpoint subtests do not contend on the global.
func setupSSETestServer(t *testing.T) (*httptest.Server, *apicore.Core) {
	t.Helper()
	e := echo.New()
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithoutSettingsPublish())

	h := New(core, func(echo.Context) bool { return false })
	h.RegisterRoutes(core.Group)

	server := httptest.NewServer(e)
	t.Cleanup(func() {
		if core.SSEManager != nil {
			core.SSEManager.CloseAllClients()
		}
		server.Close()
	})

	return server, core
}
