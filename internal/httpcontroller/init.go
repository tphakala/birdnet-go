package httpcontroller

import (
	"fmt"
	"html/template"
	"io"
	"log"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/crypto/acme/autocert"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// TemplateRenderer is a custom HTML template renderer for Echo framework.
type TemplateRenderer struct {
	templates *template.Template
}

// Render renders a template with the given data.
func (t *TemplateRenderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	return t.templates.ExecuteTemplate(w, name, data)
}

// Server encapsulates Echo server and related configurations.
type Server struct {
	Echo              *echo.Echo          // Echo framework instance
	ds                datastore.Interface // Datastore interface
	Settings          *conf.Settings      // Application settings
	DashboardSettings *conf.Dashboard     // Dashboard settings
	Logger            *logger.Logger      // Custom logger
	BirdImageCache    *imageprovider.BirdImageCache
}

// New initializes a new HTTP server with given context and datastore.
func New(settings *conf.Settings, dataStore datastore.Interface, birdImageCache *imageprovider.BirdImageCache) *Server {
	// Default port configuration
	configureDefaultSettings(settings)

	s := &Server{
		Echo:              echo.New(),
		ds:                dataStore,
		Settings:          settings,
		BirdImageCache:    birdImageCache,
		DashboardSettings: &settings.Realtime.Dashboard,
	}

	// Server initialization
	s.initializeServer()

	// Conditional AutoTLS setup with improved error handling and readability
	errChan := make(chan error)

	go func() {
		var err error // Declare err variable at the top of the goroutine scope

		if settings.WebServer.AutoTLS {
			// Attempt to configure AutoTLS
			configPaths, configErr := conf.GetDefaultConfigPaths() // Use a different variable name to avoid shadowing err
			if configErr != nil {
				errChan <- fmt.Errorf("failed to get config paths: %v", configErr) // Send error to channel
				return                                                             // Exit the goroutine upon error
			}

			// Configure AutoTLS Manager
			s.Echo.AutoTLSManager.Prompt = autocert.AcceptTOS
			s.Echo.AutoTLSManager.Cache = autocert.DirCache(configPaths[0])
			s.Echo.AutoTLSManager.HostPolicy = autocert.HostWhitelist("") // Adjust as needed

			// Start server with AutoTLS
			err = s.Echo.StartAutoTLS(":" + settings.WebServer.Port) //nolint:errcheck
		} else {
			// Start server without AutoTLS
			err = s.Echo.Start(":" + settings.WebServer.Port)
		}

		// Check if there was an error in starting the server
		if err != nil {
			errChan <- err // Send error to channel
		}
	}()

	go handleServerError(errChan)

	return s
}

// configureDefaultSettings sets default values for server settings.
func configureDefaultSettings(settings *conf.Settings) {
	if settings.WebServer.Port == "" {
		settings.WebServer.Port = "8080"
	}
}

// initializeServer configures and initializes the server.
func (s *Server) initializeServer() {
	s.Echo.HideBanner = true
	s.initLogger()
	s.configureMiddleware()
	s.initRoutes()
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

	s.Logger = logger.NewLogger(map[string]logger.LogOutput{
		"web":    logger.FileOutput{Handler: fileHandler},
		"stdout": logger.StdoutOutput{},
	}, true)

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
