package security

import (
	"testing"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// newOAuth2ServerForTesting is the in-package counterpart of
// securitytest.NewOAuth2ServerForTesting. It publishes the settings as the
// global test snapshot so that currentSettings() returns the expected values,
// registers cleanup to restore the global after the test finishes, and
// delegates construction to the production NewOAuth2ServerForTest helper. It
// lives in a _test.go file so the "testing" import stays out of the production
// build, and it cannot reuse the securitytest subpackage because that would
// create an import cycle.
func newOAuth2ServerForTesting(tb testing.TB, settings *conf.Settings) *OAuth2Server {
	tb.Helper()
	conf.StoreSettings(settings)
	tb.Cleanup(func() { conf.StoreSettings(nil) })
	return NewOAuth2ServerForTest(settings)
}
