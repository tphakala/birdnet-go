// internal/api/v2/api.go
package api

import (
	"crypto/rand"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/patrickmn/go-cache"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
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
}

// New creates a new API controller
func New(e *echo.Echo, ds datastore.Interface, settings *conf.Settings,
	birdImageCache *imageprovider.BirdImageCache, sunCalc *suncalc.SunCalc,
	controlChan chan string, logger *log.Logger) *Controller {

	if logger == nil {
		logger = log.Default()
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
	}

	// Create v2 API group
	c.Group = e.Group("/api/v2")

	// Configure middlewares
	c.Group.Use(middleware.Logger())
	c.Group.Use(middleware.Recover())
	c.Group.Use(middleware.CORS())

	// Initialize start time for uptime tracking
	now := time.Now()
	c.startTime = &now

	// Initialize routes
	c.initRoutes()

	return c
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
	c.logger.Printf("API Error [%s]: %s: %v", errorResp.CorrelationID, message, err)
	return ctx.JSON(code, errorResp)
}

// Debug logs debug messages when debug mode is enabled
func (c *Controller) Debug(format string, v ...interface{}) {
	if c.Settings.WebServer.Debug {
		c.logger.Printf(format, v...)
	}
}
