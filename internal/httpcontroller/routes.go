// internal/httpcontroller/routes.go
package httpcontroller

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
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
	IsCloudflare  bool
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
		"/detections":         {Path: "/detections", TemplateName: "", Title: "", Handler: h.WithErrorHandling(h.Detections)},
		"/detections/recent":  {Path: "/detections/recent", TemplateName: "recentDetections", Title: "Recent Detections", Handler: h.WithErrorHandling(h.RecentDetections)},
		"/detections/details": {Path: "/detections/details", TemplateName: "detectionDetails", Title: "Detection Details", Handler: h.WithErrorHandling(h.DetectionDetails)},
		"/top-birds":          {Path: "/top-birds", TemplateName: "birdsTableHTML", Title: "Top Birds", Handler: h.WithErrorHandling(h.TopBirds)},
		"/notes":              {Path: "/notes", TemplateName: "notes", Title: "All Notes", Handler: h.WithErrorHandling(h.GetAllNotes)},
		"/media/spectrogram":  {Path: "/media/spectrogram", TemplateName: "", Title: "", Handler: h.WithErrorHandling(h.ServeSpectrogram)},
		"/media/audio":        {Path: "/media/audio", TemplateName: "", Title: "", Handler: h.WithErrorHandling(h.ServeAudioClip)},
		"/login":              {Path: "/login", TemplateName: "login", Title: "Login", Handler: h.WithErrorHandling(s.handleLoginPage)},
	}

	// Set up partial routes
	for _, route := range s.partialRoutes {
		s.Echo.GET(route.Path, func(c echo.Context) error {
			// If the request is a hx-request or media request, call the partial route handler
			if c.Request().Header.Get("HX-Request") != "" ||
				strings.HasPrefix(c.Request().URL.Path, "/media/") {
				return route.Handler(c)
			} else {
				// Call the full page route handler
				return s.handlePageRequest(c)
			}
		})
	}

	s.Echo.POST("/login", s.handleBasicAuthLogin)
	s.Echo.GET("/logout", s.handleLogout)

	// Special routes
	s.Echo.GET("/sse", s.Handlers.SSE.ServeSSE)
	s.Echo.GET("/audio-level", s.Handlers.WithErrorHandling(s.Handlers.AudioLevelSSE))
	s.Echo.POST("/settings/save", h.WithErrorHandling(h.SaveSettings), s.AuthMiddleware)
	s.Echo.GET("/settings/audio/get", h.WithErrorHandling(h.GetAudioDevices), s.AuthMiddleware)

	// Add DELETE method for detection deletion
	s.Echo.DELETE("/detections/delete", h.WithErrorHandling(h.DeleteDetection))

	// Add POST method for ignoring species
	s.Echo.POST("/detections/ignore", h.WithErrorHandling(h.IgnoreSpecies))

	// Add POST method for reviewing detections
	s.Echo.POST("/detections/review", h.WithErrorHandling(h.ReviewDetection))

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

// handlePageRequest handles requests for full page routes
func (s *Server) handlePageRequest(c echo.Context) error {
	path := c.Path()
	pageRoute, isPageRoute := s.pageRoutes[path]
	partialRoute, isFragment := s.partialRoutes[path]
	isCloudflare := s.CloudflareAccess.IsEnabled(c)

	// Return an error if route is unknown
	if !isPageRoute && !isFragment {
		return s.Handlers.NewHandlerError(
			fmt.Errorf("no route found for path: %s", path),
			"Page not found",
			http.StatusNotFound,
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
			IsCloudflare:  isCloudflare,
		},
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
