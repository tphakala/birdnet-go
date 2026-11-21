// internal/api/auth/adapter.go
package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/url"
	"reflect"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/security"
)

// SecurityAdapter adapts the security package to our API auth interface
type SecurityAdapter struct {
	OAuth2Server *security.OAuth2Server
}

// NewSecurityAdapter creates a new adapter for the security package
func NewSecurityAdapter(oauth2Server *security.OAuth2Server) *SecurityAdapter {
	return &SecurityAdapter{
		OAuth2Server: oauth2Server,
	}
}

// log returns the auth package logger.
func (a *SecurityAdapter) log() logger.Logger {
	return GetLogger()
}

// CheckAccess validates if a request has access to protected resources
// Returns nil if authenticated, ErrSessionNotFound otherwise.
func (a *SecurityAdapter) CheckAccess(c echo.Context) error {
	if a.OAuth2Server.IsUserAuthenticated(c) {
		return nil // Success
	}
	return ErrSessionNotFound // Failure
}

// IsAuthRequired checks if authentication is required for this request
func (a *SecurityAdapter) IsAuthRequired(c echo.Context) bool {
	return a.OAuth2Server.IsAuthenticationEnabled(c.Request())
}

// GetUsername retrieves the username of the authenticated user (if available)
// It prioritizes the username stored in the context by the authentication middleware.
func (a *SecurityAdapter) GetUsername(c echo.Context) string {
	// 1. Check context first (set by middleware after successful auth)
	if usernameCtx := c.Get(CtxKeyUsername); usernameCtx != nil {
		if username, ok := usernameCtx.(string); ok && username != "" {
			return username
		}
	}

	// 2. Fallback: the session record (for cases where middleware might not have set it, though it should)
	if rec, ok := a.sessionRecord(c); ok && rec.UserID != "" {
		a.log().Debug("Retrieved username from session as fallback",
			logger.String("path", c.Request().URL.Path),
			logger.String("ip", c.RealIP()))
		return rec.UserID
	}

	// No username found in context or session - this is expected for LAN bypass
	// where users are authenticated by IP without going through login flow
	a.log().Debug("No username in context or session (expected for subnet bypass)",
		logger.String("path", c.Request().URL.Path),
		logger.String("ip", c.RealIP()))
	return ""
}

// sessionRecord returns the session record of the request: the one an earlier
// session check left on the context, else the one the request's cookie names.
func (a *SecurityAdapter) sessionRecord(c echo.Context) (security.AccessToken, bool) {
	if rec, ok := security.SessionFromContext(c); ok {
		return rec, true
	}
	return a.OAuth2Server.Session(c.Request())
}

// GetAuthMethod returns the authentication method used as a defined constant.
// It prioritizes context values set by the middleware if available.
func (a *SecurityAdapter) GetAuthMethod(c echo.Context) AuthMethod {
	// 1. Check context first (set by middleware)
	if authMethodCtx := c.Get(CtxKeyAuthMethod); authMethodCtx != nil {
		// Check if it's the expected AuthMethod type
		if method, ok := authMethodCtx.(AuthMethod); ok {
			// Return method determined by middleware (e.g., Token, Session)
			return method
		}

		// Check if it's a string representation
		if methodStr, ok := authMethodCtx.(string); ok {
			// Convert string to AuthMethod if possible
			convertedMethod := AuthMethodFromString(methodStr)
			if convertedMethod != AuthMethodUnknown {
				return convertedMethod
			}
		}

		// If type assertion or conversion fails, log it but fall through to other checks
		a.log().Warn("Context value for auth method has unexpected type or invalid string value",
			logger.Any("type", reflect.TypeOf(authMethodCtx)),
			logger.Any("value", authMethodCtx))
	}

	// 2. Check subnet bypass (if context wasn't set or middleware didn't handle)
	if a.OAuth2Server.IsRequestFromAllowedSubnet(c.Request()) {
		return AuthMethodLocalSubnet // Changed from AuthMethodUnknown
	}

	// 3. Check generic authentication status (if context wasn't set). A session
	// check that already succeeded on this request left its record on the context.
	// This might catch session types not explicitly handled by middleware context setting.
	if _, ok := security.SessionFromContext(c); ok || a.OAuth2Server.IsUserAuthenticated(c) {
		// Could attempt more detailed session type detection here if needed,
		// but for now, return generic Session if middleware didn't specify.
		return AuthMethodBrowserSession // Use BrowserSession for generic session
	}

	// 4. If none of the above, assume no authentication
	return AuthMethodNone // Use None for explicitly no authentication
}

