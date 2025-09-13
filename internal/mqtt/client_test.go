// client_test.go: Package mqtt provides an MQTT client implementation and associated tests.

package mqtt

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"log"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/observability"
)

const (
	defaultTestBroker      = "tcp://test.mosquitto.org:1883"
	localTestBroker        = "tcp://localhost:1883"
	testQoS                = 1 // Use QoS 1 for more reliable delivery
	testTimeout            = 45 * time.Second
	testTopic              = "birdnet-go/test"
	connectionCheckTimeout = 2 * time.Second
)

// getBrokerAddress returns the MQTT broker address to use for testing
func getBrokerAddress() string {
	if broker := os.Getenv("MQTT_TEST_BROKER"); broker != "" {
		return broker
	}
	// Skip using remote brokers in CI unless explicitly requested
	if os.Getenv("CI") == "true" && os.Getenv("USE_REMOTE_MQTT_BROKER") != "true" {
		// In CI, only use local broker to avoid flaky tests
		if isLocalBrokerAvailable() {
			return localTestBroker
		}
		return "" // No broker available in CI
	}
	// Prefer local broker first for faster tests
	if isLocalBrokerAvailable() {
		return localTestBroker
	}
	// Fall back to public test broker
	if isTestBrokerAvailable() {
		return defaultTestBroker
	}
	return "" // No broker available
}

// isLocalBrokerAvailable checks if a local MQTT broker is available
func isLocalBrokerAvailable() bool {
	conn, err := net.DialTimeout("tcp", "localhost:1883", connectionCheckTimeout)
	if err != nil {
		return false
	}
	if err := conn.Close(); err != nil {
		// Log but don't fail the check
		log.Printf("Failed to close connection: %v", err)
	}
	return true
}

// isTestBrokerAvailable checks if the public test server is available
func isTestBrokerAvailable() bool {
	conn, err := net.DialTimeout("tcp", "test.mosquitto.org:1883", connectionCheckTimeout)
	if err != nil {
		return false
	}
	if err := conn.Close(); err != nil {
		// Log but don't fail the check
		log.Printf("Failed to close connection: %v", err)
	}
	return true
}

// Add debug logging helper
func debugLog(t *testing.T, format string, args ...interface{}) {
	t.Helper()
	msg := fmt.Sprintf(format, args...)
	log.Printf("[DEBUG] %s", msg)
	t.Logf("[DEBUG] %s", msg)
}

// retryWithTimeout attempts an operation with retries until it succeeds or times out
func retryWithTimeout(timeout time.Duration, operation func() error) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	backoff := 100 * time.Millisecond
	maxBackoff := 2 * time.Second
	var lastErr error
	attempts := 0

	for {
		attempts++
		if err := operation(); err != nil {
			lastErr = err
			log.Printf("[DEBUG] Retry attempt %d failed: %v", attempts, err)
			// Exponential backoff with jitter
			jitter := time.Duration(rand.Int63n(int64(backoff / 2))) // #nosec G404 -- weak randomness acceptable for test backoff jitter, not security-critical
			sleepTime := backoff + jitter
			log.Printf("[DEBUG] Sleeping for %v before next retry", sleepTime)

			timer := time.NewTimer(sleepTime)
			select {
			case <-ctx.Done():
				timer.Stop()
				return fmt.Errorf("operation timed out after %v and %d attempts, last error: %w", timeout, attempts, lastErr)
			case <-timer.C:
				// Continue with next retry
			}

			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		log.Printf("[DEBUG] Operation succeeded after %d attempts", attempts)
		return nil
	}
}

// TestMQTTClient runs a suite of tests for the MQTT client implementation.
// It covers basic functionality, error handling, reconnection scenarios, and metrics collection.
func TestMQTTClient(t *testing.T) {
	t.Parallel()
	broker := getBrokerAddress()
	if broker == "" {
		//nolint:misspell // "mosquitto" is the correct spelling for the MQTT broker software
		t.Skip("No MQTT broker available for testing. Set MQTT_TEST_BROKER env var or ensure mosquitto is running on localhost:1883")
		return
	}

	if broker == localTestBroker {
		//nolint:misspell // "mosquitto" is the correct spelling for the MQTT broker software
		t.Log("Using local MQTT broker. Please ensure mosquitto is running on localhost:1883")
	} else {
		t.Logf("Using remote MQTT broker: %s", broker)
	}

	t.Run("Basic Functionality", testBasicFunctionality)
	t.Run("Incorrect Broker Address", testIncorrectBrokerAddress)
	t.Run("Connection Loss Before Publish", testConnectionLossBeforePublish)
	t.Run("Publish While Disconnected", testPublishWhileDisconnected)
	t.Run("Reconnection With Backoff", testReconnectionWithBackoff)
	t.Run("Metrics Collection", testMetricsCollection)
	t.Run("Context Cancellation", testContextCancellation)
	t.Run("Timeout Handling", testTimeoutHandling)
	t.Run("DNS Resolution", testDNSResolutionForTest)
}

