// integrations_test.go: tests for the api/v2 integrations domain endpoints.

package integrations

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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Test constants for integration tests
const (
	testBirdNetName = "TestBirdNet"
	testMQTTBroker  = "tcp://mqtt.example.com:1883"
)

// runIntegrationConnectionHandlerTest runs table-driven tests for integration connection handlers
func runIntegrationConnectionHandlerTest(t *testing.T, handlerFunc func(*Handler, echo.Context) error, endpoint string, testCases []struct {
	name           string
	setupSettings  func(*Handler)
	expectedStatus int
	expectedBody   string
}) {
	t.Helper()

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup
			e, controller := newIntegrationsTestHandler(t)

			// Configure settings
			tc.setupSettings(controller)

			// Create request with appropriate body
			var req *http.Request
			if strings.Contains(endpoint, "birdweather") {
				// BirdWeather endpoint expects JSON body matching the controller settings
				bwSettings := controller.Settings.Load().Realtime.Birdweather
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
func runIntegrationConnectionWithDisconnectionTest(t *testing.T, handlerFunc func(*Handler, echo.Context) error, endpoint string, setupSettings func(*Handler)) {
	t.Helper()

	// Skip this test since we can't override package-level functions in our test environment
	t.Skip("This test requires mocking package-level functions - preserved for future implementation")

	// Setup
	e, controller := newIntegrationsTestHandler(t)

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
			e, controller := newIntegrationsTestHandler(t)

			// Configure settings
			controller.Settings.Load().Realtime.MQTT.Enabled = tc.mqttEnabled
			controller.Settings.Load().Realtime.MQTT.Broker = tc.mqttBroker
			controller.Settings.Load().Realtime.MQTT.Topic = tc.mqttTopic
			controller.Settings.Load().Main.Name = testBirdNetName

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
			e, controller := newIntegrationsTestHandler(t)

			// Configure settings for the test case
			controller.Settings.Load().Realtime.Birdweather.Enabled = tc.bwEnabled
			controller.Settings.Load().Realtime.Birdweather.ID = tc.bwID
			controller.Settings.Load().Realtime.Birdweather.Threshold = tc.bwThreshold
			controller.Settings.Load().Realtime.Birdweather.LocationAccuracy = tc.bwLocationAcc

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
		setupSettings  func(*Handler)
		expectedStatus int
		expectedBody   string
	}{
		{
			name: "MQTT Not Enabled",
			setupSettings: func(controller *Handler) {
				controller.Settings.Load().Realtime.MQTT.Enabled = false
				controller.Settings.Load().Realtime.MQTT.Broker = testMQTTBroker
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `{"success":false,"message":"MQTT is not enabled in settings"}`,
		},
		{
			name: "Broker Not Configured",
			setupSettings: func(controller *Handler) {
				controller.Settings.Load().Realtime.MQTT.Enabled = true
				controller.Settings.Load().Realtime.MQTT.Broker = ""
			},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"success":false,"message":"MQTT broker not configured"}`,
		},
	}

	runIntegrationConnectionHandlerTest(t, (*Handler).TestMQTTConnection, "/api/v2/integrations/mqtt/test", testCases)
}

// TestTestBirdWeatherConnection tests the TestBirdWeatherConnection handler
func TestTestBirdWeatherConnection(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name           string
		setupSettings  func(*Handler)
		expectedStatus int
		expectedBody   string
	}{
		{
			name: "BirdWeather Not Enabled",
			setupSettings: func(controller *Handler) {
				controller.Settings.Load().Realtime.Birdweather.Enabled = false
				controller.Settings.Load().Realtime.Birdweather.ID = "ABC123"
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `{"success":false,"message":"BirdWeather integration is not enabled","state":"failed"}`,
		},
		{
			name: "Station ID Not Configured",
			setupSettings: func(controller *Handler) {
				controller.Settings.Load().Realtime.Birdweather.Enabled = true
				controller.Settings.Load().Realtime.Birdweather.ID = ""
			},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"success":false,"message":"BirdWeather station ID not configured","state":"failed"}`,
		},
	}

	runIntegrationConnectionHandlerTest(t, (*Handler).TestBirdWeatherConnection, "/api/v2/integrations/birdweather/test", testCases)
}

// TestTestEBirdConnection tests the TestEBirdConnection handler
func TestTestEBirdConnection(t *testing.T) {
	testCases := []struct {
		name           string
		requestBody    string
		expectedStatus int
		expectError    bool
		validateResult func(*testing.T, string)
	}{
		{
			name:           "eBird Not Enabled",
			requestBody:    `{"enabled":false,"apiKey":"test-key","locale":"en"}`,
			expectedStatus: http.StatusOK,
			validateResult: func(t *testing.T, body string) {
				t.Helper()
				assert.Contains(t, body, `"success":false`)
				assert.Contains(t, body, "not enabled")
			},
		},
		{
			name:           "API Key Not Configured",
			requestBody:    `{"enabled":true,"apiKey":"","locale":"en"}`,
			expectedStatus: http.StatusBadRequest,
			validateResult: func(t *testing.T, body string) {
				t.Helper()
				assert.Contains(t, body, `"success":false`)
				assert.Contains(t, body, "API key is required")
			},
		},
		{
			name:           "Invalid Request Body",
			requestBody:    `{invalid json`,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
			validateResult: func(t *testing.T, body string) {
				t.Helper()
				assert.Contains(t, body, "error")
			},
		},
		{
			name:           "Valid Request Enters Streaming",
			requestBody:    `{"enabled":true,"apiKey":"test-key-123","locale":"en"}`,
			expectedStatus: http.StatusOK,
			validateResult: func(t *testing.T, body string) {
				t.Helper()
				assert.NotEmpty(t, body, "Response body should not be empty")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			e, controller := newIntegrationsTestHandler(t)

			req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/ebird/test",
				strings.NewReader(tc.requestBody))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := controller.TestEBirdConnection(c)

			if tc.expectError {
				if err != nil {
					return
				}
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tc.expectedStatus, rec.Code)

			tc.validateResult(t, strings.TrimSpace(rec.Body.String()))
		})
	}
}

