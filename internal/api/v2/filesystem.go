// internal/api/v2/filesystem.go
package api

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/labstack/echo/v4"
)

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

// initFileSystemRoutes registers all filesystem-related API endpoints
func (c *Controller) initFileSystemRoutes() {
	if c.apiLogger != nil {
		c.apiLogger.Info("Initializing filesystem routes")
	}

	// Create filesystem API group with authentication
	fsGroup := c.Group.Group("/filesystem", c.authMiddleware)

	// GET /api/v2/filesystem/browse - Browse files and directories
	fsGroup.GET("/browse", c.BrowseFileSystem)

	if c.apiLogger != nil {
		c.apiLogger.Info("Filesystem routes initialized")
	}
}

// BrowseFileSystem lists files and directories in the specified path
func (c *Controller) BrowseFileSystem(ctx echo.Context) error {
	var req BrowseRequest
	if err := ctx.Bind(&req); err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to bind browse request",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, err, "Invalid request parameters", http.StatusBadRequest)
	}

	// Default to base directory if no path specified
	browsePath := req.Path
	if browsePath == "" {
		// Use SecureFS base directory as default
		browsePath = c.SFS.BaseDir()
	}

	// Convert to relative path and validate using SecureFS
	relPath, err := c.SFS.RelativePath(browsePath)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Warn("Invalid or unsafe path requested",
				"requested_path", req.Path,
				"error", err.Error(),
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, err, "Invalid or unsafe path", http.StatusBadRequest)
	}

	// Check for symlinks and validate targets
	info, err := c.SFS.Lstat(browsePath)
	if err != nil {
		if os.IsNotExist(err) {
			return c.HandleError(ctx, err, "Path does not exist", http.StatusNotFound)
		}
		return c.HandleError(ctx, err, "Unable to access path", http.StatusForbidden)
	}

	// If it's a symlink, validate the target
	if info.Mode()&os.ModeSymlink != 0 {
		if err := c.validateSymlinkTarget(browsePath); err != nil {
			if c.apiLogger != nil {
				c.apiLogger.Warn("Symlink target validation failed",
					"symlink_path", browsePath,
					"error", err.Error(),
					"ip", ctx.RealIP(),
				)
			}
			return c.HandleError(ctx, err, "Symlink target not allowed", http.StatusForbidden)
		}
	}

	// Ensure it's a directory
	if !info.IsDir() {
		return c.HandleError(ctx, fmt.Errorf("not a directory"), "Path is not a directory", http.StatusBadRequest)
	}

	// Read directory contents using SecureFS
	entries, err := c.SFS.ReadDir(browsePath)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to read directory",
				"path", browsePath,
				"error", err.Error(),
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, err, "Unable to read directory", http.StatusForbidden)
	}

	// Convert to response format
	items := make([]FileSystemItem, 0, len(entries))
	for _, entry := range entries {
		item, err := c.convertDirEntryToItem(browsePath, entry)
		if err != nil {
			// Log error but continue with other files
			if c.apiLogger != nil {
				c.apiLogger.Debug("Skipping file due to error",
					"file", entry.Name(),
					"directory", browsePath,
					"error", err.Error(),
				)
			}
			continue
		}
		items = append(items, item)
	}

	// Determine parent path securely using SecureFS
	parentPath, err := c.SFS.ParentPath(browsePath)
	if err != nil {
		// Log error but continue - parent path is optional
		if c.apiLogger != nil {
			c.apiLogger.Debug("Failed to get parent path", "path", browsePath, "error", err.Error())
		}
		parentPath = ""
	}

	// Get the current absolute path for response
	currentPath := browsePath
	if !filepath.IsAbs(currentPath) {
		currentPath = filepath.Join(c.SFS.BaseDir(), relPath)
	}

	response := BrowseResponse{
		Items:       items,
		CurrentPath: currentPath,
		ParentPath:  parentPath,
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Successfully browsed directory",
			"path", currentPath,
			"item_count", len(items),
			"ip", ctx.RealIP(),
		)
	}

	return ctx.JSON(http.StatusOK, response)
}

// validateSymlinkTarget validates that a symlink target is within allowed boundaries
func (c *Controller) validateSymlinkTarget(symlinkPath string) error {
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
func (c *Controller) convertDirEntryToItem(basePath string, entry fs.DirEntry) (FileSystemItem, error) {
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