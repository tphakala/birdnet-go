package middleware

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// CSRFContextKey is the key used to store CSRF token in the context.
// This must match what spa.go expects when retrieving the token.
const CSRFContextKey = "csrf"

// CSRFConfig holds configuration for the CSRF middleware.
type CSRFConfig struct {
	// Skipper defines a function to skip the middleware.
	// If nil, the default skipper is used which exempts common safe routes.
	Skipper middleware.Skipper

	// TokenLength is the length of the generated token.
	// Default is 32.
	TokenLength uint8

	// TokenLookup is a string in the form of "<source>:<key>" or "<source>:<key>,<source>:<key>"
	// that is used to extract token from the request.
	// Default is "header:X-CSRF-Token,form:_csrf".
	TokenLookup string

	// CookieName is the name of the CSRF cookie.
	// Default is "csrf".
	CookieName string

	// CookieMaxAge is the max age (in seconds) of the CSRF cookie.
	// Default is 1800 (30 minutes).
	CookieMaxAge int
}

// DefaultCSRFSkipper returns the default skipper function that exempts
// static assets, media streams, SSE, and auth endpoints from CSRF protection.
func DefaultCSRFSkipper(c echo.Context) bool {
	path := c.Request().URL.Path

	// Skip CSRF for static assets
	if strings.HasPrefix(path, "/assets/") ||
		strings.HasPrefix(path, "/ui/assets/") {
		return true
	}

	// Skip for health check
	if path == "/health" {
		return true
	}

	// Skip for media and streaming endpoints (read-only)
	if strings.HasPrefix(path, "/api/v2/media/") ||
		strings.HasPrefix(path, "/api/v2/streams/") ||
		strings.HasPrefix(path, "/api/v2/spectrogram/") ||
		strings.HasPrefix(path, "/api/v2/audio/") {
		return true
	}

	// Skip for auth endpoints (login handles auth, logout is low-risk)
	if path == "/api/v2/auth/login" ||
		path == "/api/v2/auth/logout" ||
		strings.HasPrefix(path, "/api/v2/auth/callback") {
		return true
	}

	// Skip for social OAuth endpoints (GET requests for OAuth flow)
	if strings.HasPrefix(path, "/auth/") {
		return true
	}

	return false
}

// NewCSRF creates a CSRF middleware with the given configuration.
// If config is nil, sensible defaults are used that match the legacy implementation.
func NewCSRF(config *CSRFConfig) echo.MiddlewareFunc {
	// Apply defaults
	if config == nil {
		config = &CSRFConfig{}
	}

	skipper := config.Skipper
	if skipper == nil {
		skipper = DefaultCSRFSkipper
	}

	tokenLength := config.TokenLength
	if tokenLength == 0 {
		tokenLength = 32
	}

	tokenLookup := config.TokenLookup
	if tokenLookup == "" {
		tokenLookup = "header:X-CSRF-Token,form:_csrf"
	}

	cookieName := config.CookieName
	if cookieName == "" {
		cookieName = "csrf"
	}

	cookieMaxAge := config.CookieMaxAge
	if cookieMaxAge == 0 {
		cookieMaxAge = 1800 // 30 minutes
	}

	return middleware.CSRFWithConfig(middleware.CSRFConfig{
		Skipper:        skipper,
		TokenLength:    tokenLength,
		TokenLookup:    tokenLookup,
		ContextKey:     CSRFContextKey,
		CookieName:     cookieName,
		CookiePath:     "/",
		CookieHTTPOnly: false, // Allow JavaScript to read the cookie for hobby/LAN use
		CookieSecure:   false, // Allow cookies over HTTP for non-HTTPS deployments
		CookieSameSite: http.SameSiteLaxMode,
		CookieMaxAge:   cookieMaxAge,
		ErrorHandler: func(err error, c echo.Context) error {
			GetLogger().Warn("CSRF validation failed",
				logger.String("method", c.Request().Method),
				logger.String("path", c.Request().URL.Path),
				logger.String("remote_ip", c.RealIP()),
				logger.String("error", err.Error()))

			return echo.NewHTTPError(http.StatusForbidden, "Invalid CSRF token")
		},
	})
}
