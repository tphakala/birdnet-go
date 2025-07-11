package mqtt

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// TestTLSFileExistenceChecks verifies that the MQTT client provides helpful
// error messages when TLS certificate files don't exist
func TestTLSFileExistenceChecks(t *testing.T) {
	t.Parallel()
	// Skip if no broker is available
	broker := getBrokerAddress()
	if broker == "" {
		t.Skip("No MQTT broker configured for testing")
	}

	// Create a temporary directory for non-existent certificate paths
	tempDir := t.TempDir()

	metrics, err := observability.NewMetrics()
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}

	tests := []struct {
		name          string
		tlsSettings   conf.MQTTTLSSettings
		expectedError string
	}{
		{
			name: "Non-existent CA certificate",
			tlsSettings: conf.MQTTTLSSettings{
				Enabled: true,
				CACert:  filepath.Join(tempDir, "non-existent-ca.crt"),
			},
			expectedError: "CA certificate file does not exist",
		},
		{
			name: "Non-existent client certificate",
			tlsSettings: conf.MQTTTLSSettings{
				Enabled:    true,
				ClientCert: filepath.Join(tempDir, "non-existent-client.crt"),
				ClientKey:  filepath.Join(tempDir, "client.key"), // Create this so we test cert check first
			},
			expectedError: "client certificate file does not exist",
		},
		{
			name: "Non-existent client key",
			tlsSettings: conf.MQTTTLSSettings{
				Enabled:    true,
				ClientCert: filepath.Join(tempDir, "client.crt"), // Create this so we test key check
				ClientKey:  filepath.Join(tempDir, "non-existent-client.key"),
			},
			expectedError: "client key file does not exist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create any files that should exist for the test
			switch tt.name {
			case "Non-existent client key":
				// Create a dummy client certificate file
				certPath := filepath.Join(tempDir, "client.crt")
				if err := os.WriteFile(certPath, []byte("dummy cert"), 0o600); err != nil {
					t.Fatalf("Failed to create dummy cert file: %v", err)
				}
			case "Non-existent client certificate":
				// Create a dummy key file
				keyPath := filepath.Join(tempDir, "client.key")
				if err := os.WriteFile(keyPath, []byte("dummy key"), 0o600); err != nil {
					t.Fatalf("Failed to create dummy key file: %v", err)
				}
			}

			settings := &conf.Settings{
				Main: struct {
					Name      string
					TimeAs24h bool
					Log       conf.LogConfig
				}{
					Name: "TestNode-FileCheck",
				},
				Realtime: conf.RealtimeSettings{
					MQTT: conf.MQTTSettings{
						Enabled: true,
						Broker:  "tls://localhost:8883", // Use TLS broker URL
						Topic:   "birdnet-go/test",
						TLS:     tt.tlsSettings,
					},
				},
			}

			client, err := NewClient(settings, metrics)
			if err != nil {
				t.Fatalf("Failed to create MQTT client: %v", err)
			}
			defer client.Disconnect()

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			// Attempt to connect - should fail with helpful error message
			err = client.Connect(ctx)
			if err == nil {
				t.Fatal("Expected connection to fail due to missing certificate files")
			}

			// Check that the error message contains the expected text
			assert.Contains(t, err.Error(), tt.expectedError)
		})
	}
}
