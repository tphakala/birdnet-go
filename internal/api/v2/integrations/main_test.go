package integrations

import (
	"os"
	"testing"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
)

// TestMain disables HTTP keep-alives before any test runs. The integration
// test-connection handlers spin up real HTTP clients (eBird/weather probes) and
// MQTT clients (broker connection attempts); disabling keep-alives prevents
// persistent-connection goroutines from outliving the tests, matching the
// package api test harness this domain was extracted from.
func TestMain(m *testing.M) {
	apitest.DisableHTTPKeepAlivesForTesting()
	os.Exit(m.Run())
}
