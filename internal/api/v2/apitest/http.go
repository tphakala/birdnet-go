package apitest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/httpclient"
)

// TestResponseHeaderTimeout is the HTTP response-header timeout used by
// NewTestHTTPClient and DisableHTTPKeepAlivesForTesting.
const TestResponseHeaderTimeout = 30 * time.Second

// newLeakSafeTestTransport clones DefaultTransport (per golang/go#26013, to
// inherit proxy support and bounded dials) and disables keep-alives, connection
// pooling, and HTTP/2 so test HTTP clients leave no persistent-connection
// goroutines behind. The response-header timeout is caller-supplied.
func newLeakSafeTestTransport(responseHeaderTimeout time.Duration) *http.Transport {
	transport := httpclient.CloneDefaultTransport()
	transport.DisableKeepAlives = true // Prevents persistent connection goroutines
	transport.IdleConnTimeout = 1 * time.Second
	transport.MaxIdleConns = 0 // Disable connection pooling
	transport.MaxIdleConnsPerHost = 0
	transport.ForceAttemptHTTP2 = false // Disable HTTP/2 for simplicity in tests
	transport.ResponseHeaderTimeout = responseHeaderTimeout
	return transport
}

// NewTestHTTPClient creates an HTTP client optimized for tests to prevent
// goroutine leaks (see newLeakSafeTestTransport).
func NewTestHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: newLeakSafeTestTransport(timeout),
	}
}

// DisableHTTPKeepAlivesForTesting overrides the default HTTP client transport to
// prevent goroutine leaks from persistent connections in ALL HTTP clients during
// testing. Call it once from a package's TestMain before any test creates a client.
func DisableHTTPKeepAlivesForTesting() {
	http.DefaultTransport = newLeakSafeTestTransport(TestResponseHeaderTimeout)
}

// AssertControllerError asserts a handler error response. It handles both cases:
// when the handler returned an echo.HTTPError directly, and when the handler
// already sent an HTTP error response to the recorder.
func AssertControllerError(t *testing.T, err error, rec *httptest.ResponseRecorder, expectedCode int, expectedMessage string) {
	t.Helper()

	if err == nil {
		// Handler handled the error and sent an HTTP response.
		assert.Equal(t, expectedCode, rec.Code, "Expected HTTP status code")

		var response map[string]any
		jsonErr := json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, jsonErr, "Response should be valid JSON")

		// Check that the error response contains the expected message (if specified).
		if expectedMessage != "" {
			if message, exists := response["message"]; exists {
				assert.Contains(t, message, expectedMessage, "Error message should contain expected text")
			}
		}
		return
	}

	// Handler returned an error directly.
	var httpErr *echo.HTTPError
	require.ErrorAs(t, err, &httpErr, "Error should be an echo.HTTPError")
	assert.Equal(t, expectedCode, httpErr.Code, "Error should have expected HTTP code")
	if expectedMessage != "" {
		assert.Contains(t, httpErr.Message, expectedMessage, "Error message should contain expected text")
	}
}
