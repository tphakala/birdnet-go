package httpcontroller

import (
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/security"
)

// CSRFContextKey is the key used to store CSRF token in the context
const CSRFContextKey = "birdnet-go-csrf"

// configureMiddleware sets up middleware for the server.
func (s *Server) configureMiddleware() {
	s.Echo.Use(middleware.Recover())
	s.Echo.Use(s.CSRFMiddleware())
	s.Echo.Use(s.AuthMiddleware)
	s.Echo.Use(s.GzipMiddleware())
	s.Echo.Use(s.CacheControlMiddleware())
	s.Echo.Use(s.VaryHeaderMiddleware())
}

// getMiddlewareLogger creates a component-specific middleware logger
func (s *Server) getMiddlewareLogger(component string) *logger.Logger {
	if s.Logger == nil {
		return nil
	}
	return s.Logger.Named("middleware." + component)
}

// getRequestLogger creates a request-specific logger with connection ID and client IP
func (s *Server) getRequestLogger(componentLogger *logger.Logger, c echo.Context) *logger.Logger {
	if componentLogger == nil {
		return nil
	}

	// Generate a short unique ID for this request if not already present
	requestID := c.Request().Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = uuid.New().String()[:8]
		c.Request().Header.Set("X-Request-ID", requestID)
	}

	clientIP := s.RealIP(c)

	return componentLogger.With(
		"request_id", requestID,
		"client_ip", clientIP,
		"method", c.Request().Method,
		"path", c.Request().URL.Path,
	)
}

