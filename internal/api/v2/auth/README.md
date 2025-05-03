# Authentication Package (`internal/api/v2/auth`)

This package handles authentication and authorization for the API v2 endpoints. It defines a common interface for authentication services, provides an adapter for the underlying security implementation, and includes middleware for enforcing authentication checks.

## Overview

The package aims to decouple authentication logic from the specific API route handlers. It supports various authentication methods and allows for configuration-based enabling/disabling of authentication, including bypass for specific IP subnets.

## Key Components

1.  **`Service` Interface (`service.go`)**:
    *   Defines the contract for any authentication service used by the API.
    *   Methods include checking access (`CheckAccess`), determining if auth is required (`IsAuthRequired`), retrieving username (`GetUsername`), getting the auth method (`GetAuthMethod`), validating tokens (`ValidateToken`), handling basic auth (`AuthenticateBasic`), and logging out (`Logout`).
    *   Defines sentinel errors (`ErrInvalidCredentials`, `ErrInvalidToken`, `ErrSessionNotFound`, `ErrLogoutFailed`, `ErrBasicAuthDisabled`) for common authentication failure scenarios.

2.  **`AuthMethod` Enum (`service.go`, `authmethod_string.go`)**:
    *   An `int`-based enum representing the different authentication methods detected or used (e.g., `AuthMethodToken`, `AuthMethodBrowserSession`, `AuthMethodBasicAuth`, `AuthMethodLocalSubnet`, `AuthMethodNone`).
    *   Uses `go generate` with `stringer` to automatically create the `String()` method for readable representations. Remember to run `go generate ./...` in the package directory after adding or modifying `AuthMethod` values.

3.  **`SecurityAdapter` (`adapter.go`)**:
    *   An implementation of the `Service` interface.
    *   Adapts the functionality of the `internal/security` package, specifically using `security.OAuth2Server` for core authentication logic (session checks, token validation, subnet bypass, basic auth credential verification).
    *   Provides logic to retrieve username and determine the authentication method, often relying on context values set by the middleware.
    *   Includes `AuthMethodFromString` helper to convert string representations back to `AuthMethod` constants.

4.  **`Middleware` (`middleware.go`)**:
    *   An Echo middleware struct that utilizes an instance of the `Service` interface.
    *   The `Authenticate` method wraps API handlers to enforce authentication.
    *   Checks if authentication is required based on the client IP (`IsAuthRequired`).
    *   Attempts authentication in the following order:
        1.  Bearer Token (`Authorization: Bearer <token>`) via `ValidateToken`.
        2.  Session-based authentication via `CheckAccess`.
    *   Sets context values (`isAuthenticated`, `username`, `authMethod`) upon successful authentication.
    *   Handles unauthenticated requests:
        *   Redirects browser clients (HTML `Accept` header or `HX-Request` header) to `/login` with a `redirect` query parameter. Handles HTMX redirects appropriately (`HX-Redirect` header).
        *   Returns a `401 Unauthorized` JSON response for API clients.

## Authentication Flow

1.  The `Middleware` intercepts an incoming request.
2.  It checks if auth is required using `AuthService.IsAuthRequired`. If not (e.g., local subnet bypass), it sets `authMethod` to `AuthMethodNone` and proceeds.
3.  If auth is required, it looks for a `Bearer` token in the `Authorization` header. If found, it validates it using `AuthService.ValidateToken`. On success, it sets context (`isAuthenticated=true`, `authMethod=AuthMethodToken`, `username`) and proceeds.
4.  If no valid token is found, it checks for an existing session using `AuthService.CheckAccess`. On success, it sets context (`isAuthenticated=true`, `authMethod` via `GetAuthMethod`, `username`) and proceeds.
5.  If neither token nor session authentication succeeds, the `handleUnauthenticated` function is called to either redirect the client (browsers) or return a 401 error (API clients).

## Basic Authentication

*   Handled by `SecurityAdapter.AuthenticateBasic`.
*   Relies on a *single*, fixed username/password combination configured in settings (`Security.BasicAuth.ClientID` and `Security.BasicAuth.Password`).
*   The provided username must match the configured `ClientID`.
*   Uses constant-time comparison for security.
*   If basic auth is disabled in the configuration, it returns `ErrBasicAuthDisabled`.
*   On successful basic auth, it stores the username (`userId`) in the session.

## Usage

1.  Create an instance of the `SecurityAdapter` (or another `Service` implementation), providing the necessary dependencies (like `security.OAuth2Server` and a `*slog.Logger`).
2.  Create an instance of the `Middleware`, passing the `Service` instance and a logger.
3.  Apply the `Middleware.Authenticate` function to the Echo routes or groups that require authentication.

```go
// Example (simplified setup)
logger := slog.Default() // Or your configured logger
oauthServer := security.NewOAuth2Server(...) // Initialize your security server
authService := auth.NewSecurityAdapter(oauthServer, logger)
authMiddleware := auth.NewMiddleware(authService, logger)

apiGroup := e.Group("/api/v2")
apiGroup.Use(authMiddleware.Authenticate) // Apply middleware

// Routes within this group are now protected
apiGroup.GET("/protected/resource", handlerFunc)
```

## Dependencies

*   `github.com/labstack/echo/v4`: Web framework.
*   `github.com/markbates/goth/gothic`: Session management, particularly for OAuth and storing user IDs post-login.
*   `github.com/tphakala/birdnet-go/internal/security`: Provides the underlying authentication logic adapted by `SecurityAdapter`.
*   `log/slog`: Structured logging.
*   `golang.org/x/tools/cmd/stringer`: Used via `go generate` for `AuthMethod`. 