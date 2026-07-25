// internal/api/pprof.go
package api

import (
	"crypto/subtle"
	"net/http"
	"net/http/pprof"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// PprofBasePath is the URL prefix serving the Go pprof endpoints. The pprof
	// package derives the requested profile name from this exact prefix, so it
	// cannot be relocated without wrapping every handler.
	PprofBasePath = "/debug/pprof"

	// pprofTokenParam carries the profiling token when no authentication
	// provider is configured. A query parameter rather than a header because
	// `go tool pprof` has no flag for custom headers, but does preserve query
	// parameters it did not set and merges its own into them, which keeps the
	// one-command workflow intact:
	//
	//	go tool pprof "http://host:8080/debug/pprof/profile?token=SECRET&seconds=3"
	//
	// The access logger scrubs token= from the query string it records
	// (privacy.ScrubQueryString), so the credential does not land in the logs.
	pprofTokenParam = "token" //nolint:gosec // parameter name, not a credential

	// profilingRefusedMessage is returned for every refused profiling request.
	// It is deliberately identical whether the token was absent, malformed, or
	// simply wrong.
	profilingRefusedMessage = "Profiling access denied"
)

// registerPprofRoutes mounts the Go pprof endpoints on the main web server.
//
// They used to live on the Prometheus telemetry listener, which is served by a
// bare ServeMux with no middleware, so enabling metrics also published
// profiling on every interface with no authentication at all. Here they sit
// behind the server's own auth, or behind a generated token where no
// authentication provider is configured.
//
// The routes are registered unconditionally and gated per request, so
// diagnostics.profiling.enabled hot-reloads like every other setting.
//
// Long-running requests need no special handling here: net/http/pprof extends
// the response write deadline itself (configureWriteDeadline) to the server's
// WriteTimeout plus the requested sampling duration, so the default 30-second
// CPU profile completes against the server's 30-second WriteTimeout. It reaches
// the connection through http.ResponseController, which walks the writer chain
// via Unwrap; both echo.Response and Echo's gzip writer implement Unwrap, so
// the chain holds. TestProfilingCPUProfileOutlivesWriteTimeout is the guard on
// that, since SetWriteDeadline's error is discarded inside pprof and a broken
// chain would truncate profiles silently.
func (s *Server) registerPprofRoutes() {
	gate := s.profilingGate()

	// pprof.Index serves the landing page and also dispatches every named
	// runtime profile (heap, allocs, goroutine, block, mutex, threadcreate)
	// from the path suffix, so one wildcard route covers all of them. The
	// handlers below are not runtime profiles and need their own routes; Index
	// would answer them with "Unknown profile".
	index := echo.WrapHandler(http.HandlerFunc(pprof.Index))

	s.echo.GET(PprofBasePath, index, gate)
	s.echo.GET(PprofBasePath+"/cmdline", echo.WrapHandler(http.HandlerFunc(pprof.Cmdline)), gate)
	s.echo.GET(PprofBasePath+"/profile", echo.WrapHandler(http.HandlerFunc(pprof.Profile)), gate)
	s.echo.GET(PprofBasePath+"/trace", echo.WrapHandler(http.HandlerFunc(pprof.Trace)), gate)
	// go tool pprof resolves addresses with a POST body of newline-separated
	// addresses, so /symbol answers both methods.
	s.echo.GET(PprofBasePath+"/symbol", echo.WrapHandler(http.HandlerFunc(pprof.Symbol)), gate)
	s.echo.POST(PprofBasePath+"/symbol", echo.WrapHandler(http.HandlerFunc(pprof.Symbol)), gate)
	s.echo.GET(PprofBasePath+"/*", index, gate)

	s.slogger.Debug("pprof routes registered (gated by diagnostics.profiling.enabled)",
		logger.String("path", PprofBasePath))
}

// profilingGate guards every pprof route.
//
// Settings are read per request through currentSettings() rather than captured
// at construction, so toggling diagnostics.profiling.enabled takes effect
// without a restart, matching how the basepath middleware resolves its own
// setting.
func (s *Server) profilingGate() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			settings := s.currentSettings()
			if settings == nil || !settings.Diagnostics.Profiling.Enabled {
				// 404 rather than 403: a disabled feature should not announce
				// that it exists and is merely closed.
				return echo.NewHTTPError(http.StatusNotFound)
			}

			// With an authentication provider configured, the server's own auth
			// middleware is the gate. It also applies the allowed-subnet bypass
			// exactly as it does for every other protected route, so profiling
			// is not held to a stricter standard than the settings pages that
			// already expose real credentials.
			if settings.IsAuthProviderConfigured() {
				if s.authMiddleware == nil {
					// Configured but never wired (no OAuth2Server injected).
					// Fail closed rather than fall through to the token branch,
					// where no token was minted precisely because auth exists.
					s.slogger.Error("Profiling request refused: authentication is configured but the auth middleware is not initialized",
						logger.String("ip", c.RealIP()))
					return echo.NewHTTPError(http.StatusForbidden, profilingRefusedMessage)
				}
				return s.authMiddleware(next)(c)
			}

			// No authentication provider, so the generated token is the only
			// credential. Never log its value, presented or configured.
			if !profilingTokenMatches(settings.Diagnostics.Profiling.Token, c.QueryParam(pprofTokenParam)) {
				s.slogger.Warn("Profiling request refused: missing or invalid token",
					logger.String("ip", c.RealIP()),
					logger.String("path", c.Request().URL.Path))
				return echo.NewHTTPError(http.StatusForbidden, profilingRefusedMessage)
			}

			return next(c)
		}
	}
}

// profilingTokenMatches compares the presented token against the configured one
// in constant time.
//
// An empty configured token never matches, so an instance where token
// generation failed refuses every request instead of accepting any.
func profilingTokenMatches(configured, presented string) bool {
	if configured == "" || presented == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(configured), []byte(presented)) == 1
}
