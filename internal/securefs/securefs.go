package securefs

import (
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// GetLogger returns the securefs package logger scoped to the securefs module.
// The logger is fetched from the global logger each time to ensure it uses
// the current centralized logger (which may be set after package init).
func GetLogger() logger.Logger {
	return logger.Global().Module("securefs")
}

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
	baseDir         string     // The base directory that all operations are restricted to
	root            *os.Root   // The sandboxed filesystem root
	pipeName        string     // Platform-specific pipe name for named pipes
	cache           *PathCache // Smart memoization cache for expensive operations
	maxReadFileSize int64      // Maximum file size for ReadFile (0 = unlimited)
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

	// Create the directory if it doesn't exist with secure permissions
	// Only owner can write, others can read/execute for serving files
	if err := os.MkdirAll(absPath, 0o750); err != nil {
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
		cache:   NewPathCache(),
	}, nil
}

// IsPathWithinBase checks if targetPath is within or equal to basePath
func IsPathWithinBase(basePath, targetPath string) (bool, error) {
	return IsPathWithinBaseWithCache(nil, basePath, targetPath)
}

// resolveAbsPath resolves a path to absolute, using cache if available
func resolveAbsPath(cache *PathCache, path string) (string, error) {
	if cache != nil {
		return cache.GetAbsPath(path, filepath.Abs)
	}
	return filepath.Abs(path)
}

// resolveSymlinks resolves symlinks for a path, using cache if available
func resolveSymlinks(cache *PathCache, path string) string {
	var resolved string
	var err error

	if cache != nil {
		resolved, err = cache.GetSymlinkResolution(path, filepath.EvalSymlinks)
	} else {
		resolved, err = filepath.EvalSymlinks(path)
	}

	if err == nil {
		return resolved
	}
	return path
}

// resolveParentSymlinks attempts to resolve symlinks in parent directories
// when the full path cannot be resolved (e.g., file doesn't exist yet)
func resolveParentSymlinks(cache *PathCache, absTarget string) string {
	dir := filepath.Dir(absTarget)

	for dir != "/" && dir != "." && dir != "" {
		resolvedDir := resolveSymlinks(cache, dir)
		if resolvedDir != dir {
			// Found a parent directory that's a symlink
			// Reconstruct the target with the resolved parent
			return filepath.Join(resolvedDir, filepath.Base(absTarget))
		}
		dir = filepath.Dir(dir)
	}
	return absTarget
}

// isPathPrefix checks if target is within or equal to base
func isPathPrefix(absBase, absTarget string) bool {
	return strings.HasPrefix(absTarget, absBase+string(filepath.Separator)) || absTarget == absBase
}

// IsPathWithinBaseWithCache checks if targetPath is within or equal to basePath with optional caching
func IsPathWithinBaseWithCache(cache *PathCache, basePath, targetPath string) (bool, error) {
	absBase, err := resolveAbsPath(cache, basePath)
	if err != nil {
		return false, fmt.Errorf("failed to resolve base path: %w", err)
	}

	absTarget, err := resolveAbsPath(cache, targetPath)
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
	if _, err := os.Stat(absTarget); os.IsNotExist(err) {
		return isPathPrefix(absBase, absTarget), nil
	}

	// Resolve symlinks for existing paths
	absBase = resolveSymlinks(cache, absBase)
	resolved := resolveSymlinks(cache, absTarget)

	// If target symlink resolution failed, try resolving parent directories
	if resolved == absTarget {
		absTarget = resolveParentSymlinks(cache, absTarget)
	} else {
		absTarget = resolved
	}

	// Clean paths again after symlink resolution
	absBase = filepath.Clean(absBase)
	absTarget = filepath.Clean(absTarget)

	return isPathPrefix(absBase, absTarget), nil
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
		return fmt.Errorf("%w: path %s is outside allowed directory %s",
			ErrPathTraversal, path, baseDir)
	}

	return nil
}

