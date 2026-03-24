package middleware

import (
	"net"
	"net/http"
	pathpkg "path"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// CSRF configuration constants used by both csrf.go and csrf_token.go.
// These are unexported since they're only used within the middleware package.
const (
	// CSRFContextKey is the key used to store CSRF token in the context.
	// This must match what spa.go expects when retrieving the token.
	CSRFContextKey = "csrf"

	// csrfCookieName is the name of the CSRF cookie.
	csrfCookieName = "csrf"

	// csrfCookieMaxAge is the max age of the CSRF cookie in seconds (30 minutes).
	csrfCookieMaxAge = 1800

	// csrfTokenLength is the length of the generated CSRF token in bytes.
	csrfTokenLength = 32
)

// IsSecureRequest determines if the request is over HTTPS.
// Checks direct TLS connection first, then X-Forwarded-Proto but only when
// the request originates from a trusted source (loopback or private network).
// Trusting X-Forwarded-Proto from arbitrary clients would let an attacker on
// plain HTTP inject the header, forcing Secure=true on cookies and causing
// browsers to drop them (denial-of-service on CSRF tokens).
func IsSecureRequest(r *http.Request) bool {
	// Direct TLS connection — always authoritative.
	if r.TLS != nil {
		return true
	}

	// Only trust X-Forwarded-Proto when the immediate client is on a
	// loopback or private network address, which implies a trusted reverse
	// proxy (nginx, Caddy, Cloudflare tunnel, etc.).
	if proto := r.Header.Get("X-Forwarded-Proto"); proto == "https" {
		if isTrustedRemote(r.RemoteAddr) {
			return true
		}
	}

	return false
}

// isTrustedRemote reports whether remoteAddr belongs to a loopback or
// private (RFC 1918 / RFC 4193) network, indicating a trusted reverse proxy.
func isTrustedRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// RemoteAddr without a port (unlikely but handle gracefully).
		host = remoteAddr
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	return ip.IsLoopback() || ip.IsPrivate()
}

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

	// SecureCookie sets the Secure flag on the initial CSRF cookie set by Echo's
	// middleware. Set to true when the server is configured with TLS directly.
	// For reverse-proxy deployments (TLS terminated upstream, TLSEnabled=false),
	// CSRFCookieRefresh overwrites the cookie with the correct Secure flag via
	// IsSecureRequest() on every successful response, so the initial value here
	// is only relevant for the first request before CSRFCookieRefresh runs.
	SecureCookie bool
}

// isSafeHTTPMethod reports whether the given HTTP method is safe (read-only)
// per RFC 7231 and therefore does not need CSRF protection.
func isSafeHTTPMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

// DefaultCSRFSkipper returns the default skipper function that exempts
// static assets, media streams, SSE, and auth endpoints from CSRF protection.
func DefaultCSRFSkipper(c echo.Context) bool {
	// Clean the path to prevent traversal bypasses (e.g., /assets/../api/v2/admin
	// would match the /assets/ prefix but route to a protected endpoint).
	path := pathpkg.Clean(c.Request().URL.Path)

	// Skip CSRF for static assets
	if strings.HasPrefix(path, "/assets/") ||
		strings.HasPrefix(path, "/ui/assets/") {
		return true
	}

	// Skip for health check
	if path == "/health" {
		return true
	}

	// Skip for media and streaming endpoints only when using safe (read-only)
	// HTTP methods. POST/PUT/DELETE/PATCH on these paths still require CSRF
	// to prevent state-changing actions from bypassing protection.
	if strings.HasPrefix(path, "/api/v2/media/") ||
		strings.HasPrefix(path, "/api/v2/streams/") ||
		strings.HasPrefix(path, "/api/v2/spectrogram/") ||
		strings.HasPrefix(path, "/api/v2/audio/") {
		return isSafeHTTPMethod(c.Request().Method)
	}

	// Skip for auth endpoints (login needs to work before CSRF token exists)
	if path == "/api/v2/auth/login" ||
		strings.HasPrefix(path, "/api/v2/auth/callback") {
		return true
	}

	// Skip for social OAuth endpoints (GET requests for OAuth flow)
	if strings.HasPrefix(path, "/auth/") {
		return true
	}

	return false
}

// CSRFCookieRefresh returns a middleware that refreshes the CSRF cookie expiration
// on every non-skipped API request. The skipper should match the one used by
// NewCSRF to ensure consistent skip behavior. If nil, DefaultCSRFSkipper is used.
//
// Echo v4.15.0+ introduced Sec-Fetch-Site header checks that short-circuit the
// CSRF middleware before it reaches the cookie-setting code. This means the
// cookie's max-age is never extended during normal same-origin browsing, causing
// it to expire after 30 minutes. This middleware fixes that by refreshing the
// cookie independently of the token validation path.
func CSRFCookieRefresh(skipper middleware.Skipper) echo.MiddlewareFunc {
	if skipper == nil {
		skipper = DefaultCSRFSkipper
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if skipper(c) {
				return next(c)
			}

			err := next(c)

			// On success, refresh the CSRF cookie if one exists
			if err == nil {
				if cookie, cookieErr := c.Cookie(csrfCookieName); cookieErr == nil && cookie.Value != "" {
					setCSRFCookie(c, cookie.Value)
				}
			}

			return err
		}
	}
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
		tokenLength = csrfTokenLength
	}

	tokenLookup := config.TokenLookup
	if tokenLookup == "" {
		tokenLookup = "header:X-CSRF-Token,form:_csrf"
	}

	cookieName := config.CookieName
	if cookieName == "" {
		cookieName = csrfCookieName
	}

	cookieMaxAge := config.CookieMaxAge
	if cookieMaxAge == 0 {
		cookieMaxAge = csrfCookieMaxAge
	}

	return middleware.CSRFWithConfig(middleware.CSRFConfig{
		Skipper:        skipper,
		TokenLength:    tokenLength,
		TokenLookup:    tokenLookup,
		ContextKey:     CSRFContextKey,
		CookieName:     cookieName,
		CookiePath:     "/",
		CookieHTTPOnly: false, // Allow JavaScript to read the cookie for hobby/LAN use
		CookieSecure:   config.SecureCookie,
		CookieSameSite: http.SameSiteLaxMode,
		CookieMaxAge:   cookieMaxAge,
		ErrorHandler: func(err error, c echo.Context) error {
			GetLogger().Warn("CSRF validation failed",
				logger.String("method", c.Request().Method),
				logger.String("path", c.Request().URL.Path),
				logger.String("remote_ip", c.RealIP()),
				logger.Error(err))

			return echo.NewHTTPError(http.StatusForbidden, "Invalid CSRF token")
		},
	})
}