// AuthMethodFromString converts a string representation to its AuthMethod constant.
// Returns AuthMethodUnknown if the string does not match any known method.
func AuthMethodFromString(s string) AuthMethod {
	switch s {
	case AuthMethodBrowserSession.String():
		return AuthMethodBrowserSession
	case AuthMethodAPIKey.String():
		return AuthMethodAPIKey
	case AuthMethodLocalSubnet.String():
		return AuthMethodLocalSubnet
	case AuthMethodBasicAuth.String():
		return AuthMethodBasicAuth
	case AuthMethodToken.String():
		return AuthMethodToken
	case AuthMethodOAuth2.String():
		return AuthMethodOAuth2
	case AuthMethodNone.String():
		return AuthMethodNone
	default:
		return AuthMethodUnknown
	}
}

// ValidateToken checks if a bearer token is valid by calling the underlying OAuth2Server.
// Returns the specific error from OAuth2Server.ValidateAccessToken if validation fails,
// or nil if the token is valid.
func (a *SecurityAdapter) ValidateToken(token string) error {
	// Directly return the error from the underlying validation method.
	return a.OAuth2Server.ValidateAccessToken(token)
}

// AuthenticateBasic handles basic authentication with username/password.
// NOTE: This application does not support multiple user accounts or authorization levels.
// Basic authentication relies on a single, fixed username/password combination
// configured in settings (Security.BasicAuth.ClientID and Security.BasicAuth.Password).
//
// Username validation behavior:
// - If ClientID is configured (non-empty): username MUST match ClientID
// - If ClientID is empty: username check is skipped (backwards compatible with V1)
//
// This ensures backwards compatibility with configurations that don't have ClientID set
// while still allowing stricter authentication when ClientID is explicitly configured.
// See Issue #1234 for details.
//
// remember is the "Remember this browser" choice; the session issued for the
// code keeps it.
//
// Returns auth code on success, error on failure.
func (a *SecurityAdapter) AuthenticateBasic(c echo.Context, username, password string, remember bool) (string, error) {
	a.log().Info("Basic authentication login attempt", logger.Username(username))

	// Snapshot the live settings once so credentials configured through the web
	// UI take effect without a restart (issue #3370), and so the enabled and
	// credential checks observe a single consistent view (TOCTOU guard).
	settings := a.OAuth2Server.CurrentSettings()
	if settings == nil {
		a.log().Warn("Basic authentication failed: settings unavailable", logger.Username(username))
		return "", ErrBasicAuthDisabled
	}
	basicAuth := settings.Security.BasicAuth

	if err := a.validateBasicAuthEnabled(basicAuth.Enabled, username); err != nil {
		return "", err
	}

	storedPassword := basicAuth.Password
	storedClientID := basicAuth.ClientID

	a.logDebugAuthConfig(username, storedClientID)

	userMatch := a.validateUsername(username, storedClientID)
	passMatch := a.validatePassword(password, storedPassword)

	if !userMatch || !passMatch {
		return "", a.handleAuthFailure(userMatch, username)
	}

	return a.generateAuthCodeOnSuccess(settings, username, remember)
}

