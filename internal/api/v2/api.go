// internal/api/v2/api.go
package api

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"log/slog"
	"net"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/patrickmn/go-cache"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/api/v2/auth"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/ebird"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/securefs"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/observability"
	runtimectx "github.com/tphakala/birdnet-go/internal/buildinfo"
	"github.com/tphakala/birdnet-go/internal/security"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// Controller manages the API routes and handlers
type Controller struct {
	Echo                *echo.Echo
	Group               *echo.Group
	DS                  datastore.Interface
	Settings            *conf.Settings
	// Runtime provides build-time metadata. A nil Runtime is permitted for tests/early-boot scenarios; 
	// handlers will fallback to "unknown" values for version, build date, and system ID when nil.
	Runtime             *runtimectx.Context
	BirdImageCache      *imageprovider.BirdImageCache
	SunCalc             *suncalc.SunCalc
	Processor           *processor.Processor
	EBirdClient         *ebird.Client
	logger              *log.Logger
	controlChan         chan string
	speciesExcludeMutex sync.RWMutex // Mutex for species exclude list operations
	DisableSaveSettings bool          // Flag to disable saving settings to disk (for tests)
	settingsMutex       sync.RWMutex // Mutex for settings operations
	detectionCache      *cache.Cache // Cache for detection queries
	startTime           *time.Time
	SFS                 *securefs.SecureFS     // Add SecureFS instance
	apiLogger           *slog.Logger           // Structured logger for API operations
	apiLevelVar         *slog.LevelVar         // Dynamic level control (type declaration)
	apiLoggerClose      func() error           // Function to close the log file
	metrics             *observability.Metrics // Shared metrics instance

	// Auth related fields
	// AuthService stores the shared authentication service instance.
	// NOTE: This instance is shared across all requests handled by this controller.
	// The underlying implementation (auth.SecurityAdapter embedding security.OAuth2Server)
	// is designed to be concurrency-safe through internal locking (e.g., RWMutex for token maps).
	AuthService      auth.Service        // Store the auth service instance
	authMiddlewareFn echo.MiddlewareFunc // Authentication middleware function (set if auth configured)

	// SSE related fields
	sseManager *SSEManager // Manager for Server-Sent Events connections

	// Cleanup related fields
	ctx    context.Context    // Context for managing goroutines
	cancel context.CancelFunc // Cancel function for graceful shutdown
	
	// Test synchronization fields
	goroutinesStarted chan struct{} // Signals when all background goroutines have started (test only)
	wg                sync.WaitGroup // Tracks background goroutines for clean shutdown
}

// Define specific errors for token handling failures
var (
	errMalformedAuthHeader = fmt.Errorf("malformed authorization header")
	errInvalidAuthToken    = fmt.Errorf("invalid or expired token")
	errAuthServiceNil      = fmt.Errorf("internal configuration error: auth service is nil")
)

// Custom IP Extractor prioritizing CF-Connecting-IP
func ipExtractorFromCloudflareHeader(req *http.Request) string {
	// 1. Check CF-Connecting-IP
	cfIP := req.Header.Get("CF-Connecting-IP")
	if cfIP != "" {
		ip := net.ParseIP(cfIP)
		if ip != nil {
			return ip.String() // Return valid IP
		}
	}

	// 2. Check X-Forwarded-For (taking the first valid IP)
	xff := req.Header.Get(echo.HeaderXForwardedFor)
	if xff != "" {
		parts := strings.Split(xff, ",")
		for _, part := range parts {
			ipStr := strings.TrimSpace(part)
			ip := net.ParseIP(ipStr)
			if ip != nil {
				return ip.String() // Return first valid IP found
			}
		}
	}

	// 3. Check X-Real-IP
	xri := req.Header.Get(echo.HeaderXRealIP)
	if xri != "" {
		ip := net.ParseIP(xri)
		if ip != nil {
			return ip.String() // Return valid IP
		}
	}

	// 4. Fallback to Remote Address (might be proxy)
	// Use SplitHostPort for robustness, ignoring potential errors
	remoteAddr, _, _ := net.SplitHostPort(req.RemoteAddr)
	ip := net.ParseIP(remoteAddr)
	if ip != nil {
		return ip.String() // Return valid IP if RemoteAddr is just an IP
	}

	// If RemoteAddr contained a port or was invalid, return the raw string
	// (though ideally, it should be a valid IP:port format)
	return remoteAddr
}

