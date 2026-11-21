package security

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	testUser        = "user@example.com"
	testProviderSub = "12345"
)

// newSessionTestServer returns a server on a real session store (HttpOnly,
// SameSite=Lax cookies) with basic authentication enabled and Google enabled for
// testUser. The caller may change the returned settings, which are the live
// snapshot.
func newSessionTestServer(t *testing.T, secureCookies bool, plainClients []string) (*OAuth2Server, *conf.Settings) {
	t.Helper()
	settings := useSessionTestSettings(t, secureCookies, plainClients)
	settings.Security.BasicAuth = conf.BasicAuth{
		Enabled: true, ClientID: "client", Password: "pw", AuthCodeExp: time.Minute,
	}
	settings.Security.OAuthProviders = []conf.OAuthProviderConfig{
		{Provider: ConfigGoogle, Enabled: true, UserID: testUser},
	}
	return NewOAuth2ServerForTest(settings), settings
}

// authenticate runs checkSession for a request that carries token and, for a
// non-nil prep, lets the test change the request or response first. It writes a
// response so a Before hook runs, and returns the result with the recorder.
func authenticate(t *testing.T, s *OAuth2Server, token string, prep func(*http.Request, echo.Context)) (bool, *httptest.ResponseRecorder) {
	t.Helper()
	req := sessionRequest(token)
	c, rec := sessionContext(req)
	if prep != nil {
		prep(req, c)
	}
	ok := s.checkSession(c, testLogger{})
	if !c.Response().Committed {
		require.NoError(t, c.NoContent(http.StatusOK))
	}
	return ok, rec
}

// expiryOf returns the stored expiry of token's record.
func expiryOf(t *testing.T, s *OAuth2Server, token string) time.Time {
	t.Helper()
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	rec, ok := s.accessTokens[sessionTokenKey(token)]
	require.True(t, ok, "record must exist")
	return rec.ExpiresAt
}

// TestSessionSlidingExpiry walks a session through renewals over fake time for
// both remember values.
func TestSessionSlidingExpiry(t *testing.T) {
	tests := []struct {
		name     string
		remember bool
	}{
		{"remembered", true},
		{"unticked", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, _ := newSessionTestServer(t, false, nil)
			lifetime := sessionLifetime(tt.remember)
			interval := sessionRenewInterval(tt.remember)

			synctest.Test(t, func(t *testing.T) {
				token := seedSession(t, server, &AccessToken{Remember: tt.remember})
				first := expiryOf(t, server, token)

				// A request before the renew interval changes nothing.
				time.Sleep(interval / 2)
				ok, rec := authenticate(t, server, token, nil)
				assert.True(t, ok)
				assert.Equal(t, first, expiryOf(t, server, token))
				assert.Nil(t, findCookie(rec, SessionCookieName))

				// One after it extends to a full lifetime.
				time.Sleep(interval / 2)
				ok, rec = authenticate(t, server, token, nil)
				assert.True(t, ok)
				extended := expiryOf(t, server, token)
				assert.Equal(t, time.Now().Add(lifetime), extended)

				cookie := findCookie(rec, SessionCookieName)
				if tt.remember {
					require.NotNil(t, cookie, "a remembered renewal sends the cookie again")
					assert.Equal(t, token, cookie.Value)
					assert.Equal(t, int(RememberedSessionDuration.Seconds()), cookie.MaxAge)
					assert.Equal(t, "private", rec.Header().Get("Cache-Control"))
				} else {
					assert.Nil(t, cookie, "an unticked session never re-sends its cookie")
					assert.Empty(t, rec.Header().Get("Cache-Control"))
				}

				// Two requests in the same instant extend once.
				ok, rec = authenticate(t, server, token, nil)
				assert.True(t, ok)
				assert.Equal(t, extended, expiryOf(t, server, token))
				assert.Nil(t, findCookie(rec, SessionCookieName))

				// Silence longer than the lifetime ends the session.
				time.Sleep(lifetime + time.Second)
				ok, _ = authenticate(t, server, token, nil)
				assert.False(t, ok)
			})
		})
	}
}

