package httpcontroller

import (
	"io"
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

		// Get file info for http.ServeContent
		stat, err := file.Stat()
		if err != nil {
			// Log the actual error for observability
			log.Printf("Failed to stat file %s: %v", path, err)
			// Return generic message to client but include error in Internal field for telemetry
			httpErr := echo.NewHTTPError(http.StatusInternalServerError, "Failed to get file info")
			httpErr.Internal = err
			return httpErr
		}

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

		// Check if file implements io.ReadSeeker (required for http.ServeContent)
		seeker, ok := file.(io.ReadSeeker)
		if !ok {
			// Try to build a ReadSeeker from a ReaderAt + size (common for embed.FS)
			if ra, ok2 := file.(io.ReaderAt); ok2 {
				seeker = io.NewSectionReader(ra, 0, stat.Size())
			} else {
				// Last-resort fallback to streaming if truly not seekable
				log.Printf("File %s does not implement io.ReadSeeker or io.ReaderAt, falling back to streaming", path)
				return c.Stream(http.StatusOK, contentType, file)
			}
		}

		// Use http.ServeContent for efficient file serving with proper buffer management
		// This handles Range requests, caching headers, and prevents buffer accumulation
		http.ServeContent(c.Response(), c.Request(), path, stat.ModTime(), seeker)
		return nil
	})
}