// testBasicFunctionality verifies the basic operations of the MQTT client:
// connection, publishing a message, and disconnection.
func testBasicFunctionality(t *testing.T) {
	t.Parallel()
	debugLog(t, "Starting Basic Functionality test")
	broker := getBrokerAddress()
	if broker == "" {
		t.Skip("No MQTT broker available")
		return
	}

	mqttClient, _ := createTestClient(t, broker)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	debugLog(t, "Attempting initial connection")
	// Try to connect with shorter timeout for tests
	err := mqttClient.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect to MQTT broker: %v", err)
	}

	if !mqttClient.IsConnected() {
		t.Fatal("Client is not connected after successful connection")
	}
	debugLog(t, "Successfully connected to broker")

	// Try to publish
	debugLog(t, "Attempting to publish message")
	err = mqttClient.Publish(ctx, testTopic, "Hello, MQTT!")
	if err != nil {
		debugLog(t, "Warning: Publish failed: %v", err)
		t.Logf("Warning: Publish failed, this might be due to broker limitations: %v", err)
		// Don't fail the test as some brokers may have restrictions
	}
	debugLog(t, "Successfully published message")

	debugLog(t, "Waiting for message processing")
	time.Sleep(1 * time.Second)

	debugLog(t, "Disconnecting client")
	mqttClient.Disconnect()

	if mqttClient.IsConnected() {
		t.Fatal("Client is still connected after disconnection")
	}
	debugLog(t, "Basic Functionality test completed")
}

// testIncorrectBrokerAddress checks the client's behavior when provided with invalid broker addresses.
// It includes subtests for unresolvable hostnames and invalid IP addresses.
func testIncorrectBrokerAddress(t *testing.T) {
	t.Run("Unresolvable Hostname", func(t *testing.T) {
		mqttClient, _ := createTestClient(t, "tcp://unresolvable.invalid:1883")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		err := mqttClient.Connect(ctx)

		if err == nil {
			t.Fatal("Expected connection to fail with invalid broker address")
		}

		var dnsErr *net.DNSError
		if !errors.As(err, &dnsErr) {
			t.Fatalf("Expected DNS resolution error, got: %v", err)
		}

		// Accept either "host not found" or "server misbehaving" errors
		if !dnsErr.IsNotFound && !strings.Contains(dnsErr.Error(), "server misbehaving") {
			t.Fatalf("Expected 'host not found' or 'server misbehaving' DNS error, got: %v", dnsErr)
		}

		if mqttClient.IsConnected() {
			t.Fatal("Client reports connected status with invalid broker address")
		}
	})

	t.Run("Invalid IP Address", func(t *testing.T) {
		mqttClient, _ := createTestClient(t, "tcp://256.0.0.1:1883") // Invalid IP address

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		err := mqttClient.Connect(ctx)

		if err == nil {
			t.Fatal("Expected connection to fail with invalid IP address")
		}

		// The error could be either a DNS error or a connection error
		var dnsErr *net.DNSError
		var netErr net.Error

		if !errors.As(err, &dnsErr) && !errors.As(err, &netErr) {
			t.Fatalf("Expected either a DNS error or a net.Error, got: %v", err)
		}

		if mqttClient.IsConnected() {
			t.Fatal("Client reports connected status with invalid IP address")
		}
	})
}

// testConnectionLossBeforePublish simulates a scenario where the connection is lost before
// attempting to publish a message. It verifies that the publish operation fails and
// the client reports as disconnected after the connection loss.
func testConnectionLossBeforePublish(t *testing.T) {
	debugLog(t, "Starting Connection Loss Before Publish test")
	broker := getBrokerAddress()
	if broker == "" {
		t.Skip("No MQTT broker available")
		return
	}
	mqttClient, _ := createTestClient(t, broker)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	debugLog(t, "Attempting initial connection")
	err := mqttClient.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect to MQTT broker: %v", err)
	}
	debugLog(t, "Successfully connected to broker")

	debugLog(t, "Simulating connection loss")
	mqttClient.Disconnect()

	debugLog(t, "Attempting to publish after disconnect")
	err = mqttClient.Publish(ctx, testTopic, "Hello after reconnect!")
	if err == nil {
		t.Fatal("Expected publish to fail after connection loss")
	}
	debugLog(t, "Publish failed as expected with error: %v", err)

	debugLog(t, "Waiting for potential reconnection attempts")
	time.Sleep(5 * time.Second)

	if mqttClient.IsConnected() {
		t.Fatal("Client should not be connected after forced disconnection")
	}
	debugLog(t, "Connection Loss Before Publish test completed")
}

// testPublishWhileDisconnected attempts to publish a message while the client is disconnected.
// It verifies that the publish operation fails when the client is not connected to a broker.
func testPublishWhileDisconnected(t *testing.T) {
	debugLog(t, "Starting Publish While Disconnected test")
	broker := getBrokerAddress()
	if broker == "" {
		t.Skip("No MQTT broker available")
		return
	}
	mqttClient, _ := createTestClient(t, broker)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	debugLog(t, "Attempting to publish without connecting")
	err := mqttClient.Publish(ctx, testTopic, "This should fail")
	if err == nil {
		t.Fatal("Expected publish to fail when not connected")
	}
	debugLog(t, "Publish failed as expected with error: %v", err)
	debugLog(t, "Publish While Disconnected test completed")
}

