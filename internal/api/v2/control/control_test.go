// control_test.go: tests for the API v2 control domain endpoints.

package control

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/restart"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// testControlChannelBuf is the control channel buffer size for the handlers
// built by the test helpers.
const testControlChannelBuf = 10

// newControlTestHandler builds a control Handler around an apicore.Core (via
// apitest) and a buffered control channel. It returns the Echo instance, the
// handler, and the bidirectional channel so tests can read the signals the
// handler sends (the handler holds only the send-only view).
func newControlTestHandler(t *testing.T) (*echo.Echo, *Handler, chan string) {
	t.Helper()
	e := echo.New()
	ch := make(chan string, testControlChannelBuf)
	core := apitest.NewCore(t, apitest.WithEcho(e))
	return e, New(core, ch), ch
}

// newControlHandler builds a control Handler on the supplied Echo instance with a
// buffered control channel the caller does not need to observe. It is for
// parallel tests, so it does not publish settings (a process-global); the
// restart-source handler reads none.
func newControlHandler(t *testing.T, e *echo.Echo) *Handler {
	t.Helper()
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithoutSettingsPublish())
	return New(core, make(chan string, testControlChannelBuf))
}

// runControlEndpointTest runs a control endpoint test with the given parameters.
// signals are read from the supplied bidirectional channel.
func runControlEndpointTest(t *testing.T, e *echo.Echo, controlChan chan string, method, path string, handler func(echo.Context) error, expectedMessage, expectedAction, expectedSignal string) {
	t.Helper()

	// The signal handlers take no input, so a body that is not valid JSON must
	// not change the outcome. A non-empty body with a JSON content type is
	// what makes a handler that binds its body fail.
	req := httptest.NewRequest(method, path, strings.NewReader("{invalid-json"))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath(path)

	// Test
	require.NoError(t, handler(c))
	{
		// Check status code
		assert.Equal(t, http.StatusOK, rec.Code)

		// Check content type
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

		// Parse response body
		var result ControlResult
		err := json.Unmarshal(rec.Body.Bytes(), &result)
		require.NoError(t, err)

		// Pin the wire key names, which decoding into ControlResult cannot do.
		var raw map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
		assert.ElementsMatch(t, []string{"success", "message", "action", "timestamp"}, slices.Collect(maps.Keys(raw)))

		// Check response content
		assert.True(t, result.Success)
		assert.Equal(t, expectedMessage, result.Message)
		assert.Equal(t, expectedAction, result.Action)
		assert.NotZero(t, result.Timestamp)

		// The send completes before the handler returns, so the signal is
		// already buffered and no wait is needed.
		require.Len(t, controlChan, 1, "Control signal was not sent")
		assert.Equal(t, expectedSignal, <-controlChan)
	}
}

// TestGetAvailableActions tests the GetAvailableActions endpoint with the
// container check replaced, so the restart_container action is checked both
// offered and withheld on any host. Each element must carry exactly the
// "action" and "description" keys (the ControlAction JSON tags).
func TestGetAvailableActions(t *testing.T) {
	baseActions := []string{
		ActionRestartAnalysis,
		ActionReloadModel,
		ActionRebuildFilter,
		ActionRestartServer,
		ActionRestartAudioSource,
	}

	tests := []struct {
		name        string
		inContainer bool
		want        []string
	}{
		{name: "outside a container", inContainer: false, want: baseActions},
		{name: "inside a container", inContainer: true, want: append(slices.Clone(baseActions), ActionRestartContainer)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, h, _ := newControlTestHandler(t)
			h.isContainer = func() bool { return tt.inContainer }

			req := httptest.NewRequest(http.MethodGet, "/api/v2/control/actions", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath("/api/v2/control/actions")

			require.NoError(t, h.GetAvailableActions(c))
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

			var actions []map[string]string
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &actions))

			ids := make([]string, 0, len(actions))
			for _, action := range actions {
				assert.ElementsMatch(t, []string{"action", "description"}, slices.Collect(maps.Keys(action)))
				assert.NotEmpty(t, action["description"])
				ids = append(ids, action["action"])
			}
			assert.ElementsMatch(t, tt.want, ids)
		})
	}
}

