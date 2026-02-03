// integrations_test.go: Package api provides tests for API v2 integration endpoints.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Test constants for integration tests
const (
	testBirdNetName = "TestBirdNet"
	testMQTTBroker  = "tcp://mqtt.example.com:1883"
)

// runIntegrationConnectionHandlerTest runs table-driven tests for integration connection handlers
func runIntegrationConnectionHandlerTest(t *testing.T, handlerFunc func(*Controller, echo.Context) error, endpoint string, testCases []struct {
	name           string
	setupSettings  func(*Controller)
	expectedStatus int
	expectedBody   string
}) {
	t.Helper()

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup
			e, _, controller := setupTestEnvironment(t)

			// Configure settings
			tc.setupSettings(controller)

			// Create request with appropriate body
			var req *http.Request
			if strings.Contains(endpoint, "birdweather") {
				// BirdWeather endpoint expects JSON body matching the controller settings
				bwSettings := controller.Settings.Realtime.Birdweather
				bodyJSON := fmt.Sprintf(`{"enabled":%t,"id":%q,"threshold":%f,"locationAccuracy":%f}`,
					bwSettings.Enabled, bwSettings.ID, bwSettings.Threshold, bwSettings.LocationAccuracy)
				req = httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(bodyJSON))
				req.Header.Set("Content-Type", "application/json")
			} else {
				req = httptest.NewRequest(http.MethodPost, endpoint, http.NoBody)
			}
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Call handler
			err := handlerFunc(controller, c)
			require.NoError(t, err)

			// Check status code
			assert.Equal(t, tc.expectedStatus, rec.Code)

			// Check response body
			if tc.expectedBody != "" {
				assert.JSONEq(t, tc.expectedBody, strings.TrimSpace(rec.Body.String()))
			}
		})
	}
}

// runIntegrationConnectionWithDisconnectionTest runs integration connection handlers test with client disconnection.
// This helper is preserved for future use when mock injection becomes available in the test framework.
// Currently skipped because it requires mocking package-level functions for network operations.
func runIntegrationConnectionWithDisconnectionTest(t *testing.T, handlerFunc func(*Controller, echo.Context) error, endpoint string, setupSettings func(*Controller)) {
	t.Helper()

	// Skip this test since we can't override package-level functions in our test environment
	t.Skip("This test requires mocking package-level functions - preserved for future implementation")

	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Configure settings
	setupSettings(controller)

	// Create a cancellable context
	ctx, cancel := context.WithCancel(t.Context())

	// Create request with the cancellable context
	req := httptest.NewRequest(http.MethodPost, endpoint, http.NoBody).WithContext(ctx)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Start the handler in a goroutine
	errChan := make(chan error, 1)
	go func() {
		// Cancel the context after a short delay to simulate client disconnection
		time.Sleep(50 * time.Millisecond)
		cancel()

		errChan <- handlerFunc(controller, c)
	}()

	// Wait for the handler to complete
	select {
	case err := <-errChan:
		require.NoError(t, err)
	case <-time.After(500 * time.Millisecond):
		require.Fail(t, "Handler took too long to complete")
	}

	// Verify the handler completed gracefully despite the client disconnection
}

// TestInitIntegrationsRoutesRegistration tests the registration of integration-related API endpoints
func TestInitIntegrationsRoutesRegistration(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Re-initialize the routes to ensure a clean state
	controller.initIntegrationsRoutes()

	// Verify expected integration routes are registered
	assertRoutesRegistered(t, e, []string{
		"GET /api/v2/integrations/mqtt/status",
		"POST /api/v2/integrations/mqtt/test",
		"GET /api/v2/integrations/birdweather/status",
		"POST /api/v2/integrations/birdweather/test",
	})
}

// MockMQTTClient is a mock implementation for MQTT client testing
type MockMQTTClient struct {
	mock.Mock
	ConnectFunc     func(ctx context.Context) error
	DisconnectFunc  func()
	IsConnectedFunc func() bool
	TestConnectFunc func(ctx context.Context, resultChan chan<- map[string]any)
}

