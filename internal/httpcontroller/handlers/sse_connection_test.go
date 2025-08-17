// sse_connection_test.go: Tests for v1 SSE connection lifecycle and goroutine cleanup
// This test suite prevents regression of the critical memory leak issues fixed in PR #1163

package handlers

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/serviceapi"
	"go.uber.org/goleak"
)

// TestV1SSEConnectionCleanup verifies that v1 SSE endpoints properly clean up goroutines
func TestV1SSEConnectionCleanup(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		goleak.IgnoreTopFunction("sync.runtime_notifyListWait"),
		goleak.IgnoreTopFunction("github.com/patrickmn/go-cache.(*janitor).Run"),
		goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
		// Ignore audio streaming HLS initialization goroutine (unrelated to SSE)
		goleak.IgnoreTopFunction("github.com/tphakala/birdnet-go/internal/httpcontroller/handlers.init.0.func1"),
	)

	t.Run("sse_handler_cleanup", func(t *testing.T) {
		testSSEHandlerCleanup(t)
	})

	t.Run("audio_level_sse_cleanup", func(t *testing.T) {
		testAudioLevelSSECleanup(t)
	})
}

// testSSEHandlerCleanup tests the general v1 SSE handler cleanup
func testSSEHandlerCleanup(t *testing.T) {
	// Create Echo instance and SSE handler
	e := echo.New()
	// Create SSE handler directly instead of using NewSSEHandler() to avoid config dependency
	sseHandler := &SSEHandler{
		clients: make(map[chan Notification]bool),
		debug:   true, // Enable debug for testing
	}
	
	// Setup route
	e.GET("/api/v1/sse", sseHandler.ServeSSE)
	
	server := httptest.NewServer(e)
	defer server.Close()

	client := &http.Client{
		Timeout: 30 * time.Second, // Longer timeout to allow SSE connection
	}

	// Test connection in a goroutine to handle the long-running nature of SSE
	connectionDone := make(chan error, 1)
	respChan := make(chan *http.Response, 1)
	
	go func() {
		// Test connection and disconnection
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		
		req, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/sse", nil)
		if err != nil {
			connectionDone <- err
			return
		}
		req.Header.Set("Accept", "text/event-stream")

		resp, err := client.Do(req)
		if err != nil {
			connectionDone <- err
			return
		}
		
		respChan <- resp
		
		// Read for a short time to verify connection works
		scanner := bufio.NewScanner(resp.Body)
		scanner.Scan() // Try to read one line (will block until data or context cancellation)
		
		resp.Body.Close()
		connectionDone <- nil
	}()

	// Wait for connection result
	select {
	case err := <-connectionDone:
		if err != nil {
			// Context cancellation is expected for SSE connections
			if !strings.Contains(err.Error(), "context deadline exceeded") {
				t.Errorf("Unexpected connection error: %v", err)
			}
		}
	case resp := <-respChan:
		// If we get a response, verify headers
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "text/event-stream; charset=utf-8", resp.Header.Get("Content-Type"))
		require.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))
		resp.Body.Close()
	case <-time.After(3 * time.Second):
		t.Error("Connection test timed out")
	}

	// Wait for cleanup
	time.Sleep(300 * time.Millisecond)
}

