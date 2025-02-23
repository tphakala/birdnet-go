// mqtt.go provides HTTP handlers for MQTT-related functionality
package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/mqtt"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// TestMQTT handles requests to test MQTT connectivity and functionality
func (h *Handlers) TestMQTT(c echo.Context) error {
	// Define a struct for the test configuration
	type TestConfig struct {
		Enabled  bool   `json:"enabled"`
		Broker   string `json:"broker"`
		Topic    string `json:"topic"`
		Username string `json:"username"`
		Password string `json:"password"`
	}

	var testConfig TestConfig
	var settings *conf.Settings

	// If this is a POST request, use the provided test configuration
	if c.Request().Method == "POST" {
		if err := c.Bind(&testConfig); err != nil {
			return h.NewHandlerError(err, "Invalid test configuration", http.StatusBadRequest)
		}

		// Create temporary settings for the test
		settings = &conf.Settings{
			Realtime: conf.RealtimeSettings{
				MQTT: conf.MQTTSettings{
					Enabled:  testConfig.Enabled,
					Broker:   testConfig.Broker,
					Topic:    testConfig.Topic,
					Username: testConfig.Username,
					Password: testConfig.Password,
				},
			},
		}
	} else {
		// For GET requests, use the current settings
		settings = h.Settings
	}

	// Check if MQTT is enabled
	if !settings.Realtime.MQTT.Enabled {
		return h.NewHandlerError(
			nil,
			"MQTT is not enabled in settings",
			http.StatusBadRequest,
		)
	}

	// Create a temporary MQTT client for testing
	metrics, err := telemetry.NewMetrics()
	if err != nil {
		return h.NewHandlerError(err, "Failed to create metrics", http.StatusInternalServerError)
	}

	mqttClient, err := mqtt.NewClient(settings, metrics)
	if err != nil {
		return h.NewHandlerError(err, "Failed to create MQTT client", http.StatusInternalServerError)
	}

	// Set the control channel for the MQTT client
	mqttClient.SetControlChannel(h.controlChan)

	// Create context with timeout for the test
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Run the test
	results := mqttClient.TestConnection(ctx)

	// Clean up
	mqttClient.Disconnect()

	// Enhance test results with troubleshooting suggestions
	enhancedResults := enhanceTestResults(results, settings.Realtime.MQTT.Broker)

	return c.JSON(http.StatusOK, enhancedResults)
}

// enhanceTestResults adds helpful troubleshooting suggestions to test results
func enhanceTestResults(results []mqtt.TestResult, broker string) []mqtt.TestResult {
	for i := range results {
		if !results[i].Success {
			hint := generateTroubleshootingHint(results[i], broker)
			if hint != "" {
				// Format message with each component on its own line
				results[i].Message = fmt.Sprintf("%s\n\n%s\n\n%s",
					results[i].Message,
					results[i].Error,
					hint)
				results[i].Error = "" // Clear the error since we included it in the message
			}
		}
	}
	return results
}

// generateTroubleshootingHint provides context-specific troubleshooting suggestions
func generateTroubleshootingHint(result mqtt.TestResult, broker string) string {
	switch result.Stage {
	case "DNS Resolution":
		if strings.Contains(result.Error, "no such host") {
			return "Please verify that the broker hostname is correct."
		}
		return "Please check if the broker address is correctly formatted."

	case "TCP Connection":
		if strings.Contains(result.Error, "connection refused") {
			if strings.Contains(broker, "localhost") || strings.Contains(broker, "127.0.0.1") {
				return "The MQTT broker service does not appear to be running."
			}
			return "Please check:\n" +
				"1. The broker service is running\n" +
				"2. The port number is correct\n" +
				"3. No firewall rules are blocking the connection"
		}
		if strings.Contains(result.Error, "i/o timeout") {
			return "Please check:\n" +
				"1. The broker address and port are correct\n" +
				"2. The broker is accessible from your network\n" +
				"3. No firewall rules are blocking the connection"
		}
		return "Please verify the broker is running and accessible from your network."

	case "MQTT Connection":
		if strings.Contains(strings.ToLower(result.Error), "auth") {
			return "Please verify your username and password are correct."
		}
		if strings.Contains(result.Error, "Bad Connection") {
			return "Please check:\n" +
				"1. Your credentials are correct\n" +
				"2. The broker is accepting connections\n" +
				"3. The correct protocol (mqtt:// or mqtts://) is being used"
		}
		return "Please verify your broker settings and credentials."

	case "Message Publishing":
		return "Please check:\n" +
			"1. You have publish permissions on the topic\n" +
			"2. The topic format is valid\n" +
			"3. The broker allows publishing"
	}

	return ""
}
