// adapter_test.go: Regression tests for SecurityAdapter ensuring basic-auth
// credentials configured via the web UI take effect without a server restart
// (GitHub issue #3370). The bug was that AuthenticateBasic read the
// construction-time OAuth2Server.settings pointer instead of the live snapshot.

package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/security"
)

// TestAuthenticateBasicHotReloadAfterEnable reproduces issue #3370: a user starts
// BirdNET-Go without security, then enables basic auth and sets a password through
// the web portal. The save publishes a new settings snapshot via the atomic
// pointer, but the construction-time OAuth2Server.settings pointer remains the
// (disabled) startup snapshot. Login must succeed against the freshly configured
// credentials without restarting the process.
func TestAuthenticateBasicHotReloadAfterEnable(t *testing.T) {
	// Startup snapshot: basic auth disabled, no credentials. This becomes the
	// construction-time OAuth2Server.settings pointer.
	startup := &conf.Settings{}

	server := security.NewOAuth2ServerForTesting(t, startup)

	// Simulate a UI save: enable basic auth with credentials and publish the new
	// snapshot through the atomic pointer (what conf.StoreSettings does on save).
	updated := &conf.Settings{}
	updated.Security.BasicAuth.Enabled = true
	updated.Security.BasicAuth.ClientID = "admin"
	updated.Security.BasicAuth.Password = "correct-horse"
	updated.Security.BasicAuth.AuthCodeExp = time.Minute
	conf.SetTestSettings(updated)
	t.Cleanup(func() { conf.SetTestSettings(nil) })

	adapter := NewSecurityAdapter(server)

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	authCode, err := adapter.AuthenticateBasic(c, "admin", "correct-horse")
	require.NoError(t, err, "login should succeed with credentials configured via UI without restart")
	require.NotEmpty(t, authCode, "a non-empty auth code should be generated on success")
}