// TestRememberedSessionNotExtendedWithoutCookieChannel verifies that a
// remembered record is extended only on a request whose response can carry the
// renewed cookie, and the next ordinary request extends it.
func TestRememberedSessionNotExtendedWithoutCookieChannel(t *testing.T) {
	tests := []struct {
		name string
		prep func(*http.Request, echo.Context)
	}{
		{"committed response", func(_ *http.Request, c echo.Context) { c.Response().WriteHeader(http.StatusOK) }},
		{"websocket upgrade", func(r *http.Request, _ echo.Context) {
			r.Header.Set(echo.HeaderUpgrade, "websocket")
			r.Header.Set(echo.HeaderConnection, "Upgrade")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, _ := newSessionTestServer(t, false, nil)
			synctest.Test(t, func(t *testing.T) {
				token := seedSession(t, server, &AccessToken{Remember: true})
				first := expiryOf(t, server, token)

				time.Sleep(RememberedSessionRenewInterval)
				ok, _ := authenticate(t, server, token, tt.prep)
				assert.True(t, ok)
				assert.Equal(t, first, expiryOf(t, server, token), "must not extend without a way to send the cookie")

				ok, rec := authenticate(t, server, token, nil)
				assert.True(t, ok)
				assert.Equal(t, time.Now().Add(RememberedSessionDuration), expiryOf(t, server, token))
				assert.NotNil(t, findCookie(rec, SessionCookieName))
			})
		})
	}
}

// TestCheckSession covers which records authenticate a request.
func TestCheckSession(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(s *OAuth2Server, settings *conf.Settings) string
		wantOK bool
	}{
		{"valid password record", func(s *OAuth2Server, _ *conf.Settings) string {
			return seedSession(t, s, &AccessToken{})
		}, true},
		{"expired record", func(s *OAuth2Server, _ *conf.Settings) string {
			return seedSession(t, s, &AccessToken{ExpiresAt: time.Now().Add(-time.Minute)})
		}, false},
		{"unknown token", func(_ *OAuth2Server, _ *conf.Settings) string { return "unknown" }, false},
		{"no cookie", func(_ *OAuth2Server, _ *conf.Settings) string { return "" }, false},
		{"basic authentication disabled", func(s *OAuth2Server, settings *conf.Settings) string {
			token := seedSession(t, s, &AccessToken{})
			settings.Security.BasicAuth.Enabled = false
			return token
		}, false},
		{"social record, allowed user", func(s *OAuth2Server, _ *conf.Settings) string {
			return seedSession(t, s, &AccessToken{Provider: ProviderGoogle, UserID: testUser, ProviderUserID: testProviderSub})
		}, true},
		{"social record, allowed by provider user ID", func(s *OAuth2Server, settings *conf.Settings) string {
			settings.Security.OAuthProviders[0].UserID = testProviderSub
			return seedSession(t, s, &AccessToken{Provider: ProviderGoogle, UserID: testUser, ProviderUserID: testProviderSub})
		}, true},
		{"social record, user removed while another provider still lists them", func(s *OAuth2Server, settings *conf.Settings) string {
			settings.Security.OAuthProviders[0].UserID = "someone-else@example.com"
			settings.Security.OAuthProviders = append(settings.Security.OAuthProviders,
				conf.OAuthProviderConfig{Provider: ConfigGitHub, Enabled: true, UserID: testUser})
			return seedSession(t, s, &AccessToken{Provider: ProviderGoogle, UserID: testUser, ProviderUserID: testProviderSub})
		}, false},
		{"social record, provider disabled", func(s *OAuth2Server, settings *conf.Settings) string {
			settings.Security.OAuthProviders[0].Enabled = false
			return seedSession(t, s, &AccessToken{Provider: ProviderGoogle, UserID: testUser})
		}, false},
		{"record ended by EndSession", func(s *OAuth2Server, _ *conf.Settings) string {
			token := seedSession(t, s, &AccessToken{})
			s.EndSession(httptest.NewRecorder(), sessionRequest(token))
			return token
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, settings := newSessionTestServer(t, false, nil)
			token := tt.setup(server, settings)
			c, _ := sessionContext(sessionRequest(token))
			assert.Equal(t, tt.wantOK, server.checkSession(c, testLogger{}))
		})
	}
}

