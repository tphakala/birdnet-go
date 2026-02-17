// internal/api/v2/api.go
package api

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/patrickmn/go-cache"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/api/auth"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/ebird"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/securefs"
	"github.com/tphakala/birdnet-go/internal/spectrogram"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// Tunnel provider constant for unknown providers
const tunnelProviderUnknown = "unknown"

// Controller manages the API routes and handlers
type Controller struct {
	Echo                *echo.Echo
	Group               *echo.Group
	DS                  datastore.Interface           // Deprecated: Use Repo for new detection operations
	Repo                datastore.DetectionRepository // New: Preferred for detection CRUD operations
	Settings            *conf.Settings
	BirdImageCache      *imageprovider.BirdImageCache
	SunCalc             *suncalc.SunCalc
	Processor           *processor.Processor
	EBirdClient         *ebird.Client
	TaxonomyDB          *birdnet.TaxonomyDatabase
	controlChan         chan string
	speciesExcludeMutex sync.RWMutex // Mutex for species exclude list operations
	// DisableSaveSettings prevents persisting settings changes to disk.
	// When set to true, all settings modifications remain in memory only.
	// This is primarily used in testing but can be used in production for read-only mode.
	// Thread-safe: should be set before controller initialization.
	DisableSaveSettings  bool         // disables disk persistence of settings
	settingsMutex        sync.RWMutex // Mutex for settings operations
	detectionCache       *cache.Cache // Cache for detection queries
	startTime            *time.Time
	SFS                  *securefs.SecureFS     // Add SecureFS instance
	apiLogger            logger.Logger          // Structured logger for API operations
	metrics              *observability.Metrics // Shared metrics instance
	spectrogramGenerator *spectrogram.Generator // Shared spectrogram generator (initialized after SFS)

	// Auth related fields (injected from server via functional options)
	authService    auth.Service        // Authentication service (injected from server)
	authMiddleware echo.MiddlewareFunc // Authentication middleware function (injected from server)

	// SSE related fields
	sseManager *SSEManager // Manager for Server-Sent Events connections

	// Cleanup related fields
	ctx    context.Context    // Context for managing goroutines
	cancel context.CancelFunc // Cancel function for graceful shutdown

	// Goroutine lifecycle management
	wg sync.WaitGroup // tracks background goroutines for clean shutdown

	// Audio level channel for SSE streaming
	// TODO: Consider moving to a dedicated audio manager
	audioLevelChan chan myaudio.AudioLevelData

	// V2Manager provides access to the v2 normalized database for stats and backup
	V2Manager datastoreV2.Manager

	// Legacy cleanup state tracker
	cleanupStatus *CleanupStatus

	// Test synchronization fields (only populated when initializeRoutes is true)
	// goroutinesStarted signals when all background goroutines have successfully started.
	// This is primarily used in testing to ensure proper setup before assertions.
	// Only created when routes are initialized (production mode or specific tests).
	goroutinesStarted chan struct{} // signals when all background goroutines have started (nil if routes not initialized)
}

// Option is a functional option for configuring the Controller.
type Option func(*Controller)

// WithAuthMiddleware sets the authentication middleware for the controller.
func WithAuthMiddleware(mw echo.MiddlewareFunc) Option {
	return func(c *Controller) {
		c.authMiddleware = mw
	}
}

// WithAuthService sets the authentication service for the controller.
func WithAuthService(svc auth.Service) Option {
	return func(c *Controller) {
		c.authService = svc
	}
}

// WithV2Manager sets the v2 database manager for the controller.
// This enables v2 database stats and backup endpoints.
func WithV2Manager(mgr datastoreV2.Manager) Option {
	return func(c *Controller) {
		c.V2Manager = mgr
	}
}

// parseIPFromHeader attempts to parse a valid IP from a header value.
// Returns the IP string if valid, empty string otherwise.
func parseIPFromHeader(headerValue string) string {
	if headerValue == "" {
		return ""
	}
	ip := net.ParseIP(headerValue)
	if ip != nil {
		return ip.String()
	}
	return ""
}

