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
	// CSRFContextKey is the key used to store the CSRF token in the Echo context.
	// EnsureCSRFToken reads it to hand the token to the SPA via /api/v2/app/config.
	CSRFContextKey = "csrf"

	// csrfCookieName is the name of the CSRF cookie.
	csrfCookieName = "csrf"

	// csrfCookieMaxAge is the max age of the CSRF cookie in seconds (30 minutes).
	csrfCookieMaxAge = 1800

	// csrfTokenLength is the length of the generated CSRF token in bytes.
	csrfTokenLength = 32

	// secFetchSiteSameOrigin and secFetchSiteNone are the Sec-Fetch-Site header
	// values that Echo v4.15 treats as already-safe, returning before it compares
	// the CSRF token (GHSA-9fhj-f35q-w532). NewCSRF strips these values on
	// non-skipped requests so Echo always validates the token.
	secFetchSiteSameOrigin = "same-origin"
	secFetchSiteNone       = "none"
)

// pprofBasePath is the URL prefix under which the api package mounts the Go
// pprof endpoints. It is duplicated from api.PprofBasePath rather than imported
// because the api package imports this one, so the dependency cannot be
// reversed.
//
// Nothing mechanically couples the two constants, so TestPprofSkippersMatchMountPath
// in package api feeds api.PprofBasePath through both skippers. Without it a
// relocated mount would silently re-enable CSRF on the symbol POST and
// double-gzip every profile, with no test failing.
const pprofBasePath = "/debug/pprof"

// isPprofPath reports whether an already-cleaned request path addresses the
// pprof mount. Shared by the CSRF and gzip skippers so the two cannot drift
// into different spellings of the same predicate.
//
// The caller passes a path that has been through path.Clean, which is what
// keeps a traversal such as /debug/pprof/../api/v2/settings from inheriting the
// exemption: it cleans to the settings path and stops matching here.
func isPprofPath(cleanPath string) bool {
	return cleanPath == pprofBasePath || strings.HasPrefix(cleanPath, pprofBasePath+"/")
}

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

	// SecureCookie sets the Secure flag on the CSRF cookie Echo's middleware sets.
	// Set to true when the server terminates TLS directly. For reverse-proxy
	// deployments that terminate TLS upstream (TLSEnabled=false), the cookie is
	// not marked Secure; making that flag request-aware (via IsSecureRequest) is a
	// separate, pre-existing improvement tracked outside this change.
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

	// Skip for auth endpoints (login needs to work before CSRF token exists,
	// logout must work even with expired CSRF tokens on long-lived pages)
	if path == "/api/v2/auth/login" ||
		path == "/api/v2/auth/logout" ||
		strings.HasPrefix(path, "/api/v2/auth/callback") {
		return true
	}

	// Skip for social OAuth endpoints (GET requests for OAuth flow)
	if strings.HasPrefix(path, "/auth/") {
		return true
	}

	// Skip for the pprof endpoints. `go tool pprof` resolves addresses with a
	// POST to /debug/pprof/symbol and sends no CSRF token, so requiring one
	// would break symbolization for the tool the endpoints exist to serve.
	// Exempting it is safe on both counts CSRF protects: pprof.Symbol is
	// read-only (it returns function names for addresses, changing nothing),
	// and the routes carry their own gate, either the auth middleware or a
	// secret query token a cross-site request cannot supply.
	//
	// Scoped to safe methods plus that one POST, matching the narrower idiom
	// the media/streams block above uses, so a future state-changing route
	// under this prefix does not inherit the exemption by accident.
	if isPprofPath(path) {
		method := c.Request().Method
		return isSafeHTTPMethod(method) ||
			(method == http.MethodPost && path == pprofBasePath+"/symbol")
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

	echoCSRF := middleware.CSRFWithConfig(middleware.CSRFConfig{
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

	// Defense in depth against GHSA-9fhj-f35q-w532. Echo v4.15's CSRF middleware
	// treats a request as already safe, skipping token comparison entirely, when
	// it carries Sec-Fetch-Site: same-origin or none. Browsers forbid scripts from
	// setting Sec-Fetch-Site, but a non-browser client that already holds a session
	// cookie can forge it and reach state-changing routes without a token. For
	// requests the skipper does not exempt, strip those two values before Echo
	// inspects them so Echo always falls through to token validation, then restore
	// the header afterward so the request is left as received. same-site and
	// cross-site are left intact, preserving Echo's explicit cross-site block.
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		guarded := echoCSRF(next)
		return func(c echo.Context) error {
			if !skipper(c) {
				if secFetchSite := c.Request().Header.Get(echo.HeaderSecFetchSite); secFetchSite == secFetchSiteSameOrigin || secFetchSite == secFetchSiteNone {
					c.Request().Header.Del(echo.HeaderSecFetchSite)
					defer c.Request().Header.Set(echo.HeaderSecFetchSite, secFetchSite)
				}
			}
			return guarded(c)
		}
	}
}