// RelativePath converts an absolute path to a path relative to the base directory
// and validates it against the SecureFS root.
// This is used internally and potentially by callers needing the relative path.
func (sfs *SecureFS) RelativePath(path string) (string, error) {
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
		return "", fmt.Errorf("%w: path contains invalid components", ErrInvalidPath)
	}

	// Using the cached version of IsPathWithinBase for better performance
	cacheKey := fmt.Sprintf("%s|%s", sfs.baseDir, absPath)
	isWithin, err := sfs.cache.GetWithinBase(cacheKey, func() (bool, error) {
		return IsPathWithinBaseWithCache(sfs.cache, sfs.baseDir, absPath)
	})
	if err != nil {
		return "", fmt.Errorf("path validation error: %w", err)
	}

	if !isWithin {
		return "", fmt.Errorf("%w: path %s is outside allowed directory %s", ErrPathTraversal, path, sfs.baseDir)
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

// ValidateRelativePath validates a path assumed to be relative to the base directory.
// It returns a cleaned, validated path or an error if the path is not valid.
func (sfs *SecureFS) ValidateRelativePath(relPath string) (string, error) {
	// Use cache for path validation as this is expensive and deterministic
	return sfs.cache.GetValidatePath(relPath, func(path string) (string, error) {
		// Clean the path first to resolve . and .. components where possible
		cleanedPath := filepath.Clean(path)

		// Check for absolute paths after cleaning (should not happen if input is truly relative)
		if filepath.IsAbs(cleanedPath) {
			return "", fmt.Errorf("%w: path must be relative, but got '%s' after cleaning '%s'",
				ErrInvalidPath, cleanedPath, path)
		}

		// Check for attempts to traverse upwards from the root.
		// After cleaning, paths starting with ".." indicate an attempt to go above the root.
		if strings.HasPrefix(cleanedPath, ".."+string(filepath.Separator)) || cleanedPath == ".." {
			return "", fmt.Errorf("%w: '%s' (cleaned from '%s')",
				ErrPathTraversal, cleanedPath, path)
		}

		// Ensure no leading separator after cleaning (should be handled by Clean, but double-check)
		cleanedPath = strings.TrimPrefix(cleanedPath, string(filepath.Separator))

		// The path is considered valid relative to the os.Root sandbox
		return cleanedPath, nil
	})
}

// createDirComponent attempts to create a single directory component, ignoring "already exists" errors
func (sfs *SecureFS) createDirComponent(path string, perm os.FileMode) error {
	err := sfs.root.Mkdir(path, perm)
	if err != nil && !os.IsExist(err) {
		return fmt.Errorf("failed to create directory component %s: %w", path, err)
	}
	return nil
}

// MkdirAll creates a directory and all necessary parent directories with path validation
func (sfs *SecureFS) MkdirAll(path string, perm os.FileMode) error {
	relPath, err := sfs.RelativePath(path)
	if err != nil {
		return err
	}

	if relPath == "" || relPath == "." {
		return nil
	}

	components := strings.Split(relPath, string(filepath.Separator))
	currentPath := ""

	for _, component := range components {
		if component == "" {
			continue
		}

		currentPath = filepath.Join(currentPath, component)
		if err := sfs.createDirComponent(currentPath, perm); err != nil {
			return err
		}
	}

	return nil
}

// removeAllRelative removes a path using already-validated relative path
func (sfs *SecureFS) removeAllRelative(relPath string) error {
	info, err := sfs.root.Stat(relPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	if !info.IsDir() {
		return sfs.root.Remove(relPath)
	}

	return sfs.removeDirContents(relPath)
}

// removeDirContents removes all contents of a directory and then the directory itself
func (sfs *SecureFS) removeDirContents(relPath string) error {
	dir, err := sfs.root.Open(relPath)
	if err != nil {
		return err
	}

	entries, err := dir.ReadDir(0)
	if closeErr := dir.Close(); closeErr != nil {
		GetLogger().Warn("Failed to close directory",
			logger.String("path", relPath),
			logger.Error(closeErr))
	}
	if err != nil {
		return err
	}

	for _, entry := range entries {
		childPath := filepath.Join(relPath, entry.Name())
		if err := sfs.removeAllRelative(childPath); err != nil {
			return err
		}
	}

	return sfs.root.Remove(relPath)
}

// RemoveAll removes a directory and all its contents with path validation
// This implementation provides a more secure alternative to os.RemoveAll by using
// os.Root operations for each individual file/directory where possible
func (sfs *SecureFS) RemoveAll(path string) error {
	relPath, err := sfs.RelativePath(path)
	if err != nil {
		return err
	}
	return sfs.removeAllRelative(relPath)
}

// Remove removes a file with path validation
func (sfs *SecureFS) Remove(path string) error {
	// Get relative path for os.Root operations
	relPath, err := sfs.RelativePath(path)
	if err != nil {
		return err
	}

	// Use os.Root.Remove to safely remove the file
	return sfs.root.Remove(relPath)
}

// Rename renames (moves) oldpath to newpath within the sandbox.
// Both paths must be relative to the SecureFS base directory.
// This uses os.Root.Rename (Go 1.25+) to maintain the security boundary.
func (sfs *SecureFS) Rename(oldpath, newpath string) error {
	// Get relative paths for os.Root operations
	oldRelPath, err := sfs.RelativePath(oldpath)
	if err != nil {
		return err
	}

	newRelPath, err := sfs.RelativePath(newpath)
	if err != nil {
		return err
	}

	// Use os.Root.Rename to safely rename within the sandbox
	return sfs.root.Rename(oldRelPath, newRelPath)
}

// OpenFile opens a file with path validation
func (sfs *SecureFS) OpenFile(path string, flag int, perm os.FileMode) (*os.File, error) {
	// Get relative path for os.Root operations
	relPath, err := sfs.RelativePath(path)
	if err != nil {
		return nil, err
	}

	// Use os.Root.OpenFile to safely open the file
	return sfs.root.OpenFile(relPath, flag, perm)
}

// Open opens a file for reading with path validation
func (sfs *SecureFS) Open(path string) (*os.File, error) {
	// Get relative path for os.Root operations
	relPath, err := sfs.RelativePath(path)
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
	relPath, err := sfs.RelativePath(path)
	if err != nil {
		return nil, err
	}

	// Use os.Root.OpenRoot to safely open the subdirectory as a new Root
	return sfs.root.OpenRoot(relPath)
}

// Stat returns file info with path validation
func (sfs *SecureFS) Stat(path string) (fs.FileInfo, error) {
	// Get relative path for os.Root operations
	relPath, err := sfs.RelativePath(path)
	if err != nil {
		return nil, err
	}

	// Use os.Root.Stat to safely get file info
	return sfs.root.Stat(relPath)
}

// Lstat returns file info without following symlinks
func (sfs *SecureFS) Lstat(path string) (fs.FileInfo, error) {
	// Get relative path for os.Root operations
	relPath, err := sfs.RelativePath(path)
	if err != nil {
		return nil, err
	}

	// Use os.Root.Lstat to safely get file info without following symlinks
	return sfs.root.Lstat(relPath)
}

// StatRel returns file info for a path assumed to be relative to the base directory.
// It uses ValidateRelativePath for security checks.
func (sfs *SecureFS) StatRel(relPath string) (fs.FileInfo, error) {
	// Validate the provided relative path (uses cache)
	validatedRelPath, err := sfs.ValidateRelativePath(relPath)
	if err != nil {
		return nil, err
	}

	// Use cache for stat operations as filesystem calls are expensive
	return sfs.cache.GetStat(validatedRelPath, func(path string) (fs.FileInfo, error) {
		return sfs.root.Stat(path)
	})
}

// Exists checks if a path exists with validation
func (sfs *SecureFS) Exists(path string) (bool, error) {
	// Get relative path for os.Root operations
	relPath, err := sfs.RelativePath(path)
	if err != nil {
		// Return the validation error instead of silently returning false
		return false, err
	}

	// Use os.Root.Stat to check if the file exists
	_, err = sfs.root.Stat(relPath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err // propagate unexpected errors
}

// ExistsNoErr is a convenience method that returns only a boolean
// This is helpful for backward compatibility with existing code
// Note: This method will log security errors rather than returning them
func (sfs *SecureFS) ExistsNoErr(path string) bool {
	exists, err := sfs.Exists(path)
	if err != nil {
		GetLogger().Warn("Failed to validate path in Exists check",
			logger.String("path", path),
			logger.Error(err))
		return false
	}
	return exists
}

// SetMaxReadFileSize sets the maximum file size that ReadFile will read.
// A value of 0 means unlimited (no size check).
// This helps prevent memory exhaustion from reading very large files.
func (sfs *SecureFS) SetMaxReadFileSize(maxSize int64) {
	sfs.maxReadFileSize = maxSize
}

// GetMaxReadFileSize returns the current maximum file size for ReadFile.
func (sfs *SecureFS) GetMaxReadFileSize() int64 {
	return sfs.maxReadFileSize
}

// ErrFileTooLarge is returned when a file exceeds the configured size limit
var ErrFileTooLarge = errors.NewStd("file size exceeds maximum allowed size")

// ReadFile reads a file with path validation and returns its contents.
// If maxReadFileSize is set (> 0), files exceeding that size will return ErrFileTooLarge.
func (sfs *SecureFS) ReadFile(path string) ([]byte, error) {
	// Open the file using secure methods
	file, err := sfs.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			GetLogger().Warn("Failed to close file", logger.Error(err))
		}
	}()

	// Check file size if limit is configured
	if sfs.maxReadFileSize > 0 {
		stat, err := file.Stat()
		if err != nil {
			return nil, fmt.Errorf("failed to stat file: %w", err)
		}
		if stat.Size() > sfs.maxReadFileSize {
			return nil, fmt.Errorf("%w: file is %d bytes, limit is %d bytes",
				ErrFileTooLarge, stat.Size(), sfs.maxReadFileSize)
		}
	}

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
	defer func() {
		if err := file.Close(); err != nil {
			GetLogger().Warn("Failed to close file", logger.Error(err))
		}
	}()

	// Write the data
	_, err = file.Write(data)
	return err
}

// mapOpenErrorToHTTP converts file open errors to appropriate HTTP errors
func mapOpenErrorToHTTP(err error, effectivePath string) *echo.HTTPError {
	switch {
	case errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist):
		return echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("File not found: %s", effectivePath))
	case errors.Is(err, fs.ErrPermission) || errors.Is(err, os.ErrPermission) || errors.Is(err, ErrAccessDenied):
		return echo.NewHTTPError(http.StatusForbidden, "Access denied")
	case errors.Is(err, ErrPathTraversal) || errors.Is(err, ErrInvalidPath):
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid file path").SetInternal(err)
	case errors.Is(err, ErrNotRegularFile):
		return echo.NewHTTPError(http.StatusForbidden, "Not a regular file")
	default:
		GetLogger().Error("Unhandled error serving file",
			logger.String("path", effectivePath),
			logger.Error(err))
		return echo.NewHTTPError(http.StatusInternalServerError, "Error serving file").SetInternal(err)
	}
}

