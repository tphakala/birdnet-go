// internal/api/v2/auth/adapter.go
package auth

import (
	"crypto/subtle"
	"log/slog"
	"net"

	"github.com/labstack/echo/v4"
	"github.com/markbates/goth/gothic"
	"github.com/tphakala/birdnet-go/internal/security"
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

// CheckAccess validates if a request has access to protected resources
func (a *SecurityAdapter) CheckAccess(c echo.Context) bool {
	return a.OAuth2Server.IsUserAuthenticated(c)
}

// IsAuthRequired checks if authentication is required for this request
func (a *SecurityAdapter) IsAuthRequired(c echo.Context) bool {
	return a.OAuth2Server.IsAuthenticationEnabled(c.RealIP())
}

// GetUsername retrieves the username of the authenticated user (if available)
func (a *SecurityAdapter) GetUsername(c echo.Context) string {
	// Try to get username from session
	userId, err := gothic.GetFromSession("userId", c.Request())
	if err == nil && userId != "" {
		return userId
	}

	// Alternative: check basic auth client ID as username
	// This is a simplification; in a real system, we'd retrieve username from token claims
	if token, err := gothic.GetFromSession("access_token", c.Request()); err == nil && token != "" {
		if a.OAuth2Server.ValidateAccessToken(token) {
			return "api-client" // Placeholder for token-based username
		}
	}

	// No username found
	return ""
}

// GetAuthMethod returns the authentication method used
func (a *SecurityAdapter) GetAuthMethod(c echo.Context) string {
	// Check if authenticated by token
	if token, err := gothic.GetFromSession("access_token", c.Request()); err == nil && token != "" {
		if a.OAuth2Server.ValidateAccessToken(token) {
			return "token"
		}
	}

	// Check if authenticated by Google
	if googleUser, err := gothic.GetFromSession("google", c.Request()); err == nil && googleUser != "" {
		return "google"
	}

	// Check if authenticated by GitHub
	if githubUser, err := gothic.GetFromSession("github", c.Request()); err == nil && githubUser != "" {
		return "github"
	}

	// Check if authenticated by local subnet
	clientIP := c.RealIP()
	if clientIP != "" && security.IsInLocalSubnet(net.ParseIP(clientIP)) {
		return "local-subnet"
	}

	// Check if allowed by subnet configuration
	if a.OAuth2Server.IsRequestFromAllowedSubnet(c.RealIP()) {
		return "allowed-subnet"
	}

	// Default if method can't be determined but user is authenticated
	if a.OAuth2Server.IsUserAuthenticated(c) {
		return "session"
	}

	return "none"
}

// ValidateToken checks if a bearer token is valid
func (a *SecurityAdapter) ValidateToken(token string) bool {
	return a.OAuth2Server.ValidateAccessToken(token)
}

// AuthenticateBasic handles basic authentication with username/password
func (a *SecurityAdapter) AuthenticateBasic(c echo.Context, username, password string) bool {
	// For basic auth, we'll just check against configured password
	// This can be expanded as needed
	storedPassword := a.OAuth2Server.Settings.Security.BasicAuth.Password

	// Skip if basic auth is not enabled
	if !a.OAuth2Server.Settings.Security.BasicAuth.Enabled {
		if a.logger != nil {
			a.logger.Debug("Basic auth is not enabled")
		}
		return false
	}

	// Constant-time comparison to prevent timing attacks
	isValidPassword := subtle.ConstantTimeCompare([]byte(password), []byte(storedPassword)) == 1

	if isValidPassword {
		// Generate auth code and create session on successful authentication
		authCode, err := a.OAuth2Server.GenerateAuthCode()
		if err != nil {
			if a.logger != nil {
				a.logger.Error("Failed to generate auth code", "error", err.Error())
			}
			return false
		}

		// Store the auth code for callback
		if err := gothic.StoreInSession("auth_code", authCode, c.Request(), c.Response()); err != nil {
			if a.logger != nil {
				a.logger.Error("Failed to store auth code in session", "error", err.Error())
			}
			return false
		}

		return true
	}

	return false
}

// Logout invalidates the current session/token
func (a *SecurityAdapter) Logout(c echo.Context) error {
	// Clear all session values
	gothic.StoreInSession("userId", "", c.Request(), c.Response())       //nolint:errcheck
	gothic.StoreInSession("access_token", "", c.Request(), c.Response()) //nolint:errcheck
	gothic.StoreInSession("google", "", c.Request(), c.Response())       //nolint:errcheck
	gothic.StoreInSession("github", "", c.Request(), c.Response())       //nolint:errcheck

	// Log out from gothic session
	return gothic.Logout(c.Response(), c.Request())
}
