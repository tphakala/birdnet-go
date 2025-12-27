// testing.go provides MQTT connection and functionality testing capabilities
package mqtt

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// TestConfig encapsulates test configuration for artificial delays and failures
type TestConfig struct {
	// Set to true to enable artificial delays and random failures
	Enabled bool
	// Internal flag to enable random failures (for testing UI behavior)
	RandomFailureMode bool
	// Probability of failure for each stage (0.0 - 1.0)
	FailureProbability float64
	// Min and max artificial delay in milliseconds
	MinDelay int
	MaxDelay int
	// Thread-safe random number generator
	rng *rand.Rand
	mu  sync.Mutex
}

// Default test configuration instance
var testConfig = &TestConfig{
	Enabled:            false,
	RandomFailureMode:  false,
	FailureProbability: 0.5,
	MinDelay:           500,
	MaxDelay:           3000,
	rng:                rand.New(rand.NewSource(time.Now().UnixNano())), // #nosec G404 -- weak randomness acceptable for test utilities, not security-critical
}

// simulateDelay adds an artificial delay
func simulateDelay() {
	if !testConfig.Enabled {
		return
	}
	testConfig.mu.Lock()
	delay := testConfig.rng.Intn(testConfig.MaxDelay-testConfig.MinDelay) + testConfig.MinDelay
	testConfig.mu.Unlock()
	time.Sleep(time.Duration(delay) * time.Millisecond)
}

// simulateFailure returns true if the test should fail
func simulateFailure() bool {
	if !testConfig.Enabled || !testConfig.RandomFailureMode {
		return false
	}
	testConfig.mu.Lock()
	defer testConfig.mu.Unlock()
	return testConfig.rng.Float64() < testConfig.FailureProbability
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

// Timeout constants for various test stages
const (
	dnsTimeout  = 5 * time.Second
	tcpTimeout  = 5 * time.Second
	mqttTimeout = 10 * time.Second
	pubTimeout  = 12 * time.Second
)

// State constants for test results
const (
	stateRunning   = "running"
	stateCompleted = "completed"
	stateFailed    = "failed"
	stateTimeout   = "timeout"
)

// networkTest represents a generic network test function
type networkTest func(context.Context) error

// runNetworkTest executes a network test with proper timeout and cleanup
func runNetworkTest(ctx context.Context, stage TestStage, test networkTest) TestResult {
	// Add simulated delay if enabled
	simulateDelay()

	// Check for simulated failure
	if simulateFailure() {
		return TestResult{
			Success: false,
			Stage:   stage.String(),
			Error:   fmt.Sprintf("simulated %s failure", stage),
			Message: fmt.Sprintf("Failed to perform %s", stage),
		}
	}

	// Create buffered channel for test result
	resultChan := make(chan error, 1)

	// Run the test in a goroutine
	go func() {
		resultChan <- test(ctx)
	}()

	// Wait for either test completion or context cancellation
	select {
	case <-ctx.Done():
		return TestResult{
			Success: false,
			Stage:   stage.String(),
			Error:   "operation timeout",
			Message: fmt.Sprintf("%s operation timed out", stage),
		}
	case err := <-resultChan:
		if err == nil {
			return TestResult{
				Success: true,
				Stage:   stage.String(),
				Message: fmt.Sprintf("Successfully completed %s", stage),
			}
		}
		// Extract error details for better categorization
		errorCategory := categorizeError(err)
		return TestResult{
			Success: false,
			Stage:   stage.String(),
			Error:   err.Error(),
			Message: fmt.Sprintf("Failed to perform %s (%s)", stage, errorCategory),
		}
	}
}

// categorizeError determines the error category based on error type and content
func categorizeError(err error) string {
	// Check if it's already an enhanced error
	var enhancedErr *errors.EnhancedError
	if errors.As(err, &enhancedErr) {
		return enhancedErr.GetCategory()
	}

	// Categorize based on error content
	errorLower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(errorLower, "dns"):
		return "network"
	case strings.Contains(errorLower, "connection refused"), strings.Contains(errorLower, "tcp"):
		return "network"
	case strings.Contains(errorLower, "mqtt"), strings.Contains(errorLower, "auth"), strings.Contains(errorLower, "unauthorized"):
		return "mqtt-connection"
	case strings.Contains(errorLower, "publish"), strings.Contains(errorLower, "timeout"):
		return "mqtt-publish"
	default:
		return "generic"
	}
}

