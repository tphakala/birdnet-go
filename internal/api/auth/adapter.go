// internal/api/auth/adapter.go
package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"reflect"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/markbates/goth/gothic"
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
	return a.OAuth2Server.IsAuthenticationEnabled(c.RealIP())
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

	// 2. Fallback: Try to get username from session (for cases where middleware might not have set it, though it should)
	//    NOTE: Removed the redundant token validation logic that was here.
	//    If authentication succeeded, the username should already be in the context.
	userId, err := gothic.GetFromSession("userId", c.Request())
	if err == nil && userId != "" {
		a.log().Debug("Retrieved username from session as fallback",
			logger.String("path", c.Request().URL.Path),
			logger.String("ip", c.RealIP()))
		return userId
	}

	// No username found in context or session - this is expected for LAN bypass
	// where users are authenticated by IP without going through login flow
	a.log().Debug("No username in context or session (expected for subnet bypass)",
		logger.String("path", c.Request().URL.Path),
		logger.String("ip", c.RealIP()))
	return ""
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
	if a.OAuth2Server.IsRequestFromAllowedSubnet(c.RealIP()) {
		return AuthMethodLocalSubnet // Changed from AuthMethodUnknown
	}

	// 3. Check generic authentication status (if context wasn't set)
	// This might catch session types not explicitly handled by middleware context setting.
	if a.OAuth2Server.IsUserAuthenticated(c) {
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
// Returns auth code on success, error on failure.
func (a *SecurityAdapter) AuthenticateBasic(c echo.Context, username, password string) (string, error) {
	a.log().Info("Basic authentication login attempt", logger.Username(username))

	if err := a.validateBasicAuthEnabled(username); err != nil {
		return "", err
	}

	storedPassword := a.OAuth2Server.Settings.Security.BasicAuth.Password
	storedClientID := a.OAuth2Server.Settings.Security.BasicAuth.ClientID

	a.logDebugAuthConfig(username, storedClientID)

	userMatch := a.validateUsername(username, storedClientID)
	passMatch := a.validatePassword(password, storedPassword)

	if !userMatch || !passMatch {
		return "", a.handleAuthFailure(userMatch, username)
	}

	return a.generateAuthCodeOnSuccess(username)
}

// validateBasicAuthEnabled checks if basic auth is enabled.
func (a *SecurityAdapter) validateBasicAuthEnabled(username string) error {
	if !a.OAuth2Server.Settings.Security.BasicAuth.Enabled {
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

// generateAuthCodeOnSuccess generates an auth code after successful authentication.
func (a *SecurityAdapter) generateAuthCodeOnSuccess(username string) (string, error) {
	log := a.log()
	log.Info("Credentials validated successfully", logger.Username(username))

	authCode, err := a.OAuth2Server.GenerateAuthCode()
	if err != nil {
		log.Error("Failed to generate auth code during basic auth",
			logger.Username(username),
			logger.Error(err))
		return "", ErrAuthCodeGeneration
	}

	log.Info("Basic authentication successful",
		logger.Username(username),
		logger.Bool("auth_code_generated", authCode != ""))
	return authCode, nil
}

// Logout invalidates the current session/token
func (a *SecurityAdapter) Logout(c echo.Context) error {
	// Clear all session values
	gothic.StoreInSession("userId", "", c.Request(), c.Response())       //nolint:errcheck // Error checking not critical during logout
	gothic.StoreInSession("access_token", "", c.Request(), c.Response()) //nolint:errcheck // Error checking not critical during logout
	gothic.StoreInSession("google", "", c.Request(), c.Response())       //nolint:errcheck // Error checking not critical during logout
	gothic.StoreInSession("github", "", c.Request(), c.Response())       //nolint:errcheck // Error checking not critical during logout

	// Log out from gothic session
	return gothic.Logout(c.Response().Writer, c.Request())
}

// ExchangeAuthCode exchanges an authorization code for an access token.
// This delegates to the underlying OAuth2Server.
func (a *SecurityAdapter) ExchangeAuthCode(ctx context.Context, code string) (string, error) {
	return a.OAuth2Server.ExchangeAuthCode(ctx, code)
}

// EstablishSession creates a new session with the given access token.
// Handles session fixation mitigation by clearing old session first.
func (a *SecurityAdapter) EstablishSession(c echo.Context, accessToken string) error {
	log := a.log()
	// Session fixation mitigation: clear old session first
	// This regenerates the session ID to prevent session fixation attacks
	if err := gothic.Logout(c.Response().Writer, c.Request()); err != nil {
		log.Warn("Error during session regeneration (session fixation mitigation)", logger.Error(err))
		// Continue anyway - StoreInSession might still create a new session
	} else {
		log.Info("Successfully cleared old session before storing new token (session fixation mitigation)")
	}

	// Store access token in new session
	if err := gothic.StoreInSession("access_token", accessToken, c.Request(), c.Response()); err != nil {
		log.Error("Failed to store access token in new session after logout/regeneration", logger.Error(err))
		return err
	}

	log.Info("Successfully stored access token in new session")
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
