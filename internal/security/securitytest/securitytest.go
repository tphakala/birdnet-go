// Package securitytest provides test-support helpers for the security package.
// It lives in its own importable package so that the helpers (and their
// "testing" import) are kept out of the production build.
package securitytest

import (
	"testing"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/security"
)

// NewOAuth2ServerForTesting creates an OAuth2Server with the provided settings
// for testing. It publishes the settings as the global test snapshot so that
// currentSettings() returns the expected values, and registers cleanup to
// restore the global after the test finishes.
func NewOAuth2ServerForTesting(tb testing.TB, settings *conf.Settings) *security.OAuth2Server {
	tb.Helper()
	conf.StoreSettings(settings)
	tb.Cleanup(func() { conf.StoreSettings(nil) })
	return security.NewOAuth2ServerForTest(settings)
}
