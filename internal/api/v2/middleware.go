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
		// Check if this is an API request with Authorization header (for Svelte UI)
		if authHeader := ctx.Request().Header.Get("Authorization"); authHeader != "" {
			// Extract and validate the token
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || parts[0] != "Bearer" {
				return ctx.JSON(http.StatusUnauthorized, map[string]string{
					"error": "Invalid Authorization header format. Use 'Bearer {token}'",
				})
			}

			token := parts[1]

			// Get server from context to access token validation
			server := ctx.Get("server")
			if server == nil {
				c.Debug("Server context not available for token validation")
				return ctx.JSON(http.StatusInternalServerError, map[string]string{
					"error": "Authentication service unavailable",
				})
			}

			// Try to validate the token using OAuth2Server
			if s, ok := server.(interface {
				ValidateAccessToken(token string) bool
			}); ok {
				if s.ValidateAccessToken(token) {
					return next(ctx)
				}
				return ctx.JSON(http.StatusUnauthorized, map[string]string{
					"error": "Invalid or expired token",
				})
			} else {
				c.Debug("Cannot validate token, server interface doesn't have ValidateAccessToken method")
				return ctx.JSON(http.StatusUnauthorized, map[string]string{
					"error": "Authentication service unavailable",
				})
			}
		}

		// For browser/web UI requests, check for authenticated session
		// When no Authorization header is present, we fall back to session-based authentication
		// which is typically handled through cookies set during login
		authenticated := false

		// Get server from context to check authentication status
		server := ctx.Get("server")
		if server != nil {
			// Try to use server's authentication methods
			if s, ok := server.(interface {
				IsAccessAllowed(c echo.Context) bool
				isAuthenticationEnabled(c echo.Context) bool
			}); ok {
				// Two distinct checks:
				// 1. If authentication is globally disabled across the application, allow access
				// 2. If authentication is enabled, check if this specific session has valid credentials
				if !s.isAuthenticationEnabled(ctx) {
					// Authentication is disabled globally, so all requests are allowed
					authenticated = true
				} else if s.IsAccessAllowed(ctx) {
					// Authentication is enabled, and this session has valid credentials
					authenticated = true
				}
				// Otherwise, authentication is required but not provided
			}
		}

		if !authenticated {
			// Return JSON error for API calls
			return ctx.JSON(http.StatusUnauthorized, map[string]string{
				"error": "Authentication required",
			})
		}

		return next(ctx)
	}
}
