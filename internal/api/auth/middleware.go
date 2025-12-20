// internal/api/auth/middleware.go
package auth

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/security"
)

// bearerTokenParts is the expected number of parts when splitting Authorization header.
const bearerTokenParts = 2

// Middleware provides authentication middleware with the Service
type Middleware struct {
	AuthService Service
	logger      *slog.Logger
}

// NewMiddleware creates a new auth middleware
func NewMiddleware(service Service, logger *slog.Logger) *Middleware {
	return &Middleware{
		AuthService: service,
		logger:      logger,
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
		m.logError("Authentication middleware called with nil AuthService",
			"path", c.Request().URL.Path, "ip", c.RealIP())
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Internal configuration error: authentication service not available",
		})
	}
	return nil
}

// shouldBypassAuth checks if authentication should be bypassed for this request.
func (m *Middleware) shouldBypassAuth(c echo.Context) bool {
	if !m.AuthService.IsAuthRequired(c) {
		m.logDebug("Authentication not required for this client",
			"ip", c.RealIP(), "path", c.Request().URL.Path)
		c.Set("isAuthenticated", false)
		c.Set("authMethod", AuthMethodNone)
		return true
	}
	return false
}

// tryTokenAuth attempts to authenticate using a Bearer token from the Authorization header.
func (m *Middleware) tryTokenAuth(c echo.Context) authResult {
	authHeader := c.Request().Header.Get("Authorization")
	if authHeader == "" {
		return authResult{handled: false}
	}

	path := c.Request().URL.Path
	ip := c.RealIP()
	m.logDebug("Attempting token authentication", "path", path, "ip", ip)

	parts := strings.SplitN(authHeader, " ", bearerTokenParts)
	if len(parts) != bearerTokenParts || !strings.EqualFold(parts[0], "bearer") {
		return m.handleMalformedAuthHeader(c, path, ip)
	}

	token := strings.TrimSpace(parts[1])
	if err := m.AuthService.ValidateToken(token); err != nil {
		return m.handleInvalidToken(c, path, ip)
	}

	m.logDebug("Token authentication successful", "path", path, "ip", ip)
	c.Set("isAuthenticated", true)
	c.Set("username", m.AuthService.GetUsername(c))
	c.Set("authMethod", AuthMethodToken)
	return authResult{handled: true, err: nil}
}

// handleMalformedAuthHeader returns an error response for malformed Authorization headers.
func (m *Middleware) handleMalformedAuthHeader(c echo.Context, path, ip string) authResult {
	m.logWarn("Malformed Authorization header", "path", path, "ip", ip)
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
	m.logWarn("Token validation failed", "path", path, "ip", ip)
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
	m.logDebug("Attempting session authentication", "path", path, "ip", ip)

	if err := m.AuthService.CheckAccess(c); err != nil {
		return false
	}

	m.logDebug("Session authentication successful", "path", path, "ip", ip)
	c.Set("isAuthenticated", true)
	c.Set("authMethod", m.AuthService.GetAuthMethod(c))
	c.Set("username", m.AuthService.GetUsername(c))
	return true
}

// logDebug logs a debug message if logger is available.
func (m *Middleware) logDebug(msg string, args ...any) {
	if m.logger != nil {
		m.logger.Debug(msg, args...)
	}
}

// logWarn logs a warning message if logger is available.
func (m *Middleware) logWarn(msg string, args ...any) {
	if m.logger != nil {
		m.logger.Warn(msg, args...)
	}
}

// logError logs an error message if logger is available.
func (m *Middleware) logError(msg string, args ...any) {
	if m.logger != nil {
		m.logger.Error(msg, args...)
	}
}

// logInfo logs an info message if logger is available.
func (m *Middleware) logInfo(msg string, args ...any) {
	if m.logger != nil {
		m.logger.Info(msg, args...)
	}
}

// handleUnauthenticated determines the appropriate response for unauthenticated requests
func (m *Middleware) handleUnauthenticated(c echo.Context) error {
	ip := c.RealIP()
	path := c.Request().URL.Path

	m.logInfo("Authentication required but not provided/valid", "path", path, "ip", ip)

	if m.isBrowserRequest(c) {
		return m.redirectToLogin(c, path, ip)
	}

	return m.returnAPIUnauthorized(c, path, ip)
}

// isBrowserRequest determines if the request is from a browser or an API client.
func (m *Middleware) isBrowserRequest(c echo.Context) bool {
	acceptHeader := c.Request().Header.Get("Accept")
	isHXRequest := c.Request().Header.Get("HX-Request") == "true"
	return strings.Contains(acceptHeader, "text/html") || isHXRequest
}

// redirectToLogin handles browser requests by redirecting to the login page.
func (m *Middleware) redirectToLogin(c echo.Context, path, ip string) error {
	acceptHeader := c.Request().Header.Get("Accept")
	isHXRequest := c.Request().Header.Get("HX-Request") == "true"

	m.logInfo("Redirecting unauthenticated browser client to login page",
		"path", path, "ip", ip, "accept_header", acceptHeader, "is_htmx", isHXRequest)

	finalLoginPath := m.buildLoginRedirectURL(c, ip)

	if isHXRequest {
		c.Response().Header().Set("HX-Redirect", finalLoginPath)
		return c.String(http.StatusUnauthorized, "")
	}

	return c.Redirect(http.StatusFound, finalLoginPath)
}

// buildLoginRedirectURL constructs the login URL with a safe redirect parameter.
func (m *Middleware) buildLoginRedirectURL(c echo.Context, ip string) string {
	const loginPath = "/login"

	originURL := c.Request().URL
	originPath := originURL.Path
	originQuery := originURL.RawQuery

	safeRedirectPath := m.getSafeRedirectPath(originPath, originQuery, loginPath, ip)
	return loginPath + "?redirect=" + url.QueryEscape(safeRedirectPath)
}

// getSafeRedirectPath validates and returns a safe redirect path.
func (m *Middleware) getSafeRedirectPath(originPath, originQuery, loginPath, ip string) string {
	if originPath == "" || strings.HasPrefix(originPath, loginPath) {
		return "/"
	}

	if !security.IsValidRedirect(originPath) {
		m.logWarn("Invalid redirect path detected during unauthenticated request, defaulting to '/'",
			"invalid_path", originPath, "ip", ip)
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
	m.logInfo("Returning 401 Unauthorized for unauthenticated API client",
		"path", path, "ip", ip, "accept_header", acceptHeader)

	return c.JSON(http.StatusUnauthorized, map[string]string{
		"error": "Authentication required",
	})
}