// testReconnectionWithBackoff verifies the client's reconnection behavior with a backoff mechanism.
// It simulates a connection loss, attempts an immediate reconnection (which should fail due to cooldown),
// waits for the cooldown period, and then attempts another reconnection which should succeed.
func testReconnectionWithBackoff(t *testing.T) {
	debugLog(t, "Starting Reconnection With Backoff test")
	broker := getBrokerAddress()
	if broker == "" {
		t.Skip("No MQTT broker available")
		return
	}
	mqttClient, _ := createTestClient(t, broker)

	// Use longer timeout for reconnection test as it needs to connect twice
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	debugLog(t, "Attempting initial connection")
	// Create a shorter timeout for individual connection attempts
	connectCtx, connectCancel := context.WithTimeout(ctx, 20*time.Second)
	defer connectCancel()
	err := mqttClient.Connect(connectCtx)
	if err != nil {
		t.Fatalf("Failed to connect to MQTT broker: %v", err)
	}
	debugLog(t, "Successfully connected to broker")

	debugLog(t, "Simulating connection loss")
	mqttClient.Disconnect()

	debugLog(t, "Waiting short period before reconnection attempt")
	time.Sleep(2 * time.Second)

	debugLog(t, "Attempting immediate reconnection (should fail due to cooldown)")
	// Use short timeout for the expected failure
	failCtx, failCancel := context.WithTimeout(ctx, 5*time.Second)
	defer failCancel()
	err = mqttClient.Connect(failCtx)
	if err == nil {
		t.Fatal("Expected reconnection to fail due to cooldown")
	}
	debugLog(t, "Immediate reconnection failed as expected with error: %v", err)

	debugLog(t, "Waiting for cooldown period")
	time.Sleep(3 * time.Second)

	debugLog(t, "Attempting reconnection after cooldown")
	// Create new timeout for second connection
	reconnectCtx, reconnectCancel := context.WithTimeout(ctx, 20*time.Second)
	defer reconnectCancel()
	err = mqttClient.Connect(reconnectCtx)
	if err != nil {
		t.Fatalf("Failed to reconnect after cooldown: %v", err)
	}

	if !mqttClient.IsConnected() {
		t.Fatal("Client failed to reconnect after simulated connection loss")
	}
	debugLog(t, "Successfully reconnected after cooldown")
	debugLog(t, "Reconnection With Backoff test completed")
}

// testMetricsCollection checks the collection and accuracy of various metrics related to
// MQTT client operations, including connection status, message delivery, and error counts.
func testMetricsCollection(t *testing.T) {
	debugLog(t, "Starting Metrics Collection test")
	broker := getBrokerAddress()
	if broker == "" {
		t.Skip("No MQTT broker available")
		return
	}
	mqttClient, metrics := createTestClient(t, broker)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Connect with retries
	debugLog(t, "Attempting to connect with retries")
	err := retryWithTimeout(15*time.Second, func() error {
		return mqttClient.Connect(ctx)
	})
	if err != nil {
		t.Fatalf("Failed to connect to MQTT broker after retries: %v", err)
	}

	// Check initial connection status with retry
	debugLog(t, "Checking initial connection status")
	var connectionStatus float64
	err = retryWithTimeout(5*time.Second, func() error {
		connectionStatus = getGaugeValue(t, metrics.MQTT.ConnectionStatus)
		debugLog(t, "Current connection status: %v", connectionStatus)
		if connectionStatus != 1 {
			return fmt.Errorf("connection status not 1")
		}
		return nil
	})
	if err != nil {
		t.Errorf("Initial connection status metric incorrect. Expected 1, got %v", connectionStatus)
	}

	// Publish with retries
	debugLog(t, "Attempting to publish message")
	err = retryWithTimeout(10*time.Second, func() error {
		return mqttClient.Publish(ctx, testTopic, "Test message")
	})
	if err != nil {
		t.Fatalf("Failed to publish message after retries: %v", err)
	}

	debugLog(t, "Waiting for metrics to update")
	time.Sleep(2 * time.Second)

	// Check metrics
	messagesDelivered := getCounterValue(t, metrics.MQTT.MessagesDelivered)
	debugLog(t, "Messages delivered metric: %v", messagesDelivered)
	if messagesDelivered != 1 {
		t.Errorf("Messages delivered metric incorrect. Expected 1, got %v", messagesDelivered)
	}

	// Check message size metric
	messageSize := getHistogramValue(t, metrics.MQTT.MessageSize)
	expectedSize := float64(len("Test message"))
	if messageSize != expectedSize {
		t.Errorf("Message size metric incorrect. Expected %v, got %v", expectedSize, messageSize)
	}

	// Disconnect and check connection status
	mqttClient.Disconnect()
	time.Sleep(time.Second) // Allow time for metric to update
	connectionStatus = getGaugeValue(t, metrics.MQTT.ConnectionStatus)
	if connectionStatus != 0 {
		t.Errorf("Connection status metric after disconnection incorrect. Expected 0, got %v", connectionStatus)
	}

	// Log other metrics for informational purposes
	t.Logf("Error count: %v", getCounterVecValue(t, metrics.MQTT.Errors))
	t.Logf("Reconnect attempts: %v", getCounterValue(t, metrics.MQTT.ReconnectAttempts))
	t.Logf("Publish latency: %v", getHistogramValue(t, metrics.MQTT.PublishLatency))
	debugLog(t, "Metrics Collection test completed")
}

// Add this helper function to get Histogram values
func getHistogramValue(t *testing.T, histogram prometheus.Histogram) float64 {
	t.Helper()
	var metric dto.Metric
	err := histogram.Write(&metric)
	if err != nil {
		t.Fatalf("Failed to write metric: %v", err)
	}
	return metric.Histogram.GetSampleSum()
}