// TestCheckSessionMismatchDoesNotLogAllowlist verifies that a provider session
// for a user outside the allowlist is rejected without writing the configured
// allowlist (email addresses, user IDs) to the log.
func TestCheckSessionMismatchDoesNotLogAllowlist(t *testing.T) {
	server, settings := newSessionTestServer(t, false, nil)
	settings.Security.OAuthProviders[0].UserID = "admin@example.com, 1234567890"
	token := seedSession(t, server, &AccessToken{Provider: ProviderGoogle, UserID: "stranger@example.com", ProviderUserID: "stranger-sub"})

	buf := &bytes.Buffer{}
	log := logger.NewSlogLogger(buf, logger.LogLevelDebug, time.UTC)
	c, _ := sessionContext(sessionRequest(token))

	require.False(t, server.checkSession(c, log), "a user outside the allowlist must be rejected")
	output := buf.String()
	assert.Contains(t, output, "does not match allowed IDs", "the mismatch must still be logged")
	assert.NotContains(t, output, "admin@example.com", "the allowlist must not be logged")
	assert.NotContains(t, output, "1234567890", "the allowlist must not be logged")
}

// TestCredentialsChangeEndsAllSessions verifies that changing the basic-auth
// client ID or password ends password and social sessions and outstanding
// authorization codes, and that changing it back brings none of them back.
func TestCredentialsChangeEndsAllSessions(t *testing.T) {
	tests := []struct {
		name   string
		change func(*conf.BasicAuth, bool)
	}{
		{"password", func(b *conf.BasicAuth, change bool) {
			b.Password = map[bool]string{true: "changed", false: "pw"}[change]
		}},
		{"client ID", func(b *conf.BasicAuth, change bool) {
			b.ClientID = map[bool]string{true: "changed", false: "client"}[change]
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, settings := newSessionTestServer(t, false, nil)
			password := seedSession(t, server, &AccessToken{})
			social := seedSession(t, server, &AccessToken{Provider: ProviderGoogle, UserID: testUser})
			code := server.GenerateAuthCode(false)

			tt.change(&settings.Security.BasicAuth, true)
			_, err := server.ExchangeAuthCode(t.Context(), code)
			require.Error(t, err, "a code issued before the change must not be exchanged after it")

			for _, token := range []string{password, social} {
				c, _ := sessionContext(sessionRequest(token))
				assert.False(t, server.checkSession(c, testLogger{}))
			}

			tt.change(&settings.Security.BasicAuth, false)
			for _, token := range []string{password, social} {
				c, _ := sessionContext(sessionRequest(token))
				assert.False(t, server.checkSession(c, testLogger{}), "changing it back must not bring a session back")
			}
		})
	}
}

// signedInCookie signs a browser in with a password through the public flow and
// returns the cookie the response sets.
func signedInCookie(t *testing.T, s *OAuth2Server, prev *http.Request, remember bool) (cookie *http.Cookie, rawHeader string) {
	t.Helper()
	code := s.GenerateAuthCode(remember)
	token, err := s.ExchangeAuthCode(t.Context(), code)
	require.NoError(t, err)
	if prev == nil {
		prev = sessionRequest("")
	}
	rec := httptest.NewRecorder()
	require.NoError(t, s.StartSession(rec, prev, token))
	cookie = findCookie(rec, SessionCookieName)
	require.NotNil(t, cookie)
	return cookie, rec.Header().Get("Set-Cookie")
}

// TestSessionCookieAttributes pins the cookie a sign-in sends: persistent only
// when remembered, always HttpOnly and SameSite=Lax, and Secure except for a
// listed plain-HTTP client.
func TestSessionCookieAttributes(t *testing.T) {
	server, _ := newSessionTestServer(t, true, []string{testInternalClientAddr})

	t.Run("unticked is a browser-session cookie", func(t *testing.T) {
		cookie, raw := signedInCookie(t, server, nil, false)
		assert.NotContains(t, raw, "Max-Age")
		assert.NotContains(t, raw, "Expires")
		assert.True(t, cookie.HttpOnly)
		assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
		assert.True(t, cookie.Secure)
	})

	t.Run("ticked carries the remembered max age", func(t *testing.T) {
		cookie, _ := signedInCookie(t, server, nil, true)
		assert.Equal(t, int(RememberedSessionDuration.Seconds()), cookie.MaxAge)
		assert.True(t, cookie.HttpOnly)
		assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	})

	t.Run("listed plain-HTTP client gets no Secure, others keep it", func(t *testing.T) {
		listed := sessionRequest("")
		listed.RemoteAddr = testInternalClientAddr + testRemotePort
		other := sessionRequest("")
		other.RemoteAddr = testExternalAddr + testRemotePort

		for _, tc := range []struct {
			req        *http.Request
			wantSecure bool
		}{{listed, false}, {other, true}} {
			token, err := server.ExchangeAuthCode(t.Context(), server.GenerateAuthCode(true))
			require.NoError(t, err)
			rec := httptest.NewRecorder()
			require.NoError(t, server.StartSession(rec, tc.req, token))
			cookie := findCookie(rec, SessionCookieName)
			require.NotNil(t, cookie)
			assert.Equal(t, tc.wantSecure, cookie.Secure)
		}
	})
}

