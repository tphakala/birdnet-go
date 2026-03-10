// telemetry_test.go: Tests for API v2 Sentry telemetry integration.
// Verifies that HandleError() reports 5xx errors to telemetry and excludes 4xx errors.
package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// captureHook is a helper that installs an error hook for the duration of a test
// and returns the captured errors. Note: tests using this must NOT be run in
// parallel because error hooks are global state.
func captureHook(t *testing.T) *[]*errors.EnhancedError {
	t.Helper()
	captured := make([]*errors.EnhancedError, 0, 4)
	errors.AddErrorHook(func(ee *errors.EnhancedError) {
		captured = append(captured, ee)
	})
	t.Cleanup(errors.ClearErrorHooks)
	return &captured
}

// newTestContext creates an echo.Context for use in telemetry tests.
func newTestContext(t *testing.T, method, path string) (echo.Context, *httptest.ResponseRecorder) {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(method, path, http.NoBody)
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec), rec
}

// TestHandleError_5xxReportedToTelemetry verifies that a 5xx HandleError call
// publishes exactly one EnhancedError to the telemetry hook.
// Note: Not parallel — modifies global error-hook state.
func TestHandleError_5xxReportedToTelemetry(t *testing.T) {
	captured := captureHook(t)

	c := &Controller{Settings: getTestSettings(t)}
	ctx, _ := newTestContext(t, http.MethodGet, "/api/v2/detections")

	err := c.HandleError(ctx, fmt.Errorf("database failure"), "Internal server error", http.StatusInternalServerError)
	require.NoError(t, err)

	require.Len(t, *captured, 1, "expected exactly one telemetry event for 5xx error")
	ee := (*captured)[0]
	assert.Equal(t, "api", ee.GetComponent())
	assert.Equal(t, errors.CategoryHTTP, ee.Category)
	assert.Equal(t, http.StatusInternalServerError, ee.Context["http_status"])
	assert.Equal(t, "/api/v2/detections", ee.Context["endpoint"])
	assert.Equal(t, http.MethodGet, ee.Context["method"])
}

// TestHandleError_4xxNotReportedToTelemetry verifies that a 4xx HandleError call
// produces no telemetry events (4xx errors are client mistakes, not server bugs).
// Note: Not parallel — modifies global error-hook state.
func TestHandleError_4xxNotReportedToTelemetry(t *testing.T) {
	captured := captureHook(t)

	c := &Controller{Settings: getTestSettings(t)}
	ctx, _ := newTestContext(t, http.MethodPost, "/api/v2/settings")

	err := c.HandleError(ctx, fmt.Errorf("missing field"), "Bad request", http.StatusBadRequest)
	require.NoError(t, err)

	assert.Empty(t, *captured, "expected no telemetry events for 4xx error")
}

// TestHandleError_NilError_5xxReported verifies that a nil underlying error with a
// 5xx code still produces a telemetry event, using the message as the error text.
// Note: Not parallel — modifies global error-hook state.
func TestHandleError_NilError_5xxReported(t *testing.T) {
	captured := captureHook(t)

	c := &Controller{Settings: getTestSettings(t)}
	ctx, _ := newTestContext(t, http.MethodGet, "/api/v2/health")

	err := c.HandleError(ctx, nil, "Service unavailable", http.StatusServiceUnavailable)
	require.NoError(t, err)

	require.Len(t, *captured, 1, "expected one telemetry event even with nil error")
	ee := (*captured)[0]
	assert.Equal(t, "api", ee.GetComponent())
	assert.Equal(t, errors.CategoryHTTP, ee.Category)
	assert.Equal(t, http.StatusServiceUnavailable, ee.Context["http_status"])
}

// TestHandleErrorWithKey_5xxReportedToTelemetry verifies the i18n variant also
// reports 5xx errors to telemetry.
// Note: Not parallel — modifies global error-hook state.
func TestHandleErrorWithKey_5xxReportedToTelemetry(t *testing.T) {
	captured := captureHook(t)

	c := &Controller{Settings: getTestSettings(t)}
	ctx, _ := newTestContext(t, http.MethodDelete, "/api/v2/detections/123")

	err := c.HandleErrorWithKey(
		ctx,
		fmt.Errorf("constraint violation"),
		"Internal server error",
		http.StatusInternalServerError,
		"error.internal",
		nil,
	)
	require.NoError(t, err)

	require.Len(t, *captured, 1, "expected one telemetry event from HandleErrorWithKey")
	assert.Equal(t, "api", (*captured)[0].GetComponent())
}

// TestHandleError_AlreadyReportedSkipsDuplicate verifies that an error already
// reported by a lower layer (IsReported() == true) is not sent to Sentry again
// from the API layer.
// Note: Not parallel — modifies global error-hook state.
func TestHandleError_AlreadyReportedSkipsDuplicate(t *testing.T) {
	// Build and pre-report an EnhancedError to simulate a lower-layer report.
	preReported := errors.Newf("db deadlock").
		Component("datastore").
		Category(errors.CategoryDatabase).
		Build()
	preReported.MarkReported()

	// Now install the hook (after pre-reporting, so the Build() call above is not counted).
	captured := captureHook(t)

	c := &Controller{Settings: getTestSettings(t)}
	ctx, _ := newTestContext(t, http.MethodGet, "/api/v2/detections")

	err := c.HandleError(ctx, preReported, "Internal server error", http.StatusInternalServerError)
	require.NoError(t, err)

	assert.Empty(t, *captured, "expected no duplicate telemetry event for already-reported error")
}

// TestHandleError_404NotReportedToTelemetry verifies that 404 Not Found errors
// are not reported (they are expected conditions for unknown resources).
// Note: Not parallel — modifies global error-hook state.
func TestHandleError_404NotReportedToTelemetry(t *testing.T) {
	captured := captureHook(t)

	c := &Controller{Settings: getTestSettings(t)}
	ctx, _ := newTestContext(t, http.MethodGet, "/api/v2/detections/99999")

	err := c.HandleError(ctx, fmt.Errorf("record not found"), "Not found", http.StatusNotFound)
	require.NoError(t, err)

	assert.Empty(t, *captured, "expected no telemetry events for 404 error")
}

// TestHandleError_502BadGatewayReportedToTelemetry verifies that non-500 5xx codes
// (e.g., 502 Bad Gateway) are also reported as server errors.
// Note: Not parallel — modifies global error-hook state.
func TestHandleError_502BadGatewayReportedToTelemetry(t *testing.T) {
	captured := captureHook(t)

	c := &Controller{Settings: getTestSettings(t)}
	ctx, _ := newTestContext(t, http.MethodGet, "/api/v2/system")

	err := c.HandleError(ctx, fmt.Errorf("upstream timeout"), "Bad gateway", http.StatusBadGateway)
	require.NoError(t, err)

	require.Len(t, *captured, 1, "expected one telemetry event for 502 error")
	assert.Equal(t, http.StatusBadGateway, (*captured)[0].Context["http_status"])
}
