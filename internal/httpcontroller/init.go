package httpcontroller

import (
	"fmt"
	"html/template"
	"io"
	"log"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
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
	Echo     *echo.Echo          // Echo framework instance
	ds       datastore.Interface // Datastore interface
	Settings *conf.Settings      // Application settings
	Logger   *logger.Logger      // Custom logger
}

// New initializes a new HTTP server with given context and datastore.
func New(settings *conf.Settings, dataStore datastore.Interface) *Server {
	// Default port configuration
	configureDefaultSettings(settings)

	s := &Server{
		Echo:     echo.New(),
		ds:       dataStore,
		Settings: settings,
	}

	// Server initialization
	s.initializeServer()

	// Start the server in a new goroutine and handle errors
	errChan := make(chan error)
	go func() {
		if err := s.Echo.Start(":" + settings.WebServer.Port); err != nil {
			errChan <- err
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
	s.setupCustomLogger()
	s.configureMiddleware()
	s.initRoutes()
}

// handleServerError listens for server errors and handles them.
func handleServerError(errChan chan error) {
	for {
		select {
		case err := <-errChan:
			log.Printf("Server error: %v", err)
			// Additional error handling logic here
		}
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
}

// setupCustomLogger sets up the custom logger for the Echo server.
func (s *Server) setupCustomLogger() {
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

// configureMiddleware sets up middleware for the server.
func (s *Server) configureMiddleware() {
	s.Echo.Use(middleware.Recover())
	s.Echo.Use(middleware.GzipWithConfig(middleware.GzipConfig{
		Level: 5,
	}))
	// Additional middleware can be added here
}
