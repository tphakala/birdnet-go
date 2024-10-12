// internal/httpcontroller/server.go
package httpcontroller

import (
	"fmt"
	"log"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/httpcontroller/handlers"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/suncalc"
	"golang.org/x/crypto/acme/autocert"
)

// Server encapsulates Echo server and related configurations.
type Server struct {
	Echo              *echo.Echo
	DS                datastore.Interface
	Settings          *conf.Settings
	DashboardSettings *conf.Dashboard
	Logger            *logger.Logger
	BirdImageCache    *imageprovider.BirdImageCache
	Handlers          *handlers.Handlers
	pageRoutes        map[string]PageRouteConfig
	partialRoutes     map[string]PartialRouteConfig
	SunCalc           *suncalc.SunCalc            // SunCalc instance for calculating sun event times
	AudioLevelChan    chan myaudio.AudioLevelData // Channel for audio level updates
}

// New initializes a new HTTP server with given context and datastore.
func New(settings *conf.Settings, dataStore datastore.Interface, birdImageCache *imageprovider.BirdImageCache, audioLevelChan chan myaudio.AudioLevelData) *Server {
	configureDefaultSettings(settings)

	s := &Server{
		Echo:              echo.New(),
		DS:                dataStore,
		Settings:          settings,
		BirdImageCache:    birdImageCache,
		AudioLevelChan:    audioLevelChan,
		DashboardSettings: &settings.Realtime.Dashboard,
	}

	s.initLogger()

	// Initialize SunCalc for calculating sun event times
	s.SunCalc = suncalc.NewSunCalc(settings.BirdNET.Latitude, settings.BirdNET.Longitude)

	// Initialize handlers
	s.Handlers = handlers.New(s.DS, s.Settings, s.DashboardSettings, s.BirdImageCache, nil, s.SunCalc, s.AudioLevelChan)

	s.initializeServer()
	return s
}

// Start begins listening and serving HTTP requests.
func (s *Server) Start() {
	errChan := make(chan error)

	go func() {
		var err error

		if s.Settings.WebServer.AutoTLS {
			configPaths, configErr := conf.GetDefaultConfigPaths()
			if configErr != nil {
				errChan <- fmt.Errorf("failed to get config paths: %w", configErr)
				return
			}

			s.Echo.AutoTLSManager.Prompt = autocert.AcceptTOS
			s.Echo.AutoTLSManager.Cache = autocert.DirCache(configPaths[0])
			s.Echo.AutoTLSManager.HostPolicy = autocert.HostWhitelist("") // Adjust as needed

			err = s.Echo.StartAutoTLS(":" + s.Settings.WebServer.Port)
		} else {
			err = s.Echo.Start(":" + s.Settings.WebServer.Port)
		}

		if err != nil {
			errChan <- err
		}
	}()

	go handleServerError(errChan)

	fmt.Printf("HTTP server started on port %s (AutoTLS: %v)\n", s.Settings.WebServer.Port, s.Settings.WebServer.AutoTLS)
}

// initializeServer configures and initializes the server.
func (s *Server) initializeServer() {
	s.Echo.HideBanner = true
	s.initLogger()
	s.configureMiddleware()
	s.initRoutes()
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
