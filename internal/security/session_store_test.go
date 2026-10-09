package security

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth/gothic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// Addresses used by the session cookie tests: a reverse proxy and an internal
// client on the same container network, and an address outside it.
const (
	testProxyAddr          = "172.18.0.2"
	testInternalClientAddr = "172.18.0.5"
	testExternalAddr       = "203.0.113.9"
	testRemotePort         = ":40000"
)

// useSessionTestSettings initializes the session store with secureCookies and
// publishes settings listing clients as plain-HTTP session clients. The
// previous gothic store, settings snapshot and test config path are restored
// when the test ends.
func useSessionTestSettings(t *testing.T, secureCookies bool, clients []string) *conf.Settings {
	t.Helper()
	prevStore := gothic.Store
	prevSettings := conf.GetSettings()
	t.Cleanup(func() {
		gothic.Store = prevStore
		conftest.SetTestSettings(prevSettings)
	})
	SetTestConfigPath(t.TempDir())
	t.Cleanup(func() { SetTestConfigPath("") })

	settings := &conf.Settings{Security: conf.Security{
		SessionSecret:           "test-secret",
		PlainHTTPSessionClients: clients,
	}}
	conftest.SetTestSettings(settings)
	InitializeGoth(settings, secureCookies)
	return settings
}

// sessionCookieFromResponse returns the gothic session cookie set on rec.
func sessionCookieFromResponse(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == gothic.SessionName {
			return cookie
		}
	}
	require.Fail(t, "session cookie not set", "Set-Cookie headers: %v", rec.Header().Values("Set-Cookie"))
	return nil
}

// loginCookieSecure stores a session value for req, as login does, and reports
// whether the resulting session cookie carries Secure.
func loginCookieSecure(t *testing.T, req *http.Request) bool {
	t.Helper()
	rec := httptest.NewRecorder()
	require.NoError(t, gothic.StoreInSession("access_token", "token", req, rec))
	return sessionCookieFromResponse(t, rec).Secure
}

// logoutCookieSecure clears the session for req, as logout does, and reports
// whether the resulting session cookie carries Secure.
func logoutCookieSecure(t *testing.T, req *http.Request) bool {
	t.Helper()
	rec := httptest.NewRecorder()
	require.NoError(t, gothic.Logout(rec, req))
	return sessionCookieFromResponse(t, rec).Secure
}

