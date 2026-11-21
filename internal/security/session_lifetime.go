package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/sessions"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth/gothic"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// legacySessionDuration and legacyAccessTokenExp are the old defaults of
// security.sessionduration and security.basicauth.accesstokenexp. A configured
// value that differs from them was a deliberate choice that no longer applies,
// so the server warns once at startup.
const (
	legacySessionDuration = time.Duration(DefaultSessionMaxAgeDays) * 24 * time.Hour
	legacyAccessTokenExp  = time.Hour
)

// credentialsSeparator separates the client ID from the password in the input
// of basicCredentialsID, so two different pairs cannot concatenate to the same
// bytes.
const credentialsSeparator = "\x00"

// sessionLifetime returns how long a session lasts after its last renewal.
func sessionLifetime(remember bool) time.Duration {
	if remember {
		return RememberedSessionDuration
	}
	return sessionIdleTimeout
}

// sessionRenewInterval returns the most often a session is extended.
func sessionRenewInterval(remember bool) time.Duration {
	if remember {
		return rememberedSessionRenewInterval
	}
	return idleSessionRenewInterval
}

// sessionTokenKey returns the map key and on-disk identifier of a session
// token: the hex SHA-256 of the token, so a session record read from the
// persisted file does not reveal the token that signs in with it.
func sessionTokenKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// basicCredentialsID derives an identifier of the live basic-auth credentials
// from settings: an HMAC keyed with the session secret over the client ID and
// the password. It changes whenever either changes, and the file that stores it
// does not hold the password.
func basicCredentialsID(settings *conf.Settings) string {
	mac := hmac.New(sha256.New, []byte(settings.Security.SessionSecret))
	mac.Write([]byte(settings.Security.BasicAuth.ClientID))
	mac.Write([]byte(credentialsSeparator))
	mac.Write([]byte(settings.Security.BasicAuth.Password))
	return hex.EncodeToString(mac.Sum(nil))
}

// credentialsMemo remembers the credentials ID computed for one settings
// snapshot.
type credentialsMemo struct {
	settings *conf.Settings
	id       string
}

// liveCredentialsID returns basicCredentialsID(settings). Settings snapshots are
// immutable once published (internal/conf/storage.go), so the HMAC is computed
// once per snapshot, not once per request.
func (s *OAuth2Server) liveCredentialsID(settings *conf.Settings) string {
	if memo := s.credentialsMemo.Load(); memo != nil && memo.settings == settings {
		return memo.id
	}
	id := basicCredentialsID(settings)
	s.credentialsMemo.Store(&credentialsMemo{settings: settings, id: id})
	return id
}

// syncCredentials ends every session and authorization code when the basic-auth
// client ID or password differ from those the stored sessions were issued
// under. It persists the result before it returns, so a restart cannot bring
// the sessions back. A store that has never recorded credentials has issued
// nothing under any (every save follows a sync), so it adopts the live ones
// without ending anything. Every lookup that decides whether a request is signed
// in, and every write of a new record or code, goes through it first
// (liveRecord, GenerateAuthCode, ExchangeAuthCode, StartProviderSession). It reads the live settings snapshot
// itself while holding s.mutex, so concurrent callers cannot write an older ID
// after a newer one, and it returns the snapshot it synced against: the one
// the calling operation should use for any other settings read. It returns nil
// when there are no settings. It must not be called with s.mutex held.
func (s *OAuth2Server) syncCredentials() *conf.Settings {
	s.mutex.RLock()
	settings := s.currentSettings()
	same := settings == nil || s.liveCredentialsID(settings) == s.credentialsID
	s.mutex.RUnlock()
	if settings == nil {
		return nil
	}
	if same {
		return settings
	}

	s.mutex.Lock()
	settings = s.currentSettings()
	if settings == nil {
		s.mutex.Unlock()
		return nil
	}
	id := s.liveCredentialsID(settings)
	if id == s.credentialsID {
		s.mutex.Unlock()
		return settings
	}
	adopting := s.credentialsID == ""
	hadEntries := len(s.accessTokens) > 0 || len(s.authCodes) > 0
	if !adopting {
		clear(s.accessTokens)
		clear(s.authCodes)
	}
	s.credentialsID = id
	s.mutex.Unlock()

	if hadEntries && !adopting {
		GetLogger().Info("Basic-auth credentials or session secret changed, all sign-in sessions ended")
	}
	s.persistTokensIfEnabled()
	return settings
}

