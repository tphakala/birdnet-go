// client_test.go: Package mqtt provides an MQTT client implementation and associated tests.

package mqtt

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// Add this helper function at the top of the file
func isMosquittoTestServerAvailable() bool {
	conn, err := net.DialTimeout("tcp", "test.mosquitto.org:1883", 5*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// TestMQTTClient runs a suite of tests for the MQTT client implementation.
// It covers basic functionality, error handling, reconnection scenarios, and metrics collection.
func TestMQTTClient(t *testing.T) {
	mosquittoAvailable := isMosquittoTestServerAvailable()
	if !mosquittoAvailable {
		t.Skip("Skipping MQTT tests: test.mosquitto.org is not available")
	}

	t.Run("Basic Functionality", testBasicFunctionality)
	t.Run("Incorrect Broker Address", testIncorrectBrokerAddress)
	t.Run("Connection Loss Before Publish", testConnectionLossBeforePublish)
	t.Run("Publish While Disconnected", testPublishWhileDisconnected)
	t.Run("Reconnection With Backoff", testReconnectionWithBackoff)
	t.Run("Metrics Collection", testMetricsCollection)
}

// testBasicFunctionality verifies the basic operations of the MQTT client:
// connection, publishing a message, and disconnection.
func testBasicFunctionality(t *testing.T) {
	mqttClient, _ := createTestClient(t, "tcp://test.mosquitto.org:1883")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := mqttClient.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect to MQTT broker: %v", err)
	}

	if !mqttClient.IsConnected() {
		t.Fatal("Client is not connected after successful connection")
	}

	err = mqttClient.Publish(ctx, "birdnet-go/test", "Hello, MQTT!")
	if err != nil {
		t.Fatalf("Failed to publish message: %v", err)
	}

	time.Sleep(2 * time.Second)

	mqttClient.Disconnect()

	if mqttClient.IsConnected() {
		t.Fatal("Client is still connected after disconnection")
	}
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
	mqttClient, _ := createTestClient(t, "tcp://test.mosquitto.org:1883")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := mqttClient.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect to MQTT broker: %v", err)
	}

	// Simulate connection loss
	mqttClient.Disconnect()

	err = mqttClient.Publish(ctx, "birdnet-go/test", "Hello after reconnect!")
	if err == nil {
		t.Fatal("Expected publish to fail after connection loss")
	}

	// Allow time for potential reconnection attempts
	time.Sleep(5 * time.Second)

	if mqttClient.IsConnected() {
		t.Fatal("Client should not be connected after forced disconnection")
	}
}

// testPublishWhileDisconnected attempts to publish a message while the client is disconnected.
// It verifies that the publish operation fails when the client is not connected to a broker.
func testPublishWhileDisconnected(t *testing.T) {
	mqttClient, _ := createTestClient(t, "tcp://test.mosquitto.org:1883")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := mqttClient.Publish(ctx, "birdnet-go/test", "This should fail")
	if err == nil {
		t.Fatal("Expected publish to fail when not connected")
	}
}

// testReconnectionWithBackoff verifies the client's reconnection behavior with a backoff mechanism.
// It simulates a connection loss, attempts an immediate reconnection (which should fail due to cooldown),
// waits for the cooldown period, and then attempts another reconnection which should succeed.
func testReconnectionWithBackoff(t *testing.T) {
	mqttClient, _ := createTestClient(t, "tcp://test.mosquitto.org:1883")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := mqttClient.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect to MQTT broker: %v", err)
	}

	// Simulate connection loss
	mqttClient.Disconnect()

	// Wait for a short period (less than the cooldown)
	time.Sleep(2 * time.Second)

	// Attempt reconnection (this should fail due to cooldown)
	err = mqttClient.Connect(ctx)
	if err == nil {
		t.Fatal("Expected reconnection to fail due to cooldown")
	}

	// Wait for the cooldown period
	time.Sleep(3 * time.Second)

	// Attempt reconnection again (this should succeed)
	err = mqttClient.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to reconnect after cooldown: %v", err)
	}

	if !mqttClient.IsConnected() {
		t.Fatal("Client failed to reconnect after simulated connection loss")
	}
}

// testMetricsCollection checks the collection and accuracy of various metrics related to
// MQTT client operations, including connection status, message delivery, and error counts.
func testMetricsCollection(t *testing.T) {
	mqttClient, metrics := createTestClient(t, "tcp://test.mosquitto.org:1883")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Connect to the broker
	err := mqttClient.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect to MQTT broker: %v", err)
	}

	// Check initial connection status
	connectionStatus := getGaugeValue(t, metrics.MQTT.ConnectionStatus)
	if connectionStatus != 1 {
		t.Errorf("Initial connection status metric incorrect. Expected 1, got %v", connectionStatus)
	}

	// Publish a message and check delivery metric
	err = mqttClient.Publish(ctx, "birdnet-go/test", "Test message")
	if err != nil {
		t.Fatalf("Failed to publish message: %v", err)
	}
	time.Sleep(time.Second) // Allow time for metric to update
	messagesDelivered := getCounterValue(t, metrics.MQTT.MessagesDelivered)
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

// createTestClient is a helper function that creates and configures an MQTT client for testing purposes.
// It sets up the client with the provided broker address, a custom reconnect cooldown period, and metrics.
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
		t.Fatalf("Failed to create metrics: %v", err)
	}
	client, err := NewClient(testSettings, metrics)
	if err != nil {
		t.Fatalf("Failed to create MQTT client: %v", err)
	}
	return client, metrics
}
