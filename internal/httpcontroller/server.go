// internal/httpcontroller/server.go
package httpcontroller

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/httpcontroller/handlers"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/security"
	"github.com/tphakala/birdnet-go/internal/suncalc"
	"golang.org/x/crypto/acme/autocert"
)

// Server encapsulates Echo server and related configurations.
type Server struct {
	Echo              *echo.Echo
	DS                datastore.Interface
	Settings          *conf.Settings
	OAuth2Server      *security.OAuth2Server
	CloudflareAccess  *security.CloudflareAccess
	DashboardSettings *conf.Dashboard
	Logger            *logger.Logger
	BirdImageCache    *imageprovider.BirdImageCache
	Handlers          *handlers.Handlers
	SunCalc           *suncalc.SunCalc
	AudioLevelChan    chan myaudio.AudioLevelData
	controlChan       chan string
	notificationChan  chan handlers.Notification
	Processor         *processor.Processor
	APIV2             *api.Controller // Our new JSON API

	// Page and partial routes
	pageRoutes    map[string]PageRouteConfig
	partialRoutes map[string]PartialRouteConfig
}

// New initializes a new HTTP server with given context and datastore.
func New(settings *conf.Settings, dataStore datastore.Interface, birdImageCache *imageprovider.BirdImageCache, audioLevelChan chan myaudio.AudioLevelData, controlChan chan string, proc *processor.Processor) *Server {
	configureDefaultSettings(settings)

	s := &Server{
		Echo:              echo.New(),
		DS:                dataStore,
		Settings:          settings,
		BirdImageCache:    birdImageCache,
		AudioLevelChan:    audioLevelChan,
		DashboardSettings: &settings.Realtime.Dashboard,
		OAuth2Server:      security.NewOAuth2Server(),
		CloudflareAccess:  security.NewCloudflareAccess(),
		controlChan:       controlChan,
		notificationChan:  make(chan handlers.Notification, 10),
		Processor:         proc,
	}

	// Configure an IP extractor
	s.Echo.IPExtractor = echo.ExtractIPFromXFFHeader()

	// Initialize SunCalc for calculating sun event times
	s.SunCalc = suncalc.NewSunCalc(settings.BirdNET.Latitude, settings.BirdNET.Longitude)

	// Initialize handlers
	s.Handlers = handlers.New(s.DS, s.Settings, s.DashboardSettings, s.BirdImageCache, nil, s.SunCalc, s.AudioLevelChan, s.OAuth2Server, s.controlChan, s.notificationChan, s)

	// Add processor middleware
	s.Echo.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set("processor", s.Processor)
			return next(c)
		}
	})

	s.initializeServer()

	// Set the logger in handlers after it's initialized
	if s.Logger != nil {
		s.Handlers.SetLogger(s.Logger)
	}

	return s
}

// NewWithLogger initializes a new HTTP server with an explicit logger.
// This is the preferred constructor as it ensures proper logger hierarchy.
func NewWithLogger(settings *conf.Settings, dataStore datastore.Interface, birdImageCache *imageprovider.BirdImageCache, audioLevelChan chan myaudio.AudioLevelData, controlChan chan string, proc *processor.Processor, parentLogger *logger.Logger) *Server {
	configureDefaultSettings(settings)

	// Create a server-specific logger
	var serverLogger *logger.Logger
	if parentLogger != nil {
		serverLogger = parentLogger.Named("server")
	} else {
		// Fall back to global logger with proper naming if no parent logger provided
		serverLogger = logger.Named("server")
	}

	s := &Server{
		Echo:              echo.New(),
		DS:                dataStore,
		Settings:          settings,
		BirdImageCache:    birdImageCache,
		AudioLevelChan:    audioLevelChan,
		DashboardSettings: &settings.Realtime.Dashboard,
		OAuth2Server:      security.NewOAuth2Server(),
		CloudflareAccess:  security.NewCloudflareAccess(),
		controlChan:       controlChan,
		notificationChan:  make(chan handlers.Notification, 10),
		Processor:         proc,
		Logger:            serverLogger,
	}

	// Configure an IP extractor
	s.Echo.IPExtractor = echo.ExtractIPFromXFFHeader()

	// Initialize SunCalc for calculating sun event times
	s.SunCalc = suncalc.NewSunCalc(settings.BirdNET.Latitude, settings.BirdNET.Longitude)

	// Initialize handlers with a child logger
	s.Handlers = handlers.New(s.DS, s.Settings, s.DashboardSettings, s.BirdImageCache, nil, s.SunCalc, s.AudioLevelChan, s.OAuth2Server, s.controlChan, s.notificationChan, s)

	// Set our custom logger in the handlers
	s.Handlers.SetLogger(s.Logger.Named("handlers"))

	// Add processor middleware
	s.Echo.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set("processor", s.Processor)
			return next(c)
		}
	})

	s.initializeServer()

	serverLogger.Info("HTTP server initialized", "port", s.Settings.WebServer.Port)

	return s
}

// configureDefaultSettings sets default values for server settings.
func configureDefaultSettings(settings *conf.Settings) {
	if settings.WebServer.Port == "" {
		settings.WebServer.Port = "8080"
	}
}