// TestWriteJSONResponse tests the writeJSONResponse helper function
func TestWriteJSONResponse(t *testing.T) {
	// Setup
	e, controller := newIntegrationsTestHandler(t)

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
	runIntegrationConnectionWithDisconnectionTest(t, (*Handler).TestMQTTConnection, "/api/v2/integrations/mqtt/test", func(controller *Handler) {
		controller.Settings.Load().Realtime.MQTT.Enabled = true
		controller.Settings.Load().Realtime.MQTT.Broker = testMQTTBroker
	})
}

// Advanced test for BirdWeather connection with client disconnection
func TestBirdWeatherConnectionWithClientDisconnection(t *testing.T) {
	runIntegrationConnectionWithDisconnectionTest(t, (*Handler).TestBirdWeatherConnection, "/api/v2/integrations/birdweather/test", func(controller *Handler) {
		controller.Settings.Load().Realtime.Birdweather.Enabled = true
		controller.Settings.Load().Realtime.Birdweather.ID = "ABC123"
	})
}

// TestGetMQTTStatusEnabled exercises GetMQTTStatus with MQTT enabled and verifies
// the broker/topic are reported back. The connection check runs against an
// unreachable broker, so connected stays false.
func TestGetMQTTStatusEnabled(t *testing.T) {
	// Setup
	e, controller := newIntegrationsTestHandler(t)

	// Configure settings
	controller.Settings.Load().Realtime.MQTT.Enabled = true
	controller.Settings.Load().Realtime.MQTT.Broker = testMQTTBroker
	controller.Settings.Load().Realtime.MQTT.Topic = "birdnet/detections"
	controller.Settings.Load().Main.Name = testBirdNetName

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
}

