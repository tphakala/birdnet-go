// Package targets provides backup target implementations
package targets

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/backup"
)

// Common constants for file operations and limits
const (
	// File and directory permissions
	PermDir       = 0o700 // rwx------ for directories
	PermFile      = 0o600 // rw------- for files
	PermDirGroup  = 0o750 // rwxr-x--- for shared directories
	PermFileGroup = 0o640 // rw-r----- for shared files

	// Path and size limits
	MaxPathLength      = 4096                    // Maximum total path length
	MaxComponentLength = 255                     // Maximum path component length (filename)
	MaxBackupSizeBytes = 10 * 1024 * 1024 * 1024 // 10GB

	// Buffer sizes
	CopyBufferSize = 32 * 1024 // 32KB for file copies

	// Retry defaults
	DefaultMaxRetries   = 3
	DefaultRetryBackoff = time.Second

	// Connection pool defaults
	DefaultMaxConns = 5

	// Timeout defaults
	DefaultTimeout = 30 * time.Second

	// Default ports
	DefaultFTPPort = 21
	DefaultSSHPort = 22

	// Metadata file extensions
	MetadataFileExt = ".meta"

	// HTTP status codes for error classification
	HTTPUnauthorized    = 401
	HTTPForbidden       = 403
	HTTPNotFound        = 404
	HTTPTooManyRequests = 429
	HTTPInternalError   = 500
	HTTPBadGateway      = 502
	HTTPServiceUnavail  = 503
	HTTPGatewayTimeout  = 504

	// Parsing constants
	MinLsOutputFields = 8 // Minimum fields in ls -l output for parsing
)

// transientErrorPatterns contains substrings that indicate a transient/retriable error
var transientErrorPatterns = []string{
	"connection reset",
	"connection refused",
	"connection closed",
	"timeout",
	"temporary",
	"broken pipe",
	"no route to host",
	"EOF",
	"ssh: handshake failed",
	"resource temporarily unavailable",
}

// IsTransientError determines if an error is likely transient and can be retried.
// This is a shared implementation to avoid duplication across target types.
func IsTransientError(err error) bool {
	if err == nil {
		return false
	}

	// Check for OS timeout errors
	if os.IsTimeout(err) {
		return true
	}

	errStr := err.Error()
	for _, pattern := range transientErrorPatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}
	return false
}

// RetryConfig holds configuration for retry operations
type RetryConfig struct {
	MaxRetries int
	Backoff    time.Duration
	Debug      bool
	DebugLog   func(format string, args ...any)
}

// DefaultRetryConfig returns a RetryConfig with sensible defaults
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries: DefaultMaxRetries,
		Backoff:    DefaultRetryBackoff,
	}
}

// WithRetry executes an operation with retry logic for transient errors.
// The operation is retried up to MaxRetries times with linear backoff.
// This is a shared implementation to avoid duplication across target types.
func WithRetry(ctx context.Context, cfg RetryConfig, op func() error) error {
	var lastErr error

	for attempt := range cfg.MaxRetries {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return backup.NewError(backup.ErrCanceled, "operation canceled", ctx.Err())
		default:
		}

		if err := op(); err == nil {
			return nil
		} else if !IsTransientError(err) {
			return err
		} else {
			lastErr = err
			if cfg.Debug && cfg.DebugLog != nil {
				cfg.DebugLog("Retrying operation after error: %v (attempt %d/%d)", err, attempt+1, cfg.MaxRetries)
			}
		}

		// Linear backoff: backoff * (attempt + 1) gives 1x, 2x, 3x delays
		time.Sleep(cfg.Backoff * time.Duration(attempt+1))
	}

	return backup.NewError(backup.ErrIO, "operation failed after retries", lastErr)
}

// TempFileTracker tracks temporary files for cleanup.
// This is a shared implementation to avoid duplication across target types.
type TempFileTracker struct {
	mu    sync.Mutex
	files map[string]bool
}

// NewTempFileTracker creates a new temp file tracker
func NewTempFileTracker() *TempFileTracker {
	return &TempFileTracker{
		files: make(map[string]bool),
	}
}

// Track adds a file path to be tracked for cleanup
func (t *TempFileTracker) Track(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.files[path] = true
}

// Untrack removes a file path from tracking
func (t *TempFileTracker) Untrack(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.files, path)
}

// GetTracked returns a copy of all tracked file paths
func (t *TempFileTracker) GetTracked() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	paths := slices.Collect(maps.Keys(t.files))
	return paths
}

// Cleanup calls the provided delete function for each tracked file.
// Successfully deleted files are untracked.
func (t *TempFileTracker) Cleanup(deleteFunc func(string) error) []error {
	paths := t.GetTracked()
	var errs []error

	for _, path := range paths {
		if err := deleteFunc(path); err != nil {
			errs = append(errs, err)
		} else {
			t.Untrack(path)
		}
	}

	return errs
}

