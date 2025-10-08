// internal/api/v2/auth/adapter.go
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"reflect"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/markbates/goth/gothic"
	"github.com/tphakala/birdnet-go/internal/security"
	"golang.org/x/crypto/bcrypt"
)

// SecurityAdapter adapts the security package to our API auth interface
type SecurityAdapter struct {
	OAuth2Server *security.OAuth2Server
	logger       *slog.Logger
}

// NewSecurityAdapter creates a new adapter for the security package
func NewSecurityAdapter(oauth2Server *security.OAuth2Server, logger *slog.Logger) *SecurityAdapter {
	return &SecurityAdapter{
		OAuth2Server: oauth2Server,
		logger:       logger,
	}
}

// isBcryptHash checks if a password string is a bcrypt hash by examining its prefix.
// This is much faster than calling bcrypt.Cost() and provides clear detection.
// Bcrypt hashes start with known prefixes: $2a$, $2b$, $2y$ (and legacy $2x$).
func isBcryptHash(password string) bool {
	return strings.HasPrefix(password, "$2a$") ||
		strings.HasPrefix(password, "$2b$") ||
		strings.HasPrefix(password, "$2y$") ||
		strings.HasPrefix(password, "$2x$") // legacy format, rarely used
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
	if usernameCtx := c.Get("username"); usernameCtx != nil {
		if username, ok := usernameCtx.(string); ok && username != "" {
			return username
		}
	}

	// 2. Fallback: Try to get username from session (for cases where middleware might not have set it, though it should)
	//    NOTE: Removed the redundant token validation logic that was here.
	//    If authentication succeeded, the username should already be in the context.
	userId, err := gothic.GetFromSession("userId", c.Request())
	if err == nil && userId != "" {
		if a.logger != nil {
			a.logger.Debug("Retrieved username from session as fallback", "path", c.Request().URL.Path, "ip", c.RealIP())
		}
		return userId
	}

	// No username found in context or session
	if a.logger != nil {
		a.logger.Warn("Could not retrieve username from context or session", "path", c.Request().URL.Path, "ip", c.RealIP())
	}
	return ""
}

// GetAuthMethod returns the authentication method used as a defined constant.
// It prioritizes context values set by the middleware if available.
func (a *SecurityAdapter) GetAuthMethod(c echo.Context) AuthMethod {
	// 1. Check context first (set by middleware)
	if authMethodCtx := c.Get("authMethod"); authMethodCtx != nil {
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
		if a.logger != nil {
			a.logger.Warn("Context value 'authMethod' has unexpected type or invalid string value", slog.Any("type", reflect.TypeOf(authMethodCtx)), "value", authMethodCtx)
		}
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
// The provided username MUST match the configured ClientID.
// Returns auth code on success, error on failure.
func (a *SecurityAdapter) AuthenticateBasic(c echo.Context, username, password string) (string, error) {
	// For basic auth, check against configured ClientID and Password
	storedPassword := a.OAuth2Server.Settings.Security.BasicAuth.Password
	storedClientID := a.OAuth2Server.Settings.Security.BasicAuth.ClientID // Use ClientID as the username

	// Log basic auth attempt
	security.LogInfo("Basic authentication login attempt", "username", username)

	// Temporary debug logging to diagnose auth issues
	if a.logger != nil {
		a.logger.Debug("BasicAuth configuration check",
			"provided_username", username,
			"configured_clientid", storedClientID,
			"clientid_match", username == storedClientID)
	}

	// Skip if basic auth is not enabled
	if !a.OAuth2Server.Settings.Security.BasicAuth.Enabled {
		if a.logger != nil {
			a.logger.Debug("Basic auth is not enabled")
		}
		security.LogWarn("Basic authentication failed: Basic auth not enabled", "username", username)
		return "", ErrBasicAuthDisabled // Return the specific error for disabled basic auth
	}

	// Check username with constant-time comparison
	usernameHash := sha256.Sum256([]byte(username))
	storedClientIDHash := sha256.Sum256([]byte(storedClientID))
	userMatch := subtle.ConstantTimeCompare(usernameHash[:], storedClientIDHash[:]) == 1

	// Check password - handle both hashed and legacy plaintext passwords
	var passMatch bool
	// Use prefix checking to detect bcrypt hashes - much faster than bcrypt.Cost()
	if isBcryptHash(storedPassword) {
		// Password is already hashed with bcrypt
		err := bcrypt.CompareHashAndPassword([]byte(storedPassword), []byte(password))
		passMatch = (err == nil)

		if err != nil && !errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			security.LogError("Error comparing bcrypt password", "error", err.Error())
		}
	} else {
		// Legacy: plaintext password comparison (for backwards compatibility)
		// Use constant-time comparison for security
		passwordHash := sha256.Sum256([]byte(password))
		storedPasswordHash := sha256.Sum256([]byte(storedPassword))
		passMatch = subtle.ConstantTimeCompare(passwordHash[:], storedPasswordHash[:]) == 1

		if passMatch {
			security.LogInfo("Legacy plaintext password detected - consider re-saving settings to hash password")
		}
	}

	credentialsValid := userMatch && passMatch

	if credentialsValid {
		if a.logger != nil {
			a.logger.Info("Credentials validated successfully", "username", username)
		}

		// Generate auth code for OAuth callback (V1 pattern - no session storage)
		authCode, err := a.OAuth2Server.GenerateAuthCode()
		if err != nil {
			if a.logger != nil {
				a.logger.Error("Failed to generate auth code during basic auth", "error", err.Error())
			}
			security.LogError("Basic authentication failed: Internal error", "username", username, "error", "auth code generation failed")
			// Treat internal errors during login also as invalid credentials from user's perspective
			return "", ErrInvalidCredentials
		}

		if a.logger != nil {
			a.logger.Info("Auth code generated successfully", "username", username, "auth_code_length", len(authCode))
		}

		// Log successful authentication
		security.LogInfo("Basic authentication successful", "username", username)
		return authCode, nil // Return auth code directly (V1 pattern)
	}

	// Log failed authentication attempt
	if !userMatch {
		security.LogWarn("Basic authentication failed: Invalid username", "username", username)
	} else {
		security.LogWarn("Basic authentication failed: Invalid password", "username", username)
	}
	return "", ErrInvalidCredentials // Failure
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
