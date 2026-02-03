package mqtt

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// tlsFileTestCase defines a test case for TLS file existence checks
type tlsFileTestCase struct {
	name          string
	tlsSettings   conf.MQTTTLSSettings
	expectedError string
}

// createDummyFilesForTLSTest creates required dummy files for specific test cases
func createDummyFilesForTLSTest(t *testing.T, testName, tempDir string) {
	t.Helper()
	switch testName {
	case "Non-existent client key":
		certPath := filepath.Join(tempDir, "client.crt")
		err := os.WriteFile(certPath, []byte("dummy cert"), 0o600)
		require.NoError(t, err, "Failed to create dummy cert file")
	case "Non-existent client certificate":
		keyPath := filepath.Join(tempDir, "client.key")
		err := os.WriteFile(keyPath, []byte("dummy key"), 0o600)
		require.NoError(t, err, "Failed to create dummy key file")
	}
}

// runTLSFileExistenceTest executes a single TLS file existence test case
func runTLSFileExistenceTest(t *testing.T, tc *tlsFileTestCase, tempDir string, metrics *observability.Metrics) {
	t.Helper()

	createDummyFilesForTLSTest(t, tc.name, tempDir)

	settings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			MQTT: conf.MQTTSettings{
				Enabled: true,
				Broker:  "tls://localhost:8883",
				Topic:   "birdnet-go/test",
				TLS:     tc.tlsSettings,
			},
		},
	}

	client, err := NewClient(settings, metrics)
	require.NoError(t, err, "Failed to create MQTT client")
	defer client.Disconnect()

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()

	err = client.Connect(ctx)
	require.Error(t, err, "Expected connection to fail due to missing certificate files")

	assert.Contains(t, err.Error(), tc.expectedError)
}

// TestTLSFileExistenceChecks verifies that the MQTT client provides helpful
// error messages when TLS certificate files don't exist
func TestTLSFileExistenceChecks(t *testing.T) {
	t.Parallel()

	broker := getBrokerAddress()
	if broker == "" {
		t.Skip("No MQTT broker configured for testing")
	}

	tempDir := t.TempDir()

	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to create metrics")

	tests := []tlsFileTestCase{
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
				ClientKey:  filepath.Join(tempDir, "client.key"),
			},
			expectedError: "client certificate file does not exist",
		},
		{
			name: "Non-existent client key",
			tlsSettings: conf.MQTTTLSSettings{
				Enabled:    true,
				ClientCert: filepath.Join(tempDir, "client.crt"),
				ClientKey:  filepath.Join(tempDir, "non-existent-client.key"),
			},
			expectedError: "client key file does not exist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runTLSFileExistenceTest(t, &tt, tempDir, metrics)
		})
	}
}
