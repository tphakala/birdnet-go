// integrations_test.go: Package api provides tests for API v2 integration endpoints.

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TestInitIntegrationsRoutesRegistration tests the registration of integration-related API endpoints
func TestInitIntegrationsRoutesRegistration(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Re-initialize the routes to ensure a clean state
	controller.initIntegrationsRoutes()

	// Get all routes from the Echo instance
	routes := e.Routes()

	// Define the integration routes we expect to find
	expectedRoutes := map[string]bool{
		"GET /api/v2/integrations/mqtt/status":        false,
		"POST /api/v2/integrations/mqtt/test":         false,
		"GET /api/v2/integrations/birdweather/status": false,
		"POST /api/v2/integrations/birdweather/test":  false,
	}

	// Check each route
	for _, r := range routes {
		routePath := r.Method + " " + r.Path
		if _, exists := expectedRoutes[routePath]; exists {
			expectedRoutes[routePath] = true
		}
	}

	// Verify that all expected routes were registered
	for route, found := range expectedRoutes {
		assert.True(t, found, "Integration route not registered: %s", route)
	}
}

// MockMQTTClient is a mock implementation for MQTT client testing
type MockMQTTClient struct {
	mock.Mock
	ConnectFunc     func(ctx context.Context) error
	DisconnectFunc  func()
	IsConnectedFunc func() bool
	TestConnectFunc func(ctx context.Context, resultChan chan<- map[string]interface{})
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

func (m *MockMQTTClient) TestConnection(ctx context.Context, resultChan chan<- map[string]interface{}) {
	if m.TestConnectFunc != nil {
		m.TestConnectFunc(ctx, resultChan)
		return
	}
	m.Called(ctx, resultChan)
}

// MockBirdWeatherClient is a mock implementation for BirdWeather client testing
type MockBirdWeatherClient struct {
	mock.Mock
	TestConnectFunc func(ctx context.Context, resultChan chan<- map[string]interface{})
	CloseFunc       func()
}

func (m *MockBirdWeatherClient) TestConnection(ctx context.Context, resultChan chan<- map[string]interface{}) {
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
		validateResult func(*testing.T, map[string]interface{})
	}{
		{
			name:           "MQTT Disabled",
			mqttEnabled:    false,
			mqttBroker:     "tcp://mqtt.example.com:1883",
			mqttTopic:      "birdnet/detections",
			expectedStatus: http.StatusOK,
			validateResult: func(t *testing.T, result map[string]interface{}) {
				assert.False(t, result["connected"].(bool), "Connected should be false when MQTT is disabled")
				assert.Equal(t, "tcp://mqtt.example.com:1883", result["broker"], "Broker should match configuration")
				assert.Equal(t, "birdnet/detections", result["topic"], "Topic should match configuration")
				_, hasLastError := result["last_error"]
				assert.False(t, hasLastError, "There should be no error when MQTT is disabled")
			},
		},
		{
			name:           "MQTT Enabled but Not Connected",
			mqttEnabled:    true,
			mqttBroker:     "tcp://mqtt.example.com:1883",
			mqttTopic:      "birdnet/detections",
			expectedStatus: http.StatusOK,
			validateResult: func(t *testing.T, result map[string]interface{}) {
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
			controller.Settings.Main.Name = "TestBirdNet"

			// Create request
			req := httptest.NewRequest(http.MethodGet, "/api/v2/integrations/mqtt/status", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Call handler - we'll patch the client creation in the real system
			err := controller.GetMQTTStatus(c)
			assert.NoError(t, err)

			// Check status code
			assert.Equal(t, tc.expectedStatus, rec.Code)

			// Parse and validate response
			var result map[string]interface{}
			err = json.Unmarshal(rec.Body.Bytes(), &result)
			assert.NoError(t, err)

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
		validateResult func(*testing.T, map[string]interface{})
	}{
		{
			name:           "BirdWeather Disabled",
			bwEnabled:      false,
			bwID:           "",
			bwThreshold:    0.7,
			bwLocationAcc:  50.0,
			expectedStatus: http.StatusOK,
			validateResult: func(t *testing.T, result map[string]interface{}) {
				assert.False(t, result["enabled"].(bool), "Enabled should be false")
				assert.Equal(t, "", result["station_id"], "Station ID should be empty")
				assert.Equal(t, 0.7, result["threshold"], "Threshold should match configuration")
				assert.Equal(t, 50.0, result["location_accuracy"], "Location accuracy should match configuration")
			},
		},
		{
			name:           "BirdWeather Enabled",
			bwEnabled:      true,
			bwID:           "ABC123",
			bwThreshold:    0.8,
			bwLocationAcc:  100.0,
			expectedStatus: http.StatusOK,
			validateResult: func(t *testing.T, result map[string]interface{}) {
				assert.True(t, result["enabled"].(bool), "Enabled should be true")
				assert.Equal(t, "ABC123", result["station_id"], "Station ID should match configuration")
				assert.Equal(t, 0.8, result["threshold"], "Threshold should match configuration")
				assert.Equal(t, 100.0, result["location_accuracy"], "Location accuracy should match configuration")
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
			assert.NoError(t, err)

			// Check status code
			assert.Equal(t, tc.expectedStatus, rec.Code)

			// Parse response body
			var result map[string]interface{}
			err = json.Unmarshal(rec.Body.Bytes(), &result)
			assert.NoError(t, err)

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
		mqttEnabled    bool
		mqttBroker     string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "MQTT Not Enabled",
			mqttEnabled:    false,
			mqttBroker:     "tcp://mqtt.example.com:1883",
			expectedStatus: http.StatusOK,
			expectedBody:   `{"success":false,"message":"MQTT is not enabled in settings"}`,
		},
		{
			name:           "Broker Not Configured",
			mqttEnabled:    true,
			mqttBroker:     "",
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"success":false,"message":"MQTT broker not configured"}`,
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

			// Create request
			req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/mqtt/test", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Call handler
			err := controller.TestMQTTConnection(c)
			assert.NoError(t, err)

			// Check status code
			assert.Equal(t, tc.expectedStatus, rec.Code)

			// Check response body
			if tc.expectedBody != "" {
				assert.JSONEq(t, tc.expectedBody, strings.TrimSpace(rec.Body.String()))
			}
		})
	}
}

// TestTestBirdWeatherConnection tests the TestBirdWeatherConnection handler
func TestTestBirdWeatherConnection(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name           string
		bwEnabled      bool
		bwID           string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "BirdWeather Not Enabled",
			bwEnabled:      false,
			bwID:           "ABC123",
			expectedStatus: http.StatusOK,
			expectedBody:   `{"success":false,"message":"BirdWeather integration is not enabled in settings","state":"failed"}`,
		},
		{
			name:           "Station ID Not Configured",
			bwEnabled:      true,
			bwID:           "",
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"success":false,"message":"BirdWeather station ID not configured","state":"failed"}`,
		},
	}

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup
			e, _, controller := setupTestEnvironment(t)

			// Configure settings
			controller.Settings.Realtime.Birdweather.Enabled = tc.bwEnabled
			controller.Settings.Realtime.Birdweather.ID = tc.bwID

			// Create request
			req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/birdweather/test", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Call handler
			err := controller.TestBirdWeatherConnection(c)
			assert.NoError(t, err)

			// Check status code
			assert.Equal(t, tc.expectedStatus, rec.Code)

			// Check response body
			if tc.expectedBody != "" {
				assert.JSONEq(t, tc.expectedBody, strings.TrimSpace(rec.Body.String()))
			}
		})
	}
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
	testData := map[string]interface{}{
		"key1": "value1",
		"key2": 42,
		"key3": true,
		"nested": map[string]interface{}{
			"nested_key": "nested_value",
		},
	}

	// Call the function
	err := controller.writeJSONResponse(c, testData)
	assert.NoError(t, err)

	// Parse the response
	var result map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &result)
	assert.NoError(t, err)

	// Verify the result
	assert.Equal(t, "value1", result["key1"])
	assert.Equal(t, float64(42), result["key2"])
	assert.Equal(t, true, result["key3"])
	assert.IsType(t, map[string]interface{}{}, result["nested"])
	nestedMap := result["nested"].(map[string]interface{})
	assert.Equal(t, "nested_value", nestedMap["nested_key"])
}

