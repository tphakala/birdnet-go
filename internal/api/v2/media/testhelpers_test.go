package media

import (
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// testFailFastTimeout is a short timeout injected into deliberate-failure waits
// (an audio file that never appears) so those tests reach the expected timeout
// state quickly instead of waiting the full production defaults. It is well above
// any local/CI scheduling latency while staying far below the production timeouts.
const testFailFastTimeout = 200 * time.Millisecond

// passthroughMiddleware returns a no-op auth middleware used so the
// authentication-protected media routes (clip extraction, audio processing) can
// be registered and exercised in tests without a real auth service.
func passthroughMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			return next(c)
		}
	}
}

// withRestoredGlobalSettings snapshots the process-global settings and restores
// them on cleanup, so a test that publishes a divergent snapshot does not leak it
// into sibling tests.
func withRestoredGlobalSettings(t *testing.T) {
	t.Helper()
	orig := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(orig) })
}
