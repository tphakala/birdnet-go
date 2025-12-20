package middleware

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

// Security configuration constants.
const (
	// HStsMaxAge is the max-age value for HSTS header (1 year in seconds).
	HSTSMaxAge = 31536000
)

// SecurityConfig holds configuration for security middleware.
type SecurityConfig struct {
	// CORS settings
	AllowedOrigins   []string
	AllowCredentials bool

	// HSTS settings
	HSTSMaxAge            int
	HSTSExcludeSubdomains bool

	// Content Security Policy
	ContentSecurityPolicy string
}

// DefaultSecurityConfig returns a SecurityConfig with sensible defaults.
func DefaultSecurityConfig() SecurityConfig {
	return SecurityConfig{
		AllowedOrigins:        []string{"*"},
		AllowCredentials:      true,
		HSTSMaxAge:            HSTSMaxAge,
		HSTSExcludeSubdomains: false,
		ContentSecurityPolicy: "",
	}
}

// NewCORS creates a CORS middleware with the given configuration.
func NewCORS(config SecurityConfig) echo.MiddlewareFunc {
	return middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: config.AllowedOrigins,
		AllowMethods: []string{
			http.MethodGet,
			http.MethodHead,
			http.MethodPut,
			http.MethodPatch,
			http.MethodPost,
			http.MethodDelete,
			http.MethodOptions,
		},
		AllowHeaders: []string{
			echo.HeaderOrigin,
			echo.HeaderContentType,
			echo.HeaderAccept,
			echo.HeaderAuthorization,
			"X-Requested-With",
			"HX-Request",
			"HX-Target",
			"HX-Current-URL",
		},
		AllowCredentials: config.AllowCredentials,
	})
}

// NewSecureHeaders creates a middleware that sets security-related HTTP headers.
func NewSecureHeaders(config SecurityConfig) echo.MiddlewareFunc {
	return middleware.SecureWithConfig(middleware.SecureConfig{
		XSSProtection:         "1; mode=block",
		ContentTypeNosniff:    "nosniff",
		XFrameOptions:         "SAMEORIGIN",
		HSTSMaxAge:            config.HSTSMaxAge,
		HSTSExcludeSubdomains: config.HSTSExcludeSubdomains,
		ContentSecurityPolicy: config.ContentSecurityPolicy,
	})
}

// NewBodyLimit creates a middleware that limits the request body size.
func NewBodyLimit(limit string) echo.MiddlewareFunc {
	return middleware.BodyLimit(limit)
}
