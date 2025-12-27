package ebird

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// mockResponse represents a mocked HTTP response
type mockResponse struct {
	status      int
	body        string
	contentType string
}

// setupTestClient creates a test client with the given server
func setupTestClient(tb testing.TB, server *httptest.Server) *Client {
	tb.Helper()

	config := Config{
		APIKey:      "test-key",
		BaseURL:     server.URL,
		Timeout:     5 * time.Second,
		CacheTTL:    1 * time.Hour,
		RateLimitMS: 10, // Fast for tests
	}

	client, err := NewClient(config)
	require.NoError(tb, err)

	// Only use Cleanup for *testing.T
	if tt, ok := tb.(*testing.T); ok {
		tt.Cleanup(func() {
			client.Close()
		})
	}

	return client
}

// setupMockServer creates a mock server with predefined responses
func setupMockServer(tb testing.TB, responses map[string]mockResponse) *httptest.Server {
	tb.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check API key
		if apiKey := r.Header.Get("X-eBirdApiToken"); apiKey == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"title": "Unauthorized", "status": 401, "detail": "Missing API key"}`))
			return
		}

		// Find matching response
		key := r.URL.Path
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}

		if response, ok := responses[key]; ok {
			if response.contentType != "" {
				w.Header().Set("Content-Type", response.contentType)
			} else {
				w.Header().Set("Content-Type", "application/json")
			}
			w.WriteHeader(response.status)
			_, _ = w.Write([]byte(response.body))
			return
		}

		// Default 404
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"title": "Not Found", "status": 404, "detail": "Endpoint not found"}`))
	}))

	return server
}

// loadTestData loads test data from testdata directory
func loadTestData(tb testing.TB, filename string) string {
	tb.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", filename)) //nolint:gosec // G304: test fixture path
	require.NoError(tb, err)

	return string(data)
}

// disableLogging is a no-op with the new centralized logger
// Note: The new logger system uses a global logger that cannot be easily swapped in tests.
// Tests that were checking log output should be refactored to test behavior instead of logging.
func disableLogging(_ testing.TB) {
	// No-op: centralized logger doesn't support test-time replacement
}