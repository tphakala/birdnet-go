// testing.go provides MQTT connection and functionality testing capabilities
package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestResult represents the result of an MQTT test
type TestResult struct {
	Success bool   `json:"success"`
	Stage   string `json:"stage"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// TestStage represents a stage in the MQTT test process
type TestStage int

const (
	DNSResolution TestStage = iota
	TCPConnection
	MQTTConnection
	MessagePublish
)

// String returns the string representation of a test stage
func (s TestStage) String() string {
	switch s {
	case DNSResolution:
		return "DNS Resolution"
	case TCPConnection:
		return "TCP Connection"
	case MQTTConnection:
		return "MQTT Connection"
	case MessagePublish:
		return "Message Publishing"
	default:
		return "Unknown Stage"
	}
}

// TestConnection performs a multi-stage test of the MQTT connection and functionality
func (c *client) TestConnection(ctx context.Context) []TestResult {
	var results []TestResult

	// Check if MQTT service is enabled and running in BirdNET-Go
	if !c.IsConnected() {
		// Try to connect first to ensure MQTT service is running
		if err := c.Connect(ctx); err != nil {
			// If connection fails, we need to reconfigure MQTT service
			results = append(results, TestResult{
				Success: false,
				Stage:   "Service Check",
				Message: "MQTT service not running, attempting to start...",
			})

			// Send reconfiguration signal through control channel
			if c.controlChan != nil {
				c.controlChan <- "reconfigure_mqtt"

				// Try to reconnect with retries
				maxRetries := 3
				retryDelay := 1 * time.Second

				for i := 0; i < maxRetries; i++ {
					time.Sleep(retryDelay)
					if err := c.Connect(ctx); err == nil {
						results = append(results, TestResult{
							Success: true,
							Stage:   "Service Start",
							Message: "Successfully started MQTT service",
						})
						break
					}
					retryDelay *= 2 // Exponential backoff
					if i == maxRetries-1 {
						results = append(results, TestResult{
							Success: false,
							Stage:   "Service Start",
							Error:   "Maximum retry attempts reached",
							Message: "Failed to start MQTT service",
						})
						return results
					}
				}
			} else {
				results = append(results, TestResult{
					Success: false,
					Stage:   "Service Start",
					Error:   "Control channel not available",
					Message: "Cannot start MQTT service automatically",
				})
				return results
			}
		}
	}

	// Stage 1: DNS Resolution
	brokerHost := extractHost(c.config.Broker)
	if result := testDNSResolution(brokerHost); !result.Success {
		return append(results, result)
	} else {
		results = append(results, result)
	}

	// Stage 2: TCP Connection
	if result := testTCPConnection(c.config.Broker); !result.Success {
		return append(results, result)
	} else {
		results = append(results, result)
	}

	// Stage 3: MQTT Connection
	if !c.IsConnected() {
		if err := c.Connect(ctx); err != nil {
			results = append(results, TestResult{
				Success: false,
				Stage:   MQTTConnection.String(),
				Error:   err.Error(),
				Message: "Failed to connect to MQTT broker",
			})
			return results
		}
	}
	results = append(results, TestResult{
		Success: true,
		Stage:   MQTTConnection.String(),
		Message: "Successfully connected to MQTT broker",
	})

	// Stage 4: Test Message Publishing
	if result := c.publishTestMessage(ctx); !result.Success {
		return append(results, result)
	} else {
		results = append(results, result)
	}

	return results
}

// publishTestMessage publishes a test message using a mock Whooper Swan detection
func (c *client) publishTestMessage(ctx context.Context) TestResult {
	// Create a mock detection for Whooper Swan
	mockNote := datastore.Note{
		Time:           time.Now().Format(time.RFC3339),
		CommonName:     "Whooper Swan",
		ScientificName: "Cygnus cygnus",
		Confidence:     0.95,
		Source:         "MQTT Test",
	}

	// Convert to JSON
	noteJson, err := json.Marshal(mockNote)
	if err != nil {
		return TestResult{
			Success: false,
			Stage:   MessagePublish.String(),
			Error:   err.Error(),
			Message: "Failed to create test message",
		}
	}

	// Construct test topic with proper handling of base topic
	testTopic := constructTestTopic(c.config.Topic)

	err = c.Publish(ctx, testTopic, string(noteJson))
	if err != nil {
		return TestResult{
			Success: false,
			Stage:   MessagePublish.String(),
			Error:   err.Error(),
			Message: "Failed to publish test message",
		}
	}

	return TestResult{
		Success: true,
		Stage:   MessagePublish.String(),
		Message: "Successfully published test message",
	}
}

// constructTestTopic creates a proper test topic path handling edge cases
func constructTestTopic(baseTopic string) string {
	// Remove trailing slashes
	baseTopic = strings.TrimRight(baseTopic, "/")

	// If base topic is empty, use a default
	if baseTopic == "" {
		return "birdnet-go/test"
	}

	return baseTopic + "/test"
}

// testDNSResolution tests DNS resolution for the broker hostname
func testDNSResolution(host string) TestResult {
	_, err := net.LookupHost(host)
	if err != nil {
		return TestResult{
			Success: false,
			Stage:   DNSResolution.String(),
			Error:   err.Error(),
			Message: fmt.Sprintf("Failed to resolve hostname: %s", host),
		}
	}
	return TestResult{
		Success: true,
		Stage:   DNSResolution.String(),
		Message: fmt.Sprintf("Successfully resolved hostname: %s", host),
	}
}

// testTCPConnection tests TCP connection to the broker
func testTCPConnection(broker string) TestResult {
	// Extract host and port from broker URL
	hostPort := extractHostPort(broker)

	conn, err := net.DialTimeout("tcp", hostPort, 5*time.Second)
	if err != nil {
		return TestResult{
			Success: false,
			Stage:   TCPConnection.String(),
			Error:   err.Error(),
			Message: fmt.Sprintf("Failed to establish TCP connection to %s", hostPort),
		}
	}
	defer conn.Close()

	return TestResult{
		Success: true,
		Stage:   TCPConnection.String(),
		Message: fmt.Sprintf("Successfully established TCP connection to %s", hostPort),
	}
}

// extractHost extracts the hostname from broker URL
func extractHost(broker string) string {
	// Remove protocol prefix if present
	if strings.Contains(broker, "://") {
		parts := strings.Split(broker, "://")
		broker = parts[1]
	}

	// Remove port if present
	if strings.Contains(broker, ":") {
		parts := strings.Split(broker, ":")
		broker = parts[0]
	}

	return broker
}

// extractHostPort extracts host:port from broker URL
func extractHostPort(broker string) string {
	// Remove protocol prefix if present
	if strings.Contains(broker, "://") {
		parts := strings.Split(broker, "://")
		broker = parts[1]
	}

	// If no port specified, use default MQTT port
	if !strings.Contains(broker, ":") {
		broker += ":1883"
	}

	return broker
}
