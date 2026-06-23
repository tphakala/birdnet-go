package api

import (
	"context"
	"embed"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"
	"github.com/markbates/goth/gothic"
	"github.com/tphakala/birdnet-go/frontend"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/api/auth"
	mw "github.com/tphakala/birdnet-go/internal/api/middleware"
	apiv2 "github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/engine"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/health"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/security"
	"github.com/tphakala/birdnet-go/internal/suncalc"

	"golang.org/x/crypto/acme/autocert"
)

// ImageDataFs holds the embedded image provider data filesystem.
// This is set by main.go before starting the server.
var ImageDataFs embed.FS

// ImageProviderRegistry is set by main.go before starting the server.
// It provides access to bird image providers.
var ImageProviderRegistry *imageprovider.ImageProviderRegistry

// Server is the main HTTP server for BirdNET-Go.
// It manages the Echo framework instance, middleware, and all HTTP routes.
type Server struct {
	// Core components
	echo           *echo.Echo
	config         *Config
	settings       *conf.Settings
	slogger        logger.Logger // Centralized structured logger (Module("api"))
	securityLogger logger.Logger // OAuth login-flow logger (Module("security"), issue #3381)

	// Dependencies
	dataStore      datastore.Interface
	v2Manager      datastoreV2.Manager
	birdImageCache *imageprovider.BirdImageCache
	sunCalc        *suncalc.SunCalc
	processor      *processor.Processor
	oauth2Server   *security.OAuth2Server
	metrics        *observability.Metrics

	// Auth components (owned by server, injected into controllers)
	authService    auth.Service
	authMiddleware echo.MiddlewareFunc

	// Audio engine (unified audio subsystem)
	engine *engine.AudioEngine

	// Model gallery manager (optional, nil when not configured)
	modelManager *classifier.ModelManager

	// Health error buffer shared between the logger and health checks
	healthErrors *health.ErrorRingBuffer

	// Channels
	controlChan    chan string
	audioLevelChan chan audiocore.AudioLevelData

	// API controller
	apiController *apiv2.Controller

	// Static file serving
	staticServer *StaticFileServer
	spaHandler   *SPAHandler

	// HTTP server for redirect/ACME challenges (when TLS is enabled)
	httpRedirectServer *http.Server

	// Lifecycle management
	startTime time.Time
}

// currentSettings returns the latest *conf.Settings snapshot published via
// conf.StoreSettings, or the server's constructor-provided pointer when no
// snapshot has been published yet (test harnesses that skip the global
// publish, or early startup before the first Load). Routing the hot-path
// middleware through this helper keeps the basepath resolution race-free
// while still handling the uninitialised-global case gracefully.
func (s *Server) currentSettings() *conf.Settings {
	if cur := conf.GetSettings(); cur != nil {
		return cur
	}
	return s.settings
}

// ingressPath returns the effective base path prefix for the current request.
// Priority: X-Ingress-Path header > X-Forwarded-Prefix header > config BasePath > empty.
//
// Header values are trimmed of trailing slashes before the safety check so that
// real-world values like "/ingress/token///" are accepted (and normalized) while
// embedded dangerous sequences like "//evil" or "/../admin" still get rejected.
func ingressPath(c echo.Context, settings *conf.Settings) string {
	if p := strings.TrimRight(c.Request().Header.Get("X-Ingress-Path"), "/"); isSafePathPrefix(p) {
		return p
	}
	if p := strings.TrimRight(c.Request().Header.Get("X-Forwarded-Prefix"), "/"); isSafePathPrefix(p) {
		return p
	}
	if settings != nil && settings.WebServer.BasePath != "" {
		return strings.TrimRight(settings.WebServer.BasePath, "/")
	}
	return ""
}

// hasPercentEncodedPrefix reports whether rawPath begins with encodedPrefix,
// matching the hex digits inside any %XX sequences case-insensitively so that
// reverse proxies that forward lowercase hex (%c3%9c) still match Go's
// canonical uppercase form (%C3%9C). All non-%-prefixed bytes are compared
// byte-for-byte because URL path components are case-sensitive.
// Returns the matched prefix length (in rawPath bytes), or -1 on mismatch.
func hasPercentEncodedPrefix(rawPath, encodedPrefix string) int {
	i := 0
	for i < len(encodedPrefix) {
		if i >= len(rawPath) {
			return -1
		}
		if encodedPrefix[i] == '%' && rawPath[i] == '%' &&
			i+2 < len(encodedPrefix) && i+2 < len(rawPath) {
			if !strings.EqualFold(rawPath[i+1:i+3], encodedPrefix[i+1:i+3]) {
				return -1
			}
			i += 3
			continue
		}
		if rawPath[i] != encodedPrefix[i] {
			return -1
		}
		i++
	}
	return i
}

// isSafePathPrefix validates that a path prefix (typically from a proxy header)
// is safe for use in redirects and HTML asset rewriting. Rules align with
// validateBasePath() in internal/conf/validate.go so that header-supplied and
// YAML-configured basepaths share the same rejection rules.
//
// Rejects:
//   - empty strings
//   - values not starting with "/"
//   - protocol-relative URLs ("//...") and embedded "//" sequences
//   - absolute URLs ("://")
//   - backslashes ("/\..." normalizes to "//" in browsers)
//   - path traversal ("..")
//   - CR/LF/NUL injection
func isSafePathPrefix(p string) bool {
	if p == "" || !strings.HasPrefix(p, "/") {
		return false
	}
	for _, bad := range []string{"//", "\\", "://", "..", "\n", "\r", "\x00"} {
		if strings.Contains(p, bad) {
			return false
		}
	}
	return true
}

