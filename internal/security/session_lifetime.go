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
	return SessionIdleTimeout
}

// sessionRenewInterval returns the most often a session is extended.
func sessionRenewInterval(remember bool) time.Duration {
	if remember {
		return RememberedSessionRenewInterval
	}
	return IdleSessionRenewInterval
}

// sessionTokenKey returns the map key and on-disk identifier of a session
// token: the hex SHA-256 of the token, so the persisted file cannot be used to
// sign in.
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

// syncCredentials ends every session and authorization code when the basic-auth
// client ID or password differ from those the stored sessions were issued
// under. It persists the result before it returns, so a restart cannot bring
// the sessions back. A store that has never recorded credentials has issued
// nothing under any (every save follows a sync), so it adopts the live ones
// without ending anything. It must not be called with s.mutex held.
func (s *OAuth2Server) syncCredentials(settings *conf.Settings) {
	if settings == nil {
		return
	}
	id := basicCredentialsID(settings)

	s.mutex.RLock()
	same := id == s.credentialsID
	s.mutex.RUnlock()
	if same {
		return
	}

	s.mutex.Lock()
	if id == s.credentialsID {
		s.mutex.Unlock()
		return
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
		GetLogger().Info("Basic-auth credentials changed, all sign-in sessions ended")
	}
	s.persistTokensIfEnabled()
}

// newOpaqueToken returns a fresh random token for a session cookie.
func newOpaqueToken() (string, error) {
	tokenBytes := make([]byte, AccessTokenByteLength)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", errors.New(err).Component("security").Category(errors.CategorySystem).Build()
	}
	return base64.URLEncoding.EncodeToString(tokenBytes), nil
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

// writeSessionCookie sends the session cookie for token. A remembered session
// gets a persistent cookie; an unticked one a browser-session cookie.
func writeSessionCookie(w http.ResponseWriter, r *http.Request, token string, remember bool) {
	opts := sessionCookieOptions(r)
	opts.MaxAge = 0
	if remember {
		opts.MaxAge = int(RememberedSessionDuration.Seconds())
	}
	http.SetCookie(w, sessions.NewCookie(SessionCookieName, token, &opts))
}

// deleteCookie sends a cookie that makes the browser drop the named cookie.
func deleteCookie(w http.ResponseWriter, r *http.Request, name string) {
	opts := sessionCookieOptions(r)
	opts.MaxAge = -1
	http.SetCookie(w, sessions.NewCookie(name, "", &opts))
}

// SetRememberChoice records the "Remember this browser" choice for the
// provider callback that follows the redirect to the identity provider. A false
// choice clears any stale cookie.
func SetRememberChoice(w http.ResponseWriter, r *http.Request, remember bool) {
	if !remember {
		deleteCookie(w, r, rememberChoiceCookieName)
		return
	}
	opts := sessionCookieOptions(r)
	opts.MaxAge = rememberChoiceMaxAgeSeconds
	http.SetCookie(w, sessions.NewCookie(rememberChoiceCookieName, RememberQueryValue, &opts))
}

// TakeRememberChoice returns the choice SetRememberChoice recorded and clears
// the cookie. An absent or unexpected value is false.
func TakeRememberChoice(w http.ResponseWriter, r *http.Request) bool {
	cookie, err := r.Cookie(rememberChoiceCookieName)
	if err != nil {
		return false
	}
	deleteCookie(w, r, rememberChoiceCookieName)
	return cookie.Value == RememberQueryValue
}

// liveRecord returns the unexpired session record for token.
func (s *OAuth2Server) liveRecord(token string) (AccessToken, bool) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	rec, ok := s.accessTokens[sessionTokenKey(token)]
	if !ok || !time.Now().Before(rec.ExpiresAt) {
		return AccessToken{}, false
	}
	return rec, true
}

// Session returns the live session record the request's cookie names.
func (s *OAuth2Server) Session(r *http.Request) (AccessToken, bool) {
	token := sessionTokenFromRequest(r)
	if token == "" {
		return AccessToken{}, false
	}
	s.syncCredentials(s.currentSettings())
	return s.liveRecord(token)
}

// revokeRequestSession deletes the record the request's cookie names, if any,
// except the one for keepToken.
func (s *OAuth2Server) revokeRequestSession(r *http.Request, keepToken string) {
	token := sessionTokenFromRequest(r)
	if token == "" || token == keepToken {
		return
	}
	s.mutex.Lock()
	key := sessionTokenKey(token)
	_, existed := s.accessTokens[key]
	delete(s.accessTokens, key)
	s.mutex.Unlock()
	if existed {
		go s.persistTokensIfEnabled()
	}
}