// getContentType determines the content type for a file, using extension-based detection
func getContentType(path string) string {
	contentType := mime.TypeByExtension(filepath.Ext(path))
	if contentType == "" {
		return "application/octet-stream"
	}
	return contentType
}

// serveInternal handles the core logic for serving a file using an opener function.
// The opener function is responsible for securely opening the file and returning
// the file handle, the effective path used (for logging/headers), and any error.
func (sfs *SecureFS) serveInternal(c echo.Context, opener func() (*os.File, string, error)) error {
	f, effectivePath, err := opener()
	if err != nil {
		GetLogger().Error("Error opening file via opener",
			logger.String("path", effectivePath),
			logger.Error(err))
		return mapOpenErrorToHTTP(err, effectivePath)
	}
	defer func() {
		if err := f.Close(); err != nil {
			GetLogger().Warn("Failed to close file", logger.Error(err))
		}
	}()

	stat, err := f.Stat()
	if err != nil {
		GetLogger().Error("Stat error",
			logger.String("path", effectivePath),
			logger.Error(err))
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to get file info").SetInternal(err)
	}

	if !stat.Mode().IsRegular() {
		return echo.NewHTTPError(http.StatusForbidden, "Not a regular file")
	}

	// Only set content type if not already set by the caller
	if c.Response().Header().Get(echo.HeaderContentType) == "" {
		c.Response().Header().Set(echo.HeaderContentType, getContentType(effectivePath))
	}

	http.ServeContent(c.Response(), c.Request(), filepath.Base(effectivePath), stat.ModTime(), f)
	return nil
}

