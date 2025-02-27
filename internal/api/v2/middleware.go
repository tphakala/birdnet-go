// internal/api/v2/middleware.go
package api

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

// AuthMiddleware middleware function for API routes that require authentication
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
		authenticated := false

		// If authentication is enabled, check that it passes the requirements
		server := ctx.Get("server")
		if server != nil {
			// Try to use server's authentication methods
			if s, ok := server.(interface {
				IsAccessAllowed(c echo.Context) bool
				isAuthenticationEnabled(c echo.Context) bool
			}); ok {
				if !s.isAuthenticationEnabled(ctx) || s.IsAccessAllowed(ctx) {
					authenticated = true
				}
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