// Advanced test for MQTT connection with client disconnection
func TestMQTTConnectionWithClientDisconnection(t *testing.T) {
	// Skip this test since we can't override package-level functions in our test environment
	t.Skip("This test requires mocking package-level functions")

	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Configure settings
	controller.Settings.Realtime.MQTT.Enabled = true
	controller.Settings.Realtime.MQTT.Broker = "tcp://mqtt.example.com:1883"

	// Create a cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	// Create request with the cancellable context
	req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/mqtt/test", http.NoBody).WithContext(ctx)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Start the handler in a goroutine
	errChan := make(chan error, 1)
	go func() {
		// Cancel the context after a short delay to simulate client disconnection
		time.Sleep(50 * time.Millisecond)
		cancel()

		errChan <- controller.TestMQTTConnection(c)
	}()

	// Wait for the handler to complete
	select {
	case err := <-errChan:
		assert.NoError(t, err)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Handler took too long to complete")
	}

	// Verify the handler completed gracefully despite the client disconnection
}

// Advanced test for BirdWeather connection with client disconnection
func TestBirdWeatherConnectionWithClientDisconnection(t *testing.T) {
	// Skip this test since we can't override package-level functions in our test environment
	t.Skip("This test requires mocking package-level functions")

	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Configure settings
	controller.Settings.Realtime.Birdweather.Enabled = true
	controller.Settings.Realtime.Birdweather.ID = "ABC123"

	// Create a cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	// Create request with the cancellable context
	req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/birdweather/test", http.NoBody).WithContext(ctx)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Start the handler in a goroutine
	errChan := make(chan error, 1)
	go func() {
		// Cancel the context after a short delay to simulate client disconnection
		time.Sleep(50 * time.Millisecond)
		cancel()

		errChan <- controller.TestBirdWeatherConnection(c)
	}()

	// Wait for the handler to complete
	select {
	case err := <-errChan:
		assert.NoError(t, err)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Handler took too long to complete")
	}

	// Verify the handler completed gracefully despite the client disconnection
}

