// internal/httpcontroller/server.go
package httpcontroller

import (
	"fmt"
	"log"
	"net"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
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

	// Page and partial routes
	pageRoutes    map[string]PageRouteConfig
	partialRoutes map[string]PartialRouteConfig
}

// New initializes a new HTTP server with given context and datastore.
func New(settings *conf.Settings, dataStore datastore.Interface, birdImageCache *imageprovider.BirdImageCache, audioLevelChan chan myaudio.AudioLevelData, controlChan chan string) *Server {
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
	}

	// Configure an IP extractor
	s.Echo.IPExtractor = echo.ExtractIPFromXFFHeader()

	// Initialize SunCalc for calculating sun event times
	s.SunCalc = suncalc.NewSunCalc(settings.BirdNET.Latitude, settings.BirdNET.Longitude)

	// Initialize handlers
	s.Handlers = handlers.New(s.DS, s.Settings, s.DashboardSettings, s.BirdImageCache, nil, s.SunCalc, s.AudioLevelChan, s.OAuth2Server, s.controlChan, s.notificationChan, s)

	s.initializeServer()
	return s
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

	go handleServerError(errChan)

	fmt.Printf("HTTP server started on port %s (AutoTLS: %v)\n", s.Settings.WebServer.Port, s.Settings.Security.AutoTLS)
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
	s.initRoutes()
	s.initAuthRoutes()
	s.initBackupRoutes()
}

// configureDefaultSettings sets default values for server settings.
func configureDefaultSettings(settings *conf.Settings) {
	if settings.WebServer.Port == "" {
		settings.WebServer.Port = "8080"
	}
}

// handleServerError listens for server errors and handles them.
func handleServerError(errChan chan error) {
	for err := range errChan {
		log.Printf("Server error: %v", err)
		// Additional error handling logic here
	}
}

// initLogger initializes the custom logger.
func (s *Server) initLogger() {
	if !s.Settings.WebServer.Log.Enabled {
		fmt.Println("Logging disabled")
		return
	}

	fileHandler := &logger.DefaultFileHandler{}
	if err := fileHandler.Open(s.Settings.WebServer.Log.Path); err != nil {
		log.Fatal(err) // Use standard log here as logger isn't initialized yet
	}

	// Convert conf.RotationType to logger.RotationType
	var loggerRotationType logger.RotationType
	switch s.Settings.WebServer.Log.Rotation {
	case conf.RotationDaily:
		loggerRotationType = logger.RotationDaily
	case conf.RotationWeekly:
		loggerRotationType = logger.RotationWeekly
	case conf.RotationSize:
		loggerRotationType = logger.RotationSize
	default:
		log.Fatal("Invalid rotation type")
	}

	// Create rotation settings
	rotationSettings := logger.Settings{
		RotationType: loggerRotationType,
		MaxSize:      s.Settings.WebServer.Log.MaxSize,
		RotationDay:  s.Settings.WebServer.Log.RotationDay,
	}

	s.Logger = logger.NewLogger(map[string]logger.LogOutput{
		"web":    logger.FileOutput{Handler: fileHandler},
		"stdout": logger.StdoutOutput{},
	}, true, rotationSettings)

	// Set Echo's Logger to use the custom logger
	s.Echo.Logger.SetOutput(s.Logger)

	// Set Echo's logging format
	s.Echo.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogURI:      true,
		LogStatus:   true,
		LogRemoteIP: true,
		LogMethod:   true,
		LogError:    true,
		HandleError: true,
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			// Use your custom logger here
			s.Logger.Info("web", "%s %v %s %d %v", v.RemoteIP, v.Method, v.URI, v.Status, v.Error)
			return nil
		},
	}))
}

// Debug logs debug messages if debug mode is enabled
func (s *Server) Debug(format string, v ...interface{}) {
	if s.Settings.WebServer.Debug {
		if len(v) == 0 {
			log.Print(format)
		} else {
			log.Printf(format, v...)
		}
	}
}