// newOpaqueToken returns a fresh random token of byteLen random bytes, encoded
// for use in a cookie or a URL. crypto/rand.Read never returns an error: it
// crashes the program instead.
func newOpaqueToken(byteLen int) string {
	tokenBytes := make([]byte, byteLen)
	_, _ = rand.Read(tokenBytes)
	return base64.URLEncoding.EncodeToString(tokenBytes)
}

// sessionTokenFromRequest returns the session token the request's cookie
// carries, or "" when there is none.
func sessionTokenFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// sessionCookieOptions returns a copy of the cookie options the gothic session
// store applies to r: HttpOnly, SameSite=Lax and the Secure policy, including
// the plain-HTTP client exception. A copy, because the session object is
// cached per request. Without a store it falls back to a strict default.
func sessionCookieOptions(r *http.Request) sessions.Options {
	if gothic.Store != nil {
		if session, _ := gothic.Store.Get(r, gothic.SessionName); session != nil && session.Options != nil {
			return *session.Options
		}
	}
	return *buildSessionOptions(true, 0)
}

// writeSessionCookie sends the session cookie for token, at sign-in and again
// when a remembered session is renewed (renewSession). A remembered session gets
// a persistent cookie with the longest lifetime browsers keep; an unticked one a
// browser-session cookie. The server record decides when the session ends; the
// renewal keeps the cookie of a browser that is in use from running out first.
func writeSessionCookie(w http.ResponseWriter, r *http.Request, token string, remember bool) {
	opts := sessionCookieOptions(r)
	opts.MaxAge = 0
	if remember {
		opts.MaxAge = int(RememberedCookieMaxAge.Seconds())
	}
	http.SetCookie(w, sessions.NewCookie(SessionCookieName, token, &opts))
}

// deleteCookie sends a cookie that makes the browser drop the named cookie.
func deleteCookie(w http.ResponseWriter, r *http.Request, name string) {
	opts := sessionCookieOptions(r)
	opts.MaxAge = -1
	http.SetCookie(w, sessions.NewCookie(name, "", &opts))
}

// rememberChoiceCookie returns the name of the choice cookie of provider, the
// canonical goth provider name. One cookie per provider keeps sign-ins with
// different providers, started together in one browser, from sharing a choice.
func rememberChoiceCookie(provider string) string {
	return rememberChoiceCookieName + "_" + provider
}

// SetRememberChoice records the "Remember this browser" choice for the
// callback of provider that follows the redirect to the identity provider. A
// false choice clears any stale cookie of that provider.
func SetRememberChoice(w http.ResponseWriter, r *http.Request, provider string, remember bool) {
	if !remember {
		deleteCookie(w, r, rememberChoiceCookie(provider))
		return
	}
	opts := sessionCookieOptions(r)
	opts.MaxAge = rememberChoiceMaxAgeSeconds
	http.SetCookie(w, sessions.NewCookie(rememberChoiceCookie(provider), RememberQueryValue, &opts))
}

// TakeRememberChoice returns the choice SetRememberChoice recorded for provider
// and clears that cookie. An absent or unexpected value is false.
func TakeRememberChoice(w http.ResponseWriter, r *http.Request, provider string) bool {
	cookie, err := r.Cookie(rememberChoiceCookie(provider))
	if err != nil {
		return false
	}
	deleteCookie(w, r, rememberChoiceCookie(provider))
	return cookie.Value == RememberQueryValue
}

// liveRecord returns the unexpired session record for token, after ending the
// sessions of changed credentials (syncCredentials). It returns ErrTokenNotFound
// for an unknown token and ErrTokenExpired for an expired record.
func (s *OAuth2Server) liveRecord(token string) (AccessToken, error) {
	s.syncCredentials()
	return s.lookupRecord(token)
}

// lookupRecord returns the unexpired session record for token, without syncing
// credentials first: the caller has just done it (syncCredentials).
func (s *OAuth2Server) lookupRecord(token string) (AccessToken, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	rec, ok := s.accessTokens[sessionTokenKey(token)]
	if !ok {
		return AccessToken{}, ErrTokenNotFound
	}
	if !time.Now().Before(rec.ExpiresAt) {
		return AccessToken{}, ErrTokenExpired
	}
	return rec, nil
}

// Session returns the live session record the request's cookie names.
func (s *OAuth2Server) Session(r *http.Request) (AccessToken, bool) {
	token := sessionTokenFromRequest(r)
	if token == "" {
		return AccessToken{}, false
	}
	rec, err := s.liveRecord(token)
	return rec, err == nil
}

