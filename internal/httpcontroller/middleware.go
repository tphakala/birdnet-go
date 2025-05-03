package httpcontroller

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"net"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/security"
)

// CSRFContextKey is the key used to store CSRF token in the context
const CSRFContextKey = "birdnet-go-csrf"

// Defines the V2 API path prefixes that are publicly accessible without authentication.
// Used as a single source of truth for route classification.
var publicV2ApiPrefixes = map[string]struct{}{
	"/api/v2/detections":          {},
	"/api/v2/analytics":           {},
	"/api/v2/media/species-image": {},
	"/api/v2/media/audio":         {},
	"/api/v2/spectrogram":         {},
	"/api/v2/audio":               {},
	"/api/v2/health":              {}, // Health check should always be public
}

// configureMiddleware sets up middleware for the server.
func (s *Server) configureMiddleware() {
	s.Echo.Use(middleware.Recover())

	// Add structured logging middleware if available
	if s.webLogger != nil {
		s.Echo.Use(s.LoggingMiddleware())
	}

	s.Echo.Use(s.CSRFMiddleware())
	s.Echo.Use(s.AuthMiddleware)
	s.Echo.Use(s.GzipMiddleware())
	s.Echo.Use(s.CacheControlMiddleware())
	s.Echo.Use(s.VaryHeaderMiddleware())
}

// CSRFMiddleware configures CSRF protection for the server
func (s *Server) CSRFMiddleware() echo.MiddlewareFunc {
	config := middleware.CSRFConfig{
		TokenLookup:    "header:X-CSRF-Token,form:_csrf",
		CookieName:     "csrf",
		CookiePath:     "/",
		CookieHTTPOnly: true,
		CookieSameSite: http.SameSiteLaxMode,
		CookieSecure:   false, // Allow cookies over HTTP, if user is not
		// using HTTPS they don't care about security anyway
		CookieMaxAge: 1800, // 30 minutes token lifetime
		TokenLength:  32,
		ContextKey:   CSRFContextKey,
		Skipper: func(c echo.Context) bool {
			path := c.Path()
			// Skip CSRF for static assets and auth endpoints only
			return strings.HasPrefix(path, "/assets/") ||
				strings.HasPrefix(path, "/api/v1/media/") ||
				strings.HasPrefix(path, "/api/v1/sse") ||
				strings.HasPrefix(path, "/api/v1/audio-level") ||
				strings.HasPrefix(path, "/api/v1/audio-stream-hls") || // Skip CSRF for HLS streaming
				strings.HasPrefix(path, "/api/v1/auth/") ||
				strings.HasPrefix(path, "/api/v1/oauth2/token") ||
				path == "/api/v1/oauth2/callback"
		},
		ErrorHandler: func(err error, c echo.Context) error {
			// Keep the original debug logging for backward compatibility
			s.Debug("🚨 CSRF ERROR: Rejected request")
			s.Debug("🔍 Request Method: %s, Path: %s", c.Request().Method, c.Request().URL.Path)
			s.Debug("📌 CSRF Token in Header: %s", c.Request().Header.Get("X-CSRF-Token"))
			s.Debug("📌 CSRF Token in Form: %s", c.FormValue("_csrf"))
			s.Debug("📝 All Cookies: %s", c.Request().Header.Get("Cookie"))
			s.Debug("💡 Error Details: %v", err)

			// Add enhanced structured logging
			if s.webLogger != nil {
				s.LogError(c, err, "CSRF validation failed")
			}

			return echo.NewHTTPError(http.StatusForbidden, "Invalid CSRF token")
		},
	}

	return middleware.CSRFWithConfig(config)
}

// GzipMiddleware configures Gzip compression for the server
func (s *Server) GzipMiddleware() echo.MiddlewareFunc {
	return middleware.GzipWithConfig(middleware.GzipConfig{
		Level:     6,
		MinLength: 2048,
	})
}

// CacheControlMiddleware sets appropriate cache control headers based on the request path
func (s *Server) CacheControlMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Skip cache control for HTMX requests
			if c.Request().Header.Get("HX-Request") != "" {
				return next(c)
			}

			path := c.Request().URL.Path
			s.Debug("CacheControlMiddleware: Processing request for path: %s", path)

			switch {
			case strings.HasSuffix(path, ".css"), strings.HasSuffix(path, ".js"), strings.HasSuffix(path, ".html"):
				c.Response().Header().Set("Cache-Control", "public, max-age=3600, must-revalidate")
				c.Response().Header().Set("ETag", generateETag(path))
			case strings.HasSuffix(path, ".png"), strings.HasSuffix(path, ".jpg"),
				strings.HasSuffix(path, ".ico"), strings.HasSuffix(path, ".svg"):
				c.Response().Header().Set("Cache-Control", "public, max-age=604800, immutable")
			case strings.HasPrefix(path, "/api/v1/media/audio"):
				c.Response().Header().Set("Cache-Control", "no-store")
				c.Response().Header().Set("X-Content-Type-Options", "nosniff")
				c.Response().Header().Set("Accept-Ranges", "bytes")
				s.Debug("CacheControlMiddleware: Set headers for audio file: %s", path)
			case strings.HasPrefix(path, "/api/v1/media/spectrogram"):
				c.Response().Header().Set("Cache-Control", "public, max-age=2592000, immutable") // Cache spectrograms for 30 days
				s.Debug("CacheControlMiddleware: Set headers for spectrogram: %s", path)
			case strings.HasPrefix(path, "/api/v1/"):
				c.Response().Header().Set("Cache-Control", "no-store")
				c.Response().Header().Set("Pragma", "no-cache")
				c.Response().Header().Set("Expires", "0")
			default:
				c.Response().Header().Set("Cache-Control", "no-store")
			}

			err := next(c)
			if err != nil {
				// Original debug logging for backward compatibility
				s.Debug("CacheControlMiddleware: Error processing request: %v", err)

				// Enhanced structured logging
				if s.webLogger != nil {
					s.LogError(c, err, "Error in CacheControlMiddleware")
				}
			}
			return err
		}
	}
}

