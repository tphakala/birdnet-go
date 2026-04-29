package security

import (
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// NewOAuth2ServerForTesting creates an OAuth2Server with the provided settings
// for testing. It publishes the settings as the global test snapshot so that
// currentSettings() returns the expected values, and registers cleanup to
// restore the global after the test finishes.
func NewOAuth2ServerForTesting(tb testing.TB, settings *conf.Settings) *OAuth2Server {
	tb.Helper()
	conf.SetTestSettings(settings)
	tb.Cleanup(func() { conf.SetTestSettings(nil) })
	return &OAuth2Server{
		Settings:          settings,
		authCodes:         make(map[string]AuthCode),
		accessTokens:      make(map[string]AccessToken),
		throttledMessages: make(map[string]time.Time),
	}
}
