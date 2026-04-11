package api

import (
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/frontend"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// SPA handler constants
const (
	// indexHTMLPath is the path to the index.html file within the dist directory
	indexHTMLPath = "index.html"

	// contentTypeHTML is the Content-Type header value for HTML responses
	contentTypeHTML = "text/html; charset=utf-8"

	// cacheControlNoCache disables caching for the HTML shell
	// This ensures users always get the latest version with updated asset references
	cacheControlNoCache = "no-cache, no-store, must-revalidate"
)

// SPAHandler handles serving the Single Page Application HTML shell.
// It serves Vite's generated index.html directly, with the frontend
// fetching configuration from /api/v2/app/config at runtime.
type SPAHandler struct {
	devMode     bool
	devModePath string
}

// NewSPAHandler creates a new SPA handler.
// devMode indicates whether to serve from disk (true) or embedded FS (false).
// devModePath is the path to the frontend/dist directory when in dev mode.
func NewSPAHandler(devMode bool, devModePath string) *SPAHandler {
	return &SPAHandler{
		devMode:     devMode,
		devModePath: devModePath,
	}
}

// ServeApp serves the SPA HTML shell for all frontend routes.
// The HTML is served directly from Vite's build output.
// Configuration is fetched by the frontend from /api/v2/app/config.
// When a base path is set (reverse proxy or config), absolute asset URLs
// in the HTML are rewritten to include the prefix.
func (h *SPAHandler) ServeApp(c echo.Context) error {
	if h.devMode {
		return h.serveFromDisk(c)
	}
	return h.serveFromEmbed(c)
}

// rewriteHTMLBasePath prefixes absolute asset URLs in HTML content with the
// given base path. This ensures the browser requests assets through the
// reverse proxy (e.g., /birdnet/ui/assets/main.js instead of /ui/assets/main.js).
//
// The rewriting is idempotent: paths already containing the base path prefix
// are not double-prefixed. This is important because some reverse proxies
// (e.g., nginx sub_filter) may also rewrite paths in the response body.
func rewriteHTMLBasePath(content []byte, basePath string) []byte {
	if basePath == "" {
		return content
	}
	s := string(content)

	// Rewrite href="/..." and src="/..." attributes, but skip paths that
	// already start with the base path to avoid double-prefixing.
	s = strings.ReplaceAll(s, `href="`+basePath+`/`, "\x00HREF_PLACEHOLDER\x00")
	s = strings.ReplaceAll(s, `src="`+basePath+`/`, "\x00SRC_PLACEHOLDER\x00")
	s = strings.ReplaceAll(s, `href="/`, `href="`+basePath+`/`)
	s = strings.ReplaceAll(s, `src="/`, `src="`+basePath+`/`)
	s = strings.ReplaceAll(s, "\x00HREF_PLACEHOLDER\x00", `href="`+basePath+`/`)
	s = strings.ReplaceAll(s, "\x00SRC_PLACEHOLDER\x00", `src="`+basePath+`/`)

	// Rewrite service worker registration path and scope.
	if !strings.Contains(s, `'`+basePath+`/sw.js'`) {
		s = strings.ReplaceAll(s, `'/sw.js'`, `'`+basePath+`/sw.js'`)
	}
	if !strings.Contains(s, `scope: '`+basePath+`/'`) {
		s = strings.ReplaceAll(s, `scope: '/'`, `scope: '`+basePath+`/'`)
	}

	return []byte(s)
}

// serveFromDisk serves index.html from the local filesystem (dev mode).
// This allows hot-reloading of the frontend during development.
func (h *SPAHandler) serveFromDisk(c echo.Context) error {
	// Use os.OpenRoot for secure sandboxed access (Go 1.24+)
	root, err := os.OpenRoot(h.devModePath)
	if err != nil {
		h.logError("Failed to open frontend dist directory", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to open dist directory")
	}
	defer h.closeWithLog(root, "root handle")

	file, err := root.Open(indexHTMLPath)
	if err != nil {
		h.logError("Failed to open index.html", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to load page")
	}
	defer h.closeFileWithLog(file, indexHTMLPath)

	// Read file content
	content, err := io.ReadAll(file)
	if err != nil {
		h.logError("Failed to read index.html", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to read page")
	}

	// Set headers for dev mode (no caching)
	c.Response().Header().Set(echo.HeaderContentType, contentTypeHTML)
	c.Response().Header().Set(echo.HeaderCacheControl, cacheControlNoCache)
	c.Response().Header().Set("Pragma", "no-cache")
	c.Response().Header().Set("Expires", "0")

	// Rewrite absolute asset URLs when served behind a base path.
	basePath, _ := c.Get("basePath").(string)
	content = rewriteHTMLBasePath(content, basePath)

	return c.HTMLBlob(http.StatusOK, content)
}

// serveFromEmbed serves index.html from the embedded filesystem (production mode).
func (h *SPAHandler) serveFromEmbed(c echo.Context) error {
	file, err := frontend.DistFS.Open(indexHTMLPath)
	if err != nil {
		h.logError("Failed to open embedded index.html", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to load page")
	}
	defer h.closeEmbedFileWithLog(file, indexHTMLPath)

	// Read file content
	content, err := io.ReadAll(file)
	if err != nil {
		h.logError("Failed to read embedded index.html", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to read page")
	}

	// Set headers - still no caching for the HTML shell even in production
	// This ensures users always get the latest version after deployments
	// The JS/CSS assets are cached via their content hashes
	c.Response().Header().Set(echo.HeaderContentType, contentTypeHTML)
	c.Response().Header().Set(echo.HeaderCacheControl, cacheControlNoCache)
	c.Response().Header().Set("Pragma", "no-cache")
	c.Response().Header().Set("Expires", "0")

	// Rewrite absolute asset URLs when served behind a base path.
	basePath, _ := c.Get("basePath").(string)
	content = rewriteHTMLBasePath(content, basePath)

	return c.HTMLBlob(http.StatusOK, content)
}

// logError logs an error using the centralized logger.
func (h *SPAHandler) logError(msg string, err error) {
	GetLogger().Error(msg, logger.Error(err))
}

// closeWithLog closes an io.Closer and logs any error.
func (h *SPAHandler) closeWithLog(c io.Closer, name string) {
	if err := c.Close(); err != nil {
		GetLogger().Warn("Error closing resource",
			logger.String("name", name),
			logger.Error(err))
	}
}

// closeFileWithLog closes a file and logs any error with the path.
func (h *SPAHandler) closeFileWithLog(f *os.File, path string) {
	if err := f.Close(); err != nil {
		GetLogger().Warn("Error closing file",
			logger.String("path", path),
			logger.Error(err))
	}
}

// closeEmbedFileWithLog closes an embedded file and logs any error.
func (h *SPAHandler) closeEmbedFileWithLog(f fs.File, path string) {
	if err := f.Close(); err != nil {
		GetLogger().Warn("Error closing embedded file",
			logger.String("path", path),
			logger.Error(err))
	}
}

// DevModeStatus returns a human-readable status of the SPA handler mode.
func (h *SPAHandler) DevModeStatus() string {
	if h.devMode {
		return "Dev mode (disk): " + filepath.Join(h.devModePath, indexHTMLPath)
	}
	return "Production mode (embedded)"
}