// ServerOption is a functional option for configuring the Server.
type ServerOption func(*Server)

// WithDataStore sets the datastore for the server.
func WithDataStore(ds datastore.Interface) ServerOption {
	return func(s *Server) {
		s.dataStore = ds
	}
}

// WithBirdImageCache sets the bird image cache for the server.
func WithBirdImageCache(cache *imageprovider.BirdImageCache) ServerOption {
	return func(s *Server) {
		s.birdImageCache = cache
	}
}

// WithSunCalc sets the sun calculator for the server.
func WithSunCalc(sc *suncalc.SunCalc) ServerOption {
	return func(s *Server) {
		s.sunCalc = sc
	}
}

// WithProcessor sets the processor for the server.
func WithProcessor(proc *processor.Processor) ServerOption {
	return func(s *Server) {
		s.processor = proc
	}
}

// WithOAuth2Server sets the OAuth2 server for authentication.
func WithOAuth2Server(oauth *security.OAuth2Server) ServerOption {
	return func(s *Server) {
		s.oauth2Server = oauth
	}
}

// WithMetrics sets the observability metrics for the server.
func WithMetrics(m *observability.Metrics) ServerOption {
	return func(s *Server) {
		s.metrics = m
	}
}

// WithControlChannel sets the control channel for system commands.
func WithControlChannel(ch chan string) ServerOption {
	return func(s *Server) {
		s.controlChan = ch
	}
}

// WithAudioLevelChannel sets the audio level channel for SSE streaming.
func WithAudioLevelChannel(ch chan audiocore.AudioLevelData) ServerOption {
	return func(s *Server) {
		s.audioLevelChan = ch
	}
}

// WithV2Manager sets the v2 database manager for the server.
func WithV2Manager(mgr datastoreV2.Manager) ServerOption {
	return func(s *Server) {
		s.v2Manager = mgr
	}
}

// WithAudioEngine sets the AudioEngine for audio subsystem access.
func WithAudioEngine(e *engine.AudioEngine) ServerOption {
	return func(s *Server) {
		s.engine = e
	}
}

// WithModelManager sets the ModelManager for model gallery operations.
func WithModelManager(mm *classifier.ModelManager) ServerOption {
	return func(s *Server) {
		s.modelManager = mm
	}
}

// WithHealthErrorBuffer sets a shared ErrorRingBuffer that the logger writes to
// and the health checks read from.
func WithHealthErrorBuffer(buf *health.ErrorRingBuffer) ServerOption {
	return func(s *Server) {
		s.healthErrors = buf
	}
}

// New creates a new HTTP server with the given settings and options.
func New(settings *conf.Settings, opts ...ServerOption) (*Server, error) {
	// Create configuration from settings
	config := ConfigFromSettings(settings)
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid server configuration: %w", err)
	}

	// Create the server instance
	s := &Server{
		config:    config,
		settings:  settings,
		startTime: time.Now(),
	}

	// Apply options
	for _, opt := range opts {
		opt(s)
	}

	// Populate valid UI locales from the embedded frontend message files.
	// This must happen before any settings validation that checks locale values.
	conf.SetValidUILocales(conf.DiscoverUILocales(frontend.DistFS))

	// Initialize structured loggers
	s.slogger = GetLogger()                 // Uses Module("api")
	s.securityLogger = security.GetLogger() // Uses Module("security") for the OAuth login flow (issue #3381)

	// Initialize Echo
	s.echo = echo.New()
	s.echo.HideBanner = true
	s.echo.HidePort = true

	// Configure Echo server timeouts
	s.echo.Server.ReadTimeout = config.ReadTimeout
	s.echo.Server.WriteTimeout = config.WriteTimeout
	s.echo.Server.IdleTimeout = config.IdleTimeout

	// Initialize auth service and middleware at server level
	s.initAuth()

	// Setup middleware
	s.setupMiddleware()

	// Setup routes
	if err := s.setupRoutes(); err != nil {
		return nil, fmt.Errorf("failed to setup routes: %w", err)
	}

	s.slogger.Info("HTTP server initialized",
		logger.String("address", config.Address()),
		logger.Bool("tls", config.TLSEnabled),
		logger.Bool("debug", config.Debug),
	)

	return s, nil
}

// initAuth initializes authentication service and middleware at server level.
// This is called before setupRoutes to ensure auth is available for route protection.
func (s *Server) initAuth() {
	if s.oauth2Server == nil {
		s.slogger.Warn("OAuth2Server not provided, authentication not configured")
		return
	}

	// Create auth service adapter (uses centralized logger internally)
	s.authService = auth.NewSecurityAdapter(s.oauth2Server)

	// Create auth middleware (uses centralized logger internally)
	authMw := auth.NewMiddleware(s.authService)
	s.authMiddleware = authMw.Authenticate

	s.slogger.Info("Auth middleware initialized at server level")
}

