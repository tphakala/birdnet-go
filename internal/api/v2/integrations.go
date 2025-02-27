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
	"github.com/tphakala/birdnet-go/internal/mqtt"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// MQTTStatus represents the current status of the MQTT connection
type MQTTStatus struct {
	Connected bool   `json:"connected"`
	Broker    string `json:"broker"`
	Topic     string `json:"topic"`
	ClientID  string `json:"client_id"`
	LastError string `json:"last_error,omitempty"`
}

// MQTTTestResult represents the result of an MQTT connection test
type MQTTTestResult struct {
	Success     bool   `json:"success"`
	Message     string `json:"message"`
	ElapsedTime int64  `json:"elapsed_time_ms,omitempty"`
}

// initIntegrationsRoutes registers all integration-related API endpoints
func (c *Controller) initIntegrationsRoutes() {
	// Create integrations API group with auth middleware
	integrationsGroup := c.Group.Group("/integrations", c.AuthMiddleware)

	// MQTT routes
	mqttGroup := integrationsGroup.Group("/mqtt")
	mqttGroup.GET("/status", c.GetMQTTStatus)
	mqttGroup.POST("/test", c.TestMQTTConnection)

	// Other integration routes could be added here:
	// - BirdWeather
	// - Weather APIs
	// - External media storage
}

// GetMQTTStatus handles GET /api/v2/integrations/mqtt/status
func (c *Controller) GetMQTTStatus(ctx echo.Context) error {
	// Get MQTT configuration from settings
	mqttConfig := c.Settings.Realtime.MQTT

	status := MQTTStatus{
		Connected: false, // Default to not connected
		Broker:    mqttConfig.Broker,
		Topic:     mqttConfig.Topic,
		ClientID:  c.Settings.Main.Name, // Use the application name as client ID
	}

	// Check if there's an active MQTT client we can query
	if c.controlChan != nil {
		c.Debug("Requesting MQTT status check")

		// Send a status request through the control channel
		// The actual message format should match what your control monitor expects
		statusReqChan := make(chan bool, 1)

		// NOTE: There appears to be an issue here - statusReqChan is created locally
		// but there's no mechanism visible in this function to write to it.
		// This means the select below may always timeout after 2 seconds.
		//
		// TODO: Ensure that:
		// 1. The component handling "mqtt:status" messages has access to this channel
		// 2. That component writes the connection status to statusReqChan
		// 3. Consider passing statusReqChan to the control system or using a response channel pattern

		// We assume the controller has a method to handle "mqtt:status" commands
		// and will respond with the connection status
		select {
		case c.controlChan <- "mqtt:status":
			// Wait for response with timeout
			select {
			case connected := <-statusReqChan:
				status.Connected = connected
			case <-time.After(2 * time.Second):
				c.logger.Printf("Timeout waiting for MQTT status response")
				status.LastError = "Timeout waiting for status"
			}
		default:
			// Channel is full or blocked
			c.logger.Printf("Control channel is not accepting messages")
			status.LastError = "Control system busy"
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
					status.LastError = err.Error()
				}

				// Disconnect the temporary client
				tempClient.Disconnect()
			} else {
				status.LastError = err.Error()
			}
		}
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

// writeJSONResponse writes a JSON response to the client
func (c *Controller) writeJSONResponse(ctx echo.Context, data interface{}) error {
	encoder := json.NewEncoder(ctx.Response())
	return encoder.Encode(data)
}