// TestErrorHandlingForIntegrations tests error handling for both MQTT and BirdWeather integrations
func TestErrorHandlingForIntegrations(t *testing.T) {
	// Test error handling for MQTT and BirdWeather integrations
	t.Run("MQTT Client Creation Error", func(t *testing.T) {
		// Setup
		e, controller := newIntegrationsTestHandler(t)

		// Configure settings to enable MQTT
		controller.Settings.Load().Realtime.MQTT.Enabled = true
		controller.Settings.Load().Realtime.MQTT.Broker = "tcp://invalid-broker.example.com:1883"

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
		e, controller := newIntegrationsTestHandler(t)

		// Configure settings to enable BirdWeather but with invalid configuration
		controller.Settings.Load().Realtime.Birdweather.Enabled = true
		controller.Settings.Load().Realtime.Birdweather.ID = "INVALID_ID"

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
		e, controller := newIntegrationsTestHandler(t)

		// Configure settings to enable MQTT
		controller.Settings.Load().Realtime.MQTT.Enabled = true
		controller.Settings.Load().Realtime.MQTT.Broker = testMQTTBroker

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
		e, controller := newIntegrationsTestHandler(t)

		// Configure settings to enable BirdWeather
		controller.Settings.Load().Realtime.Birdweather.Enabled = true
		controller.Settings.Load().Realtime.Birdweather.ID = "VALID_ID" // Use a valid-looking ID

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
		e, controller := newIntegrationsTestHandler(t)

		// Configure settings to enable MQTT with an invalid broker
		controller.Settings.Load().Realtime.MQTT.Enabled = true
		controller.Settings.Load().Realtime.MQTT.Broker = "tcp://nonexistent.broker:1883"
		controller.Settings.Load().Realtime.MQTT.Topic = "birdnet/detections"
		controller.Settings.Load().Main.Name = testBirdNetName

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

// redactedSecretPlaceholder is the sentinel the settings UI returns in place of
// stored secrets; it aliases the shared apicore.RedactedValue constant so the
// tests and the handlers agree on the exact value a client sends when the user
// has not re-entered a secret.
const redactedSecretPlaceholder = apicore.RedactedValue

// TestRestoreRedactedSecret verifies the canonical single-field restore
// primitive shared by the settings save flow and the integration
// test-connection handlers.
func TestRestoreRedactedSecret(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		current  string
		incoming string
		want     string
	}{
		{
			name:     "placeholder is restored to current real secret",
			current:  "real-saved-key",
			incoming: redactedSecretPlaceholder,
			want:     "real-saved-key",
		},
		{
			name:     "real incoming value is left untouched",
			current:  "real-saved-key",
			incoming: "user-typed-new-key",
			want:     "user-typed-new-key",
		},
		{
			name:     "empty incoming stays empty (not a placeholder)",
			current:  "real-saved-key",
			incoming: "",
			want:     "",
		},
		{
			name:     "placeholder with empty current resolves to empty",
			current:  "",
			incoming: redactedSecretPlaceholder,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			incoming := tt.incoming
			apicore.RestoreRedactedSecret(tt.current, &incoming)
			assert.Equal(t, tt.want, incoming)
		})
	}
}

// publishWeatherTestSettings sets the saved weather provider config the handler
// reads via CurrentSettings() and returns the handler. It re-publishes the
// settings so CurrentSettings() (which reads the global snapshot first) observes
// the saved keys/endpoint.
func publishWeatherTestSettings(t *testing.T, mutate func(*conf.Settings)) (*echo.Echo, *Handler) {
	t.Helper()
	e, controller := newIntegrationsTestHandler(t)
	settings := controller.Settings.Load()
	mutate(settings)
	apitest.PublishTestSettings(t, settings)
	return e, controller
}

// TestTestWeatherConnection_RestoresRedactedAPIKey verifies the weather
// test-connection handler restores redacted API-key placeholders from the saved
// settings before running the test, fixing the bug where clicking "Test" after
// saving (without re-entering the key) failed with a bogus authentication error.
func TestTestWeatherConnection_RestoresRedactedAPIKey(t *testing.T) {
	// Negative path: a redacted placeholder with NO saved key must restore to
	// the empty string and fail validation with "API key is required". On the
	// buggy code (no restore) the non-empty placeholder slips past validation
	// and the handler proceeds to stream, so this case fails on main.
	t.Run("placeholder without saved key fails validation", func(t *testing.T) {
		e, controller := publishWeatherTestSettings(t, func(s *conf.Settings) {
			s.Realtime.Weather.OpenWeather.APIKey = "" // nothing saved
		})

		body := fmt.Sprintf(`{"provider":%q,"openWeather":{"apiKey":%q}}`,
			WeatherProviderOpenWeather, redactedSecretPlaceholder)
		req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/weather/test",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := controller.TestWeatherConnection(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, rec.Code,
			"placeholder restored to empty key must fail the required-key validation")
		assert.Contains(t, rec.Body.String(), "API key is required")
	})

	// Positive path (hermetic): with a real saved key, the handler's restore
	// sequence must make the OpenWeather authentication stage send the REAL key
	// (not the placeholder) to the API endpoint. We exercise the exact restore
	// the handler runs and the production auth stage against a local server.
	t.Run("placeholder is restored to real saved key at auth stage", func(t *testing.T) {
		const realKey = "real-openweather-key-123"

		var gotAppID string
		var gotAppIDOnce sync.Once
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAppIDOnce.Do(func() { gotAppID = r.URL.Query().Get("appid") })
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(server.Close)

		_, controller := publishWeatherTestSettings(t, func(s *conf.Settings) {
			s.Realtime.Weather.OpenWeather.APIKey = realKey
			s.Realtime.Weather.OpenWeather.Endpoint = server.URL
		})

		// The client echoes back the saved provider config (endpoint included) but
		// with the API key as the redacted placeholder, because the user did not
		// re-type the key on the settings page.
		request := WeatherTestRequest{
			Provider: WeatherProviderOpenWeather,
			OpenWeather: conf.OpenWeatherSettings{
				APIKey:   redactedSecretPlaceholder,
				Endpoint: server.URL,
			},
		}

		// Reproduce the handler's restore sequence using the production helper.
		current := controller.CurrentSettings()
		apicore.RestoreRedactedSecret(current.Realtime.Weather.OpenWeather.APIKey, &request.OpenWeather.APIKey)
		apicore.RestoreRedactedSecret(current.Realtime.Weather.Wunderground.APIKey, &request.Wunderground.APIKey)

		testSettings := conf.CloneSettings(current)
		testSettings.Realtime.Weather = conf.WeatherSettings{
			Provider:     request.Provider,
			OpenWeather:  request.OpenWeather,
			Wunderground: request.Wunderground,
		}

		msg, err := controller.testWeatherAuthentication(t.Context(), testSettings)
		require.NoError(t, err)
		assert.Contains(t, msg, "Successfully authenticated")
		assert.Equal(t, realKey, gotAppID,
			"auth stage must use the real saved key, not the redacted placeholder")
		assert.NotEqual(t, redactedSecretPlaceholder, gotAppID)
	})
}