// setupMiddleware configures the Echo middleware stack.
func (s *Server) setupMiddleware() {
	// HEAD→GET rewrite — runs before routing so HEAD requests match GET routes.
	// Per RFC 9110 §9.3.2, HEAD must return the same status as GET.
	// Go's net/http automatically suppresses the response body for HEAD.
	s.echo.Pre(mw.NewHeadToGet())

	// Base path prefix stripping - enables direct access when basepath is in effect.
	// The effective basepath is resolved per-request via ingressPath(), which checks
	// (in priority order) the X-Ingress-Path header, the X-Forwarded-Prefix header,
	// and the settings.WebServer.BasePath config value. This matches the resolver
	// used by the context middleware below, so a callback URL emitted by the login
	// handler (which prefixes using the same chain) always maps back to a route
	// even when the basepath comes only from a proxy header (no YAML config).
	//
	// Resolving per-request also means settings.WebServer.BasePath changes take
	// effect without a server restart.
	s.echo.Pre(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Read the current *conf.Settings through the atomic pointer so a
			// settings hot-reload (CoW publish via conf.StoreSettings) takes
			// effect for the next request without restart, and so readers
			// never observe a torn view of the basepath field. Fall back to
			// the constructor-provided pointer if no snapshot has been
			// published yet (test harnesses that skip the global publish).
			bp := ingressPath(c, s.currentSettings())
			if bp == "" {
				return next(c)
			}

			// When a reverse proxy header is present, the proxy usually already
			// stripped the prefix before forwarding. Only skip our stripping when
			// the path does NOT start with the basepath; a client (or a proxy that
			// forwards paths verbatim) may send the header with the prefix still
			// attached, in which case we must strip it ourselves.
			req := c.Request()
			hasProxyHeader := req.Header.Get("X-Ingress-Path") != "" || req.Header.Get("X-Forwarded-Prefix") != ""
			if hasProxyHeader && !strings.HasPrefix(req.URL.Path, bp+"/") && req.URL.Path != bp {
				return next(c)
			}

			path := req.URL.Path
			if strings.HasPrefix(path, bp) {
				rest := path[len(bp):]
				if rest == "" || rest[0] == '/' {
					if rest == "" {
						rest = "/"
					}
					req.URL.Path = rest
					// Also update RawPath if set (percent-encoded paths). The
					// decoded bp must be encoded before the prefix check
					// because RawPath is always in escaped form; comparing a
					// decoded prefix against an encoded string breaks for
					// basepaths that require encoding (spaces, non-ASCII),
					// leaving Path stripped and RawPath prefixed. Echo then
					// sees mismatched Path/RawPath and may route inconsistently.
					// hasPercentEncodedPrefix treats %XX hex digits
					// case-insensitively so lowercase-hex-forwarding proxies
					// also match. Fixes Forgejo #447.
					if req.URL.RawPath != "" {
						encodedBP := (&url.URL{Path: bp}).EscapedPath()
						if n := hasPercentEncodedPrefix(req.URL.RawPath, encodedBP); n >= 0 {
							raw := req.URL.RawPath[n:]
							if raw == "" {
								raw = "/"
							}
							req.URL.RawPath = raw
						}
					}
				}
			}
			return next(c)
		}
	})
	if initialBP := strings.TrimRight(s.settings.WebServer.BasePath, "/"); initialBP != "" {
		s.slogger.Info("Base path prefix stripping enabled (hot-reloadable)",
			logger.String("initialBasePath", initialBP))
	} else {
		s.slogger.Debug("Base path prefix stripping middleware installed (no basepath configured yet)")
	}

	// Recovery middleware - should be first
	s.echo.Use(echomw.Recover())

	// Base path context — makes the effective base path available to all handlers
	// via c.Get("basePath"). Computed per-request from ingressPath() which checks
	// proxy headers and config in priority order.
	s.echo.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set("basePath", ingressPath(c, s.currentSettings()))
			return next(c)
		}
	})

	// Request logging using custom middleware package (uses centralized logger)
	s.echo.Use(mw.NewRequestLogger())

	// Security middleware configuration — start from defaults, override server-specific values
	securityConfig := mw.DefaultSecurityConfig()
	securityConfig.AllowedOrigins = s.config.AllowedOrigins
	securityConfig.AllowCredentials = true
	securityConfig.AllowEmbedding = s.config.AllowEmbedding

	// CORS middleware
	s.echo.Use(mw.NewCORS(securityConfig))

	// CSRF protection middleware (uses centralized logger)
	s.echo.Use(mw.NewCSRF(&mw.CSRFConfig{
		SecureCookie: s.config.TLSEnabled,
	}))

	// Refresh CSRF cookie expiration on every API request.
	// Echo v4.15's Sec-Fetch-Site check short-circuits before the built-in
	// cookie refresh code, so the cookie expires after 30 minutes without this.
	s.echo.Use(mw.CSRFCookieRefresh(nil))

	// Body limit middleware
	s.echo.Use(mw.NewBodyLimit(s.config.BodyLimit))

	// Gzip compression (auto-skips SSE endpoints)
	s.echo.Use(mw.NewGzip())

	// Secure headers
	s.echo.Use(mw.NewSecureHeaders(securityConfig))
}

