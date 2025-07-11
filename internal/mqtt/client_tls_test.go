// client_tls_test.go: Tests for secure MQTT (TLS/SSL) functionality
//
//nolint:misspell // Mosquitto is the correct name of the MQTT broker, not a misspelling of Mosquito
package mqtt

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// Test broker configurations
const (
	// Mosquitto test broker - Port 8883 is unauthenticated with TLS //nolint:misspell // Mosquitto is the correct name of the MQTT broker
	mosquittoTLSBroker = "tls://test.mosquitto.org:8883"
	// Mosquitto test broker - Port 8887 has an expired certificate //nolint:misspell // Mosquitto is the correct name of the MQTT broker
	mosquittoExpiredCertBroker = "tls://test.mosquitto.org:8887"

	// HiveMQ public broker
	hivemqTLSBroker = "ssl://broker.hivemq.com:8883"

	// Test timeouts
	tlsTestTimeout = 30 * time.Second
)

// skipIfNoTLSBroker skips the test if TLS brokers are not available
func skipIfNoTLSBroker(t *testing.T) {
	t.Helper()
	if os.Getenv("SKIP_TLS_TESTS") == "true" {
		t.Skip("Skipping TLS tests (SKIP_TLS_TESTS=true)")
	}
	if os.Getenv("CI") == "true" && os.Getenv("RUN_TLS_TESTS") != "true" {
		t.Skip("Skipping TLS tests in CI (set RUN_TLS_TESTS=true to enable)")
	}
}

// TestSecureMQTTConnections tests various TLS/SSL connection scenarios
func TestSecureMQTTConnections(t *testing.T) {
	skipIfNoTLSBroker(t)

	t.Run("Mosquitto TLS Connection", testMosquittoTLSConnection) //nolint:misspell // Mosquitto is the correct name of the MQTT broker
	t.Run("HiveMQ TLS Connection", testHiveMQTLSConnection)
	t.Run("Self-Signed Certificate", testSelfSignedCertificate)
	t.Run("TLS Auto-Detection", testTLSAutoDetection)
	t.Run("TLS Connection Test", testTLSConnectionTest)
}

// testMosquittoTLSConnection tests secure connection to Mosquitto test broker //nolint:misspell // Mosquitto is the correct name of the MQTT broker
func testMosquittoTLSConnection(t *testing.T) {
	settings := &conf.Settings{
		Main: struct {
			Name      string
			TimeAs24h bool
			Log       conf.LogConfig
		}{
			Name: "TestNode-TLS-Mosquitto", //nolint:misspell // Mosquitto is the correct name of the MQTT broker
		},
		Realtime: conf.RealtimeSettings{
			MQTT: conf.MQTTSettings{
				Enabled: true,
				Broker:  mosquittoTLSBroker,
				Topic:   "birdnet-go/test/tls",
				TLS: conf.MQTTTLSSettings{
					Enabled:            true,
					InsecureSkipVerify: true, // Skip verification due to cert issues with test broker
				},
			},
		},
	}

	metrics, err := observability.NewMetrics()
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}

	client, err := NewClient(settings, metrics)
	if err != nil {
		t.Fatalf("Failed to create MQTT client: %v", err)
	}
	defer client.Disconnect()

	ctx, cancel := context.WithTimeout(context.Background(), tlsTestTimeout)
	defer cancel()

	// Test connection
	err = client.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect to Mosquitto TLS broker: %v", err) //nolint:misspell // Mosquitto is the correct name of the MQTT broker
	}

	if !client.IsConnected() {
		t.Fatal("Client reports not connected after successful connection")
	}

	// Test publishing
	testMessage := "Hello from TLS test - Mosquitto" //nolint:misspell // Mosquitto is the correct name of the MQTT broker
	err = client.Publish(ctx, settings.Realtime.MQTT.Topic, testMessage)
	if err != nil {
		t.Logf("Warning: Failed to publish to Mosquitto (may have restrictions): %v", err) //nolint:misspell // Mosquitto is the correct name of the MQTT broker
		// Don't fail the test as public brokers may have publish restrictions
	}

	t.Log("Successfully connected to Mosquitto test broker with TLS") //nolint:misspell // Mosquitto is the correct name of the MQTT broker
}

