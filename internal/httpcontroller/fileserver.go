package httpcontroller

import (
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
)

// customFileServer sets up a file server for serving static assets with correct MIME types.
func customFileServer(e *echo.Echo, fileSystem fs.FS, root string) {
	fileServer := http.FileServer(http.FS(fileSystem))

	e.GET("/"+root+"/*", echo.WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Correctly set the URL path for the file server
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/"+root)

		// Extract the requested file's extension
		ext := filepath.Ext(r.URL.Path)

		// Set the MIME type based on the file extension
		mimeType := mime.TypeByExtension(ext)
		if mimeType != "" {
			w.Header().Set("Content-Type", mimeType)
		} else {
			// Default to 'text/plain' if MIME type is not detected
			w.Header().Set("Content-Type", "text/plain")
		}

		// Serve the file
		fileServer.ServeHTTP(w, r)
	})))
}