// setupRoutes configures all HTTP routes.
func (s *Server) setupRoutes() error {
	// Health check endpoint at root level
	s.echo.GET("/health", s.healthCheck)

	// Social OAuth routes (Google, GitHub, Microsoft)
	// These must be at /auth/:provider to match frontend expectations
	s.registerOAuthRoutes()

	// Initialize static file server for frontend assets (uses centralized logger)
	s.staticServer = NewStaticFileServer()
	s.staticServer.RegisterRoutes(s.echo)

	// Register PWA routes (manifest and service worker at root paths)
	s.registerPWARoutes()

	// Initialize SPA handler - serves Vite's index.html directly
	// Configuration is fetched by frontend from /api/v2/app/config
	devMode := s.staticServer.IsDevMode()
	devModePath := s.staticServer.DevModePath()
	s.spaHandler = NewSPAHandler(devMode, devModePath)

	s.slogger.Info("Static file server initialized",
		logger.String("mode", s.staticServer.DevModeStatus()),
		logger.String("spa_mode", s.spaHandler.DevModeStatus()),
	)

	// Build the list of v2 controller options.
	v2Opts := []apiv2.Option{
		apiv2.WithAuthMiddleware(s.authMiddleware),
		apiv2.WithAuthService(s.authService),
		apiv2.WithV2Manager(s.v2Manager),
		apiv2.WithMetricsStore(observability.NewMemoryStore(apiv2.MetricsHistoryMaxPoints)),
		apiv2.WithAudioEngine(s.engine),
	}
	if s.modelManager != nil {
		v2Opts = append(v2Opts, apiv2.WithModelManager(s.modelManager))
	}
	if s.healthErrors != nil {
		v2Opts = append(v2Opts, apiv2.WithHealthErrorBuffer(s.healthErrors))
	}

	// Initialize API v2 controller with auth middleware and service injected
	apiController, err := apiv2.New(
		s.echo,
		s.dataStore,
		s.settings,
		s.birdImageCache,
		s.sunCalc,
		s.controlChan,
		s.metrics,
		v2Opts...,
	)
	if err != nil {
		return fmt.Errorf("failed to initialize API v2: %w", err)
	}
	s.apiController = apiController

	// Assign processor to API controller and connect SSE broadcaster
	if s.processor != nil {
		s.apiController.Processor = s.processor
		// Connect SSE broadcaster for real-time detection streaming
		s.processor.SetSSEBroadcaster(s.apiController.BroadcastDetection)
		s.slogger.Debug("SSE broadcaster connected to processor")

		// Connect pending detection broadcaster for "currently hearing" UI
		s.processor.SetPendingBroadcaster(func(snapshot []processor.SSEPendingDetection) {
			s.apiController.BroadcastPending(snapshot)
		})
		s.slogger.Debug("Pending broadcaster connected to processor")
	}

	// Set audio level channel if available
	if s.audioLevelChan != nil {
		s.apiController.SetAudioLevelChan(s.audioLevelChan)
	}

	// Register SPA routes (after API controller for auth middleware access)
	s.registerSPARoutes()

	s.slogger.Info("Routes initialized",
		logger.String("api_version", "v2"),
		logger.String("static_mode", s.staticServer.DevModeStatus()),
	)

	return nil
}

// healthCheck handles the server health check endpoint.
func (s *Server) healthCheck(c echo.Context) error {
	uptime := time.Since(s.startTime)

	return c.JSON(http.StatusOK, map[string]any{
		"status":         "healthy",
		"version":        s.settings.Version,
		"build_date":     s.settings.BuildDate,
		"uptime":         uptime.String(),
		"uptime_seconds": uptime.Seconds(),
		"timestamp":      time.Now().Format(time.RFC3339),
	})
}

// Start begins serving HTTP requests in a background goroutine.
// The server starts asynchronously and returns immediately.
// Use Shutdown() to stop the server.
func (s *Server) Start() {
	go func() {
		if err := s.startBlocking(); err != nil {
			s.slogger.Error("Server error", logger.Error(err))
		}
	}()

	addr := s.config.Address()
	switch {
	case s.config.AutoTLS:
		s.slogger.Info("HTTPS server starting with AutoTLS", logger.String("address", addr))
	case s.config.TLSEnabled:
		s.slogger.Info("HTTPS server starting",
			logger.String("https_address", s.config.TLSAddress()),
			logger.String("http_address", addr),
		)
	default:
		s.slogger.Info("HTTP server starting", logger.String("address", addr))
	}
}

// autoTLSCacheDirName is the subdirectory under the config directory where
// AutoTLS (Let's Encrypt) certificates are cached so they survive restarts.
const autoTLSCacheDirName = "tls-acme"