// parseFirstIPFromXFF extracts the first valid IP from X-Forwarded-For header.
func parseFirstIPFromXFF(xff string) string {
	if xff == "" {
		return ""
	}
	parts := strings.SplitSeq(xff, ",")
	for part := range parts {
		if ip := parseIPFromHeader(strings.TrimSpace(part)); ip != "" {
			return ip
		}
	}
	return ""
}

// Custom IP Extractor prioritizing CF-Connecting-IP
func ipExtractorFromCloudflareHeader(req *http.Request) string {
	// 1. Check CF-Connecting-IP
	if ip := parseIPFromHeader(req.Header.Get("CF-Connecting-IP")); ip != "" {
		return ip
	}

	// 2. Check X-Forwarded-For (taking the first valid IP)
	if ip := parseFirstIPFromXFF(req.Header.Get(echo.HeaderXForwardedFor)); ip != "" {
		return ip
	}

	// 3. Check X-Real-IP
	if ip := parseIPFromHeader(req.Header.Get(echo.HeaderXRealIP)); ip != "" {
		return ip
	}

	// 4. Fallback to Remote Address (might be proxy)
	remoteAddr, _, _ := net.SplitHostPort(req.RemoteAddr)
	if ip := parseIPFromHeader(remoteAddr); ip != "" {
		return ip
	}

	return remoteAddr
}

// TunnelDetectionMiddleware inspects headers to determine if the request is likely proxied
// and sets context values for logging.
func (c *Controller) TunnelDetectionMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(ctx echo.Context) error {
			req := ctx.Request()
			tunneled := false
			provider := tunnelProviderUnknown

			// Check Cloudflare header first
			if req.Header.Get("CF-Connecting-IP") != "" {
				tunneled = true
				provider = "cloudflare"
			} else if req.Header.Get(echo.HeaderXForwardedFor) != "" || req.Header.Get(echo.HeaderXRealIP) != "" {
				// If other proxy headers exist, mark as tunneled but provider is generic
				tunneled = true
				provider = "generic"
			}

			ctx.Set("is_tunneled", tunneled)
			ctx.Set("tunnel_provider", provider)

			return next(ctx)
		}
	}
}

// New creates a new API controller, returning an error if initialization fails.
func New(e *echo.Echo, ds datastore.Interface, settings *conf.Settings,
	birdImageCache *imageprovider.BirdImageCache, sunCalc *suncalc.SunCalc,
	controlChan chan string,
	metrics *observability.Metrics, opts ...Option) (*Controller, error) {
	return NewWithOptions(e, ds, settings, birdImageCache, sunCalc, controlChan, metrics, true, opts...)
}

// resolveAndValidateMediaPath resolves a potentially relative media path and ensures it exists as a directory.
// Returns the absolute path and any error encountered.
func resolveAndValidateMediaPath(configPath string) (string, error) {
	if configPath == "" {
		return "", fmt.Errorf("settings.realtime.audio.export.path must not be empty")
	}

	mediaPath := configPath

	// Resolve relative path to absolute based on working directory
	if !filepath.IsAbs(mediaPath) {
		workDir, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to get working directory to resolve relative media path: %w", err)
		}
		mediaPath = filepath.Join(workDir, mediaPath)
		GetLogger().Debug("Resolved relative media export path",
			logger.String("config_path", configPath),
			logger.String("absolute_path", mediaPath),
		)
	}

	// Ensure directory exists, creating if necessary
	if err := ensureDirectoryExists(mediaPath); err != nil {
		return "", err
	}

	return mediaPath, nil
}

// ensureDirectoryExists checks that a path exists and is a directory, creating it if needed.
func ensureDirectoryExists(path string) error {
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(path, FilePermExecutable); err != nil {
			return fmt.Errorf("failed to create directory %q: %w", path, err)
		}
		fi, err = os.Stat(path)
	}
	if err != nil {
		return fmt.Errorf("error checking path %q: %w", path, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("path is not a directory: %q", path)
	}
	return nil
}

