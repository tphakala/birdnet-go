package security

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

// settleCredentials marks the server's live basic-auth credentials as the ones
// its records were issued under, so a test that seeds records by hand is not
// purged by syncCredentials. Call it after the settings are final.
func settleCredentials(s *OAuth2Server) {
	s.credentialsID = basicCredentialsID(s.currentSettings())
}

// seedSession stores rec as a session record for a fresh token and returns the
// token (the value of the session cookie). A zero ExpiresAt means a full
// lifetime from now.
func seedSession(tb testing.TB, s *OAuth2Server, rec *AccessToken) string {
	tb.Helper()
	token, err := newOpaqueToken()
	require.NoError(tb, err)
	if rec.ExpiresAt.IsZero() {
		rec.ExpiresAt = time.Now().Add(sessionLifetime(rec.Remember))
	}
	settleCredentials(s)
	s.mutex.Lock()
	s.accessTokens[sessionTokenKey(token)] = *rec
	s.mutex.Unlock()
	return token
}

// sessionRequest returns a GET request carrying the session cookie for token.
// An empty token gives a request without the cookie.
func sessionRequest(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
	}
	return req
}

// sessionContext returns an echo context for req and the recorder its response
// goes to.
func sessionContext(req *http.Request) (echo.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	return echo.New().NewContext(req, rec), rec
}

// findCookie returns the first Set-Cookie of the recorder with the given name.
func findCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}
