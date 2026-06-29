// Package filesystem is the api/v2 filesystem domain handler. It owns the
// /api/v2/filesystem/* endpoints: the secure file-browser endpoint backing the
// frontend directory picker. The Handler embeds *apicore.Core by pointer so the
// shared dependencies and helpers (the media SecureFS sandbox, the auth
// middleware, and the error/log helpers) promote onto it; the facade constructs
// one Handler and calls RegisterRoutes to wire the routes in their existing
// order.
package filesystem

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Handler serves the filesystem domain endpoints. It embeds *apicore.Core BY
// POINTER so the shared Core members (notably the SecureFS sandbox SFS, the
// AuthMiddleware, and the error/log helpers) promote onto it without re-wiring;
// Core carries atomic/lock-bearing fields and must never be copied by value.
type Handler struct {
	*apicore.Core
}

// New builds a filesystem Handler around the shared core. The filesystem
// handlers need only the shared *apicore.Core (the media SecureFS instance, the
// auth middleware, and the error/log helpers), so there are no facade-owned
// dependencies to inject.
func New(core *apicore.Core) *Handler {
	return &Handler{Core: core}
}

// RegisterRoutes registers all filesystem-related API endpoints on the supplied
// API v2 group, preserving the exact route, order, and per-route middleware the
// facade used before the filesystem domain was extracted.
func (c *Handler) RegisterRoutes(g *echo.Group) {
	c.LogInfoIfEnabled("Initializing filesystem routes")

	// Create filesystem API group with authentication
	fsGroup := g.Group("/filesystem", c.AuthMiddleware)

	// GET /api/v2/filesystem/browse - Browse files and directories
	fsGroup.GET("/browse", c.BrowseFileSystem)

	c.LogInfoIfEnabled("Filesystem routes initialized")
}

// FileSystemItem represents a file or directory for the frontend file browser
type FileSystemItem struct {
	ID   string    `json:"id"`   // Full path
	Size int64     `json:"size"` // Size in bytes
	Date time.Time `json:"date"` // Modification date
	Type string    `json:"type"` // "file" or "folder"
	Name string    `json:"name"` // Display name (basename)
}

// BrowseRequest represents a request to browse a directory
type BrowseRequest struct {
	Path string `query:"path"` // Directory path to browse
}

// BrowseResponse represents the response from browsing a directory
type BrowseResponse struct {
	Items       []FileSystemItem `json:"items"`
	CurrentPath string           `json:"currentPath"`
	ParentPath  string           `json:"parentPath,omitempty"`
}

// browsePathResult holds validated path information for browsing.
type browsePathResult struct {
	browsePath string
	relPath    string
}

// validateBrowsePath validates and normalizes the browse path, checking security constraints.
// Returns the validated path info or an error with appropriate HTTP status code.
func (c *Handler) validateBrowsePath(reqPath string) (browsePathResult, error) {
	// Default to base directory if no path specified
	browsePath := reqPath
	if browsePath == "" {
		browsePath = c.SFS.BaseDir()
	}

	// Convert to relative path and validate using SecureFS
	relPath, err := c.SFS.RelativePath(browsePath)
	if err != nil {
		return browsePathResult{}, fmt.Errorf("invalid or unsafe path: %w", err)
	}

	// Check for symlinks and validate targets using Lstat
	info, err := c.SFS.Lstat(browsePath)
	if err != nil {
		return browsePathResult{}, err
	}

	// If it's a symlink, validate the target and get target info
	if info.Mode()&os.ModeSymlink != 0 {
		if err := c.validateSymlinkTarget(browsePath); err != nil {
			return browsePathResult{}, fmt.Errorf("symlink target not allowed: %w", err)
		}
		// Use Stat to get info about the symlink target (not the symlink itself)
		info, err = c.SFS.Stat(browsePath)
		if err != nil {
			return browsePathResult{}, fmt.Errorf("failed to stat symlink target: %w", err)
		}
	}

	// Ensure it's a directory (now correctly checks target for symlinks)
	if !info.IsDir() {
		return browsePathResult{}, fmt.Errorf("path is not a directory")
	}

	return browsePathResult{browsePath: browsePath, relPath: relPath}, nil
}

