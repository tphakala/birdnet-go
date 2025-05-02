// internal/api/v2/auth/service.go
package auth

import (
	"errors"

	"github.com/labstack/echo/v4"
)

// Sentinel errors for authentication failures.
var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidToken       = errors.New("invalid or expired token")
	ErrSessionNotFound    = errors.New("session not found or expired")
	ErrAuthNotRequired    = errors.New("authentication not required") // Indicate auth bypassed
	ErrLogoutFailed       = errors.New("logout operation failed")
)

// AuthMethod represents the method used for authentication.
type AuthMethod uint8

// Constants defining the possible authentication methods.
const (
	AuthMethodUnknown AuthMethod = iota // 0
	AuthMethodToken                     // 1
	AuthMethodSession                   // 2
	AuthMethodSubnet                    // 3
	AuthMethodNone                      // 4 - Explicitly indicate no auth applied (e.g., bypassed)
)

// String returns the string representation of the AuthMethod.
func (m AuthMethod) String() string {
	switch m {
	case AuthMethodToken:
		return "token"
	case AuthMethodSession:
		return "session"
	case AuthMethodSubnet:
		return "subnet"
	case AuthMethodNone:
		return "none"
	// AuthMethodUnknown is handled by the default case
	default:
		return "unknown"
	}
}

// Service defines the authentication interface for API endpoints
type Service interface {
	// CheckAccess validates if a request has access to protected resources.
	// Returns nil on success, or an error (e.g., ErrSessionNotFound) on failure.
	CheckAccess(c echo.Context) error

	// IsAuthRequired checks if authentication is required for this request
	IsAuthRequired(c echo.Context) bool

	// GetUsername retrieves the username of the authenticated user (if available)
	GetUsername(c echo.Context) string

	// GetAuthMethod returns the authentication method used as a defined constant.
	GetAuthMethod(c echo.Context) AuthMethod

	// ValidateToken checks if a bearer token is valid.
	// Returns nil on success, or ErrInvalidToken on failure.
	ValidateToken(token string) error

	// AuthenticateBasic handles basic authentication with username/password.
	// Returns nil on success, or ErrInvalidCredentials on failure.
	AuthenticateBasic(c echo.Context, username, password string) error

	// Logout invalidates the current session/token.
	// Returns nil on success, or ErrLogoutFailed on failure.
	Logout(c echo.Context) error
}
