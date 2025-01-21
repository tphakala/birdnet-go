package httpcontroller

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

// configureMiddleware sets up middleware for the server.
func (s *Server) configureMiddleware() {
	s.Echo.Use(middleware.Recover())
	s.Echo.Use(s.AuthMiddleware)
	s.Echo.Use(middleware.GzipWithConfig(middleware.GzipConfig{
		Level:     6,
		MinLength: 2048,
	}))
	// Apply the Cache Control Middleware
	s.Echo.Use(s.CacheControlMiddleware())
	s.Echo.Use(VaryHeaderMiddleware())
}

// CacheControlMiddleware sets appropriate cache control headers based on the request path
func (s *Server) CacheControlMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			path := c.Request().URL.Path
			s.Debug("CacheControlMiddleware: Processing request for path: %s", path)

			switch {
			case strings.HasSuffix(path, ".css"), strings.HasSuffix(path, ".js"), strings.HasSuffix(path, ".html"):
				// CSS and JS files - shorter cache with validation
				c.Response().Header().Set("Cache-Control", "public, max-age=3600, must-revalidate")
				c.Response().Header().Set("ETag", generateETag(path))
				s.Debug("CacheControlMiddleware: Set cache headers for static file: %s", path)
			case strings.HasSuffix(path, ".png"), strings.HasSuffix(path, ".jpg"),
				strings.HasSuffix(path, ".ico"), strings.HasSuffix(path, ".svg"):
				// Images can be cached longer
				c.Response().Header().Set("Cache-Control", "public, max-age=604800, immutable")
				s.Debug("CacheControlMiddleware: Set cache headers for image: %s", path)
			case strings.HasPrefix(path, "/media/audio"):
				// Audio files - set proper headers for downloads
				c.Response().Header().Set("Cache-Control", "private, no-store")
				c.Response().Header().Set("X-Content-Type-Options", "nosniff")
				s.Debug("CacheControlMiddleware: Set headers for audio file: %s", path)
				s.Debug("CacheControlMiddleware: Headers after setting - Cache-Control: %s, X-Content-Type-Options: %s",
					c.Response().Header().Get("Cache-Control"),
					c.Response().Header().Get("X-Content-Type-Options"))
			case strings.HasPrefix(path, "/media/spectrogram"):
				// Spectrograms can be cached
				c.Response().Header().Set("Cache-Control", "public, max-age=2592000, immutable")
				s.Debug("CacheControlMiddleware: Set cache headers for spectrogram: %s", path)
			default:
				// Dynamic content
				c.Response().Header().Set("Cache-Control", "private, no-cache, must-revalidate")
				s.Debug("CacheControlMiddleware: Set default cache headers for: %s", path)
			}

			err := next(c)
			if err != nil {
				s.Debug("CacheControlMiddleware: Error processing request: %v", err)
			}
			return err
		}
	}
}

// VaryHeaderMiddleware sets the "Vary: HX-Request" header for all responses.
func VaryHeaderMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if c.Request().Header.Get("HX-Request") != "" {
				c.Response().Header().Set("Vary", "HX-Request")
			}
			return next(c)
		}
	}
}

func (s *Server) AuthMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if isProtectedRoute(c.Path()) {
			// Check for Cloudflare bypass
			if s.CloudflareAccess.IsEnabled(c) {
				return next(c)
			}

			// Check if authentication is required for this IP
			if s.OAuth2Server.IsAuthenticationEnabled(s.RealIP(c)) {
				if !s.IsAccessAllowed(c) {
					redirectPath := url.QueryEscape(c.Request().URL.Path)
					// Validate redirect path against whitelist
					if !isValidRedirect(redirectPath) {
						redirectPath = "/"
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
func isProtectedRoute(path string) bool {
	return strings.HasPrefix(path, "/settings/")
}

// generateETag creates a simple hash-based ETag for a given path
func generateETag(path string) string {
	h := sha256.New()
	h.Write([]byte(path))
	return fmt.Sprintf(`"%x"`, h.Sum(nil)[:8])
}