// startBlocking begins serving HTTP requests and blocks until the server is shut down.
func (s *Server) startBlocking() error {
	addr := s.config.Address()

	s.slogger.Info("Starting HTTP server", logger.String("address", addr))

	var err error
	switch {
	case s.config.AutoTLS:
		// Configure persistent cert cache so certificates survive restarts.
		// Without this, Echo's AutoTLSManager has no storage backend and certs
		// are lost on every shutdown, triggering a fresh ACME request each time
		// and quickly exhausting Let's Encrypt's rate limit.
		if configFile, pathErr := conf.FindConfigFile(); pathErr == nil {
			cacheDir := filepath.Join(filepath.Dir(configFile), autoTLSCacheDirName)
			s.echo.AutoTLSManager.Cache = autocert.DirCache(cacheDir)
			s.slogger.Info("AutoTLS certificate cache configured", logger.String("path", cacheDir))
		} else {
			s.slogger.Warn("Could not determine config path for AutoTLS cache; certificates will not persist across restarts",
				logger.Error(pathErr))
		}
		// Restrict ACME issuance to the configured hostname. A nil HostPolicy
		// lets autocert request a certificate for any SNI presented, which on a
		// public host is a Let's Encrypt rate-limit exhaustion vector. Config
		// validation (validateTLSMode) already requires a hostname for AutoTLS,
		// so an empty value here means validation was bypassed: fail closed
		// rather than start with an open policy.
		host := s.settings.Security.GetHostnameForCertificates()
		if host == "" {
			return errors.Newf("AutoTLS enabled but no hostname configured; set security.host or a hostname in security.baseURL").
				Category(errors.CategoryValidation).
				Context("operation", "configure-autotls-host-policy").
				Build()
		}
		s.echo.AutoTLSManager.HostPolicy = autocert.HostWhitelist(host)

		// Start HTTP server on the configured port for ACME HTTP-01 challenges.
		// The fallback handler determines what happens to non-ACME traffic:
		// - RedirectToHTTPS=false: serve the app over plain HTTP
		// - RedirectToHTTPS=true + port 443: nil lets autocert redirect (no port in URL)
		// - RedirectToHTTPS=true + custom port: custom handler includes port in redirect
		var httpFallback http.Handler
		if !s.config.RedirectToHTTPS {
			httpFallback = s.echo
		} else if s.config.TLSPort != "443" {
			tlsPort := s.config.TLSPort
			httpFallback = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				host := r.Host
				if h, _, err := net.SplitHostPort(host); err == nil {
					host = h
				}
				host = strings.TrimRight(strings.TrimLeft(host, "["), "]")
				target := "https://" + net.JoinHostPort(host, tlsPort) + r.URL.RequestURI()
				http.Redirect(w, r, target, http.StatusPermanentRedirect)
			})
		}
		s.httpRedirectServer = &http.Server{
			Addr:         addr,
			Handler:      s.echo.AutoTLSManager.HTTPHandler(httpFallback),
			ReadTimeout:  s.config.ReadTimeout,
			WriteTimeout: s.config.WriteTimeout,
		}

		// Pre-bind the HTTP listener so ACME challenges work. Fail fast if the
		// port is unavailable rather than discovering it asynchronously.
		httpLn, listenErr := net.Listen("tcp", addr)
		if listenErr != nil {
			return fmt.Errorf("AutoTLS: cannot bind HTTP listener on %s (required for ACME HTTP-01 challenges): %w", addr, listenErr)
		}
		go s.serveHTTPOnListener(httpLn)

		// Start HTTPS server on TLS port (this blocks)
		tlsAddr := s.config.TLSAddress()
		s.slogger.Info("Starting with AutoTLS (Let's Encrypt)",
			logger.String("https_address", tlsAddr),
			logger.String("http_address", addr),
		)
		err = s.echo.StartAutoTLS(tlsAddr)
		if err != nil && s.httpRedirectServer != nil {
			_ = s.httpRedirectServer.Close()
		}
	case s.config.TLSEnabled:
		// Manual/Self-signed TLS: HTTPS on TLSPort, HTTP stays on configured port
		tlsAddr := s.config.TLSAddress()
		s.slogger.Info("Starting with manual TLS",
			logger.String("https_address", tlsAddr),
			logger.String("http_address", addr),
			logger.String("cert", s.config.TLSCertFile),
			logger.String("key", s.config.TLSKeyFile),
		)

		// Start HTTP redirect server on the regular port if configured
		if s.config.RedirectToHTTPS {
			s.httpRedirectServer = s.newHTTPRedirectServer(addr, s.config.TLSPort)
			go s.serveHTTPRedirect()
		}

		// Start HTTPS server on TLS port (this blocks)
		err = s.echo.StartTLS(tlsAddr, s.config.TLSCertFile, s.config.TLSKeyFile)
		// If HTTPS startup failed, shut down the redirect server
		if err != nil && s.httpRedirectServer != nil {
			_ = s.httpRedirectServer.Close()
		}
	default:
		// Plain HTTP
		err = s.echo.Start(addr)
	}

	if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("server error: %w", err)
	}

	return nil
}

// StartWithGracefulShutdown starts the server and handles graceful shutdown on SIGINT/SIGTERM.
func (s *Server) StartWithGracefulShutdown() error {
	// Start server in goroutine
	s.Start()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	s.slogger.Info("Shutdown signal received, initiating graceful shutdown")

	ctx, cancel := context.WithTimeout(context.Background(), s.config.ShutdownTimeout)
	defer cancel()

	return s.ShutdownWithContext(ctx)
}

// ShutdownWithContext gracefully stops the server using the provided context
// for timeout control. Use this when the caller manages the shutdown budget.
func (s *Server) ShutdownWithContext(ctx context.Context) error {
	// Disable keep-alives immediately so idle connections close and no new
	// keep-alive connections are accepted. This starts draining while the
	// controller is still shutting down its background goroutines.
	s.echo.Server.SetKeepAlivesEnabled(false)

	// Close the TCP listener so no new connections are accepted and existing
	// idle connections receive RST. This allows connections to drain in
	// parallel with the controller shutdown below.
	if s.echo.Listener != nil {
		if err := s.echo.Listener.Close(); err != nil {
			s.slogger.Warn("Error closing listener during shutdown", logger.Error(err))
		}
	}

	// Shutdown HTTP redirect server if running
	if s.httpRedirectServer != nil {
		if err := s.httpRedirectServer.Shutdown(ctx); err != nil && !errors.Is(err, net.ErrClosed) {
			s.slogger.Warn("Error shutting down HTTP redirect server", logger.Error(err))
		}
	}

	// Shutdown API controller (waits for its background goroutines)
	if s.apiController != nil {
		s.apiController.Shutdown()
	}

	// Shutdown Echo server — since the listener is already closed and SSE
	// clients are disconnected, this should complete quickly.
	if err := s.echo.Shutdown(ctx); err != nil {
		// Ignore "use of closed network connection" since we closed the listener above
		if !errors.Is(err, net.ErrClosed) {
			s.slogger.Error("Error during server shutdown", logger.Error(err))
			return fmt.Errorf("shutdown error: %w", err)
		}
	}

	// Log completion and flush
	s.slogger.Info("Server shutdown complete")

	// Flush logger to ensure all messages are written
	if err := s.slogger.Flush(); err != nil {
		fmt.Printf("Error flushing log: %v\n", err)
	}

	return nil
}