// PathValidator provides common path validation functionality.
// This is shared logic to prevent directory traversal and other security issues.
type PathValidator struct {
	AllowHidden bool // Whether to allow hidden files/directories (starting with .)
}

// InvalidPathChars contains characters that are not allowed in paths
const InvalidPathChars = "<>:\"\\|?*$()[]{}!&;#`"

// Validate checks a path for security issues including directory traversal,
// invalid characters, and length limits.
func (v *PathValidator) Validate(path string) error {
	if path == "" {
		return backup.NewError(backup.ErrValidation, "path cannot be empty", nil)
	}

	// Use filepath.IsLocal for comprehensive path validation (prevents CVE-2023-45284, CVE-2023-45283)
	if !filepath.IsLocal(path) {
		return backup.NewError(backup.ErrSecurity, "path is not local or contains traversal", nil)
	}

	// Check total path length
	if len(path) > MaxPathLength {
		return backup.NewError(backup.ErrValidation, "path exceeds maximum length", nil)
	}

	// Check each component
	for component := range strings.SplitSeq(path, "/") {
		if component == "" {
			continue
		}

		// Check for hidden files/directories
		if !v.AllowHidden && strings.HasPrefix(component, ".") {
			return backup.NewError(backup.ErrSecurity, "hidden files/directories are not allowed: "+component, nil)
		}

		// Check for invalid characters
		if strings.ContainsAny(component, InvalidPathChars) {
			return backup.NewError(backup.ErrValidation, "path contains invalid characters", nil)
		}

		// Check component length
		if len(component) > MaxComponentLength {
			return backup.NewError(backup.ErrValidation, "path component exceeds maximum length", nil)
		}
	}

	return nil
}

// SettingsParser provides type-safe extraction of configuration values from map[string]any.
// It collects errors during parsing and returns them all at once, reducing complexity
// in NewXXXTarget functions.
type SettingsParser struct {
	settings map[string]any
	errors   []string
}

// NewSettingsParser creates a new settings parser
func NewSettingsParser(settings map[string]any) *SettingsParser {
	return &SettingsParser{
		settings: settings,
		errors:   make([]string, 0),
	}
}

// RequireString extracts a required string value, recording an error if missing
func (p *SettingsParser) RequireString(key, component string) string {
	if val, ok := p.settings[key].(string); ok && val != "" {
		return val
	}
	p.errors = append(p.errors, component+": "+key+" is required")
	return ""
}

// OptionalString extracts an optional string value with a default
func (p *SettingsParser) OptionalString(key, defaultVal string) string {
	if val, ok := p.settings[key].(string); ok {
		return val
	}
	return defaultVal
}

// OptionalInt extracts an optional int value with a default
func (p *SettingsParser) OptionalInt(key string, defaultVal int) int {
	if val, ok := p.settings[key].(int); ok {
		return val
	}
	return defaultVal
}

// OptionalBool extracts an optional bool value with a default
func (p *SettingsParser) OptionalBool(key string, defaultVal bool) bool {
	if val, ok := p.settings[key].(bool); ok {
		return val
	}
	return defaultVal
}

// OptionalDuration extracts an optional duration from a string value with a default
func (p *SettingsParser) OptionalDuration(key string, defaultVal time.Duration, component string) time.Duration {
	if val, ok := p.settings[key].(string); ok {
		duration, err := time.ParseDuration(val)
		if err != nil {
			p.errors = append(p.errors, component+": invalid "+key+" format")
			return defaultVal
		}
		return duration
	}
	return defaultVal
}

// OptionalPath extracts an optional path, trimming trailing slashes
// If preserveRoot is true, "/" is preserved as-is
func (p *SettingsParser) OptionalPath(key string, preserveRoot bool) string {
	if val, ok := p.settings[key].(string); ok {
		if preserveRoot && val == "/" {
			return "/"
		}
		return strings.TrimRight(val, "/")
	}
	return ""
}

// RequirePath extracts a required path value, trimming trailing slashes
func (p *SettingsParser) RequirePath(key, component string, preserveRoot bool) string {
	if val, ok := p.settings[key].(string); ok && val != "" {
		if preserveRoot && val == "/" {
			return "/"
		}
		return strings.TrimRight(val, "/")
	}
	p.errors = append(p.errors, component+": "+key+" is required")
	return ""
}

// Error returns a combined error if any parsing errors occurred, nil otherwise
func (p *SettingsParser) Error() error {
	if len(p.errors) == 0 {
		return nil
	}
	return backup.NewError(backup.ErrConfig, strings.Join(p.errors, "; "), nil)
}

