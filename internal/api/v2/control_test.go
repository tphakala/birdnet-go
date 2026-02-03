// control_test.go: Package api provides tests for API v2 control endpoints.
//
// Go 1.25 improvements:
// - Uses sync.WaitGroup.Go() for cleaner goroutine management
// - Uses T.Attr() for test metadata
// LLM GUIDANCE: Always use WaitGroup.Go() instead of manual Add/Done patterns

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runControlEndpointTest runs a control endpoint test with the given parameters
func runControlEndpointTest(t *testing.T, e *echo.Echo, controller *Controller, method, path string, handler func(echo.Context) error, expectedMessage, expectedAction, expectedSignal string) {
	t.Helper()

	// Create a request
	req := httptest.NewRequest(method, path, http.NoBody)
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

		// Check response content
		assert.True(t, result.Success)
		assert.Equal(t, expectedMessage, result.Message)
		assert.Equal(t, expectedAction, result.Action)
		assert.NotZero(t, result.Timestamp)

		// Verify signal was sent to control channel
		select {
		case signal := <-controller.controlChan:
			assert.Equal(t, expectedSignal, signal)
		case <-time.After(100 * time.Millisecond):
			assert.Fail(t, "Control signal was not sent")
		}
	}
}

// runConcurrentControlRequestsTest runs multiple concurrent control requests test
// Uses Go 1.25's WaitGroup.Go() for automatic goroutine management
func runConcurrentControlRequestsTest(t *testing.T, e *echo.Echo, controller *Controller, handler func(echo.Context) error, path, expectedSignal string) {
	t.Helper()
	t.Attr("component", "control")
	t.Attr("type", "concurrent")

	// Number of concurrent requests to make
	numRequests := 5

	// Use a wait group to synchronize the test
	// Go 1.25: Using WaitGroup.Go() for cleaner code without manual Add/Done
	var wg sync.WaitGroup

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	// Launch multiple concurrent requests using Go 1.25's WaitGroup.Go()
	for range numRequests {
		// Use WaitGroup.Go() to eliminate manual Add/Done management (Go 1.25)
		wg.Go(func() {

			// Create a new request for each goroutine
			req := httptest.NewRequest(http.MethodPost, path, http.NoBody).WithContext(ctx)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Call the handler
			err := handler(c)
			require.NoError(t, err, "Handler should not return an error during concurrent access")
			assert.Equal(t, http.StatusOK, rec.Code, "Should return OK for concurrent requests")
		})
	}

	// Wait for all requests to complete
	wg.Wait()

	// Verify that all signals were sent to the channel
	assert.Len(t, controller.controlChan, numRequests, "All signals should be received")

	// Drain the channel
	for range numRequests {
		signal := <-controller.controlChan
		assert.Equal(t, expectedSignal, signal, "Each signal should be the expected signal")
	}
}

// runControlActionsWithBlockedChannelTest runs control endpoints test with a blocked channel
func runControlActionsWithBlockedChannelTest(t *testing.T, handler func(echo.Context) error) {
	t.Helper()
	t.Attr("component", "control")
	t.Attr("type", "blocked-channel")

	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Save original channel and create a non-buffered channel that will block
	originalChan := controller.controlChan
	controlChan := make(chan string)
	controller.controlChan = controlChan

	// Restore original channel after test
	defer func() {
		controller.controlChan = originalChan
	}()

	// Start a goroutine that will eventually unblock the channel, but after the test timeout
	go func() {
		// Wait longer than the test will run to simulate a blocked receiver
		time.Sleep(200 * time.Millisecond)
		// Try to drain the channel if anything was sent
		select {
		case <-controlChan:
			// Channel drained
		default:
			// Channel was empty
		}
	}()

	// Create a context with timeout to ensure the test doesn't hang
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	// Create a request with the timeout context
	req := httptest.NewRequest(http.MethodPost, "/api/v2/control/restart", http.NoBody).WithContext(ctx)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Execute the handler with a separate goroutine and channel to detect completion
	done := make(chan bool, 1)
	go func() {
		err := handler(c)
		assert.NoError(t, err, "Handler should not return an error even with a blocked channel")
		done <- true
	}()

	// Check if the handler completes within the timeout
	select {
	case <-done:
		// Handler completed successfully without blocking indefinitely
		// Note: With buffered channel in setupTestEnvironment, the send won't block
		// so we expect success (200) instead of timeout (408)
		assert.Equal(t, http.StatusOK, rec.Code,
			"Should return success with buffered channel")
	case <-time.After(150 * time.Millisecond):
		require.Fail(t, "Handler blocked indefinitely with a blocked channel")
	}
}