// newHTTPRedirectServer creates an HTTP server that redirects all requests to HTTPS.
// The server is created synchronously to avoid a race between assignment and shutdown.
func (s *Server) newHTTPRedirectServer(httpAddr, tlsPort string) *http.Server {
	if tlsPort == "" {
		tlsPort = "8443"
	}

	redirectHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			host = h
		}
		host = strings.TrimRight(strings.TrimLeft(host, "["), "]")
		var hostPort string
		if tlsPort == "443" {
			hostPort = host
			if strings.Contains(host, ":") {
				hostPort = "[" + host + "]"
			}
		} else {
			hostPort = net.JoinHostPort(host, tlsPort)
		}
		target := "https://" + hostPort + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
	})

	return &http.Server{
		Addr:         httpAddr,
		Handler:      redirectHandler,
		ReadTimeout:  s.config.ReadTimeout,
		WriteTimeout: s.config.WriteTimeout,
	}
}

// serveHTTPRedirect starts listening on the HTTP redirect server.
// Must be called in a goroutine after newHTTPRedirectServer.
func (s *Server) serveHTTPRedirect() {
	s.slogger.Info("Starting HTTP->HTTPS redirect server",
		logger.String("http_address", s.httpRedirectServer.Addr),
	)

	if err := s.httpRedirectServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		s.slogger.Error("HTTP redirect server error", logger.Error(err))
	}
}

// serveHTTPOnListener serves the HTTP redirect/ACME server on a pre-bound listener.
func (s *Server) serveHTTPOnListener(ln net.Listener) {
	s.slogger.Info("Starting HTTP->HTTPS redirect server",
		logger.String("http_address", s.httpRedirectServer.Addr),
	)

	if err := s.httpRedirectServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		s.slogger.Error("HTTP redirect server error", logger.Error(err))
	}
}

// APIController returns the v2 API controller for SSE broadcasting and other features.
func (s *Server) APIController() *apiv2.Controller {
	return s.apiController
}

// Echo returns the underlying Echo instance.
// This is useful for testing or advanced configuration.
func (s *Server) Echo() *echo.Echo {
	return s.echo
}

// StaticServer returns the static file server.
func (s *Server) StaticServer() *StaticFileServer {
	return s.staticServer
}

// IsDevMode returns whether the frontend is running in dev mode.
func (s *Server) IsDevMode() bool {
	if s.staticServer != nil {
		return s.staticServer.IsDevMode()
	}
	return false
}

// registerSPARoutes registers all frontend SPA routes.
// These routes serve the HTML shell that loads the Svelte application.
// All SPA routes are public so the SPA can bootstrap and render the login
// form when Security.PrivateMode is enabled. The data layer (v2 API
// endpoints) is the actual security boundary: it is gated by
// privateModeAuth in api/v2/api.go and returns 401 to unauthenticated
// requests in private mode, prompting the SPA to render the login form.
// Settings and system routes remain auth-gated at this layer too so deep
// links to those pages still server-side redirect to /login for unauth
// browser navigations.
func (s *Server) registerSPARoutes() {
	// Redirect root path to dashboard
	s.echo.GET("/", func(c echo.Context) error {
		return c.Redirect(http.StatusFound, ingressPath(c, s.currentSettings())+"/ui/dashboard")
	})

	authMiddleware := s.getAuthMiddleware()

	// Public SPA shell routes — the SPA itself decides what to render
	// based on Security.PrivateMode and the authenticated/guest state
	// returned by /api/v2/app/config.
	publicRoutes := []string{
		"/login",
		"/ui",
		"/ui/",
		"/ui/dashboard",
		"/ui/detections",
		"/ui/detections/:id",
		"/ui/notifications",
		"/ui/analytics",
		"/ui/analytics/species",
		"/ui/analytics/advanced",
		"/ui/search",
		"/ui/about",
	}
	for _, route := range publicRoutes {
		s.echo.GET(route, s.spaHandler.ServeApp)
	}

	// Settings and system routes are always protected at the SPA layer
	// so direct deep links from a browser hit the auth middleware (which
	// redirects to /login) instead of loading the SPA shell.
	protectedRoutes := []string{
		"/ui/system",
		"/ui/settings",
		"/ui/settings/main",
		"/ui/settings/audio",
		"/ui/settings/detectionfilters",
		"/ui/settings/integrations",
		"/ui/settings/notifications",
		"/ui/settings/security",
		"/ui/settings/species",
		"/ui/settings/support",
		"/ui/settings/userinterface",
	}
	for _, route := range protectedRoutes {
		if authMiddleware != nil {
			s.echo.GET(route, s.spaHandler.ServeApp, authMiddleware)
		} else {
			s.echo.GET(route, s.spaHandler.ServeApp)
		}
	}

	// Protected catch-all for settings routes
	if authMiddleware != nil {
		s.echo.GET("/ui/settings/*", s.spaHandler.ServeApp, authMiddleware)
	} else {
		s.echo.GET("/ui/settings/*", s.spaHandler.ServeApp)
	}

	// Public catch-all for any other unmatched /ui/* paths so the SPA can
	// handle client-side routing for unknown routes consistently.
	s.echo.GET("/ui/*", s.spaHandler.ServeApp)

	s.slogger.Debug("SPA routes registered",
		logger.Bool("auth_enabled", authMiddleware != nil),
	)
}

// getAuthMiddleware returns the authentication middleware owned by the server.
func (s *Server) getAuthMiddleware() echo.MiddlewareFunc {
	return s.authMiddleware
}

// securityLog returns the logger scoped to the "security" module. The OAuth
// login flow uses it so authentication events land in logs/security.log next to
// the provider-init logging, instead of logs/api.log where admins do not think
// to look (issue #3381). The detail it records is for server-side diagnostics
// only and must never be reflected back to unauthenticated visitors.
//
// It returns the cached securityLogger (set in the constructor, matching how
// s.slogger is cached) and falls back to a fresh module logger when the Server
// was built without the constructor (e.g. in unit tests).
func (s *Server) securityLog() logger.Logger {
	if s.securityLogger != nil {
		return s.securityLogger
	}
	return security.GetLogger()
}

