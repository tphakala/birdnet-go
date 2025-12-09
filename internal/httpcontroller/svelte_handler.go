package httpcontroller

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/frontend"
)

// frontendDevMode tracks whether we're serving frontend assets from disk (dev mode)
// or from the embedded filesystem (production mode)
var (
	frontendDevMode     bool
	frontendDevModePath string
	frontendDevModeOnce sync.Once
)

// initFrontendDevMode checks if frontend/dist exists on disk and enables dev mode if so.
// This allows hot-reloading of frontend assets during development without rebuilding the Go binary.
func initFrontendDevMode() {
	frontendDevModeOnce.Do(func() {
		// Check relative to current working directory
		distPath := filepath.Join("frontend", "dist")

		// Check if the directory exists and contains index.js (main entry point)
		indexPath := filepath.Join(distPath, "index.js")
		if info, err := os.Stat(indexPath); err == nil && !info.IsDir() {
			frontendDevMode = true
			frontendDevModePath = distPath
			log.Printf("🔧 Frontend dev mode ENABLED - serving assets from disk: %s", distPath)
			log.Printf("   Run 'npm run build -- --watch' in frontend/ for auto-rebuild on changes")
			log.Printf("   Or run 'npm run dev' for Vite dev server with HMR (use port 5173)")
		} else {
			frontendDevMode = false
			log.Printf("📦 Frontend production mode - serving assets from embedded filesystem")
		}
	})
}

// IsFrontendDevMode returns whether frontend dev mode is enabled
func IsFrontendDevMode() bool {
	initFrontendDevMode()
	return frontendDevMode
}

// ServeSvelteApp serves the Svelte application within the main layout
func (s *Server) ServeSvelteApp(c echo.Context) error {
	// This is handled as a regular page route now
	return s.handlePageRequest(c)
}

// SetupSvelteRoutes sets up routes for the Svelte application
func (s *Server) SetupSvelteRoutes() {
	// Initialize dev mode detection
	initFrontendDevMode()

	// Serve Svelte static assets from a separate path to avoid conflicts
	// This serves JS, CSS and other assets from the dist folder
	s.Echo.GET("/ui/assets/*", func(c echo.Context) error {
		// Get the requested path
		path := c.Param("*")

		if frontendDevMode {
			return s.serveFrontendFromDisk(c, path)
		}
		return s.serveFrontendFromEmbed(c, path)
	})
}

// serveFrontendFromDisk serves frontend assets from the local filesystem (dev mode)
// Falls back to embedded filesystem if the file doesn't exist on disk (e.g., during Vite rebuild)
func (s *Server) serveFrontendFromDisk(c echo.Context, path string) error {
	// Use os.OpenRoot for secure filesystem sandboxing (Go 1.24+)
	// This automatically prevents path traversal, symlink escapes, and TOCTOU races
	root, err := os.OpenRoot(frontendDevModePath)
	if err != nil {
		// Dist directory doesn't exist - fall back to embedded
		log.Printf("Dev mode fallback: dist directory unavailable, serving from embedded: %v", err)
		return s.serveFrontendFromEmbed(c, path)
	}
	defer func() {
		if err := root.Close(); err != nil {
			log.Printf("Error closing root handle: %v", err)
		}
	}()

	// Open file within the sandboxed root - path traversal is blocked at OS level
	file, err := root.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist on disk (likely during Vite rebuild) - fall back to embedded
			// This prevents 404s during hot reload when dist is being rebuilt
			return s.serveFrontendFromEmbed(c, path)
		}
		// Path traversal attempts will return an error here
		if os.IsPermission(err) {
			return echo.NewHTTPError(http.StatusForbidden, "Access denied")
		}
		log.Printf("Error opening file %s: %v", path, err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to open file")
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Printf("Error closing file %s: %v", path, err)
		}
	}()

	// Get file info
	stat, err := file.Stat()
	if err != nil {
		log.Printf("Failed to stat file %s: %v", path, err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to get file info")
	}

	// Set content type
	contentType := getContentType(path)
	c.Response().Header().Set("Content-Type", contentType)

	// Dev mode: disable caching to always get fresh files
	c.Response().Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Response().Header().Set("Pragma", "no-cache")
	c.Response().Header().Set("Expires", "0")

	// Serve the file
	http.ServeContent(c.Response(), c.Request(), filepath.Base(path), stat.ModTime(), file)
	return nil
}

// serveFrontendFromEmbed serves frontend assets from the embedded filesystem (production mode)
func (s *Server) serveFrontendFromEmbed(c echo.Context, path string) error {
	// Open the file from embedded FS
	file, err := frontend.DistFS.Open(path)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "File not found")
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Printf("Error closing file %s: %v", path, err)
		}
	}()

	// Get file info for http.ServeContent
	stat, err := file.Stat()
	if err != nil {
		log.Printf("Failed to stat file %s: %v", path, err)
		httpErr := echo.NewHTTPError(http.StatusInternalServerError, "Failed to get file info")
		httpErr.Internal = err
		return httpErr
	}

	// Set content type
	contentType := getContentType(path)
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

	// Use http.ServeContent for efficient file serving
	http.ServeContent(c.Response(), c.Request(), filepath.Base(path), stat.ModTime(), seeker)
	return nil
}

// getContentType returns the appropriate MIME type for a file based on its extension
func getContentType(path string) string {
	ext := filepath.Ext(path)
	switch ext {
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".mjs":
		return "application/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".eot":
		return "application/vnd.ms-fontobject"
	case ".html":
		return "text/html; charset=utf-8"
	case ".map":
		return "application/json; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

// ResetFrontendDevMode resets the dev mode detection (useful for testing)
func ResetFrontendDevMode() {
	frontendDevModeOnce = sync.Once{}
	frontendDevMode = false
	frontendDevModePath = ""
}

// GetFrontendDevModeStatus returns a human-readable status of frontend dev mode
func GetFrontendDevModeStatus() string {
	initFrontendDevMode()
	if frontendDevMode {
		return fmt.Sprintf("Dev mode (disk): %s", frontendDevModePath)
	}
	return "Production mode (embedded)"
}