// validateBasicAuthEnabled checks if basic auth is enabled. The enabled flag is
// read from a live settings snapshot by the caller so UI changes apply without a
// restart (issue #3370).
func (a *SecurityAdapter) validateBasicAuthEnabled(enabled bool, username string) error {
	if !enabled {
		a.log().Warn("Basic authentication failed: Basic auth not enabled",
			logger.Username(username))
		return ErrBasicAuthDisabled
	}
	return nil
}

// logDebugAuthConfig logs debug information about auth configuration.
func (a *SecurityAdapter) logDebugAuthConfig(username, storedClientID string) {
	a.log().Debug("BasicAuth configuration check",
		logger.String("provided_username", username),
		logger.String("configured_clientid", storedClientID),
		logger.Bool("clientid_empty", storedClientID == ""),
		logger.Bool("clientid_match", username == storedClientID))
}

// validateUsername checks if the provided username matches the stored ClientID.
func (a *SecurityAdapter) validateUsername(username, storedClientID string) bool {
	if storedClientID == "" {
		a.log().Debug("ClientID is empty, skipping username validation (V1 compatible mode)")
		return true
	}

	usernameHash := sha256.Sum256([]byte(username))
	storedClientIDHash := sha256.Sum256([]byte(storedClientID))
	return subtle.ConstantTimeCompare(usernameHash[:], storedClientIDHash[:]) == 1
}

// validatePassword checks if the provided password matches the stored password.
func (a *SecurityAdapter) validatePassword(password, storedPassword string) bool {
	passwordHash := sha256.Sum256([]byte(password))
	storedPasswordHash := sha256.Sum256([]byte(storedPassword))
	return subtle.ConstantTimeCompare(passwordHash[:], storedPasswordHash[:]) == 1
}

// handleAuthFailure logs the appropriate failure message and returns an error.
func (a *SecurityAdapter) handleAuthFailure(userMatch bool, username string) error {
	if !userMatch {
		a.log().Warn("Basic authentication failed: Invalid username",
			logger.Username(username))
	} else {
		a.log().Warn("Basic authentication failed: Invalid password",
			logger.Username(username))
	}
	return ErrInvalidCredentials
}

// generateAuthCodeOnSuccess generates an auth code after successful
// authentication against settings, the snapshot the password was checked
// against. It fails like a wrong password when the credentials changed since.
func (a *SecurityAdapter) generateAuthCodeOnSuccess(settings *conf.Settings, username string, remember bool) (string, error) {
	log := a.log()
	log.Info("Credentials validated successfully", logger.Username(username))

	authCode, err := a.OAuth2Server.GenerateAuthCode(settings, remember)
	if err != nil {
		log.Warn("Basic authentication failed: credentials changed during sign-in", logger.Username(username))
		return "", ErrInvalidCredentials
	}

	log.Info("Basic authentication successful", logger.Username(username))
	return authCode, nil
}

// Logout ends the browser's session: the record is deleted and the cookie
// cleared. The gothic session is cleared too, to drop a leftover handshake or a
// session from before the session record existed.
func (a *SecurityAdapter) Logout(c echo.Context) error {
	a.OAuth2Server.EndSession(c.Response(), c.Request())
	return gothic.Logout(c.Response().Writer, c.Request())
}

// oidcLogoutSkippedPrefix starts every message that reports a skipped OIDC
// sign-out at the identity provider.
const oidcLogoutSkippedPrefix = "OIDC sign-out at the identity provider skipped: "

// logoutReturnNotSentPrefix starts every message that explains why the
// post-logout return URL was left out while the sign-out itself goes ahead.
const logoutReturnNotSentPrefix = "post-logout return URL not sent: "

