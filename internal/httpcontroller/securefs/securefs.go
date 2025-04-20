package securefs

import (
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
)

// SecureFS provides filesystem operations with path validation
// using Go 1.24's os.Root for OS-level filesystem sandboxing.
//
// The os.Root feature provides directory-limited filesystem access,
// preventing path traversal vulnerabilities by enforcing access boundaries at the OS level.
// This implementation reliably prevents access to files outside the specified directory,
// including via symlinks, relative paths, or other traversal techniques.
//
// Security guarantees:
// - Prevents directory traversal attacks using "../" or other relative paths
// - Prevents access via symlinks that point outside the base directory
// - Prevents time-of-check/time-of-use (TOCTOU) race conditions
// - Prevents access to reserved Windows device names
// - Handles platform-specific path validation (Windows, Unix, WASI, etc.)
type SecureFS struct {
	baseDir  string   // The base directory that all operations are restricted to
	root     *os.Root // The sandboxed filesystem root
	pipeName string   // Platform-specific pipe name for named pipes
}

// New creates a new secure filesystem with the specified base directory
// using Go 1.24's os.Root for OS-level sandboxing. All operations through the
// returned SecureFS will be restricted to the specified base directory.
func New(baseDir string) (*SecureFS, error) {
	// Ensure baseDir is an absolute path
	absPath, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve base path: %w", err)
	}

	// Create the directory if it doesn't exist
	if err := os.MkdirAll(absPath, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create base directory: %w", err)
	}

	// Create a sandboxed filesystem with os.Root
	// This is a Go 1.24 feature that provides OS-level filesystem isolation
	root, err := os.OpenRoot(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create filesystem sandbox: %w", err)
	}

	return &SecureFS{
		baseDir: absPath,
		root:    root,
	}, nil
}

// IsPathWithinBase checks if targetPath is within or equal to basePath
func IsPathWithinBase(basePath, targetPath string) (bool, error) {
	// Resolve both paths to absolute, clean paths
	absBase, err := filepath.Abs(basePath)
	if err != nil {
		return false, fmt.Errorf("failed to resolve base path: %w", err)
	}

	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return false, fmt.Errorf("failed to resolve target path: %w", err)
	}

	// Clean paths to remove any . or .. components
	absBase = filepath.Clean(absBase)
	absTarget = filepath.Clean(absTarget)

	// Check if the path is local (no traversal components)
	if !filepath.IsLocal(filepath.Base(absTarget)) {
		return false, nil
	}

	// For paths that don't exist yet, we can only do string prefix comparison
	// which is good enough for testing and validation
	if _, err := os.Stat(absTarget); os.IsNotExist(err) {
		// Check if target path starts with base path
		return strings.HasPrefix(absTarget, absBase+string(filepath.Separator)) || absTarget == absBase, nil
	}

	// For paths that exist, try to resolve symlinks
	// If the base path is accessible, resolve it
	resolvedBase, err := filepath.EvalSymlinks(absBase)
	if err == nil {
		absBase = resolvedBase
	}

	// If the target path is accessible, resolve it
	resolvedTarget, err := filepath.EvalSymlinks(absTarget)
	if err == nil {
		absTarget = resolvedTarget
	} else {
		// Handle the case where intermediate components might be symlinks
		// This is a fallback for paths that don't fully exist yet
		dir := filepath.Dir(absTarget)
		// Try to resolve parent directories if possible
		for dir != "/" && dir != "." && dir != "" {
			resolvedDir, err := filepath.EvalSymlinks(dir)
			if err == nil && resolvedDir != dir {
				// Found a parent directory that's a symlink
				// Reconstruct the target with the resolved parent
				base := filepath.Base(absTarget)
				absTarget = filepath.Join(resolvedDir, base)
				break
			}
			dir = filepath.Dir(dir)
		}
	}

	// Clean paths again after symlink resolution
	absBase = filepath.Clean(absBase)
	absTarget = filepath.Clean(absTarget)

	// Check if target path starts with base path or is exactly the base path
	return strings.HasPrefix(absTarget, absBase+string(filepath.Separator)) || absTarget == absBase, nil
}

// IsPathValidWithinBase is a helper that checks if a path is within a base directory
// and returns an error if not
func IsPathValidWithinBase(baseDir, path string) error {
	isWithin, err := IsPathWithinBase(baseDir, path)
	if err != nil {
		// If the error is because the target doesn't exist, don't treat it as a security error
		// This is common during cleanup operations when we're checking paths that might be gone
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("path validation error: %w", err)
	}

	if !isWithin {
		return fmt.Errorf("security error: path %s is outside allowed directory %s", path, baseDir)
	}

	return nil
}

