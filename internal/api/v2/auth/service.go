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
	ErrLogoutFailed       = errors.New("logout operation failed")
)

//go:generate go run golang.org/x/tools/cmd/stringer -type=AuthMethod

// AuthMethod represents the type of authentication used
type AuthMethod int

//go:generate stringer -type=AuthMethod -trimprefix=AuthMethod
const (
	AuthMethodUnknown AuthMethod = iota
	AuthMethodNone               // Added to explicitly indicate no auth was applied (bypass)
	AuthMethodBasicAuth
	AuthMethodToken
	AuthMethodOAuth2
	AuthMethodBrowserSession // Added for explicit browser session identification
	AuthMethodAPIKey         // Added for API key authentication
	// NOTE: Remember to run `go generate` in this directory after adding new methods.
)

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