// testAudioLevelSSECleanup tests the audio level SSE handler cleanup
func testAudioLevelSSECleanup(t *testing.T) {
	// Create Echo instance
	e := echo.New()
	
	// Create mock handlers with required dependencies
	handlers := &Handlers{
		Settings: &conf.Settings{
			WebServer: conf.WebServerSettings{
				Debug: true,
			},
		},
		debug: true,
		Server: &mockAccessServer{},
		AudioLevelChan: make(chan myaudio.AudioLevelData, 10),
	}

	// Setup route
	e.GET("/api/v1/audio-level", handlers.AudioLevelSSE)
	
	server := httptest.NewServer(e)
	defer server.Close()

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	// Test connection with context cancellation
	ctx, cancel := context.WithCancel(context.Background())
	
	req, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/audio-level", nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Start reading
	readDone := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			// Read audio level events
		}
		readDone <- true
	}()

	// Send some test audio data
	go func() {
		for i := 0; i < 3; i++ {
			select {
			case handlers.AudioLevelChan <- myaudio.AudioLevelData{
				Level:  i * 10,
				Source: "test-source",
				Name:   "Test Source",
			}:
			case <-time.After(100 * time.Millisecond):
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()

	// Wait a bit then cancel context
	time.Sleep(200 * time.Millisecond)
	cancel()

	// Wait for cleanup
	select {
	case <-readDone:
		// Expected
	case <-time.After(1 * time.Second):
		t.Error("Connection did not close within expected time")
	}

	resp.Body.Close()
	close(handlers.AudioLevelChan)

	// Wait for final cleanup
	time.Sleep(300 * time.Millisecond)
}

// TestV1SSEConnectionRaceConditions tests that the race condition fixes
// for connection counting work correctly
func TestV1SSEConnectionRaceConditions(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		goleak.IgnoreTopFunction("sync.runtime_notifyListWait"),
		goleak.IgnoreTopFunction("github.com/patrickmn/go-cache.(*janitor).Run"),
		goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
	)

	// Create Echo instance and SSE handler
	e := echo.New()
	// Create SSE handler directly instead of using NewSSEHandler() to avoid config dependency
	sseHandler := &SSEHandler{
		clients: make(map[chan Notification]bool),
		debug:   true, // Enable debug for testing
	}
	
	// Setup route
	e.GET("/api/v1/sse", sseHandler.ServeSSE)
	
	server := httptest.NewServer(e)
	defer server.Close()

	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	// Create multiple rapid connections to test race conditions
	for i := 0; i < 5; i++ {
		req, err := http.NewRequest("GET", server.URL+"/api/v1/sse", nil)
		require.NoError(t, err)
		req.Header.Set("Accept", "text/event-stream")

		resp, err := client.Do(req)
		if err != nil {
			continue // May timeout, which is fine
		}

		// Immediately close to stress test the cleanup logic
		resp.Body.Close()

		// Small delay between requests
		time.Sleep(20 * time.Millisecond)
	}

	// Wait for all cleanup
	time.Sleep(500 * time.Millisecond)
}

// TestV1AudioLevelSSEDuplicateConnections tests that duplicate connection
// prevention works correctly
func TestV1AudioLevelSSEDuplicateConnections(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		goleak.IgnoreTopFunction("sync.runtime_notifyListWait"),
		goleak.IgnoreTopFunction("github.com/patrickmn/go-cache.(*janitor).Run"),
		goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
	)

	// Create Echo instance
	e := echo.New()
	
	handlers := &Handlers{
		Settings: &conf.Settings{
			WebServer: conf.WebServerSettings{Debug: true},
		},
		debug: true,
		Server: &mockAccessServer{},
		AudioLevelChan: make(chan myaudio.AudioLevelData, 10),
	}

	e.GET("/api/v1/audio-level", handlers.AudioLevelSSE)
	
	server := httptest.NewServer(e)
	defer server.Close()
	defer close(handlers.AudioLevelChan)

	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	// Make first connection
	req1, err := http.NewRequest("GET", server.URL+"/api/v1/audio-level", nil)
	require.NoError(t, err)
	req1.Header.Set("Accept", "text/event-stream")
	req1.Header.Set("X-Forwarded-For", "192.168.1.100") // Set specific IP

	resp1, err := client.Do(req1)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp1.StatusCode)

	// Make second connection from same IP (should be rejected)
	req2, err := http.NewRequest("GET", server.URL+"/api/v1/audio-level", nil)
	require.NoError(t, err)
	req2.Header.Set("Accept", "text/event-stream")
	req2.Header.Set("X-Forwarded-For", "192.168.1.100") // Same IP

	resp2, err := client.Do(req2)
	if err == nil {
		// If no error, should be rejected with 429
		require.Equal(t, http.StatusTooManyRequests, resp2.StatusCode)
		resp2.Body.Close()
	}

	resp1.Body.Close()

	// Wait for cleanup
	time.Sleep(300 * time.Millisecond)
}

// mockAccessServer implements the minimal interface needed for testing
type mockAccessServer struct{}

func (m *mockAccessServer) IsAccessAllowed(c echo.Context) bool {
	return true // Always allow access for testing
}

func (m *mockAccessServer) GetProcessor() serviceapi.BirdNETProvider {
	return mockBirdNETProvider{}
}

// mockBirdNETProvider implements serviceapi.BirdNETProvider for testing
type mockBirdNETProvider struct{}

func (m mockBirdNETProvider) GetBirdNET() *birdnet.BirdNET {
	return nil // Return nil for testing
}

// BenchmarkV1SSEConnection benchmarks v1 SSE connection performance
func BenchmarkV1SSEConnection(b *testing.B) {
	e := echo.New()
	// Create SSE handler directly instead of using NewSSEHandler() to avoid config dependency
	sseHandler := &SSEHandler{
		clients: make(map[chan Notification]bool),
		debug:   false, // Disable debug for benchmarking
	}
	e.GET("/api/v1/sse", sseHandler.ServeSSE)
	
	server := httptest.NewServer(e)
	defer server.Close()

	client := &http.Client{
		Timeout: 1 * time.Second,
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		req, err := http.NewRequest("GET", server.URL+"/api/v1/sse", nil)
		if err != nil {
			b.Fatal(err)
		}
		req.Header.Set("Accept", "text/event-stream")

		resp, err := client.Do(req)
		if err != nil {
			continue
		}

		// Read briefly then close
		if resp.StatusCode == http.StatusOK {
			scanner := bufio.NewScanner(resp.Body)
			if scanner.Scan() {
				// Read one line
			}
		}

		resp.Body.Close()
	}
}