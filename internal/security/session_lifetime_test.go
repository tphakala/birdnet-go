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
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

const (
	testUser        = "user@example.com"
	testProviderSub = "12345"

	// testCachePrivate is the Cache-Control value a remembered renewal sets.
	testCachePrivate = "private"
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
// both remember values. A remembered session's renewal also re-sends its cookie;
// an unticked session's renewal only slides the server record.
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
					require.NotNil(t, cookie, "a remembered renewal must re-send the cookie")
					assert.Equal(t, int(RememberedCookieMaxAge.Seconds()), cookie.MaxAge)
					assert.Equal(t, testCachePrivate, rec.Header().Get(echo.HeaderCacheControl))
				} else {
					assert.Nil(t, cookie, "an unticked renewal must not send a cookie")
					assert.Empty(t, rec.Header().Get(echo.HeaderCacheControl))
				}

				// Two requests in the same instant extend once.
				ok, _ = authenticate(t, server, token, nil)
				assert.True(t, ok)
				assert.Equal(t, extended, expiryOf(t, server, token))

				// Silence longer than the lifetime ends the session.
				time.Sleep(lifetime + time.Second)
				ok, _ = authenticate(t, server, token, nil)
				assert.False(t, ok)
			})
		})
	}
}

// TestRememberedSessionRenewalResendsCookie verifies what a remembered renewal
// adds to its response: the session cookie again, so the cookie lasts as long as
// the browser is used, and Cache-Control private so no shared cache stores the
// Set-Cookie. A record that was revoked before the header is written (a sign-in
// in another tab) gets neither.
func TestRememberedSessionRenewalResendsCookie(t *testing.T) {
	cacheControl := []struct {
		name    string
		handler string
		want    string
	}{
		{"none set", "", testCachePrivate},
		{"public max-age is replaced", "public, max-age=60", testCachePrivate},
		{"no-store is kept", "no-store", "no-store"},
	}
	for _, tt := range cacheControl {
		t.Run("Cache-Control "+tt.name, func(t *testing.T) {
			server, _ := newSessionTestServer(t, false, nil)
			synctest.Test(t, func(t *testing.T) {
				token := seedSession(t, server, &AccessToken{Remember: true})
				time.Sleep(rememberedSessionRenewInterval)

				ok, rec := authenticate(t, server, token, func(_ *http.Request, c echo.Context) {
					if tt.handler != "" {
						c.Response().Header().Set(echo.HeaderCacheControl, tt.handler)
					}
				})
				require.True(t, ok)
				cookie := findCookie(rec, SessionCookieName)
				require.NotNil(t, cookie)
				assert.Equal(t, token, cookie.Value)
				assert.Equal(t, int(RememberedCookieMaxAge.Seconds()), cookie.MaxAge)
				assert.Equal(t, tt.want, rec.Header().Get(echo.HeaderCacheControl))
			})
		})
	}

	t.Run("a record revoked before the response is written gets no cookie", func(t *testing.T) {
		server, _ := newSessionTestServer(t, false, nil)
		synctest.Test(t, func(t *testing.T) {
			token := seedSession(t, server, &AccessToken{Remember: true})
			time.Sleep(rememberedSessionRenewInterval)

			req := sessionRequest(token)
			c, rec := sessionContext(req)
			require.True(t, server.checkSession(c, testLogger{}))
			require.True(t, server.revokeRequestSession(req, ""))
			require.NoError(t, c.NoContent(http.StatusOK))

			assert.Nil(t, findCookie(rec, SessionCookieName))
			assert.Empty(t, rec.Header().Get(echo.HeaderCacheControl))
		})
	})
}