// GetProviderLogoutURL returns the provider's end-session URL for RP-Initiated Logout.
// Returns empty string if the active provider doesn't support it.
func (a *SecurityAdapter) GetProviderLogoutURL(c echo.Context) string {
	log := a.log()

	if rec, ok := a.OAuth2Server.Session(c.Request()); ok && rec.Provider != "" {
		return a.buildProviderLogoutURL(&rec, log)
	}

	settings := a.OAuth2Server.CurrentSettings()
	if settings != nil && settings.IsOAuthProviderEnabled(security.ConfigOIDC) {
		security.LogOIDCLogoutEvent(logger.LogLevelInfo,
			oidcLogoutSkippedPrefix+"the session was not created by an OAuth provider")
	} else {
		log.Debug("No OAuth provider session found, skipping provider logout")
	}
	return ""
}

// logLogoutSkipped reports that provider sign-out was skipped. For the OIDC
// provider it reaches the console as well as the security log, at oidcLevel.
// Other providers stay on the package logger at otherLevel: most of them never
// support provider sign-out, so that outcome is not worth more than Debug.
func logLogoutSkipped(log logger.Logger, oidcLevel, otherLevel logger.LogLevel, gothProviderName, reason string, fields ...logger.Field) {
	if gothProviderName == security.ProviderOIDC {
		security.LogOIDCLogoutEvent(oidcLevel, oidcLogoutSkippedPrefix+reason, fields...)
		return
	}
	fields = append(fields, logger.String("provider", gothProviderName))
	log.Log(otherLevel, reason, fields...)
}

// postLogoutReturnPath is the page the identity provider sends the browser to
// after sign-out, relative to the OAuth origin.
const postLogoutReturnPath = "/ui/"

// endSessionSchemeAllowed reports whether an end-session URL with the given
// scheme may be sent to the browser: https always, http only when the
// configured issuer is itself http.
func endSessionSchemeAllowed(scheme string, entry *conf.OAuthProviderConfig) bool {
	if scheme == conf.SchemeHTTPS {
		return true
	}
	if scheme != conf.SchemeHTTP || entry == nil {
		return false
	}
	issuer, err := url.Parse(entry.IssuerURL)
	return err == nil && issuer.Scheme == conf.SchemeHTTP
}

// postLogoutReturnURL returns the URL the identity provider should send the
// browser to after sign-out, or an empty string for none: for any provider other
// than OIDC, when the entry has the option off, when the session holds no ID
// token, or when sec has no usable OAuth origin. Otherwise it is sec's OAuth
// origin plus postLogoutReturnPath, so it never comes from a request. It needs
// the ID token because the RP-Initiated Logout 1.0 specification lets a provider
// refuse the redirect without a hint.
func postLogoutReturnURL(gothProviderName string, entry *conf.OAuthProviderConfig, sec *conf.Security, idToken string) string {
	if gothProviderName != security.ProviderOIDC || entry == nil || sec == nil || !entry.PostLogoutRedirect {
		return ""
	}
	if idToken == "" {
		security.LogOIDCLogoutEvent(logger.LogLevelInfo, logoutReturnNotSentPrefix+"the session holds no ID token")
		return ""
	}
	origin := sec.OAuthOrigin()
	if origin == "" {
		security.LogOIDCLogoutEvent(logger.LogLevelInfo, logoutReturnNotSentPrefix+"no usable base URL or host is configured")
		return ""
	}
	return origin + postLogoutReturnPath
}

