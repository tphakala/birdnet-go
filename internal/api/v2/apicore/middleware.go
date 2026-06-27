package apicore

import (
	"net/http"
	"net/url"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// tunnelProviderUnknown is the tunnel provider label for unknown providers.
const tunnelProviderUnknown = "unknown"

// unmatchedRoutePattern is the placeholder logged for the request route when echo
// has not matched the request to a registered route. echo populates ctx.Path()
// (the matched route pattern) only after routing, so an unmatched request (a 404,
// or any log emitted before routing completes) has an empty Path.
const unmatchedRoutePattern = "<unmatched>"

// RoutePattern returns the matched echo route pattern for ctx (for example
// "/api/v2/audio/:id") instead of the raw request URL path. Logging the pattern
// rather than req.URL.Path keeps secrets that live in path segments out of logs
// and telemetry: an HLS stream token in
// /api/v2/streams/hls/t/:streamToken/playlist.m3u8 and a note :id collapse to
// their route placeholders, so neither value is ever persisted. echo sets
// ctx.Path() only after routing, so an unmatched request yields
// unmatchedRoutePattern.
func RoutePattern(ctx echo.Context) string {
	if pattern := ctx.Path(); pattern != "" {
		return pattern
	}
	return unmatchedRoutePattern
}

// scrubQueryForLog redacts credential-bearing values from a raw URL query string
// before it is logged. It URL-decodes the query first because privacy's token
// scrubber pattern does not span percent-escapes, so an encoded token value (for
// example token=ab%2Bcd1234) would otherwise slip through unredacted; on a decode
// error it falls back to scrubbing the raw string. privacy.ScrubMessage also
// redacts URLs, emails, UUIDs, IPs, coordinates, and file paths.
func scrubQueryForLog(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	decoded, err := url.QueryUnescape(rawQuery)
	if err != nil {
		decoded = rawQuery
	}
	return privacy.ScrubMessage(decoded)
}

// Echo context keys set by TunnelDetectionMiddleware and read by
// LoggingMiddleware, handleErrorInternal, and domain handlers (e.g. media). They
// are exported so no package re-literals the string key.
const (
	// CtxKeyIsTunneled holds a bool: whether the request was classified as proxied/tunneled.
	CtxKeyIsTunneled = "is_tunneled"
	// CtxKeyTunnelProvider holds a string: the detected tunnel provider label.
	CtxKeyTunnelProvider = "tunnel_provider"
)

// TunnelDetectionMiddleware inspects headers to determine if the request is likely proxied
// and sets context values for logging.
func (c *Core) TunnelDetectionMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(ctx echo.Context) error {
			req := ctx.Request()
			tunneled := false
			provider := tunnelProviderUnknown

			// Only classify the request as tunneled when the IP extractor actually
			// honored a forwarded header, i.e. the resolved client IP differs from
			// the immediate connection peer. That happens only for a trusted proxy,
			// so a directly-connected client cannot spoof a "tunneled" label by
			// sending forwarded headers from an untrusted address.
			if peerIP, _ := peerAddrFromRequest(req); peerIP != nil && peerIP.String() != ctx.RealIP() {
				switch {
				case req.Header.Get(headerCFConnectingIP) != "":
					tunneled = true
					provider = "cloudflare"
				case req.Header.Get(echo.HeaderXForwardedFor) != "" || req.Header.Get(echo.HeaderXRealIP) != "":
					// Other proxy headers present: tunneled, but provider is generic.
					tunneled = true
					provider = "generic"
				}
			}

			ctx.Set(CtxKeyIsTunneled, tunneled)
			ctx.Set(CtxKeyTunnelProvider, provider)

			return next(ctx)
		}
	}
}

