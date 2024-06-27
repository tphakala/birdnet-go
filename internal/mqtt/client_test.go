// client_test.go: Package mqtt provides an MQTT client implementation and associated tests.

package mqtt

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestMQTTClient runs a suite of tests for the MQTT client implementation.
// It covers basic functionality, error handling, and reconnection scenarios.
func TestMQTTClient(t *testing.T) {
	t.Run("Basic Functionality", testBasicFunctionality)
	t.Run("Incorrect Broker Address", testIncorrectBrokerAddress)
	t.Run("Connection Loss Before Publish", testConnectionLossBeforePublish)
	t.Run("Publish While Disconnected", testPublishWhileDisconnected)
	t.Run("Reconnection With Backoff", testReconnectionWithBackoff)
}

// testBasicFunctionality verifies the basic operations of the MQTT client:
// connection, publishing a message, and disconnection.
func testBasicFunctionality(t *testing.T) {
	mqttClient := createTestClient(t, "tcp://test.mosquitto.org:1883")

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
		mqttClient := createTestClient(t, "tcp://unresolvable.invalid:1883")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		err := mqttClient.Connect(ctx)

		if err == nil {
			t.Fatal("Expected connection to fail with invalid broker address")
		}

		dnsErr, ok := err.(*net.DNSError)
		if !ok {
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
		mqttClient := createTestClient(t, "tcp://256.0.0.1:1883") // Invalid IP address

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		err := mqttClient.Connect(ctx)

		if err == nil {
			t.Fatal("Expected connection to fail with invalid IP address")
		}

		// The error could be either a DNS error or a connection error
		if _, ok := err.(*net.DNSError); !ok {
			if _, ok := err.(net.Error); !ok {
				t.Fatalf("Expected either a DNS error or a net.Error, got: %v", err)
			}
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
	mqttClient := createTestClient(t, "tcp://test.mosquitto.org:1883")

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
	mqttClient := createTestClient(t, "tcp://test.mosquitto.org:1883")

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
	mqttClient := createTestClient(t, "tcp://test.mosquitto.org:1883")

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

// createTestClient is a helper function that creates and configures an MQTT client for testing purposes.
// It sets up the client with the provided broker address and a custom reconnect cooldown period.
func createTestClient(t *testing.T, broker string) Client {
	testSettings := &conf.Settings{
		Main: struct {
			Name      string
			TimeAs24h bool
			Log       conf.LogConfig
		}{
			Name: "TestNode", // Set a default name for testing
		},
		Realtime: conf.RealtimeSettings{
			MQTT: conf.MQTTSettings{
				Broker:   broker,
				Username: "",
				Password: "",
			},
		},
	}
	client := NewClient(testSettings).(*client) // type assertion
	// We can adjust the cooldown for testing if needed
	client.config.ReconnectCooldown = 5 * time.Second
	return client
}