// buildProviderLogoutURL builds the end-session URL for a specific goth provider.
// Returns empty string if the provider doesn't support RP-Initiated Logout or URL validation fails
// (the scheme must be https, or http when the OIDC issuer URL is http).
// For OIDC the URL also carries the return URL when postLogoutReturnURL returns one.
// The URL carries the ID token hint, so it is never logged.
func (a *SecurityAdapter) buildProviderLogoutURL(rec *security.AccessToken, log logger.Logger) string {
	gothProviderName := rec.Provider
	gothProvider, err := goth.GetProvider(gothProviderName)
	if err != nil {
		logLogoutSkipped(log, logger.LogLevelWarn, logger.LogLevelDebug, gothProviderName, "provider is not registered", logger.Error(err))
		return ""
	}

	logoutProvider, ok := gothProvider.(goth.LogoutProvider)
	if !ok {
		logLogoutSkipped(log, logger.LogLevelWarn, logger.LogLevelDebug, gothProviderName, "provider does not support RP-Initiated Logout")
		return ""
	}

	// The ID token kept in the session record is the hint
	idToken := rec.IDToken

	// Settings are read per sign-out so a change applies without a restart.
	var entry *conf.OAuthProviderConfig
	var sec *conf.Security
	if settings := a.OAuth2Server.CurrentSettings(); settings != nil {
		entry = settings.GetOAuthProvider(security.GothToConfigProvider[gothProviderName])
		sec = &settings.Security
	}

	logoutURL, err := logoutProvider.EndSessionURL(idToken, postLogoutReturnURL(gothProviderName, entry, sec, idToken), "")
	if err != nil {
		// A parse error embeds the endpoint URL, query included, so it is
		// reported by kind only; other errors carry no URL.
		if _, isURLErr := errors.AsType[*url.Error](err); isURLErr {
			logLogoutSkipped(log, logger.LogLevelWarn, logger.LogLevelWarn, gothProviderName, "the end-session endpoint is not a valid URL")
		} else {
			logLogoutSkipped(log, logger.LogLevelWarn, logger.LogLevelWarn, gothProviderName, "could not build the end-session URL", logger.Error(err))
		}
		return ""
	}

	// Validate the URL scheme and host
	parsed, parseErr := url.Parse(logoutURL)
	if parseErr != nil || !endSessionSchemeAllowed(parsed.Scheme, entry) || parsed.Host == "" {
		var fields []logger.Field
		if parseErr == nil {
			fields = append(fields, logger.String("scheme", parsed.Scheme), logger.String("host", parsed.Host))
		}
		logLogoutSkipped(log, logger.LogLevelWarn, logger.LogLevelWarn, gothProviderName, "end-session URL is not HTTPS (HTTP is allowed only for an HTTP issuer) or has no host", fields...)
		return ""
	}

	log.Info("Built RP-Initiated Logout URL",
		logger.String("provider", gothProviderName),
		logger.Bool("id_token_hint", idToken != ""))
	return parsed.String()
}

// ExchangeAuthCode exchanges an authorization code for an access token.
// This delegates to the underlying OAuth2Server.
func (a *SecurityAdapter) ExchangeAuthCode(ctx context.Context, code string) (string, error) {
	return a.OAuth2Server.ExchangeAuthCode(ctx, code)
}

// EstablishSession starts the browser session for the given access token: it
// revokes the record the request's cookie names, then sends the session cookie.
// The new token is never one the browser held before, so a planted cookie
// cannot become a signed-in one (session fixation).
func (a *SecurityAdapter) EstablishSession(c echo.Context, accessToken string) error {
	log := a.log()
	if err := a.OAuth2Server.StartSession(c.Response(), c.Request(), accessToken); err != nil {
		log.Error("Failed to start session", logger.Error(err))
		return err
	}
	log.Info("Successfully started session")
	return nil
}

// IsAuthenticated checks if a request is authenticated via any supported method.
// Returns true if auth is bypassed (not required) or if token/session auth succeeds.
// This centralizes authentication checking logic to avoid duplication across handlers.
func (a *SecurityAdapter) IsAuthenticated(c echo.Context) bool {
	// Check if auth is required for this client (e.g., local subnet bypass)
	if !a.IsAuthRequired(c) {
		return true // Bypassed auth is treated as authenticated for data access
	}

	// Try token auth from Authorization header
	if authHeader := c.Request().Header.Get("Authorization"); authHeader != "" {
		parts := strings.Fields(authHeader)
		if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
			if a.ValidateToken(parts[1]) == nil {
				return true
			}
		}
	}

	// Try session auth
	return a.CheckAccess(c) == nil
}
