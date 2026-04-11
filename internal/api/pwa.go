package api

import (
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/frontend"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// registerPWARoutes registers routes for PWA support files.
// The manifest and service worker must be served from root paths
// so the service worker scope covers the entire application.
func (s *Server) registerPWARoutes() {
	// Serve manifest.webmanifest from root path, with base path rewriting.
	s.echo.GET("/manifest.webmanifest", func(c echo.Context) error {
		basePath, _ := c.Get("basePath").(string)
		return s.staticServer.serveManifest(c, basePath)
	})

	// Serve service worker from root path with Service-Worker-Allowed header.
	s.echo.GET("/sw.js", func(c echo.Context) error {
		basePath, _ := c.Get("basePath").(string)
		scope := "/"
		if basePath != "" {
			scope = basePath + "/"
		}
		c.Response().Header().Set("Service-Worker-Allowed", scope)
		return s.staticServer.handlePWAFile(c, "sw.js")
	})
}

// serveManifest serves manifest.webmanifest, rewriting paths when a base path is active.
func (sfs *StaticFileServer) serveManifest(c echo.Context, basePath string) error {
	sfs.initDevMode()

	var content []byte
	var err error

	if sfs.devMode {
		content, err = sfs.readPWAFromDisk("manifest.webmanifest")
	} else {
		content, err = sfs.readPWAFromEmbed("manifest.webmanifest")
	}
	if err != nil {
		return err
	}

	// Rewrite absolute paths in the manifest JSON when base path is set.
	if basePath != "" {
		content = rewriteManifestBasePath(content, basePath)
	}

	c.Response().Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
	c.Response().Header().Set("Cache-Control", "no-cache")
	return c.Blob(http.StatusOK, "application/manifest+json; charset=utf-8", content)
}

// rewriteManifestBasePath prefixes absolute paths in the manifest JSON
// and updates start_url/scope to point to the base path root.
func rewriteManifestBasePath(content []byte, basePath string) []byte {
	s := string(content)
	// Prefix absolute icon src paths: "/ui/assets/..." → "{basePath}/ui/assets/..."
	s = strings.ReplaceAll(s, `"/ui/assets/`, `"`+basePath+`/ui/assets/`)
	// Rewrite start_url and scope to base path root.
	// The manifest uses relative "../../" which doesn't work behind a proxy.
	s = strings.ReplaceAll(s, `"../../"`, `"`+basePath+`/"`)
	return []byte(s)
}

// readPWAFromDisk reads a PWA file from the dev mode dist directory.
func (sfs *StaticFileServer) readPWAFromDisk(filename string) ([]byte, error) {
	root, err := os.OpenRoot(sfs.devModePath)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusInternalServerError, "Failed to open dist directory")
	}
	defer sfs.closeWithLog(root, "root handle")

	file, err := root.Open(filename)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusNotFound, "File not found")
	}
	defer sfs.closeFileWithLog(file, filename)

	return io.ReadAll(file)
}

// readPWAFromEmbed reads a PWA file from the embedded filesystem.
func (sfs *StaticFileServer) readPWAFromEmbed(filename string) ([]byte, error) {
	log := GetLogger()
	file, err := frontend.DistFS.Open(filename)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusNotFound, "File not found")
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			log.Warn("Error closing embedded file",
				logger.String("path", filename),
				logger.Error(closeErr))
		}
	}()

	return io.ReadAll(file)
}

// handlePWAFile serves a PWA file from the static file server (dev or embedded).
// These files are stored in frontend/static/ and built into dist/.
// PWA files have fixed (non-hashed) names, so we override the default
// immutable cache headers that serveFileContent sets for Vite-hashed assets.
func (sfs *StaticFileServer) handlePWAFile(c echo.Context, filename string) error {
	sfs.initDevMode()
	if sfs.devMode {
		return sfs.serveFromDisk(c, filename)
	}
	// Set cache headers before serveFromEmbed — prevents the 1-year immutable
	// cache that serveFileContent applies to content-hashed Vite bundles.
	c.Response().Header().Set("Cache-Control", "no-cache")
	return sfs.serveFromEmbed(c, filename)
}
