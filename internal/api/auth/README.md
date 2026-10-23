# Authentication Package (`internal/api/auth`)

This package provides the authentication middleware and the authentication service interface used by the API. It defines the `Service` interface, an adapter that implements it on top of `internal/security`, and an Echo middleware that enforces authentication on routes.

The login, callback, logout and status endpoints are not in this package. They live in `internal/api/v2/auth`, which calls the `Service` methods defined here.

## Overview

The package decouples authentication logic from the route handlers. Whether authentication is required for a request is decided per request by the security layer, so a request from an allowed subnet (subnet bypass) or an instance with no authentication provider enabled passes through without credentials.

## Key Components

1.  **`Service` interface (`service.go`)**:
    - The contract for the authentication service used by the API.
    - Request checks: `CheckAccess` (session), `IsAuthRequired`, `IsAuthenticated` (bypass, token or session), `ValidateToken`.
    - Identity: `GetUsername` and `GetAuthMethod`.
    - Login and logout: `AuthenticateBasic`, `ExchangeAuthCode`, `EstablishSession`, `Logout` and `GetProviderLogoutURL` (the provider's end-session URL for RP-Initiated Logout, empty when unsupported).
    - Sentinel errors: `ErrInvalidCredentials`, `ErrInvalidToken`, `ErrSessionNotFound`, `ErrLogoutFailed` and `ErrBasicAuthDisabled`.
    - `GetLogger` returns the logger for this package. It writes to the `security` log module, next to the OAuth and OIDC logging.

2.  **`AuthMethod` enum (`service.go`, `authmethod_string.go`)**:
    - An `int` enum: `AuthMethodUnknown`, `AuthMethodNone`, `AuthMethodBasicAuth`, `AuthMethodToken`, `AuthMethodOAuth2`, `AuthMethodBrowserSession`, `AuthMethodAPIKey` and `AuthMethodLocalSubnet`.
    - `String()` is generated with `stringer` through `go generate`. Run `go generate` in the package directory after adding or changing a value.
    - Only some values are ever set. The middleware sets `AuthMethodNone` (bypass) and `AuthMethodToken` itself, and for a session it records what `GetAuthMethod` returns, which is `AuthMethodBrowserSession` (or `AuthMethodLocalSubnet` when the request comes from an allowed subnet). `AuthMethodBasicAuth`, `AuthMethodOAuth2` and `AuthMethodAPIKey` are declared and can be parsed by `AuthMethodFromString`, but no code assigns them.

3.  **`SecurityAdapter` (`adapter.go`)**:
    - The `Service` implementation. It wraps a `*security.OAuth2Server`, which does the work: session checks, access token validation, subnet bypass and auth code exchange.
    - `GetUsername` prefers the value the middleware stored in the context and falls back to the `userId` in the session. `GetAuthMethod` prefers the context value, then checks subnet bypass, then the session.
    - `AuthMethodFromString` converts a string back to an `AuthMethod` and returns `AuthMethodUnknown` for anything it does not recognize.
    - The server creates the adapter in `initAuth` (`internal/api/server.go`) with `NewSecurityAdapter(oauth2Server)`.

4.  **`Middleware` (`middleware.go`)**:
    - Created with `NewMiddleware(service)`. Its `Authenticate` method is an Echo middleware (`func(echo.HandlerFunc) echo.HandlerFunc`) that the server applies to the routes that need authentication.
    - On success it stores `CtxKeyIsAuthenticated`, `CtxKeyAuthMethod` and `CtxKeyUsername` (`auth:isAuthenticated`, `auth:authMethod`, `auth:username`) in the Echo context.

## Authentication Flow

`Authenticate` handles a request in this order:

1.  **No service**: if the `Service` is nil, it logs an error and returns `500` with a JSON error.
2.  **Bypass**: if `IsAuthRequired` is false, it sets `isAuthenticated` to true and `authMethod` to `AuthMethodNone`, and calls the next handler.
3.  **Bearer token**: if the request has an `Authorization` header, it is handled here and never falls through to the session.
    - A header that is not `Bearer <token>` gets `401` with `Invalid Authorization header` and a `WWW-Authenticate: Bearer realm="api"` header.
    - A token that `ValidateToken` rejects gets `401` with `Invalid or expired token` and a `WWW-Authenticate` header carrying `error="invalid_token"`.
    - A valid token sets `isAuthenticated`, `authMethod` to `AuthMethodToken` and an empty `username` (tokens carry no user identity), then calls the next handler.
4.  **Session**: with no `Authorization` header, it calls `CheckAccess`. On success it sets `isAuthenticated`, and `authMethod` and `username` from `GetAuthMethod` and `GetUsername`, then calls the next handler.
5.  **Unauthenticated**: if the request has no usable credentials, it responds based on the `Accept` header.
    - If `Accept` contains `text/html`, it redirects (`302`) to `<basepath>/login?redirect=<path>`. The redirect target is the original path and query string, checked with `security.IsValidRedirect`. A path that is empty, starts with `/login` or fails the check becomes `/`. The base path comes from the `basePath` context value, then the `X-Ingress-Path` and `X-Forwarded-Prefix` headers, when they pass a safety check.
    - Otherwise it returns `401` with the JSON body `{"error": "Authentication required"}`.

After any response the middleware has written itself (the cases above that return an error status or a redirect), it does not call the next handler.

## Basic Authentication

- Handled by `SecurityAdapter.AuthenticateBasic`, which returns an auth code that the callback handler exchanges for an access token.
- It uses a single, fixed credential pair from the settings (`Security.BasicAuth.ClientID` and `Security.BasicAuth.Password`), read from the live settings on each call so changes take effect without a restart.
- If `Security.BasicAuth.Enabled` is false, it returns `ErrBasicAuthDisabled`.
- If `ClientID` is set, the username must match it. If `ClientID` is empty, the username check is skipped.
- Comparisons are constant time (SHA-256 digests compared with `subtle.ConstantTimeCompare`). A mismatch returns `ErrInvalidCredentials`.

## Usage

```go
authService := auth.NewSecurityAdapter(oauth2Server) // *security.OAuth2Server
authMiddleware := auth.NewMiddleware(authService)

apiGroup := e.Group("/api/v2")
apiGroup.Use(authMiddleware.Authenticate)

// Routes in this group now require authentication.
apiGroup.GET("/protected/resource", handlerFunc)
```

## Dependencies

- `github.com/labstack/echo/v4`: web framework.
- `github.com/markbates/goth` and `github.com/markbates/goth/gothic`: session storage and provider logout URLs.
- `github.com/tphakala/birdnet-go/internal/security`: the underlying authentication logic, redirect validation and session keys.
- `github.com/tphakala/birdnet-go/internal/logger` and `internal/errors`: logging and the sentinel errors.
- `golang.org/x/tools/cmd/stringer`: run through `go generate` for `AuthMethod`.
