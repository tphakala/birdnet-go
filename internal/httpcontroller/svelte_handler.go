package httpcontroller

import (
	"log"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/frontend"
)

// ServeSvelteApp serves the Svelte application within the main layout
func (s *Server) ServeSvelteApp(c echo.Context) error {
	// This is handled as a regular page route now
	return s.handlePageRequest(c)
}

// SetupSvelteRoutes sets up routes for the Svelte application
func (s *Server) SetupSvelteRoutes() {
	// Serve Svelte static assets from a separate path to avoid conflicts
	// This serves JS, CSS and other assets from the dist folder
	s.Echo.GET("/ui/assets/*", func(c echo.Context) error {
		// Get the requested path
		path := c.Param("*")

		// Open the file from embedded FS
		file, err := frontend.DistFS.Open(path)
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, "File not found")
		}
		defer func() {
			if err := file.Close(); err != nil {
				// Log error but don't fail the request
				log.Printf("Error closing file %s: %v", path, err)
			}
		}()

		// Set correct MIME type based on file extension
		contentType := "application/octet-stream"
		if len(path) > 5 && path[len(path)-5:] == ".json" {
			contentType = "application/json; charset=utf-8"
		} else if len(path) > 4 {
			switch path[len(path)-4:] {
			case ".css":
				contentType = "text/css; charset=utf-8"
			case ".svg":
				contentType = "image/svg+xml"
			}
		}
		if len(path) > 3 && path[len(path)-3:] == ".js" {
			contentType = "application/javascript; charset=utf-8"
		}

		// Set content type header
		c.Response().Header().Set("Content-Type", contentType)

		// Serve the file
		return c.Stream(http.StatusOK, contentType, file)
	})
}
