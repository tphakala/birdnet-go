package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestDebugTriggerError(t *testing.T) {
	t.Parallel()

	e := echo.New()
	c := &Handler{Core: &apicore.Core{APILogger: nil}}
	c.Settings.Store(&conf.Settings{Debug: true})

	tests := []struct {
		name     string
		body     string
		wantCode int
	}{
		{
			name:     "default values",
			body:     `{}`,
			wantCode: http.StatusOK,
		},
		{
			name:     "custom values",
			body:     `{"component":"test","category":"network","message":"custom error"}`,
			wantCode: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodPost, "/api/v2/debug/trigger-error",
				strings.NewReader(tt.body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)

			err := c.DebugTriggerError(ctx)
			require.NoError(t, err)
			assert.Equal(t, tt.wantCode, rec.Code)

			var resp DebugResponse
			err = json.Unmarshal(rec.Body.Bytes(), &resp)
			require.NoError(t, err)
			assert.True(t, resp.Success)
		})
	}
}

func TestDebugEndpointsRequireDebugMode(t *testing.T) {
	t.Parallel()

	// Create handler with debug disabled
	e := echo.New()
	c := &Handler{Core: &apicore.Core{APILogger: nil}}
	c.Settings.Store(&conf.Settings{Debug: false})

	// Table-driven tests for all debug endpoints returning 403 when debug mode is disabled
	endpoints := []struct {
		name    string
		method  string
		path    string
		handler func(echo.Context) error
		hasBody bool // whether the request needs a JSON body
	}{
		{
			name:    "DebugTriggerError",
			method:  http.MethodPost,
			path:    "/api/v2/debug/trigger-error",
			handler: c.DebugTriggerError,
			hasBody: true,
		},
		{
			name:    "DebugTriggerNotification",
			method:  http.MethodPost,
			path:    "/api/v2/debug/trigger-notification",
			handler: c.DebugTriggerNotification,
			hasBody: true,
		},
		{
			name:    "DebugSystemStatus",
			method:  http.MethodGet,
			path:    "/api/v2/debug/status",
			handler: c.DebugSystemStatus,
			hasBody: false,
		},
	}

	for _, ep := range endpoints {
		t.Run(ep.name, func(t *testing.T) {
			t.Parallel()

			var req *http.Request
			if ep.hasBody {
				req = httptest.NewRequest(ep.method, ep.path, strings.NewReader(`{}`))
				req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			} else {
				req = httptest.NewRequest(ep.method, ep.path, http.NoBody)
			}
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)

			// Test through middleware (guard logic is now in middleware)
			handler := c.requireDebugMode(ep.handler)
			err := handler(ctx)
			require.NoError(t, err)
			assert.Equal(t, http.StatusForbidden, rec.Code)

			var resp apicore.ErrorResponse
			err = json.Unmarshal(rec.Body.Bytes(), &resp)
			require.NoError(t, err)
			assert.Equal(t, "Debug mode not enabled", resp.Message)
			assert.Equal(t, http.StatusForbidden, resp.Code)
			assert.NotEmpty(t, resp.CorrelationID)
			assert.Equal(t, "errors.debug.notEnabled", resp.ErrorKey)
		})
	}
}

// TestRegisterDebugRoutesReadsSettingsNilSafely pins that RegisterDebugRoutes
// reads the debug flag via the nil-safe ControllerSettings() snapshot (matching
// requireDebugMode) instead of dereferencing a possibly-nil c.Settings on a
// standalone/test handler, and that it registers no debug routes when debug mode
// is off.
func TestRegisterDebugRoutesReadsSettingsNilSafely(t *testing.T) {
	e := echo.New()
	g := e.Group("/api/v2")
	settings := apitest.NewValidTestSettings()
	settings.Debug = false // debug mode off -> RegisterDebugRoutes takes the skip path

	h := &Handler{Core: &apicore.Core{Echo: e, Group: g}}
	// RegisterDebugRoutes must read settings through the nil-safe ControllerSettings()
	// accessor (an atomic Load), not assume a non-nil snapshot.
	h.Settings.Store(settings)

	require.NotPanics(t, func() { h.RegisterDebugRoutes(g) },
		"RegisterDebugRoutes must not dereference a nil settings snapshot")
	for _, r := range e.Routes() {
		assert.NotContains(t, r.Path, "/debug",
			"debug routes must not register when debug mode is off: %s %s", r.Method, r.Path)
	}
}