// revokeRequestSession deletes the record the request's cookie names, if any,
// except the one for keepToken, and reports whether it deleted one. The caller
// persists.
func (s *OAuth2Server) revokeRequestSession(r *http.Request, keepToken string) bool {
	token := sessionTokenFromRequest(r)
	if token == "" || token == keepToken {
		return false
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	key := sessionTokenKey(token)
	_, existed := s.accessTokens[key]
	delete(s.accessTokens, key)
	return existed
}

// StartSession starts the browser session for a password sign-in: it revokes
// the record the request's cookie names, waits for the result to be written to
// the token file (a failed write is logged and does not stop the sign-in), then
// sends the cookie for token, the access token ExchangeAuthCode issued.
func (s *OAuth2Server) StartSession(w http.ResponseWriter, r *http.Request, token string) error {
	rec, err := s.liveRecord(token)
	if err != nil {
		return err
	}
	s.revokeRequestSession(r, token)
	s.persistTokensIfEnabled()
	writeSessionCookie(w, r, token, rec.Remember)
	return nil
}

// ErrSessionProviderMissing is returned by StartProviderSession for an identity
// without a Provider: such a record would pass for a password session.
var ErrSessionProviderMissing = errors.NewStd("session identity has no provider")

// StartProviderSession starts the browser session for a social sign-in. It
// issues a record carrying the identity (Provider, UserID, ProviderUserID and
// IDToken of identity), revokes the browser's previous record, waits for the
// result to be written to the token file (a failed write is logged and does not
// stop the sign-in) and sends the cookie. Every sign-in gets a fresh token. It
// refuses a nil identity or one
// without a Provider with ErrSessionProviderMissing and issues nothing.
func (s *OAuth2Server) StartProviderSession(w http.ResponseWriter, r *http.Request, identity *AccessToken, remember bool) error {
	if identity == nil || identity.Provider == "" {
		return ErrSessionProviderMissing
	}
	s.syncCredentials()

	token := newOpaqueToken(AccessTokenByteLength)
	record := *identity
	record.Remember = remember
	record.ExpiresAt = time.Now().Add(sessionLifetime(remember))

	s.revokeRequestSession(r, token)
	s.mutex.Lock()
	s.accessTokens[sessionTokenKey(token)] = record
	s.mutex.Unlock()
	s.persistTokensIfEnabled()

	writeSessionCookie(w, r, token, remember)
	return nil
}

// EndSession ends the request's session: the record is deleted and, when the
// token file can be written, the deletion is written before it returns; a
// failed write is logged at error level. Then the cookie is cleared.
func (s *OAuth2Server) EndSession(w http.ResponseWriter, r *http.Request) {
	if s.revokeRequestSession(r, "") {
		if err := s.trySaveTokens(); err != nil {
			GetLogger().Error("Failed to persist the end of a session, the record may return after a restart", logger.Error(err))
		}
	}
	deleteCookie(w, r, SessionCookieName)
}

// checkSession reports whether the request carries a live session whose
// identity is still allowed, and renews it. It is the only session check.
func (s *OAuth2Server) checkSession(c echo.Context, log SecurityLogger) bool {
	token := sessionTokenFromRequest(c.Request())
	if token == "" {
		log.Debug("No session cookie")
		return false
	}
	settings := s.syncCredentials()
	if settings == nil {
		log.Debug("Settings unavailable")
		return false
	}
	rec, err := s.lookupRecord(token)
	if err != nil {
		log.Debug("Session cookie names no live session")
		return false
	}
	if !recordAllowed(settings, &rec, log) {
		return false
	}

	log.Debug("User authenticated: valid session")
	s.renewSession(c, token, &rec)
	c.Set(sessionRecordContextKey, rec)
	return true
}

// sessionRecordContextKey is the echo context key under which a successful
// session check stores the record it found.
const sessionRecordContextKey = "security:sessionRecord"

// SessionFromContext returns the session record an earlier IsUserAuthenticated
// on c found, so a caller later in the same request does not run the check
// again. ok is false when no session check succeeded on c (a request let in by
// the subnet bypass has none).
func SessionFromContext(c echo.Context) (AccessToken, bool) {
	rec, ok := c.Get(sessionRecordContextKey).(AccessToken)
	return rec, ok
}

// recordAllowed reports whether a live session record is still allowed under
// settings: a password record only while basic authentication is enabled, a
// social record while its own provider is enabled and lists its user.
func recordAllowed(settings *conf.Settings, rec *AccessToken, log SecurityLogger) bool {
	if rec.Provider != "" {
		return providerSessionAllowed(settings, rec, log)
	}
	if !settings.Security.BasicAuth.Enabled {
		log.Debug("Password session ignored: basic authentication is disabled")
		return false
	}
	return true
}

// providerSessionAllowed reports whether a social session's own provider is
// still enabled and still lists its user. It checks both the identity shown as
// the user name (typically the email) and the provider's user ID, since the
// administrator may have configured either.
func providerSessionAllowed(settings *conf.Settings, rec *AccessToken, log SecurityLogger) bool {
	provider := settings.GetOAuthProvider(gothToConfigProvider(rec.Provider))
	if provider == nil || !provider.Enabled {
		log.Debug("Session provider is not enabled", logger.String("provider", rec.Provider))
		return false
	}
	if isValidUserId(provider.UserID, rec.UserID) || isValidUserId(provider.UserID, rec.ProviderUserID) {
		return true
	}
	// The allowlist holds email addresses and user IDs, so it is never logged.
	log.Warn("Provider session found, but userId does not match allowed IDs", logger.String("provider", rec.Provider))
	return false
}

// sessionNeedsRenewal reports whether a record with expiry expiresAt is due an
// extension: it depends only on the record, so it holds across restarts.
func sessionNeedsRenewal(expiresAt time.Time, remember bool) bool {
	return time.Until(expiresAt) <= sessionLifetime(remember)-sessionRenewInterval(remember)
}

// Cache-Control directives renewSession looks at or sets.
const (
	cacheControlPrivate = "private"
	cacheControlNoStore = "no-store"
)

// renewSession slides the expiry of a live session on the server. Renewing a
// remembered session also re-sends its cookie, so a browser in daily use keeps
// its 30 days instead of reaching the end of the cookie's own Max-Age (see
// RememberedCookieMaxAge). A response that cannot carry a cookie (already
// committed, or a WebSocket upgrade) does not renew a remembered session: the
// next ordinary request does both. An unticked session's cookie has no Max-Age,
// so its renewal touches only the server record.
func (s *OAuth2Server) renewSession(c echo.Context, token string, rec *AccessToken) {
	if !sessionNeedsRenewal(rec.ExpiresAt, rec.Remember) {
		return
	}
	if rec.Remember && !responseCanCarryCookie(c) {
		return
	}
	if !s.extendToken(token) {
		return
	}
	if rec.Remember {
		s.resendRememberedCookie(c, token)
	}
	go s.persistTokensIfEnabled()
}

// responseCanCarryCookie reports whether a Set-Cookie can still reach the
// browser on c's response.
func responseCanCarryCookie(c echo.Context) bool {
	return !c.Response().Committed && !websocket.IsWebSocketUpgrade(c.Request())
}

// resendRememberedCookie adds the renewed session cookie to c's response when
// its headers are written, and only if the record still exists: a sign-in in
// another tab revokes the old record, and its cookie must not land after the new
// one. The response is marked private (unless it is no-store) so a shared cache
// never stores the Set-Cookie; this replaces a handler's own public directive.
func (s *OAuth2Server) resendRememberedCookie(c echo.Context, token string) {
	res, req := c.Response(), c.Request()
	res.Before(func() {
		if !s.recordExists(token) {
			return
		}
		writeSessionCookie(res, req, token, true)
		header := res.Header()
		if !strings.Contains(header.Get(echo.HeaderCacheControl), cacheControlNoStore) {
			header.Set(echo.HeaderCacheControl, cacheControlPrivate)
		}
	})
}

// recordExists reports whether a session record is stored for token.
func (s *OAuth2Server) recordExists(token string) bool {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	_, ok := s.accessTokens[sessionTokenKey(token)]
	return ok
}

// extendToken moves the expiry of the record for token to a full lifetime from
// now, re-checking under the write lock so that of several parallel requests
// only one extends (and so persists). It reports whether it extended.
func (s *OAuth2Server) extendToken(token string) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	key := sessionTokenKey(token)
	rec, ok := s.accessTokens[key]
	if !ok || !time.Now().Before(rec.ExpiresAt) || !sessionNeedsRenewal(rec.ExpiresAt, rec.Remember) {
		return false
	}
	rec.ExpiresAt = time.Now().Add(sessionLifetime(rec.Remember))
	s.accessTokens[key] = rec
	return true
}