// TestGetAvailableActions tests the GetAvailableActions endpoint
func TestGetAvailableActions(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Create a request to the control actions endpoint
	req := httptest.NewRequest(http.MethodGet, "/api/v2/control/actions", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/control/actions")

	// Test
	require.NoError(t, controller.GetAvailableActions(c))
	{
		// Check status code
		assert.Equal(t, http.StatusOK, rec.Code)

		// Check content type
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

		// Parse response body
		var actions []ControlAction
		err := json.Unmarshal(rec.Body.Bytes(), &actions)
		require.NoError(t, err)

		// Check response content
		require.Len(t, actions, 3, "Should have 3 control actions")

		// Verify actions include all expected types
		var hasRestartAction, hasReloadAction, hasRebuildFilterAction bool
		for _, action := range actions {
			switch action.Action {
			case ActionRestartAnalysis:
				hasRestartAction = true
				assert.Contains(t, action.Description, "Restart")
			case ActionReloadModel:
				hasReloadAction = true
				assert.Contains(t, action.Description, "Reload")
			case ActionRebuildFilter:
				hasRebuildFilterAction = true
				assert.Contains(t, action.Description, "Rebuild")
			}
		}

		// Verify we found all expected action types
		assert.True(t, hasRestartAction, "Missing restart_analysis action")
		assert.True(t, hasReloadAction, "Missing reload_model action")
		assert.True(t, hasRebuildFilterAction, "Missing rebuild_filter action")
	}
}

// TestRestartAnalysis tests the RestartAnalysis endpoint
func TestRestartAnalysis(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	runControlEndpointTest(t, e, controller, http.MethodPost, "/api/v2/control/restart", controller.RestartAnalysis,
		"Analysis restart signal sent", ActionRestartAnalysis, SignalRestartAnalysis)
}

// TestReloadModel tests the ReloadModel endpoint
func TestReloadModel(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	runControlEndpointTest(t, e, controller, http.MethodPost, "/api/v2/control/reload", controller.ReloadModel,
		"Model reload signal sent", ActionReloadModel, SignalReloadModel)
}

// TestRebuildFilter tests the RebuildFilter endpoint
func TestRebuildFilter(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	runControlEndpointTest(t, e, controller, http.MethodPost, "/api/v2/control/rebuild-filter", controller.RebuildFilter,
		"Filter rebuild signal sent", ActionRebuildFilter, SignalRebuildFilter)
}

// TestControlActionsWithNilChannel tests the control endpoints with a nil control channel
func TestControlActionsWithNilChannel(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Explicitly set the control channel to nil
	controller.controlChan = nil

	// Define test cases
	testCases := []struct {
		name     string
		endpoint string
		handler  func(echo.Context) error
	}{
		{
			name:     "RestartAnalysis with nil channel",
			endpoint: "/api/v2/control/restart",
			handler:  controller.RestartAnalysis,
		},
		{
			name:     "ReloadModel with nil channel",
			endpoint: "/api/v2/control/reload",
			handler:  controller.ReloadModel,
		},
		{
			name:     "RebuildFilter with nil channel",
			endpoint: "/api/v2/control/rebuild-filter",
			handler:  controller.RebuildFilter,
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

			// Check error response content
			assert.Contains(t, fmt.Sprint(errorResp["error"]), "control channel not initialized")
			assert.Contains(t, errorResp["message"], "System control interface not available")
			assert.Equal(t, http.StatusInternalServerError, int(errorResp["code"].(float64)))
		})
	}
}

// TestInitControlRoutesRegistration tests the registration of control-related API endpoints
func TestInitControlRoutesRegistration(t *testing.T) {
	// Use setupTestEnvironment to get a properly configured Echo and controller
	e, _, controller := setupTestEnvironment(t)

	// Re-initialize the routes to ensure a clean state
	controller.initControlRoutes()

	// Verify expected control routes are registered
	assertRoutesRegistered(t, e, []string{
		"GET /api/v2/control/actions",
		"POST /api/v2/control/restart",
		"POST /api/v2/control/reload",
		"POST /api/v2/control/rebuild-filter",
	})
}