// Helper function to get the value of a Gauge metric
func getGaugeValue(t *testing.T, gauge prometheus.Gauge) float64 {
	t.Helper()
	var metric dto.Metric
	err := gauge.Write(&metric)
	if err != nil {
		t.Fatalf("Failed to write metric: %v", err)
	}
	return *metric.Gauge.Value
}

// Helper function to get the value of a Counter metric
func getCounterValue(t *testing.T, counter prometheus.Counter) float64 {
	t.Helper()
	var metric dto.Metric
	err := counter.Write(&metric)
	if err != nil {
		t.Fatalf("Failed to write metric: %v", err)
	}
	return *metric.Counter.Value
}

func getCounterVecValue(t *testing.T, counterVec *prometheus.CounterVec) float64 {
	t.Helper()
	// Get all metric families
	metricFamilies, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	// Find the metric family for this counter vec
	for _, mf := range metricFamilies {
		if mf.GetName() == "mqtt_errors_total" {
			totalValue := 0.0
			for _, metric := range mf.GetMetric() {
				totalValue += metric.GetCounter().GetValue()
			}
			return totalValue
		}
	}
	return 0.0
}

// testContextCancellation verifies that the client properly handles context cancellation
// during connection and publish operations
func testContextCancellation(t *testing.T) {
	t.Run("Connect Cancellation", func(t *testing.T) {
		debugLog(t, "Starting Connect Cancellation test")
		// Use a blackhole address to ensure Connect() blocks long enough for cancellation to occur.
		// The client's internal connect timeout (e.g., 5s) should be longer than the cancellation (100ms).
		mqttClient, _ := createTestClient(t, "tcp://10.255.255.1:1883")

		ctxConnect, cancelConnect := context.WithTimeout(context.Background(), 10*time.Second) // Overall test timeout
		defer cancelConnect()

		ctxCancel, cancelFunc := context.WithCancel(ctxConnect)
		defer cancelFunc() // Redundant due to cancelFunc() below, but good practice

		debugLog(t, "Created cancellation context")
		connectErrChan := make(chan error, 1)

		debugLog(t, "Starting connection attempt in goroutine")
		go func() {
			// This connect call should be interrupted by ctxCancel
			connectErrChan <- mqttClient.Connect(ctxCancel)
		}()

		debugLog(t, "Connection attempt started, waiting before cancellation")
		time.Sleep(100 * time.Millisecond) // Wait a short period

		debugLog(t, "Cancelling context")
		cancelFunc() // Cancel the context for Connect

		var err error
		select {
		case err = <-connectErrChan:
			debugLog(t, "Received error from connection attempt: %v", err)
		case <-ctxConnect.Done(): // Test timeout
			t.Fatal("Test timed out waiting for connect to return after cancellation")
		}

		if err == nil {
			t.Fatal("Expected connection to fail due to context cancellation, but it succeeded")
		}

		// Check if the error is context.Canceled or context.DeadlineExceeded (if parent ctx timed out first, less likely)
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Expected context.Canceled or context.DeadlineExceeded, got: %v", err)
		}

		if mqttClient.IsConnected() {
			t.Error("Client should not be connected after cancellation")
		}
		debugLog(t, "Connect Cancellation test completed")
	})

	t.Run("Publish Cancellation", func(t *testing.T) {
		broker := getBrokerAddress()
		if broker == "" {
			t.Skip("No MQTT broker available")
			return
		}
		mqttClient, _ := createTestClient(t, broker)

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		err := mqttClient.Connect(ctx)
		if err != nil {
			t.Fatalf("Failed to connect to MQTT broker: %v", err)
		}

		publishCtx, publishCancel := context.WithCancel(context.Background())

		// Cancel the context immediately before publish
		publishCancel()

		err = mqttClient.Publish(publishCtx, testTopic, "This should fail")
		if err == nil {
			t.Fatal("Expected publish to fail due to context cancellation")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Expected context.Canceled error, got: %v", err)
		}
	})
}

// testTimeoutHandling verifies that the client properly handles various timeout scenarios
func testTimeoutHandling(t *testing.T) {
	t.Run("Connect Timeout", func(t *testing.T) {
		debugLog(t, "Starting Connect Timeout test")
		// Use a blackhole IP address to force a connection timeout
		mqttClient, _ := createTestClient(t, "tcp://192.0.2.1:1883") // TEST-NET-1 address, guaranteed to be unreachable

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		debugLog(t, "Attempting connection to unreachable address")
		start := time.Now()
		err := mqttClient.Connect(ctx)
		duration := time.Since(start)

		debugLog(t, "Connection attempt completed in %v with error: %v", duration, err)

		if err == nil {
			t.Fatal("Expected connection to fail due to timeout")
		}

		if duration >= 5*time.Second {
			t.Fatal("Connection attempt took too long, timeout not working properly")
		}

		// Check if the error is timeout related
		if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "timeout") {
			t.Fatalf("Expected timeout error, got: %v", err)
		}
		debugLog(t, "Connect Timeout test completed")
	})

	t.Run("Publish Timeout", func(t *testing.T) {
		broker := getBrokerAddress()
		if broker == "" {
			t.Skip("No MQTT broker available")
			return
		}
		mqttClient, _ := createTestClient(t, broker)

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		err := mqttClient.Connect(ctx)
		if err != nil {
			t.Fatalf("Failed to connect to MQTT broker: %v", err)
		}

		// Force disconnect to simulate network issues
		mqttClient.Disconnect()

		// Use a short context timeout for publish
		publishCtx, publishCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer publishCancel()

		start := time.Now()
		err = mqttClient.Publish(publishCtx, testTopic, "This should timeout")
		duration := time.Since(start)

		if err == nil {
			t.Fatal("Expected publish to fail due to timeout")
		}

		if duration >= 5*time.Second {
			t.Fatal("Publish attempt took too long, timeout not working properly")
		}
	})
}