// relativePath converts an absolute path to a path relative to the base directory
// This is used internally to prepare paths for os.Root operations
func (sfs *SecureFS) relativePath(path string) (string, error) {
	// Clean the path to handle any . or .. components safely
	path = filepath.Clean(path)

	// Get absolute paths for consistent comparison
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Since the path will be used with os.Root, which already provides
	// OS-level security against traversal, we mostly need to make the path relative.
	// However, we still validate it as a defense-in-depth measure.

	// Verify the path is within the base directory for safety
	// Additional check using filepath.IsLocal for defense in depth
	if !filepath.IsLocal(filepath.Base(absPath)) {
		return "", fmt.Errorf("security error: path contains invalid components")
	}

	// Using the updated IsPathWithinBase that handles non-existent paths
	isWithin, err := IsPathWithinBase(sfs.baseDir, absPath)
	if err != nil {
		return "", fmt.Errorf("path validation error: %w", err)
	}

	if !isWithin {
		return "", fmt.Errorf("security error: path %s is outside allowed directory %s", path, sfs.baseDir)
	}

	// Make the path relative to the base directory for os.Root operations
	relPath, err := filepath.Rel(sfs.baseDir, absPath)
	if err != nil {
		return "", fmt.Errorf("failed to make path relative: %w", err)
	}

	// Ensure no leading slash which would make a relative path be treated as absolute
	relPath = strings.TrimPrefix(relPath, string(filepath.Separator))

	return relPath, nil
}

// MkdirAll creates a directory and all necessary parent directories with path validation
func (sfs *SecureFS) MkdirAll(path string, perm os.FileMode) error {
	// Get relative path for os.Root operations
	relPath, err := sfs.relativePath(path)
	if err != nil {
		return err
	}

	// If the path is the root, it's already created
	if relPath == "" || relPath == "." {
		return nil
	}

	// Create directories recursively
	components := strings.Split(relPath, string(filepath.Separator))
	currentPath := ""

	// Create each directory component
	for i, component := range components {
		// Skip empty components that might result from path normalization
		if component == "" {
			continue
		}

		// Build the current path
		if currentPath == "" {
			currentPath = component
		} else {
			currentPath = filepath.Join(currentPath, component)
		}

		// Try to create the directory using os.Root.Mkdir
		// Ignore "already exists" errors
		err := sfs.root.Mkdir(currentPath, perm)
		if err != nil && !os.IsExist(err) {
			return fmt.Errorf("failed to create directory component %s: %w", currentPath, err)
		}

		// If this is the last component, we're done
		if i == len(components)-1 {
			return nil
		}
	}

	return nil
}

