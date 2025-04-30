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
	if c.apiLogger != nil {
		c.apiLogger.Info("Initializing integrations routes")
	}

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

	if c.apiLogger != nil {
		c.apiLogger.Info("Integrations routes initialized successfully")
	}
}

// GetMQTTStatus handles GET /api/v2/integrations/mqtt/status
func (c *Controller) GetMQTTStatus(ctx echo.Context) error {
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path
	if c.apiLogger != nil {
		c.apiLogger.Info("Getting MQTT status", "path", path, "ip", ip)
	}

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
		if c.apiLogger != nil {
			c.apiLogger.Info("MQTT is disabled, returning status", "path", path, "ip", ip)
		}
		return ctx.JSON(http.StatusOK, status)
	}

	// Check connection status using a temporary client
	if c.apiLogger != nil {
		c.apiLogger.Debug("Checking MQTT connection status", "path", path, "ip", ip)
	}
	connected, checkErr := c.checkMQTTConnectionStatus(ctx.Request().Context())
	status.Connected = connected
	if checkErr != "" {
		status.LastError = checkErr
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieved MQTT status successfully",
			"connected", status.Connected,
			"broker", status.Broker,
			"last_error", status.LastError,
			"path", path,
			"ip", ip,
		)
	}
	return ctx.JSON(http.StatusOK, status)
}

// checkMQTTConnectionStatus attempts to connect to the MQTT broker using a temporary client
// to determine the current connection status.
// Returns true if connected, false otherwise, along with any error message encountered.
func (c *Controller) checkMQTTConnectionStatus(parentCtx context.Context) (connected bool, lastError string) {
	metrics, err := telemetry.NewMetrics()
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to create metrics for temporary MQTT client", "error", err)
		}
		return false, fmt.Sprintf("error:metrics:initialization:%s", err.Error())
	}

	tempClient, err := mqtt.NewClient(c.Settings, metrics)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to create temporary MQTT client for status check", "error", err)
		}
		return false, fmt.Sprintf("error:client:mqtt_client_creation:%s", err.Error())
	}
	defer tempClient.Disconnect() // Ensure temporary client is disconnected

	// Use a short timeout for the connection attempt
	connectCtx, cancel := context.WithTimeout(parentCtx, 3*time.Second)
	defer cancel()

	// Try to connect
	err = tempClient.Connect(connectCtx)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Warn("Temporary MQTT client connection failed during status check", "error", err, "broker", c.Settings.Realtime.MQTT.Broker)
		}
		return false, fmt.Sprintf("error:connection:mqtt_broker:%s", err.Error())
	}

	// Check if genuinely connected
	if !tempClient.IsConnected() {
		if c.apiLogger != nil {
			c.apiLogger.Warn("Temporary MQTT client connected but IsConnected() returned false", "broker", c.Settings.Realtime.MQTT.Broker)
		}
		// Consider this a failure for status purposes, though connection might be flapping
		return false, "error:connection:mqtt_connection_unstable"
	}

	if c.apiLogger != nil {
		c.apiLogger.Debug("Temporary MQTT client connected successfully for status check", "broker", c.Settings.Realtime.MQTT.Broker)
	}
	return true, "" // Connected successfully
}

// GetBirdWeatherStatus handles GET /api/v2/integrations/birdweather/status
func (c *Controller) GetBirdWeatherStatus(ctx echo.Context) error {
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path
	if c.apiLogger != nil {
		c.apiLogger.Info("Getting BirdWeather status", "path", path, "ip", ip)
	}

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
	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieved BirdWeather status successfully",
			"enabled", status.Enabled,
			"station_id", status.StationID,
			"threshold", status.Threshold,
			"path", path,
			"ip", ip,
		)
	}

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
