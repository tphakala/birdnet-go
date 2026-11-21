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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/security"
	"github.com/tphakala/birdnet-go/internal/security/securitytest"
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

	server := securitytest.NewOAuth2ServerForTesting(t, startup)

	// Simulate a UI save: enable basic auth with credentials and publish the new
	// snapshot through the atomic pointer (what conf.StoreSettings does on save).
	updated := &conf.Settings{}
	updated.Security.BasicAuth.Enabled = true
	updated.Security.BasicAuth.ClientID = "admin"
	updated.Security.BasicAuth.Password = "correct-horse"
	updated.Security.BasicAuth.AuthCodeExp = time.Minute
	conftest.SetTestSettings(updated)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	adapter := NewSecurityAdapter(server)

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	authCode, err := adapter.AuthenticateBasic(c, "admin", "correct-horse", false)
	require.NoError(t, err, "login should succeed with credentials configured via UI without restart")
	require.NotEmpty(t, authCode, "a non-empty auth code should be generated on success")
}

// TestSessionCheckRecordReusedByGetters verifies that GetUsername and
// GetAuthMethod read the record a successful session check left on the context
// instead of checking the session again: they still answer after the cookie is
// gone from the request, and answer nothing for a request with no session.
func TestSessionCheckRecordReusedByGetters(t *testing.T) {
	const user = "user@example.com"
	settings := &conf.Settings{Security: conf.Security{
		SessionSecret:  "test-secret",
		OAuthProviders: []conf.OAuthProviderConfig{{Provider: security.ConfigGoogle, Enabled: true, UserID: user}},
	}}
	server := securitytest.NewOAuth2ServerForTesting(t, settings)
	adapter := NewSecurityAdapter(server)

	signIn := httptest.NewRecorder()
	server.StartProviderSession(signIn, httptest.NewRequest(http.MethodGet, "/", http.NoBody),
		&security.AccessToken{Provider: security.ProviderGoogle, UserID: user, ProviderUserID: "sub"}, false)
	cookies := signIn.Result().Cookies()
	require.NotEmpty(t, cookies)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	c := echo.New().NewContext(req, httptest.NewRecorder())
	require.NoError(t, adapter.CheckAccess(c))

	req.Header.Del("Cookie")
	assert.Equal(t, user, adapter.GetUsername(c))
	assert.Equal(t, AuthMethodBrowserSession, adapter.GetAuthMethod(c))

	anonymous := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/", http.NoBody), httptest.NewRecorder())
	assert.Empty(t, adapter.GetUsername(anonymous))
	assert.Equal(t, AuthMethodNone, adapter.GetAuthMethod(anonymous))
}
