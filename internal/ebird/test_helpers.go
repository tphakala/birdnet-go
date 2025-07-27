package ebird

import (
	"bytes"
	"io"
	"log/slog"
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

	data, err := os.ReadFile(filepath.Join("testdata", filename))
	require.NoError(tb, err)

	return string(data)
}

// captureTestLogs captures logs during test execution
func captureTestLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	var logBuf bytes.Buffer
	testLogger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Save current logger
	oldLogger := logger
	logger = testLogger.With("service", "ebird")

	t.Cleanup(func() {
		logger = oldLogger
	})

	return &logBuf
}

// assertLogContains checks if log buffer contains expected string
func assertLogContains(t *testing.T, logs *bytes.Buffer, expected string) {
	t.Helper()
	if !bytes.Contains(logs.Bytes(), []byte(expected)) {
		t.Errorf("Log should contain: %s\nActual logs: %s", expected, logs.String())
	}
}

// disableLogging disables logging for tests that don't need it
func disableLogging(tb testing.TB) {
	tb.Helper()

	oldLogger := logger
	logger = slog.New(slog.NewJSONHandler(io.Discard, nil))

	// Only use Cleanup for *testing.T
	if tt, ok := tb.(*testing.T); ok {
		tt.Cleanup(func() {
			logger = oldLogger
		})
	}
}