// ServeFile serves a file through an HTTP response
// This provides a secure alternative to echo.Context.File()
// It assumes the input 'path' might be absolute or relative to CWD and validates it.
func (sfs *SecureFS) ServeFile(c echo.Context, path string) error {
	return sfs.serveInternal(c, func() (*os.File, string, error) {
		// Get relative path securely
		relPath, err := sfs.RelativePath(path)
		if err != nil {
			// DO NOT wrap validation errors, return them directly
			// Return original path for better error logging even on validation failure
			return nil, path, err
		}

		// Open the file using the sandboxed root
		file, err := sfs.root.Open(relPath)
		if err != nil {
			// Wrap operational errors for context
			return nil, relPath, fmt.Errorf("ServeFile open failed for relPath '%s': %w", relPath, err)
		}
		// Return the file handle, the validated relative path, and nil error
		return file, relPath, nil
	})
}

// ServeRelativeFile serves a file through an HTTP response, assuming the input path
// is already relative to the SecureFS base directory. It validates this relative path.
func (sfs *SecureFS) ServeRelativeFile(c echo.Context, relPath string) error {
	return sfs.serveInternal(c, func() (*os.File, string, error) {
		// Validate the assumed relative path
		validatedRelPath, err := sfs.ValidateRelativePath(relPath)
		if err != nil {
			// DO NOT wrap validation errors, return them directly
			// Return original path for better error logging even on validation failure
			return nil, relPath, err
		}

		// Open the file using the sandboxed root with the validated path
		file, err := sfs.root.Open(validatedRelPath)
		if err != nil {
			// Wrap operational errors for context
			return nil, validatedRelPath, fmt.Errorf("ServeRelativeFile open failed for validatedRelPath '%s': %w", validatedRelPath, err)
		}
		// Return the file handle, the validated relative path, and nil error
		return file, validatedRelPath, nil
	})
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

// ClearExpiredCache removes expired entries from the cache
// This should be called periodically to prevent memory leaks
func (sfs *SecureFS) ClearExpiredCache() {
	if sfs.cache != nil {
		sfs.cache.ClearExpired()
	}
}

// GetCacheStats returns statistics about cache usage for monitoring
func (sfs *SecureFS) GetCacheStats() CacheStats {
	if sfs.cache != nil {
		return sfs.cache.GetCacheStats()
	}
	return CacheStats{}
}

// StartCacheCleanup starts a background goroutine that periodically cleans expired cache entries
func (sfs *SecureFS) StartCacheCleanup(interval time.Duration) chan<- struct{} {
	stopCh := make(chan struct{})

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				sfs.ClearExpiredCache()
			case <-stopCh:
				return
			}
		}
	}()

	return stopCh
}