// TestNewWiresContainerCheck tests that New installs sysinfo.IsContainer itself.
// Every other test replaces the field, so without this a New that left it nil
// would pass them all and panic in production. The functions are compared by
// identity because a constant check returns the same value as the real one on
// any host that is not in a container.
func TestNewWiresContainerCheck(t *testing.T) {
	_, h, _ := newControlTestHandler(t)

	require.NotNil(t, h.isContainer)
	assert.Equal(t, reflect.ValueOf(sysinfo.IsContainer).Pointer(), reflect.ValueOf(h.isContainer).Pointer())
}

// testControlSendTimeout is the request deadline for the blocked-channel test.
// It elapses in synctest virtual time, so it costs no wall-clock time.
const testControlSendTimeout = time.Second

// TestControlSignalBlocked tests the two exits of the control-signal send when
// nothing receives on the channel: the request deadline answers 408 and server
// shutdown answers 503. Each runs inside a synctest bubble, with the core built
// there, so the select blocks durably and virtual time advances to the deadline.
// Only one of the two contexts is ended per row, because the select picks
// randomly when both are ready.
func TestControlSignalBlocked(t *testing.T) {
	t.Run("request deadline answers 408", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			e := echo.New()
			core := apitest.NewCore(t, apitest.WithEcho(e))
			h := New(core, make(chan string))

			ctx, cancel := context.WithTimeout(t.Context(), testControlSendTimeout)
			defer cancel()
			req := httptest.NewRequest(http.MethodPost, "/api/v2/control/restart", http.NoBody).WithContext(ctx)
			rec := httptest.NewRecorder()

			err := h.RestartAnalysis(e.NewContext(req, rec))
			apitest.AssertControllerError(t, err, rec, http.StatusRequestTimeout, "Request timeout while sending control signal")
		})
	})

	t.Run("shutdown answers 503", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			e := echo.New()
			core := apitest.NewCore(t, apitest.WithEcho(e))
			h := New(core, make(chan string))
			core.Cancel()

			req := httptest.NewRequest(http.MethodPost, "/api/v2/control/restart", http.NoBody)
			rec := httptest.NewRecorder()

			err := h.RestartAnalysis(e.NewContext(req, rec))
			apitest.AssertControllerError(t, err, rec, http.StatusServiceUnavailable, "Server is shutting down")
		})
	})
}

// TestRestartAnalysis tests the RestartAnalysis endpoint
func TestRestartAnalysis(t *testing.T) {
	// Setup
	e, h, ch := newControlTestHandler(t)

	runControlEndpointTest(t, e, ch, http.MethodPost, "/api/v2/control/restart", h.RestartAnalysis,
		"Analysis restart signal sent", ActionRestartAnalysis, SignalRestartAnalysis)
}

// TestReloadModel tests the ReloadModel endpoint
func TestReloadModel(t *testing.T) {
	// Setup
	e, h, ch := newControlTestHandler(t)

	runControlEndpointTest(t, e, ch, http.MethodPost, "/api/v2/control/reload", h.ReloadModel,
		"Model reload signal sent", ActionReloadModel, SignalReloadModel)
}

// TestRebuildFilter tests the RebuildFilter endpoint
func TestRebuildFilter(t *testing.T) {
	// Setup
	e, h, ch := newControlTestHandler(t)

	runControlEndpointTest(t, e, ch, http.MethodPost, "/api/v2/control/rebuild-filter", h.RebuildFilter,
		"Filter rebuild signal sent", ActionRebuildFilter, SignalRebuildFilter)
}