// StartSession starts the browser session for a password sign-in: it revokes
// the record the request's cookie names, then sends the cookie for token, the
// access token ExchangeAuthCode issued.
func (s *OAuth2Server) StartSession(w http.ResponseWriter, r *http.Request, token string) error {
	rec, ok := s.liveRecord(token)
	if !ok {
		return ErrTokenNotFound
	}
	s.revokeRequestSession(r, token)
	writeSessionCookie(w, r, token, rec.Remember)
	return nil
}

// StartProviderSession starts the browser session for a social sign-in. It
// issues a record carrying the identity (Provider, UserID, ProviderUserID and
// IDToken of identity), revokes the browser's previous record and sends the
// cookie. Every sign-in gets a fresh token.
func (s *OAuth2Server) StartProviderSession(w http.ResponseWriter, r *http.Request, identity *AccessToken, remember bool) error {
	s.syncCredentials(s.currentSettings())

	token, err := newOpaqueToken()
	if err != nil {
		return err
	}

	record := *identity
	record.Remember = remember
	record.ExpiresAt = time.Now().Add(sessionLifetime(remember))

	s.revokeRequestSession(r, token)
	s.mutex.Lock()
	s.accessTokens[sessionTokenKey(token)] = record
	s.mutex.Unlock()
	go s.persistTokensIfEnabled()

	writeSessionCookie(w, r, token, remember)
	return nil
}

// EndSession ends the request's session: the record is deleted and the
// deletion persisted before it returns, then the cookie is cleared.
func (s *OAuth2Server) EndSession(w http.ResponseWriter, r *http.Request) {
	if token := sessionTokenFromRequest(r); token != "" {
		s.mutex.Lock()
		delete(s.accessTokens, sessionTokenKey(token))
		s.mutex.Unlock()
		s.persistTokensIfEnabled()
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
	settings := s.currentSettings()
	s.syncCredentials(settings)

	rec, ok := s.liveRecord(token)
	if !ok {
		log.Debug("Session cookie names no live session")
		return false
	}

	if rec.Provider == "" {
		if !settings.Security.BasicAuth.Enabled {
			log.Debug("Password session ignored: basic authentication is disabled")
			return false
		}
	} else if !providerSessionAllowed(settings, &rec, log) {
		return false
	}

	log.Info("User authenticated: valid session")
	s.renewSession(c, token, &rec)
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

// renewSession slides the expiry of a live session. A remembered session is
// extended only on a request whose response can carry the renewed cookie, so
// the cookie and the record never drift apart.
func (s *OAuth2Server) renewSession(c echo.Context, token string, rec *AccessToken) {
	if !sessionNeedsRenewal(rec.ExpiresAt, rec.Remember) {
		return
	}
	if rec.Remember && (c.Response().Committed || c.IsWebSocket()) {
		return
	}
	if !s.extendToken(token, rec.Remember) {
		return
	}
	go s.persistTokensIfEnabled()

	if !rec.Remember {
		return
	}
	writeSessionCookie(c.Response(), c.Request(), token, true)
	c.Response().Before(func() {
		markResponsePrivate(c.Response().Header())
	})
}

// extendToken moves the expiry of the record for token to a full lifetime from
// now, re-checking under the write lock so that of several parallel requests
// only one extends (and so persists). It reports whether it extended.
func (s *OAuth2Server) extendToken(token string, remember bool) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	key := sessionTokenKey(token)
	rec, ok := s.accessTokens[key]
	if !ok || !time.Now().Before(rec.ExpiresAt) || !sessionNeedsRenewal(rec.ExpiresAt, rec.Remember) {
		return false
	}
	rec.ExpiresAt = time.Now().Add(sessionLifetime(remember))
	s.accessTokens[key] = rec
	return true
}

// markResponsePrivate keeps shared caches from storing a response that sets the
// session cookie: some authenticated routes send Cache-Control: public.
func markResponsePrivate(h http.Header) {
	current := h.Get("Cache-Control")
	switch {
	case current == "":
		h.Set("Cache-Control", "private")
	case strings.Contains(current, "no-store"), strings.Contains(current, "private"):
		// Already not shared-cacheable.
	case strings.Contains(current, "public"):
		h.Set("Cache-Control", strings.Replace(current, "public", "private", 1))
	default:
		h.Set("Cache-Control", current+", private")
	}
}
