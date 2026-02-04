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

package api

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
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// SSETestConfig holds configuration for SSE connection tests
type SSETestConfig struct {
	endpoint          string
	maxConnections    int
	testTimeout       time.Duration
	connectionTimeout time.Duration
}

// attemptSSEConnection attempts to establish an SSE connection and read events.
// Returns true if connection was established and events were read.
func attemptSSEConnection(t *testing.T, serverURL, endpoint string, connID int, timeout time.Duration) bool {
	t.Helper()
	client := createTestHTTPClient(timeout)
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

	// Read a few events
	scanner := bufio.NewScanner(resp.Body)
	eventCount := 0
	for scanner.Scan() && eventCount < 3 {
		eventCount++
	}
	return true
}

// Common test configurations for different SSE endpoints
var sseTestConfigs = []SSETestConfig{
	// Note: /notifications/stream endpoint requires authentication and is tested separately
	{
		endpoint:          "/detections/stream",
		maxConnections:    3,
		testTimeout:       5 * time.Second,
		connectionTimeout: 1 * time.Second,
	},
	{
		endpoint:          "/soundlevels/stream",
		maxConnections:    3,
		testTimeout:       5 * time.Second,
		connectionTimeout: 1 * time.Second,
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
	server, controller := setupSSETestServer(t)
	t.Cleanup(func() {
		controller.Shutdown()
		server.Close()
	})

	t.Run("single_connection_manual_disconnect", func(t *testing.T) {
		testSingleConnectionManualDisconnect(t, server, config)
	})

	t.Run("single_connection_context_cancellation", func(t *testing.T) {
		testSingleConnectionContextCancellation(t, server, config)
	})

	t.Run("multiple_concurrent_connections", func(t *testing.T) {
		testMultipleConcurrentConnections(t, server, config)
	})

	t.Run("connection_timeout", func(t *testing.T) {
		testConnectionTimeout(t, server, config)
	})
}

// testSingleConnectionManualDisconnect verifies that manually closing an SSE connection
// properly cleans up goroutines
func testSingleConnectionManualDisconnect(t *testing.T, server *httptest.Server, config SSETestConfig) {
	t.Helper()
	// Create HTTP client optimized for tests
	client := createTestHTTPClient(config.testTimeout)

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
	client := createTestHTTPClient(config.testTimeout)

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
	var connectionsEstablished int32

	for i := range config.maxConnections {
		connID := i
		wg.Go(func() {
			if attemptSSEConnection(t, server.URL, config.endpoint, connID, config.testTimeout) {
				atomic.AddInt32(&connectionsEstablished, 1)
			}
		})
	}

	wg.Wait()
	require.Positive(t, int(atomic.LoadInt32(&connectionsEstablished)),
		"At least one connection should have been established")
}

// testConnectionTimeout verifies that connections are properly cleaned up
// when they reach the maximum duration timeout
func testConnectionTimeout(t *testing.T, server *httptest.Server, config SSETestConfig) {
	t.Helper()
	// This test would require modifying the timeout constants for testing
	// For now, we'll test the behavior with a short-lived connection

	client := createTestHTTPClient(config.connectionTimeout)

	req, err := http.NewRequest("GET", server.URL+"/api/v2"+config.endpoint, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(req)
	if err != nil {
		// Timeout is expected behavior
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// Read until timeout
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		// Read until client timeout occurs
	}

	// Close idle connections - should be immediate with DisableKeepAlives
	client.CloseIdleConnections()

	// Connection cleanup is immediate with DisableKeepAlives=true
}

// TestSSEUnbufferedChannelFix specifically tests that the critical unbuffered channel
// deadlock issue has been fixed
func TestSSEUnbufferedChannelFix(t *testing.T) {
	// Goroutine leak checking is now handled centrally in TestMain
	t.Attr("component", "sse")
	t.Attr("issue", "unbuffered-channel-fix")

	// Test the detections endpoint to verify the critical unbuffered channel fix
	server, controller := setupSSETestServer(t)
	defer server.Close()
	defer controller.Shutdown()

	client := createTestHTTPClient(3 * time.Second)

	// Create multiple rapid connections and disconnections to stress test
	// the Done channel handling
	for range 5 {
		req, err := http.NewRequest("GET", server.URL+"/api/v2/detections/stream", http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Accept", "text/event-stream")

		resp, err := client.Do(req)
		if err != nil {
			continue // May fail due to rate limiting
		}

		// Immediately close to trigger the disconnect handler
		_ = resp.Body.Close() // Ignore close error in test

		// No artificial delay - test rapid connections/disconnections
	}

	// Close idle connections - should be immediate with DisableKeepAlives
	client.CloseIdleConnections()

	// If the test reaches here without hanging, the unbuffered channel issue is fixed
	// Cleanup is immediate with DisableKeepAlives=true
}

// TestSSERateLimiting verifies that SSE rate limiting works correctly
// and doesn't cause goroutine leaks
func TestSSERateLimiting(t *testing.T) {
	// Goroutine leak checking is now handled centrally in TestMain
	t.Attr("component", "sse")
	t.Attr("feature", "rate-limiting")

	server, controller := setupSSETestServer(t)
	defer server.Close()
	defer controller.Shutdown()

	client := createTestHTTPClient(2 * time.Second)

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

// setupSSETestServer creates a test server with SSE endpoints configured
func setupSSETestServer(t *testing.T) (*httptest.Server, *Controller) {
	t.Helper()
	// Create Echo instance
	e := echo.New()

	// Create mock datastore
	mockDS := mocks.NewMockInterface(t)

	// Create settings with required paths
	settings := &conf.Settings{
		WebServer: conf.WebServerSettings{
			Debug: true,
		},
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				Export: conf.ExportSettings{
					Path: t.TempDir(),
				},
			},
		},
	}

	// Create control channel
	controlChan := make(chan string, 10)

	// Create mock metrics
	mockMetrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to initialize metrics")

	// Create controller WITH route initialization
	controller, err := NewWithOptions(e, mockDS, settings, nil, nil, controlChan, mockMetrics, true)
	require.NoError(t, err)

	// Wait for goroutines to start
	if controller.goroutinesStarted != nil {
		select {
		case <-controller.goroutinesStarted:
			// Controller is ready
		case <-time.After(2 * time.Second):
			require.Fail(t, "Controller failed to start within timeout")
		}
	}

	// Create test server
	server := httptest.NewServer(e)

	return server, controller
}

// Benchmark for SSE connection performance
// Uses Go 1.25's b.Loop() for iteration
func BenchmarkSSEConnectionSetup(b *testing.B) {
	server, controller := setupSSETestServerForBench(b)
	defer server.Close()
	defer controller.Shutdown()

	client := createTestHTTPClient(5 * time.Second)
	defer client.CloseIdleConnections()

	b.ReportAllocs()
	b.ResetTimer()

	// Use b.Loop() for benchmark iteration (Go 1.25)
	for b.Loop() {
		req, err := http.NewRequest("GET", server.URL+"/api/v2/notifications/stream", http.NoBody)
		if err != nil {
			b.Fatal(err)
		}
		req.Header.Set("Accept", "text/event-stream")

		resp, err := client.Do(req)
		if err != nil {
			continue // Rate limiting may occur
		}

		if resp.StatusCode == http.StatusOK {
			// Read one event then close
			scanner := bufio.NewScanner(resp.Body)
			if scanner.Scan() {
				// Read and discard one event for testing
				_ = scanner.Text()
			}
		}

		_ = resp.Body.Close() // Ignore close error in test
	}
}

// setupSSETestServerForBench creates a test server for benchmarking
func setupSSETestServerForBench(b *testing.B) (*httptest.Server, *Controller) {
	b.Helper()
	e := echo.New()
	mockDS := mocks.NewMockInterface(b)
	settings := &conf.Settings{
		WebServer: conf.WebServerSettings{Debug: false}, // Disable debug for benchmarking
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				Export: conf.ExportSettings{Path: b.TempDir()},
			},
		},
	}
	controlChan := make(chan string, 10)
	mockMetrics, _ := observability.NewMetrics()

	controller, err := NewWithOptions(e, mockDS, settings, nil, nil, controlChan, mockMetrics, true)
	if err != nil {
		b.Fatal(err)
	}

	if controller.goroutinesStarted != nil {
		select {
		case <-controller.goroutinesStarted:
			// Controller is ready
		case <-time.After(2 * time.Second):
			b.Fatal("Controller failed to start within timeout")
		}
	}

	return httptest.NewServer(e), controller
}