// Test MQTT status with control channel
func TestGetMQTTStatusWithControlChannel(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Configure settings
	controller.Settings.Realtime.MQTT.Enabled = true
	controller.Settings.Realtime.MQTT.Broker = "tcp://mqtt.example.com:1883"
	controller.Settings.Realtime.MQTT.Topic = "birdnet/detections"
	controller.Settings.Main.Name = "TestBirdNet"

	// Create a mock control channel
	controlChan := make(chan string, 1)
	controller.controlChan = controlChan

	// Create request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/integrations/mqtt/status", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Call handler
	err := controller.GetMQTTStatus(c)
	assert.NoError(t, err)

	// Check status code
	assert.Equal(t, http.StatusOK, rec.Code)

	// Parse response body
	var result map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &result)
	assert.NoError(t, err)

	// Basic validation since we can't mock the MQTT client creation directly
	assert.Contains(t, result, "connected")
	assert.Equal(t, "tcp://mqtt.example.com:1883", result["broker"])
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
		assert.NoError(t, err, "Handler should not return an error")

		// And that the HTTP status is set correctly
		assert.Equal(t, http.StatusOK, rec.Code, "HTTP status should be OK")
	})

	t.Run("BirdWeather Client Creation Error", func(t *testing.T) {
		// Setup
		e, _, controller := setupTestEnvironment(t)

		// Configure settings to enable BirdWeather but with invalid configuration
		controller.Settings.Realtime.Birdweather.Enabled = true
		controller.Settings.Realtime.Birdweather.ID = "INVALID_ID"

		// Create a test HTTP request
		req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/birdweather/test", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		// Call handler
		err := controller.TestBirdWeatherConnection(c)

		// For streaming responses, we verify that the handler completes without error
		assert.NoError(t, err, "Handler should not return an error")

		// And that the HTTP status is set correctly
		assert.Equal(t, http.StatusOK, rec.Code, "HTTP status should be OK")
	})

	t.Run("MQTT Connection Context Cancellation", func(t *testing.T) {
		// Setup
		e, _, controller := setupTestEnvironment(t)

		// Configure settings to enable MQTT
		controller.Settings.Realtime.MQTT.Enabled = true
		controller.Settings.Realtime.MQTT.Broker = "tcp://mqtt.example.com:1883"

		// Create a cancellable context
		ctx, cancel := context.WithCancel(context.Background())
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
		assert.NoError(t, err, "Handler should not return an error")
	})

	t.Run("BirdWeather Connection Context Cancellation", func(t *testing.T) {
		// Setup
		e, _, controller := setupTestEnvironment(t)

		// Configure settings to enable BirdWeather
		controller.Settings.Realtime.Birdweather.Enabled = true
		controller.Settings.Realtime.Birdweather.ID = "VALID_ID" // Use a valid-looking ID

		// Create a cancellable context
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Create a test HTTP request with the cancellable context
		req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/birdweather/test", http.NoBody).WithContext(ctx)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		// Cancel the context after a very short delay
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		// Call handler - should return no error even when context is cancelled
		err := controller.TestBirdWeatherConnection(c)
		assert.NoError(t, err, "Handler should not return an error")
	})

	t.Run("MQTT Status Error Handling", func(t *testing.T) {
		// Setup
		e, _, controller := setupTestEnvironment(t)

		// Configure settings to enable MQTT with an invalid broker
		controller.Settings.Realtime.MQTT.Enabled = true
		controller.Settings.Realtime.MQTT.Broker = "tcp://nonexistent.broker:1883"
		controller.Settings.Realtime.MQTT.Topic = "birdnet/detections"
		controller.Settings.Main.Name = "TestBirdNet"

		// Create request
		req := httptest.NewRequest(http.MethodGet, "/api/v2/integrations/mqtt/status", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		// Call handler
		err := controller.GetMQTTStatus(c)
		assert.NoError(t, err)

		// Check status code
		assert.Equal(t, http.StatusOK, rec.Code)

		// Parse and validate response
		var result MQTTStatus
		err = json.Unmarshal(rec.Body.Bytes(), &result)
		assert.NoError(t, err)

		// Connected should be false with a connection error
		assert.False(t, result.Connected, "Connected should be false when MQTT connection fails")
		assert.NotEmpty(t, result.LastError, "LastError should not be empty when connection fails")
		assert.Contains(t, result.LastError, "error:connection:mqtt_broker", "Error should be properly formatted")
	})
}