// TestTestEBirdConnection_RestoresRedactedAPIKey verifies the eBird
// test-connection handler restores the redacted API-key placeholder from the
// saved settings before running the test.
func TestTestEBirdConnection_RestoresRedactedAPIKey(t *testing.T) {
	// Negative path: placeholder with NO saved key restores to empty and fails
	// the required-key validation. On the buggy code the non-empty placeholder
	// passes validation, so this case fails on main.
	t.Run("placeholder without saved key fails validation", func(t *testing.T) {
		e, controller := publishWeatherTestSettings(t, func(s *conf.Settings) {
			s.Realtime.EBird.APIKey = "" // nothing saved
		})

		body := fmt.Sprintf(`{"enabled":true,"apiKey":%q,"locale":"en"}`, redactedSecretPlaceholder)
		req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/ebird/test",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := controller.TestEBirdConnection(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, rec.Code,
			"placeholder restored to empty key must fail the required-key validation")
		assert.Contains(t, rec.Body.String(), "API key is required")
	})

	// Positive path: with a real saved key, a placeholder request restores to the
	// real key so the value handed to the auth stage is the real key.
	t.Run("placeholder is restored to real saved key", func(t *testing.T) {
		const realKey = "real-ebird-key-456"

		_, controller := publishWeatherTestSettings(t, func(s *conf.Settings) {
			s.Realtime.EBird.APIKey = realKey
		})

		apiKey := redactedSecretPlaceholder
		apicore.RestoreRedactedSecret(controller.CurrentSettings().Realtime.EBird.APIKey, &apiKey)
		assert.Equal(t, realKey, apiKey,
			"eBird test must use the real saved key, not the redacted placeholder")
	})
}
