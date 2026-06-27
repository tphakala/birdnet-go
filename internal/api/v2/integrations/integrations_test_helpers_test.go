// integrations_test_helpers_test.go: shared test scaffolding for the api/v2
// integrations domain tests.
//
// Core-level scaffolding (settings builders, mock metrics, route assertions, the
// *apicore.Core builder) lives in the importable internal/api/v2/apitest package.
// The helpers here build the integrations Handler with in-memory test doubles for
// the facade's settings-save machinery, mirroring the TLS domain tests.

package integrations

import (
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
	tlspkg "github.com/tphakala/birdnet-go/internal/tls"
)

// newTestHandler builds an integrations Handler around a *apicore.Core with
// in-memory test doubles for the facade's settings-save machinery. The doubles
// mirror the production behavior: getSettingsOrFallback reads the core's atomic
// snapshot, publishAndSaveSettings stores the updated snapshot back (no disk
// write, matching the original DisableSaveSettings=true), and handleSettingsChanges
// is a no-op. They share the supplied mutex so the handler serialises just as the
// facade does.
func newTestHandler(t *testing.T, core *apicore.Core, mu *sync.RWMutex) *Handler {
	t.Helper()
	return New(core, mu,
		func() *conf.Settings { return core.Settings.Load() },
		func(_, updated *conf.Settings) error { core.Settings.Store(updated); return nil },
		func(_, _ *conf.Settings) error { return nil },
	)
}

// newIntegrationsTestHandler builds an Echo instance and an integrations Handler
// backed by an apitest core (Metrics plus published settings). The returned
// Handler reads through the same core whose Settings the test doubles mutate, so
// assertions observe the writes.
func newIntegrationsTestHandler(t *testing.T) (*echo.Echo, *Handler) {
	t.Helper()
	e := echo.New()
	core := apitest.NewCore(t, apitest.WithEcho(e))
	var mu sync.RWMutex
	return e, newTestHandler(t, core, &mu)
}

// generateTestCertKeyPair creates a valid self-signed cert+key pair for testing.
// Shared by the MQTT TLS handler tests (mqtt_tls_test.go).
func generateTestCertKeyPair(t *testing.T) (certPEM, keyPEM string) {
	t.Helper()
	cert, key, err := tlspkg.GenerateSelfSigned(tlspkg.SelfSignedOptions{
		Validity: 24 * time.Hour,
		SANs:     []string{"localhost", "127.0.0.1"},
	})
	require.NoError(t, err, "failed to generate test certificate")
	return cert, key
}

// setupTLSTestEnvironment creates an integrations Handler plus a TLS manager
// pointing at a temporary directory, used by the MQTT TLS handler tests. It
// overrides the global TLS manager for the duration of the test. The returned
// Handler reads through the same core whose Settings the test doubles mutate.
func setupTLSTestEnvironment(t *testing.T) (*echo.Echo, *Handler, *conf.TLSManager) {
	t.Helper()

	e, h := newIntegrationsTestHandler(t)

	// Create a TLS manager using a temp directory
	tlsDir := t.TempDir()
	tlsMgr := conf.NewTLSManager(tlsDir)

	// Override the global TLS manager for the duration of this test
	origManager := conf.GetTLSManager()
	conf.SetTLSManagerForTest(tlsMgr)
	t.Cleanup(func() {
		conf.SetTLSManagerForTest(origManager)
	})

	return e, h, tlsMgr
}
