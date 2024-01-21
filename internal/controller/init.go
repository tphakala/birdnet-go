package controller

import (
	"fmt"
	"html/template"
	"io"
	"log"
	"log/syslog"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/tphakala/birdnet-go/internal/config"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/model"
)

type TemplateRenderer struct {
	templates *template.Template
}

func (t *TemplateRenderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	return t.templates.ExecuteTemplate(w, name, data)
}

type Server struct {
	Echo     *echo.Echo
	SysLog   *syslog.Writer
	ds       model.StoreInterface
	Settings *config.Settings
	Logger   *logger.Logger
}

/*
func (s *Server) initLogger() {
	var err error
	s.SysLog, err = syslog.New(syslog.LOG_INFO|syslog.LOG_LOCAL0, "webui")
	if err != nil {
		s.Echo.Logger.Error(err)
		return
	}

	// Multi-writer to log to both syslog and stdout
	multi := io.MultiWriter(os.Stdout, s.SysLog)

	s.Echo.Logger.SetOutput(multi)
}*/

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

func NewServer(dataStore model.StoreInterface, ctx *config.Context) *Server {
	s := &Server{
		Echo:     echo.New(),
		ds:       dataStore,
		Settings: ctx.Settings,
	}

	// Disable Echo startup banner
	s.Echo.HideBanner = false

	// Initialize the logger
	s.initLogger()

	// Use middleware
	//s.Echo.Use(middleware.Logger())
	s.Echo.Use(middleware.Recover())

	// Setup custom logger for Echo
	s.setupCustomLogger()

	s.initRoutes(ctx)
	return s
}

// EchoStart starts the Echo server. Returns an error instead of logging fatal directly.
func (s *Server) EchoStart(ctx *config.Context) error {
	if ctx == nil || ctx.Settings == nil {
		return fmt.Errorf("invalid context or settings")
	}

	port := ctx.Settings.WebServer.Port
	if port == "" {
		port = "8080" // Default port if not specified
	}

	return s.Echo.Start(":" + port)
}

// Start initializes and starts the server.
// Returns an error to allow the caller to handle it, rather than terminating the program directly.
func Start(ctx *config.Context, dataStore model.StoreInterface) error {
	if ctx == nil || ctx.Settings == nil {
		return fmt.Errorf("context or settings are nil")
	}

	// Use existing settings or default values
	serverPort := ctx.Settings.WebServer.Port
	if serverPort == "" {
		serverPort = "8080"
	}
	ctx.Settings.WebServer.Port = serverPort

	serverInstance := NewServer(dataStore, ctx)

	// Test the logger directly
	logger.SetupDefaultLogger(map[string]logger.LogOutput{
		"web":    logger.StdoutOutput{},
		"stdout": logger.StdoutOutput{},
	}, true)

	logger.Debug("web", "Starting web server on port %s", serverPort)

	// Start the server
	return serverInstance.EchoStart(ctx)
}