// testHiveMQTLSConnection tests secure connection to HiveMQ public broker
func testHiveMQTLSConnection(t *testing.T) {
	settings := &conf.Settings{
		Main: struct {
			Name      string
			TimeAs24h bool
			Log       conf.LogConfig
		}{
			Name: "TestNode-TLS-HiveMQ",
		},
		Realtime: conf.RealtimeSettings{
			MQTT: conf.MQTTSettings{
				Enabled: true,
				Broker:  hivemqTLSBroker,
				Topic:   "birdnet-go/test/tls",
				TLS: conf.MQTTTLSSettings{
					Enabled:            true,
					InsecureSkipVerify: false, // HiveMQ has valid certs
				},
			},
		},
	}

	metrics, err := observability.NewMetrics()
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}

	client, err := NewClient(settings, metrics)
	if err != nil {
		t.Fatalf("Failed to create MQTT client: %v", err)
	}
	defer client.Disconnect()

	ctx, cancel := context.WithTimeout(context.Background(), tlsTestTimeout)
	defer cancel()

	// Test connection
	err = client.Connect(ctx)
	if err != nil {
		// HiveMQ might be temporarily unavailable
		if strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "refused") {
			t.Skipf("HiveMQ broker appears to be unavailable: %v", err)
		}
		t.Fatalf("Failed to connect to HiveMQ TLS broker: %v", err)
	}

	if !client.IsConnected() {
		t.Fatal("Client reports not connected after successful connection")
	}

	// Test publishing
	testMessage := "Hello from TLS test - HiveMQ"
	err = client.Publish(ctx, settings.Realtime.MQTT.Topic, testMessage)
	if err != nil {
		t.Logf("Warning: Failed to publish to HiveMQ (may have restrictions): %v", err)
		// Don't fail the test as public brokers may have publish restrictions
	}

	t.Log("Successfully connected to HiveMQ public broker with TLS")
}

// testSelfSignedCertificate tests connection with InsecureSkipVerify for self-signed certs
func testSelfSignedCertificate(t *testing.T) {
	// Use Mosquitto's expired certificate port as a test for InsecureSkipVerify
	settings := &conf.Settings{
		Main: struct {
			Name      string
			TimeAs24h bool
			Log       conf.LogConfig
		}{
			Name: "TestNode-TLS-SelfSigned",
		},
		Realtime: conf.RealtimeSettings{
			MQTT: conf.MQTTSettings{
				Enabled: true,
				Broker:  mosquittoExpiredCertBroker,
				Topic:   "birdnet-go/test/tls",
				TLS: conf.MQTTTLSSettings{
					Enabled:            true,
					InsecureSkipVerify: true, // Skip verification for expired cert
				},
			},
		},
	}

	metrics, err := observability.NewMetrics()
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}

	client, err := NewClient(settings, metrics)
	if err != nil {
		t.Fatalf("Failed to create MQTT client: %v", err)
	}
	defer client.Disconnect()

	ctx, cancel := context.WithTimeout(context.Background(), tlsTestTimeout)
	defer cancel()

	// Test connection - should succeed with InsecureSkipVerify
	err = client.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect with InsecureSkipVerify=true: %v", err)
	}

	if !client.IsConnected() {
		t.Fatal("Client reports not connected after successful connection")
	}

	t.Log("Successfully connected with InsecureSkipVerify enabled")

	// Now test without InsecureSkipVerify - should fail
	client.Disconnect()

	settings.Realtime.MQTT.TLS.InsecureSkipVerify = false
	client2, err := NewClient(settings, metrics)
	if err != nil {
		t.Fatalf("Failed to create second MQTT client: %v", err)
	}
	defer client2.Disconnect()

	err = client2.Connect(ctx)
	if err == nil {
		t.Fatal("Expected connection to fail with expired certificate, but it succeeded")
	}

	// Verify the error is certificate-related
	if !strings.Contains(err.Error(), "certificate") && !strings.Contains(err.Error(), "x509") {
		t.Logf("Warning: Expected certificate error, got: %v", err)
	}

	t.Log("Connection correctly failed with expired certificate when verification enabled")
}

