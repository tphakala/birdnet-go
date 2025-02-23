// testing.go provides MQTT connection and functionality testing capabilities
package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// Test configuration for artificial delays and failures
// for UI testing
var (
	// Set to true to enable artificial delays and random failures
	TestMode = false
	// Internal flag to enable random failures (for testing UI behavior)
	randomFailureMode = false
	// Probability of failure for each stage (0.0 - 1.0)
	FailureProbability = 0.5
	// Min and max artificial delay in milliseconds
	MinDelay = 500
	MaxDelay = 3000
)

// simulateDelay adds an artificial delay
func simulateDelay() {
	if !TestMode {
		return
	}
	delay := rand.Intn(MaxDelay-MinDelay) + MinDelay
	time.Sleep(time.Duration(delay) * time.Millisecond)
}

// simulateFailure returns true if the test should fail
func simulateFailure() bool {
	if !TestMode || !randomFailureMode {
		return false
	}
	return rand.Float64() < FailureProbability
}

// TestResult represents the result of an MQTT test
type TestResult struct {
	Success    bool   `json:"success"`
	Stage      string `json:"stage"`
	Message    string `json:"message"`
	Error      string `json:"error,omitempty"`
	IsProgress bool   `json:"isProgress,omitempty"`
	State      string `json:"state,omitempty"`     // Current state: running, completed, failed, timeout
	Timestamp  string `json:"timestamp,omitempty"` // ISO8601 timestamp of the result
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

// isIPAddress checks if the given host is an IP address
func isIPAddress(host string) bool {
	// Remove protocol prefix if present
	if strings.Contains(host, "://") {
		parts := strings.Split(host, "://")
		if len(parts) != 2 {
			return false
		}
		// Only allow mqtt and tcp protocols
		if parts[0] != "mqtt" && parts[0] != "tcp" {
			return false
		}
		host = parts[1]
	}

	// Handle IPv6 addresses with brackets
	if strings.HasPrefix(host, "[") {
		// Extract the IPv6 address from within brackets
		end := strings.LastIndex(host, "]")
		if end == -1 {
			return false // Malformed IPv6 address with opening bracket but no closing bracket
		}
		// Extract the address without brackets
		host = host[1:end]
	} else if strings.Contains(host, ":") {
		// If it contains a colon but no brackets, it could be either:
		// 1. An IPv4 address with port (e.g. "192.168.1.1:1883")
		// 2. A raw IPv6 address (e.g. "::1" or "2001:db8::1")

		// If it has more than 2 colons, assume it's IPv6
		if strings.Count(host, ":") <= 1 {
			// Likely IPv4 with port, remove the port
			host = strings.Split(host, ":")[0]
		}
		// Otherwise leave it as is for IPv6 parsing
	}

	// Try to parse as IP address
	ip := net.ParseIP(host)
	return ip != nil
}

// TestConnection performs a multi-stage test of the MQTT connection and functionality
func (c *client) TestConnection(ctx context.Context, resultChan chan<- TestResult) {
	// Helper function to send a result
	sendResult := func(result TestResult) {
		// Mark progress messages
		result.IsProgress = strings.Contains(strings.ToLower(result.Message), "running") ||
			strings.Contains(strings.ToLower(result.Message), "testing") ||
			strings.Contains(strings.ToLower(result.Message), "establishing") ||
			strings.Contains(strings.ToLower(result.Message), "initializing")

		// Set state based on result
		switch {
		case result.State != "":
			// Keep existing state if explicitly set
		case result.Error != "":
			result.State = "failed"
			result.Success = false
			result.IsProgress = false
		case result.IsProgress:
			result.State = "running"
		case result.Success:
			result.State = "completed"
		case strings.Contains(strings.ToLower(result.Error), "timeout") ||
			strings.Contains(strings.ToLower(result.Error), "deadline exceeded"):
			result.State = "timeout"
		default:
			result.State = "failed"
		}

		// Add timestamp
		result.Timestamp = time.Now().Format(time.RFC3339)

		// Log the result with emoji
		emoji := "❌"
		if result.Success {
			emoji = "✅"
		}

		// Format the log message
		logMsg := result.Message
		if !result.Success && result.Error != "" {
			logMsg = fmt.Sprintf("%s: %s", result.Message, result.Error)
		}
		log.Printf("%s %s: %s", emoji, result.Stage, logMsg)

		// Send result to channel
		select {
		case <-ctx.Done():
			return
		case resultChan <- result:
		}
	}

	// Check context before starting
	if err := ctx.Err(); err != nil {
		sendResult(TestResult{
			Success: false,
			Stage:   "Test Setup",
			Message: "Test cancelled",
			Error:   err.Error(),
			State:   "timeout",
		})
		return
	}

	// Extract broker host for testing
	brokerHost := extractHost(c.config.Broker)
	isIP := isIPAddress(brokerHost)

	// Stage 1: DNS Resolution (skip if IP address)
	if !isIP {
		dnsCtx, dnsCancel := context.WithTimeout(ctx, 5*time.Second)
		defer dnsCancel()

		sendResult(TestResult{
			Success: true,
			Stage:   "DNS Resolution",
			Message: fmt.Sprintf("Running DNS resolution test for %s...", brokerHost),
		})

		if result := testDNSResolution(dnsCtx, brokerHost); !result.Success {
			sendResult(result)
			return
		}
		sendResult(TestResult{
			Success: true,
			Stage:   DNSResolution.String(),
			Message: fmt.Sprintf("Successfully resolved hostname: %s", brokerHost),
		})
	}

	// Stage 2: TCP Connection
	tcpCtx, tcpCancel := context.WithTimeout(ctx, 5*time.Second)
	defer tcpCancel()

	// First send the "running test" message
	sendResult(TestResult{
		Success: true,
		Stage:   TCPConnection.String(),
		Message: fmt.Sprintf("Running TCP connection test for %s...", c.config.Broker),
	})

	// Then perform the actual test
	if result := testTCPConnection(tcpCtx, c.config.Broker); !result.Success {
		sendResult(result)
		return
	}

	// Finally send success message
	sendResult(TestResult{
		Success: true,
		Stage:   TCPConnection.String(),
		Message: fmt.Sprintf("Successfully established TCP connection to %s", c.config.Broker),
	})

	// Stage 3: MQTT Connection
	if !c.IsConnected() {
		mqttCtx, mqttCancel := context.WithTimeout(ctx, 10*time.Second)
		defer mqttCancel()

		sendResult(TestResult{
			Success: true,
			Stage:   "MQTT Connection",
			Message: fmt.Sprintf("Establishing MQTT connection to %s...", c.config.Broker),
		})

		simulateDelay()

		if simulateFailure() {
			sendResult(TestResult{
				Success: false,
				Stage:   MQTTConnection.String(),
				Error:   "simulated MQTT connection failure",
				Message: "Failed to connect to MQTT broker",
			})
			return
		}

		if err := c.Connect(mqttCtx); err != nil {
			sendResult(TestResult{
				Success: false,
				Stage:   MQTTConnection.String(),
				Error:   err.Error(),
				Message: "Failed to connect to MQTT broker",
			})
			return
		}
	}
	sendResult(TestResult{
		Success: true,
		Stage:   MQTTConnection.String(),
		Message: fmt.Sprintf("Successfully connected to MQTT broker: %s", c.config.Broker),
	})

	// Stage 4: Test Message Publishing
	pubCtx, pubCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pubCancel()

	sendResult(TestResult{
		Success: true,
		Stage:   "Message Publishing",
		Message: fmt.Sprintf("Testing message publishing to topic: %s", constructTestTopic(c.config.Topic)),
	})

	if result := c.publishTestMessage(pubCtx); !result.Success {
		sendResult(result)
		return
	}
	sendResult(TestResult{
		Success: true,
		Stage:   MessagePublish.String(),
		Message: "Successfully published test message",
	})
}

// publishTestMessage publishes a test message using a mock Whooper Swan detection
func (c *client) publishTestMessage(ctx context.Context) TestResult {
	simulateDelay()

	if simulateFailure() {
		return TestResult{
			Success: false,
			Stage:   MessagePublish.String(),
			Error:   "simulated message publishing failure",
			Message: "Failed to publish test message",
		}
	}

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
func testDNSResolution(ctx context.Context, host string) TestResult {
	simulateDelay()

	if simulateFailure() {
		return TestResult{
			Success: false,
			Stage:   DNSResolution.String(),
			Error:   "simulated DNS resolution failure",
			Message: fmt.Sprintf("Failed to resolve hostname: %s", host),
		}
	}

	// Create a channel for the DNS lookup result
	resultChan := make(chan error, 1)

	go func() {
		_, err := net.LookupHost(host)
		resultChan <- err
	}()

	// Wait for either the context to be done or the lookup to complete
	select {
	case <-ctx.Done():
		return TestResult{
			Success: false,
			Stage:   DNSResolution.String(),
			Error:   "DNS resolution timeout",
			Message: fmt.Sprintf("DNS resolution for %s timed out", host),
		}
	case err := <-resultChan:
		if err != nil {
			return TestResult{
				Success: false,
				Stage:   DNSResolution.String(),
				Error:   err.Error(),
				Message: fmt.Sprintf("Failed to resolve hostname: %s", host),
			}
		}
	}

	return TestResult{
		Success: true,
		Stage:   DNSResolution.String(),
		Message: fmt.Sprintf("Successfully resolved hostname: %s", host),
	}
}

// testTCPConnection tests TCP connection to the broker
func testTCPConnection(ctx context.Context, broker string) TestResult {
	simulateDelay()

	if simulateFailure() {
		return TestResult{
			Success: false,
			Stage:   TCPConnection.String(),
			Error:   "simulated TCP connection failure",
			Message: fmt.Sprintf("Failed to establish TCP connection to %s", broker),
		}
	}

	// Extract host and port from broker URL
	hostPort := extractHostPort(broker)

	// Create a channel for the connection result
	resultChan := make(chan error, 1)

	go func() {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", hostPort)
		if err == nil {
			conn.Close()
		}
		resultChan <- err
	}()

	// Wait for either the context to be done or the connection to complete
	select {
	case <-ctx.Done():
		return TestResult{
			Success: false,
			Stage:   TCPConnection.String(),
			Error:   "TCP connection timeout",
			Message: fmt.Sprintf("TCP connection to %s timed out", hostPort),
		}
	case err := <-resultChan:
		if err != nil {
			return TestResult{
				Success: false,
				Stage:   TCPConnection.String(),
				Error:   err.Error(),
				Message: fmt.Sprintf("Failed to establish TCP connection to %s", hostPort),
			}
		}
	}

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
		if len(parts) != 2 {
			return broker
		}
		broker = parts[1]
	}

	// Handle IPv6 addresses with brackets
	if strings.HasPrefix(broker, "[") {
		end := strings.LastIndex(broker, "]")
		if end == -1 {
			return broker // Malformed IPv6 address
		}
		return broker[1:end] // Return without brackets
	}

	// For IPv4 or hostname, remove port if present
	if strings.Count(broker, ":") <= 1 {
		if i := strings.LastIndex(broker, ":"); i != -1 {
			return broker[:i]
		}
	}
	// For IPv6 without brackets or no port, return as is
	return broker
}

// extractHostPort extracts host:port from broker URL
func extractHostPort(broker string) string {
	// Remove protocol prefix if present
	if strings.Contains(broker, "://") {
		parts := strings.Split(broker, "://")
		if len(parts) != 2 {
			return broker
		}
		broker = parts[1]
	}

	// Handle IPv6 addresses
	if strings.HasPrefix(broker, "[") {
		// IPv6 with port
		if i := strings.LastIndex(broker, "]:"); i != -1 {
			return broker
		}
		// IPv6 without port
		if strings.HasSuffix(broker, "]") {
			return broker[:len(broker)-1] + "]:1883"
		}
		// Malformed IPv6
		return broker
	}

	// Check if this might be a raw IPv6 address
	if strings.Count(broker, ":") > 1 {
		// Add brackets and port
		return "[" + broker + "]:1883"
	}

	// IPv4 or hostname
	if !strings.Contains(broker, ":") {
		return broker + ":1883"
	}

	return broker
}