// TestRenewalOnCommittedResponseAndWebSocketUpgrade verifies that a response that
// cannot carry the cookie renews an unticked session (server record only) and
// does not renew a remembered one, whose renewal needs the cookie: the next
// ordinary request renews it and sends the cookie.
func TestRenewalOnCommittedResponseAndWebSocketUpgrade(t *testing.T) {
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
		t.Run(tt.name+" renews an unticked session", func(t *testing.T) {
			server, _ := newSessionTestServer(t, false, nil)
			synctest.Test(t, func(t *testing.T) {
				token := seedSession(t, server, &AccessToken{})

				time.Sleep(idleSessionRenewInterval)
				ok, _ := authenticate(t, server, token, tt.prep)
				assert.True(t, ok)
				assert.Equal(t, time.Now().Add(sessionIdleTimeout), expiryOf(t, server, token))
			})
		})
		t.Run(tt.name+" leaves a remembered session to the next ordinary request", func(t *testing.T) {
			server, _ := newSessionTestServer(t, false, nil)
			synctest.Test(t, func(t *testing.T) {
				token := seedSession(t, server, &AccessToken{Remember: true})
				first := expiryOf(t, server, token)

				time.Sleep(rememberedSessionRenewInterval)
				ok, rec := authenticate(t, server, token, tt.prep)
				assert.True(t, ok)
				assert.Equal(t, first, expiryOf(t, server, token), "no cookie can be sent, so the record is not extended")
				assert.Nil(t, findCookie(rec, SessionCookieName))

				ok, rec = authenticate(t, server, token, nil)
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

// TestCheckSessionStoresRecordInContext verifies that a successful session check
// leaves its record on the request context, and a failed one leaves none.
func TestCheckSessionStoresRecordInContext(t *testing.T) {
	server, _ := newSessionTestServer(t, false, nil)
	token := seedSession(t, server, &AccessToken{Provider: ProviderGoogle, UserID: testUser, ProviderUserID: testProviderSub})

	c, _ := sessionContext(sessionRequest(token))
	require.True(t, server.checkSession(c, testLogger{}))
	rec, ok := SessionFromContext(c)
	require.True(t, ok)
	assert.Equal(t, testUser, rec.UserID)

	c, _ = sessionContext(sessionRequest("unknown"))
	require.False(t, server.checkSession(c, testLogger{}))
	_, ok = SessionFromContext(c)
	assert.False(t, ok)
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
			code, err := server.GenerateAuthCode(settings, false)
			require.NoError(t, err)

			// Settings snapshots are immutable, so a change is a new snapshot.
			publish := func(changed bool) {
				next := conf.CloneSettings(settings)
				tt.change(&next.Security.BasicAuth, changed)
				conftest.SetTestSettings(next)
			}

			publish(true)
			_, err = server.ExchangeAuthCode(t.Context(), code)
			require.Error(t, err, "a code issued before the change must not be exchanged after it")

			for _, token := range []string{password, social} {
				c, _ := sessionContext(sessionRequest(token))
				assert.False(t, server.checkSession(c, testLogger{}))
			}

			publish(false)
			for _, token := range []string{password, social} {
				c, _ := sessionContext(sessionRequest(token))
				assert.False(t, server.checkSession(c, testLogger{}), "changing it back must not bring a session back")
			}
		})
	}
}

// TestGenerateAuthCodeRefusesCredentialsChangedSinceVerification verifies that a
// login whose password was checked against the old credentials gets no code
// once the new ones are live, and that the old credentials never become the
// server's again.
func TestGenerateAuthCodeRefusesCredentialsChangedSinceVerification(t *testing.T) {
	server, verified := newSessionTestServer(t, false, nil)

	// The password changes while the login is in flight.
	live := conf.CloneSettings(verified)
	live.Security.BasicAuth.Password = "changed"
	conftest.SetTestSettings(live)

	code, err := server.GenerateAuthCode(verified, false)
	require.ErrorIs(t, err, ErrCredentialsChanged)
	assert.Empty(t, code)
	server.mutex.RLock()
	assert.Empty(t, server.authCodes, "no code may be stored for the stale credentials")
	assert.Equal(t, basicCredentialsID(live), server.credentialsID, "the server keeps the live credentials")
	server.mutex.RUnlock()

	code, err = server.GenerateAuthCode(live, false)
	require.NoError(t, err)
	assert.NotEmpty(t, code, "a login verified against the live credentials still gets a code")

	_, err = server.GenerateAuthCode(nil, false)
	require.ErrorIs(t, err, ErrCredentialsChanged, "no verified snapshot means no code")
}

// signedInCookie signs a browser in with a password through the public flow and
// returns the cookie the response sets.
func signedInCookie(t *testing.T, s *OAuth2Server, prev *http.Request, remember bool) (cookie *http.Cookie, rawHeader string) {
	t.Helper()
	code, err := s.GenerateAuthCode(s.currentSettings(), remember)
	require.NoError(t, err)
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

	t.Run("ticked carries the long fixed max age", func(t *testing.T) {
		cookie, _ := signedInCookie(t, server, nil, true)
		assert.Equal(t, int(RememberedCookieMaxAge.Seconds()), cookie.MaxAge)
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
			code, err := server.GenerateAuthCode(server.currentSettings(), true)
			require.NoError(t, err)
			token, err := server.ExchangeAuthCode(t.Context(), code)
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
	_, err := server.liveRecord(first.Value)
	require.ErrorIs(t, err, ErrTokenNotFound, "the previous record must be revoked")
	rec, err := server.liveRecord(second.Value)
	require.NoError(t, err)
	assert.False(t, rec.Remember, "the new sign-in's choice applies, not the old one")

	w := httptest.NewRecorder()
	server.StartProviderSession(w, sessionRequest(second.Value), &AccessToken{
		Provider: ProviderGoogle, UserID: testUser, ProviderUserID: testProviderSub, IDToken: "id",
	}, true)
	third := findCookie(w, SessionCookieName)
	require.NotNil(t, third)
	assert.NotEqual(t, second.Value, third.Value)
	assert.Equal(t, int(RememberedCookieMaxAge.Seconds()), third.MaxAge)
	_, err = server.liveRecord(second.Value)
	require.ErrorIs(t, err, ErrTokenNotFound)
	social, err := server.liveRecord(third.Value)
	require.NoError(t, err)
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

	_, err := server.liveRecord(token)
	require.ErrorIs(t, err, ErrTokenNotFound)
	cookie := findCookie(rec, SessionCookieName)
	require.NotNil(t, cookie)
	assert.Negative(t, cookie.MaxAge, "the cookie must be deleted")
}

// TestRememberChoiceCookie covers the round trip of the choice between the
// start of a provider sign-in and its callback, kept per provider.
func TestRememberChoiceCookie(t *testing.T) {
	newServer := func(t *testing.T) { t.Helper(); newSessionTestServer(t, false, nil) }
	googleCookie := rememberChoiceCookie(ProviderGoogle)

	t.Run("set true then take returns true and deletes", func(t *testing.T) {
		newServer(t)
		w := httptest.NewRecorder()
		SetRememberChoice(w, sessionRequest(""), ProviderGoogle, true)
		set := findCookie(w, googleCookie)
		require.NotNil(t, set)
		assert.Equal(t, rememberChoiceMaxAgeSeconds, set.MaxAge)
		assert.True(t, set.HttpOnly)

		req := sessionRequest("")
		req.AddCookie(&http.Cookie{Name: googleCookie, Value: set.Value})
		w = httptest.NewRecorder()
		assert.True(t, TakeRememberChoice(w, req, ProviderGoogle))
		deleted := findCookie(w, googleCookie)
		require.NotNil(t, deleted)
		assert.Negative(t, deleted.MaxAge)
	})

	t.Run("set false deletes a stale one", func(t *testing.T) {
		newServer(t)
		w := httptest.NewRecorder()
		SetRememberChoice(w, sessionRequest(""), ProviderGoogle, false)
		deleted := findCookie(w, googleCookie)
		require.NotNil(t, deleted)
		assert.Negative(t, deleted.MaxAge)
		assert.Nil(t, findCookie(w, rememberChoiceCookie(ProviderGitHub)), "another provider's choice must be left alone")
	})

	t.Run("absent or unexpected value is false", func(t *testing.T) {
		newServer(t)
		assert.False(t, TakeRememberChoice(httptest.NewRecorder(), sessionRequest(""), ProviderGoogle))

		req := sessionRequest("")
		req.AddCookie(&http.Cookie{Name: googleCookie, Value: strings.Repeat("x", 3)})
		assert.False(t, TakeRememberChoice(httptest.NewRecorder(), req, ProviderGoogle))
	})

	t.Run("another provider's choice is neither read nor cleared", func(t *testing.T) {
		newServer(t)
		req := sessionRequest("")
		req.AddCookie(&http.Cookie{Name: rememberChoiceCookie(ProviderGitHub), Value: RememberQueryValue})
		w := httptest.NewRecorder()
		assert.False(t, TakeRememberChoice(w, req, ProviderGoogle))
		assert.Empty(t, w.Result().Cookies())
	})
}

// TestWarnSupersededSessionSettings verifies that the startup warning names each
// superseded setting whose value differs from its old default, and stays quiet
// for the default and for an unset value.
func TestWarnSupersededSessionSettings(t *testing.T) {
	tests := []struct {
		name     string
		security conf.Security
		want     []string
		notWant  []string
	}{
		{"old defaults", conf.Security{
			SessionDuration: legacySessionDuration,
			BasicAuth:       conf.BasicAuth{AccessTokenExp: legacyAccessTokenExp},
		}, nil, []string{"no longer controls"}},
		{"unset", conf.Security{}, nil, []string{"no longer controls"}},
		{"session duration changed", conf.Security{SessionDuration: time.Hour},
			[]string{"security.sessionduration no longer controls"}, []string{"accesstokenexp"}},
		{"access token expiry changed", conf.Security{BasicAuth: conf.BasicAuth{AccessTokenExp: 2 * time.Hour}},
			[]string{"security.basicauth.accesstokenexp no longer controls"}, []string{"security.sessionduration"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := logtest.Capture(t, func() {
				warnSupersededSessionSettings(&conf.Settings{Security: tt.security})
			})
			for _, want := range tt.want {
				assert.Contains(t, output, want)
			}
			for _, notWant := range tt.notWant {
				assert.NotContains(t, output, notWant)
			}
		})
	}
}

// TestStartSessionRejectsDeadToken verifies that no cookie is sent for a token
// that names no live record.
func TestStartSessionRejectsDeadToken(t *testing.T) {
	server, _ := newSessionTestServer(t, false, nil)
	expired := seedSession(t, server, &AccessToken{ExpiresAt: time.Now().Add(-time.Minute)})

	for token, want := range map[string]error{"unknown": ErrTokenNotFound, expired: ErrTokenExpired} {
		rec := httptest.NewRecorder()
		require.ErrorIs(t, server.StartSession(rec, sessionRequest(""), token), want)
		assert.Nil(t, findCookie(rec, SessionCookieName))
	}
}