// HasErrors returns true if any parsing errors were recorded
func (p *SettingsParser) HasErrors() bool {
	return len(p.errors) > 0
}

// DefaultKnownHostsFile returns the default SSH known_hosts file path
func DefaultKnownHostsFile() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(homeDir, ".ssh", "known_hosts")
}

// PathValidationOpts configures path validation behavior
type PathValidationOpts struct {
	AllowHidden    bool   // Whether to allow hidden files/directories (starting with .)
	AllowAbsolute  bool   // Whether to allow absolute paths
	ConvertToSlash bool   // Convert path separators to forward slashes (for remote targets)
	CheckSymlinks  bool   // Check if path is a symlink (local filesystem only)
	InvalidChars   string // Additional invalid characters beyond the default set
	ReturnCleaned  bool   // Return the cleaned path instead of just validating
}

// ValidatePathWithOpts performs comprehensive path validation with configurable options.
// Returns the cleaned path (if ReturnCleaned is true) and any validation error.
func ValidatePathWithOpts(pathToCheck string, opts PathValidationOpts) (string, error) {
	if pathToCheck == "" {
		return "", backup.NewError(backup.ErrValidation, "path cannot be empty", nil)
	}

	// Clean the path
	clean := filepath.Clean(pathToCheck)

	// Handle Windows drive prefix if present
	if len(clean) >= 2 && clean[1] == ':' {
		clean = clean[2:]
	}

	// Convert to forward slashes if requested (for remote targets)
	if opts.ConvertToSlash {
		clean = filepath.ToSlash(clean)
	}

	// Check for absolute paths
	if !opts.AllowAbsolute {
		clean = strings.TrimPrefix(clean, "/")
		if filepath.IsAbs(clean) {
			return "", backup.NewError(backup.ErrValidation, "absolute paths are not allowed", nil)
		}
	}

	// Use filepath.IsLocal for comprehensive path validation (prevents CVE-2023-45284, CVE-2023-45283)
	if !filepath.IsLocal(clean) {
		return "", backup.NewError(backup.ErrSecurity, "path is not local or contains traversal", nil)
	}

	// Check total path length
	if len(clean) > MaxPathLength {
		return "", backup.NewError(backup.ErrValidation, "path exceeds maximum length", nil)
	}

	// Build invalid characters set
	invalidChars := InvalidPathChars
	if opts.InvalidChars != "" {
		invalidChars += opts.InvalidChars
	}

	// Check each component
	separator := string(filepath.Separator)
	if opts.ConvertToSlash {
		separator = "/"
	}
	for component := range strings.SplitSeq(clean, separator) {
		if component == "" {
			continue
		}

		// Check for hidden files/directories
		if !opts.AllowHidden && strings.HasPrefix(component, ".") {
			return "", backup.NewError(backup.ErrSecurity, "hidden files/directories are not allowed: "+component, nil)
		}

		// Check for invalid characters
		if strings.ContainsAny(component, invalidChars) {
			return "", backup.NewError(backup.ErrValidation, "path contains invalid characters", nil)
		}

		// Check component length
		if len(component) > MaxComponentLength {
			return "", backup.NewError(backup.ErrValidation, "path component exceeds maximum length", nil)
		}
	}

	if opts.ReturnCleaned {
		return clean, nil
	}
	return pathToCheck, nil
}

// TempFileResult contains the result of writing content to a temporary file
type TempFileResult struct {
	Path    string // Path to the temporary file
	Cleanup func() // Function to remove the temp file (safe to call multiple times)
}

// WriteTempFile writes content to a temporary file and returns the path and cleanup function.
// The caller is responsible for calling Cleanup() when done with the file.
// This is a shared implementation to avoid duplication in Store functions across targets.
func WriteTempFile(content []byte, prefix string) (*TempFileResult, error) {
	tempFile, err := os.CreateTemp("", prefix+"-*")
	if err != nil {
		return nil, backup.NewError(backup.ErrIO, "failed to create temporary file", err)
	}

	tempPath := tempFile.Name()
	cleanedUp := false
	cleanup := func() {
		if cleanedUp {
			return
		}
		cleanedUp = true
		_ = os.Remove(tempPath)
	}

	// Write content
	if _, err := tempFile.Write(content); err != nil {
		_ = tempFile.Close()
		cleanup()
		return nil, backup.NewError(backup.ErrIO, "failed to write to temporary file", err)
	}

	// Sync to ensure data is flushed
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		cleanup()
		return nil, backup.NewError(backup.ErrIO, "failed to sync temporary file", err)
	}

	// Close the file
	if err := tempFile.Close(); err != nil {
		cleanup()
		return nil, backup.NewError(backup.ErrIO, "failed to close temporary file", err)
	}

	return &TempFileResult{
		Path:    tempPath,
		Cleanup: cleanup,
	}, nil
}
