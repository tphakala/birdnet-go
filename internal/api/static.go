package api

import (
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/frontend"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// StaticFileServer handles serving static files for the frontend.
// It supports both development mode (serving from disk) and production mode
// (serving from embedded filesystem).
type StaticFileServer struct {
	// Dev mode state
	devMode     bool
	devModePath string
	initOnce    sync.Once
}

// NewStaticFileServer creates a new static file server.
func NewStaticFileServer() *StaticFileServer {
	return &StaticFileServer{}
}

// initDevMode checks if frontend/dist exists on disk and enables dev mode if so.
// This allows hot-reloading of frontend assets during development.
func (sfs *StaticFileServer) initDevMode() {
	sfs.initOnce.Do(func() {
		log := GetLogger()
		// Check relative to current working directory
		distPath := filepath.Join("frontend", "dist")

		// Check if the directory contains the Vite manifest (indicates a valid build)
		if ManifestExistsOnDisk(distPath) {
			sfs.devMode = true
			sfs.devModePath = distPath
			log.Info("Frontend dev mode enabled",
				logger.String("path", distPath),
				logger.String("hint", "Run 'npm run build -- --watch' in frontend/ for auto-rebuild"))
		} else {
			sfs.devMode = false
			log.Info("Frontend production mode - serving from embedded filesystem")
		}
	})
}

// IsDevMode returns whether dev mode is enabled.
func (sfs *StaticFileServer) IsDevMode() bool {
	sfs.initDevMode()
	return sfs.devMode
}

// DevModePath returns the path to the dev mode dist directory.
// Returns empty string if not in dev mode.
func (sfs *StaticFileServer) DevModePath() string {
	sfs.initDevMode()
	if sfs.devMode {
		return sfs.devModePath
	}
	return ""
}

// RegisterRoutes registers the static file serving routes on the Echo instance.
func (sfs *StaticFileServer) RegisterRoutes(e *echo.Echo) {
	sfs.initDevMode()

	// Serve Svelte static assets from /ui/assets/*
	e.GET("/ui/assets/*", sfs.handleAssetRequest)
}

// handleAssetRequest serves static assets based on dev/prod mode.
func (sfs *StaticFileServer) handleAssetRequest(c echo.Context) error {
	path := c.Param("*")

	if sfs.devMode {
		return sfs.serveFromDisk(c, path)
	}
	return sfs.serveFromEmbed(c, path)
}

// serveFromDisk serves frontend assets from the local filesystem (dev mode).
// Uses os.OpenRoot (Go 1.24+) for secure sandboxing.
func (sfs *StaticFileServer) serveFromDisk(c echo.Context, path string) error {
	root, err := sfs.openDevRoot()
	if err != nil {
		return err
	}
	defer sfs.closeWithLog(root, "root handle")

	file, err := sfs.openFileFromRoot(root, path)
	if err != nil {
		return err
	}
	defer sfs.closeFileWithLog(file, path)

	stat, err := file.Stat()
	if err != nil {
		sfs.logError("Failed to stat file", path, err)
		httpErr := echo.NewHTTPError(http.StatusInternalServerError, "Failed to get file info")
		httpErr.Internal = err
		return httpErr
	}

	sfs.setDevModeHeaders(c, path)
	http.ServeContent(c.Response(), c.Request(), filepath.Base(path), stat.ModTime(), file)
	return nil
}

// openDevRoot opens the dev mode directory with secure sandboxing.
func (sfs *StaticFileServer) openDevRoot() (*os.Root, error) {
	root, err := os.OpenRoot(sfs.devModePath)
	if err != nil {
		sfs.logError("Failed to open frontend dist directory", sfs.devModePath, err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError, "Failed to open dist directory")
	}
	return root, nil
}

// openFileFromRoot opens a file within the sandboxed root directory.
func (sfs *StaticFileServer) openFileFromRoot(root *os.Root, path string) (*os.File, error) {
	file, err := root.Open(path)
	if err == nil {
		return file, nil
	}

	switch {
	case os.IsNotExist(err):
		return nil, echo.NewHTTPError(http.StatusNotFound, "File not found")
	case os.IsPermission(err):
		return nil, echo.NewHTTPError(http.StatusForbidden, "Access denied")
	default:
		sfs.logError("Failed to open file", path, err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError, "Failed to open file")
	}
}

// setDevModeHeaders sets HTTP headers for dev mode (no caching).
func (sfs *StaticFileServer) setDevModeHeaders(c echo.Context, path string) {
	c.Response().Header().Set("Content-Type", getMIMEType(path))
	c.Response().Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Response().Header().Set("Pragma", "no-cache")
	c.Response().Header().Set("Expires", "0")
}

// logError logs an error using the centralized logger.
func (sfs *StaticFileServer) logError(msg, path string, err error) {
	GetLogger().Error(msg,
		logger.String("path", path),
		logger.Error(err))
}

// closeWithLog closes an io.Closer and logs any error.
func (sfs *StaticFileServer) closeWithLog(c io.Closer, name string) {
	if err := c.Close(); err != nil {
		GetLogger().Warn("Error closing resource",
			logger.String("name", name),
			logger.Error(err))
	}
}

// closeFileWithLog closes a file and logs any error with the path.
func (sfs *StaticFileServer) closeFileWithLog(f *os.File, path string) {
	if err := f.Close(); err != nil {
		GetLogger().Warn("Error closing file",
			logger.String("path", path),
			logger.Error(err))
	}
}

// serveFromEmbed serves frontend assets from the embedded filesystem (production mode).
func (sfs *StaticFileServer) serveFromEmbed(c echo.Context, path string) error {
	log := GetLogger()
	// Open the file from embedded FS
	file, err := frontend.DistFS.Open(path)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "File not found")
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			log.Warn("Error closing embedded file",
				logger.String("path", path),
				logger.Error(closeErr))
		}
	}()

	// Get file info
	stat, err := file.Stat()
	if err != nil {
		log.Error("Failed to stat embedded file",
			logger.String("path", path),
			logger.Error(err))
		httpErr := echo.NewHTTPError(http.StatusInternalServerError, "Failed to get file info")
		httpErr.Internal = err
		return httpErr
	}

	// Set short cache for assets with fixed (unhashed) filenames.
	// Vite-hashed files (e.g., index-abc123.js) use immutable cache set by serveFileContent.
	// Non-hashed files (e.g., messages/en.json) must not be cached long-term
	// because their content changes across app updates without filename changes.
	if isUnhashedAsset(path) {
		c.Response().Header().Set("Cache-Control", "no-cache, must-revalidate")
	}

	return sfs.serveFileContent(c, file, stat, path)
}