// TunnelDetectionMiddleware inspects headers to determine if the request is likely proxied
// and sets context values for logging.
func (c *Controller) TunnelDetectionMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(ctx echo.Context) error {
			req := ctx.Request()
			tunneled := false
			provider := "unknown"

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
func New(e *echo.Echo, ds datastore.Interface, settings *conf.Settings, runtimeCtx *runtimectx.Context,
	birdImageCache *imageprovider.BirdImageCache, sunCalc *suncalc.SunCalc,
	controlChan chan string, logger *log.Logger, oauth2Server *security.OAuth2Server,
	metrics *observability.Metrics) (*Controller, error) {
	return NewWithOptions(e, ds, settings, runtimeCtx, birdImageCache, sunCalc, controlChan, logger, oauth2Server, metrics, true)
}

// NewWithOptions creates a new API controller with optional route initialization.
// Set initializeRoutes to false for testing to avoid starting background goroutines.
func NewWithOptions(e *echo.Echo, ds datastore.Interface, settings *conf.Settings, runtimeCtx *runtimectx.Context,
	birdImageCache *imageprovider.BirdImageCache, sunCalc *suncalc.SunCalc,
	controlChan chan string, logger *log.Logger, oauth2Server *security.OAuth2Server,
	metrics *observability.Metrics, initializeRoutes bool) (*Controller, error) {

	if logger == nil {
		logger = log.Default()
	}

	// --- Configure IP Extractor ---
	// IMPORTANT: For this to be secure in production, you would typically
	// combine this with middleware that verifies the request came *from*
	// a trusted proxy (like Cloudflare's IP ranges). Echo doesn't have
	// built-in trusted proxy IP range checking, so this might require
	// custom middleware or careful infrastructure setup (e.g., firewall rules).
	// Without trusting the proxy, these headers can be spoofed.
	e.IPExtractor = ipExtractorFromCloudflareHeader
	logger.Println("Configured custom IP extractor prioritizing CF-Connecting-IP")
	// --- End IP Extractor Configuration ---

	// Validate and Initialize SecureFS for the media export path
	mediaPath := settings.Realtime.Audio.Export.Path

	// --- Sanity checks for mediaPath ---
	if mediaPath == "" {
		return nil, fmt.Errorf("settings.realtime.audio.export.path must not be empty")
	}

	// Resolve relative path to absolute based on working directory
	if !filepath.IsAbs(mediaPath) {
		// Get the current working directory
		workDir, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get working directory to resolve relative media path: %w", err)
		}
		mediaPath = filepath.Join(workDir, mediaPath)
		logger.Printf("Resolved relative media export path \"%s\" to absolute path \"%s\"", settings.Realtime.Audio.Export.Path, mediaPath) // Log the resolution
	}

	// Now perform checks on the potentially resolved absolute path
	fi, err := os.Stat(mediaPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Attempt to create the directory if it doesn't exist
			if err := os.MkdirAll(mediaPath, 0o755); err != nil {
				return nil, fmt.Errorf("failed to create media export directory %q: %w", mediaPath, err)
			}
			// Stat again after creation
			fi, err = os.Stat(mediaPath)
			if err != nil {
				return nil, fmt.Errorf("error checking newly created media export path %q: %w", mediaPath, err)
			}
		} else {
			// Other Stat error
			return nil, fmt.Errorf("error checking settings.realtime.audio.export.path %q: %w", mediaPath, err)
		}
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("settings.realtime.audio.export.path is not a directory: %q", mediaPath)
	}
	// --- End sanity checks ---

	sfs, err := securefs.New(mediaPath) // Create SecureFS rooted at the export path
	if err != nil {
		return nil, fmt.Errorf("failed to initialize secure filesystem for media: %w", err)
	}

	// Create context for managing goroutines
	ctx, cancel := context.WithCancel(context.Background())

	c := &Controller{
		Echo:           e,
		DS:             ds,
		Settings:       settings,
		Runtime:        runtimeCtx,
		BirdImageCache: birdImageCache,
		SunCalc:        sunCalc,
		controlChan:    controlChan,
		logger:         logger,
		detectionCache: cache.New(5*time.Minute, 10*time.Minute),
		SFS:            sfs, // Assign SecureFS instance
		metrics:        metrics,
		ctx:            ctx,
		cancel:         cancel,
	}

	// Initialize structured logger for API requests
	apiLogPath := "logs/web.log"
	initialLevel := slog.LevelInfo
	c.apiLevelVar = new(slog.LevelVar) // Initialize here
	c.apiLevelVar.Set(initialLevel)

	apiLogger, closeFunc, err := logging.NewFileLogger(apiLogPath, "api", c.apiLevelVar)
	if err != nil {
		logger.Printf("Warning: Failed to initialize API structured logger: %v", err)
		// Fallback to a disabled logger (writes to io.Discard) but respects the level var
		logger.Printf("API falling back to a disabled logger due to initialization error.")
		fbHandler := slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: c.apiLevelVar})
		c.apiLogger = slog.New(fbHandler).With("service", "api")
		c.apiLoggerClose = func() error { return nil } // No-op closer
	} else {
		c.apiLogger = apiLogger
		c.apiLoggerClose = closeFunc
		logger.Printf("API structured logging initialized to %s", apiLogPath)
	}

	// If OAuth2Server is provided, setup authentication service and middleware function
	if oauth2Server != nil {
		// Create and store the auth service instance directly.
		// This single instance is shared across requests handled by this controller.
		// Concurrency safety is handled within the auth.Service implementation.
		c.AuthService = auth.NewSecurityAdapter(oauth2Server, c.apiLogger)

		// Create the middleware provider using the stored service
		authMiddlewareProvider := auth.NewMiddleware(c.AuthService, c.apiLogger)
		c.authMiddlewareFn = authMiddlewareProvider.Authenticate

		logger.Println("Initialized API authentication service and middleware function")
	} else {
		logger.Println("Warning: OAuth2Server not provided, API authentication not configured")
		// Potentially set a NoOp auth service here if needed for consistency
		// c.AuthService = auth.NewNoOpService()
	}

	// Create v2 API group
	c.Group = e.Group("/api/v2")

	// Configure middlewares
	c.Group.Use(middleware.Recover())          // Recover should be early
	c.Group.Use(c.TunnelDetectionMiddleware()) // Add tunnel detection **before** logging
	// c.Group.Use(middleware.Logger())        // Removed: Use custom LoggingMiddleware below for structured logging
	c.Group.Use(middleware.CORS())     // CORS handling
	c.Group.Use(middleware.BodyLimit("1M")) // Limit request body to 1MB to prevent DoS attacks
	c.Group.Use(c.LoggingMiddleware()) // Use custom structured logging middleware

	// NOTE: CSRF Protection Consideration
	// The V2 API uses Bearer token authentication (Authorization: Bearer <token>)
	// which is not vulnerable to CSRF attacks since browsers cannot automatically
	// include Bearer tokens in cross-origin requests. CSRF protection is only
	// needed for cookie-based authentication. If session-based auth is added
	// in the future, consider adding CSRF middleware for those specific endpoints.

	// Initialize start time for uptime tracking
	now := time.Now()
	c.startTime = &now

	// Initialize SSE manager
	c.sseManager = NewSSEManager(logger)

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
			logger.Println("Warning: eBird integration enabled but API key not configured")
		} else {
			ebirdConfig := ebird.Config{
				APIKey:   settings.Realtime.EBird.APIKey,
				CacheTTL: time.Duration(settings.Realtime.EBird.CacheTTL) * time.Hour,
			}
			ebirdClient, err := ebird.NewClient(ebirdConfig)
			if err != nil {
				// Initialization error - already enhanced by ebird.NewClient
				logger.Printf("Warning: Failed to initialize eBird client: %v", err)
				// Continue without eBird client - it's not critical
			} else {
				c.EBirdClient = ebirdClient
				logger.Println("Initialized eBird API client")
			}
		}
	} else {
		logger.Println("eBird integration disabled")
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

			// Log the request with structured data using LogAttrs to avoid allocations
			// when the log level is disabled.
			attrs := []slog.Attr{
				slog.String("method", req.Method),
				slog.String("path", req.URL.Path),
				slog.String("query", req.URL.RawQuery),
				slog.Int("status", res.Status),
				slog.String("ip", ctx.RealIP()), // Uses custom extractor
				slog.Bool("tunneled", isTunneled),
				slog.String("tunnel_provider", tunnelProvider),
				slog.String("user_agent", req.UserAgent()),
				slog.Int64("latency_ms", time.Since(start).Milliseconds()),
			}
			if err != nil {
				attrs = append(attrs, slog.Any("error", err))
			}

			c.apiLogger.LogAttrs(ctx.Request().Context(), slog.LevelInfo, "API Request", attrs...)

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
		{"search routes", c.initSearchRoutes},
		{"detection routes", c.initDetectionRoutes},
		{"analytics routes", c.initAnalyticsRoutes},
		{"weather routes", c.initWeatherRoutes},
		{"system routes", c.initSystemRoutes},
		{"settings routes", c.initSettingsRoutes},
		{"filesystem routes", c.initFileSystemRoutes},
		{"stream routes", c.initStreamRoutes},
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
	}

	for _, initializer := range routeInitializers {
		c.Debug("Initializing %s...", initializer.name)

		// Use a deferred function to recover from panics during route initialization
		func() {
			defer func() {
				if r := recover(); r != nil {
					c.logger.Printf("PANIC during %s initialization: %v", initializer.name, r)
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
	// Use BuildInfo interface methods for safe access
	version := runtimectx.UnknownValue
	buildDate := runtimectx.UnknownValue
	if c.Runtime != nil {
		version = c.Runtime.Version()
		buildDate = c.Runtime.BuildDate()
	}
	
	// Create response structure
	response := map[string]interface{}{
		"status":     "healthy",
		"version":    version,
		"build_date": buildDate,
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
	systemMetrics := make(map[string]interface{})

	// Add placeholder CPU usage (would be implemented with actual metrics in production)
	systemMetrics["cpu_usage"] = 0.0

	// Add placeholder memory usage
	memoryMetrics := map[string]interface{}{
		"used_percent": 0.0,
		"total_mb":     0.0,
		"used_mb":      0.0,
	}
	systemMetrics["memory"] = memoryMetrics

	// Add placeholder disk space
	diskMetrics := map[string]interface{}{
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

	// Close the API logger if it was initialized
	if c.apiLoggerClose != nil {
		if err := c.apiLoggerClose(); err != nil {
			c.logger.Printf("Error closing API log file: %v", err)
		}
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

	// Log the error with both the existing logger and the structured logger
	c.logger.Printf("API Error [%s] from %s (Tunneled: %v, Provider: %s): %s: %v",
		errorResp.CorrelationID, ip, isTunneled, tunnelProvider, message, err)

	// Also log to structured logger if available
	if c.apiLogger != nil {
		var errorStr string
		if err != nil {
			errorStr = err.Error()
		} else {
			errorStr = message
		}

		c.apiLogger.Error("API Error",
			"correlation_id", errorResp.CorrelationID,
			"message", message,
			"error", errorStr,
			"code", code,
			"path", ctx.Request().URL.Path,
			"method", ctx.Request().Method,
			"ip", ip, // Log the extracted IP
			"tunneled", isTunneled,
			"tunnel_provider", tunnelProvider,
		)
	}

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
func (c *Controller) Debug(format string, v ...interface{}) {
	if c.Settings.WebServer.Debug {
		msg := fmt.Sprintf(format, v...)
		c.logger.Printf("[DEBUG] %s", msg)

		// Also log to structured logger if available
		if c.apiLogger != nil {
			// No IP available here, log simple debug message
			c.apiLogger.Debug(msg)
		}
	}
}

// logAPIRequest is a helper to log API requests with common context fields.
func (c *Controller) logAPIRequest(ctx echo.Context, level slog.Level, msg string, args ...any) {
	if c.apiLogger == nil {
		return // Do nothing if logger isn't initialized
	}

	// Extract common context info
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path

	// Create base attributes
	baseAttrs := []any{
		"path", path,
		"ip", ip,
	}

	// Append specific attributes to base attributes
	baseAttrs = append(baseAttrs, args...) // Assign append result back to baseAttrs

	// Log at the specified level
	switch level {
	case slog.LevelDebug:
		c.apiLogger.Debug(msg, baseAttrs...)
	case slog.LevelInfo:
		c.apiLogger.Info(msg, baseAttrs...)
	case slog.LevelWarn:
		c.apiLogger.Warn(msg, baseAttrs...)
	case slog.LevelError:
		c.apiLogger.Error(msg, baseAttrs...)
	default:
		// Default to Info if level is unknown or custom (like Fatal)
		c.apiLogger.Log(ctx.Request().Context(), level, msg, baseAttrs...)
	}
}

// handleTokenAuth attempts authentication using a Bearer token from the Authorization header.
// It returns true if authentication succeeds.
// It returns false and a specific error (errMalformedAuthHeader, errInvalidAuthToken, errAuthServiceNil, or nil for no header)
// if authentication fails or is not attempted.
// It no longer writes the HTTP response directly.
func (c *Controller) handleTokenAuth(ctx echo.Context) (bool, error) {
	if c.AuthService == nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("handleTokenAuth called but AuthService is nil")
		}
		// Return a specific error indicating the internal config issue
		return false, errAuthServiceNil
	}

	authHeader := ctx.Request().Header.Get("Authorization")
	if authHeader == "" {
		return false, nil // No header, token auth not attempted, no error
	}

	parts := strings.Fields(authHeader)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		if c.apiLogger != nil {
			c.apiLogger.Warn("Invalid Authorization header format",
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		// Return specific error for malformed header
		return false, errMalformedAuthHeader
	}

	token := parts[1]
	validationErr := c.AuthService.ValidateToken(token) // Capture the error
	if validationErr == nil {
		if c.apiLogger != nil {
			c.apiLogger.Debug("Token authentication successful", "path", ctx.Request().URL.Path, "ip", ctx.RealIP())
		}
		return true, nil // Token validation successful
	}

	// Token validation failed
	if c.apiLogger != nil {
		c.apiLogger.Warn("Token validation failed",
			"error", validationErr.Error(), // Log the specific validation error
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}
	// Return specific error for invalid token
	return false, errInvalidAuthToken
}

// handleSessionAuth attempts authentication using the existing session.
// It returns true if authentication succeeds, false otherwise.
// It now uses the Controller's AuthService instance.
func (c *Controller) handleSessionAuth(ctx echo.Context) bool {
	if c.AuthService == nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("handleSessionAuth called but AuthService is nil")
		}
		return false // Cannot authenticate without a service
	}

	err := c.AuthService.CheckAccess(ctx)
	if err == nil {
		if c.apiLogger != nil {
			c.apiLogger.Debug("Session authentication successful", "path", ctx.Request().URL.Path, "ip", ctx.RealIP())
		}
		return true
	}

	if c.apiLogger != nil {
		c.apiLogger.Debug("Session authentication failed", "path", ctx.Request().URL.Path, "ip", ctx.RealIP(), "error", err.Error())
	}
	return false
}

// handleUnauthorized determines the appropriate response for unauthenticated requests.
func (c *Controller) handleUnauthorized(ctx echo.Context) error {
	acceptHeader := ctx.Request().Header.Get("Accept")
	isHXRequest := ctx.Request().Header.Get("HX-Request") == "true"
	isBrowserRequest := strings.Contains(acceptHeader, "text/html") || isHXRequest

	if isBrowserRequest {
		loginPath := "/login" // Assuming default login path is /login
		// Get the actual origin URL of the request
		originURL := ctx.Request().URL.String()
		// Compare against the actual request path, not the route pattern
		if !strings.HasPrefix(ctx.Request().URL.Path, loginPath) {
			// Append redirect parameter only if the current path is not the login path
			loginPath += "?redirect=" + url.QueryEscape(originURL)
		}

		if isHXRequest {
			ctx.Response().Header().Set("HX-Redirect", loginPath)
			return ctx.String(http.StatusUnauthorized, "")
		}
		return ctx.Redirect(http.StatusFound, loginPath)
	}

	// For API clients, return JSON error response
	// Add WWW-Authenticate header for RFC 6750 compliance, indicating Bearer scheme is expected
	ctx.Response().Header().Set("WWW-Authenticate", `Bearer realm="api"`)
	return ctx.JSON(http.StatusUnauthorized, map[string]string{
		"error": "Authentication required",
	})
}

// isAuthRequiredWithoutService checks if authentication would be required based on settings
// and IP bypass rules, even if the AuthService itself is nil. This is used as a fallback
// check in AuthMiddleware.
func (c *Controller) isAuthRequiredWithoutService(ctx echo.Context) bool {
	// Assume auth is required if any provider is enabled
	authWouldBeRequired := c.Settings.Security.BasicAuth.Enabled || c.Settings.Security.GoogleAuth.Enabled || c.Settings.Security.GithubAuth.Enabled

	// Check for subnet bypass only if auth would otherwise be required
	if authWouldBeRequired && c.Settings.Security.AllowSubnetBypass.Enabled {
		ipStr := ctx.RealIP()
		ip := net.ParseIP(ipStr)
		if ip != nil {
			if ip.IsLoopback() {
				// Loopback always bypasses
				authWouldBeRequired = false
			} else {
				// Check configured subnets
				allowedSubnetsStr := c.Settings.Security.AllowSubnetBypass.Subnet
				if allowedSubnetsStr != "" {
					allowedSubnets := strings.Split(allowedSubnetsStr, ",")
					for _, cidr := range allowedSubnets {
						trimmedCIDR := strings.TrimSpace(cidr)
						if trimmedCIDR == "" {
							continue
						}
						_, subnet, err := net.ParseCIDR(trimmedCIDR)
						if err == nil {
							if subnet.Contains(ip) {
								authWouldBeRequired = false // Bypass cancels requirement
								break                       // Found a match, exit inner loop
							}
						} else {
							// Log CIDR parsing errors
							if c.apiLogger != nil {
								c.apiLogger.Warn("Failed to parse CIDR string from AllowSubnetBypass settings",
									"cidr_string", trimmedCIDR,
									"error", err.Error(),
								)
							} else {
								// Fallback to standard logger if apiLogger is nil
								c.logger.Printf("[WARN] Failed to parse CIDR string \"%s\" from AllowSubnetBypass settings: %v", trimmedCIDR, err)
							}
						}
					}
				}
			}
		}
	}

	return authWouldBeRequired
}

// AuthMiddleware is a method that returns the auth middleware function
// This is now considered the *fallback* middleware if authMiddlewareFn is nil.
// It uses the Controller's stored AuthService instance.
func (c *Controller) AuthMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) error {
		// Use the stored AuthService instance
		authService := c.AuthService

		if authService == nil {
			// If service is nil (should only happen if auth wasn't configured properly),
			// check if auth *would* have been required based on settings and IP bypass.
			if c.isAuthRequiredWithoutService(ctx) {
				if c.apiLogger != nil {
					c.apiLogger.Error("AuthMiddleware called but AuthService is nil, denying access",
						"path", ctx.Request().URL.Path,
						"ip", ctx.RealIP(),
					)
				}
				return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": "Internal auth configuration error"})
			}

			// Otherwise, if auth wouldn't have been required anyway, allow access.
			if c.apiLogger != nil {
				c.apiLogger.Warn("AuthMiddleware called but AuthService is nil; auth not required, allowing access",
					"path", ctx.Request().URL.Path,
					"ip", ctx.RealIP(),
				)
			}
			ctx.Set("isAuthenticated", false)
			ctx.Set("authMethod", auth.AuthMethodUnknown) // Use defined enum for 'none'
			return next(ctx)
		}

		// Skip auth check if auth is not required for this client IP
		if !authService.IsAuthRequired(ctx) {
			if c.apiLogger != nil {
				c.apiLogger.Debug("Authentication not required for this request", "path", ctx.Request().URL.Path, "ip", ctx.RealIP())
			}
			ctx.Set("isAuthenticated", false)
			ctx.Set("authMethod", auth.AuthMethodUnknown) // Use defined enum for 'none'
			return next(ctx)
		}

		// Try token authentication first
		authenticated, tokenErr := c.handleTokenAuth(ctx)
		if authenticated {
			// Token auth successful
			ctx.Set("isAuthenticated", true)
			// NOTE: Cannot reliably get username from opaque token via current AuthService.
			// Username will be empty unless the session also exists and contains it.
			// ctx.Set("username", authService.GetUsername(ctx)) // Removed this line
			ctx.Set("authMethod", auth.AuthMethodToken) // Store enum directly
			return next(ctx)
		}

		// Handle errors from token authentication attempt
		if tokenErr != nil {
			switch { // Use switch without true (shorthand)
			case errors.Is(tokenErr, errAuthServiceNil):
				// Logged in handleTokenAuth already
				return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": "Internal auth configuration error"})
			case errors.Is(tokenErr, errMalformedAuthHeader):
				// Logged in handleTokenAuth already
				ctx.Response().Header().Set("WWW-Authenticate", `Bearer realm="api"`)
				return ctx.JSON(http.StatusUnauthorized, map[string]string{"error": "Invalid Authorization header"})
			case errors.Is(tokenErr, errInvalidAuthToken):
				// Logged in handleTokenAuth already
				ctx.Response().Header().Set("WWW-Authenticate", `Bearer realm="api", error="invalid_token", error_description="Invalid or expired token"`)
				return ctx.JSON(http.StatusUnauthorized, map[string]string{"error": "Invalid or expired token"})
			default:
				// Handle unexpected errors from handleTokenAuth if any arise
				if c.apiLogger != nil {
					c.apiLogger.Error("Unexpected error during token authentication", "error", tokenErr)
				}
				return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": "Internal server error during authentication"})
			}
		}

		// Token auth not attempted (no header) or failed, try session auth
		if c.handleSessionAuth(ctx) {
			ctx.Set("isAuthenticated", true)
			ctx.Set("username", authService.GetUsername(ctx))
			ctx.Set("authMethod", auth.AuthMethodBrowserSession) // Use defined enum for session
			return next(ctx)
		}

		// Authentication failed completely, handle unauthorized response
		return c.handleUnauthorized(ctx)
	}
}

// getEffectiveAuthMiddleware returns the appropriate authentication middleware function.
// It prioritizes the configured authMiddlewareFn and falls back to the
// controller's AuthMiddleware method if the function is not set.
func (c *Controller) getEffectiveAuthMiddleware() echo.MiddlewareFunc {
	if c.authMiddlewareFn != nil {
		if c.apiLogger != nil {
			c.apiLogger.Info("Using configured authMiddlewareFn for route protection")
		}
		return c.authMiddlewareFn
	} else {
		if c.apiLogger != nil {
			c.apiLogger.Warn("authMiddlewareFn not configured, using fallback AuthMiddleware method for route protection")
		}
		return c.AuthMiddleware // Return the method itself
	}
}

// InitializeAPI creates a new API controller and registers all routes
// It now accepts the OAuth2Server instance directly.
func InitializeAPI(e *echo.Echo, ds datastore.Interface, settings *conf.Settings, runtimeCtx *runtimectx.Context,
	birdImageCache *imageprovider.BirdImageCache, sunCalc *suncalc.SunCalc,
	controlChan chan string, logger *log.Logger, proc *processor.Processor,
	oauth2Server *security.OAuth2Server, metrics *observability.Metrics) (*Controller, error) { // Return error instead of Fatalf

	// Create API controller, passing oauth2Server and metrics directly to New
	apiController, err := New(e, ds, settings, runtimeCtx, birdImageCache, sunCalc, controlChan, logger, oauth2Server, metrics)
	if err != nil {
		return nil, err
	}

	// Assign processor after initialization
	apiController.Processor = proc

	// Log initialization using BuildInfo interface methods
	if apiController.apiLogger != nil {
		version := runtimectx.UnknownValue
		buildDate := runtimectx.UnknownValue
		if runtimeCtx != nil {
			version = runtimeCtx.Version()
			buildDate = runtimeCtx.BuildDate()
		}
		apiController.apiLogger.Info("API v2 initialized",
			"version", version,
			"build_date", buildDate,
		)
	}

	return apiController, nil
}