// registerOAuthRoutes registers social OAuth provider routes.
// These routes handle OAuth flow for Google, GitHub, and Microsoft providers.
func (s *Server) registerOAuthRoutes() {
	// GET /auth/:provider - Initiates OAuth flow with the provider
	s.echo.GET("/auth/:provider", s.handleOAuthBegin)

	// GET /auth/:provider/callback - Handles OAuth callback from provider
	s.echo.GET("/auth/:provider/callback", s.handleOAuthCallback)

	s.slogger.Debug("OAuth routes registered",
		logger.String("begin_route", "/auth/:provider"),
		logger.String("callback_route", "/auth/:provider/callback"),
	)
}

// handleOAuthBegin initiates the OAuth flow with the requested provider.
// GET /auth/:provider
func (s *Server) handleOAuthBegin(c echo.Context) error {
	provider := c.Param("provider")
	log := s.securityLog().With(
		logger.String("provider", provider),
		logger.String("ip", c.RealIP()),
	)

	log.Info("OAuth login begin request")

	// Validate provider is one we support
	if !s.isValidOAuthProvider(provider) {
		log.Warn("OAuth login rejected: unsupported or unrecognized provider requested")
		return c.String(http.StatusBadRequest, "Invalid OAuth provider")
	}

	// Reject providers that are not currently enabled and fully configured before
	// sending the user on the provider round-trip, so a disabled or half-configured
	// provider fails fast with a clear security.log entry here instead of an opaque
	// error after the redirect (issue #3381). The visitor only sees a generic message.
	configProvider := s.configProviderFor(provider)
	if settings := s.currentSettings(); settings == nil || configProvider == "" || !settings.IsOAuthProviderEnabled(configProvider) {
		log.Warn("OAuth login rejected: provider is disabled or not fully configured")
		return c.String(http.StatusBadRequest, "OAuth provider not available")
	}

	// Add provider to request context for gothic
	req := gothic.GetContextWithProvider(c.Request(), provider)

	// Try to complete auth if user already has a valid session
	if user, err := gothic.CompleteUserAuth(c.Response(), req); err == nil {
		log.Info("OAuth login: user already authenticated, redirecting to dashboard",
			logger.Username(oauthIdentity(user.Email, user.UserID)),
		)
		// User is already authenticated, redirect to dashboard
		return c.Redirect(http.StatusFound, ingressPath(c, s.currentSettings())+"/ui/dashboard")
	}

	// Begin OAuth flow - this will redirect to the provider
	log.Debug("Redirecting to OAuth provider to begin authentication")
	gothic.BeginAuthHandler(c.Response(), req)
	return nil
}

// handleOAuthCallback handles the OAuth callback from the provider.
// GET /auth/:provider/callback
func (s *Server) handleOAuthCallback(c echo.Context) error {
	provider := c.Param("provider")
	log := s.securityLog().With(
		logger.String("provider", provider),
		logger.String("ip", c.RealIP()),
	)

	log.Info("OAuth login callback received")

	// Add provider to request context for gothic
	req := gothic.GetContextWithProvider(c.Request(), provider)

	// Complete the OAuth flow and get user info
	user, err := gothic.CompleteUserAuth(c.Response(), req)
	if err != nil {
		// Log the detailed provider/token error for the admin, but return a
		// generic message so internal details are not exposed to the visitor.
		log.Error("OAuth login failed: could not complete authentication with provider",
			logger.Error(err),
		)
		return c.String(http.StatusUnauthorized, "Authentication failed")
	}

	// Log a hashed identity (logger.Username) rather than the raw email/name so
	// security.log stays free of plaintext PII while still allowing an admin to
	// correlate repeated attempts by the same user (matches the basic-auth flow).
	log.Info("OAuth provider authentication succeeded, checking authorization",
		logger.Username(oauthIdentity(user.Email, user.UserID)),
	)

	// Validate user is allowed (check against configured allowed user IDs).
	// The deny reason is for server-side diagnostics only; the response to the
	// visitor stays generic so configuration problems are not disclosed to
	// unauthenticated users (issue #3381).
	if allowed, reason := s.isAllowedOAuthUser(provider, user.UserID, user.Email); !allowed {
		log.Warn("OAuth login denied: user not authorized",
			logger.String("reason", string(reason)),
			logger.Username(oauthIdentity(user.Email, user.UserID)),
		)
		if reason == oauthDenyAllowlistEmpty {
			// The single most common misconfiguration (issue #3381): the provider
			// is enabled but has no allowed users, so nobody can ever log in.
			log.Warn("OAuth provider has no allowed users configured; no one can log in until at least one allowed user or email is added under Settings > Security > Authentication")
		}
		return c.String(http.StatusForbidden, "Access denied: user not authorized")
	}

	// Persist the authenticated session. The userId, provider, and active-provider
	// keys are read by the auth middleware on every subsequent request, so a write
	// failure here means the redirect would land on a page that immediately bounces
	// the user back. Fail fast with a generic error instead of redirecting into a
	// broken session.
	userId := oauthIdentity(user.Email, user.UserID)

	if err := gothic.StoreInSession("userId", userId, req, c.Response()); err != nil {
		log.Error("OAuth login failed: could not persist userId in session", logger.Error(err))
		return c.String(http.StatusInternalServerError, "Authentication failed")
	}

	// Store provider name in session for later reference
	if err := gothic.StoreInSession(provider, user.UserID, req, c.Response()); err != nil {
		log.Error("OAuth login failed: could not persist provider session", logger.Error(err))
		return c.String(http.StatusInternalServerError, "Authentication failed")
	}

	// Store active provider name for direct lookup (avoids iterating all providers)
	if err := gothic.StoreInSession(security.SessionKeyAuthProvider, provider, req, c.Response()); err != nil {
		log.Error("OAuth login failed: could not persist active auth provider in session", logger.Error(err))
		return c.String(http.StatusInternalServerError, "Authentication failed")
	}

	// Store or clear ID token for RP-Initiated Logout (OIDC providers). Best-effort:
	// a failure here does not break authentication, only later RP-initiated logout.
	if user.IDToken != "" {
		if err := gothic.StoreInSession("id_token", user.IDToken, req, c.Response()); err != nil {
			log.Error("Failed to store ID token in session", logger.Error(err))
		}
	} else {
		// Clear stale ID token to prevent reuse across auth flows
		if err := gothic.StoreInSession("id_token", "", req, c.Response()); err != nil {
			log.Error("Failed to clear ID token in session", logger.Error(err))
		}
	}

	log.Info("OAuth login successful, session established, redirecting to dashboard",
		logger.Username(userId),
	)

	// Redirect to dashboard after successful authentication
	return c.Redirect(http.StatusFound, ingressPath(c, s.currentSettings())+"/ui/dashboard")
}