// Start begins listening and serving HTTP requests.
func (s *Server) Start() {
	errChan := make(chan error)

	go func() {
		var err error

		if s.Settings.Security.AutoTLS {
			configPaths, configErr := conf.GetDefaultConfigPaths()
			if configErr != nil {
				errChan <- fmt.Errorf("failed to get config paths: %w", configErr)
				return
			}

			s.Echo.AutoTLSManager.Prompt = autocert.AcceptTOS
			s.Echo.AutoTLSManager.Cache = autocert.DirCache(configPaths[0])
			s.Echo.AutoTLSManager.HostPolicy = autocert.HostWhitelist(s.Settings.Security.Host)

			err = s.Echo.StartAutoTLS(":" + s.Settings.WebServer.Port)
		} else {
			err = s.Echo.Start(":" + s.Settings.WebServer.Port)
		}

		if err != nil {
			errChan <- err
		}
	}()

	go s.handleServerError(errChan)

	s.Logger.Info("HTTP server started", "port", s.Settings.WebServer.Port, "autotls", s.Settings.Security.AutoTLS)
}

func (s *Server) isAuthenticationEnabled(c echo.Context) bool {
	return s.Handlers.OAuth2Server.IsAuthenticationEnabled(s.RealIP(c))
}

func (s *Server) IsAccessAllowed(c echo.Context) bool {
	// First check Cloudflare Access JWT
	if s.CloudflareAccess.IsEnabled(c) {
		return true
	}

	return s.OAuth2Server.IsUserAuthenticated(c)
}

func (s *Server) RealIP(c echo.Context) string {
	// If Cloudflare Access is enabled, prioritize CF-Connecting-IP
	if s.CloudflareAccess.IsEnabled(c) {
		if cfIP := c.Request().Header.Get("CF-Connecting-IP"); cfIP != "" {
			return strings.TrimSpace(cfIP)
		}
	}

	// Get the X-Forwarded-For header
	if xff := c.Request().Header.Get("X-Forwarded-For"); xff != "" {
		// Split and get the first IP in the chain
		ips := strings.Split(xff, ",")
		if len(ips) > 0 {
			// Take the last IP which should be the original client IP
			return strings.TrimSpace(ips[len(ips)-1])
		}
	}

	// Fallback to direct RemoteAddr
	ip, _, _ := net.SplitHostPort(c.Request().RemoteAddr)
	return ip
}

// initializeServer configures and initializes the server.
func (s *Server) initializeServer() {
	s.Echo.HideBanner = true
	s.initLogger()
	s.configureMiddleware()

	// Initialize handlers after the logger is initialized
	if s.Handlers != nil && s.Logger != nil {
		s.Handlers.SetLogger(s.Logger)
	}

	s.initRoutes()

	// Initialize the JSON API v2
	s.Debug("Initializing JSON API v2")
	s.APIV2 = api.InitializeAPI(
		s.Echo,
		s.DS,
		s.Settings,
		s.BirdImageCache,
		s.SunCalc,
		s.controlChan,
		s.Logger.Named("api.v2"),
	)

	// Add the server to Echo context for API v2 authentication
	s.Echo.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Add server as a context value for API v2 to access authentication methods
			if strings.HasPrefix(c.Path(), "/api/v2/") {
				c.Set("server", s)
			}
			return next(c)
		}
	})
}

// isDevMode checks if the application is running in development mode
func (s *Server) isDevMode() bool {
	// Check for environment variable first
	envVal := strings.ToLower(os.Getenv("BIRDNET_GO_DEV"))
	if envVal == "true" || envVal == "1" || envVal == "yes" {
		return true
	}

	// Fall back to configuration setting
	return s.Settings.WebServer.Debug
}

// handleServerError listens for server errors and handles them.
func (s *Server) handleServerError(errChan chan error) {
	for err := range errChan {
		if s.Logger != nil {
			s.Logger.Error("Server error", "error", err)
		}
		// Additional error handling logic here
	}
}

// getHttpLogger returns a component-specific logger for HTTP operations
// It creates a hierarchical structure (e.g., http.api.v1)
func (s *Server) getHttpLogger(component string) *logger.Logger {
	if s.Logger == nil {
		return nil
	}
	return s.Logger.Named("http." + component)
}

// getApiLogger returns a component-specific logger for API operations
// It creates a hierarchical structure (e.g., api.v1.detection)
func (s *Server) getApiLogger(version, component string) *logger.Logger {
	if s.Logger == nil {
		return nil
	}

	path := "api"
	if version != "" {
		path += "." + version
	}

	if component != "" {
		path += "." + component
	}

	return s.Logger.Named(path)
}

// createRequestContextLogger creates a request-specific logger with all relevant request details
// This is different from getRequestLogger in middleware.go which creates a logger from a component logger
func (s *Server) createRequestContextLogger(c echo.Context) *logger.Logger {
	if s.Logger == nil {
		return nil
	}

	// Generate request ID if not present
	requestID := c.Request().Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = c.Response().Header().Get("X-Request-ID")
	}

	// Determine logger component path based on request path
	path := c.Path()
	var loggerComponent string

	switch {
	case strings.HasPrefix(path, "/api/v1"):
		loggerComponent = "api.v1"
	case strings.HasPrefix(path, "/api/v2"):
		loggerComponent = "api.v2"
	case strings.HasPrefix(path, "/dashboard"):
		loggerComponent = "http.dashboard"
	case strings.HasPrefix(path, "/settings"):
		loggerComponent = "http.settings"
	default:
		loggerComponent = "http.request"
	}

	// Create the logger with the right component path
	requestLogger := s.Logger.Named(loggerComponent)

	// Add request-specific context
	return requestLogger.With(
		"request_id", requestID,
		"client_ip", s.RealIP(c),
		"method", c.Request().Method,
		"path", path,
	)
}
