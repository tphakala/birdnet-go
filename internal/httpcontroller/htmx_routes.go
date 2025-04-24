// internal/httpcontroller/routes.go
package httpcontroller

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Embed the assets and views directories.
var AssetsFs embed.FS
var ViewsFs embed.FS

// PageRouteConfig defines the structure for each full page route.
type PageRouteConfig struct {
	Path         string
	TemplateName string
	Title        string
	Authorized   bool // Whether the route requires authentication
}

// PartialRouteConfig defines the structure for each partial route (HTMX response).
type PartialRouteConfig struct {
	Path         string
	TemplateName string
	Title        string
	Handler      echo.HandlerFunc
}

type Security struct {
	Enabled       bool
	AccessAllowed bool
}

type RenderData struct {
	C               echo.Context
	Page            string
	Title           string
	Settings        *conf.Settings
	Locales         []LocaleData
	Charts          template.HTML
	ContentTemplate string
	PreloadFragment string
	Security        *Security
	CSRFToken       string
}

// initRoutes initializes the routes for the server.
func (s *Server) initRoutes() {
	// Initialize handlers
	h := s.Handlers

	// Initialize OAuth2 routes
	s.initAuthRoutes()

	// Full page routes
	s.pageRoutes = map[string]PageRouteConfig{
		"/":          {Path: "/", TemplateName: "dashboard", Title: "Dashboard"},
		"/dashboard": {Path: "/dashboard", TemplateName: "dashboard", Title: "Dashboard"},
		"/logs":      {Path: "/logs", TemplateName: "logs", Title: "Logs"},
		"/stats":     {Path: "/stats", TemplateName: "stats", Title: "Statistics"},
		"/search":    {Path: "/search", TemplateName: "search", Title: "Search Detections"},
		"/about":     {Path: "/about", TemplateName: "about", Title: "About BirdNET-Go"},
		// Settings Routes are managed by settingsBase template
		"/settings/main":             {Path: "/settings/main", TemplateName: "settingsBase", Title: "Main Settings", Authorized: true},
		"/settings/audio":            {Path: "/settings/audio", TemplateName: "settingsBase", Title: "Audio Settings", Authorized: true},
		"/settings/detectionfilters": {Path: "/settings/detectionfilters", TemplateName: "settingsBase", Title: "Detection Filters", Authorized: true},
		"/settings/integrations":     {Path: "/settings/integrations", TemplateName: "settingsBase", Title: "Integration Settings", Authorized: true},
		"/settings/security":         {Path: "/settings/security", TemplateName: "settingsBase", Title: "Security & Access Settings", Authorized: true},
		"/settings/species":          {Path: "/settings/species", TemplateName: "settingsBase", Title: "Species Settings", Authorized: true},
	}

	// Set up full page routes
	for _, route := range s.pageRoutes {
		if route.Authorized {
			s.Echo.GET(route.Path, h.WithErrorHandling(s.handlePageRequest), s.AuthMiddleware)
		} else {
			s.Echo.GET(route.Path, h.WithErrorHandling(s.handlePageRequest))

		}
	}

	// Partial routes (HTMX responses)
	s.partialRoutes = map[string]PartialRouteConfig{
		"/api/v1/detections":         {Path: "/api/v1/detections", TemplateName: "", Title: "", Handler: h.WithErrorHandling(h.Detections)},
		"/api/v1/detections/recent":  {Path: "/api/v1/detections/recent", TemplateName: "recentDetections", Title: "Recent Detections", Handler: h.WithErrorHandling(h.RecentDetections)},
		"/api/v1/detections/details": {Path: "/api/v1/detections/details", TemplateName: "detectionDetails", Title: "Detection Details", Handler: h.WithErrorHandling(h.DetectionDetails)},
		"/api/v1/top-birds":          {Path: "/api/v1/top-birds", TemplateName: "birdsTableHTML", Title: "Top Birds", Handler: h.WithErrorHandling(h.TopBirds)},
		"/api/v1/notes":              {Path: "/api/v1/notes", TemplateName: "notes", Title: "All Notes", Handler: h.WithErrorHandling(h.GetAllNotes)},
		"/api/v1/media/spectrogram":  {Path: "/api/v1/media/spectrogram", TemplateName: "", Title: "", Handler: h.WithErrorHandling(h.ServeSpectrogram)},
		"/api/v1/media/audio":        {Path: "/api/v1/media/audio", TemplateName: "", Title: "", Handler: h.WithErrorHandling(h.ServeAudioClip)},
		"/login":                     {Path: "/login", TemplateName: "login", Title: "Login", Handler: h.WithErrorHandling(s.handleLoginPage)},
	}

	// Set up partial routes
	for _, route := range s.partialRoutes {
		s.Echo.GET(route.Path, func(c echo.Context) error {
			// If the request is a hx-request or media request, call the partial route handler
			if c.Request().Header.Get("HX-Request") != "" ||
				strings.HasPrefix(c.Request().URL.Path, "/api/v1/media/") {
				return route.Handler(c)
			} else {
				// Call the full page route handler
				return s.handlePageRequest(c)
			}
		})
	}

	// Special routes
	s.Echo.GET("/api/v1/sse", s.Handlers.SSE.ServeSSE)
	s.Echo.GET("/api/v1/audio-level", s.Handlers.WithErrorHandling(s.Handlers.AudioLevelSSE))

	// HLS streaming routes
	// Note: The route order here is important - Echo matches the first registered route
	// that fits the pattern. We intentionally register the wildcard route "/:sourceID/*" first
	// to handle full paths including segment names, and the simpler "/:sourceID" route second
	// to handle requests for the source's base playlist.
	//
	// This works because both handlers call the same function and the handler will parse the
	// path details regardless of which route matched.
	s.Echo.GET("/api/v1/audio-stream-hls/:sourceID/*", h.WithErrorHandling(s.handleHLSStreamRequest))
	s.Echo.GET("/api/v1/audio-stream-hls/:sourceID", h.WithErrorHandling(s.handleHLSStreamRequest))

	// Add HLS stream management routes for client synchronization
	s.Echo.POST("/api/v1/audio-stream-hls/:sourceID/start", func(c echo.Context) error {
		// Add server to context for authentication
		c.Set("server", s)
		sourceID := c.Param("sourceID")
		decodedSourceID, err := DecodeSourceID(sourceID)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid source ID format")
		}

		s.Debug("Client requested HLS stream start for source: %s", decodedSourceID)

		// Start the ffmpeg process if not already running
		status, err := s.Handlers.StartHLSStream(c, decodedSourceID)
		if err != nil {
			s.Debug("Error starting HLS stream: %v", err)
			return echo.NewHTTPError(http.StatusInternalServerError, "Failed to start streaming")
		}

		// Return a response with stream status
		return c.JSON(http.StatusOK, status)
	}, s.AuthMiddleware)

	// Add HLS client heartbeat endpoint to track active clients
	s.Echo.POST("/api/v1/audio-stream-hls/heartbeat", func(c echo.Context) error {
		// Add server to context for authentication
		c.Set("server", s)

		s.Debug("Received HLS client heartbeat")

		// Process the heartbeat (but don't write response)
		err := s.Handlers.ProcessHLSHeartbeat(c)
		if err != nil {
			s.Debug("Error processing HLS heartbeat: %v", err)
			return echo.NewHTTPError(http.StatusInternalServerError, "Failed to process heartbeat")
		}

		// Return empty JSON object response
		return c.JSON(http.StatusOK, map[string]any{})
	}, s.AuthMiddleware)

	s.Echo.POST("/api/v1/audio-stream-hls/:sourceID/stop", func(c echo.Context) error {
		// Add server to context for authentication
		c.Set("server", s)
		sourceID := c.Param("sourceID")
		decodedSourceID, err := DecodeSourceID(sourceID)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid source ID format")
		}

		s.Debug("Client requested HLS stream stop for source: %s", decodedSourceID)

		// Register client disconnect
		err = s.Handlers.StopHLSClientStream(c, decodedSourceID)
		if err != nil {
			s.Debug("Error stopping HLS stream: %v", err)
			return echo.NewHTTPError(http.StatusInternalServerError, "Failed to stop streaming")
		}

		return c.JSON(http.StatusOK, map[string]string{
			"status": "stopped",
			"source": decodedSourceID,
		})
	}, s.AuthMiddleware)

	s.Echo.POST("/api/v1/settings/save", h.WithErrorHandling(h.SaveSettings), s.AuthMiddleware)
	s.Echo.GET("/api/v1/settings/audio/get", h.WithErrorHandling(h.GetAudioDevices), s.AuthMiddleware)

	// Add DELETE method for detection deletion
	s.Echo.DELETE("/api/v1/detections/delete", h.WithErrorHandling(h.DeleteDetection), s.AuthMiddleware)

	// Add POST method for ignoring species
	s.Echo.POST("/api/v1/detections/ignore", h.WithErrorHandling(h.IgnoreSpecies), s.AuthMiddleware)

	// Add POST method for reviewing detections
	s.Echo.POST("/api/v1/detections/review", h.WithErrorHandling(h.ReviewDetection), s.AuthMiddleware)

	// Add POST method for locking/unlocking detections
	s.Echo.POST("/api/v1/detections/lock", h.WithErrorHandling(h.LockDetection), s.AuthMiddleware)

	// Add GET method for testing MQTT connection
	s.Echo.GET("/api/v1/mqtt/test", h.WithErrorHandling(h.TestMQTT), s.AuthMiddleware)
	s.Echo.POST("/api/v1/mqtt/test", h.WithErrorHandling(h.TestMQTT), s.AuthMiddleware)

	// Add GET and POST methods for testing BirdWeather connection
	s.Echo.GET("/api/v1/birdweather/test", h.WithErrorHandling(h.TestBirdWeather), s.AuthMiddleware)
	s.Echo.POST("/api/v1/birdweather/test", h.WithErrorHandling(h.TestBirdWeather), s.AuthMiddleware)

	// Setup Error handler
	s.Echo.HTTPErrorHandler = func(err error, c echo.Context) {
		if handleErr := s.Handlers.HandleError(err, c); handleErr != nil {
			// If HandleError itself returns an error, create a new HandlerError and render it
			newErr := s.Handlers.NewHandlerError(
				handleErr,
				"Error occurred while handling another error",
				http.StatusInternalServerError,
			)
			if !c.Response().Committed {
				if renderErr := c.Render(newErr.Code, "error", newErr); renderErr != nil {
					c.Logger().Error(renderErr)
				}
			}
		}
	}

	// Set up template renderer
	s.setupTemplateRenderer()

	// Set up static file serving
	s.setupStaticFileServing()
}

