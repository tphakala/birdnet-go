package security

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/markbates/goth/gothic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

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

// TestSessionCookieSecure_DirectPlainHTTP verifies that a client reaching the
// app directly over plain HTTP gets a session cookie without Secure, so it can
// send it back, even when the app is configured for HTTPS behind a reverse
// proxy (issue #4540). Requests over TLS or through a proxy keep the
// configured Secure attribute.
func TestSessionCookieSecure_DirectPlainHTTP(t *testing.T) {
	// Not parallel: InitializeGoth mutates gothic.Store and the test config path.
	tests := []struct {
		name          string
		secureCookies bool
		tls           bool
		header        string
		headerValue   string
		wantSecure    bool
	}{
		{name: "direct plain HTTP", secureCookies: true, wantSecure: false},
		{name: "direct TLS", secureCookies: true, tls: true, wantSecure: true},
		{name: "proxied with X-Forwarded-Proto", secureCookies: true, header: echo.HeaderXForwardedProto, headerValue: conf.SchemeHTTPS, wantSecure: true},
		{name: "proxied with X-Forwarded-For only", secureCookies: true, header: echo.HeaderXForwardedFor, headerValue: "192.0.2.10", wantSecure: true},
		{name: "proxied with Forwarded", secureCookies: true, header: headerForwarded, headerValue: "for=192.0.2.10;proto=https", wantSecure: true},
		{name: "Secure not configured", secureCookies: false, tls: true, wantSecure: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetTestConfigPath(t.TempDir())
			t.Cleanup(func() { SetTestConfigPath("") })
			InitializeGoth(&conf.Settings{Security: conf.Security{SessionSecret: "test-secret"}}, tt.secureCookies)

			newRequest := func(target string) *http.Request {
				req := httptest.NewRequest(http.MethodPost, target, http.NoBody)
				if tt.tls {
					req.TLS = &tls.ConnectionState{}
				}
				if tt.header != "" {
					req.Header.Set(tt.header, tt.headerValue)
				}
				return req
			}

			t.Run("store", func(t *testing.T) {
				rec := httptest.NewRecorder()
				require.NoError(t, gothic.StoreInSession("access_token", "token", newRequest("http://birdnet-go:8080/api/v2/auth/login"), rec))

				assert.Equal(t, tt.wantSecure, sessionCookieFromResponse(t, rec).Secure)
			})

			t.Run("logout", func(t *testing.T) {
				rec := httptest.NewRecorder()
				require.NoError(t, gothic.Logout(rec, newRequest("http://birdnet-go:8080/api/v2/auth/logout")))

				assert.Equal(t, tt.wantSecure, sessionCookieFromResponse(t, rec).Secure)
			})
		})
	}
}
