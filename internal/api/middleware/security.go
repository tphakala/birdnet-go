package middleware

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

// Security configuration constants.
const (
	// HSTSMaxAge is the max-age value for HSTS header (1 year in seconds).
	HSTSMaxAge = 31536000
)

// SecurityConfig holds configuration for security middleware.
type SecurityConfig struct {
	// AllowedOrigins specifies which origins are permitted for CORS requests.
	// Use []string{"*"} to allow all origins (not recommended with credentials).
	AllowedOrigins []string

	// AllowCredentials indicates whether credentials (cookies, auth headers) are allowed.
	// Note: Cannot be true when AllowedOrigins contains "*" per CORS specification.
	AllowCredentials bool

	// HSTSMaxAge is the max-age value in seconds for the Strict-Transport-Security header.
	// Set to 0 to disable HSTS.
	HSTSMaxAge int

	// HSTSExcludeSubdomains controls whether HSTS applies to subdomains.
	// When false (default), HSTS includes subdomains.
	HSTSExcludeSubdomains bool

	// ContentSecurityPolicy specifies the Content-Security-Policy header value.
	// Leave empty to not set the header.
	ContentSecurityPolicy string
}

// DefaultSecurityConfig returns a SecurityConfig with sensible defaults.
// This can be used as a starting point when configuring security middleware.
// Note: Currently the server constructs SecurityConfig directly, but this
// function is provided for convenience and testing.
//
// Important: AllowCredentials is false by default because wildcard origins ("*")
// cannot be combined with credentials per the CORS specification. If you need
// credentials, specify explicit origins instead of using this default.
func DefaultSecurityConfig() SecurityConfig {
	return SecurityConfig{
		AllowedOrigins:        []string{"*"},
		AllowCredentials:      false, // Must be false with wildcard origins per CORS spec
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
			"X-CSRF-Token",
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