// BrowseFileSystem lists files and directories in the specified path
func (c *Handler) BrowseFileSystem(ctx echo.Context) error {
	var req BrowseRequest
	if err := ctx.Bind(&req); err != nil {
		c.LogErrorIfEnabled("Failed to bind browse request",
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, "Invalid request parameters", http.StatusBadRequest)
	}

	// Validate and normalize the browse path
	pathResult, err := c.validateBrowsePath(req.Path)
	if err != nil {
		status := http.StatusBadRequest
		if os.IsNotExist(err) {
			status = http.StatusNotFound
		} else if os.IsPermission(err) {
			status = http.StatusForbidden
		}
		c.LogWarnIfEnabled("Path validation failed",
			logger.String("requested_path", req.Path),
			logger.Error(err),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, "Unable to access path", status)
	}

	// Read directory contents using SecureFS
	entries, err := c.SFS.ReadDir(pathResult.browsePath)
	if err != nil {
		c.LogErrorIfEnabled("Failed to read directory",
			logger.String("path", pathResult.browsePath),
			logger.Error(err),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, "Unable to read directory", http.StatusForbidden)
	}

	// Convert to response format
	items := make([]FileSystemItem, 0, len(entries))
	for _, entry := range entries {
		item, err := c.convertDirEntryToItem(pathResult.browsePath, entry)
		if err != nil {
			c.LogDebugIfEnabled("Skipping file due to error",
				logger.String("file", entry.Name()),
				logger.String("directory", pathResult.browsePath),
				logger.Error(err),
			)
			continue
		}
		items = append(items, item)
	}

	// Determine parent path securely using SecureFS
	parentPath, err := c.SFS.ParentPath(pathResult.browsePath)
	if err != nil {
		c.LogDebugIfEnabled("Failed to get parent path",
			logger.String("path", pathResult.browsePath),
			logger.Error(err))
		parentPath = ""
	}

	// Get the current absolute path for response
	currentPath := pathResult.browsePath
	if !filepath.IsAbs(currentPath) {
		currentPath = filepath.Join(c.SFS.BaseDir(), pathResult.relPath)
	}

	response := BrowseResponse{
		Items:       items,
		CurrentPath: currentPath,
		ParentPath:  parentPath,
	}

	c.LogInfoIfEnabled("Successfully browsed directory",
		logger.String("path", currentPath),
		logger.Int("item_count", len(items)),
		logger.String("ip", ctx.RealIP()),
	)

	return ctx.JSON(http.StatusOK, response)
}

// validateSymlinkTarget validates that a symlink target is within allowed boundaries
func (c *Handler) validateSymlinkTarget(symlinkPath string) error {
	// Use SecureFS to safely read the symlink target within the sandbox
	target, err := c.SFS.Readlink(symlinkPath)
	if err != nil {
		return fmt.Errorf("failed to read symlink target: %w", err)
	}

	// If target is relative, resolve it relative to the symlink's directory
	var targetPath string
	if filepath.IsAbs(target) {
		targetPath = target
	} else {
		// Resolve relative target relative to the symlink's directory
		symlinkDir := filepath.Dir(symlinkPath)
		targetPath = filepath.Join(symlinkDir, target)
	}

	// Validate that the resolved target is within the SecureFS boundaries
	_, err = c.SFS.RelativePath(targetPath)
	if err != nil {
		return fmt.Errorf("symlink target outside allowed boundaries: %w", err)
	}

	return nil
}

// convertDirEntryToItem converts a fs.DirEntry to FileSystemItem with enhanced error context
func (c *Handler) convertDirEntryToItem(basePath string, entry fs.DirEntry) (FileSystemItem, error) {
	fullPath := filepath.Join(basePath, entry.Name())

	info, err := entry.Info()
	if err != nil {
		return FileSystemItem{}, fmt.Errorf("unable to get file info for '%s' in directory '%s': %w", entry.Name(), basePath, err)
	}

	itemType := "file"
	if info.IsDir() {
		itemType = "folder"
	} else if info.Mode()&os.ModeSymlink != 0 {
		// Mark symlinks distinctly for frontend handling
		itemType = "symlink"
	}

	return FileSystemItem{
		ID:   fullPath,
		Size: info.Size(),
		Date: info.ModTime(),
		Type: itemType,
		Name: entry.Name(),
	}, nil
}