// TestEverySignInGetsAFreshToken verifies that signing in again on a browser
// that holds a session revokes the old record and issues a different token (no
// session fixation), for password and provider sign-ins.
func TestEverySignInGetsAFreshToken(t *testing.T) {
	server, _ := newSessionTestServer(t, false, nil)

	first, _ := signedInCookie(t, server, nil, true)
	second, _ := signedInCookie(t, server, sessionRequest(first.Value), false)
	assert.NotEqual(t, first.Value, second.Value)
	_, ok := server.liveRecord(first.Value)
	assert.False(t, ok, "the previous record must be revoked")
	rec, ok := server.liveRecord(second.Value)
	require.True(t, ok)
	assert.False(t, rec.Remember, "the new sign-in's choice applies, not the old one")

	w := httptest.NewRecorder()
	require.NoError(t, server.StartProviderSession(w, sessionRequest(second.Value), &AccessToken{
		Provider: ProviderGoogle, UserID: testUser, ProviderUserID: testProviderSub, IDToken: "id",
	}, true))
	third := findCookie(w, SessionCookieName)
	require.NotNil(t, third)
	assert.NotEqual(t, second.Value, third.Value)
	assert.Equal(t, int(RememberedSessionDuration.Seconds()), third.MaxAge)
	_, ok = server.liveRecord(second.Value)
	assert.False(t, ok)
	social, ok := server.liveRecord(third.Value)
	require.True(t, ok)
	assert.Equal(t, ProviderGoogle, social.Provider)
	assert.Equal(t, "id", social.IDToken)
}

// TestEndSessionClearsCookie verifies logout deletes the record and tells the
// browser to drop the cookie.
func TestEndSessionClearsCookie(t *testing.T) {
	server, _ := newSessionTestServer(t, false, nil)
	token := seedSession(t, server, &AccessToken{Remember: true})

	rec := httptest.NewRecorder()
	server.EndSession(rec, sessionRequest(token))

	_, ok := server.liveRecord(token)
	assert.False(t, ok)
	cookie := findCookie(rec, SessionCookieName)
	require.NotNil(t, cookie)
	assert.Negative(t, cookie.MaxAge, "the cookie must be deleted")
}

// TestRememberChoiceCookie covers the round trip of the choice between the
// start of a provider sign-in and its callback.
func TestRememberChoiceCookie(t *testing.T) {
	newServer := func(t *testing.T) { t.Helper(); newSessionTestServer(t, false, nil) }

	t.Run("set true then take returns true and deletes", func(t *testing.T) {
		newServer(t)
		w := httptest.NewRecorder()
		SetRememberChoice(w, sessionRequest(""), true)
		set := findCookie(w, rememberChoiceCookieName)
		require.NotNil(t, set)
		assert.Equal(t, rememberChoiceMaxAgeSeconds, set.MaxAge)
		assert.True(t, set.HttpOnly)

		req := sessionRequest("")
		req.AddCookie(&http.Cookie{Name: rememberChoiceCookieName, Value: set.Value})
		w = httptest.NewRecorder()
		assert.True(t, TakeRememberChoice(w, req))
		deleted := findCookie(w, rememberChoiceCookieName)
		require.NotNil(t, deleted)
		assert.Negative(t, deleted.MaxAge)
	})

	t.Run("set false deletes a stale one", func(t *testing.T) {
		newServer(t)
		w := httptest.NewRecorder()
		SetRememberChoice(w, sessionRequest(""), false)
		deleted := findCookie(w, rememberChoiceCookieName)
		require.NotNil(t, deleted)
		assert.Negative(t, deleted.MaxAge)
	})

	t.Run("absent or unexpected value is false", func(t *testing.T) {
		newServer(t)
		assert.False(t, TakeRememberChoice(httptest.NewRecorder(), sessionRequest("")))

		req := sessionRequest("")
		req.AddCookie(&http.Cookie{Name: rememberChoiceCookieName, Value: strings.Repeat("x", 3)})
		assert.False(t, TakeRememberChoice(httptest.NewRecorder(), req))
	})
}