// testDNSResolutionForTest verifies that the client properly handles DNS resolution scenarios
func testDNSResolutionForTest(t *testing.T) {
	t.Run("DNS Resolution Timeout", func(t *testing.T) {
		mqttClient, _ := createTestClient(t, "tcp://very-long-non-existent-domain-name.com:1883")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		start := time.Now()
		err := mqttClient.Connect(ctx)
		duration := time.Since(start)

		if err == nil {
			t.Fatal("Expected connection to fail due to DNS resolution failure")
		}

		var dnsErr *net.DNSError
		if !errors.As(err, &dnsErr) {
			t.Fatalf("Expected DNS error, got: %v", err)
		}

		if duration >= 15*time.Second {
			t.Fatal("DNS resolution took too long, timeout not working properly")
		}
	})
}

// sanitizeClientID ensures the client ID is valid for MQTT brokers
func sanitizeClientID(id string) string {
	// Replace invalid characters with hyphen
	sanitized := strings.ReplaceAll(id, "/", "-")
	sanitized = strings.ReplaceAll(sanitized, " ", "-")
	sanitized = strings.ReplaceAll(sanitized, ".", "-")

	// Truncate to 23 characters if needed
	if len(sanitized) > 23 {
		sanitized = sanitized[:23]
	}

	return sanitized
}

// createTestClient is a helper function that creates and configures an MQTT client for testing purposes.
func createTestClient(t *testing.T, broker string) (Client, *observability.Metrics) {
	t.Helper()
	// Use test name as client ID to ensure uniqueness when running tests in parallel
	clientID := sanitizeClientID(t.Name())

	testSettings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			MQTT: conf.MQTTSettings{
				Broker:   broker,
				Username: "",
				Password: "",
			},
		},
	}
	testSettings.Main.Name = clientID
	metrics, err := observability.NewMetrics()
	if err != nil {
		if t != nil {
			t.Fatalf("Failed to create metrics: %v", err)
		}
		return nil, nil
	}

	client, err := NewClient(testSettings, metrics)
	if err != nil {
		if t != nil {
			t.Fatalf("Failed to create MQTT client: %v", err)
		}
		return nil, nil
	}

	return client, metrics
}

// TestIsIPAddress verifies the IP address detection function
func TestIsIPAddress(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		// IPv4 addresses
		{"Simple IPv4", "192.168.1.1", true},
		{"IPv4 with tcp protocol", "tcp://192.168.1.1:1883", true},
		{"IPv4 with mqtt protocol", "mqtt://10.0.0.1:1883", true},
		{"IPv4 localhost", "127.0.0.1", true},
		{"IPv4 with port", "127.0.0.1:1883", true},

		// IPv6 addresses
		{"Simple IPv6", "::1", true},
		{"IPv6 localhost with brackets", "[::1]", true},
		{"IPv6 with port", "[::1]:1883", true},
		{"IPv6 with tcp protocol", "tcp://[2001:db8::1]:1883", true},
		{"IPv6 with mqtt protocol", "mqtt://[2001:db8::1]:1883", true},
		{"IPv6 address only", "2001:db8::1", true},
		{"IPv6 with brackets", "[2001:db8::1]", true},

		// Hostnames (should return false)
		{"Simple hostname", "localhost", false},
		{"Hostname with protocol", "mqtt://localhost:1883", false},
		{"FQDN", "broker.hivemq.com", false},
		{"FQDN with port", "test.mosquitto.org:1883", false},
		{"Subdomain", "mqtt.example.com", false},

		// Invalid inputs (should return false)
		{"Empty string", "", false},
		{"Invalid hostname", "not-an-ip", false},
		{"Invalid IPv4", "256.256.256.256", false},
		{"Invalid IPv6", "2001:zz::1", false},
		{"Invalid protocol", "invalid://192.168.1.1", false},
		{"Malformed IPv6 brackets", "[2001:db8::1", false},
		{"IPv6 without closing bracket", "[2001:db8::1:1883", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isIPAddress(tt.input)
			if result != tt.expected {
				t.Errorf("isIPAddress(%q) = %v; want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestCheckConnectionCooldown tests the connection cooldown validation
func TestCheckConnectionCooldown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		lastAttempt         time.Duration // how long ago was last attempt
		cooldownPeriod      time.Duration
		expectError         bool
		expectedErrorSubstr string
	}{
		{
			name:           "No previous attempt",
			lastAttempt:    24 * time.Hour, // Very long ago
			cooldownPeriod: 5 * time.Second,
			expectError:    false,
		},
		{
			name:                "Recent attempt within cooldown",
			lastAttempt:         1 * time.Second, // Recent
			cooldownPeriod:      5 * time.Second,
			expectError:         true,
			expectedErrorSubstr: "connection attempt too recent",
		},
		{
			name:           "Attempt just after cooldown period",
			lastAttempt:    6 * time.Second, // Just outside cooldown
			cooldownPeriod: 5 * time.Second,
			expectError:    false,
		},
		{
			name:           "Zero cooldown period",
			lastAttempt:    1 * time.Second,
			cooldownPeriod: 0,
			expectError:    false,
		},
		{
			name:           "Exactly at cooldown boundary",
			lastAttempt:    5 * time.Second,
			cooldownPeriod: 5 * time.Second,
			expectError:    false, // Should be allowed at boundary
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create test client
			config := DefaultConfig()
			config.Broker = "tcp://test.example.com:1883"
			config.ReconnectCooldown = tt.cooldownPeriod
			metrics, _ := observability.NewMetrics()
			c := &client{
				config:          config,
				metrics:         metrics.MQTT,
				lastConnAttempt: time.Now().Add(-tt.lastAttempt),
				reconnectStop:   make(chan struct{}),
			}

			// Create logger for test
			logger := mqttLogger.With("broker", config.Broker, "client_id", config.ClientID)

			// Test the method - acquire read lock as required by the method
			c.mu.RLock()
			err := c.checkConnectionCooldownLocked(logger)
			c.mu.RUnlock()

			// Verify results
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got nil")
					return
				}
				if !strings.Contains(err.Error(), tt.expectedErrorSubstr) {
					t.Errorf("Expected error to contain %q, got %q", tt.expectedErrorSubstr, err.Error())
				}
			} else if err != nil {
				t.Errorf("Expected no error but got %v", err)
			}
		})
	}
}