// NewWithOptions creates a new API controller with optional route initialization.
// Set initializeRoutes to false for testing to avoid starting background goroutines.
func NewWithOptions(e *echo.Echo, ds datastore.Interface, settings *conf.Settings,
	birdImageCache *imageprovider.BirdImageCache, sunCalc *suncalc.SunCalc,
	controlChan chan string,
	metrics *observability.Metrics, initializeRoutes bool, opts ...Option) (*Controller, error) {

	// Configure IP extractor for Cloudflare proxy support
	e.IPExtractor = ipExtractorFromCloudflareHeader
	GetLogger().Info("Configured custom IP extractor prioritizing CF-Connecting-IP")

	// Validate and resolve media export path
	mediaPath, err := resolveAndValidateMediaPath(settings.Realtime.Audio.Export.Path)
	if err != nil {
		return nil, err
	}

	sfs, err := securefs.New(mediaPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize secure filesystem for media: %w", err)
	}

	// Create context for managing goroutines
	ctx, cancel := context.WithCancel(context.Background())

	// Only create DetectionRepository if datastore is available.
	// This prevents nil pointer dereference when datastore is disabled.
	var repo datastore.DetectionRepository
	if ds != nil {
		repo = datastore.NewDetectionRepository(ds, nil)
	}

	c := &Controller{
		Echo:                 e,
		DS:                   ds,
		Repo:                 repo, // Bridge to new domain model (nil if datastore disabled)
		Settings:             settings,
		BirdImageCache:       birdImageCache,
		SunCalc:              sunCalc,
		controlChan:          controlChan,
		detectionCache:       cache.New(detectionCacheExpiry, detectionCacheCleanup),
		SFS:                  sfs, // Assign SecureFS instance
		metrics:              metrics,
		ctx:                  ctx,
		cancel:               cancel,
		spectrogramGenerator: spectrogram.NewGenerator(settings, sfs, getSpectrogramLogger()), // Initialize shared generator
	}

	// Initialize structured logger for API requests
	c.apiLogger = logger.Global().Module("api")

	// Load local taxonomy database for fast species lookups
	taxonomyDB, err := birdnet.LoadTaxonomyDatabase()
	if err != nil {
		c.logWarnIfEnabled("Failed to load taxonomy database", logger.Error(err))
		c.logWarnIfEnabled("Species taxonomy lookups will fall back to eBird API")
		// Continue without taxonomy database - eBird API fallback will be used
		c.TaxonomyDB = nil
	} else {
		c.TaxonomyDB = taxonomyDB
		stats := taxonomyDB.Stats()
		c.logInfoIfEnabled("Loaded taxonomy database",
			logger.Any("genus_count", stats["genus_count"]),
			logger.Any("family_count", stats["family_count"]),
			logger.Any("species_count", stats["species_count"]),
			logger.String("version", taxonomyDB.Version),
			logger.String("updated_at", taxonomyDB.UpdatedAt),
		)
	}

	// Apply functional options (auth middleware and service injected from server)
	for _, opt := range opts {
		opt(c)
	}

	// Log auth configuration status
	log := GetLogger()
	if c.authMiddleware != nil {
		log.Info("Auth middleware configured via functional options")
	} else {
		log.Warn("Auth middleware not configured")
	}

	// Create v2 API group
	c.Group = e.Group("/api/v2")

	// Configure middlewares
	c.Group.Use(middleware.Recover())          // Recover should be early
	c.Group.Use(c.TunnelDetectionMiddleware()) // Add tunnel detection **before** logging
	// c.Group.Use(middleware.Logger())        // Removed: Use custom LoggingMiddleware below for structured logging
	// NOTE: CORS middleware is configured at the global Echo level in server.go
	// Removing duplicate CORS here to avoid conflicts with global CORS configuration
	c.Group.Use(middleware.BodyLimit("1M")) // Limit request body to 1MB to prevent DoS attacks
	c.Group.Use(c.LoggingMiddleware())      // Use custom structured logging middleware

	// NOTE: CSRF token is provided by the /app/config endpoint using middleware.EnsureCSRFToken()
	// which handles Echo v4.15.0's Sec-Fetch-Site optimization that may skip token generation
	// for same-origin requests. Global CSRF middleware in server.go handles validation.

	// Initialize start time for uptime tracking
	now := time.Now()
	c.startTime = &now

	// Initialize SSE manager
	c.sseManager = NewSSEManager()

	// Initialize eBird client if enabled
	if settings.Realtime.EBird.Enabled {
		if settings.Realtime.EBird.APIKey == "" {
			// Create notification for missing API key
			// The Build() method automatically publishes to the event bus for notifications
			_ = errors.Newf("eBird integration enabled but API key not configured").
				Category(errors.CategoryConfiguration).
				Context("setting", "realtime.ebird.apikey").
				Component("ebird").
				Build()
			log.Warn("eBird integration enabled but API key not configured")
		} else {
			ebirdConfig := ebird.Config{
				APIKey:   settings.Realtime.EBird.APIKey,
				CacheTTL: time.Duration(settings.Realtime.EBird.CacheTTL) * time.Hour,
			}
			ebirdClient, err := ebird.NewClient(ebirdConfig)
			if err != nil {
				// Initialization error - already enhanced by ebird.NewClient
				log.Warn("Failed to initialize eBird client", logger.Error(err))
				// Continue without eBird client - it's not critical
			} else {
				c.EBirdClient = ebirdClient
				log.Info("Initialized eBird API client")
			}
		}
	} else {
		log.Debug("eBird integration disabled")
	}

	// Initialize routes if requested (skip in tests to avoid starting background goroutines)
	if initializeRoutes {
		// Initialize synchronization channel for testing
		c.goroutinesStarted = make(chan struct{})
		c.initRoutes()
		// Signal that all goroutines have started
		close(c.goroutinesStarted)
	}

	return c, nil // Return controller and nil error
}

