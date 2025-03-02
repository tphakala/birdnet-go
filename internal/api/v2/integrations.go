// internal/api/v2/integrations.go
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/birdweather"
	"github.com/tphakala/birdnet-go/internal/mqtt"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// MQTTStatus represents the current status of the MQTT connection
type MQTTStatus struct {
	Connected bool   `json:"connected"`            // Whether the MQTT client is currently connected to the broker
	Broker    string `json:"broker"`               // The URI of the MQTT broker (e.g., tcp://mqtt.example.com:1883)
	Topic     string `json:"topic"`                // The topic pattern used for publishing/subscribing to MQTT messages
	ClientID  string `json:"client_id"`            // The unique identifier used by this client when connecting to the broker
	LastError string `json:"last_error,omitempty"` // Most recent error message, if any connection issues occurred
}

// MQTTTestResult represents the result of an MQTT connection test
type MQTTTestResult struct {
	Success     bool   `json:"success"`                   // Whether the connection test was successful
	Message     string `json:"message"`                   // Human-readable description of the test result
	ElapsedTime int64  `json:"elapsed_time_ms,omitempty"` // Time taken to complete the test in milliseconds
}

// BirdWeatherStatus represents the current status of the BirdWeather integration
type BirdWeatherStatus struct {
	Enabled          bool    `json:"enabled"`              // Whether BirdWeather integration is enabled
	StationID        string  `json:"station_id"`           // The BirdWeather station ID
	Threshold        float64 `json:"threshold"`            // The confidence threshold for reporting detections
	LocationAccuracy float64 `json:"location_accuracy"`    // The location accuracy in meters
	LastError        string  `json:"last_error,omitempty"` // Most recent error message, if any issues occurred
}

// initIntegrationsRoutes registers all integration-related API endpoints
func (c *Controller) initIntegrationsRoutes() {
	// Create integrations API group with auth middleware
	integrationsGroup := c.Group.Group("/integrations", c.AuthMiddleware)

	// MQTT routes
	mqttGroup := integrationsGroup.Group("/mqtt")
	mqttGroup.GET("/status", c.GetMQTTStatus)
	mqttGroup.POST("/test", c.TestMQTTConnection)

	// BirdWeather routes
	bwGroup := integrationsGroup.Group("/birdweather")
	bwGroup.GET("/status", c.GetBirdWeatherStatus)
	bwGroup.POST("/test", c.TestBirdWeatherConnection)

	// Other integration routes could be added here:
	// - Weather APIs
	// - External media storage
}

// GetMQTTStatus handles GET /api/v2/integrations/mqtt/status
func (c *Controller) GetMQTTStatus(ctx echo.Context) error {
	// Get MQTT configuration from settings
	mqttConfig := c.Settings.Realtime.MQTT

	// Prepare status response
	status := MQTTStatus{
		Connected: false, // Default to not connected
		Broker:    mqttConfig.Broker,
		Topic:     mqttConfig.Topic,
		ClientID:  c.Settings.Main.Name, // Use the application name as client ID
	}

	// If MQTT is not enabled, return status as-is
	if !mqttConfig.Enabled {
		return ctx.JSON(http.StatusOK, status)
	}

	// Check if we have an active MQTT connection through the control channel
	if c.controlChan != nil {
		c.Debug("Requesting MQTT status check")

		// Create a temporary MQTT client to check connection status
		metrics, err := telemetry.NewMetrics()
		if err == nil {
			tempClient, err := mqtt.NewClient(c.Settings, metrics)
			if err == nil {
				// Use a short timeout to check connection
				testCtx, cancel := context.WithTimeout(ctx.Request().Context(), 3*time.Second)
				defer cancel()

				// Try to connect and set status based on result
				err = tempClient.Connect(testCtx)
				status.Connected = err == nil && tempClient.IsConnected()

				if err != nil {
					status.LastError = fmt.Sprintf("error:connection:mqtt_broker:%s", err.Error()) // Standardized error code format
				}

				// Disconnect the temporary client
				tempClient.Disconnect()
			} else {
				status.LastError = fmt.Sprintf("error:client:mqtt_client_creation:%s", err.Error()) // Standardized error code format
			}
		} else {
			status.LastError = fmt.Sprintf("error:metrics:initialization:%s", err.Error()) // Standardized error code format
		}
	} else if mqttConfig.Enabled {
		// If control channel is not available but MQTT is enabled,
		// we can create a temporary client to check connection status
		metrics, err := telemetry.NewMetrics()
		if err == nil {
			tempClient, err := mqtt.NewClient(c.Settings, metrics)
			if err == nil {
				// Use a short timeout to check connection
				testCtx, cancel := context.WithTimeout(ctx.Request().Context(), 3*time.Second)
				defer cancel()

				// Try to connect and set status based on result
				err = tempClient.Connect(testCtx)
				status.Connected = err == nil && tempClient.IsConnected()

				if err != nil {
					status.LastError = fmt.Sprintf("error:connection:mqtt_broker:%s", err.Error()) // Standardized error code format
				}

				// Disconnect the temporary client
				tempClient.Disconnect()
			} else {
				status.LastError = fmt.Sprintf("error:client:mqtt_client_creation:%s", err.Error()) // Standardized error code format
			}
		} else {
			status.LastError = fmt.Sprintf("error:metrics:initialization:%s", err.Error()) // Standardized error code format
		}
	}

	return ctx.JSON(http.StatusOK, status)
}