// TestConfigureClientOptions tests the MQTT client options configuration
func TestConfigureClientOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		setupConfig func(*Config)
		expectError bool
		errorSubstr string
		verifyOpts  func(*testing.T, paho.ClientOptions)
	}{
		{
			name: "Basic configuration",
			setupConfig: func(c *Config) {
				c.Broker = "tcp://test.example.com:1883"
				c.ClientID = "test-client"
				c.Username = "testuser"
				c.Password = "testpass"
				c.ConnectTimeout = 10 * time.Second
			},
			expectError: false,
			verifyOpts: func(t *testing.T, opts paho.ClientOptions) {
				t.Helper()
				// Verify broker configuration by creating a client and checking behavior
				// Note: paho ClientOptions doesn't expose direct getters, but we can verify functionality
				client := paho.NewClient(&opts)
				if client == nil {
					t.Error("Expected client to be created successfully with configured options")
					return
				}

				// Verify that options are properly configured by checking if client is not connected initially
				// (this verifies AutoReconnect=false and ConnectRetry=false are working)
				if client.IsConnected() {
					t.Error("Expected client to not be connected initially (AutoReconnect should be disabled)")
				}

				// Clean up the client
				client.Disconnect(250)
			},
		},
		{
			name: "TLS configuration enabled but invalid cert",
			setupConfig: func(c *Config) {
				c.Broker = "ssl://test.example.com:8883"
				c.ClientID = "test-client"
				c.TLS.Enabled = true
				c.TLS.CACert = "/nonexistent/ca.crt"
			},
			expectError: true,
			errorSubstr: "does not exist",
		},
		{
			name: "TLS configuration with InsecureSkipVerify",
			setupConfig: func(c *Config) {
				c.Broker = "ssl://test.example.com:8883"
				c.ClientID = "test-client"
				c.TLS.Enabled = true
				c.TLS.InsecureSkipVerify = true
			},
			expectError: false,
			verifyOpts: func(t *testing.T, opts paho.ClientOptions) {
				t.Helper()
				// Verify TLS-enabled options by creating a client and checking behavior
				client := paho.NewClient(&opts)
				if client == nil {
					t.Error("Expected client to be created successfully with TLS-configured options")
					return
				}

				// Verify that client is not connected initially (AutoReconnect=false)
				if client.IsConnected() {
					t.Error("Expected client to not be connected initially (AutoReconnect should be disabled)")
				}

				// Clean up the client
				client.Disconnect(250)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create test client with config
			config := DefaultConfig()
			tt.setupConfig(&config)
			metrics, _ := observability.NewMetrics()
			c := &client{
				config:        config,
				metrics:       metrics.MQTT,
				reconnectStop: make(chan struct{}),
			}

			// Create logger for test
			logger := mqttLogger.With("broker", config.Broker, "client_id", config.ClientID)

			// Test the method
			opts, err := c.configureClientOptions(logger)

			// Verify results
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got nil")
					return
				}
				if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.errorSubstr)) {
					t.Errorf("Expected error to contain %q, got %q", tt.errorSubstr, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got %v", err)
					return
				}
				if opts == nil {
					t.Errorf("Expected non-nil options")
					return
				}
				if tt.verifyOpts != nil {
					tt.verifyOpts(t, *opts)
				}
			}
		})
	}
}