// isUnhashedAsset returns true for asset paths that don't include a content hash
// in their filename. These files need short cache durations because their content
// can change across app updates without filename changes.
func isUnhashedAsset(path string) bool {
	clean := filepath.Clean(path)
	return strings.HasPrefix(clean, "messages/") && strings.HasSuffix(clean, ".json")
}

// serveFileContent serves file content with appropriate headers and efficient delivery.
// This is a shared helper used by both serveFromEmbed and ServeEmbeddedFS.
func (sfs *StaticFileServer) serveFileContent(c echo.Context, file fs.File, stat fs.FileInfo, path string) error {
	contentType := getMIMEType(path)
	c.Response().Header().Set("Content-Type", contentType)

	// Enable long-term caching for embedded assets (Vite-hashed filenames).
	// Callers may pre-set Cache-Control for assets with fixed (non-hashed) names.
	if c.Response().Header().Get("Cache-Control") == "" {
		c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}

	// Try to serve with http.ServeContent for efficient range requests
	if seeker, ok := file.(io.ReadSeeker); ok {
		http.ServeContent(c.Response(), c.Request(), filepath.Base(path), stat.ModTime(), seeker)
		return nil
	}

	// Try to build a ReadSeeker from ReaderAt (common for embed.FS)
	if ra, ok := file.(io.ReaderAt); ok {
		seeker := io.NewSectionReader(ra, 0, stat.Size())
		http.ServeContent(c.Response(), c.Request(), filepath.Base(path), stat.ModTime(), seeker)
		return nil
	}

	// Fallback to streaming
	GetLogger().Debug("Falling back to streaming for file", logger.String("path", path))
	return c.Stream(http.StatusOK, contentType, file)
}

// ServeEmbeddedFS serves files from any embedded filesystem.
// This is useful for serving other embedded assets like views or static assets.
func (sfs *StaticFileServer) ServeEmbeddedFS(c echo.Context, fsys fs.FS, path string) error {
	log := GetLogger()
	if fsys == nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Assets filesystem not available")
	}

	file, err := fsys.Open(path)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "File not found")
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			log.Warn("Error closing file",
				logger.String("path", path),
				logger.Error(closeErr))
		}
	}()

	stat, err := file.Stat()
	if err != nil {
		log.Error("Failed to stat file from embedded FS",
			logger.String("path", path),
			logger.Error(err))
		httpErr := echo.NewHTTPError(http.StatusInternalServerError, "Failed to get file info")
		httpErr.Internal = err
		return httpErr
	}

	return sfs.serveFileContent(c, file, stat, path)
}

// getMIMEType returns the appropriate MIME type for a file based on its extension.
func getMIMEType(path string) string {
	ext := filepath.Ext(path)
	switch ext {
	case ".js", ".mjs":
		return "application/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json", ".map":
		return "application/json; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
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
	case ".ico":
		return "image/x-icon"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".eot":
		return "application/vnd.ms-fontobject"
	case ".otf":
		return "font/otf"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".xml":
		return "application/xml; charset=utf-8"
	case ".webmanifest":
		return "application/manifest+json; charset=utf-8"
	case ".pdf":
		return "application/pdf"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".ogg":
		return "audio/ogg"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	default:
		return "application/octet-stream"
	}
}

// DevModeStatus returns a human-readable status of the dev mode.
func (sfs *StaticFileServer) DevModeStatus() string {
	sfs.initDevMode()
	if sfs.devMode {
		return "Dev mode (disk): " + sfs.devModePath
	}
	return "Production mode (embedded)"
}

// ResetDevMode resets the dev mode detection (useful for testing).
func (sfs *StaticFileServer) ResetDevMode() {
	sfs.initOnce = sync.Once{}
	sfs.devMode = false
	sfs.devModePath = ""
}
