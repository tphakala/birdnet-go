package api

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	mw "github.com/tphakala/birdnet-go/internal/api/middleware"
	v2 "github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/security"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// AssetsFs holds the embedded assets filesystem (sounds, images, etc.).
// This is set by main.go before starting the server.
var AssetsFs embed.FS

// Server is the main HTTP server for BirdNET-Go.
// It manages the Echo framework instance, middleware, and all HTTP routes.
type Server struct {
	// Core components
	echo     *echo.Echo
	config   *Config
	settings *conf.Settings
	logger   *log.Logger
	slogger  *slog.Logger
	levelVar *slog.LevelVar

	// Dependencies
	dataStore      datastore.Interface
	birdImageCache *imageprovider.BirdImageCache
	sunCalc        *suncalc.SunCalc
	processor      *processor.Processor
	oauth2Server   *security.OAuth2Server
	metrics        *observability.Metrics

	// Channels
	controlChan    chan string
	audioLevelChan chan myaudio.AudioLevelData

	// API controller
	apiController *v2.Controller

	// Static file serving
	staticServer *StaticFileServer
	spaHandler   *SPAHandler
	assetsFS     fs.FS // Embedded assets filesystem (sounds, images, etc.)

	// Lifecycle management
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	startTime time.Time

	// Cleanup
	logCloser func() error
}

// ServerOption is a functional option for configuring the Server.
type ServerOption func(*Server)

// WithLogger sets the standard logger for the server.
func WithLogger(logger *log.Logger) ServerOption {
	return func(s *Server) {
		s.logger = logger
	}
}

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
func WithAudioLevelChannel(ch chan myaudio.AudioLevelData) ServerOption {
	return func(s *Server) {
		s.audioLevelChan = ch
	}
}

// WithAssetsFS sets the embedded assets filesystem for serving static assets.
// This is used to serve files from /assets/* (sounds, images, etc.).
func WithAssetsFS(assets embed.FS) ServerOption {
	return func(s *Server) {
		// Extract the "assets" subdirectory from the embedded FS
		subFS, err := fs.Sub(assets, "assets")
		if err != nil {
			// If extraction fails, use the root FS
			s.assetsFS = assets
		} else {
			s.assetsFS = subFS
		}
	}
}

// New creates a new HTTP server with the given settings and options.
func New(settings *conf.Settings, opts ...ServerOption) (*Server, error) {
	// Create configuration from settings
	config := ConfigFromSettings(settings)
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid server configuration: %w", err)
	}

	// Create context for lifecycle management
	ctx, cancel := context.WithCancel(context.Background())

	// Create the server instance
	s := &Server{
		config:    config,
		settings:  settings,
		ctx:       ctx,
		cancel:    cancel,
		startTime: time.Now(),
	}

	// Apply options
	for _, opt := range opts {
		opt(s)
	}

	// Set default logger if not provided
	if s.logger == nil {
		s.logger = log.Default()
	}

	// Initialize structured logger
	if err := s.initLogger(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to initialize logger: %w", err)
	}

	// Initialize Echo
	s.echo = echo.New()
	s.echo.HideBanner = true
	s.echo.HidePort = true

	// Configure Echo server timeouts
	s.echo.Server.ReadTimeout = config.ReadTimeout
	s.echo.Server.WriteTimeout = config.WriteTimeout
	s.echo.Server.IdleTimeout = config.IdleTimeout

	// Setup middleware
	s.setupMiddleware()

	// Setup routes
	if err := s.setupRoutes(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to setup routes: %w", err)
	}

	s.slogger.Info("HTTP server initialized",
		"address", config.Address(),
		"tls", config.TLSEnabled,
		"debug", config.Debug,
	)

	return s, nil
}

// initLogger initializes the structured logger for the server.
func (s *Server) initLogger() error {
	s.levelVar = new(slog.LevelVar)
	s.levelVar.Set(s.config.LogLevel)

	logPath := "logs/server.log"
	logger, closer, err := logging.NewFileLogger(logPath, "server", s.levelVar)
	if err != nil {
		// Fallback to discard logger
		s.logger.Printf("Warning: Failed to initialize server logger: %v", err)
		handler := slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: s.levelVar})
		s.slogger = slog.New(handler).With("service", "server")
		s.logCloser = func() error { return nil }
		return nil
	}

	s.slogger = logger
	s.logCloser = closer
	s.logger.Printf("Server logging initialized to %s", logPath)
	return nil
}

