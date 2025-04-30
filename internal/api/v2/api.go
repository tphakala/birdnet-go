// internal/api/v2/api.go
package api

import (
	"crypto/rand"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"log/slog"
	"net"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/patrickmn/go-cache"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/httpcontroller/securefs"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// Controller manages the API routes and handlers
type Controller struct {
	Echo                *echo.Echo
	Group               *echo.Group
	DS                  datastore.Interface
	Settings            *conf.Settings
	BirdImageCache      *imageprovider.BirdImageCache
	SunCalc             *suncalc.SunCalc
	Processor           *processor.Processor
	logger              *log.Logger
	controlChan         chan string
	speciesExcludeMutex sync.RWMutex // Mutex for species exclude list operations
	settingsMutex       sync.RWMutex // Mutex for settings operations
	detectionCache      *cache.Cache // Cache for detection queries
	startTime           *time.Time
	SFS                 *securefs.SecureFS // Add SecureFS instance
	apiLogger           *slog.Logger       // Structured logger for API operations
	apiLoggerClose      func() error       // Function to close the log file
}

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
func New(e *echo.Echo, ds datastore.Interface, settings *conf.Settings,
	birdImageCache *imageprovider.BirdImageCache, sunCalc *suncalc.SunCalc,
	controlChan chan string, logger *log.Logger) (*Controller, error) {

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

	c := &Controller{
		Echo:           e,
		DS:             ds,
		Settings:       settings,
		BirdImageCache: birdImageCache,
		SunCalc:        sunCalc,
		controlChan:    controlChan,
		logger:         logger,
		detectionCache: cache.New(5*time.Minute, 10*time.Minute),
		SFS:            sfs, // Assign SecureFS instance
	}

	// Initialize structured logger for API requests
	apiLogPath := "logs/web.log"
	apiLogger, closeFunc, err := logging.NewFileLogger(apiLogPath, "api", slog.LevelInfo)
	if err != nil {
		logger.Printf("Warning: Failed to initialize API structured logger: %v", err)
		// Continue without structured logging rather than failing completely
	} else {
		c.apiLogger = apiLogger
		c.apiLoggerClose = closeFunc
		logger.Printf("API structured logging initialized to %s", apiLogPath)
	}

	// Create v2 API group
	c.Group = e.Group("/api/v2")

	// Configure middlewares
	c.Group.Use(middleware.Recover())          // Recover should be early
	c.Group.Use(c.TunnelDetectionMiddleware()) // Add tunnel detection **before** logging
	// c.Group.Use(middleware.Logger())        // Removed: Use custom LoggingMiddleware below for structured logging
	c.Group.Use(middleware.CORS())     // CORS handling
	c.Group.Use(c.LoggingMiddleware()) // Use custom structured logging middleware

	// Initialize start time for uptime tracking
	now := time.Now()
	c.startTime = &now

	// Initialize routes
	c.initRoutes()

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
			attrs := []any{
				"method", req.Method,
				"path", req.URL.Path,
				"query", req.URL.RawQuery,
				"status", res.Status,
				"ip", ctx.RealIP(), // Uses custom extractor
				"tunneled", isTunneled,
				"tunnel_provider", tunnelProvider,
				"user_agent", req.UserAgent(),
				"latency_ms", time.Since(start).Milliseconds(),
			}
			if err != nil {
				attrs = append(attrs, "error", err)
			}

			c.apiLogger.Info("API Request", attrs...)

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
		{"stream routes", c.initStreamRoutes},
		{"integration routes", c.initIntegrationsRoutes},
		{"control routes", c.initControlRoutes},
		{"auth routes", c.initAuthRoutes},
		{"media routes", c.initMediaRoutes},
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
	// Create response structure
	response := map[string]interface{}{
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
	// Close the API logger if it was initialized
	if c.apiLoggerClose != nil {
		if err := c.apiLoggerClose(); err != nil {
			c.logger.Printf("Error closing API log file: %v", err)
		}
	}

	// Call shutdown methods of individual components
	// Currently, only the system component needs cleanup
	StopCPUMonitoring()

	// Log shutdown
	c.Debug("API Controller shutting down, CPU monitoring stopped")
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

	return &ErrorResponse{
		Error:         err.Error(),
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
		c.apiLogger.Error("API Error",
			"correlation_id", errorResp.CorrelationID,
			"message", message,
			"error", err.Error(),
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

// InitializeAPI creates a new API controller and registers all routes
func InitializeAPI(e *echo.Echo, ds datastore.Interface, settings *conf.Settings,
	birdImageCache *imageprovider.BirdImageCache, sunCalc *suncalc.SunCalc,
	controlChan chan string, logger *log.Logger, proc *processor.Processor) *Controller {

	// Create API controller
	apiController, err := New(e, ds, settings, birdImageCache, sunCalc, controlChan, logger)
	if err != nil {
		logger.Fatalf("Failed to initialize API: %v", err)
	}

	// Assign processor after initialization
	apiController.Processor = proc

	// Log initialization
	if apiController.apiLogger != nil {
		apiController.apiLogger.Info("API v2 initialized",
			"version", settings.Version,
			"build_date", settings.BuildDate,
		)
	}

	return apiController
}