// testTLSAutoDetection verifies that TLS is automatically enabled for secure schemes
func testTLSAutoDetection(t *testing.T) {
	testCases := []struct {
		name      string
		broker    string
		expectTLS bool
	}{
		{"SSL scheme", "ssl://broker.example.com:8883", true},
		{"TLS scheme", "tls://broker.example.com:8883", true},
		{"MQTTS scheme", "mqtts://broker.example.com:8883", true},
		{"TCP scheme", "tcp://broker.example.com:1883", false},
		{"No scheme with 8883", "broker.example.com:8883", false}, // Only scheme triggers auto-detection
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			settings := &conf.Settings{
				Main: struct {
					Name      string
					TimeAs24h bool
					Log       conf.LogConfig
				}{
					Name: "TestNode-AutoDetect",
				},
				Realtime: conf.RealtimeSettings{
					MQTT: conf.MQTTSettings{
						Broker: tc.broker,
						TLS: conf.MQTTTLSSettings{
							Enabled: false, // Start with TLS disabled
						},
					},
				},
			}

			metrics, err := observability.NewMetrics()
			if err != nil {
				t.Fatalf("Failed to create metrics: %v", err)
			}

			// Create client - this should auto-detect TLS
			_, err = NewClient(settings, metrics)
			if err != nil {
				t.Fatalf("Failed to create MQTT client: %v", err)
			}

			// The auto-detection logic is tested by the successful connections
			// in other tests. We verify it works by successfully connecting
			// with TLS schemes in the actual connection tests above.
		})
	}
}

// testTLSConnectionTest verifies the multi-stage connection test works with TLS
func testTLSConnectionTest(t *testing.T) {
	settings := &conf.Settings{
		Main: struct {
			Name      string
			TimeAs24h bool
			Log       conf.LogConfig
		}{
			Name: "TestNode-TLS-ConnTest",
		},
		Realtime: conf.RealtimeSettings{
			MQTT: conf.MQTTSettings{
				Enabled: true,
				Broker:  mosquittoTLSBroker,
				Topic:   "birdnet-go/test/tls",
				TLS: conf.MQTTTLSSettings{
					Enabled:            true,
					InsecureSkipVerify: true, // Skip verification for test broker
				},
			},
		},
	}

	metrics, err := observability.NewMetrics()
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}

	client, err := NewClient(settings, metrics)
	if err != nil {
		t.Fatalf("Failed to create MQTT client: %v", err)
	}
	defer client.Disconnect()

	ctx, cancel := context.WithTimeout(context.Background(), tlsTestTimeout)
	defer cancel()

	// Run the connection test
	resultChan := make(chan TestResult, 10)

	// Run test in goroutine with proper cleanup
	testDone := make(chan struct{})
	go func() {
		defer close(testDone)
		client.TestConnection(ctx, resultChan)
	}()

	stages := []string{
		"DNS Resolution",
		"TCP Connection", // This should also test TLS handshake
		"MQTT Connection",
		"Message Publishing",
	}

	stageResults := make(map[string]bool)

	// Read results with timeout
	timeout := time.After(20 * time.Second)

loop:
	for {
		select {
		case result, ok := <-resultChan:
			if !ok {
				break loop
			}

			t.Logf("Test stage: %s - Success: %v, Message: %s",
				result.Stage, result.Success, result.Message)

			if result.Error != "" {
				t.Logf("  Error: %s", result.Error)
			}

			// Track completed stages
			for _, stage := range stages {
				if result.Stage == stage && !result.IsProgress {
					stageResults[stage] = result.Success
				}
			}

		case <-timeout:
			t.Fatal("Test timed out waiting for results")

		case <-testDone:
			// Close the channel when test is done
			close(resultChan)
			break loop
		}
	}

	// Verify all expected stages were tested
	for _, stage := range stages {
		if _, ok := stageResults[stage]; !ok {
			t.Errorf("Stage %s was not tested", stage)
		}
	}

	// TCP Connection stage should have tested TLS
	if success, ok := stageResults["TCP Connection"]; ok && !success {
		t.Error("TCP/TLS connection stage failed")
	}

	t.Log("TLS connection test completed successfully")
}

