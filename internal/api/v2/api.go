// internal/api/v2/api.go
package api

import (
	"log"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// Controller manages the API routes and handlers
type Controller struct {
	Echo           *echo.Echo
	Group          *echo.Group
	DS             datastore.Interface
	Settings       *conf.Settings
	BirdImageCache *imageprovider.BirdImageCache
	SunCalc        *suncalc.SunCalc
	logger         *log.Logger
	controlChan    chan string
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

	// Initialize detection routes
	c.initDetectionRoutes()

	// Analytics routes - for statistics and data analysis
	c.initAnalyticsRoutes()

	// Weather routes - for weather data and detection conditions
	c.initWeatherRoutes()

	// System routes (for hardware and software information) - protected
	c.initSystemRoutes()

	// Settings routes (for application configuration) - protected
	c.initSettingsRoutes()

	// Stream routes (for real-time data) - protected
	c.initStreamRoutes()

	// Integration routes (for external services) - protected
	c.initIntegrationsRoutes()

	// Control routes (for application control) - protected
	c.initControlRoutes()

	// Authentication routes - partially protected based on their implementation
	c.initAuthRoutes()

	// Initialize media routes - protected
	c.initMediaRoutes()
}

// HealthCheck handles the API health check endpoint
func (c *Controller) HealthCheck(ctx echo.Context) error {
	return ctx.JSON(http.StatusOK, map[string]string{
		"status": "healthy",
	})
}

// Error response structure
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// NewErrorResponse creates a new API error response
func NewErrorResponse(err error, message string, code int) *ErrorResponse {
	return &ErrorResponse{
		Error:   err.Error(),
		Message: message,
		Code:    code,
	}
}

// HandleError constructs and returns an appropriate error response
func (c *Controller) HandleError(ctx echo.Context, err error, message string, code int) error {
	c.logger.Printf("API Error: %s: %v", message, err)
	return ctx.JSON(code, NewErrorResponse(err, message, code))
}

// Debug logs debug messages when debug mode is enabled
func (c *Controller) Debug(format string, v ...interface{}) {
	if c.Settings.WebServer.Debug {
		c.logger.Printf(format, v...)
	}
}