// testDNSStage performs DNS resolution testing
func (c *client) testDNSStage(ctx context.Context, brokerHost string) TestResult {
	dnsCtx, dnsCancel := context.WithTimeout(ctx, dnsTimeout)
	defer dnsCancel()

	return runNetworkTest(dnsCtx, DNSResolution, func(ctx context.Context) error {
		_, err := net.DefaultResolver.LookupHost(ctx, brokerHost)
		if err != nil {
			// Enhance DNS resolution errors
			enhancedErr := errors.New(err).
				Component("mqtt").
				Category(errors.CategoryNetwork).
				Context("broker", c.config.Broker).
				Context("hostname", brokerHost).
				Context("operation", "dns_resolution_test").
				Build()
			return enhancedErr
		}
		return nil
	})
}

// testTCPStage performs TCP connection testing
func (c *client) testTCPStage(ctx context.Context) TestResult {
	tcpCtx, tcpCancel := context.WithTimeout(ctx, tcpTimeout)
	defer tcpCancel()

	return runNetworkTest(tcpCtx, TCPConnection, func(ctx context.Context) error {
		hostPort := extractHostPort(c.config.Broker)
		if c.config.TLS.Enabled {
			return c.testTLSConnection(ctx, hostPort)
		}
		return c.testPlainTCPConnection(ctx, hostPort)
	})
}

// testTLSConnection tests TCP connection with TLS handshake
func (c *client) testTLSConnection(ctx context.Context, hostPort string) error {
	tlsConfig, err := c.createTLSConfig()
	if err != nil {
		return errors.New(err).
			Component("mqtt").
			Category(errors.CategoryConfiguration).
			Context("broker", c.config.Broker).
			Context("operation", "tls_config_test").
			Build()
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", hostPort)
	if err != nil {
		return c.buildTCPConnectionError(err, hostPort)
	}

	tlsConn := tls.Client(conn, tlsConfig)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return errors.New(err).
			Component("mqtt").
			Category(errors.CategoryNetwork).
			Context("broker", c.config.Broker).
			Context("host_port", hostPort).
			Context("operation", "tls_handshake_test").
			Build()
	}

	if err := tlsConn.Close(); err != nil {
		GetLogger().Warn("Failed to close TLS connection", logger.Error(err))
	}
	return nil
}

// testPlainTCPConnection tests plain TCP connection without TLS
func (c *client) testPlainTCPConnection(ctx context.Context, hostPort string) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", hostPort)
	if err != nil {
		return c.buildTCPConnectionError(err, hostPort)
	}
	if err := conn.Close(); err != nil {
		GetLogger().Warn("Failed to close connection", logger.Error(err))
	}
	return nil
}

// buildTCPConnectionError creates a standardized TCP connection error
func (c *client) buildTCPConnectionError(err error, hostPort string) error {
	return errors.New(err).
		Component("mqtt").
		Category(errors.CategoryNetwork).
		Context("broker", c.config.Broker).
		Context("host_port", hostPort).
		Context("operation", "tcp_connection_test").
		Build()
}

// testMQTTStage performs MQTT connection testing
func (c *client) testMQTTStage(ctx context.Context) TestResult {
	if c.IsConnected() {
		return TestResult{
			Success: true,
			Stage:   MQTTConnection.String(),
			Message: "Already connected to MQTT broker",
		}
	}

	mqttCtx, mqttCancel := context.WithTimeout(ctx, mqttTimeout)
	defer mqttCancel()

	return runNetworkTest(mqttCtx, MQTTConnection, func(ctx context.Context) error {
		return c.Connect(ctx)
	})
}

// testPublishStage performs message publishing testing
func (c *client) testPublishStage(ctx context.Context) TestResult {
	pubCtx, pubCancel := context.WithTimeout(ctx, pubTimeout)
	defer pubCancel()

	return runNetworkTest(pubCtx, MessagePublish, func(ctx context.Context) error {
		// Create a mock detection for Whooper Swan
		mockNote := datastore.Note{
			Time:           time.Now().Format(time.RFC3339),
			CommonName:     "Whooper Swan",
			ScientificName: "Cygnus cygnus",
			Confidence:     0.95,
			Source: datastore.AudioSource{
				ID:          "mqtt_test",
				SafeString:  "MQTT Test",
				DisplayName: "MQTT Test",
			},
		}

		// Convert to JSON
		noteJson, err := json.Marshal(mockNote)
		if err != nil {
			enhancedErr := errors.New(err).
				Component("mqtt").
				Category(errors.CategoryValidation).
				Context("broker", c.config.Broker).
				Context("operation", "json_marshal_test").
				Build()
			return enhancedErr
		}

		// Construct test topic with proper handling of base topic
		testTopic := constructTestTopic(c.config.Topic)

		// The Publish method already returns enhanced errors, so we can pass it through
		err = c.Publish(ctx, testTopic, string(noteJson))
		if err != nil {
			// Add test-specific context to publish errors
			var enhancedErr *errors.EnhancedError
			if errors.As(err, &enhancedErr) {
				// Add test context to existing enhanced error
				enhancedErr.Context["test_topic"] = testTopic
				enhancedErr.Context["test_payload_size"] = len(noteJson)
				enhancedErr.Context["operation"] = "publish_test"
				return enhancedErr
			}
			// If not enhanced, create new enhanced error
			newErr := errors.New(err).
				Component("mqtt").
				Category(errors.CategoryMQTTPublish).
				Context("broker", c.config.Broker).
				Context("test_topic", testTopic).
				Context("test_payload_size", len(noteJson)).
				Context("operation", "publish_test").
				Build()
			return newErr
		}
		return nil
	})
}