// handleHLSStreamRequest handles requests for HLS stream endpoints
func (s *Server) handleHLSStreamRequest(c echo.Context) error {
	// Add server to context for authentication
	c.Set("server", s)
	s.Debug("HLS stream request for path: %s", c.Request().URL.Path)

	// Log client disconnection when the request completes
	sourceID := c.Param("sourceID")
	clientIP := c.RealIP()
	defer func() {
		select {
		case <-c.Request().Context().Done():
			s.Debug("Client %s disconnected from HLS stream: %s", clientIP, sourceID)
		default:
			// Request completed normally
		}
	}()

	return s.Handlers.WithErrorHandling(s.Handlers.AudioStreamHLS)(c)
}

// handlePageRequest handles requests for full page routes
func (s *Server) handlePageRequest(c echo.Context) error {
	path := c.Path()
	pageRoute, isPageRoute := s.pageRoutes[path]
	partialRoute, isFragment := s.partialRoutes[path]

	// Return an error if route is unknown
	if !isPageRoute && !isFragment {
		return s.Handlers.NewHandlerError(
			fmt.Errorf("no route found for path: %s", path),
			"Page not found",
			http.StatusNotFound,
		)
	}

	// Get CSRF token from context
	token := c.Get(CSRFContextKey)
	if token == nil {
		s.Debug("CSRF token missing in context for path: %s", c.Path())
		return s.Handlers.NewHandlerError(
			fmt.Errorf("CSRF token not found"),
			"Security validation failed",
			http.StatusInternalServerError,
		)
	}

	data := RenderData{
		C:        c,
		Page:     pageRoute.TemplateName,
		Title:    pageRoute.Title,
		Settings: s.Settings,
		Security: &Security{
			Enabled:       s.isAuthenticationEnabled(c),
			AccessAllowed: s.IsAccessAllowed(c),
		},
		CSRFToken: func() string {
			tokenStr, ok := token.(string)
			if !ok {
				return ""
			}
			return tokenStr
		}(),
	}

	fragmentPath := c.Request().RequestURI
	if isFragment && conf.IsSafePath(fragmentPath) {
		// If the route is for a fragment, render it with the dashboard template
		data.Page = "dashboard"
		data.Title = partialRoute.Title
		data.PreloadFragment = fragmentPath
	}

	return c.Render(http.StatusOK, "index", data)
}

// setupStaticFileServing configures static file serving for the server.
func (s *Server) setupStaticFileServing() {
	assetsFS, err := fs.Sub(AssetsFs, "assets")
	if err != nil {
		s.Echo.Logger.Fatal(err)
	}
	s.Echo.StaticFS("/assets", echo.MustSubFS(assetsFS, ""))
}

// EncodeSourceID properly encodes a source ID to be safely used in URLs
func EncodeSourceID(sourceID string) string {
	return url.QueryEscape(sourceID)
}

// DecodeSourceID decodes a source ID from a URL
func DecodeSourceID(encodedSourceID string) (string, error) {
	return url.QueryUnescape(encodedSourceID)
}