// setupMiddleware configures the Echo middleware stack.
func (s *Server) setupMiddleware() {
	// Recovery middleware - should be first
	s.echo.Use(echomw.Recover())

	// Request logging using custom middleware package
	s.echo.Use(mw.NewRequestLogger(s.slogger))

	// Security middleware configuration
	securityConfig := mw.SecurityConfig{
		AllowedOrigins:        s.config.AllowedOrigins,
		AllowCredentials:      true,
		HSTSMaxAge:            mw.HSTSMaxAge,
		HSTSExcludeSubdomains: false,
		ContentSecurityPolicy: "",
	}

	// CORS middleware
	s.echo.Use(mw.NewCORS(securityConfig))

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

	// Initialize static file server for frontend assets
	s.staticServer = NewStaticFileServer(s.slogger, s.assetsFS)
	s.staticServer.RegisterRoutes(s.echo)

	// Initialize SPA handler (routes registered after API controller)
	s.spaHandler = NewSPAHandler(s.settings)

	s.slogger.Info("Static file server initialized",
		"mode", s.staticServer.DevModeStatus(),
	)

	// Initialize API v2 controller
	apiController, err := v2.New(
		s.echo,
		s.dataStore,
		s.settings,
		s.birdImageCache,
		s.sunCalc,
		s.controlChan,
		s.logger,
		s.oauth2Server,
		s.metrics,
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
	}

	// Set audio level channel if available
	if s.audioLevelChan != nil {
		s.apiController.SetAudioLevelChan(s.audioLevelChan)
	}

	// Register SPA routes (after API controller for auth middleware access)
	s.registerSPARoutes()

	s.slogger.Info("Routes initialized",
		"api_version", "v2",
		"static_mode", s.staticServer.DevModeStatus(),
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
// This implements the httpserver.Server interface and returns immediately.
// Use Shutdown() to stop the server.
func (s *Server) Start() {
	go func() {
		if err := s.startBlocking(); err != nil {
			s.slogger.Error("Server error", "error", err)
		}
	}()

	addr := s.config.Address()
	switch {
	case s.config.AutoTLS:
		s.logger.Printf("🌐 HTTPS server starting with AutoTLS on %s", addr)
	case s.config.TLSEnabled:
		s.logger.Printf("🌐 HTTPS server starting on %s", addr)
	default:
		s.logger.Printf("🌐 HTTP server starting on %s", addr)
	}
}

// startBlocking begins serving HTTP requests and blocks until the server is shut down.
func (s *Server) startBlocking() error {
	addr := s.config.Address()

	s.slogger.Info("Starting HTTP server", "address", addr)

	var err error
	switch {
	case s.config.AutoTLS:
		// AutoTLS with Let's Encrypt
		s.slogger.Info("Starting with AutoTLS (Let's Encrypt)")
		err = s.echo.StartAutoTLS(addr)
	case s.config.TLSEnabled:
		// Manual TLS
		s.slogger.Info("Starting with manual TLS",
			"cert", s.config.TLSCertFile,
			"key", s.config.TLSKeyFile,
		)
		err = s.echo.StartTLS(addr, s.config.TLSCertFile, s.config.TLSKeyFile)
	default:
		// Plain HTTP
		err = s.echo.Start(addr)
	}

	if err != nil && !errors.Is(err, http.ErrServerClosed) {
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

	s.slogger.Info("Shutdown signal received, initiating graceful shutdown...")
	s.logger.Println("🛑 Shutdown signal received")

	return s.Shutdown()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown() error {
	// Cancel context to signal goroutines
	s.cancel()

	// Create shutdown context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), s.config.ShutdownTimeout)
	defer cancel()

	// Shutdown API controller first
	if s.apiController != nil {
		s.apiController.Shutdown()
	}

	// Shutdown Echo server
	if err := s.echo.Shutdown(ctx); err != nil {
		s.slogger.Error("Error during server shutdown", "error", err)
		return fmt.Errorf("shutdown error: %w", err)
	}

	// Wait for goroutines to finish
	s.wg.Wait()

	// Close logger
	if s.logCloser != nil {
		if err := s.logCloser(); err != nil {
			s.logger.Printf("Error closing log file: %v", err)
		}
	}

	s.slogger.Info("Server shutdown complete")
	s.logger.Println("✅ Server shutdown complete")

	return nil
}

// APIController returns the v2 API controller for SSE broadcasting and other features.
// This implements the httpserver.Server interface.
func (s *Server) APIController() *v2.Controller {
	return s.apiController
}

// Echo returns the underlying Echo instance.
// This is useful for testing or advanced configuration.
func (s *Server) Echo() *echo.Echo {
	return s.echo
}

// SetLogLevel dynamically changes the logging level.
func (s *Server) SetLogLevel(level slog.Level) {
	if s.levelVar != nil {
		s.levelVar.Set(level)
		s.slogger.Info("Log level changed", "level", level.String())
	}
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
func (s *Server) registerSPARoutes() {
	// Public routes (no authentication required)
	publicRoutes := []string{
		"/",
		"/login",
		"/ui",
		"/ui/",
		"/ui/dashboard",
		"/ui/detections",
		"/ui/notifications",
		"/ui/analytics",
		"/ui/analytics/species",
		"/ui/analytics/advanced",
		"/ui/search",
		"/ui/about",
	}

	// Public dynamic routes (with path parameters)
	publicDynamicRoutes := []string{
		"/ui/detections/:id", // Detection detail page
	}

	for _, route := range publicRoutes {
		s.echo.GET(route, s.spaHandler.ServeApp)
	}

	for _, route := range publicDynamicRoutes {
		s.echo.GET(route, s.spaHandler.ServeApp)
	}

	// Protected routes (authentication required)
	// These will be protected by the API v2 auth middleware
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

	// Get the auth middleware from API controller if available
	authMiddleware := s.getAuthMiddleware()

	for _, route := range protectedRoutes {
		if authMiddleware != nil {
			s.echo.GET(route, s.spaHandler.ServeApp, authMiddleware)
		} else {
			s.echo.GET(route, s.spaHandler.ServeApp)
		}
	}

	// Catch-all route for any unmatched /ui/* paths
	// This ensures SPA handles client-side routing for unknown routes
	// Must be registered last to not override specific routes
	s.echo.GET("/ui/*", s.spaHandler.ServeApp)

	s.slogger.Debug("SPA routes registered",
		"public_routes", len(publicRoutes),
		"public_dynamic_routes", len(publicDynamicRoutes),
		"protected_routes", len(protectedRoutes),
	)
}

// getAuthMiddleware returns the authentication middleware from the API controller.
func (s *Server) getAuthMiddleware() echo.MiddlewareFunc {
	if s.apiController != nil {
		return s.apiController.AuthMiddleware
	}
	return nil
}