// TestControlActionsWithNilChannel tests the control endpoints with a nil control channel
func TestControlActionsWithNilChannel(t *testing.T) {
	// Setup
	e, h, _ := newControlTestHandler(t)

	// Explicitly set the control channel to nil
	h.controlChan = nil

	// Define test cases
	testCases := []struct {
		name     string
		endpoint string
		handler  func(echo.Context) error
	}{
		{
			name:     "RestartAnalysis with nil channel",
			endpoint: "/api/v2/control/restart",
			handler:  h.RestartAnalysis,
		},
		{
			name:     "ReloadModel with nil channel",
			endpoint: "/api/v2/control/reload",
			handler:  h.ReloadModel,
		},
		{
			name:     "RebuildFilter with nil channel",
			endpoint: "/api/v2/control/rebuild-filter",
			handler:  h.RebuildFilter,
		},
	}

	// Run tests for each control action
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create request
			req := httptest.NewRequest(http.MethodPost, tc.endpoint, http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath(tc.endpoint)

			// Call handler
			err := tc.handler(c)

			// Check that the error is properly handled by the controller
			require.NoError(t, err, "Handler should not return error even when control channel is nil")
			assert.Equal(t, http.StatusInternalServerError, rec.Code, "Should return 500 Internal Server Error")

			// Parse error response
			var errorResp map[string]any
			err = json.Unmarshal(rec.Body.Bytes(), &errorResp)
			require.NoError(t, err)

			// Check error response content - in non-debug mode, error field uses
			// sanitized message instead of raw err.Error()
			assert.Contains(t, fmt.Sprint(errorResp["error"]), "System control interface not available")
			assert.Contains(t, errorResp["message"], "System control interface not available")
			assert.Equal(t, http.StatusInternalServerError, int(errorResp["code"].(float64)))
		})
	}
}

// TestRegisterRoutesRegistration tests the registration of control-related API endpoints
func TestRegisterRoutesRegistration(t *testing.T) {
	// Build a handler whose apicore.Core exposes the /api/v2 group
	e, h, _ := newControlTestHandler(t)

	// Register the control routes on the shared group
	h.RegisterRoutes(h.Group)

	// Verify expected control routes are registered
	apitest.AssertRoutesRegistered(t, e, []string{
		"GET /api/v2/control/actions",
		"POST /api/v2/control/restart",
		"POST /api/v2/control/reload",
		"POST /api/v2/control/rebuild-filter",
		"POST /api/v2/control/restart-server",
		"POST /api/v2/control/restart-container",
	})
}

// TestControlActionsConstants verifies that the action constants are properly defined
func TestControlActionsConstants(t *testing.T) {
	// Verify action constants have expected values
	assert.Equal(t, "restart_analysis", ActionRestartAnalysis)
	assert.Equal(t, "reload_model", ActionReloadModel)
	assert.Equal(t, "rebuild_filter", ActionRebuildFilter)

	// Verify signal constants have expected values
	assert.Equal(t, "restart_analysis", SignalRestartAnalysis)
	assert.Equal(t, "reload_birdnet", SignalReloadModel)
	assert.Equal(t, "rebuild_range_filter", SignalRebuildFilter)

	// Verify constants relationship
	// The API should send different signals than the action names in some cases
	assert.Equal(t, ActionRestartAnalysis, SignalRestartAnalysis,
		"Restart analysis action should match signal")
	assert.NotEqual(t, ActionReloadModel, SignalReloadModel,
		"Reload model action should have a different signal name")
	assert.NotEqual(t, ActionRebuildFilter, SignalRebuildFilter,
		"Rebuild filter action should have a different signal name")
}

// testFlushDelay is the flush delay the restart tests expect. It is a literal
// rather than restartFlushDelay so that changing the production value fails
// the tests instead of moving the expectation with it.
const testFlushDelay = 500 * time.Millisecond

// mockShutdownRequester is a test double for ShutdownRequester. The restart
// goroutine writes called, so it is atomic.
type mockShutdownRequester struct {
	called atomic.Bool
}

// RequestShutdown records that shutdown was requested.
func (m *mockShutdownRequester) RequestShutdown() {
	m.called.Store(true)
}