// VaryHeaderMiddleware sets the "Vary: HX-Request" header for all responses.
func (s *Server) VaryHeaderMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Always set Vary header for HTMX requests
			c.Response().Header().Set("Vary", "HX-Request")

			// Ensure HTMX headers are preserved
			if c.Request().Header.Get("HX-Request") != "" {
				c.Response().Header().Set("Cache-Control", "no-store")
			}

			err := next(c)
			return err
		}
	}
}

// isProtectedRoute checks if the request is protected
func isProtectedRoute(path string) bool {
	// HLS streaming routes should be protected (require authentication)
	// but we'll handle them specially in the CSRFMiddleware

	return strings.HasPrefix(path, "/settings/") ||
		strings.HasPrefix(path, "/api/v1/settings/") ||
		strings.HasPrefix(path, "/api/v1/detections/delete") ||
		strings.HasPrefix(path, "/api/v1/detections/ignore") ||
		strings.HasPrefix(path, "/api/v1/detections/review") ||
		strings.HasPrefix(path, "/api/v1/detections/lock") ||
		strings.HasPrefix(path, "/api/v1/mqtt/") ||
		strings.HasPrefix(path, "/api/v1/birdweather/") ||
		strings.HasPrefix(path, "/api/v2/") || // All v2 API routes require auth check (IP-based or login)
		strings.HasPrefix(path, "/api/v1/audio-stream-hls") || // Protect HLS streams
		strings.HasPrefix(path, "/logout") ||
		strings.HasPrefix(path, "/system") // Protect system dashboard
}

// isPublicApiRoute returns true for API routes that should be publicly accessible without authentication
func isPublicApiRoute(path string) bool {
	// Check against the defined map of public V2 prefixes.
	for prefix := range publicV2ApiPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// AuthMiddleware checks if the user is authenticated and if the request is protected
func (s *Server) AuthMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		path := c.Path()

		// Skip check for non-protected routes
		if !isProtectedRoute(path) {
			return next(c)
		}

		// Allow public API routes without authentication
		if isPublicApiRoute(path) {
			return next(c)
		}

		// Get client IP once to avoid redundant calls
		clientIPString := s.RealIP(c)

		// Check if authentication is required for this IP
		if s.OAuth2Server != nil && s.OAuth2Server.IsAuthenticationEnabled(clientIPString) {
			// Check if client is in local subnet - in that case, we can bypass auth
			clientIP := net.ParseIP(clientIPString)
			if security.IsInLocalSubnet(clientIP) {
				// Local network clients can access protected API endpoints
				// Set context values to indicate authenticated state via subnet bypass
				c.Set("server", s)
				c.Set("isAuthenticated", true)
				c.Set("authMethod", "localSubnet")
				c.Set("username", "localSubnetUser") // Placeholder username
				c.Set("userClaims", nil)             // Explicitly nil as no claims from token
				s.Debug("Client %s is in local subnet, allowing access to %s", clientIP.String(), path)
				return next(c)
			}

			// Not on local subnet, check if authenticated
			if !s.IsAccessAllowed(c) {
				s.Debug("Client %s not authenticated, denying access to %s", clientIPString, path)

				// Handle API routes with JSON response for unauthorized errors
				if strings.HasPrefix(path, "/api/") {
					return c.JSON(http.StatusUnauthorized, map[string]string{
						"error":   "Authentication required",
						"message": "You must be authenticated to access this API endpoint",
					})
				}

				// Handle regular routes with redirect to login
				rawPath := c.Request().URL.Path
				var redirectPath string
				// Validate the raw path before escaping
				if !security.IsValidRedirect(rawPath) {
					redirectPath = "/"
				} else {
					redirectPath = url.QueryEscape(rawPath)
				}

				if c.Request().Header.Get("HX-Request") == "true" {
					c.Response().Header().Set("HX-Redirect", "/login?redirect="+redirectPath)
					return c.String(http.StatusUnauthorized, "")
				}
				return c.Redirect(http.StatusFound, "/login?redirect="+redirectPath)
			}
		}

		return next(c)
	}
}

// generateETag creates a simple hash-based ETag for a given path
func generateETag(path string) string {
	h := sha256.New()
	h.Write([]byte(path))
	return fmt.Sprintf(`"%x"`, h.Sum(nil)[:8])
}
