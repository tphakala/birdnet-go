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
	// In a real implementation, this would check the actual MQTT connection status
	// For now, we'll return the configuration status
	mqttConfig := c.Settings.Realtime.MQTT

	status := MQTTStatus{
		Connected: false, // Default to not connected
		Broker:    mqttConfig.Broker,
		Topic:     mqttConfig.Topic,
		ClientID:  c.Settings.Main.Name, // Use the application name as client ID
	}

	// Check if there's an active MQTT client we can query
	// This would depend on how the MQTT client is implemented and accessible
	if c.controlChan != nil {
		// Request MQTT status from the controller
		// This is a placeholder - actual implementation would depend on your architecture
		c.Debug("Requesting MQTT status check")
		// TODO: Implement actual status check
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

	// Mutex for safe writing to response
	var writeMu sync.Mutex

	// Run the test in a goroutine
	go func() {
		startTime := time.Now()

		// Create context with timeout
		testCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		// Start the test
		client.TestConnection(testCtx, resultChan)

		// Calculate elapsed time
		elapsedTime := time.Since(startTime).Milliseconds()

		// Disconnect client when done
		client.Disconnect()

		// Send final result with elapsed time
		writeMu.Lock()
		defer writeMu.Unlock()

		// Format final response (this is written in TestConnection method)
		finalResult := map[string]interface{}{
			"elapsed_time_ms": elapsedTime,
			"state":           "completed",
		}

		// Write final result to response
		if err := c.writeJSONResponse(ctx, finalResult); err != nil {
			c.logger.Printf("Error writing final MQTT test result: %v", err)
		}
	}()

	// Feed streaming results to client
	encoder := json.NewEncoder(ctx.Response())

	// Stream results to client
	for result := range resultChan {
		writeMu.Lock()
		if err := encoder.Encode(result); err != nil {
			c.logger.Printf("Error encoding MQTT test result: %v", err)
		}
		ctx.Response().Flush()
		writeMu.Unlock()
	}

	return nil
}

// writeJSONResponse writes a JSON response to the client
func (c *Controller) writeJSONResponse(ctx echo.Context, data interface{}) error {
	encoder := json.NewEncoder(ctx.Response())
	return encoder.Encode(data)
}