// LoggingMiddleware creates a middleware function that logs API requests
func (c *Controller) LoggingMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(ctx echo.Context) error {
			start := time.Now()

			// Process the request
			err := next(ctx)

			// Skip logging if apiLogger is not initialized
			if c.apiLogger == nil {
				return err
			}

			// Extract request information
			req := ctx.Request()
			res := ctx.Response()

			// Get tunnel info from context
			isTunneled, _ := ctx.Get("is_tunneled").(bool)
			tunnelProvider, _ := ctx.Get("tunnel_provider").(string)

			// Log the request with structured data
			fields := []logger.Field{
				logger.String("method", req.Method),
				logger.String("path", req.URL.Path),
				logger.String("query", req.URL.RawQuery),
				logger.Int("status", res.Status),
				logger.String("ip", ctx.RealIP()), // Uses custom extractor
				logger.Bool("tunneled", isTunneled),
				logger.String("tunnel_provider", tunnelProvider),
				logger.String("user_agent", req.UserAgent()),
				logger.Int64("latency_ms", time.Since(start).Milliseconds()),
			}
			if err != nil {
				fields = append(fields, logger.Error(err))
			}

			c.apiLogger.Info("API Request", fields...)

			return err
		}
	}
}

// initRoutes registers all API endpoints
func (c *Controller) initRoutes() {
	// Health check endpoint - publicly accessible
	c.Group.GET("/health", c.HealthCheck)

	// Initialize route groups with proper error handling and logging
	routeInitializers := []struct {
		name string
		fn   func()
	}{
		{"app routes", c.initAppRoutes},
		{"search routes", c.initSearchRoutes},
		{"detection routes", c.initDetectionRoutes},
		{"analytics routes", c.initAnalyticsRoutes},
		{"weather routes", c.initWeatherRoutes},
		{"system routes", c.initSystemRoutes},
		{"terminal routes", c.initTerminalRoutes},
		{"settings routes", c.initSettingsRoutes},
		{"filesystem routes", c.initFileSystemRoutes},
		{"stream health routes", c.initStreamHealthRoutes},
		{"audio level routes", c.initAudioLevelRoutes},
		{"hls streaming routes", c.initHLSRoutes},
		{"integration routes", c.initIntegrationsRoutes},
		{"control routes", c.initControlRoutes},
		{"auth routes", c.initAuthRoutes},
		{"media routes", c.initMediaRoutes},
		{"range routes", c.initRangeRoutes},
		{"sse routes", c.initSSERoutes},
		{"notification routes", c.initNotificationRoutes},
		{"support routes", c.initSupportRoutes},
		{"debug routes", c.initDebugRoutes},
		{"species routes", c.initSpeciesRoutes},
		{"dynamic threshold routes", c.initDynamicThresholdRoutes},
	}

	for _, initializer := range routeInitializers {
		c.Debug("Initializing %s...", initializer.name)

		// Use a deferred function to recover from panics during route initialization
		func() {
			defer func() {
				if r := recover(); r != nil {
					GetLogger().Error("PANIC during route initialization",
						logger.String("route", initializer.name),
						logger.Any("panic", r),
					)
				}
			}()

			// Call the initializer
			initializer.fn()

			c.Debug("Successfully initialized %s", initializer.name)
		}()
	}
}