// GetBirdWeatherStatus handles GET /api/v2/integrations/birdweather/status
func (c *Controller) GetBirdWeatherStatus(ctx echo.Context) error {
	// Get BirdWeather configuration from settings
	bwConfig := c.Settings.Realtime.Birdweather

	// Prepare status response
	status := BirdWeatherStatus{
		Enabled:          bwConfig.Enabled,
		StationID:        bwConfig.ID,
		Threshold:        bwConfig.Threshold,
		LocationAccuracy: bwConfig.LocationAccuracy,
	}

	// For now, we just return the configuration status
	// In the future, we could add checks for client status here

	return ctx.JSON(http.StatusOK, status)
}

// TestMQTTConnection handles POST /api/v2/integrations/mqtt/test
func (c *Controller) TestMQTTConnection(ctx echo.Context) error {
	// Get MQTT configuration from settings
	mqttConfig := c.Settings.Realtime.MQTT

	if !mqttConfig.Enabled {
		return ctx.JSON(http.StatusOK, MQTTTestResult{
			Success: false,
			Message: "MQTT is not enabled in settings",
		})
	}

	// Validate MQTT configuration
	if mqttConfig.Broker == "" {
		return ctx.JSON(http.StatusBadRequest, MQTTTestResult{
			Success: false,
			Message: "MQTT broker not configured",
		})
	}

	// Create new metrics instance for the test
	metrics, err := telemetry.NewMetrics()
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, MQTTTestResult{
			Success: false,
			Message: fmt.Sprintf("Failed to create metrics for MQTT test: %v", err),
		})
	}

	// Create test MQTT client with the current configuration
	client, err := mqtt.NewClient(c.Settings, metrics)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, MQTTTestResult{
			Success: false,
			Message: fmt.Sprintf("Failed to create MQTT client: %v", err),
		})
	}

	// Prepare for testing
	ctx.Response().Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	ctx.Response().WriteHeader(http.StatusOK)

	// Channel for test results
	resultChan := make(chan mqtt.TestResult)

	// Create a done channel to signal when the client disconnects
	doneChan := make(chan struct{})

	// Use sync.Once to ensure doneChan is closed exactly once
	var closeOnce sync.Once
	// Helper function to safely close the doneChan
	safeDoneClose := func() {
		closeOnce.Do(func() {
			close(doneChan)
		})
	}

	// Mutex for safe writing to response
	var writeMu sync.Mutex

	// Create context with timeout that also gets cancelled if HTTP client disconnects
	httpCtx := ctx.Request().Context()
	testCtx, cancel := context.WithTimeout(httpCtx, 20*time.Second)
	defer cancel()

	// Run the test in a goroutine
	go func() {
		defer close(resultChan)
		startTime := time.Now()

		// Start the test
		client.TestConnection(testCtx, resultChan)

		// Calculate elapsed time
		elapsedTime := time.Since(startTime).Milliseconds()

		// Disconnect client when done
		client.Disconnect()

		// Send final result with elapsed time if the client is still connected
		select {
		case <-doneChan:
			// HTTP client has disconnected, no need to send final result
			c.Debug("HTTP client disconnected, skipping final result")
		case <-testCtx.Done():
			// Test timed out or was cancelled
			c.Debug("Test context cancelled: %v", testCtx.Err())
		default:
			// Still connected, send final result
			writeMu.Lock()
			defer writeMu.Unlock()

			// Format final response
			finalResult := map[string]interface{}{
				"elapsed_time_ms": elapsedTime,
				"state":           "completed",
			}

			// Write final result to response if possible
			if err := c.writeJSONResponse(ctx, finalResult); err != nil {
				c.logger.Printf("Error writing final MQTT test result: %v", err)
			}
		}
	}()

	// Feed streaming results to client
	encoder := json.NewEncoder(ctx.Response())

	// Stream results to client until done
	for result := range resultChan {
		writeMu.Lock()
		if err := encoder.Encode(result); err != nil {
			c.logger.Printf("Error encoding MQTT test result: %v", err)
			writeMu.Unlock()

			// Signal that the HTTP client has disconnected using sync.Once
			safeDoneClose()

			// Cancel the test context to stop ongoing tests
			cancel()
			return nil
		}
		ctx.Response().Flush()
		writeMu.Unlock()

		// Check if HTTP context is done (client disconnected)
		select {
		case <-httpCtx.Done():
			c.Debug("HTTP client disconnected during test")
			// Use sync.Once to safely close the channel
			safeDoneClose()
			cancel() // Cancel the test context
			return nil
		default:
			// Continue processing
		}
	}

	return nil
}

