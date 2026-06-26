package api

import (
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	tlspkg "github.com/tphakala/birdnet-go/internal/tls"
)

// generateTestCertKeyPair creates a valid self-signed cert+key pair for testing.
// Shared by the MQTT TLS handler tests (mqtt_tls_test.go). The standalone TLS
// domain has its own copy in internal/api/v2/tls; this one stays here for the
// integrations (MQTT) handlers that still live in package api.
func generateTestCertKeyPair(t *testing.T) (certPEM, keyPEM string) {
	t.Helper()
	cert, key, err := tlspkg.GenerateSelfSigned(tlspkg.SelfSignedOptions{
		Validity: 24 * time.Hour,
		SANs:     []string{"localhost", "127.0.0.1"},
	})
	require.NoError(t, err, "failed to generate test certificate")
	return cert, key
}

// setupTLSTestEnvironment creates a test environment with a TLS manager pointing
// at a temporary directory, used by the MQTT TLS handler tests. It builds the
// full *Controller (the MQTT TLS handlers are facade methods) and overrides the
// global TLS manager for the duration of the test.
func setupTLSTestEnvironment(t *testing.T) (*echo.Echo, *Controller, *conf.TLSManager) {
	t.Helper()

	e, _, controller := setupTestEnvironment(t)

	// Create a TLS manager using a temp directory
	tlsDir := t.TempDir()
	tlsMgr := conf.NewTLSManager(tlsDir)

	// Override the global TLS manager for the duration of this test
	origManager := conf.GetTLSManager()
	conf.SetTLSManagerForTest(tlsMgr)
	t.Cleanup(func() {
		conf.SetTLSManagerForTest(origManager)
	})

	// Disable settings persistence in tests
	controller.DisableSaveSettings = true

	return e, controller, tlsMgr
}