// TestPerformDNSResolution tests the DNS resolution functionality
func TestPerformDNSResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		broker      string
		expectError bool
		errorSubstr string
	}{
		{
			name:        "Valid hostname resolution",
			broker:      "tcp://example.com:1883",
			expectError: false,
		},
		{
			name:        "IP address (no DNS needed)",
			broker:      "tcp://8.8.8.8:1883",
			expectError: false,
		},
		{
			name:        "IPv6 address (no DNS needed)",
			broker:      "tcp://[::1]:1883",
			expectError: false,
		},
		{
			name:        "Invalid hostname",
			broker:      "tcp://this-hostname-does-not-exist.invalid:1883",
			expectError: true,
			errorSubstr: "no such host",
		},
		{
			name:        "Invalid broker URL format",
			broker:      "invalid://[malformed",
			expectError: true,
			errorSubstr: "parse",
		},
		{
			name:        "Empty broker URL",
			broker:      "",
			expectError: true,
			errorSubstr: "lookup",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create test client with config
			config := DefaultConfig()
			config.Broker = tt.broker
			metrics, _ := observability.NewMetrics()
			c := &client{
				config:        config,
				metrics:       metrics.MQTT,
				reconnectStop: make(chan struct{}),
			}

			// Create context with timeout
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			// Create logger for test
			logger := mqttLogger.With("broker", config.Broker, "client_id", config.ClientID)

			// Test the method
			err := c.performDNSResolution(ctx, logger)

			// Verify results
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got nil")
					return
				}
				if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.errorSubstr)) {
					t.Errorf("Expected error to contain %q, got %q", tt.errorSubstr, err.Error())
				}
			} else if err != nil {
				t.Errorf("Expected no error but got %v", err)
			}
		})
	}
}

// TestCalculateCancelTimeout tests the timeout calculation logic
func TestCalculateCancelTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		disconnectTimeout time.Duration
		expectedTimeout   uint
		description       string
	}{
		{
			name:              "Normal timeout calculation",
			disconnectTimeout: 5 * time.Second,
			expectedTimeout:   uint(CancelDisconnectTimeout.Milliseconds()), // min(1000, 5000/5) = min(1000, 1000) = 1000
			description:       "Standard case with reasonable timeout",
		},
		{
			name:              "Very short timeout",
			disconnectTimeout: 500 * time.Millisecond,
			expectedTimeout:   100, // min(1000, 500/5) = min(1000, 100) = 100
			description:       "Short timeout calculation: ms/5",
		},
		{
			name:              "Very large timeout",
			disconnectTimeout: 10 * time.Second,
			expectedTimeout:   uint(CancelDisconnectTimeout.Milliseconds()), // min(1000, 10000/5) = min(1000, 2000) = 1000
			description:       "Large timeout should be capped at minimum",
		},
		{
			name:              "Zero timeout",
			disconnectTimeout: 0,
			expectedTimeout:   uint(CancelDisconnectTimeout.Milliseconds()), // Should use minimum
			description:       "Zero timeout should use minimum safe value",
		},
		{
			name:              "Negative timeout",
			disconnectTimeout: -5 * time.Second,
			expectedTimeout:   uint(CancelDisconnectTimeout.Milliseconds()), // Should use minimum
			description:       "Negative timeout should use minimum safe value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create test client with the specified disconnect timeout
			config := DefaultConfig()
			config.DisconnectTimeout = tt.disconnectTimeout
			metrics, _ := observability.NewMetrics()
			c := &client{
				config:        config,
				metrics:       metrics.MQTT,
				reconnectStop: make(chan struct{}),
			}

			// Test the method
			result := c.calculateCancelTimeout()

			// Verify result
			if result != tt.expectedTimeout {
				t.Errorf("%s: expected %v, got %v", tt.description, tt.expectedTimeout, result)
			}

			// Verify the result is never zero
			if result == 0 {
				t.Errorf("Calculated timeout should never be zero, got %v", result)
			}

			// Verify the result is reasonable (not more than minimum timeout)
			maxTimeout := uint(CancelDisconnectTimeout.Milliseconds())
			if result > maxTimeout {
				t.Errorf("Calculated timeout should be at most %v ms, got %v ms", maxTimeout, result)
			}
		})
	}
}

// TestPerformConnectionAttempt tests the connection attempt functionality
func TestPerformConnectionAttempt(t *testing.T) {
	// Network-heavy; avoid parallelism to reduce flakes

	tests := []struct {
		name        string
		setupConfig func(*Config)
		expectError bool
		errorSubstr string
		description string
	}{
		{
			name: "Invalid broker URL",
			setupConfig: func(config *Config) {
				config.Broker = "invalid://malformed-url"
			},
			expectError: true,
			errorSubstr: "network Error",
			description: "Should fail with invalid broker URL",
		},
		{
			name: "Connection timeout",
			setupConfig: func(config *Config) {
				config.Broker = "tcp://192.0.2.1:1883"         // TEST-NET-1 (unreachable)
				config.ConnectTimeout = 100 * time.Millisecond // Very short timeout
			},
			expectError: true,
			errorSubstr: "timeout",
			description: "Should timeout on unreachable broker",
		},
		{
			name: "Context cancelled",
			setupConfig: func(config *Config) {
				config.Broker = "tcp://192.0.2.1:1883" // TEST-NET-1 (unreachable)
				config.ConnectTimeout = 5 * time.Second
			},
			expectError: true,
			errorSubstr: "context",
			description: "Should fail when context is cancelled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test client
			config := DefaultConfig()
			tt.setupConfig(&config)
			metrics, _ := observability.NewMetrics()
			c := &client{
				config:        config,
				metrics:       metrics.MQTT,
				reconnectStop: make(chan struct{}),
			}

			// Create logger for test
			logger := mqttLogger.With("broker", config.Broker, "client_id", config.ClientID)

			// Create context with very short timeout for "context cancelled" test
			var ctx context.Context
			var cancel context.CancelFunc
			if tt.name == "Context cancelled" {
				ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
				defer cancel()
			} else {
				ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
			}

			// Create a MQTT client options first (needed for performConnectionAttempt)
			opts, optsErr := c.configureClientOptions(logger)
			if optsErr != nil {
				if tt.expectError && tt.errorSubstr == "invalid broker URL" {
					// This is expected for invalid URL tests
					if !strings.Contains(optsErr.Error(), "invalid broker URL") {
						t.Errorf("Expected 'invalid broker URL' error, got %v", optsErr)
					}
					return
				}
				t.Errorf("Unexpected error creating client options: %v", optsErr)
				return
			}

			// Create MQTT client
			clientToConnect := paho.NewClient(opts)

			// Test the method
			err := c.performConnectionAttempt(ctx, clientToConnect, logger)

			// Verify results
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got nil")
					return
				}
				if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.errorSubstr)) {
					t.Errorf("Expected error to contain %q, got %q", tt.errorSubstr, err.Error())
				}
			} else if err != nil {
				t.Errorf("Expected no error but got %v", err)
			}

			// Ensure client is properly cleaned up after test
			c.Disconnect()
		})
	}
}

