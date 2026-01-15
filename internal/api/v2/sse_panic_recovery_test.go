// sse_panic_recovery_test.go: Tests for panic recovery in SSE message marshaling
// This test suite prevents regression of issue #1409 where concurrent map access
// during JSON marshaling caused panics in SSE notification streams.

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestSendSSEMessagePanicRecovery verifies that sendSSEMessage handles
// unmarshalable data gracefully without panicking.
func TestSendSSEMessagePanicRecovery(t *testing.T) {
	t.Parallel()

	c := &Controller{
		Settings: &conf.Settings{
			WebServer: conf.WebServerSettings{
				Debug: true,
			},
		},
		apiLogger: nil, // Will skip logging in tests
	}

	// Create echo context with response recorder
	e := echo.New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", http.NoBody)
	ctx := e.NewContext(req, rec)

	t.Run("channel value causes marshal error not panic", func(t *testing.T) {
		// Channels cannot be marshaled to JSON and would cause a panic
		// in unsafe code. Our recovery should catch this.
		badData := map[string]any{
			"channel": make(chan int),
			"normal":  "value",
		}

		// Should return error, not panic
		err := c.sendSSEMessage(ctx, "test", badData)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "marshal")
	})

	t.Run("function value causes marshal error not panic", func(t *testing.T) {
		// Functions cannot be marshaled to JSON
		badData := map[string]any{
			"func":   func() {},
			"normal": "value",
		}

		// Should return error, not panic
		err := c.sendSSEMessage(ctx, "test", badData)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "marshal")
	})

	t.Run("valid data marshals successfully", func(t *testing.T) {
		// Reset recorder for fresh response
		rec = httptest.NewRecorder()
		ctx = e.NewContext(req, rec)

		validData := map[string]any{
			"message": "test notification",
			"id":      "12345",
			"nested": map[string]any{
				"key": "value",
			},
		}

		// Should succeed without error
		err := c.sendSSEMessage(ctx, "notification", validData)
		require.NoError(t, err)

		// Verify SSE format in response
		body := rec.Body.String()
		assert.Contains(t, body, "event: notification")
		assert.Contains(t, body, "data:")
	})
}