// ReadDir reads the directory named by path and returns
// a list of directory entries, securely using os.Root.
func (sfs *SecureFS) ReadDir(path string) ([]os.DirEntry, error) {
	// Get relative path for os.Root operations
	relPath, err := sfs.RelativePath(path)
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
	defer func() {
		if err := dirFile.Close(); err != nil {
			GetLogger().Warn("Failed to close directory", logger.Error(err))
		}
	}()

	// Read directory entries
	entries, err := dirFile.ReadDir(0) // 0 means read all entries
	if err != nil {
		return nil, fmt.Errorf("failed to read directory entries: %w", err)
	}

	return entries, nil
}

// BaseDir returns the absolute base directory path of the secure filesystem.
func (sfs *SecureFS) BaseDir() string {
	return sfs.baseDir
}

// ParentPath returns the parent directory path for a given path, or empty string
// if the path is at the root of the SecureFS base directory.
// The input path can be absolute or relative to the base directory.
func (sfs *SecureFS) ParentPath(path string) (string, error) {
	// Convert to relative path first to ensure it's within our boundaries
	relPath, err := sfs.RelativePath(path)
	if err != nil {
		return "", err
	}

	// If we're at the root (empty or "."), there's no parent
	if relPath == "" || relPath == "." {
		return "", nil
	}

	// Get the parent directory of the relative path
	parentRelPath := filepath.Dir(relPath)

	// If parent is "." or same as original, we're at the root
	if parentRelPath == "." || parentRelPath == relPath {
		return "", nil
	}

	// Convert back to absolute path
	parentAbsPath := filepath.Join(sfs.baseDir, parentRelPath)
	return parentAbsPath, nil
}

// Readlink reads the target of a symbolic link, ensuring the symlink file
// itself is within the SecureFS sandbox.
//
// Note: This method returns the symlink target as a string without validating
// whether the target is safe to follow. Security validation occurs when you
// actually try to FOLLOW the symlink (via Open, Stat, etc.).
//
// This is the expected behavior - Readlink is an informational operation
// that tells you what a symlink points to without accessing the target.
func (sfs *SecureFS) Readlink(path string) (string, error) {
	// Clean and get absolute path
	path = filepath.Clean(path)
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Use Lstat to check if the symlink file itself exists (don't follow the link)
	info, err := os.Lstat(absPath)
	if err != nil {
		return "", fmt.Errorf("failed to stat symlink: %w", err)
	}

	// Verify it's actually a symlink
	if info.Mode()&os.ModeSymlink == 0 {
		return "", fmt.Errorf("not a symbolic link: %s", path)
	}

	// Make the path relative to the base directory
	relPath, err := filepath.Rel(sfs.baseDir, absPath)
	if err != nil {
		// This error implies the path is outside the base directory.
		return "", fmt.Errorf("%w: failed to make path relative: %w", ErrPathTraversal, err)
	}

	// Validate the symlink file path itself is within bounds
	// Check for path traversal attempts
	if strings.HasPrefix(relPath, ".."+string(filepath.Separator)) || relPath == ".." {
		return "", fmt.Errorf("%w: symlink path escapes sandbox", ErrPathTraversal)
	}

	// Ensure no leading separator
	relPath = strings.TrimPrefix(relPath, string(filepath.Separator))

	// Use os.Root.Readlink to securely read the symlink target
	return sfs.root.Readlink(relPath)
}
