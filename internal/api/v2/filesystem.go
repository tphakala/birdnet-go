// internal/api/v2/filesystem.go
package api

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

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
	fsGroup := c.Group.Group("/filesystem", c.AuthMiddleware)

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

	// Default to current working directory if no path specified
	browsePath := req.Path
	if browsePath == "" {
		var err error
		browsePath, err = os.Getwd()
		if err != nil {
			if c.apiLogger != nil {
				c.apiLogger.Error("Failed to get current working directory",
					"error", err.Error(),
					"ip", ctx.RealIP(),
				)
			}
			return c.HandleError(ctx, err, "Unable to determine current directory", http.StatusInternalServerError)
		}
	}

	// Validate and sanitize the path
	sanitizedPath, err := c.validateAndSanitizePath(browsePath)
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

	// Check if path exists and is accessible
	if _, err := os.Stat(sanitizedPath); err != nil {
		if os.IsNotExist(err) {
			return c.HandleError(ctx, err, "Path does not exist", http.StatusNotFound)
		}
		return c.HandleError(ctx, err, "Unable to access path", http.StatusForbidden)
	}

	// Read directory contents
	entries, err := os.ReadDir(sanitizedPath)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to read directory",
				"path", sanitizedPath,
				"error", err.Error(),
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, err, "Unable to read directory", http.StatusForbidden)
	}

	// Convert to response format
	items := make([]FileSystemItem, 0, len(entries))
	for _, entry := range entries {
		item, err := c.convertDirEntryToItem(sanitizedPath, entry)
		if err != nil {
			// Log error but continue with other files
			if c.apiLogger != nil {
				c.apiLogger.Debug("Skipping file due to error",
					"path", filepath.Join(sanitizedPath, entry.Name()),
					"error", err.Error(),
				)
			}
			continue
		}
		items = append(items, item)
	}

	// Determine parent path
	parentPath := ""
	if sanitizedPath != "/" && sanitizedPath != "." {
		parentPath = filepath.Dir(sanitizedPath)
		// Clean the parent path
		parentPath = filepath.Clean(parentPath)
	}

	response := BrowseResponse{
		Items:       items,
		CurrentPath: sanitizedPath,
		ParentPath:  parentPath,
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Successfully browsed directory",
			"path", sanitizedPath,
			"item_count", len(items),
			"ip", ctx.RealIP(),
		)
	}

	return ctx.JSON(http.StatusOK, response)
}

// validateAndSanitizePath validates and sanitizes a file system path
func (c *Controller) validateAndSanitizePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path cannot be empty")
	}

	// Clean the path
	cleanPath := filepath.Clean(path)

	// Check for path traversal attempts
	if strings.Contains(cleanPath, "..") {
		return "", fmt.Errorf("path traversal not allowed")
	}

	// Additional security validation - check for suspicious patterns
	if !c.isSecurePath(cleanPath) {
		return "", fmt.Errorf("invalid or potentially unsafe path")
	}

	// Convert to absolute path for consistency
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return "", fmt.Errorf("unable to resolve absolute path: %w", err)
	}

	// Use SecureFS validation for additional security
	if !c.isPathSecure(absPath) {
		return "", fmt.Errorf("path failed security validation")
	}

	// Basic restrictions - prevent access to sensitive system directories
	restrictedPaths := c.getRestrictedPaths()
	for _, restricted := range restrictedPaths {
		if strings.HasPrefix(strings.ToLower(absPath), strings.ToLower(restricted)) {
			return "", fmt.Errorf("access to system directories not allowed")
		}
	}

	return absPath, nil
}

// isSecurePath performs additional security validation on paths
func (c *Controller) isSecurePath(path string) bool {
	// Check for null bytes
	if strings.Contains(path, "\x00") {
		return false
	}

	// Check for invalid UTF-8
	if !utf8.ValidString(path) {
		return false
	}

	// Check for suspicious patterns
	suspiciousPatterns := []string{
		"../", 
		"..\\", 
		"./", 
		".\\",
		"~", 
		"$",
		"`",
		"|",
		"&",
		";",
		"<",
		">",
		"*",
		"?",
	}

	lowerPath := strings.ToLower(path)
	for _, pattern := range suspiciousPatterns {
		if strings.Contains(lowerPath, pattern) {
			return false
		}
	}

	// Check for Windows device names (CON, PRN, AUX, NUL, COM1-9, LPT1-9)
	windowsDevices := []string{
		"con", "prn", "aux", "nul",
		"com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9",
		"lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9",
	}

	baseName := strings.ToLower(filepath.Base(path))
	// Remove extension for device name check
	if dotIndex := strings.LastIndex(baseName, "."); dotIndex > 0 {
		baseName = baseName[:dotIndex]
	}

	for _, device := range windowsDevices {
		if baseName == device {
			return false
		}
	}

	return true
}

// isPathSecure performs comprehensive security checks using securefs principles
func (c *Controller) isPathSecure(path string) bool {
	// Check if path is absolute (should be after filepath.Abs)
	if !filepath.IsAbs(path) {
		return false
	}

	// Check path length to prevent buffer overflow attacks
	if len(path) > 4096 {
		return false
	}

	// Check for control characters
	for _, r := range path {
		if r < 32 && r != 9 && r != 10 && r != 13 { // Allow tab, LF, CR
			return false
		}
	}

	// Additional platform-specific checks
	if runtime.GOOS == "windows" {
		// Check for trailing spaces or dots (Windows limitation)
		baseName := filepath.Base(path)
		if strings.HasSuffix(baseName, " ") || strings.HasSuffix(baseName, ".") {
			return false
		}
	}

	return true
}

// getRestrictedPaths returns a list of paths that should not be accessible
func (c *Controller) getRestrictedPaths() []string {
	restricted := []string{
		"/etc",
		"/proc",
		"/sys",
		"/dev",
		"/boot",
		"/root",
	}

	// Add Windows-specific restricted paths
	if runtime.GOOS == "windows" {
		restricted = append(restricted,
			"C:\\Windows",
			"C:\\Program Files",
			"C:\\Program Files (x86)",
			"C:\\ProgramData",
		)
	}

	return restricted
}

// convertDirEntryToItem converts a fs.DirEntry to FileSystemItem
func (c *Controller) convertDirEntryToItem(basePath string, entry fs.DirEntry) (FileSystemItem, error) {
	fullPath := filepath.Join(basePath, entry.Name())
	
	info, err := entry.Info()
	if err != nil {
		return FileSystemItem{}, fmt.Errorf("unable to get file info: %w", err)
	}

	itemType := "file"
	if info.IsDir() {
		itemType = "folder"
	}

	return FileSystemItem{
		ID:   fullPath,
		Size: info.Size(),
		Date: info.ModTime(),
		Type: itemType,
		Name: entry.Name(),
	}, nil
}