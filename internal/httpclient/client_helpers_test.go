package httpclient

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestClient creates a Client with default configuration and registers cleanup.
func newTestClient(t *testing.T) *Client {
	t.Helper()
	cfg := DefaultConfig()
	client := New(&cfg)
	t.Cleanup(func() { client.Close() })
	return client
}

// newTestClientWithConfig creates a Client with custom configuration and registers cleanup.
func newTestClientWithConfig(t *testing.T, cfg *Config) *Client {
	t.Helper()
	client := New(cfg)
	t.Cleanup(func() { client.Close() })
	return client
}

// newTestServer creates a test HTTP server and registers cleanup.
func newTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(func() { server.Close() })
	return server
}

// closeResponseBody safely closes a response body with error logging.
// Use this with defer immediately after getting a response.
func closeResponseBody(t *testing.T, resp *http.Response) {
	t.Helper()
	if resp == nil || resp.Body == nil {
		return
	}
	if err := resp.Body.Close(); err != nil {
		t.Logf("failed to close response body: %v", err)
	}
}
