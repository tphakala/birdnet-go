// internal/api/auth/middleware.go
package auth

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/security"
)

// bearerTokenParts is the expected number of parts when splitting Authorization header.
const bearerTokenParts = 2

// Context keys for authentication values stored in echo.Context.
// Using named constants prevents typos and provides centralized documentation.
// These keys are prefixed with "auth:" to prevent collisions with other packages.
const (
	// CtxKeyIsAuthenticated indicates whether the request is authenticated.
	CtxKeyIsAuthenticated = "auth:isAuthenticated"
	// CtxKeyAuthMethod indicates the authentication method used.
	CtxKeyAuthMethod = "auth:authMethod"
	// CtxKeyUsername contains the authenticated user's username (if available).
	CtxKeyUsername = "auth:username"
)

// Middleware provides authentication middleware with the Service
type Middleware struct {
	AuthService Service
}

// NewMiddleware creates a new auth middleware
func NewMiddleware(service Service) *Middleware {
	return &Middleware{
		AuthService: service,
	}
}

// Authenticate is the main middleware function for authentication
func (m *Middleware) Authenticate(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := m.validateAuthService(c); err != nil {
			return err
		}

		if m.shouldBypassAuth(c) {
			return next(c)
		}

		// Try token auth first (from Authorization header)
		if result := m.tryTokenAuth(c); result.handled {
			if result.err != nil {
				return result.err
			}
			return next(c)
		}

		// Fall back to session-based authentication
		if m.trySessionAuth(c) {
			return next(c)
		}

		// Authentication failed, determine appropriate response
		return m.handleUnauthenticated(c)
	}
}

// authResult represents the result of an authentication attempt.
type authResult struct {
	handled bool  // Whether the auth attempt was processed (header present or valid session)
	err     error // Error to return to client (nil if successful)
}

// validateAuthService checks if AuthService is configured and returns an error response if not.
func (m *Middleware) validateAuthService(c echo.Context) error {
	if m.AuthService == nil {
		m.log().Error("Authentication middleware called with nil AuthService",
			logger.String("path", c.Request().URL.Path),
			logger.String("ip", c.RealIP()))
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Internal configuration error: authentication service not available",
		})
	}
	return nil
}

// shouldBypassAuth checks if authentication should be bypassed for this request.
// When bypassed (e.g., LAN subnet), the request is treated as effectively authenticated
// for data access purposes, matching the behavior of IsAuthenticated().
func (m *Middleware) shouldBypassAuth(c echo.Context) bool {
	if !m.AuthService.IsAuthRequired(c) {
		m.log().Debug("Authentication not required for this client",
			logger.String("ip", c.RealIP()),
			logger.String("path", c.Request().URL.Path))
		c.Set(CtxKeyIsAuthenticated, true) // Bypassed = effectively authenticated
		c.Set(CtxKeyAuthMethod, AuthMethodNone)
		return true
	}
	return false
}

// tryTokenAuth attempts to authenticate using a Bearer token from the Authorization header.
// Note: Token-based authentication does not currently provide username information.
// The AccessToken structure only contains the token and expiry, not user identity.
// This is a known limitation - downstream handlers should not rely on username
// being populated for token-authenticated requests.
func (m *Middleware) tryTokenAuth(c echo.Context) authResult {
	authHeader := c.Request().Header.Get("Authorization")
	if authHeader == "" {
		return authResult{handled: false}
	}

	path := c.Request().URL.Path
	ip := c.RealIP()
	log := m.log()
	log.Debug("Attempting token authentication",
		logger.String("path", path),
		logger.String("ip", ip))

	parts := strings.SplitN(authHeader, " ", bearerTokenParts)
	if len(parts) != bearerTokenParts || !strings.EqualFold(parts[0], "bearer") {
		return m.handleMalformedAuthHeader(c, path, ip)
	}

	token := strings.TrimSpace(parts[1])
	if err := m.AuthService.ValidateToken(token); err != nil {
		return m.handleInvalidToken(c, path, ip)
	}

	log.Debug("Token authentication successful",
		logger.String("path", path),
		logger.String("ip", ip))
	c.Set(CtxKeyIsAuthenticated, true)
	// Note: Username is not available for token auth as tokens don't store user identity.
	// The current AccessToken struct only contains token string and expiry.
	// TODO: Consider adding username to AccessToken struct to support this use case.
	c.Set(CtxKeyUsername, "")
	c.Set(CtxKeyAuthMethod, AuthMethodToken)
	return authResult{handled: true, err: nil}
}

// handleMalformedAuthHeader returns an error response for malformed Authorization headers.
func (m *Middleware) handleMalformedAuthHeader(c echo.Context, path, ip string) authResult {
	m.log().Warn("Malformed Authorization header",
		logger.String("path", path),
		logger.String("ip", ip))
	c.Response().Header().Set("WWW-Authenticate", `Bearer realm="api"`)
	return authResult{
		handled: true,
		err: c.JSON(http.StatusUnauthorized, map[string]string{
			"error": "Invalid Authorization header",
		}),
	}
}

