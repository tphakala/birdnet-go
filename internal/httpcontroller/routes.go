// internal/httpcontroller/routes.go
package httpcontroller

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"

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
}

// PartialRouteConfig defines the structure for each partial route (HTMX response).
type PartialRouteConfig struct {
	Path         string
	TemplateName string
	Title        string
	Handler      echo.HandlerFunc
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
}

// initRoutes initializes the routes for the server.
func (s *Server) initRoutes() {
	// Initialize handlers
	h := s.Handlers

	// Full page routes
	s.pageRoutes = map[string]PageRouteConfig{
		"/":          {Path: "/", TemplateName: "dashboard", Title: "Dashboard"},
		"/dashboard": {Path: "/dashboard", TemplateName: "dashboard", Title: "Dashboard"},
		"/logs":      {Path: "/logs", TemplateName: "logs", Title: "Logs"},
		"/stats":     {Path: "/stats", TemplateName: "stats", Title: "Statistics"},
		// Settings Routes are managed by settingsBase template
		"/settings/main":             {Path: "/settings/main", TemplateName: "settingsBase", Title: "Main Settings"},
		"/settings/audio":            {Path: "/settings/audio", TemplateName: "settingsBase", Title: "Audio Settings"},
		"/settings/detectionfilters": {Path: "/settings/detectionfilters", TemplateName: "settingsBase", Title: "Detection Filters"},
		"/settings/integrations":     {Path: "/settings/integrations", TemplateName: "settingsBase", Title: "Integration Settings"},
		"/settings/species":          {Path: "/settings/species", TemplateName: "settingsBase", Title: "Editor"},
	}

	// Set up full page routes
	for _, route := range s.pageRoutes {
		s.Echo.GET(route.Path, h.WithErrorHandling(s.handlePageRequest))
	}

	// Partial routes (HTMX responses)
	s.partialRoutes = map[string]PartialRouteConfig{
		"/detections": {Path: "/detections", TemplateName: "", Title: "", Handler: h.WithErrorHandling(h.Detections)},
		"/detections/recent": {Path: "/detections/recent", TemplateName: "recentDetections", Title: "Recent Detections", Handler: h.WithErrorHandling(h.RecentDetections)},
		"/detections/details": {Path: "/detections/details", TemplateName: "detectionDetails", Title: "Detection Details", Handler: h.WithErrorHandling(h.DetectionDetails)},
		"/top-birds": {Path: "/top-birds", TemplateName: "birdsTableHTML", Title: "Top Birds", Handler: h.WithErrorHandling(h.TopBirds)},
		"/notes": {Path: "/notes", TemplateName: "notes", Title: "All Notes", Handler: h.WithErrorHandling(h.GetAllNotes)},
		"/media/spectrogram": {Path: "/media/spectrogram", TemplateName: "", Title: "", Handler: h.WithErrorHandling(h.ServeSpectrogram)},
	}

	// Set up partial routes
	for _, route := range s.partialRoutes {
		s.Echo.GET(route.Path, func(c echo.Context) error {
			// If the request is a hx-request or spectrogram, call the partial route handler
			if c.Request().Header.Get("HX-Request") != "" || c.Request().URL.Path == "/media/spectrogram" {
				return route.Handler(c)
			} else {
				// Call the full page route handler
				return s.handlePageRequest(c)
			}
		})
	}

	// Special routes
	s.Echo.GET("/sse", s.Handlers.SSE.ServeSSE)
	s.Echo.GET("/audio-level", s.Handlers.WithErrorHandling(s.Handlers.AudioLevelSSE))
	s.Echo.DELETE("/note", h.WithErrorHandling(h.DeleteNote))
	s.Echo.POST("/settings/save", h.WithErrorHandling(h.SaveSettings))
	s.Echo.GET("/settings/audio/get", h.WithErrorHandling(h.GetAudioDevices))

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
	var data RenderData
	path := c.Path()
	pageRoute, isPageRoute := s.pageRoutes[path]
	_, isFragment := s.partialRoutes[path]

	// Return an error if route is unknown
	if !isPageRoute && !isFragment {
		return s.Handlers.NewHandlerError(
			fmt.Errorf("no route found for path: %s", path),
			"Page not found",
			http.StatusNotFound,
		)
	}

	if isPageRoute {
		data = RenderData{
			C:        c,
			Page:     pageRoute.TemplateName,
			Title:    pageRoute.Title,
			Settings: s.Settings,
		}
	} else {
		// If the route is for a fragment, render it with the dashboard template
		data = RenderData{
			C:               c,
			Page:            "dashboard",
			Title:           "Dashboard",
			Settings: 		 s.Settings,
			PreloadFragment: c.Request().RequestURI,
		}
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
	s.Echo.Static("/clips", "clips")
}
