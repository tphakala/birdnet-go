package httpcontroller

import (
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
	s.Echo.Use(CacheControlMiddleware())
	s.Echo.Use(VaryHeaderMiddleware())
}

// CacheControlMiddleware applies cache control headers for specified routes.
func CacheControlMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			path := c.Request().URL.Path
			// Apply cache control for assets and clips paths
			if strings.HasPrefix(path, "/assets/") || strings.HasPrefix(path, "/clips/") {
				c.Response().Header().Set("Cache-Control", "no-store, max-age=0") // 1 day
			} else {
				// No cache for other routes
				c.Response().Header().Set("Cache-Control", "no-store, max-age=0")
			}
			return next(c)
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
