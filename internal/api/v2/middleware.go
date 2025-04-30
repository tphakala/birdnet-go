// internal/api/v2/middleware.go
package api

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

// CombinedAuthMiddleware middleware function that supports both bearer token
// authentication (for API clients) and session-based authentication (for web UI)
// This provides a unified authentication layer for all types of requests.
func (c *Controller) AuthMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) error {
		ip := ctx.RealIP()
		path := ctx.Request().URL.Path
		if c.apiLogger != nil {
			c.apiLogger.Debug("AuthMiddleware executing", "path", path, "ip", ip)
		}

		// Check for Bearer token first
		if authHeader := ctx.Request().Header.Get("Authorization"); authHeader != "" {
			if c.apiLogger != nil {
				c.apiLogger.Debug("Attempting Bearer token authentication", "path", path, "ip", ip)
			}
			return c.handleBearerAuth(ctx, next, authHeader)
		}

		// Fallback to session authentication
		if c.apiLogger != nil {
			c.apiLogger.Debug("Attempting Session authentication (no Bearer token found)", "path", path, "ip", ip)
		}
		return c.handleSessionAuth(ctx, next)
	}
}

// handleBearerAuth handles authentication for requests with a Bearer token.
func (c *Controller) handleBearerAuth(ctx echo.Context, next echo.HandlerFunc, authHeader string) error {
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path

	// Extract and validate the token format
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" {
		if c.apiLogger != nil {
			c.apiLogger.Warn("Invalid Authorization header format", "path", path, "ip", ip)
		}
		return ctx.JSON(http.StatusUnauthorized, map[string]string{
			"error": "Invalid Authorization header format. Use 'Bearer {token}'",
		})
	}
	token := parts[1] // Do not log the token itself

	// Get server from context to access token validation
	server := ctx.Get("server")
	if server == nil {
		c.Debug("Server context not available for token validation") // Keep existing debug
		if c.apiLogger != nil {
			c.apiLogger.Error("Authentication service unavailable (server context nil) during token validation", "path", path, "ip", ip)
		}
		return ctx.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Authentication service unavailable",
		})
	}

	// Try to validate the token using OAuth2Server interface
	validator, ok := server.(interface {
		ValidateAccessToken(token string) bool
	})
	if !ok {
		c.Debug("Cannot validate token, server interface doesn't have ValidateAccessToken method") // Keep existing debug
		if c.apiLogger != nil {
			c.apiLogger.Error("Authentication service unavailable (missing ValidateAccessToken method) during token validation", "path", path, "ip", ip)
		}
		return ctx.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Authentication service unavailable",
		})
	}

	if validator.ValidateAccessToken(token) {
		if c.apiLogger != nil {
			c.apiLogger.Debug("Bearer token validation successful", "path", path, "ip", ip)
		}
		return next(ctx) // Token valid, proceed
	}

	// Token validation failed
	if c.apiLogger != nil {
		c.apiLogger.Warn("Bearer token validation failed (invalid/expired token)", "path", path, "ip", ip)
	}
	return ctx.JSON(http.StatusUnauthorized, map[string]string{
		"error": "Invalid or expired token",
	})
}

// handleSessionAuth handles authentication for requests using sessions (typically browsers).
func (c *Controller) handleSessionAuth(ctx echo.Context, next echo.HandlerFunc) error {
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path
	authenticated := false
	authEnabled := true // Assume auth is enabled unless told otherwise

	// Get server from context to check authentication status
	server := ctx.Get("server")
	if server == nil {
		if c.apiLogger != nil {
			c.apiLogger.Warn("Session check failed: Server context is nil", "path", path, "ip", ip)
		}
		// Proceed cautiously, assuming auth is required but we cannot verify; let the failure path handle it.
		return c.handleUnauthenticated(ctx, path, ip, authEnabled)
	}

	// Try to use server's authentication methods
	authChecker, ok := server.(interface {
		IsAccessAllowed(c echo.Context) bool
		isAuthenticationEnabled(c echo.Context) bool
	})
	if !ok {
		if c.apiLogger != nil {
			c.apiLogger.Warn("Session check failed: Server does not implement required auth interfaces", "path", path, "ip", ip)
		}
		// Proceed cautiously, assuming auth is required but we cannot verify; let the failure path handle it.
		return c.handleUnauthenticated(ctx, path, ip, authEnabled)
	}

	// Check if authentication is enabled globally
	authEnabled = authChecker.isAuthenticationEnabled(ctx)
	if !authEnabled {
		authenticated = true // Auth disabled, allow access
		if c.apiLogger != nil {
			c.apiLogger.Debug("Session check skipped: Authentication disabled globally", "path", path, "ip", ip)
		}
	} else if authChecker.IsAccessAllowed(ctx) {
		authenticated = true // Auth enabled, and session is valid
		if c.apiLogger != nil {
			c.apiLogger.Debug("Session check successful: Access allowed", "path", path, "ip", ip)
		}
	}

	if !authenticated {
		return c.handleUnauthenticated(ctx, path, ip, authEnabled)
	}

	// Authentication successful
	if c.apiLogger != nil {
		c.apiLogger.Debug("AuthMiddleware passed (Session)", "path", path, "ip", ip)
	}
	return next(ctx)
}

// handleUnauthenticated determines the appropriate response for unauthenticated requests.
func (c *Controller) handleUnauthenticated(ctx echo.Context, path, ip string, authEnabled bool) error {
	if c.apiLogger != nil {
		c.apiLogger.Info("Authentication required but not provided/valid",
			"path", path,
			"ip", ip,
			"auth_enabled", authEnabled, // Log if auth was considered enabled during check
		)
	}

	// Determine if request is from a browser or an API client
	acceptHeader := ctx.Request().Header.Get("Accept")
	isBrowserRequest := strings.Contains(acceptHeader, "text/html")

	if isBrowserRequest {
		if c.apiLogger != nil {
			c.apiLogger.Info("Redirecting unauthenticated browser client to login page", "path", path, "ip", ip, "accept_header", acceptHeader)
		}
		// For browser requests, redirect to login page
		loginPath := "/login"

		// Optionally store the original URL for post-login redirect
		originURL := ctx.Request().URL.String()
		// Avoid redirect loops to login page itself
		if !strings.HasPrefix(path, loginPath) {
			loginPath += "?redirect=" + originURL
		}

		return ctx.Redirect(http.StatusFound, loginPath)
	}

	// For API clients, return JSON error response
	if c.apiLogger != nil {
		c.apiLogger.Info("Returning 401 Unauthorized for unauthenticated API client", "path", path, "ip", ip, "accept_header", acceptHeader)
	}
	return ctx.JSON(http.StatusUnauthorized, map[string]string{
		"error": "Authentication required",
	})
}