// TestConnection performs a multi-stage test of the MQTT connection and functionality
func (c *client) TestConnection(ctx context.Context, resultChan chan<- TestResult) {
	originalDebug := c.IsDebug()
	c.SetDebug(true)
	defer c.SetDebug(originalDebug)

	sendResult := createResultSender(ctx, resultChan)

	if err := ctx.Err(); err != nil {
		sendResult(TestResult{
			Success: false, Stage: "Test Setup", Message: "Test cancelled",
			Error: err.Error(), State: stateTimeout,
		})
		return
	}

	brokerHost := extractHost(c.config.Broker)
	c.runTestStages(ctx, brokerHost, sendResult)
}

// createResultSender creates a function that sends test results with proper state management
func createResultSender(ctx context.Context, resultChan chan<- TestResult) func(TestResult) {
	return func(result TestResult) {
		resultPtr := &result
		enrichTestResult(resultPtr)
		logTestResult(resultPtr)
		select {
		case <-ctx.Done():
		case resultChan <- result:
		}
	}
}

// enrichTestResult adds progress/state/timestamp to a test result
func enrichTestResult(result *TestResult) {
	msg := strings.ToLower(result.Message)
	result.IsProgress = strings.Contains(msg, "running") ||
		strings.Contains(msg, "testing") ||
		strings.Contains(msg, "establishing") ||
		strings.Contains(msg, "initializing")

	// If there's an error, ensure Success and IsProgress are correctly set
	if result.Error != "" {
		result.Success = false
		result.IsProgress = false
	}

	if result.State == "" {
		result.State = determineResultState(result)
	}
	result.Timestamp = time.Now().Format(time.RFC3339)
}

// determineResultState determines the state based on result properties (pure function)
func determineResultState(result *TestResult) string {
	// Check for timeout errors first (most specific)
	if result.Error != "" {
		errorLower := strings.ToLower(result.Error)
		if strings.Contains(errorLower, "timeout") || strings.Contains(errorLower, "deadline exceeded") {
			return stateTimeout
		}
		return stateFailed
	}

	// Check state based on flags
	switch {
	case result.IsProgress:
		return stateRunning
	case result.Success:
		return stateCompleted
	default:
		return stateFailed
	}
}

// logTestResult logs the test result using structured logging
func logTestResult(result *TestResult) {
	if result.Success {
		GetLogger().Info("Test stage completed",
			logger.String("stage", result.Stage),
			logger.String("message", result.Message))
	} else {
		GetLogger().Warn("Test stage failed",
			logger.String("stage", result.Stage),
			logger.String("message", result.Message),
			logger.String("error", result.Error))
	}
}

// runTestStages executes the test stages in sequence
func (c *client) runTestStages(ctx context.Context, brokerHost string, sendResult func(TestResult)) {
	runStage := func(stage TestStage, test func() TestResult) bool {
		sendResult(TestResult{
			Success: true,
			Stage:   stage.String(),
			Message: fmt.Sprintf("Running %s test...", stage.String()),
		})
		result := test()
		sendResult(result)
		return result.Success
	}

	// Stage 1: DNS Resolution (skip if IP address)
	if !isIPAddress(brokerHost) {
		if !runStage(DNSResolution, func() TestResult { return c.testDNSStage(ctx, brokerHost) }) {
			return
		}
	}

	// Stage 2: TCP Connection
	if !runStage(TCPConnection, func() TestResult { return c.testTCPStage(ctx) }) {
		return
	}

	// Stage 3: MQTT Connection
	if !runStage(MQTTConnection, func() TestResult { return c.testMQTTStage(ctx) }) {
		return
	}

	// Stage 4: Message Publishing
	runStage(MessagePublish, func() TestResult { return c.testPublishStage(ctx) })
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
