package security

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// NewOAuth2ServerForTest is a lightweight settings-injecting OAuth2Server
// constructor that initializes the in-memory maps without starting the
// auth-cleanup goroutine or initializing goth. It is intended for tests and
// dependency injection.
func NewOAuth2ServerForTest(settings *conf.Settings) *OAuth2Server {
	return &OAuth2Server{
		settings:          settings,
		authCodes:         make(map[string]AuthCode),
		accessTokens:      make(map[string]AccessToken),
		throttledMessages: make(map[string]time.Time),
	}
}
