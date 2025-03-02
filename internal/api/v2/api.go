// internal/api/v2/api.go
package api

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/patrickmn/go-cache"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
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
	logger              *logger.Logger
	controlChan         chan string
	speciesExcludeMutex sync.RWMutex // Mutex for species exclude list operations
	settingsMutex       sync.RWMutex // Mutex for settings operations
	detectionCache      *cache.Cache // Cache for detection queries
}

// New creates a new API controller
func New(e *echo.Echo, ds datastore.Interface, settings *conf.Settings,
	birdImageCache *imageprovider.BirdImageCache, sunCalc *suncalc.SunCalc,
	controlChan chan string, logger *logger.Logger) *Controller {

	c := &Controller{
		Echo:           e,
		DS:             ds,
		Settings:       settings,
		BirdImageCache: birdImageCache,
		SunCalc:        sunCalc,
		controlChan:    controlChan,
		logger:         logger,
	}

	// Create v2 API group
	c.Group = e.Group("/api/v2")

	// Configure middlewares
	c.Group.Use(middleware.Logger())
	c.Group.Use(middleware.Recover())
	c.Group.Use(middleware.CORS())

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
					c.logger.Error("Panic during route initialization",
						"route", initializer.name,
						"error", r)
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
	return ctx.JSON(http.StatusOK, map[string]string{
		"status":     "healthy",
		"version":    c.Settings.Version,
		"build_date": c.Settings.BuildDate,
	})
}

// InitializeAPI sets up the JSON API endpoints in the provided Echo instance
// The returned Controller has a Shutdown method that should be called during application shutdown
// to properly clean up resources and stop background goroutines
func InitializeAPI(
	e *echo.Echo,
	ds datastore.Interface,
	settings *conf.Settings,
	birdImageCache *imageprovider.BirdImageCache,
	sunCalc *suncalc.SunCalc,
	controlChan chan string,
	loggerInstance *logger.Logger,
) *Controller {
	// Get a named API logger
	var apiLogger *logger.Logger

	if loggerInstance != nil {
		// Use the provided logger and create a component-specific logger
		apiLogger = loggerInstance.Named("api.v2")
	} else {
		// Use the global logger if no specific logger was provided
		apiLogger = logger.Named("api.v2")
	}

	// Create new API controller with our structured logger
	apiController := New(e, ds, settings, birdImageCache, sunCalc, controlChan, apiLogger)

	// Log API initialization using the structured logger
	apiLogger.Info("JSON API v2 initialized", "endpoint", "/api/v2")

	return apiController
}

// Shutdown performs cleanup of all resources used by the API controller
// This should be called when the application is shutting down
func (c *Controller) Shutdown() {
	// Call shutdown methods of individual components
	// Currently, only the system component needs cleanup
	StopCPUMonitoring()

	// Log shutdown using structured logger
	c.logger.Debug("API Controller shutting down", "message", "CPU monitoring stopped")
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

	c.logger.Error("API error",
		"correlation_id", errorResp.CorrelationID,
		"message", message,
		"error", err,
		"code", code)

	return ctx.JSON(code, errorResp)
}

// Debug logs debug messages when debug mode is enabled
func (c *Controller) Debug(format string, v ...interface{}) {
	if !c.Settings.WebServer.Debug {
		return
	}

	// Format the values if needed
	var msg string
	if len(v) > 0 {
		msg = fmt.Sprintf(format, v...)
	} else {
		msg = format
	}

	c.logger.Debug(msg)
}

// LogfError is a helper for logging errors with Printf-style formatting using the structured logger
func (c *Controller) LogfError(format string, v ...interface{}) {
	c.logger.Error(fmt.Sprintf(format, v...))
}

// LogfWarn is a helper for logging warnings with Printf-style formatting using the structured logger
func (c *Controller) LogfWarn(format string, v ...interface{}) {
	c.logger.Warn(fmt.Sprintf(format, v...))
}

// LogfInfo is a helper for logging info with Printf-style formatting using the structured logger
func (c *Controller) LogfInfo(format string, v ...interface{}) {
	c.logger.Info(fmt.Sprintf(format, v...))
}

// LogfDebug is a helper for logging debug messages with Printf-style formatting using the structured logger
func (c *Controller) LogfDebug(format string, v ...interface{}) {
	if !c.Settings.WebServer.Debug {
		return
	}

	c.logger.Debug(fmt.Sprintf(format, v...))
}