// RemoveAll removes a directory and all its contents with path validation
// This implementation provides a more secure alternative to os.RemoveAll by using
// os.Root operations for each individual file/directory where possible
func (sfs *SecureFS) RemoveAll(path string) error {
	// Get relative path for os.Root operations
	relPath, err := sfs.relativePath(path)
	if err != nil {
		return err
	}

	// If the path doesn't exist, there's nothing to remove
	info, err := sfs.root.Stat(relPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	// For non-directories, just use os.Root.Remove
	if !info.IsDir() {
		return sfs.root.Remove(relPath)
	}

	// For directories, we need a recursive solution since os.Root doesn't have RemoveAll
	// Open the directory securely using os.Root
	dir, err := sfs.root.Open(relPath)
	if err != nil {
		return err
	}
	defer dir.Close()

	// Read directory entries
	entries, err := dir.ReadDir(0) // 0 means read all entries
	if err != nil {
		return err
	}

	// Remove each entry in the directory
	for _, entry := range entries {
		childRelPath := filepath.Join(relPath, entry.Name())

		if entry.IsDir() {
			// Recursive call for subdirectories
			if err := sfs.RemoveAll(filepath.Join(sfs.baseDir, childRelPath)); err != nil {
				return err
			}
		} else {
			// Remove files directly
			if err := sfs.root.Remove(childRelPath); err != nil {
				return err
			}
		}
	}

	// Now remove the empty directory
	return sfs.root.Remove(relPath)
}

// Remove removes a file with path validation
func (sfs *SecureFS) Remove(path string) error {
	// Get relative path for os.Root operations
	relPath, err := sfs.relativePath(path)
	if err != nil {
		return err
	}

	// Use os.Root.Remove to safely remove the file
	return sfs.root.Remove(relPath)
}

// OpenFile opens a file with path validation
func (sfs *SecureFS) OpenFile(path string, flag int, perm os.FileMode) (*os.File, error) {
	// Get relative path for os.Root operations
	relPath, err := sfs.relativePath(path)
	if err != nil {
		return nil, err
	}

	// Use os.Root.OpenFile to safely open the file
	return sfs.root.OpenFile(relPath, flag, perm)
}

// Open opens a file for reading with path validation
func (sfs *SecureFS) Open(path string) (*os.File, error) {
	// Get relative path for os.Root operations
	relPath, err := sfs.relativePath(path)
	if err != nil {
		return nil, err
	}

	// Use os.Root.Open to safely open the file
	return sfs.root.Open(relPath)
}

// OpenRoot opens a subdirectory as a new Root for further operations
// This provides a way to further restrict operations to a subdirectory
func (sfs *SecureFS) OpenRoot(path string) (*os.Root, error) {
	// Get relative path for os.Root operations
	relPath, err := sfs.relativePath(path)
	if err != nil {
		return nil, err
	}

	// Use os.Root.OpenRoot to safely open the subdirectory as a new Root
	return sfs.root.OpenRoot(relPath)
}

// Stat returns file info with path validation
func (sfs *SecureFS) Stat(path string) (fs.FileInfo, error) {
	// Get relative path for os.Root operations
	relPath, err := sfs.relativePath(path)
	if err != nil {
		return nil, err
	}

	// Use os.Root.Stat to safely get file info
	return sfs.root.Stat(relPath)
}

// Lstat returns file info without following symlinks
func (sfs *SecureFS) Lstat(path string) (fs.FileInfo, error) {
	// Get relative path for os.Root operations
	relPath, err := sfs.relativePath(path)
	if err != nil {
		return nil, err
	}

	// Use os.Root.Lstat to safely get file info without following symlinks
	return sfs.root.Lstat(relPath)
}

// Exists checks if a path exists with validation
func (sfs *SecureFS) Exists(path string) bool {
	// Get relative path for os.Root operations
	relPath, err := sfs.relativePath(path)
	if err != nil {
		return false
	}

	// Use os.Root.Stat to check if the file exists
	_, err = sfs.root.Stat(relPath)
	return err == nil
}

// ReadFile reads a file with path validation and returns its contents
func (sfs *SecureFS) ReadFile(path string) ([]byte, error) {
	// Open the file using secure methods
	file, err := sfs.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Read the entire file
	return io.ReadAll(file)
}

// WriteFile writes data to a file with path validation
func (sfs *SecureFS) WriteFile(path string, data []byte, perm os.FileMode) error {
	// Create or truncate the file
	file, err := sfs.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer file.Close()

	// Write the data
	_, err = file.Write(data)
	return err
}

// ServeFile serves a file through an HTTP response
// This provides a secure alternative to echo.Context.File()
func (sfs *SecureFS) ServeFile(c echo.Context, path string) error {
	// Get relative path for os.Root operations
	relPath, err := sfs.relativePath(path)
	if err != nil {
		return err
	}

	// Open the file using os.Root for sandbox protection
	file, err := sfs.root.Open(relPath)
	if err != nil {
		if os.IsNotExist(err) {
			return echo.NewHTTPError(http.StatusNotFound, "File not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to open file")
	}
	defer file.Close()

	// Get file info for content-length
	stat, err := file.Stat()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to get file info")
	}

	// Only serve regular files
	if !stat.Mode().IsRegular() {
		return echo.NewHTTPError(http.StatusForbidden, "Not a regular file")
	}

	// Set content type based on file extension
	contentType := mime.TypeByExtension(filepath.Ext(path))
	if contentType != "" {
		c.Response().Header().Set(echo.HeaderContentType, contentType)
	}

	// Set content length
	c.Response().Header().Set(echo.HeaderContentLength, strconv.FormatInt(stat.Size(), 10))

	// Stream the file directly from within the sandbox
	_, err = io.Copy(c.Response(), file)
	return err
}

// SetPipeName sets the platform-specific pipe name for this SecureFS instance
func (sfs *SecureFS) SetPipeName(pipeName string) {
	sfs.pipeName = pipeName
}

// GetPipeName returns the platform-specific pipe name for the given path
func (sfs *SecureFS) GetPipeName() string {
	return sfs.pipeName
}

// Close closes the underlying Root
func (sfs *SecureFS) Close() error {
	if sfs.root != nil {
		return sfs.root.Close()
	}
	return nil
}

// ReadDir reads the directory named by path and returns
// a list of directory entries, securely using os.Root.
func (sfs *SecureFS) ReadDir(path string) ([]os.DirEntry, error) {
	// Get relative path for os.Root operations
	relPath, err := sfs.relativePath(path)
	if err != nil {
		return nil, err
	}

	// Handle empty path (root directory)
	if relPath == "" || relPath == "." {
		relPath = "."
	}

	// Open the directory using os.Root
	dirFile, err := sfs.root.Open(relPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open directory: %w", err)
	}
	defer dirFile.Close()

	// Read directory entries
	entries, err := dirFile.ReadDir(0) // 0 means read all entries
	if err != nil {
		return nil, fmt.Errorf("failed to read directory entries: %w", err)
	}

	return entries, nil
}
