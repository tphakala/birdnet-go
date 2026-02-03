// internal/api/auth/service.go
package auth

import (
	"context"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// GetLogger returns the auth package logger.
func GetLogger() logger.Logger {
	return logger.Global().Module("auth")
}

// Sentinel errors for authentication failures.
var (
	ErrInvalidCredentials = errors.NewStd("invalid credentials")
	ErrInvalidToken       = errors.NewStd("invalid or expired token")
	ErrSessionNotFound    = errors.NewStd("session not found or expired")
	ErrLogoutFailed       = errors.NewStd("logout operation failed")
	ErrBasicAuthDisabled  = errors.NewStd("basic authentication is disabled")
	ErrAuthCodeGeneration = errors.NewStd("failed to generate authorization code")
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
	AuthMethodLocalSubnet    // Added for local subnet bypass authentication
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
	// Returns the auth code on success, or error on failure.
	AuthenticateBasic(c echo.Context, username, password string) (string, error)

	// Logout invalidates the current session/token.
	// Returns nil on success, or ErrLogoutFailed on failure.
	Logout(c echo.Context) error

	// ExchangeAuthCode exchanges an authorization code for an access token.
	// Returns the access token on success, or error on failure.
	ExchangeAuthCode(ctx context.Context, code string) (string, error)

	// EstablishSession creates a new session with the given access token.
	// Handles session fixation mitigation by clearing old session first.
	EstablishSession(c echo.Context, accessToken string) error

	// IsAuthenticated checks if a request is authenticated via any supported method.
	// Returns true if auth is bypassed (not required) or if token/session auth succeeds.
	IsAuthenticated(c echo.Context) bool
}