func (m *MockMQTTClient) Connect(ctx context.Context) error {
	if m.ConnectFunc != nil {
		return m.ConnectFunc(ctx)
	}
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockMQTTClient) Disconnect() {
	if m.DisconnectFunc != nil {
		m.DisconnectFunc()
		return
	}
	m.Called()
}

func (m *MockMQTTClient) IsConnected() bool {
	if m.IsConnectedFunc != nil {
		return m.IsConnectedFunc()
	}
	args := m.Called()
	return args.Bool(0)
}

func (m *MockMQTTClient) TestConnection(ctx context.Context, resultChan chan<- map[string]any) {
	if m.TestConnectFunc != nil {
		m.TestConnectFunc(ctx, resultChan)
		return
	}
	m.Called(ctx, resultChan)
}

// MockBirdWeatherClient is a mock implementation for BirdWeather client testing
type MockBirdWeatherClient struct {
	mock.Mock
	TestConnectFunc func(ctx context.Context, resultChan chan<- map[string]any)
	CloseFunc       func()
}

func (m *MockBirdWeatherClient) TestConnection(ctx context.Context, resultChan chan<- map[string]any) {
	if m.TestConnectFunc != nil {
		m.TestConnectFunc(ctx, resultChan)
		return
	}
	m.Called(ctx, resultChan)
}

func (m *MockBirdWeatherClient) Close() {
	if m.CloseFunc != nil {
		m.CloseFunc()
		return
	}
	m.Called()
}

// TestGetMQTTStatus tests the GetMQTTStatus handler
func TestGetMQTTStatus(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name           string
		mqttEnabled    bool
		mqttBroker     string
		mqttTopic      string
		expectedStatus int
		validateResult func(*testing.T, map[string]any)
	}{
		{
			name:           "MQTT Disabled",
			mqttEnabled:    false,
			mqttBroker:     testMQTTBroker,
			mqttTopic:      "birdnet/detections",
			expectedStatus: http.StatusOK,
			validateResult: func(t *testing.T, result map[string]any) {
				t.Helper()
				assert.False(t, result["connected"].(bool), "Connected should be false when MQTT is disabled")
				assert.Equal(t, testMQTTBroker, result["broker"], "Broker should match configuration")
				assert.Equal(t, "birdnet/detections", result["topic"], "Topic should match configuration")
				_, hasLastError := result["last_error"]
				assert.False(t, hasLastError, "There should be no error when MQTT is disabled")
			},
		},
		{
			name:           "MQTT Enabled but Not Connected",
			mqttEnabled:    true,
			mqttBroker:     testMQTTBroker,
			mqttTopic:      "birdnet/detections",
			expectedStatus: http.StatusOK,
			validateResult: func(t *testing.T, result map[string]any) {
				t.Helper()
				assert.False(t, result["connected"].(bool), "Connected should be false when MQTT connection fails")
				assert.Contains(t, result["last_error"], "error:connection:mqtt_broker", "Error should mention connection failure")
			},
		},
	}

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup
			e, _, controller := setupTestEnvironment(t)

			// Configure settings
			controller.Settings.Realtime.MQTT.Enabled = tc.mqttEnabled
			controller.Settings.Realtime.MQTT.Broker = tc.mqttBroker
			controller.Settings.Realtime.MQTT.Topic = tc.mqttTopic
			controller.Settings.Main.Name = testBirdNetName

			// Create request
			req := httptest.NewRequest(http.MethodGet, "/api/v2/integrations/mqtt/status", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Call handler - we'll patch the client creation in the real system
			err := controller.GetMQTTStatus(c)
			require.NoError(t, err)

			// Check status code
			assert.Equal(t, tc.expectedStatus, rec.Code)

			// Parse and validate response
			var result map[string]any
			err = json.Unmarshal(rec.Body.Bytes(), &result)
			require.NoError(t, err)

			tc.validateResult(t, result)
		})
	}
}