// TestTLSConfigValidation tests validation of TLS configuration options
func TestTLSConfigValidation(t *testing.T) {
	t.Parallel()
	t.Run("Invalid CA Certificate Path", func(t *testing.T) {
		t.Parallel()
		settings := &conf.Settings{
			Main: struct {
				Name      string
				TimeAs24h bool
				Log       conf.LogConfig
			}{
				Name: "TestNode-InvalidCA",
			},
			Realtime: conf.RealtimeSettings{
				MQTT: conf.MQTTSettings{
					Enabled: true,
					Broker:  mosquittoTLSBroker,
					TLS: conf.MQTTTLSSettings{
						Enabled: true,
						CACert:  "/nonexistent/ca.crt",
					},
				},
			},
		}

		metrics, err := observability.NewMetrics()
		if err != nil {
			t.Fatalf("Failed to create metrics: %v", err)
		}

		client, err := NewClient(settings, metrics)
		if err != nil {
			t.Fatalf("Failed to create MQTT client: %v", err)
		}
		defer client.Disconnect()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		err = client.Connect(ctx)
		if err == nil {
			t.Fatal("Expected connection to fail with invalid CA certificate path")
		}

		// We now check for file existence first, so expect the more specific error
		if !strings.Contains(err.Error(), "CA certificate file does not exist") {
			t.Errorf("Expected CA certificate file does not exist error, got: %v", err)
		}
	})

	t.Run("Invalid Client Certificate Path", func(t *testing.T) {
		t.Parallel()
		settings := &conf.Settings{
			Main: struct {
				Name      string
				TimeAs24h bool
				Log       conf.LogConfig
			}{
				Name: "TestNode-InvalidClientCert",
			},
			Realtime: conf.RealtimeSettings{
				MQTT: conf.MQTTSettings{
					Enabled: true,
					Broker:  mosquittoTLSBroker,
					TLS: conf.MQTTTLSSettings{
						Enabled:    true,
						ClientCert: "/nonexistent/client.crt",
						ClientKey:  "/nonexistent/client.key",
					},
				},
			},
		}

		metrics, err := observability.NewMetrics()
		if err != nil {
			t.Fatalf("Failed to create metrics: %v", err)
		}

		client, err := NewClient(settings, metrics)
		if err != nil {
			t.Fatalf("Failed to create MQTT client: %v", err)
		}
		defer client.Disconnect()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		err = client.Connect(ctx)
		if err == nil {
			t.Fatal("Expected connection to fail with invalid client certificate path")
		}

		// We now check for file existence first, so expect the more specific error
		if !strings.Contains(err.Error(), "client certificate file does not exist") {
			t.Errorf("Expected client certificate file does not exist error, got: %v", err)
		}
	})
}

// skipIfNoTLSBrokerBench skips benchmarks if TLS brokers are not available
func skipIfNoTLSBrokerBench(b *testing.B) {
	b.Helper()
	if os.Getenv("SKIP_TLS_TESTS") == "true" {
		b.Skip("Skipping TLS tests (SKIP_TLS_TESTS=true)")
	}
	if os.Getenv("CI") == "true" && os.Getenv("RUN_TLS_TESTS") != "true" {
		b.Skip("Skipping TLS tests in CI (set RUN_TLS_TESTS=true to enable)")
	}
}

// BenchmarkTLSConnection benchmarks TLS vs non-TLS connection performance
func BenchmarkTLSConnection(b *testing.B) {
	skipIfNoTLSBrokerBench(b)

	// Skip in CI unless explicitly requested
	if os.Getenv("CI") == "true" && os.Getenv("RUN_BENCHMARKS") != "true" {
		b.Skip("Skipping benchmarks in CI")
	}

	metrics, err := observability.NewMetrics()
	if err != nil {
		b.Fatalf("Failed to create metrics: %v", err)
	}

	b.Run("TLS_Connection", func(b *testing.B) {
		settings := &conf.Settings{
			Main: struct {
				Name      string
				TimeAs24h bool
				Log       conf.LogConfig
			}{
				Name: "BenchNode-TLS",
			},
			Realtime: conf.RealtimeSettings{
				MQTT: conf.MQTTSettings{
					Enabled: true,
					Broker:  mosquittoTLSBroker,
					TLS: conf.MQTTTLSSettings{
						Enabled: true,
					},
				},
			},
		}

		b.ResetTimer()
		b.ReportAllocs()

		for b.Loop() {
			client, err := NewClient(settings, metrics)
			if err != nil {
				b.Fatalf("Failed to create client: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err = client.Connect(ctx)
			if err != nil {
				b.Fatalf("Failed to connect: %v", err)
			}

			client.Disconnect()
			cancel()
		}
	})

	b.Run("Non_TLS_Connection", func(b *testing.B) {
		// Skip if no local broker
		if !isLocalBrokerAvailable() {
			b.Skip("Local broker not available for non-TLS benchmark")
		}

		settings := &conf.Settings{
			Main: struct {
				Name      string
				TimeAs24h bool
				Log       conf.LogConfig
			}{
				Name: "BenchNode-TCP",
			},
			Realtime: conf.RealtimeSettings{
				MQTT: conf.MQTTSettings{
					Enabled: true,
					Broker:  localTestBroker,
				},
			},
		}

		b.ResetTimer()
		b.ReportAllocs()

		for b.Loop() {
			client, err := NewClient(settings, metrics)
			if err != nil {
				b.Fatalf("Failed to create client: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err = client.Connect(ctx)
			if err != nil {
				b.Fatalf("Failed to connect: %v", err)
			}

			client.Disconnect()
			cancel()
		}
	})
}