// HealthCheck handles the API health check endpoint
func (c *Controller) HealthCheck(ctx echo.Context) error {
	// Create response structure
	response := map[string]any{
		"status":     "healthy",
		"version":    c.Settings.Version,
		"build_date": c.Settings.BuildDate,
		"timestamp":  time.Now().Format(time.RFC3339),
	}

	// Add environment if available in settings
	if c.Settings != nil && c.Settings.WebServer.Debug {
		response["environment"] = "development"
	} else {
		response["environment"] = "production"
	}

	// Check database connectivity - simple check if we can access the datastore
	dbStatus := "connected"
	var dbError string

	// Try a simple database operation to check connectivity
	_, dbErr := c.DS.GetLastDetections(1)
	if dbErr != nil {
		dbStatus = "disconnected"
		dbError = dbErr.Error()
		// If database is critical, we might want to change the overall status
		// response["status"] = "degraded"
	}

	response["database_status"] = dbStatus
	if dbError != "" {
		response["database_error"] = dbError
	}

	// Add uptime if available
	if c.startTime != nil {
		uptime := time.Since(*c.startTime)
		response["uptime"] = uptime.String()
		response["uptime_seconds"] = uptime.Seconds()
	}

	// Add system metrics
	systemMetrics := make(map[string]any)

	// Add placeholder CPU usage (would be implemented with actual metrics in production)
	systemMetrics["cpu_usage"] = 0.0

	// Add placeholder memory usage
	memoryMetrics := map[string]any{
		"used_percent": 0.0,
		"total_mb":     0.0,
		"used_mb":      0.0,
	}
	systemMetrics["memory"] = memoryMetrics

	// Add placeholder disk space
	diskMetrics := map[string]any{
		"total_gb":     0.0,
		"free_gb":      0.0,
		"used_percent": 0.0,
	}
	systemMetrics["disk_space"] = diskMetrics

	// Add system metrics to response
	response["system"] = systemMetrics

	return ctx.JSON(http.StatusOK, response)
}

// Shutdown performs cleanup of all resources used by the API controller
// This should be called when the application is shutting down
func (c *Controller) Shutdown() {
	// Cancel context to stop all goroutines
	if c.cancel != nil {
		c.cancel()
	}

	// Wait for all goroutines to finish
	c.wg.Wait()

	// Shutdown the backup job manager to stop its cleanup goroutine
	if backupJobManager != nil {
		backupJobManager.Shutdown()
	}

	// Flush the API logger
	if err := c.apiLogger.Flush(); err != nil {
		GetLogger().Error("Error flushing API log", logger.Error(err))
	}

	// TODO: The go-cache library's janitor goroutine cannot be stopped.
	// Consider migrating to a context-aware cache implementation.
	if c.detectionCache != nil {
		c.detectionCache.Flush()
	}

	// Log shutdown
	c.Debug("API Controller shutting down")
}

// Error response structure
type ErrorResponse struct {
	Error         string `json:"error"`
	Message       string `json:"message"`
	Code          int    `json:"code"`
	CorrelationID string `json:"correlation_id"` // Unique identifier for tracking this error
}

// NewErrorResponse creates a new API error response
func NewErrorResponse(err error, message string, code int) *ErrorResponse {
	// Generate a random correlation ID (8 characters should be sufficient)
	correlationID := generateCorrelationID()

	var errorStr string
	if err != nil {
		errorStr = err.Error()
	} else {
		errorStr = message // Use message as error if no error object is provided
	}

	return &ErrorResponse{
		Error:         errorStr,
		Message:       message,
		Code:          code,
		CorrelationID: correlationID,
	}
}

