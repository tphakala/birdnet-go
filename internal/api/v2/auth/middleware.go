// internal/api/v2/auth/middleware.go
package auth

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/security"
)

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
		// Add guard against nil AuthService
		if m.AuthService == nil {
			if m.logger != nil {
				m.logger.Error("Authentication middleware called with nil AuthService",
					"path", c.Request().URL.Path,
					"ip", c.RealIP(),
				)
			}
			// Return an internal server error as this is a configuration issue
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "Internal configuration error: authentication service not available",
			})
		}

		ip := c.RealIP()
		path := c.Request().URL.Path

		// Log middleware execution if logger available
		if m.logger != nil {
			m.logger.Debug("Auth middleware executing", "path", path, "ip", ip)
		}

		// Skip auth check if auth is not required for this client IP
		if !m.AuthService.IsAuthRequired(c) {
			if m.logger != nil {
				m.logger.Debug("Authentication not required for this client", "ip", ip, "path", path)
			}
			// Set context to indicate bypass
			c.Set("isAuthenticated", false)
			c.Set("authMethod", AuthMethodUnknown)
			return next(c)
		}

		// Try token auth first (from Authorization header)
		if authHeader := c.Request().Header.Get("Authorization"); authHeader != "" {
			if m.logger != nil {
				m.logger.Debug("Attempting token authentication", "path", path, "ip", ip)
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
				token := strings.TrimSpace(parts[1]) // Trim whitespace from token

				// Validate the token, check if the returned error is nil
				if err := m.AuthService.ValidateToken(token); err == nil {
					// Token is valid
					if m.logger != nil {
						m.logger.Debug("Token authentication successful", "path", path, "ip", ip)
					}
					// Set context values on successful authentication
					c.Set("isAuthenticated", true)
					c.Set("username", m.AuthService.GetUsername(c))
					c.Set("authMethod", AuthMethodToken)
					return next(c)
				}

				// Token validation failed (err != nil)
				if m.logger != nil {
					m.logger.Warn("Token validation failed", "path", path, "ip", ip)
				}
				// Add WWW-Authenticate header for RFC 6750 compliance
				c.Response().Header().Set("WWW-Authenticate",
					`Bearer realm="api", error="invalid_token", error_description="Invalid or expired token"`)
				return c.JSON(http.StatusUnauthorized, map[string]string{
					"error": "Invalid or expired token",
				})
			}

			// Malformed Authorization header
			if m.logger != nil {
				m.logger.Warn("Malformed Authorization header", "path", path, "ip", ip)
			}
			// Add WWW-Authenticate header as per RFC 6750
			c.Response().Header().Set("WWW-Authenticate", `Bearer realm="api"`)
			return c.JSON(http.StatusUnauthorized, map[string]string{
				"error": "Invalid Authorization header", // Generic message for client
			})
		}

		// Fall back to session-based authentication
		if m.logger != nil {
			m.logger.Debug("Attempting session authentication", "path", path, "ip", ip)
		}

		// Check session access, check if the returned error is nil
		if err := m.AuthService.CheckAccess(c); err == nil {
			// Session is valid
			if m.logger != nil {
				m.logger.Debug("Session authentication successful", "path", path, "ip", ip)
			}
			// Set context values on successful authentication
			c.Set("isAuthenticated", true)
			c.Set("authMethod", m.AuthService.GetAuthMethod(c))
			c.Set("username", m.AuthService.GetUsername(c))
			return next(c)
		}

		// Authentication failed, determine appropriate response
		return m.handleUnauthenticated(c)
	}
}

// handleUnauthenticated determines the appropriate response for unauthenticated requests
func (m *Middleware) handleUnauthenticated(c echo.Context) error {
	ip := c.RealIP()
	path := c.Request().URL.Path

	if m.logger != nil {
		m.logger.Info("Authentication required but not provided/valid",
			"path", path,
			"ip", ip,
		)
	}

	// Determine if request is from a browser or an API client
	acceptHeader := c.Request().Header.Get("Accept")
	isHXRequest := c.Request().Header.Get("HX-Request") == "true"
	isBrowserRequest := strings.Contains(acceptHeader, "text/html") || isHXRequest

	if isBrowserRequest {
		if m.logger != nil {
			m.logger.Info("Redirecting unauthenticated browser client to login page",
				"path", path,
				"ip", ip,
				"accept_header", acceptHeader,
				"is_htmx", isHXRequest,
			)
		}

		// For browser requests, redirect to login page
		loginPath := "/login"

		// Optionally store the original URL for post-login redirect
		originURL := c.Request().URL      // Get the URL object
		originPath := originURL.Path      // Get just the path
		originQuery := originURL.RawQuery // Get the raw query

		// Validate the origin path to prevent open redirects
		// Use the existing validation logic found in internal/httpcontroller/auth_routes.go
		// Default to root if path is invalid or points to login page itself
		safeRedirectPath := "/"
		if originPath != "" && !strings.HasPrefix(originPath, loginPath) && security.IsValidRedirect(originPath) {
			safeRedirectPath = originPath
			if originQuery != "" {
				safeRedirectPath += "?" + originQuery // Preserve original query if path is safe
			}
		} else if !security.IsValidRedirect(originPath) {
			// Log if the path was invalid but not the login page
			if m.logger != nil && !strings.HasPrefix(originPath, loginPath) {
				m.logger.Warn("Invalid redirect path detected during unauthenticated request, defaulting to '/'", "invalid_path", originPath, "ip", ip)
			}
		}

		// Construct the final login path with the validated redirect parameter
		finalLoginPath := loginPath + "?redirect=" + url.QueryEscape(safeRedirectPath)

		// Special handling for HTMX requests
		if isHXRequest {
			c.Response().Header().Set("HX-Redirect", finalLoginPath)
			return c.String(http.StatusUnauthorized, "")
		}

		return c.Redirect(http.StatusFound, finalLoginPath)
	}

	// For API clients, return JSON error response
	if m.logger != nil {
		m.logger.Info("Returning 401 Unauthorized for unauthenticated API client",
			"path", path,
			"ip", ip,
			"accept_header", acceptHeader,
		)
	}

	return c.JSON(http.StatusUnauthorized, map[string]string{
		"error": "Authentication required",
	})
}

/*
// isValidRedirect ensures the redirect path is safe and internal
// This helper function needs to be accessible here.
// Assuming it's moved to a shared location or duplicated if necessary.
// For now, we assume it's available in scope.
// If not, it would need to be imported or defined.
// func isValidRedirect(redirectPath string) bool { ... }
*/
