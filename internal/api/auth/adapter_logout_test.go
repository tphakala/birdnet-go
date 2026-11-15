package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/github"
	"github.com/markbates/goth/providers/openidConnect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
	"github.com/tphakala/birdnet-go/internal/security"
	"github.com/tphakala/birdnet-go/internal/security/securitytest"
)

const (
	logoutTestIDToken  = "id-token-must-not-be-logged"
	logoutTestQuery    = "post_logout_redirect_uri"
	logoutSkipMessage  = "OIDC sign-out at the identity provider skipped"
	logoutOIDCModule   = "security.oidc"
	logoutTestEndpoint = "idp.example.test/end-session"
)

// logoutRow describes one GetProviderLogoutURL scenario.
type logoutRow struct {
	name         string
	oidcEnabled  bool
	registerOIDC bool
	endSession   string
	authProvider string // value stored under auth_provider, empty for none
	legacyKey    bool   // store the provider's own session key instead of auth_provider
	registerGH   bool   // register a provider that has no RP-Initiated Logout
	noWarn       bool   // the log must hold no WARN line at all
	wantURL      bool
	wantSkipLogs int
	wantLevel    string
	wantSkipHost string
}

// TestGetProviderLogoutURL_ReportsSkippedSignOut pins that every way the OIDC
// provider sign-out is skipped is reported once on the OIDC console module, and
// that no log line carries the ID token or the end-session query string.
// Not parallel: it swaps the settings, the goth registry, gothic.Store and the global logger.
func TestGetProviderLogoutURL_ReportsSkippedSignOut(t *testing.T) {
	rows := []logoutRow{
		{name: "https endpoint returns URL", oidcEnabled: true, registerOIDC: true, endSession: "https://" + logoutTestEndpoint, authProvider: security.ProviderOIDC, wantURL: true},
		{name: "provider not registered", oidcEnabled: true, authProvider: security.ProviderOIDC, wantSkipLogs: 1, wantLevel: "WARN"},
		{name: "empty end-session endpoint", oidcEnabled: true, registerOIDC: true, authProvider: security.ProviderOIDC, wantSkipLogs: 1, wantLevel: "WARN"},
		{name: "http endpoint rejected", oidcEnabled: true, registerOIDC: true, endSession: "http://" + logoutTestEndpoint, authProvider: security.ProviderOIDC, wantSkipLogs: 1, wantLevel: "WARN", wantSkipHost: "idp.example.test"},
		{name: "no provider in session, OIDC enabled", oidcEnabled: true, wantSkipLogs: 1, wantLevel: "INFO"},
		{name: "no provider in session, OIDC disabled"},
		{name: "legacy session returns URL", oidcEnabled: true, registerOIDC: true, endSession: "https://" + logoutTestEndpoint, authProvider: security.ProviderOIDC, legacyKey: true, wantURL: true},
		{name: "legacy session with empty endpoint warns once", oidcEnabled: true, registerOIDC: true, authProvider: security.ProviderOIDC, legacyKey: true, wantSkipLogs: 1, wantLevel: "WARN"},
		{name: "provider without logout support stays quiet", oidcEnabled: true, registerGH: true, authProvider: security.ProviderGitHub, noWarn: true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			runLogoutRow(t, &row)
		})
	}
}

func runLogoutRow(t *testing.T, row *logoutRow) {
	t.Helper()
	settings := &conf.Settings{}
	if row.oidcEnabled {
		settings.Security.OAuthProviders = []conf.OAuthProviderConfig{{
			Provider: security.ConfigOIDC, Enabled: true, ClientID: "client", ClientSecret: "secret",
		}}
	}
	server := securitytest.NewOAuth2ServerForTesting(t, settings)
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	prevStore := gothic.Store
	gothic.Store = sessions.NewCookieStore([]byte("test-session-secret-long-enough-32"))
	t.Cleanup(func() { gothic.Store = prevStore })

	goth.ClearProviders()
	t.Cleanup(goth.ClearProviders)
	if row.registerOIDC {
		p, err := openidConnect.NewCustomisedURL("client", "secret", "http://localhost/cb",
			"https://idp.example.test/auth", "https://idp.example.test/token", "https://idp.example.test",
			"https://idp.example.test/userinfo", row.endSession)
		require.NoError(t, err)
		goth.UseProviders(p)
	}

	if row.registerGH {
		goth.UseProviders(github.New("client", "secret", "http://localhost/cb"))
	}

	e := echo.New()
	// Write the session cookie the way the OAuth callback does: each write goes
	// through a request that carries the cookie of the previous one.
	req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/logout", http.NoBody)
	store := func(key, value string) {
		rec := httptest.NewRecorder()
		require.NoError(t, gothic.StoreInSession(key, value, req, rec))
		for _, cookie := range rec.Result().Cookies() {
			req = httptest.NewRequest(http.MethodPost, "/api/v2/auth/logout", http.NoBody)
			req.AddCookie(cookie)
		}
	}
	switch {
	case row.legacyKey:
		store(row.authProvider, "user")
	case row.authProvider != "":
		store(security.SessionKeyAuthProvider, row.authProvider)
	}
	store("id_token", logoutTestIDToken)
	c := e.NewContext(req, httptest.NewRecorder())

	buf := logtest.CaptureBuffer(t)
	got := NewSecurityAdapter(server).GetProviderLogoutURL(c, "")
	logs := buf.String()

	if row.wantURL {
		assert.Contains(t, got, "id_token_hint="+logoutTestIDToken)
	} else {
		assert.Empty(t, got)
	}
	assert.NotContains(t, logs, logoutTestIDToken)
	assert.NotContains(t, logs, logoutTestQuery)

	var skipLines []string
	oidcLines := 0
	for line := range strings.Lines(logs) {
		if strings.Contains(line, logoutOIDCModule) {
			oidcLines++
		}
		if strings.Contains(line, logoutSkipMessage) && strings.Contains(line, logoutOIDCModule) {
			skipLines = append(skipLines, line)
		}
	}
	require.Len(t, skipLines, row.wantSkipLogs, logs)
	if row.wantSkipLogs > 0 {
		assert.Contains(t, skipLines[0], row.wantLevel)
	}
	if row.wantSkipHost != "" {
		assert.Contains(t, skipLines[0], row.wantSkipHost)
		assert.NotContains(t, skipLines[0], logoutTestEndpoint)
	}
	if row.noWarn {
		assert.NotContains(t, logs, "WARN")
		assert.Zero(t, oidcLines, "no OIDC console line for another provider")
	}
	if !row.oidcEnabled {
		assert.Zero(t, oidcLines, "no console line without an enabled OIDC provider")
	}
}