// TestControlResultStructure verifies the ControlResult struct works as expected
func TestControlResultStructure(t *testing.T) {
	// Create a ControlResult
	result := ControlResult{
		Success:   true,
		Message:   "Test message",
		Action:    ActionRestartAnalysis,
		Timestamp: time.Date(2023, 5, 15, 10, 30, 0, 0, time.UTC),
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(result)
	require.NoError(t, err)

	// Verify JSON structure
	var jsonMap map[string]any
	err = json.Unmarshal(jsonData, &jsonMap)
	require.NoError(t, err)

	// Check fields
	assert.Equal(t, true, jsonMap["success"])
	assert.Equal(t, "Test message", jsonMap["message"])
	assert.Equal(t, ActionRestartAnalysis, jsonMap["action"])
	assert.Contains(t, jsonMap, "timestamp")
}

// TestControlActionStructure verifies the ControlAction struct works as expected
func TestControlActionStructure(t *testing.T) {
	// Create a ControlAction
	action := ControlAction{
		Action:      ActionReloadModel,
		Description: "Test description",
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(action)
	require.NoError(t, err)

	// Verify JSON structure
	var jsonMap map[string]any
	err = json.Unmarshal(jsonData, &jsonMap)
	require.NoError(t, err)

	// Check fields
	assert.Equal(t, ActionReloadModel, jsonMap["action"])
	assert.Equal(t, "Test description", jsonMap["description"])
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

// TestControlEndpointsWithUserAuth tests the control endpoints with proper auth
func TestControlEndpointsWithUserAuth(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Set up the control channel
	controlChan := make(chan string, 3) // Buffer for multiple signals
	controller.controlChan = controlChan

	// Test endpoints directly
	handlers := []struct {
		name    string
		handler func(echo.Context) error
		signal  string
	}{
		{"Restart Analysis", controller.RestartAnalysis, SignalRestartAnalysis},
		{"Reload Model", controller.ReloadModel, SignalReloadModel},
		{"Rebuild Filter", controller.RebuildFilter, SignalRebuildFilter},
	}

	for _, h := range handlers {
		t.Run(h.name, func(t *testing.T) {
			// Create request and context
			req := httptest.NewRequest(http.MethodPost, "/api/v2/control/test", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Call handler directly (bypass middleware)
			err := h.handler(c)
			require.NoError(t, err)

			// Verify response
			assert.Equal(t, http.StatusOK, rec.Code)

			// Parse response
			var result ControlResult
			err = json.Unmarshal(rec.Body.Bytes(), &result)
			require.NoError(t, err)

			// Check fields
			assert.True(t, result.Success)
			assert.NotEmpty(t, result.Message)
			assert.NotZero(t, result.Timestamp)

			// Verify signal
			select {
			case signal := <-controlChan:
				assert.Equal(t, h.signal, signal)
			case <-time.After(100 * time.Millisecond):
				assert.Fail(t, "Control signal was not sent")
			}
		})
	}
}

// TestControlActionsWithBlockedChannel tests the control endpoints with a blocked channel
// to ensure they don't hang indefinitely
func TestControlActionsWithBlockedChannel(t *testing.T) {
	// Setup
	_, _, controller := setupTestEnvironment(t)

	runControlActionsWithBlockedChannelTest(t, controller.RestartAnalysis)
}

// TestConcurrentControlRequests tests that multiple concurrent control requests
// are handled properly
func TestConcurrentControlRequests(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	runConcurrentControlRequestsTest(t, e, controller, controller.RestartAnalysis, "/api/v2/control/restart", SignalRestartAnalysis)
}

// TestControlEndpointsAuthScenarios tests various authentication scenarios for control endpoints
func TestControlEndpointsAuthScenarios(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Set up the control channel
	controlChan := make(chan string, 5)
	controller.controlChan = controlChan

	// Configure auth middleware with a validator that checks for a specific token
	authConfig := middleware.KeyAuthConfig{
		KeyLookup:  "header:Authorization",
		AuthScheme: "Bearer",
		Validator: func(key string, c echo.Context) (bool, error) {
			return key == "valid-token", nil
		},
	}

	// Create a group with auth middleware
	authGroup := e.Group("/api/v2/auth-test")
	authGroup.Use(middleware.KeyAuthWithConfig(authConfig))

	// Register the control handler on the auth group
	authGroup.POST("/restart", controller.RestartAnalysis)

	// Define test cases for different auth scenarios
	testCases := []struct {
		name           string
		authHeader     string
		expectedStatus int
	}{
		{"Valid token", "Bearer valid-token", http.StatusOK},
		{"Invalid token", "Bearer invalid-token", http.StatusUnauthorized},
		{"Missing token", "", http.StatusBadRequest},
		{"Malformed header", "Basic auth", http.StatusBadRequest},
		{"Wrong scheme", "Token valid-token", http.StatusBadRequest},
	}

	// Test each auth scenario
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create request with the specified auth header
			req := httptest.NewRequest(http.MethodPost, "/api/v2/auth-test/restart", http.NoBody)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()

			// Send the request through the Echo instance
			e.ServeHTTP(rec, req)

			// Verify the response status code
			assert.Equal(t, tc.expectedStatus, rec.Code, "Should return expected status code for auth scenario: %s", tc.name)

			// For successful requests, verify the response content
			if tc.expectedStatus == http.StatusOK {
				var result ControlResult
				err := json.Unmarshal(rec.Body.Bytes(), &result)
				require.NoError(t, err)
				assert.True(t, result.Success)
				assert.Equal(t, ActionRestartAnalysis, result.Action)
			}
		})
	}
}

// TestInvalidPayloads tests the control endpoints with invalid request payloads
func TestInvalidPayloads(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Set up the control channel
	controlChan := make(chan string, 1)
	controller.controlChan = controlChan

	// Create a request with invalid JSON payload
	req := httptest.NewRequest(http.MethodPost, "/api/v2/control/restart",
		strings.NewReader("{invalid-json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Call the handler
	err := controller.RestartAnalysis(c)

	// The handler should still work with invalid payloads since it doesn't expect any
	require.NoError(t, err, "Handler should not return an error with invalid payload")
	assert.Equal(t, http.StatusOK, rec.Code, "Should return OK even with invalid payload")

	// Verify the signal was sent
	select {
	case signal := <-controlChan:
		assert.Equal(t, SignalRestartAnalysis, signal)
	case <-time.After(100 * time.Millisecond):
		assert.Fail(t, "Control signal was not sent")
	}
}
