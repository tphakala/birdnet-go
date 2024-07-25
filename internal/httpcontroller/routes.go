// File: internal/httpcontroller/routes.go
package httpcontroller

import (
	"embed"
	"encoding/json"
	"html/template"
	"io/fs"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/httpcontroller/handlers"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
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
		"/settings/main":         {Path: "/settings/main", TemplateName: "settingsBase", Title: "Main Settings"},
		"/settings/audio":        {Path: "/settings/audio", TemplateName: "settingsBase", Title: "Audio Settings"},
		"/settings/integrations": {Path: "/settings/integrations", TemplateName: "settingsBase", Title: "Integration Settings"},
	}

	// Set up full page routes
	for _, route := range s.pageRoutes {
		s.Echo.GET(route.Path, h.WithErrorHandling(s.handlePageRequest))
	}

	// Partial routes (HTMX responses)
	partialRoutes := []PartialRouteConfig{
		{Path: "/detections/hourly", TemplateName: "hourlyDetections", Title: "Hourly Detections", Handler: h.WithErrorHandling(h.HourlyDetections)},
		{Path: "/detections/recent", TemplateName: "recentDetections", Title: "Recent Detections", Handler: h.WithErrorHandling(h.RecentDetections)},
		{Path: "/detections/species", TemplateName: "speciesDetections", Title: "Species Detections", Handler: h.WithErrorHandling(h.SpeciesDetections)},
		{Path: "/detections/details", TemplateName: "detectionDetails", Title: "Detection Details", Handler: h.WithErrorHandling(h.DetectionDetails)},
		{Path: "/detections/search", TemplateName: "searchDetections", Title: "Search Detections", Handler: h.WithErrorHandling(h.SearchDetections)},
		{Path: "/top-birds", TemplateName: "birdsTableHTML", Title: "Top Birds", Handler: h.WithErrorHandling(h.TopBirds)},
		{Path: "/notes", TemplateName: "notes", Title: "All Notes", Handler: h.WithErrorHandling(h.GetAllNotes)},
		{Path: "/media/spectrogram", TemplateName: "", Title: "", Handler: h.WithErrorHandling(h.ServeSpectrogram)},
	}

	// Set up partial routes
	for _, route := range partialRoutes {
		s.Echo.GET(route.Path, route.Handler)
	}

	// Special routes
	s.Echo.GET("/sse", s.Handlers.SSE.ServeSSE)
	s.Echo.DELETE("/note", h.WithErrorHandling(h.DeleteNote))
	s.Echo.POST("/settings/save", h.WithErrorHandling(h.SaveSettings))

	// Set up template renderer
	s.setupTemplateRenderer()

	// Set up static file serving
	s.setupStaticFileServing()
}

// setupTemplateRenderer configures the template renderer for the server.
func (s *Server) setupTemplateRenderer() {
	funcMap := template.FuncMap{
		"even":                  handlers.Even,
		"calcWidth":             handlers.CalcWidth,
		"heatmapColor":          handlers.HeatmapColor,
		"title":                 cases.Title(language.English).String,
		"confidence":            handlers.Confidence,
		"confidenceColor":       handlers.ConfidenceColor,
		"thumbnail":             s.Handlers.Thumbnail,
		"thumbnailAttribution":  s.Handlers.ThumbnailAttribution,
		"RenderContent":         s.RenderContent,
		"renderSettingsContent": s.renderSettingsContent,
		"sub":                   func(a, b int) int { return a - b },
		"add":                   func(a, b int) int { return a + b },
		"toJSON": func(v interface{}) string {
			b, err := json.Marshal(v)
			if err != nil {
				return "[]"
			}
			return string(b)
		},
	}

	tmpl, err := template.New("").Funcs(funcMap).ParseFS(ViewsFs, "views/*.html", "views/**/*.html")
	if err != nil {
		s.Echo.Logger.Fatal(err)
	}
	s.Echo.Renderer = &TemplateRenderer{templates: tmpl}
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