// generateCorrelationID creates a unique identifier for error tracking using cryptographic randomness
// for better security and uniqueness guarantees across all platforms
func generateCorrelationID() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const length = 8

	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		// Fall back to a default ID if crypto/rand fails
		return "ERR-RAND"
	}

	// Map the random bytes to charset characters
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b)
}

// HandleError constructs and returns an appropriate error response
func (c *Controller) HandleError(ctx echo.Context, err error, message string, code int) error {
	errorResp := NewErrorResponse(err, message, code)

	// Determine IP to log using the request context
	ip := ctx.RealIP() // Now uses the custom extractor

	// Get tunnel info from context
	isTunneled, _ := ctx.Get("is_tunneled").(bool)
	tunnelProvider, _ := ctx.Get("tunnel_provider").(string)

	// Build error string for logging
	var errorStr string
	if err != nil {
		errorStr = err.Error()
	} else {
		errorStr = message
	}

	// Log the error using structured logger
	c.logErrorIfEnabled("API Error",
		logger.String("correlation_id", errorResp.CorrelationID),
		logger.String("message", message),
		logger.String("error", errorStr),
		logger.Int("code", code),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("method", ctx.Request().Method),
		logger.String("ip", ip), // Log the extracted IP
		logger.Bool("tunneled", isTunneled),
		logger.String("tunnel_provider", tunnelProvider),
	)

	return ctx.JSON(code, errorResp)
}

// HandleErrorForTest constructs and returns an echo.HTTPError for testing purposes
// This method is used in tests where echo.HTTPError is expected for error assertions
func (c *Controller) HandleErrorForTest(ctx echo.Context, err error, message string, code int) error {
	// Include the original error message for better test assertions
	fullMessage := message
	if err != nil {
		fullMessage = fmt.Sprintf("%s: %v", message, err)
	}
	return echo.NewHTTPError(code, fullMessage)
}

// Debug logs debug messages when debug mode is enabled
func (c *Controller) Debug(format string, v ...any) {
	if c.Settings.WebServer.Debug {
		msg := fmt.Sprintf(format, v...)
		c.logDebugIfEnabled(msg)
	}
}

// logAPIRequest is a helper to log API requests with common context fields.
func (c *Controller) logAPIRequest(ctx echo.Context, level logger.LogLevel, msg string, fields ...logger.Field) {
	if c.apiLogger == nil {
		return // Do nothing if logger isn't initialized
	}

	// Extract common context info
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path

	// Create base fields with preallocated capacity
	baseFields := make([]logger.Field, 0, 2+len(fields))
	baseFields = append(baseFields,
		logger.String("path", path),
		logger.String("ip", ip),
	)

	// Append specific fields to base fields
	baseFields = append(baseFields, fields...)

	// Log at the specified level
	c.apiLogger.Log(level, msg, baseFields...)
}

// GetAuthMiddleware returns the authentication middleware function injected from server.
// This replaces the previous getEffectiveAuthMiddleware that had fallback logic.
//
// Returns nil if no middleware was configured via WithAuthMiddleware option.
// Callers should be aware that applying nil middleware to Echo routes is a no-op
// (the routes become unprotected). A warning is logged during initialization
// if auth middleware is not configured.
func (c *Controller) GetAuthMiddleware() echo.MiddlewareFunc {
	return c.authMiddleware
}

// InitializeAPI creates a new API controller and registers all routes.
// Auth middleware and service should be passed via functional options.
func InitializeAPI(e *echo.Echo, ds datastore.Interface, settings *conf.Settings,
	birdImageCache *imageprovider.BirdImageCache, sunCalc *suncalc.SunCalc,
	controlChan chan string, proc *processor.Processor,
	metrics *observability.Metrics, opts ...Option) *Controller {

	// Create API controller with metrics and functional options
	apiController, err := New(e, ds, settings, birdImageCache, sunCalc, controlChan, metrics, opts...)
	if err != nil {
		GetLogger().Error("Failed to initialize API", logger.Error(err))
		os.Exit(1)
	}

	// Assign processor after initialization
	apiController.Processor = proc

	// Log initialization
	apiController.logInfoIfEnabled("API v2 initialized",
		logger.String("version", settings.Version),
		logger.String("build_date", settings.BuildDate),
	)

	return apiController
}
