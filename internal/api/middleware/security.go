package middleware

import (
	"net"
	"net/http"
	"slices"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// GetLogger returns the access logging package logger.
func GetLogger() logger.Logger {
	return logger.Global().Module("access")
}

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

	// ReferrerPolicy specifies the Referrer-Policy header value.
	// Default is "strict-origin-when-cross-origin".
	ReferrerPolicy string

	// AllowEmbedding controls whether the application can be embedded in iframes.
	// When true, the X-Frame-Options header is omitted, allowing embedding.
	AllowEmbedding bool
}

// DefaultSecurityConfig returns a SecurityConfig with sensible defaults.
// This can be used as a starting point when configuring security middleware.
// Note: Currently the server constructs SecurityConfig directly, but this
// function is provided for convenience and testing.
//
// Important: AllowCredentials is false by default because wildcard origins ("*")
// cannot be combined with credentials per the CORS specification. If you need
// credentials, specify explicit origins instead of using this default.
func DefaultSecurityConfig() *SecurityConfig {
	return &SecurityConfig{
		AllowedOrigins:        []string{"*"},
		AllowCredentials:      false, // Must be false with wildcard origins per CORS spec
		HSTSMaxAge:            HSTSMaxAge,
		HSTSExcludeSubdomains: false,
		ContentSecurityPolicy: "",
		ReferrerPolicy:        "strict-origin-when-cross-origin",
	}
}

// hasWildcardOrigin reports whether the origin list contains the wildcard "*".
func hasWildcardOrigin(origins []string) bool {
	return slices.Contains(origins, "*")
}

// NewCORS creates a CORS middleware with the given configuration.
//
// When AllowCredentials is true and AllowedOrigins contains "*", the middleware
// reflects the request's Origin header instead of sending the literal "*".
// Sending "Access-Control-Allow-Origin: *" together with
// "Access-Control-Allow-Credentials: true" violates the CORS specification and
// causes browsers to reject the response.
func NewCORS(config *SecurityConfig) echo.MiddlewareFunc {
	corsConfig := middleware.CORSConfig{
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
		},
		AllowCredentials: config.AllowCredentials,
	}

	// When credentials are enabled with a wildcard origin, use AllowOriginFunc
	// to reflect the actual request origin. This avoids the spec violation while
	// preserving the "allow any origin" intent for local-network deployments.
	if config.AllowCredentials && hasWildcardOrigin(config.AllowedOrigins) {
		corsConfig.AllowOriginFunc = func(origin string) (bool, error) {
			return true, nil
		}
	}

	return middleware.CORSWithConfig(corsConfig)
}

// NewSecureHeaders creates a middleware that sets security-related HTTP headers.
// In addition to Echo's built-in secure headers, this sets Cross-Origin-Opener-Policy
// and adds a frame-ancestors CSP directive when embedding is not allowed.
func NewSecureHeaders(config *SecurityConfig) echo.MiddlewareFunc {
	xFrameOptions := "SAMEORIGIN"
	if config.AllowEmbedding {
		xFrameOptions = ""
	}

	// Build CSP: add frame-ancestors 'self' when embedding is disabled
	csp := config.ContentSecurityPolicy
	if !config.AllowEmbedding && !strings.Contains(strings.ToLower(csp), "frame-ancestors") {
		if csp == "" {
			csp = "frame-ancestors 'self'"
		} else {
			csp += "; frame-ancestors 'self'"
		}
	}

	referrerPolicy := config.ReferrerPolicy
	if referrerPolicy == "" {
		referrerPolicy = "strict-origin-when-cross-origin"
	}

	echoSecure := middleware.SecureWithConfig(middleware.SecureConfig{
		XSSProtection:         "0", // Disable deprecated XSS Auditor per OWASP guidance
		ContentTypeNosniff:    "nosniff",
		XFrameOptions:         xFrameOptions,
		HSTSMaxAge:            config.HSTSMaxAge,
		HSTSExcludeSubdomains: config.HSTSExcludeSubdomains,
		ContentSecurityPolicy: csp,
		ReferrerPolicy:        referrerPolicy,
	})

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		// Wrap next once at setup time, not per-request.
		wrapped := echoSecure(next)
		return func(c echo.Context) error {
			// Set COOP only on secure contexts (HTTPS or localhost).
			// Browsers ignore COOP on non-trustworthy origins per
			// https://www.w3.org/TR/powerful-features/#potentially-trustworthy-origin
			if IsSecureRequest(c.Request()) || isLocalhost(c.Request()) {
				c.Response().Header().Set("Cross-Origin-Opener-Policy", "same-origin")
			}
			return wrapped(c)
		}
	}
}

// isLocalhost returns true if the request's Host is a localhost address,
// which browsers consider a trustworthy origin even over plain HTTP.
func isLocalhost(r *http.Request) bool {
	host := r.Host
	// Strip port if present.
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// NewBodyLimit creates a middleware that limits the request body size.
func NewBodyLimit(limit string) echo.MiddlewareFunc {
	return middleware.BodyLimit(limit)
}