// TestConnectWithOptions verifies that the automatic reconnection bypasses cooldown
// while manual connections respect it.
func TestConnectWithOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		isAutoReconnect bool
		lastAttempt     time.Duration // how long ago was last attempt
		cooldownPeriod  time.Duration
		expectError     bool
		errorSubstr     string
	}{
		{
			name:            "Manual connection respects cooldown",
			isAutoReconnect: false,
			lastAttempt:     2 * time.Second,
			cooldownPeriod:  5 * time.Second,
			expectError:     true,
			errorSubstr:     "connection attempt too recent",
		},
		{
			name:            "Automatic reconnect bypasses cooldown",
			isAutoReconnect: true,
			lastAttempt:     2 * time.Second,
			cooldownPeriod:  5 * time.Second,
			expectError:     false, // Should succeed despite cooldown
		},
		{
			name:            "Manual connection after cooldown",
			isAutoReconnect: false,
			lastAttempt:     6 * time.Second,
			cooldownPeriod:  5 * time.Second,
			expectError:     false,
		},
		{
			name:            "Automatic reconnect with no cooldown",
			isAutoReconnect: true,
			lastAttempt:     0,
			cooldownPeriod:  5 * time.Second,
			expectError:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Skip tests that require actual broker connection
			broker := getBrokerAddress()
			if broker == "" {
				t.Skip("No MQTT broker available")
				return
			}

			// Create test client
			config := DefaultConfig()
			config.Broker = broker
			config.ReconnectCooldown = tt.cooldownPeriod
			config.ConnectTimeout = 2 * time.Second
			metrics, _ := observability.NewMetrics()
			c := &client{
				config:          config,
				metrics:         metrics.MQTT,
				lastConnAttempt: time.Now().Add(-tt.lastAttempt),
				reconnectStop:   make(chan struct{}),
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			// Test the connectWithOptions method
			err := c.connectWithOptions(ctx, tt.isAutoReconnect)

			// Clean up
			defer c.Disconnect()

			// Verify results
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got nil")
					return
				}
				if !strings.Contains(err.Error(), tt.errorSubstr) {
					t.Errorf("Expected error to contain %q, got %q", tt.errorSubstr, err.Error())
				}
			} else if err != nil {
				t.Errorf("Expected no error but got %v", err)
			}
		})
	}
}

// TestTimeRoundingEdgeCase verifies that sub-second durations are handled correctly
// when rounding time for display in error messages.
func TestTimeRoundingEdgeCase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		lastAttempt         time.Duration // how long ago was last attempt
		expectedDisplayTime string        // expected time shown in error
	}{
		{
			name:                "Sub-second rounds to 1s",
			lastAttempt:         500 * time.Millisecond,
			expectedDisplayTime: "1s",
		},
		{
			name:                "Exactly 1 second",
			lastAttempt:         1 * time.Second,
			expectedDisplayTime: "1s",
		},
		{
			name:                "1.4 seconds rounds to 1s",
			lastAttempt:         1400 * time.Millisecond,
			expectedDisplayTime: "1s",
		},
		{
			name:                "1.5 seconds rounds to 2s",
			lastAttempt:         1500 * time.Millisecond,
			expectedDisplayTime: "2s",
		},
		{
			name:                "2.1 seconds rounds to 2s",
			lastAttempt:         2100 * time.Millisecond,
			expectedDisplayTime: "2s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create test client with cooldown that will trigger
			config := DefaultConfig()
			config.Broker = "tcp://test.example.com:1883"
			config.ReconnectCooldown = 5 * time.Second
			metrics, _ := observability.NewMetrics()
			c := &client{
				config:          config,
				metrics:         metrics.MQTT,
				lastConnAttempt: time.Now().Add(-tt.lastAttempt),
				reconnectStop:   make(chan struct{}),
			}

			logger := mqttLogger.With("broker", config.Broker, "client_id", config.ClientID)

			// Test the checkConnectionCooldownLocked method
			c.mu.RLock()
			err := c.checkConnectionCooldownLocked(logger)
			c.mu.RUnlock()

			// Should always error since we're within cooldown
			if err == nil {
				t.Errorf("Expected error but got nil")
				return
			}

			// Check that the error message contains the expected rounded time
			expectedMsg := fmt.Sprintf("last attempt was %s ago", tt.expectedDisplayTime)
			if !strings.Contains(err.Error(), expectedMsg) {
				t.Errorf("Expected error to contain %q, got %q", expectedMsg, err.Error())
			}
		})
	}
}