// CSRFMiddleware configures CSRF protection for the server
func (s *Server) CSRFMiddleware() echo.MiddlewareFunc {
	// Create a component-specific logger
	csrfLogger := s.getMiddlewareLogger("csrf")

	config := middleware.CSRFConfig{
		TokenLookup:    "header:X-CSRF-Token,form:_csrf",
		CookieName:     "csrf",
		CookiePath:     "/",
		CookieHTTPOnly: true,
		CookieSameSite: http.SameSiteLaxMode,
		CookieMaxAge:   1800, // 30 minutes token lifetime
		TokenLength:    32,
		ContextKey:     CSRFContextKey,
		Skipper: func(c echo.Context) bool {
			path := c.Path()
			// Skip CSRF for static assets and auth endpoints only
			return strings.HasPrefix(path, "/assets/") ||
				strings.HasPrefix(path, "/api/v1/media/") ||
				strings.HasPrefix(path, "/api/v1/sse") ||
				strings.HasPrefix(path, "/api/v1/audio-level") ||
				strings.HasPrefix(path, "/api/v1/auth/") ||
				strings.HasPrefix(path, "/api/v1/oauth2/token") ||
				path == "/api/v1/oauth2/callback"
		},
		ErrorHandler: func(err error, c echo.Context) error {
			// Get a request-specific logger
			reqLogger := s.getRequestLogger(csrfLogger, c)

			if reqLogger != nil {
				reqLogger.Error("CSRF token validation failed",
					"token_header", c.Request().Header.Get("X-CSRF-Token"),
					"token_form", c.FormValue("_csrf"),
					"cookies", c.Request().Header.Get("Cookie"),
					"error", err)
			} else if s.isDevMode() {
				s.Debug("🚨 CSRF ERROR: Rejected request")
				s.Debug("🔍 Request Method: %s, Path: %s", c.Request().Method, c.Request().URL.Path)
				s.Debug("📌 CSRF Token in Header: %s", c.Request().Header.Get("X-CSRF-Token"))
				s.Debug("📌 CSRF Token in Form: %s", c.FormValue("_csrf"))
				s.Debug("📝 All Cookies: %s", c.Request().Header.Get("Cookie"))
				s.Debug("💡 Error Details: %v", err)
			}

			return echo.NewHTTPError(http.StatusForbidden, "Invalid CSRF token")
		},
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		csrfMiddleware := middleware.CSRFWithConfig(config)
		return func(c echo.Context) error {
			clientIP := net.ParseIP(s.RealIP(c))
			config.CookieSecure = !security.IsInLocalSubnet(clientIP)
			return csrfMiddleware(next)(c)
		}
	}
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
	// Create a component-specific logger
	cacheLogger := s.getMiddlewareLogger("cache")

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Get a request-specific logger
			reqLogger := s.getRequestLogger(cacheLogger, c)

			// Skip cache control for HTMX requests
			if c.Request().Header.Get("HX-Request") != "" {
				return next(c)
			}

			path := c.Request().URL.Path

			if reqLogger != nil && s.isDevMode() {
				reqLogger.Debug("Processing request", "path", path)
			} else if s.isDevMode() {
				s.Debug("CacheControlMiddleware: Processing request for path: %s", path)
			}

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

				if reqLogger != nil && s.isDevMode() {
					reqLogger.Debug("Set headers for audio file")
				} else if s.isDevMode() {
					s.Debug("CacheControlMiddleware: Set headers for audio file: %s", path)
				}
			case strings.HasPrefix(path, "/api/v1/media/spectrogram"):
				c.Response().Header().Set("Cache-Control", "public, max-age=2592000, immutable") // Cache spectrograms for 30 days

				if reqLogger != nil && s.isDevMode() {
					reqLogger.Debug("Set headers for spectrogram")
				} else if s.isDevMode() {
					s.Debug("CacheControlMiddleware: Set headers for spectrogram: %s", path)
				}
			case strings.HasPrefix(path, "/api/v1/"):
				c.Response().Header().Set("Cache-Control", "no-store")
				c.Response().Header().Set("Pragma", "no-cache")
				c.Response().Header().Set("Expires", "0")
			default:
				c.Response().Header().Set("Cache-Control", "no-store")
			}

			err := next(c)
			if err != nil {
				if reqLogger != nil {
					reqLogger.Error("Error processing request", "error", err)
				} else if s.isDevMode() {
					s.Debug("CacheControlMiddleware: Error processing request: %v", err)
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

// AuthMiddleware checks if the user is authenticated and if the request is protected
func (s *Server) AuthMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	// Create a component-specific logger
	authLogger := s.getMiddlewareLogger("auth")

	return func(c echo.Context) error {
		// Get a request-specific logger
		reqLogger := s.getRequestLogger(authLogger, c)

		if isProtectedRoute(c.Path()) {
			// Check for Cloudflare bypass
			if s.CloudflareAccess.IsEnabled(c) {
				if reqLogger != nil && s.isDevMode() {
					reqLogger.Debug("Cloudflare access enabled, bypassing authentication")
				}
				return next(c)
			}

			// Check if authentication is required for this IP
			clientIP := s.RealIP(c)
			if s.OAuth2Server.IsAuthenticationEnabled(clientIP) {
				if !s.IsAccessAllowed(c) {
					redirectPath := url.QueryEscape(c.Request().URL.Path)
					// Validate redirect path against whitelist
					if !isValidRedirect(redirectPath) {
						if reqLogger != nil {
							reqLogger.Warn("Invalid redirect path", "redirect", redirectPath)
						}
						redirectPath = "/"
					}

					if reqLogger != nil {
						reqLogger.Info("Authentication required, redirecting",
							"redirect_to", "/login?redirect="+redirectPath,
							"is_htmx", c.Request().Header.Get("HX-Request") == "true")
					}

					if c.Request().Header.Get("HX-Request") == "true" {
						c.Response().Header().Set("HX-Redirect", "/login?redirect="+redirectPath)
						return c.String(http.StatusUnauthorized, "")
					}
					return c.Redirect(http.StatusFound, "/login?redirect="+redirectPath)
				}
			}
		}
		return next(c)
	}
}

// isProtectedRoute checks if the request is protected
func isProtectedRoute(path string) bool {
	return strings.HasPrefix(path, "/settings/") ||
		strings.HasPrefix(path, "/api/v1/settings/") ||
		strings.HasPrefix(path, "/api/v1/detections/delete") ||
		strings.HasPrefix(path, "/api/v1/detections/ignore") ||
		strings.HasPrefix(path, "/api/v1/detections/review") ||
		strings.HasPrefix(path, "/api/v1/detections/lock") ||
		strings.HasPrefix(path, "/api/v1/mqtt/") ||
		strings.HasPrefix(path, "/api/v2/system/") || // Protect all system API routes
		strings.HasPrefix(path, "/api/v2/settings/") ||
		strings.HasPrefix(path, "/api/v2/control/") ||
		strings.HasPrefix(path, "/api/v2/integrations/") ||
		strings.HasPrefix(path, "/logout")
}

// generateETag creates a simple hash-based ETag for a given path
func generateETag(path string) string {
	h := sha256.New()
	h.Write([]byte(path))
	return fmt.Sprintf(`"%x"`, h.Sum(nil)[:8])
}
