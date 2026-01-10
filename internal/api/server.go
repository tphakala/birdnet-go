package api

import (
	"context"
	"embed"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"
	"github.com/markbates/goth/gothic"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/api/auth"
	mw "github.com/tphakala/birdnet-go/internal/api/middleware"
	apiv2 "github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/security"
	"github.com/tphakala/birdnet-go/internal/suncalc"
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
	echo     *echo.Echo
	config   *Config
	settings *conf.Settings
	slogger  logger.Logger // Centralized structured logger

	// Dependencies
	dataStore      datastore.Interface
	birdImageCache *imageprovider.BirdImageCache
	sunCalc        *suncalc.SunCalc
	processor      *processor.Processor
	oauth2Server   *security.OAuth2Server
	metrics        *observability.Metrics

	// Auth components (owned by server, injected into controllers)
	authService    auth.Service
	authMiddleware echo.MiddlewareFunc

	// Channels
	controlChan    chan string
	audioLevelChan chan myaudio.AudioLevelData

	// API controller
	apiController *apiv2.Controller

	// Static file serving
	staticServer *StaticFileServer
	spaHandler   *SPAHandler

	// Lifecycle management
	startTime time.Time
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
func WithAudioLevelChannel(ch chan myaudio.AudioLevelData) ServerOption {
	return func(s *Server) {
		s.audioLevelChan = ch
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

	// Initialize structured logger
	s.slogger = GetLogger() // Uses Module("api")

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
	// Recovery middleware - should be first
	s.echo.Use(echomw.Recover())

	// Request logging using custom middleware package (uses centralized logger)
	s.echo.Use(mw.NewRequestLogger())

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

	// CSRF protection middleware (uses centralized logger)
	s.echo.Use(mw.NewCSRF(nil))

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

	// Initialize SPA handler - serves Vite's index.html directly
	// Configuration is fetched by frontend from /api/v2/app/config
	devMode := s.staticServer.IsDevMode()
	devModePath := s.staticServer.DevModePath()
	s.spaHandler = NewSPAHandler(devMode, devModePath)

	s.slogger.Info("Static file server initialized",
		logger.String("mode", s.staticServer.DevModeStatus()),
		logger.String("spa_mode", s.spaHandler.DevModeStatus()),
	)

	// Initialize API v2 controller with auth middleware and service injected
	apiController, err := apiv2.New(
		s.echo,
		s.dataStore,
		s.settings,
		s.birdImageCache,
		s.sunCalc,
		s.controlChan,
		s.metrics,
		apiv2.WithAuthMiddleware(s.authMiddleware),
		apiv2.WithAuthService(s.authService),
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
		s.slogger.Info("HTTPS server starting", logger.String("address", addr))
	default:
		s.slogger.Info("HTTP server starting", logger.String("address", addr))
	}
}

// startBlocking begins serving HTTP requests and blocks until the server is shut down.
func (s *Server) startBlocking() error {
	addr := s.config.Address()

	s.slogger.Info("Starting HTTP server", logger.String("address", addr))

	var err error
	switch {
	case s.config.AutoTLS:
		// AutoTLS with Let's Encrypt
		s.slogger.Info("Starting with AutoTLS (Let's Encrypt)")
		err = s.echo.StartAutoTLS(addr)
	case s.config.TLSEnabled:
		// Manual TLS
		s.slogger.Info("Starting with manual TLS",
			logger.String("cert", s.config.TLSCertFile),
			logger.String("key", s.config.TLSKeyFile),
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

	s.slogger.Info("Shutdown signal received, initiating graceful shutdown")

	return s.Shutdown()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown() error {
	// Create shutdown context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), s.config.ShutdownTimeout)
	defer cancel()

	// Shutdown API controller first (waits for its background goroutines)
	if s.apiController != nil {
		s.apiController.Shutdown()
	}

	// Shutdown Echo server (causes Start() goroutine to exit)
	if err := s.echo.Shutdown(ctx); err != nil {
		s.slogger.Error("Error during server shutdown", logger.Error(err))
		return fmt.Errorf("shutdown error: %w", err)
	}

	// Log completion and flush
	s.slogger.Info("Server shutdown complete")

	// Flush logger to ensure all messages are written
	if err := s.slogger.Flush(); err != nil {
		fmt.Printf("Error flushing log: %v\n", err)
	}

	return nil
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
func (s *Server) registerSPARoutes() {
	// Redirect root path to dashboard
	s.echo.GET("/", func(c echo.Context) error {
		return c.Redirect(http.StatusFound, "/ui/dashboard")
	})

	// Public routes (no authentication required)
	publicRoutes := []string{
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

	// Protected catch-all for settings routes
	// Any /ui/settings/* route not explicitly listed above requires authentication
	// This ensures new settings pages are protected by default
	if authMiddleware != nil {
		s.echo.GET("/ui/settings/*", s.spaHandler.ServeApp, authMiddleware)
	} else {
		s.echo.GET("/ui/settings/*", s.spaHandler.ServeApp)
	}

	// Catch-all route for any unmatched /ui/* paths (public)
	// This ensures SPA handles client-side routing for unknown routes
	// Must be registered last to not override specific routes
	s.echo.GET("/ui/*", s.spaHandler.ServeApp)

	s.slogger.Debug("SPA routes registered",
		logger.Int("public_routes", len(publicRoutes)),
		logger.Int("public_dynamic_routes", len(publicDynamicRoutes)),
		logger.Int("protected_routes", len(protectedRoutes)),
	)
}

// getAuthMiddleware returns the authentication middleware owned by the server.
func (s *Server) getAuthMiddleware() echo.MiddlewareFunc {
	return s.authMiddleware
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

	s.slogger.Info("OAuth begin request",
		logger.String("provider", provider),
		logger.String("ip", c.RealIP()),
	)

	// Validate provider is one we support
	if !s.isValidOAuthProvider(provider) {
		s.slogger.Warn("Invalid OAuth provider requested",
			logger.String("provider", provider),
			logger.String("ip", c.RealIP()),
		)
		return c.String(http.StatusBadRequest, "Invalid OAuth provider")
	}

	// Add provider to request context for gothic
	req := gothic.GetContextWithProvider(c.Request(), provider)

	// Try to complete auth if user already has a valid session
	if user, err := gothic.CompleteUserAuth(c.Response(), req); err == nil {
		s.slogger.Info("User already authenticated via OAuth",
			logger.String("provider", provider),
			logger.String("user_id", user.UserID),
			logger.String("email", user.Email),
		)
		// User is already authenticated, redirect to dashboard
		return c.Redirect(http.StatusFound, "/ui/dashboard")
	}

	// Begin OAuth flow - this will redirect to the provider
	gothic.BeginAuthHandler(c.Response(), req)
	return nil
}

// handleOAuthCallback handles the OAuth callback from the provider.
// GET /auth/:provider/callback
func (s *Server) handleOAuthCallback(c echo.Context) error {
	provider := c.Param("provider")

	s.slogger.Info("OAuth callback received",
		logger.String("provider", provider),
		logger.String("ip", c.RealIP()),
	)

	// Add provider to request context for gothic
	req := gothic.GetContextWithProvider(c.Request(), provider)

	// Complete the OAuth flow and get user info
	user, err := gothic.CompleteUserAuth(c.Response(), req)
	if err != nil {
		s.slogger.Error("OAuth authentication failed",
			logger.String("provider", provider),
			logger.Error(err),
			logger.String("ip", c.RealIP()),
		)
		return c.String(http.StatusUnauthorized, "Authentication failed: "+err.Error())
	}

	s.slogger.Info("OAuth authentication successful",
		logger.String("provider", provider),
		logger.String("user_id", user.UserID),
		logger.String("email", user.Email),
		logger.String("name", user.Name),
		logger.String("ip", c.RealIP()),
	)

	// Validate user is allowed (check against configured allowed user IDs)
	if !s.isAllowedOAuthUser(provider, user.UserID, user.Email) {
		s.slogger.Warn("OAuth user not in allowed list",
			logger.String("provider", provider),
			logger.String("user_id", user.UserID),
			logger.String("email", user.Email),
			logger.String("ip", c.RealIP()),
		)
		return c.String(http.StatusForbidden, "Access denied: user not authorized")
	}

	// Store user info in session
	// Use email as userId if available, otherwise use provider's UserID
	userId := user.Email
	if userId == "" {
		userId = user.UserID
	}

	if err := gothic.StoreInSession("userId", userId, req, c.Response()); err != nil {
		s.slogger.Error("Failed to store userId in session",
			logger.Error(err),
			logger.String("provider", provider),
		)
	}

	// Store provider name in session for later reference
	if err := gothic.StoreInSession(provider, user.UserID, req, c.Response()); err != nil {
		s.slogger.Error("Failed to store provider session",
			logger.Error(err),
			logger.String("provider", provider),
		)
	}

	s.slogger.Info("OAuth session established, redirecting to dashboard",
		logger.String("provider", provider),
		logger.String("user_id", userId),
	)

	// Redirect to dashboard after successful authentication
	return c.Redirect(http.StatusFound, "/ui/dashboard")
}

// isValidOAuthProvider checks if the provider name is valid.
func (s *Server) isValidOAuthProvider(provider string) bool {
	validProviders := []string{
		security.ProviderGoogle,
		security.ProviderGitHub,
		security.ProviderMicrosoft,
	}
	for _, p := range validProviders {
		if strings.EqualFold(provider, p) {
			return true
		}
	}
	return false
}

// isAllowedOAuthUser checks if the OAuth user is in the allowed users list for the provider.
func (s *Server) isAllowedOAuthUser(provider, userID, email string) bool {
	if s.settings == nil {
		return false
	}

	var allowedUsers string
	var enabled bool

	switch strings.ToLower(provider) {
	case security.ProviderGoogle:
		enabled = s.settings.Security.GoogleAuth.Enabled
		allowedUsers = s.settings.Security.GoogleAuth.UserId
	case security.ProviderGitHub:
		enabled = s.settings.Security.GithubAuth.Enabled
		allowedUsers = s.settings.Security.GithubAuth.UserId
	case security.ProviderMicrosoft:
		enabled = s.settings.Security.MicrosoftAuth.Enabled
		allowedUsers = s.settings.Security.MicrosoftAuth.UserId
	default:
		return false
	}

	if !enabled {
		return false
	}

	// If no allowed users configured, deny access
	if allowedUsers == "" {
		return false
	}

	// Check if user ID or email matches any allowed user
	for allowed := range strings.SplitSeq(allowedUsers, ",") {
		trimmed := strings.TrimSpace(allowed)
		if trimmed == "" {
			continue
		}
		// Case-insensitive comparison for both userID and email
		if strings.EqualFold(trimmed, userID) || strings.EqualFold(trimmed, email) {
			return true
		}
	}

	return false
}