// TestGetBirdWeatherStatus tests the GetBirdWeatherStatus handler
func TestGetBirdWeatherStatus(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name           string
		bwEnabled      bool
		bwID           string
		bwThreshold    float64
		bwLocationAcc  float64
		expectedStatus int
		validateResult func(*testing.T, map[string]any)
	}{
		{
			name:           "BirdWeather Disabled",
			bwEnabled:      false,
			bwID:           "",
			bwThreshold:    0.7,
			bwLocationAcc:  50.0,
			expectedStatus: http.StatusOK,
			validateResult: func(t *testing.T, result map[string]any) {
				t.Helper()
				assert.False(t, result["enabled"].(bool), "Enabled should be false")
				assert.Empty(t, result["station_id"], "Station ID should be empty")
				assert.InDelta(t, 0.7, result["threshold"], 0.01, "Threshold should match configuration")
				assert.InDelta(t, 50.0, result["location_accuracy"], 0.01, "Location accuracy should match configuration")
			},
		},
		{
			name:           "BirdWeather Enabled",
			bwEnabled:      true,
			bwID:           "ABC123",
			bwThreshold:    0.8,
			bwLocationAcc:  100.0,
			expectedStatus: http.StatusOK,
			validateResult: func(t *testing.T, result map[string]any) {
				t.Helper()
				assert.True(t, result["enabled"].(bool), "Enabled should be true")
				assert.Equal(t, "ABC123", result["station_id"], "Station ID should match configuration")
				assert.InDelta(t, 0.8, result["threshold"], 0.01, "Threshold should match configuration")
				assert.InDelta(t, 100.0, result["location_accuracy"], 0.01, "Location accuracy should match configuration")
			},
		},
	}

	// Run all test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup
			e, _, controller := setupTestEnvironment(t)

			// Configure settings for the test case
			controller.Settings.Realtime.Birdweather.Enabled = tc.bwEnabled
			controller.Settings.Realtime.Birdweather.ID = tc.bwID
			controller.Settings.Realtime.Birdweather.Threshold = tc.bwThreshold
			controller.Settings.Realtime.Birdweather.LocationAccuracy = tc.bwLocationAcc

			// Create request context
			req := httptest.NewRequest(http.MethodGet, "/api/v2/integrations/birdweather/status", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Call the handler
			err := controller.GetBirdWeatherStatus(c)
			require.NoError(t, err)

			// Check status code
			assert.Equal(t, tc.expectedStatus, rec.Code)

			// Parse response body
			var result map[string]any
			err = json.Unmarshal(rec.Body.Bytes(), &result)
			require.NoError(t, err)

			// Validate the result using the test case's validation function
			tc.validateResult(t, result)
		})
	}
}

// TestTestMQTTConnection tests the TestMQTTConnection handler
func TestTestMQTTConnection(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name           string
		setupSettings  func(*Controller)
		expectedStatus int
		expectedBody   string
	}{
		{
			name: "MQTT Not Enabled",
			setupSettings: func(controller *Controller) {
				controller.Settings.Realtime.MQTT.Enabled = false
				controller.Settings.Realtime.MQTT.Broker = testMQTTBroker
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `{"success":false,"message":"MQTT is not enabled in settings"}`,
		},
		{
			name: "Broker Not Configured",
			setupSettings: func(controller *Controller) {
				controller.Settings.Realtime.MQTT.Enabled = true
				controller.Settings.Realtime.MQTT.Broker = ""
			},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"success":false,"message":"MQTT broker not configured"}`,
		},
	}

	runIntegrationConnectionHandlerTest(t, (*Controller).TestMQTTConnection, "/api/v2/integrations/mqtt/test", testCases)
}

// TestTestBirdWeatherConnection tests the TestBirdWeatherConnection handler
func TestTestBirdWeatherConnection(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name           string
		setupSettings  func(*Controller)
		expectedStatus int
		expectedBody   string
	}{
		{
			name: "BirdWeather Not Enabled",
			setupSettings: func(controller *Controller) {
				controller.Settings.Realtime.Birdweather.Enabled = false
				controller.Settings.Realtime.Birdweather.ID = "ABC123"
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `{"success":false,"message":"BirdWeather integration is not enabled","state":"failed"}`,
		},
		{
			name: "Station ID Not Configured",
			setupSettings: func(controller *Controller) {
				controller.Settings.Realtime.Birdweather.Enabled = true
				controller.Settings.Realtime.Birdweather.ID = ""
			},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"success":false,"message":"BirdWeather station ID not configured","state":"failed"}`,
		},
	}

	runIntegrationConnectionHandlerTest(t, (*Controller).TestBirdWeatherConnection, "/api/v2/integrations/birdweather/test", testCases)
}