// TestRestartServer tests the RestartServer endpoint with a valid shutdown
// requester: the binary restart type is recorded and shutdown is requested only
// after the flush delay, not before the response is written.
func TestRestartServer(t *testing.T) {
	t.Cleanup(restart.Reset)
	synctest.Test(t, func(t *testing.T) {
		e, h, _ := newControlTestHandler(t)

		mock := &mockShutdownRequester{}
		h.SetShutdownRequester(mock)

		req := httptest.NewRequest(http.MethodPost, "/api/v2/control/restart-server", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/control/restart-server")

		require.NoError(t, h.RestartServer(c))
		assert.Equal(t, http.StatusOK, rec.Code)

		var result ControlResult
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
		assert.True(t, result.Success)
		assert.Equal(t, ActionRestartServer, result.Action)
		assert.Equal(t, restart.RestartBinary, restart.Requested())

		time.Sleep(testFlushDelay - time.Nanosecond)
		synctest.Wait()
		assert.False(t, mock.called.Load(), "shutdown must not be requested before the flush delay has elapsed")

		time.Sleep(time.Nanosecond)
		synctest.Wait()
		assert.True(t, mock.called.Load(), "shutdown must be requested after the flush delay")
	})
}

// TestRestartServerAlreadyInProgress tests that a second restart request returns 409 Conflict.
func TestRestartServerAlreadyInProgress(t *testing.T) {
	t.Cleanup(restart.Reset)
	synctest.Test(t, func(t *testing.T) {
		e, h, _ := newControlTestHandler(t)
		mock := &mockShutdownRequester{}
		h.SetShutdownRequester(mock)

		// First call succeeds
		req := httptest.NewRequest(http.MethodPost, "/api/v2/control/restart-server", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		require.NoError(t, h.RestartServer(c))
		assert.Equal(t, http.StatusOK, rec.Code)

		// Second call returns 409
		req2 := httptest.NewRequest(http.MethodPost, "/api/v2/control/restart-server", http.NoBody)
		rec2 := httptest.NewRecorder()
		c2 := e.NewContext(req2, rec2)
		require.NoError(t, h.RestartServer(c2))
		assert.Equal(t, http.StatusConflict, rec2.Code)

		// Let the first request's delayed shutdown run, so no goroutine is left
		// sleeping when the bubble ends.
		time.Sleep(restartFlushDelay)
		synctest.Wait()
		assert.True(t, mock.called.Load())
	})
}

// TestRestartServerNoShutdownRequester tests that RestartServer returns 500 when no shutdown requester is set.
func TestRestartServerNoShutdownRequester(t *testing.T) {
	t.Cleanup(restart.Reset)
	e, h, _ := newControlTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/control/restart-server", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	require.NoError(t, h.RestartServer(c))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestRestartContainer tests RestartContainer with the container check replaced,
// so both outcomes run on any host: outside a container it answers 400 and
// requests nothing; inside one it answers 200, records the container restart
// type (not the binary one), and requests shutdown after the flush delay.
func TestRestartContainer(t *testing.T) {
	tests := []struct {
		name        string
		inContainer bool
	}{
		{name: "not in a container answers 400", inContainer: false},
		{name: "in a container answers 200", inContainer: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(restart.Reset)
			synctest.Test(t, func(t *testing.T) {
				e, h, _ := newControlTestHandler(t)
				h.isContainer = func() bool { return tt.inContainer }
				mock := &mockShutdownRequester{}
				h.SetShutdownRequester(mock)

				req := httptest.NewRequest(http.MethodPost, "/api/v2/control/restart-container", http.NoBody)
				rec := httptest.NewRecorder()
				c := e.NewContext(req, rec)
				require.NoError(t, h.RestartContainer(c))

				if !tt.inContainer {
					assert.Equal(t, http.StatusBadRequest, rec.Code)
					assert.Equal(t, restart.RestartNone, restart.Requested())
					synctest.Wait()
					assert.False(t, mock.called.Load())
					return
				}

				assert.Equal(t, http.StatusOK, rec.Code)
				var result ControlResult
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
				assert.True(t, result.Success)
				assert.Equal(t, ActionRestartContainer, result.Action)
				assert.Equal(t, restart.RestartContainer, restart.Requested())

				time.Sleep(testFlushDelay - time.Nanosecond)
				synctest.Wait()
				assert.False(t, mock.called.Load(), "shutdown must not be requested before the flush delay has elapsed")

				time.Sleep(time.Nanosecond)
				synctest.Wait()
				assert.True(t, mock.called.Load(), "shutdown must be requested after the flush delay")
			})
		})
	}
}