// TestBirdWeatherConnection handles POST /api/v2/integrations/birdweather/test
func (c *Controller) TestBirdWeatherConnection(ctx echo.Context) error {
	// Get BirdWeather configuration from settings
	bwConfig := c.Settings.Realtime.Birdweather

	if !bwConfig.Enabled {
		return ctx.JSON(http.StatusOK, map[string]interface{}{
			"success": false,
			"message": "BirdWeather integration is not enabled in settings",
			"state":   "failed",
		})
	}

	// Validate BirdWeather configuration
	if bwConfig.ID == "" {
		return ctx.JSON(http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"message": "BirdWeather station ID not configured",
			"state":   "failed",
		})
	}

	// Create test BirdWeather client with the current configuration
	client, err := birdweather.New(c.Settings)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"message": fmt.Sprintf("Failed to create BirdWeather client: %v", err),
			"state":   "failed",
		})
	}

	// Prepare for testing
	ctx.Response().Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	ctx.Response().WriteHeader(http.StatusOK)

	// Channel for test results
	resultChan := make(chan birdweather.TestResult)

	// Create a done channel to signal when the client disconnects
	doneChan := make(chan struct{})

	// Use sync.Once to ensure doneChan is closed exactly once
	var closeOnce sync.Once
	// Helper function to safely close the doneChan
	safeDoneClose := func() {
		closeOnce.Do(func() {
			close(doneChan)
		})
	}

	// Mutex for safe writing to response
	var writeMu sync.Mutex

	// Create context with timeout that also gets cancelled if HTTP client disconnects
	httpCtx := ctx.Request().Context()
	testCtx, cancel := context.WithTimeout(httpCtx, 30*time.Second)
	defer cancel()

	// Run the test in a goroutine
	go func() {
		defer close(resultChan)
		startTime := time.Now()

		// Start the test
		client.TestConnection(testCtx, resultChan)

		// Calculate elapsed time
		elapsedTime := time.Since(startTime).Milliseconds()

		// Clean up client resources
		client.Close()

		// Send final result with elapsed time if the client is still connected
		select {
		case <-doneChan:
			// HTTP client has disconnected, no need to send final result
			c.Debug("HTTP client disconnected, skipping final result")
		case <-testCtx.Done():
			// Test timed out or was cancelled
			c.Debug("Test context cancelled: %v", testCtx.Err())
		default:
			// Still connected, send final result
			writeMu.Lock()
			defer writeMu.Unlock()

			// Format final response
			finalResult := map[string]interface{}{
				"elapsed_time_ms": elapsedTime,
				"state":           "completed",
			}

			// Write final result to response if possible
			if err := c.writeJSONResponse(ctx, finalResult); err != nil {
				c.logger.Printf("Error writing final BirdWeather test result: %v", err)
			}
		}
	}()

	// Feed streaming results to client
	encoder := json.NewEncoder(ctx.Response())

	// Stream results to client until done
	for result := range resultChan {
		writeMu.Lock()
		if err := encoder.Encode(result); err != nil {
			c.logger.Printf("Error encoding BirdWeather test result: %v", err)
			writeMu.Unlock()

			// Signal that the HTTP client has disconnected using sync.Once
			safeDoneClose()

			// Cancel the test context to stop ongoing tests
			cancel()
			return nil
		}
		ctx.Response().Flush()
		writeMu.Unlock()

		// Check if HTTP context is done (client disconnected)
		select {
		case <-httpCtx.Done():
			c.Debug("HTTP client disconnected during test")
			// Use sync.Once to safely close the channel
			safeDoneClose()
			cancel() // Cancel the test context
			return nil
		default:
			// Continue processing
		}
	}

	return nil
}

// writeJSONResponse writes a JSON response to the client
// NOTE: For most cases, consider using Echo's built-in ctx.JSON(httpStatus, data) instead
// This function is primarily useful for streaming or special encoding scenarios
func (c *Controller) writeJSONResponse(ctx echo.Context, data interface{}) error {
	encoder := json.NewEncoder(ctx.Response())
	return encoder.Encode(data)
}