// TestWriteJSONResponse tests the writeJSONResponse helper function
func TestWriteJSONResponse(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Create request context
	req := httptest.NewRequest(http.MethodGet, "/test", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Set content type manually since this is a helper function
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	c.Response().WriteHeader(http.StatusOK)

	// Test data
	testData := map[string]any{
		"key1": "value1",
		"key2": 42,
		"key3": true,
		"nested": map[string]any{
			"nested_key": "nested_value",
		},
	}

	// Call the function
	err := controller.writeJSONResponse(c, testData)
	require.NoError(t, err)

	// Parse the response
	var result map[string]any
	err = json.Unmarshal(rec.Body.Bytes(), &result)
	require.NoError(t, err)

	// Verify the result
	assert.Equal(t, "value1", result["key1"])
	assert.InDelta(t, 42, result["key2"], 0.01)
	assert.Equal(t, true, result["key3"])
	assert.IsType(t, map[string]any{}, result["nested"])
	nestedMap := result["nested"].(map[string]any)
	assert.Equal(t, "nested_value", nestedMap["nested_key"])
}

// Advanced test for MQTT connection with client disconnection
func TestMQTTConnectionWithClientDisconnection(t *testing.T) {
	runIntegrationConnectionWithDisconnectionTest(t, (*Controller).TestMQTTConnection, "/api/v2/integrations/mqtt/test", func(controller *Controller) {
		controller.Settings.Realtime.MQTT.Enabled = true
		controller.Settings.Realtime.MQTT.Broker = testMQTTBroker
	})
}

// Advanced test for BirdWeather connection with client disconnection
func TestBirdWeatherConnectionWithClientDisconnection(t *testing.T) {
	runIntegrationConnectionWithDisconnectionTest(t, (*Controller).TestBirdWeatherConnection, "/api/v2/integrations/birdweather/test", func(controller *Controller) {
		controller.Settings.Realtime.Birdweather.Enabled = true
		controller.Settings.Realtime.Birdweather.ID = "ABC123"
	})
}

// Test MQTT status with control channel
func TestGetMQTTStatusWithControlChannel(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Configure settings
	controller.Settings.Realtime.MQTT.Enabled = true
	controller.Settings.Realtime.MQTT.Broker = testMQTTBroker
	controller.Settings.Realtime.MQTT.Topic = "birdnet/detections"
	controller.Settings.Main.Name = testBirdNetName

	// Create a mock control channel
	controlChan := make(chan string, 1)
	controller.controlChan = controlChan

	// Create request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/integrations/mqtt/status", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Call handler
	err := controller.GetMQTTStatus(c)
	require.NoError(t, err)

	// Check status code
	assert.Equal(t, http.StatusOK, rec.Code)

	// Parse response body
	var result map[string]any
	err = json.Unmarshal(rec.Body.Bytes(), &result)
	require.NoError(t, err)

	// Basic validation since we can't mock the MQTT client creation directly
	assert.Contains(t, result, "connected")
	assert.Equal(t, testMQTTBroker, result["broker"])
	assert.Equal(t, "birdnet/detections", result["topic"])

	// Close the control channel
	close(controlChan)
}

// TestErrorHandlingForIntegrations tests error handling for both MQTT and BirdWeather integrations
func TestErrorHandlingForIntegrations(t *testing.T) {
	// Test error handling for MQTT and BirdWeather integrations
	t.Run("MQTT Client Creation Error", func(t *testing.T) {
		// Setup
		e, _, controller := setupTestEnvironment(t)

		// Configure settings to enable MQTT
		controller.Settings.Realtime.MQTT.Enabled = true
		controller.Settings.Realtime.MQTT.Broker = "tcp://invalid-broker.example.com:1883"

		// Create a test HTTP request
		req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/mqtt/test", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		// Call handler
		err := controller.TestMQTTConnection(c)

		// For streaming responses, we verify that the handler completes without error
		require.NoError(t, err, "Handler should not return an error")

		// And that the HTTP status is set correctly
		assert.Equal(t, http.StatusOK, rec.Code, "HTTP status should be OK")
	})

	t.Run("BirdWeather Client Creation Error", func(t *testing.T) {
		// Setup
		e, _, controller := setupTestEnvironment(t)

		// Configure settings to enable BirdWeather but with invalid configuration
		controller.Settings.Realtime.Birdweather.Enabled = true
		controller.Settings.Realtime.Birdweather.ID = "INVALID_ID"

		// Create JSON request body for BirdWeather test
		requestBody := `{
			"enabled": true,
			"id": "INVALID_ID",
			"threshold": 0.8,
			"locationAccuracy": 50.0,
			"debug": false
		}`
		req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/birdweather/test", strings.NewReader(requestBody))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		// Call handler
		err := controller.TestBirdWeatherConnection(c)

		// For streaming responses, we verify that the handler completes without error
		require.NoError(t, err, "Handler should not return an error")

		// And that the HTTP status is set correctly
		assert.Equal(t, http.StatusOK, rec.Code, "HTTP status should be OK")
	})

	t.Run("MQTT Connection Context Cancellation", func(t *testing.T) {
		// Setup
		e, _, controller := setupTestEnvironment(t)

		// Configure settings to enable MQTT
		controller.Settings.Realtime.MQTT.Enabled = true
		controller.Settings.Realtime.MQTT.Broker = testMQTTBroker

		// Create a cancellable context
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		// Create a test HTTP request with the cancellable context
		req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/mqtt/test", http.NoBody).WithContext(ctx)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		// Cancel the context after a very short delay
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		// Call handler - should return no error even when context is cancelled
		err := controller.TestMQTTConnection(c)
		require.NoError(t, err, "Handler should not return an error")
	})

	t.Run("BirdWeather Connection Context Cancellation", func(t *testing.T) {
		// Setup
		e, _, controller := setupTestEnvironment(t)

		// Configure settings to enable BirdWeather
		controller.Settings.Realtime.Birdweather.Enabled = true
		controller.Settings.Realtime.Birdweather.ID = "VALID_ID" // Use a valid-looking ID

		// Create a cancellable context
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		// Create JSON request body for BirdWeather test
		requestBody := `{
			"enabled": true,
			"id": "VALID_ID",
			"threshold": 0.8,
			"locationAccuracy": 50.0,
			"debug": false
		}`
		req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/birdweather/test", strings.NewReader(requestBody)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		// Cancel the context after a very short delay
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		// Call handler - should return no error even when context is cancelled
		err := controller.TestBirdWeatherConnection(c)
		require.NoError(t, err, "Handler should not return an error")
	})

	t.Run("MQTT Status Error Handling", func(t *testing.T) {
		// Setup
		e, _, controller := setupTestEnvironment(t)

		// Configure settings to enable MQTT with an invalid broker
		controller.Settings.Realtime.MQTT.Enabled = true
		controller.Settings.Realtime.MQTT.Broker = "tcp://nonexistent.broker:1883"
		controller.Settings.Realtime.MQTT.Topic = "birdnet/detections"
		controller.Settings.Main.Name = testBirdNetName

		// Create request
		req := httptest.NewRequest(http.MethodGet, "/api/v2/integrations/mqtt/status", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		// Call handler
		err := controller.GetMQTTStatus(c)
		require.NoError(t, err)

		// Check status code
		assert.Equal(t, http.StatusOK, rec.Code)

		// Parse and validate response
		var result MQTTStatus
		err = json.Unmarshal(rec.Body.Bytes(), &result)
		require.NoError(t, err)

		// Connected should be false with a connection error
		assert.False(t, result.Connected, "Connected should be false when MQTT connection fails")
		assert.NotEmpty(t, result.LastError, "LastError should not be empty when connection fails")
		assert.Contains(t, result.LastError, "error:connection:mqtt_broker", "Error should be properly formatted")
	})
}