// isValidOAuthProvider checks if the provider name is a valid goth provider name.
// Uses the ConfigToGothProvider map to derive valid providers dynamically.
func (s *Server) isValidOAuthProvider(provider string) bool {
	for _, gothName := range security.ConfigToGothProvider {
		if strings.EqualFold(provider, gothName) {
			return true
		}
	}
	return false
}

// configProviderFor maps a goth provider name back to its config provider ID
// (the reverse of ConfigToGothProvider), or returns "" when the goth name is not
// recognized. The match is case-insensitive to tolerate proxies or clients that
// alter the path casing.
func (s *Server) configProviderFor(gothProvider string) string {
	for cfg, gothName := range security.ConfigToGothProvider {
		if strings.EqualFold(gothName, gothProvider) {
			return cfg
		}
	}
	return ""
}

// oauthIdentity returns the identifier used both as the session userId and as
// the hashed identity logged to security.log: the email when present, else the
// provider's opaque user ID.
func oauthIdentity(email, userID string) string {
	if email != "" {
		return email
	}
	return userID
}

// oauthDenyReason classifies why an OAuth user was not authorized to log in.
// It is recorded in logs/security.log for admin-side diagnostics only and is
// never returned to the client, so it cannot disclose configuration problems to
// unauthenticated visitors (issue #3381).
type oauthDenyReason string

const (
	// oauthAuthorized indicates the user is allowed (no denial).
	oauthAuthorized oauthDenyReason = ""
	// oauthDenySettingsMissing: the live settings snapshot was unavailable.
	oauthDenySettingsMissing oauthDenyReason = "settings_unavailable"
	// oauthDenyProviderUnknown: the goth provider name is not in the config map.
	oauthDenyProviderUnknown oauthDenyReason = "provider_not_recognized"
	// oauthDenyProviderMissing: no matching provider entry exists in settings.
	oauthDenyProviderMissing oauthDenyReason = "provider_not_configured"
	// oauthDenyProviderDisabled: the provider exists but is disabled.
	oauthDenyProviderDisabled oauthDenyReason = "provider_disabled"
	// oauthDenyAllowlistEmpty: the provider is enabled but has no allowed users,
	// so nobody can ever log in. This is the most common misconfiguration.
	oauthDenyAllowlistEmpty oauthDenyReason = "allowed_users_empty"
	// oauthDenyUserNotAllowed: the user is not present in a non-empty allowlist.
	oauthDenyUserNotAllowed oauthDenyReason = "user_not_in_allowed_list"
)

// isAllowedOAuthUser reports whether the authenticated OAuth user may log in,
// together with an oauthDenyReason describing the cause when access is denied.
// It uses the OAuthProviders array with a reverse lookup from the goth provider
// name to the config provider ID. The reason is for server-side logging only;
// callers must keep the response to the client generic (issue #3381).
func (s *Server) isAllowedOAuthUser(gothProvider, userID, email string) (allowed bool, reason oauthDenyReason) {
	// Read the live snapshot so changes to the allowed OAuth users (or a
	// provider's enabled flag) made through the UI apply without a restart
	// (issue #3370).
	settings := s.currentSettings()
	if settings == nil {
		return false, oauthDenySettingsMissing
	}

	configProvider := s.configProviderFor(gothProvider)
	if configProvider == "" {
		return false, oauthDenyProviderUnknown
	}

	provider := settings.GetOAuthProvider(configProvider)
	if provider == nil {
		return false, oauthDenyProviderMissing
	}
	if !provider.Enabled {
		return false, oauthDenyProviderDisabled
	}

	// Walk the configured allowlist. Track whether any non-empty entry exists so
	// an empty allowlist (a misconfiguration where nobody can log in) is reported
	// distinctly from a genuine non-match.
	hasAllowedEntry := false
	for allowed := range strings.SplitSeq(provider.UserID, ",") {
		trimmed := strings.TrimSpace(allowed)
		if trimmed == "" {
			continue
		}
		hasAllowedEntry = true
		if strings.EqualFold(trimmed, userID) || strings.EqualFold(trimmed, email) {
			return true, oauthAuthorized
		}
	}
	if !hasAllowedEntry {
		return false, oauthDenyAllowlistEmpty
	}
	return false, oauthDenyUserNotAllowed
}
