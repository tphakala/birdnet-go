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
	// corsHeaderIfMatch and corsHeaderETag are the conditional-write headers the
	// settings API uses (apicore.HeaderIfMatch and apicore.HeaderETag; this package
	// does not import apicore).
	corsHeaderIfMatch = "If-Match"
	corsHeaderETag    = "ETag"

	// HSTSMaxAge is the max-age value for HSTS header (1 year in seconds).
	HSTSMaxAge = 31536000
)

// SecurityConfig holds configuration for security middleware.
type SecurityConfig struct {
	// AllowedOrigins specifies which origins are permitted for CORS requests.
	// Use []string{"*"} to allow all origins.
	AllowedOrigins []string

	// AllowCredentials indicates whether cross-origin requests may carry
	// credentials (cookies, HTTP authentication). It only takes effect for a
	// non-empty list of literal origins; see NewCORS.
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

// corsPatternChars are the characters the CORS middleware treats as pattern
// syntax in an allowed origin: "*" matches any run of characters and "?" any
// single character.
const corsPatternChars = "*?"

// hasExplicitOrigins reports whether the origin list is non-empty and holds
// only literal origins, with no "*" wildcard and no entry containing a pattern
// character. It is the only kind of list credentials may be allowed for.
func hasExplicitOrigins(origins []string) bool {
	return len(origins) > 0 && !slices.ContainsFunc(origins, func(origin string) bool {
		return strings.ContainsAny(origin, corsPatternChars)
	})
}

// NewCORS creates a CORS middleware with the given configuration.
//
// Credentials are allowed only for a non-empty list of literal origins. When
// AllowedOrigins is empty or has an entry containing "*" or "?", the middleware
// omits "Access-Control-Allow-Credentials" whatever AllowCredentials says, and
// answers a "*" entry or an empty list with "Access-Control-Allow-Origin: *". Sending
// "*" together with credentials violates the CORS specification, and reflecting
// the request origin to work around that would let any page, including one
// served from another port of the same host, read credentialed responses.
// Requests that authenticate with an Authorization header are not affected.
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
			echo.HeaderCacheControl,
			"X-Requested-With",
			"X-CSRF-Token",
			corsHeaderIfMatch,
		},
		// ETag is not a CORS-safelisted response header; cross-origin clients need
		// it to send a settings If-Match.
		ExposeHeaders:    []string{corsHeaderETag},
		AllowCredentials: config.AllowCredentials && hasExplicitOrigins(config.AllowedOrigins),
	}

	return middleware.CORSWithConfig(corsConfig)
}

// NewSecureHeaders creates a middleware that sets security-related HTTP headers.
// In addition to Echo's built-in secure headers, this sets Cross-Origin-Opener-Policy
// and adds a frame-ancestors CSP directive when embedding is not allowed.
//
// HSTS is sent when Echo's c.Scheme() reports "https". Since Echo v4.16 that
// depends on Echo#SchemeExtractor, which the API core configures to honor
// X-Forwarded-Proto only from trusted proxies (see apicore).
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
