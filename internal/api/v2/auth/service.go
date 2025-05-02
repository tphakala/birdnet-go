// internal/api/v2/auth/service.go
package auth

import "github.com/labstack/echo/v4"

// Service defines the authentication interface for API endpoints
type Service interface {
	// CheckAccess validates if a request has access to protected resources
	CheckAccess(c echo.Context) bool

	// IsAuthRequired checks if authentication is required for this request
	IsAuthRequired(c echo.Context) bool

	// GetUsername retrieves the username of the authenticated user (if available)
	GetUsername(c echo.Context) string

	// GetAuthMethod returns the authentication method used (token, session, subnet)
	GetAuthMethod(c echo.Context) string

	// ValidateToken checks if a bearer token is valid
	ValidateToken(token string) bool

	// AuthenticateBasic handles basic authentication with username/password
	AuthenticateBasic(c echo.Context, username, password string) bool

	// Logout invalidates the current session/token
	Logout(c echo.Context) error
}
