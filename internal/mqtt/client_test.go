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

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

const (
	defaultTestBroker = "tcp://test.mosquitto.org:1883"
	localTestBroker   = "tcp://localhost:1883"
	testQoS           = 1 // Use QoS 1 for more reliable delivery
	testTimeout       = 45 * time.Second
	testTopic         = "birdnet-go/test"
)

// getBrokerAddress returns the MQTT broker address to use for testing
func getBrokerAddress() string {
	if broker := os.Getenv("MQTT_TEST_BROKER"); broker != "" {
		return broker
	}
	if isMosquittoTestServerAvailable() {
		return defaultTestBroker
	}
	return localTestBroker
}

// isMosquittoTestServerAvailable checks if the public test server is available and responding properly
func isMosquittoTestServerAvailable() bool {
	// Try multiple times as the server might be temporarily busy
	for i := 0; i < 3; i++ {
		conn, err := net.DialTimeout("tcp", "test.mosquitto.org:1883", 5*time.Second)
		if err == nil {
			conn.Close()
			// Try a quick connect/disconnect to verify server responsiveness
			client, _ := createTestClient(nil, defaultTestBroker)
			if client == nil {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := client.Connect(ctx); err == nil {
				client.Disconnect()
				cancel()
				return true
			}
			cancel()
		}
		time.Sleep(time.Second)
	}
	return false
}

// Add debug logging helper
func debugLog(t *testing.T, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("[DEBUG] %s", msg)
	t.Logf("[DEBUG] %s", msg)
}

// retryWithTimeout attempts an operation with retries until it succeeds or times out
func retryWithTimeout(timeout time.Duration, operation func() error) error {
	deadline := time.Now().Add(timeout)
	backoff := 100 * time.Millisecond
	maxBackoff := 2 * time.Second
	var lastErr error
	attempts := 0

	for time.Now().Before(deadline) {
		attempts++
		if err := operation(); err != nil {
			lastErr = err
			log.Printf("[DEBUG] Retry attempt %d failed: %v", attempts, err)
			// Exponential backoff with jitter
			jitter := time.Duration(rand.Int63n(int64(backoff / 2)))
			sleepTime := backoff + jitter
			log.Printf("[DEBUG] Sleeping for %v before next retry", sleepTime)
			time.Sleep(sleepTime)
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		log.Printf("[DEBUG] Operation succeeded after %d attempts", attempts)
		return nil
	}
	return fmt.Errorf("operation timed out after %v and %d attempts, last error: %w", timeout, attempts, lastErr)
}

// TestMQTTClient runs a suite of tests for the MQTT client implementation.
// It covers basic functionality, error handling, reconnection scenarios, and metrics collection.
func TestMQTTClient(t *testing.T) {
	broker := getBrokerAddress()
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
	debugLog(t, "Starting Basic Functionality test")
	mqttClient, _ := createTestClient(t, getBrokerAddress())

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	debugLog(t, "Attempting initial connection")
	// Try to connect with retries and longer timeout
	err := retryWithTimeout(30*time.Second, func() error {
		return mqttClient.Connect(ctx)
	})
	if err != nil {
		t.Fatalf("Failed to connect to MQTT broker after retries: %v", err)
	}

	if !mqttClient.IsConnected() {
		t.Fatal("Client is not connected after successful connection")
	}
	debugLog(t, "Successfully connected to broker")

	// Try to publish with retries and longer timeout
	debugLog(t, "Attempting to publish message")
	err = retryWithTimeout(20*time.Second, func() error {
		if !mqttClient.IsConnected() {
			return fmt.Errorf("client disconnected before publish")
		}
		return mqttClient.Publish(ctx, testTopic, "Hello, MQTT!")
	})
	if err != nil {
		debugLog(t, "Warning: Publish failed: %v", err)
		t.Logf("Warning: Publish failed, this might be due to broker limitations: %v", err)
		return // Skip further tests if publish fails
	}
	debugLog(t, "Successfully published message")

	debugLog(t, "Waiting for message processing")
	time.Sleep(2 * time.Second)

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
	mqttClient, _ := createTestClient(t, getBrokerAddress())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	mqttClient, _ := createTestClient(t, getBrokerAddress())

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
	mqttClient, _ := createTestClient(t, getBrokerAddress())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	debugLog(t, "Attempting initial connection")
	err := mqttClient.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect to MQTT broker: %v", err)
	}
	debugLog(t, "Successfully connected to broker")

	debugLog(t, "Simulating connection loss")
	mqttClient.Disconnect()

	debugLog(t, "Waiting short period before reconnection attempt")
	time.Sleep(2 * time.Second)

	debugLog(t, "Attempting immediate reconnection (should fail due to cooldown)")
	err = mqttClient.Connect(ctx)
	if err == nil {
		t.Fatal("Expected reconnection to fail due to cooldown")
	}
	debugLog(t, "Immediate reconnection failed as expected with error: %v", err)

	debugLog(t, "Waiting for cooldown period")
	time.Sleep(3 * time.Second)

	debugLog(t, "Attempting reconnection after cooldown")
	err = mqttClient.Connect(ctx)
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
	mqttClient, metrics := createTestClient(t, getBrokerAddress())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Connect with retries
	debugLog(t, "Attempting to connect with retries")
	err := retryWithTimeout(20*time.Second, func() error {
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
	t.Logf("Error count: %v", getCounterValue(t, metrics.MQTT.Errors))
	t.Logf("Reconnect attempts: %v", getCounterValue(t, metrics.MQTT.ReconnectAttempts))
	t.Logf("Publish latency: %v", getHistogramValue(t, metrics.MQTT.PublishLatency))
	debugLog(t, "Metrics Collection test completed")
}

// Add this helper function to get Histogram values
func getHistogramValue(t *testing.T, histogram prometheus.Histogram) float64 {
	var metric dto.Metric
	err := histogram.Write(&metric)
	if err != nil {
		t.Fatalf("Failed to write metric: %v", err)
	}
	return metric.Histogram.GetSampleSum()
}

// Helper function to get the value of a Gauge metric
func getGaugeValue(t *testing.T, gauge prometheus.Gauge) float64 {
	var metric dto.Metric
	err := gauge.Write(&metric)
	if err != nil {
		t.Fatalf("Failed to write metric: %v", err)
	}
	return *metric.Gauge.Value
}

// Helper function to get the value of a Counter metric
func getCounterValue(t *testing.T, counter prometheus.Counter) float64 {
	var metric dto.Metric
	err := counter.Write(&metric)
	if err != nil {
		t.Fatalf("Failed to write metric: %v", err)
	}
	return *metric.Counter.Value
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
		mqttClient, _ := createTestClient(t, getBrokerAddress())

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
		mqttClient, _ := createTestClient(t, getBrokerAddress())

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

// createTestClient is a helper function that creates and configures an MQTT client for testing purposes.
func createTestClient(t *testing.T, broker string) (Client, *telemetry.Metrics) {
	testSettings := &conf.Settings{
		Main: struct {
			Name      string
			TimeAs24h bool
			Log       conf.LogConfig
		}{
			Name: "TestNode",
		},
		Realtime: conf.RealtimeSettings{
			MQTT: conf.MQTTSettings{
				Broker:   broker,
				Username: "",
				Password: "",
			},
		},
	}
	metrics, err := telemetry.NewMetrics()
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