// TestSessionCookieSecure_PlainHTTPSessionClients verifies that the session
// cookie keeps Secure whenever HTTPS is configured, except for clients the
// operator lists in Security.PlainHTTPSessionClients that connect directly over
// plain HTTP (issue #4540). In particular a TLS-terminating reverse proxy that
// sends no forwarding headers must not cost browser sessions their Secure
// attribute, and request headers must never put a client on the list.
func TestSessionCookieSecure_PlainHTTPSessionClients(t *testing.T) {
	// Not parallel: mutates gothic.Store, the published settings and the test
	// config path.
	tests := []struct {
		name          string
		secureCookies bool
		clients       []string
		remoteAddr    string
		tls           bool
		header        string
		headerValue   string
		wantSecure    bool
	}{
		{
			name:          "HTTPS reverse proxy without forwarding headers, no clients listed",
			secureCookies: true, remoteAddr: testProxyAddr + testRemotePort,
			wantSecure: true,
		},
		{
			name:          "HTTPS reverse proxy without forwarding headers, another client listed",
			secureCookies: true, clients: []string{testInternalClientAddr}, remoteAddr: testProxyAddr + testRemotePort,
			wantSecure: true,
		},
		{
			name:          "direct HTTP from a client that is not listed",
			secureCookies: true, remoteAddr: testInternalClientAddr + testRemotePort,
			wantSecure: true,
		},
		{
			name:          "direct HTTP from a listed client",
			secureCookies: true, clients: []string{testInternalClientAddr}, remoteAddr: testInternalClientAddr + testRemotePort,
			wantSecure: false,
		},
		{
			name:          "direct HTTP from a client inside a range entry",
			secureCookies: true, clients: []string{"172.18.0.4/30"}, remoteAddr: testInternalClientAddr + testRemotePort,
			wantSecure: true,
		},
		{
			name:          "direct HTTP from a client listed as an IPv4-mapped IPv6 address",
			secureCookies: true, clients: []string{"::ffff:" + testInternalClientAddr}, remoteAddr: testInternalClientAddr + testRemotePort,
			wantSecure: false,
		},
		{
			name:          "direct HTTP from a zoned link-local peer of a listed client",
			secureCookies: true, clients: []string{"fe80::5"}, remoteAddr: "[fe80::5%eth0]" + testRemotePort,
			wantSecure: false,
		},
		{
			name:          "direct HTTP from a listed IPv6 client",
			secureCookies: true, clients: []string{"2001:db8::5"}, remoteAddr: "[2001:db8::5]" + testRemotePort,
			wantSecure: false,
		},
		{
			name:          "direct HTTP from an IPv4-mapped IPv6 peer of a listed client",
			secureCookies: true, clients: []string{testInternalClientAddr}, remoteAddr: "[::ffff:" + testInternalClientAddr + "]" + testRemotePort,
			wantSecure: false,
		},
		{
			name:          "TLS from a listed client",
			secureCookies: true, clients: []string{testInternalClientAddr}, remoteAddr: testInternalClientAddr + testRemotePort, tls: true,
			wantSecure: true,
		},
		{
			name:          "forwarded with X-Forwarded-Proto from a listed address",
			secureCookies: true, clients: []string{testProxyAddr}, remoteAddr: testProxyAddr + testRemotePort,
			header: echo.HeaderXForwardedProto, headerValue: conf.SchemeHTTPS,
			wantSecure: true,
		},
		{
			name:          "forwarded with X-Forwarded-For only from a listed address",
			secureCookies: true, clients: []string{testProxyAddr}, remoteAddr: testProxyAddr + testRemotePort,
			header: echo.HeaderXForwardedFor, headerValue: testExternalAddr,
			wantSecure: true,
		},
		{
			name:          "forwarded with Forwarded from a listed address",
			secureCookies: true, clients: []string{testProxyAddr}, remoteAddr: testProxyAddr + testRemotePort,
			header: headerForwarded, headerValue: "for=" + testExternalAddr + ";proto=https",
			wantSecure: true,
		},
		{
			name:          "unlisted peer claiming a listed address in X-Real-Ip",
			secureCookies: true, clients: []string{testInternalClientAddr}, remoteAddr: testExternalAddr + testRemotePort,
			header: echo.HeaderXRealIP, headerValue: testInternalClientAddr,
			wantSecure: true,
		},
		{
			name:          "unlisted peer claiming a listed address in X-Forwarded-For",
			secureCookies: true, clients: []string{testInternalClientAddr}, remoteAddr: testExternalAddr + testRemotePort,
			header: echo.HeaderXForwardedFor, headerValue: testInternalClientAddr,
			wantSecure: true,
		},
		{
			name:          "unix socket peer",
			secureCookies: true, clients: []string{testInternalClientAddr}, remoteAddr: "@",
			wantSecure: true,
		},
		{
			name:          "Secure not configured",
			secureCookies: false, remoteAddr: testProxyAddr + testRemotePort, tls: true,
			wantSecure: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useSessionTestSettings(t, tt.secureCookies, tt.clients)

			newRequest := func(target string) *http.Request {
				req := httptest.NewRequest(http.MethodPost, target, http.NoBody)
				req.RemoteAddr = tt.remoteAddr
				if tt.tls {
					req.TLS = &tls.ConnectionState{}
				}
				if tt.header != "" {
					req.Header.Set(tt.header, tt.headerValue)
				}
				return req
			}

			assert.Equal(t, tt.wantSecure, loginCookieSecure(t, newRequest("http://birdnet-go:8080/api/v2/auth/login")), "login")
			assert.Equal(t, tt.wantSecure, logoutCookieSecure(t, newRequest("http://birdnet-go:8080/api/v2/auth/logout")), "logout")
		})
	}
}

// TestSessionCookieSecure_PlainHTTPSessionClientsHotReload verifies that the
// client list is read per request, so editing it takes effect without
// reinitializing the session store.
func TestSessionCookieSecure_PlainHTTPSessionClientsHotReload(t *testing.T) {
	// Not parallel: mutates gothic.Store, the published settings and the test
	// config path.
	settings := useSessionTestSettings(t, true, nil)
	newRequest := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "http://birdnet-go:8080/api/v2/auth/login", http.NoBody)
		req.RemoteAddr = testInternalClientAddr + testRemotePort
		return req
	}

	require.True(t, loginCookieSecure(t, newRequest()), "Secure before the client is listed")

	updated := conf.CloneSettings(settings)
	updated.Security.PlainHTTPSessionClients = []string{testInternalClientAddr}
	conftest.SetTestSettings(updated)
	assert.False(t, loginCookieSecure(t, newRequest()), "not Secure once the client is listed")

	conftest.SetTestSettings(settings)
	assert.True(t, loginCookieSecure(t, newRequest()), "Secure again once the client is removed")
}

// TestConfigureLocalNetworkCookieStore_WrappedStore verifies that the local
// network relaxation still reaches the store wrapped by
// plainHTTPClientSessionStore.
func TestConfigureLocalNetworkCookieStore_WrappedStore(t *testing.T) {
	// Not parallel: mutates the global gothic.Store.
	prevStore := gothic.Store
	t.Cleanup(func() { gothic.Store = prevStore })

	inner := sessions.NewFilesystemStore(t.TempDir(), []byte("test-secret"))
	inner.Options = buildSessionOptions(true, DefaultSessionMaxAgeSeconds)
	gothic.Store = plainHTTPClientSessionStore{Store: inner}

	server := &OAuth2Server{settings: &conf.Settings{Security: conf.Security{SessionSecret: "test-secret"}}}
	server.configureLocalNetworkCookieStore(server.settings)

	assert.False(t, inner.Options.Secure, "Secure should be false on the wrapped store for local network")
}