// LoggingMiddleware creates a middleware function that logs API requests.
func (c *Core) LoggingMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(ctx echo.Context) error {
			start := time.Now()

			// Process the request
			err := next(ctx)

			// Skip logging if APILogger is not initialized
			if c.APILogger == nil {
				return err
			}

			// Extract request information
			req := ctx.Request()
			res := ctx.Response()

			// Determine the actual status code. When a handler returns an
			// *echo.HTTPError, Echo's centralized error handler has not yet
			// executed at this point in the middleware chain, so res.Status
			// is still the default 200. Extract the real code from the error.
			status := res.Status
			if err != nil {
				var he *echo.HTTPError
				if errors.As(err, &he) {
					status = he.Code
				} else if status < http.StatusBadRequest {
					// Non-HTTP errors (e.g. database errors) won't have a
					// status set yet; Echo's error handler runs after this
					// middleware. Default to 500 to avoid logging failures
					// as successes.
					status = http.StatusInternalServerError
				}
			}

			// Get tunnel info from context
			isTunneled, _ := ctx.Get(CtxKeyIsTunneled).(bool)
			tunnelProvider, _ := ctx.Get(CtxKeyTunnelProvider).(string)

			// Log the request with structured data
			fields := []logger.Field{
				logger.String("method", req.Method),
				logger.String("path", RoutePattern(ctx)),
				logger.String("query", scrubQueryForLog(req.URL.RawQuery)),
				logger.Int("status", status),
				logger.String("ip", ctx.RealIP()), // Uses custom extractor
				logger.Bool("tunneled", isTunneled),
				logger.String("tunnel_provider", tunnelProvider),
				logger.String("user_agent", req.UserAgent()),
				logger.Int64("latency_ms", time.Since(start).Milliseconds()),
			}
			if err != nil {
				// Scrub the handler error the same way handleErrorInternal does: a
				// handler that returns an error directly (instead of routing it
				// through HandleError) would otherwise persist raw err.Error() text,
				// which can carry credentials (e.g. an RTSP/HTTP URL with embedded
				// user:pass or a token).
				fields = append(fields, logger.String("error", privacy.ScrubMessage(err.Error())))
			}

			c.APILogger.Info("API Request", fields...)

			return err
		}
	}
}

// PrivateModeAuth gates all API endpoints behind authentication when PrivateMode
// is enabled. The set of public-exempt routes is supplied by the facade via the
// injected privateModeExempt function (it composes route-path constants that live
// with their domain registrations), so the exempt allow-list cannot drift from
// the registered routes. It is applied once at the API group level by the facade.
func (c *Core) PrivateModeAuth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) error {
		// Read the live global snapshot (race-free, hot-reloading) via
		// CurrentSettings() to match the sibling publicLiveAudioAuth middleware; the
		// per-controller snapshot would miss out-of-band StoreSettings republishes.
		privateMode := c.CurrentSettings().Security.PrivateMode

		if !privateMode {
			return next(ctx)
		}
		// Fail closed: if PrivateMode is requested but no auth middleware
		// is configured the request must be rejected, not silently allowed.
		// Auth middleware is always wired up in production; reaching this
		// branch in a real deployment means the controller is misconfigured.
		if c.AuthMiddleware == nil {
			return c.HandleError(
				ctx,
				nil,
				"Private mode is enabled but authentication is not configured",
				http.StatusServiceUnavailable,
			)
		}
		// Use ctx.Path() (the registered route pattern) rather than the raw
		// request URL so the match is robust to trailing slashes, ingress
		// prefixes, and other URL normalisation differences. The method is
		// matched explicitly so that a future handler bound to the same
		// path with a different verb does not inherit the public exemption.
		if c.privateModeExempt != nil && c.privateModeExempt(ctx.Request().Method, ctx.Path()) {
			return next(ctx)
		}
		return c.AuthMiddleware(next)(ctx)
	}
}

// GetAuthMiddleware returns the authentication middleware function injected from server.
//
// Returns nil if no middleware was configured via WithAuthMiddleware option.
// Callers should be aware that applying nil middleware to Echo routes is a no-op
// (the routes become unprotected). A warning is logged during initialization
// if auth middleware is not configured.
func (c *Core) GetAuthMiddleware() echo.MiddlewareFunc {
	return c.AuthMiddleware
}