// handleInvalidToken returns an error response for invalid tokens.
func (m *Middleware) handleInvalidToken(c echo.Context, path, ip string) authResult {
	m.log().Warn("Token validation failed",
		logger.String("path", path),
		logger.String("ip", ip))
	c.Response().Header().Set("WWW-Authenticate",
		`Bearer realm="api", error="invalid_token", error_description="Invalid or expired token"`)
	return authResult{
		handled: true,
		err: c.JSON(http.StatusUnauthorized, map[string]string{
			"error": "Invalid or expired token",
		}),
	}
}

// trySessionAuth attempts to authenticate using session-based authentication.
func (m *Middleware) trySessionAuth(c echo.Context) bool {
	path := c.Request().URL.Path
	ip := c.RealIP()
	log := m.log()
	log.Debug("Attempting session authentication",
		logger.String("path", path),
		logger.String("ip", ip))

	if err := m.AuthService.CheckAccess(c); err != nil {
		return false
	}

	log.Debug("Session authentication successful",
		logger.String("path", path),
		logger.String("ip", ip))
	c.Set(CtxKeyIsAuthenticated, true)
	c.Set(CtxKeyAuthMethod, m.AuthService.GetAuthMethod(c))
	c.Set(CtxKeyUsername, m.AuthService.GetUsername(c))
	return true
}

// log returns the auth package logger.
func (m *Middleware) log() logger.Logger {
	return GetLogger()
}

// handleUnauthenticated determines the appropriate response for unauthenticated requests
func (m *Middleware) handleUnauthenticated(c echo.Context) error {
	ip := c.RealIP()
	path := c.Request().URL.Path

	m.log().Info("Authentication required but not provided/valid",
		logger.String("path", path),
		logger.String("ip", ip))

	if m.isBrowserRequest(c) {
		return m.redirectToLogin(c, path, ip)
	}

	return m.returnAPIUnauthorized(c, path, ip)
}

// isBrowserRequest determines if the request is from a browser or an API client.
func (m *Middleware) isBrowserRequest(c echo.Context) bool {
	acceptHeader := c.Request().Header.Get("Accept")
	return strings.Contains(acceptHeader, "text/html")
}

// redirectToLogin handles browser requests by redirecting to the login page.
func (m *Middleware) redirectToLogin(c echo.Context, path, ip string) error {
	m.log().Info("Redirecting unauthenticated browser client to login page",
		logger.String("path", path),
		logger.String("ip", ip))

	finalLoginPath := m.buildLoginRedirectURL(c, ip)
	return c.Redirect(http.StatusFound, finalLoginPath)
}

// buildLoginRedirectURL constructs the login URL with a safe redirect parameter.
// Supports reverse proxy prefixes via X-Ingress-Path / X-Forwarded-Prefix headers.
func (m *Middleware) buildLoginRedirectURL(c echo.Context, ip string) string {
	const loginPath = "/login"

	basePath := requestBasePath(c)

	originURL := c.Request().URL
	originPath := originURL.Path
	originQuery := originURL.RawQuery

	safeRedirectPath := m.getSafeRedirectPath(originPath, originQuery, loginPath, ip)
	return basePath + loginPath + "?redirect=" + url.QueryEscape(safeRedirectPath)
}

// requestBasePath returns the reverse proxy base path from request headers.
// Priority: X-Ingress-Path > X-Forwarded-Prefix > empty.
func requestBasePath(c echo.Context) string {
	if p := c.Request().Header.Get("X-Ingress-Path"); p != "" {
		return strings.TrimRight(p, "/")
	}
	if p := c.Request().Header.Get("X-Forwarded-Prefix"); p != "" {
		return strings.TrimRight(p, "/")
	}
	return ""
}

// getSafeRedirectPath validates and returns a safe redirect path.
func (m *Middleware) getSafeRedirectPath(originPath, originQuery, loginPath, ip string) string {
	if originPath == "" || strings.HasPrefix(originPath, loginPath) {
		return "/"
	}

	if !security.IsValidRedirect(originPath) {
		m.log().Warn("Invalid redirect path detected during unauthenticated request, defaulting to '/'",
			logger.String("invalid_path", originPath),
			logger.String("ip", ip))
		return "/"
	}

	if originQuery != "" {
		return originPath + "?" + originQuery
	}
	return originPath
}

// returnAPIUnauthorized returns a JSON error response for API clients.
func (m *Middleware) returnAPIUnauthorized(c echo.Context, path, ip string) error {
	acceptHeader := c.Request().Header.Get("Accept")
	m.log().Info("Returning 401 Unauthorized for unauthenticated API client",
		logger.String("path", path),
		logger.String("ip", ip),
		logger.String("accept_header", acceptHeader))

	return c.JSON(http.StatusUnauthorized, map[string]string{
		"error": "Authentication required",
	})
}
