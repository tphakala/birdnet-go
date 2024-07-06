// File: internal/httpcontroller/server.go
package httpcontroller

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"sort"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/httpcontroller/handlers"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"golang.org/x/crypto/acme/autocert"
)

// TemplateRenderer is a custom HTML template renderer for Echo framework.
type TemplateRenderer struct {
	templates *template.Template
}

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
}

// LocaleData represents a locale with its code and full name.
type LocaleData struct {
	Code string
	Name string
}

// PageData represents data for rendering a page.
type PageData struct {
	C        echo.Context
	Page     string
	Title    string
	Settings *conf.Settings
	Locales  []LocaleData
	Charts   template.HTML
}

// Render renders a template with the given data.
func (t *TemplateRenderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	return t.templates.ExecuteTemplate(w, name, data)
}

// New initializes a new HTTP server with given context and datastore.
func New(settings *conf.Settings, dataStore datastore.Interface, birdImageCache *imageprovider.BirdImageCache) *Server {
	configureDefaultSettings(settings)

	s := &Server{
		Echo:              echo.New(),
		DS:                dataStore,
		Settings:          settings,
		BirdImageCache:    birdImageCache,
		DashboardSettings: &settings.Realtime.Dashboard,
	}

	s.initLogger()
	s.Handlers = handlers.New(s.DS, s.Settings, s.DashboardSettings, s.BirdImageCache, nil)

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

// RenderContent modification
func (s *Server) RenderContent(data interface{}) (template.HTML, error) {
	d, ok := data.(struct {
		C        echo.Context
		Page     string
		Title    string
		Settings *conf.Settings
		Locales  []LocaleData
		Charts   template.HTML
	})
	if !ok {
		return "", fmt.Errorf("invalid data type")
	}

	c := d.C // Extracted context
	path := c.Path()
	route, exists := s.pageRoutes[path]
	if !exists {
		return "", fmt.Errorf("no route found for path: %s", path)
	}

	buf := new(bytes.Buffer)
	err := s.Echo.Renderer.Render(buf, route.TemplateName, d, c)
	if err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}

// handlePageRequest modification
func (s *Server) handlePageRequest(c echo.Context) error {
	path := c.Path()
	route, exists := s.pageRoutes[path]
	if !exists {
		return s.Handlers.NewHandlerError(fmt.Errorf("no route found for path: %s", path), "Page not found", http.StatusNotFound)
	}

	data := struct {
		C        echo.Context
		Page     string
		Title    string
		Settings *conf.Settings
		Locales  []LocaleData
		Charts   template.HTML
	}{
		C:        c,
		Page:     route.TemplateName,
		Title:    route.Title,
		Settings: s.Settings,
	}

	// Include settings and locales data only for the settings page
	if route.TemplateName == "settingsMain" {
		data.Locales = s.getLocalesData()
	}

	if route.TemplateName == "stats" {
		// Render charts to HTML
		birdVocalizationChartHTML := s.Handlers.BirdStats()
		// Prepare data for the template
		data.Charts = template.HTML(birdVocalizationChartHTML)
	}

	return c.Render(http.StatusOK, "index", data)
}

// getLocalesData returns sorted locale data
func (s *Server) getLocalesData() []LocaleData {
	var locales []LocaleData
	for code, name := range conf.LocaleCodes {
		locales = append(locales, LocaleData{Code: code, Name: name})
	}
	sort.Slice(locales, func(i, j int) bool {
		return locales[i].Name < locales[j].Name
	})
	return locales
}
